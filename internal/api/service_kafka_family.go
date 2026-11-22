package api

// service_kafka_family.go — v0.10.2450 (operatör 2026-11-22): Service →
// Messaging sekmesi önce Kafka istemci METRİKLERİNİ (VictoriaMetrics,
// örneklenmemiş), sonra span'leri okur — RED'in metrik-önce ilkesi
// (red_family.go, v0.10.2435) messaging'e.
//
//	GET /api/services/{name}/kafka-family?to=            (metricsrc=vm)
//	GET /api/services/{name}/messaging-spans?from=&to=   (span yedeği)
//
// Aile kataloğu SAF ve sıralı; VM'deki Prometheus yazımları:
//
//	otel_java        OTel Java agent kafka-clients köprüsü, *_rate göstergeleri
//	                 (operatörün "OTel Java - Kafka Client Metrics" panosu)
//	otel_java_total  aynı köprü, yalnız kümülatif *_total (rate() gerekir)
//	micrometer       Micrometer KafkaClientMetrics (fetch_manager ön ekli
//	                 tüketici adları, *_total sayaçlar)
//
// Çözücü: aile başına TEK anlık yoklama — üretici ve tüketici adı ayrı
// etiketlenir (label_replace … side="p"|"c"), ≥1 seri = o yön VAR. En çok
// yönü bulunan aile kazanır, eşitlikte katalog sırası (otel_java ve
// otel_java_total aynı üretici adını paylaşmaz; micrometer ile
// otel_java_total paylaşır — tüketici adı ayırır). Hiçbiri yoksa state
// "spans": FE span yedeğine (messaging_caller_summary_{1m,5m}) düşer.
// Yoklama hatası → state "unknown" (okunamadı ≠ yok); FE bugünkü panoyu
// çizer, span yedeğine SESSİZCE geçmez.
//
// Sınırlar: servis süzgeci kesin (service_name), yoklama [1h] pencereli,
// sonuç 60 sn önbellekte (anahtar servis + 10 dk'lık `to` kovası). Span
// yedeği serveCached 30 sn, anahtar servis + pencere + grenlik.

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

func init() { registerRoutesExtra("service-kafka-family", (*Server).registerServiceKafkaFamilyRoutes) }

func (s *Server) registerServiceKafkaFamilyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/kafka-family", s.getServiceKafkaFamily)
	mux.HandleFunc("GET /api/services/{name}/messaging-spans", s.getServiceMessagingSpans)
}

// kafkaFamily — bir Kafka istemci metrik ailesinin SAF tanımı (VM adları).
type kafkaFamily struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Counter bool   `json:"counter"` // true: Produce/Consume/Errors kümülatif → rate()
	// Produce/Consume/Errors — kayıt hızı (Counter=false: kayıt/sn göstergesi).
	Produce string `json:"produce"`
	Consume string `json:"consume"`
	Errors  string `json:"errors"`
	// Gecikmeler ms (Kafka istemcisinin beyanı); Lag kayıt sayısı.
	ProdLatency string `json:"prodLatency"`
	ConsLatency string `json:"consLatency"`
	Lag         string `json:"lag"`
}

// kafkaFamilies — sıralı katalog (PİNLİ: TestKafkaFamiliesPinned; FE
// lib/kafkaTopics.ts aynı adları KafkaFamily olarak alır, kopyalamaz).
var kafkaFamilies = []kafkaFamily{
	{ID: "otel_java", Label: "OTel Java", Counter: false,
		Produce: "kafka_producer_record_send_rate", Consume: "kafka_consumer_records_consumed_rate",
		Errors:      "kafka_producer_record_error_rate",
		ProdLatency: "kafka_producer_request_latency_avg", ConsLatency: "kafka_consumer_fetch_latency_avg",
		Lag: "kafka_consumer_records_lag_max"},
	{ID: "otel_java_total", Label: "OTel Java (total)", Counter: true,
		Produce: "kafka_producer_record_send_total", Consume: "kafka_consumer_records_consumed_total",
		Errors:      "kafka_producer_record_error_total",
		ProdLatency: "kafka_producer_request_latency_avg", ConsLatency: "kafka_consumer_fetch_latency_avg",
		Lag: "kafka_consumer_records_lag_max"},
	{ID: "micrometer", Label: "Micrometer", Counter: true,
		Produce: "kafka_producer_record_send_total", Consume: "kafka_consumer_fetch_manager_records_consumed_total",
		Errors:      "kafka_producer_record_error_total",
		ProdLatency: "kafka_producer_request_latency_avg", ConsLatency: "kafka_consumer_fetch_manager_fetch_latency_avg",
		Lag: "kafka_consumer_fetch_manager_records_lag_max"},
}

// Kaynak durumları.
const (
	kafkaSrcMetric  = "metric"
	kafkaSrcSpans   = "spans"
	kafkaSrcUnknown = "unknown"
)

// kafkaFamilyResolution — GET …/kafka-family cevabı.
type kafkaFamilyResolution struct {
	State   string       `json:"state"` // metric | spans | unknown
	Family  *kafkaFamily `json:"family,omitempty"`
	Produce bool         `json:"produce"` // seçilen ailede üretici serisi var
	Consume bool         `json:"consume"`
	Tried   []string     `json:"tried"`
	Reason  string       `json:"reason,omitempty"`
}

// kafkaPromStr — SAF: PromQL dize değişmezi (çift tırnak, kaçışlı).
func kafkaPromStr(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v) + `"`
}

