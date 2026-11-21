package runtimesig

import (
	"fmt"
	"math"
	"sort"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// common.go — runtime'dan bağımsız süreç sinyalleri (2026-11-21, prod BFF
// gözlemi: Java agent + Micrometer aynı serviste). Her profile, runtime'ın
// kendi adaylarından SONRA eklenir; aile başına İLK bulunan seçilir (OTel
// semconv önce, Micrometer yedek) — iki aile aynı sinyal için çizilmez.

// commonMappings — süreç düzeyi adaylar.
var commonMappings = map[SignalKey][]Source{
	SigFDOpen: {
		src(KindGauge, 1, "", "process.open_file_descriptor.count", "process_open_file_descriptor_count"),
		src(KindGauge, 1, "", "process_files_open_files"),
		src(KindGauge, 1, "", "process_files_open"),
	},
	SigFDMax: {
		src(KindGauge, 1, "", "process_files_max_files"),
		src(KindGauge, 1, "", "process_files_max"),
	},
	// Apache HttpClient 5 havuzu (Micrometer PoolingHttpClientConnectionManager
	// bağlayıcısı): pending, total_connections{state=leased|available}, total_max.
	SigHTTPPending: {src(KindGauge, 1, "", "httpcomponents_httpclient_pool_total_pending")},
	SigHTTPLeased:  {src(KindGauge, 1, "", "httpcomponents_httpclient_pool_total_connections").withFilter("state", "leased")},
	SigHTTPMax:     {src(KindGauge, 1, "", "httpcomponents_httpclient_pool_total_max")},
	// Kafka Java istemcisi (OTel kafka-clients metrics köprüsü; producer
	// buffer-memory baskısı + consumer gecikmesi).
	SigKafkaWaiting:      {src(KindGauge, 1, "", "kafka.producer.waiting_threads", "kafka_producer_waiting_threads")},
	SigKafkaInFlight:     {src(KindGauge, 1, "", "kafka.producer.requests_in_flight", "kafka_producer_requests_in_flight")},
	SigKafkaBufExhausted: {src(KindCounter, 1, "", "kafka.producer.buffer_exhausted_total", "kafka_producer_buffer_exhausted_total")},
	SigKafkaLag:          {withAgg(src(KindGauge, 1, "", "kafka.consumer.records_lag_max", "kafka_consumer_records_lag_max"), "max")},
}

// Kafka istemci sinyal anahtarları.
const (
	SigKafkaWaiting      SignalKey = "kafka_waiting_threads" // buffer belleği bekleyen thread
	SigKafkaInFlight     SignalKey = "kafka_requests_in_flight"
	SigKafkaBufExhausted SignalKey = "kafka_buffer_exhausted" // /sn
	SigKafkaLag          SignalKey = "kafka_records_lag_max"  // kayıt (topic/partition max)
)

func withAgg(s Source, agg string) Source { s.Agg = agg; return s }

// commonNames — ortak adayların tüm yazımları (katalog süzgeci IsRuntimeMetric).
var commonNames = func() map[string]bool {
	m := map[string]bool{}
	for _, srcs := range commonMappings {
		for _, s := range srcs {
			for _, n := range s.Names {
				m[n] = true
			}
		}
	}
	return m
}()

func isCommonName(name string) bool { return commonNames[name] }

// Kafka kontrolleri.
const (
	CheckKafkaBackpressure = "kafka-producer-backpressure"
	CheckKafkaLag          = "kafka-consumer-lag"
	kafkaLagMin            = 1000.0
)

// Ortak kontroller.
const (
	CheckFDSaturation   = "fd-saturation"
	CheckHTTPPoolSaturn = "http-pool-saturation"
)

// Eşikler (sabit; tanıtıcı / havuz doluluğu mutlak oran — taban gerekmez).
const (
	fdWarnPct       = 80.0
	fdCritPct       = 95.0
	httpPoolWarnPct = 90.0
	httpPendingWarn = 1.0  // sürdürülmüş bekleyen istek (son 10 dk ort.)
	httpPendingCrit = 10.0 // kritik bekleyen
)

// PodEntityIndex — SAF: servisin varlık-görülen pod'ları → pod adı başına
// entity kaydı (entity.PodID biçimi `pod:<cid>/<ns>/<pod>`). Cluster'ı Remote
// Cluster id'sine eşlenemeyen pod ya da birden çok kayda düşen ad (belirsiz)
// dizine girmez.
func PodEntityIndex(aggs []chstore.EntitySeenAgg, cidOf map[string]string) map[string]PodEntityRef {
	out := map[string]PodEntityRef{}
	dup := map[string]bool{}
	for _, a := range aggs {
		cid := cidOf[a.Cluster]
		if a.Pod == "" || cid == "" {
			continue
		}
		r := PodEntityRef{ID: "pod:" + cid + "/" + a.Namespace + "/" + a.Pod, ClusterID: cid, Namespace: a.Namespace, Pod: a.Pod}
		if prev, ok := out[a.Pod]; ok && prev.ID != r.ID {
			dup[a.Pod] = true
			continue
		}
		out[a.Pod] = r
	}
	for p := range dup {
		delete(out, p)
	}
	return out
}

// CapPodIndex — SAF: dizini ada göre sıralı ilk n pod'la sınırlar (yanıt
// boyutu; deterministik). Boş dizin → nil.
func CapPodIndex(in map[string]PodEntityRef, n int) map[string]PodEntityRef {
	if len(in) == 0 {
		return nil
	}
	if len(in) <= n {
		return in
	}
	names := make([]string, 0, len(in))
	for k := range in {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make(map[string]PodEntityRef, n)
	for _, k := range names[:n] {
		out[k] = in[k]
	}
	return out
}

// commonDetect — her runtime'da okunan ortak dedektör sinyalleri.
var commonDetect = []SignalKey{SigFDOpen, SigFDMax, SigHTTPPending, SigHTTPLeased, SigHTTPMax,
	SigKafkaWaiting, SigKafkaInFlight, SigKafkaBufExhausted, SigKafkaLag}

// detectSignalsFor — runtime'ın dedektör girdileri. .NET tam küme; diğerleri
// GC payı + ortak süreç sinyalleri (JVM ham heap testere dişidir, sızıntı
// eğilimi orada heap bandının işi — HeapBaselineCard / heap_scan).
func detectSignalsFor(rt Runtime) []SignalKey {
	if rt == RuntimeDotnet {
		return append(append([]SignalKey{}, detectSignals...), commonDetect...)
	}
	return append([]SignalKey{SigGCTime}, commonDetect...)
}

// hasDetectSignal — profilde en az bir dedektör girdisi çözüldü mü.
func hasDetectSignal(p Profile) bool {
	for _, k := range detectSignalsFor(p.Runtime) {
		if p.Has(k) {
			return true
		}
	}
	return false
}

// evaluateCommon — SAF: tanıtıcı doygunluğu + HttpClient havuz doygunluğu.
func evaluateCommon(d PodData, now int64, mk func(check, level string, value, base, thr float64, unit, summary string) Signal, where string) []Signal {
	var out []Signal
	if open, max := d.Series[SigFDOpen], d.Series[SigFDMax]; len(open) > 0 && len(max) > 0 {
		o, ob, nr, _ := splitWindow(open, now)
		m, _, nm, _ := splitWindow(max, now)
		if nr >= minRecentPoints && nm > 0 && m > 0 {
			pct := o / m * 100
			if pct >= fdWarnPct {
				level, thr := LevelWarning, fdWarnPct
				if pct >= fdCritPct {
					level, thr = LevelCritical, fdCritPct
				}
				s := mk(CheckFDSaturation, level, pct, ob/m*100, thr, "%",
					fmt.Sprintf("Dosya tanıtıcısı doygunluğu: %s %.0f / %.0f açık (%%%.0f, eşik %%%.0f)", where, o, m, pct, thr))
				s.Rising = ob > 0 && o > ob
				out = append(out, s)
			}
		}
	}
	if pend := d.Series[SigHTTPPending]; len(pend) > 0 {
		p, pb, nr, _ := splitWindow(pend, now)
		util := -1.0
		if l, mx := d.Series[SigHTTPLeased], d.Series[SigHTTPMax]; len(l) > 0 && len(mx) > 0 {
			lv, _, _, _ := splitWindow(l, now)
			mv, _, _, _ := splitWindow(mx, now)
			if mv > 0 {
				util = lv / mv * 100
			}
		}
		if nr >= minRecentPoints && p >= httpPendingWarn && (util < 0 || util >= httpPoolWarnPct) {
			level := LevelWarning
			if p >= httpPendingCrit {
				level = LevelCritical
			}
			use := "havuz doluluğu bilinmiyor"
			if util >= 0 {
				use = fmt.Sprintf("havuz %%%.0f dolu", util)
			}
			s := mk(CheckHTTPPoolSaturn, level, p, pb, httpPendingWarn, "items",
				fmt.Sprintf("Giden HTTP havuzu doygun: %s ortalama %.1f istek bağlantı bekliyor (önceki 50 dk %.1f), %s", where, p, pb, use))
			s.Rising = p > pb
			if util >= 0 {
				s.Extra = map[string]float64{"poolUtilPct": math.Round(util)}
			}
			out = append(out, s)
		}
	}
	// Kafka producer geri basıncı: buffer.memory dolu → send() bloklanır
	// (waiting_threads > 0), tükenme sayacı artıyorsa kayıt düşüyor/zaman aşımı.
	if wt := d.Series[SigKafkaWaiting]; len(wt) > 0 || len(d.Series[SigKafkaBufExhausted]) > 0 {
		w, wb, nr, _ := splitWindow(wt, now)
		ex, _, _, _ := splitWindow(d.Series[SigKafkaBufExhausted], now)
		if (nr >= minRecentPoints && w >= 1) || ex > 0 {
			level := LevelWarning
			if ex > 0 {
				level = LevelCritical
			}
			inflight, _, _, _ := splitWindow(d.Series[SigKafkaInFlight], now)
			s := mk(CheckKafkaBackpressure, level, w, wb, 1, "items",
				fmt.Sprintf("Kafka producer geri basıncı: %s ortalama %.1f thread buffer belleği bekliyor (önceki 50 dk %.1f), uçuştaki istek %.1f, buffer tükenmesi %.2f/sn", where, w, wb, inflight, ex))
			s.Rising = w > wb
			out = append(out, s)
		}
	}
	// Kafka consumer gecikmesi: lag tabanın SpikeRatio katı ve mutlak taban üstü.
	if lag := d.Series[SigKafkaLag]; len(lag) > 0 {
		l, lb, nr, _ := splitWindow(lag, now)
		if nr >= minRecentPoints && l >= kafkaLagMin && l >= 3*math.Max(lb, kafkaLagMin/10) {
			s := mk(CheckKafkaLag, LevelWarning, l, lb, kafkaLagMin, "items",
				fmt.Sprintf("Kafka consumer gecikmesi: %s en büyük partition lag'i %.0f kayıt (önceki 50 dk %.0f)", where, l, lb))
			s.Rising = recentSlope(lag, now) > 0
			out = append(out, s)
		}
	}
	// HikariCP havuz doygunluğu (follow-up 2026-11-21; pod kapsamlı okuma).
	out = append(out, evaluateHikari(d, now, mk, where)...)
	return out
}