// kafkaFamilyProbeQuery — SAF: tek anlık yoklama; side="p" üretici, "c" tüketici.
func kafkaFamilyProbeQuery(f kafkaFamily, service string) string {
	sel := "{service_name=" + kafkaPromStr(service) + "}"
	one := func(m, side string) string {
		return `label_replace(count(last_over_time(` + m + sel + `[1h])), "side", "` + side + `", "", "")`
	}
	return one(f.Produce, "p") + " or " + one(f.Consume, "c")
}

// kafkaSides — SAF: yoklama serilerinden yönler (değer > 0).
func kafkaSides(series []chstore.SpanMetricSeries) (produce, consume bool) {
	for _, s := range series {
		if !kafkaSeriesNonZero(s) {
			continue
		}
		for _, kv := range s.GroupKey { // vmetrics labelSetGroupKey: `side="p"`
			switch kv {
			case `side="p"`:
				produce = true
			case `side="c"`:
				consume = true
			}
		}
	}
	return
}

func kafkaSeriesNonZero(s chstore.SpanMetricSeries) bool {
	for _, p := range s.Points {
		if p.Value > 0 {
			return true
		}
	}
	return false
}

// kafkaPick — SAF: en çok yönü olan aile (eşitlikte katalog sırası); hiçbiri
// yoksa -1. hits[i] = ailenin (produce, consume) yönleri.
func kafkaPick(hits [][2]bool) int {
	best, bestN := -1, 0
	for i, h := range hits {
		n := 0
		for _, b := range h {
			if b {
				n++
			}
		}
		if n > bestN {
			best, bestN = i, n
		}
	}
	return best
}

// kafkaInstantReader — çözücünün VM bağımlılığı (test sahtesi takılır).
type kafkaInstantReader interface {
	QueryPromQLInstant(ctx context.Context, query string, at time.Time) ([]chstore.SpanMetricSeries, error)
}

// resolveServiceKafkaFamily — katalog sırasıyla yoklar. Herhangi bir yoklama
// hatası → unknown (kısmi sonuçla "yok" denmez).
func resolveServiceKafkaFamily(ctx context.Context, rd kafkaInstantReader, service string, at time.Time) (kafkaFamilyResolution, error) {
	res := kafkaFamilyResolution{State: kafkaSrcSpans, Tried: []string{}}
	hits := make([][2]bool, len(kafkaFamilies))
	for i, f := range kafkaFamilies {
		ser, err := rd.QueryPromQLInstant(ctx, kafkaFamilyProbeQuery(f, service), at)
		if err != nil {
			return kafkaFamilyResolution{State: kafkaSrcUnknown, Tried: res.Tried}, err
		}
		p, c := kafkaSides(ser)
		hits[i] = [2]bool{p, c}
		res.Tried = append(res.Tried, fmt.Sprintf("%s: %s=%v %s=%v", f.ID, f.Produce, p, f.Consume, c))
	}
	if i := kafkaPick(hits); i >= 0 {
		f := kafkaFamilies[i]
		res.State, res.Family, res.Produce, res.Consume = kafkaSrcMetric, &f, hits[i][0], hits[i][1]
	}
	return res, nil
}

// kafkaFamilyKey — önbellek anahtarı: servis (hash) + 10 dk'lık `to` kovası.
func kafkaFamilyKey(service string, to time.Time) string {
	h := fnv.New64a()
	h.Write([]byte(service))
	return fmt.Sprintf("svc-kafka-family:v1:%x:%d", h.Sum64(), to.Truncate(10*time.Minute).Unix())
}

// getServiceKafkaFamily — GET /api/services/{name}/kafka-family
func (s *Server) getServiceKafkaFamily(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" || len(name) > 512 {
		writeErr(w, fmt.Errorf("%w: service required", errBadRequest))
		return
	}
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	vm, ok := src.(vmMetricSource)
	if !ok {
		// CH kaynağında Kafka istemci metriği sorulmaz: span yedeği.
		writeJSON(w, kafkaFamilyResolution{State: kafkaSrcSpans, Tried: []string{}, Reason: "metricsrc≠vm"})
		return
	}
	now := time.Now()
	to := parseTime(r.URL.Query().Get("to"))
	if to.IsZero() || to.After(now) {
		to = now
	}
	s.serveCached(w, r, kafkaFamilyKey(name, to), 60*time.Second, func(ctx context.Context) (any, error) {
		res, rerr := resolveServiceKafkaFamily(ctx, vm, name, to)
		if rerr != nil {
			if errors.Is(rerr, context.Canceled) {
				return nil, rerr
			}
			res.Reason = "VictoriaMetrics okuması BAŞARISIZ (okunamadı ≠ yok): " + string(sourcestate.Classify(rerr))
		}
		return res, nil
	})
}

// serviceMsgSpansKey — servis + pencere + grenlik (kapsama değişince 5m yükü
// 1m anahtarında servis edilmesin).
func serviceMsgSpansKey(service string, bucketSec int, bucket string) string {
	h := fnv.New64a()
	h.Write([]byte(service))
	return fmt.Sprintf("svc-msg-spans:v1:%x:g=%d:%s", h.Sum64(), bucketSec, bucket)
}

// getServiceMessagingSpans — GET /api/services/{name}/messaging-spans
func (s *Server) getServiceMessagingSpans(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" || len(name) > 512 {
		writeErr(w, fmt.Errorf("%w: service required", errBadRequest))
		return
	}
	from, to := parseFromTo(r, time.Hour)
	g := s.store.ServiceMessagingGrain(r.Context(), from, to)
	s.serveCached(w, r, serviceMsgSpansKey(name, g.BucketSec, cacheBucket(from, to)), 30*time.Second, func(ctx context.Context) (any, error) {
		return s.store.GetServiceMessagingSpans(ctx, g, name, from, to)
	})
}
