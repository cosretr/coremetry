package api

// red_family.go — v0.10.1429 (operatör, prod: Service · coremetry-ingest →
// Overview metrik kipi "Bu filtrede veri yok — http_server_request_duration_
// seconds"; "gRPC metriklerini de HTTP Metrics altına ekleyebiliriz bence").
//
// Servis sayfasının metrik kipi (Overview karoları, HTTP Metrics sekmesi,
// /endpoints listesi, endpoint detayı) ve CoSRE servis sağlığı / raporu
// bugüne dek TEK aileyi okuyordu: OTel HTTP sunucu histogramı
// (http_server_request_duration_seconds_*). Yalnız OTLP/gRPC sunan bir servis
// (coremetry-ingest, OTel-agent gRPC servisleri) VM'e rpc.server.duration
// yazıyor ve metrik kipi boş kalıyordu.
//
// Bu dosya SAF aile kataloğu + servis başına aile ÇÖZÜCÜ:
//
//   http         http.server.request.duration (s)  — bugünkü aile, bayt bayt
//   grpc_legacy  rpc.server.duration (ms)          — rpc_server_duration_milliseconds_*,
//                etiketler rpc_system / rpc_service / rpc_method / rpc_grpc_status_code
//                (instrumentasyonların VARSAYILANI; semconv eski)
//   grpc_stable  rpc.server.call.duration (s)      — rpc_server_call_duration_seconds_*,
//                rpc_response_status_code (opt-in otel.semconv-stability.opt-in=rpc)
//
// KURALLAR (operatör onaylı):
//   - İki gRPC ailesi (ms ↔ s) ASLA tek sorguda birleşmez: VM adı aile
//     başına TAM (unit sonekli taban), __name__ alternasyonu yok. rpc/dup
//     kurulumunda legacy birincil, stable çizilmez (tried listesinde söylenir).
//   - HTTP + gRPC ASLA toplanmaz: HTTP birincil; ikisi de varsa sekmede iki
//     ayrı bölüm (önce HTTP, altta gRPC), Overview HTTP + "gRPC de var" notu.
//   - Hata kümesi gRPC'de SUNUCU-HATASI kodları (grpc-gateway kod→HTTP
//     eşlemesinin 5xx karşılığı): 2 UNKNOWN, 4 DEADLINE_EXCEEDED,
//     8 RESOURCE_EXHAUSTED, 12 UNIMPLEMENTED, 13 INTERNAL, 14 UNAVAILABLE,
//     15 DATA_LOSS. "Sıfır olmayan = hata" DEĞİL: 5 NOT_FOUND / 7
//     PERMISSION_DENIED / 16 UNAUTHENTICATED istemci hatasıdır (HTTP 4xx
//     karşılığı) ve 4xx gibi ayrı oranda gösterilir. Kümeler PİNLİ
//     (TestREDFamilyErrorSetsPinned); lib/redFamily.ts aynı kümeleri taşır
//     (TestREDFamilyLabelsMatchFrontend).
//   - Gecikme ÖLÇEĞİ ailenin BEYAN ETTİĞİ birimden (ms → /1000), ad sonekinden
//     tahmin değil; SLI kovası legacy'de le="500", stable'da le="0.5".
//   - Okuma hatası → state unknown ("okunamadı ≠ yok"); yokluk iddiası
//     yalnız known'da.
//
// Çözücü (resolveServiceREDFamily): aile sırasıyla etiket kümesi
// (metricChartLabelSet, 5 dk önbellek, son 24 sa) → servis etiketi
// (LabelMap / service_name) → TEK anlık yoklama sum(increase(<count>{svc}[1h]))
// → ≥1 seri = aile VAR. Ortam terimleri varsa aile başına
// resolveMetricEnvScope (o ailenin metriğine karşı). Sonuç 60 sn önbellekte,
// anahtar TÜM girdiler. Tüketiciler: GET /api/services/{name}/red-family
// (service_red_family_route.go) ve CoSRE servis sağlığı + raporu
// (copilot_service_http_red.go readServiceHTTPRED — tek kurucu).

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// Aile kimlikleri ve türleri.
const (
	redFamilyHTTP       = "http"
	redFamilyGRPCLegacy = "grpc_legacy"
	redFamilyGRPCStable = "grpc_stable"

	redKindHTTP = "http"
	redKindGRPC = "grpc"
	redKindNone = "none"
)

// Hata kümeleri (regex gövdeleri; `status=~"<gövde>"`). PİNLİ.
const (
	httpServerFaultMatch = "5.."
	httpClientFaultMatch = "4.."
	grpcServerFaultMatch = "2|4|8|12|13|14|15"
	grpcClientFaultMatch = "1|3|5|6|7|9|10|11|16"
)

// redFamilyTTL — çözüm önbelleği.
const redFamilyTTL = 60 * time.Second

// redFamily — bir RED metrik ailesinin SAF tanımı.
type redFamily struct {
	ID    string
	Kind  string // http | grpc
	Label string // "HTTP" | "gRPC" (başlık / rozet)
	// Probe — OTel adı: etiket keşfi (metricChartLabelSet) ve ClickHouse
	// metric_points adı. HTTP'de mcHTTPMetric (bayt parite).
	Probe string
	// VMBase — VM'de TAM taban (unit sonekli; OTel collector'ın Prometheus
	// yazımı, lib/redFamily.ts ile pinli). Exact=false (HTTP): seçici
	// bugünkü gibi vmetrics.NameCandidates(Probe) alternasyonu (bayt bayt);
	// Exact=true (gRPC): seçici YALNIZ VMBase — ms ↔ s ailesi tek
	// alternasyonda toplanmasın.
	VMBase string
	Exact  bool
	Unit   string // "s" | "ms" — gecikme ölçeği buradan
	// Route/Method eşdeğerleri: HTTP http_route / http_request_method;
	// gRPC rpc_method / rpc_service ("Service / Method" sütunları).
	RouteLabel, MethodLabel string   // VM yazımı
	RouteKey, MethodKey     string   // OTel anahtarı (CH groupBy)
	StatusLabels            []string // VM yazımı, tercih sırası
	StatusKeys              []string // OTel anahtarı (probeStatusLabel adayları)
	ServerErr, ClientErr    string   // regex gövdeleri
}

// redFamilies — sıra = çözüm sırası (HTTP birincil; legacy stable'dan önce).
var redFamilies = []redFamily{
	{ID: redFamilyHTTP, Kind: redKindHTTP, Label: "HTTP", Probe: mcHTTPMetric, VMBase: "http_server_request_duration_seconds", Unit: "s",
		RouteLabel: "http_route", MethodLabel: "http_request_method", RouteKey: "http.route", MethodKey: "http.request.method",
		StatusLabels: mcStatusLabels, StatusKeys: httpStatusKeyCandidates,
		ServerErr: httpServerFaultMatch, ClientErr: httpClientFaultMatch},
	{ID: redFamilyGRPCLegacy, Kind: redKindGRPC, Label: "gRPC", Probe: "rpc.server.duration", VMBase: "rpc_server_duration_milliseconds", Exact: true, Unit: "ms",
		RouteLabel: "rpc_method", MethodLabel: "rpc_service", RouteKey: "rpc.method", MethodKey: "rpc.service",
		StatusLabels: []string{"rpc_grpc_status_code"}, StatusKeys: []string{"rpc.grpc.status_code"},
		ServerErr: grpcServerFaultMatch, ClientErr: grpcClientFaultMatch},
	{ID: redFamilyGRPCStable, Kind: redKindGRPC, Label: "gRPC", Probe: "rpc.server.call.duration", VMBase: "rpc_server_call_duration_seconds", Exact: true, Unit: "s",
		RouteLabel: "rpc_method", MethodLabel: "rpc_service", RouteKey: "rpc.method", MethodKey: "rpc.service",
		StatusLabels: []string{"rpc_response_status_code"}, StatusKeys: []string{"rpc.response.status_code"},
		ServerErr: grpcServerFaultMatch, ClientErr: grpcClientFaultMatch},
}

// redFamilyByID — katalog araması; bilinmeyen → HTTP, ok=false.
func redFamilyByID(id string) (redFamily, bool) {
	for _, f := range redFamilies {
		if f.ID == id {
			return f, true
		}
	}
	return redFamilies[0], false
}

// grpcFamilies — SAF: yalnız gRPC aileleri (legacy önce).
func grpcFamilies() []redFamily {
	var out []redFamily
	for _, f := range redFamilies {
		if f.Kind == redKindGRPC {
			out = append(out, f)
		}
	}
	return out
}

// names — şablon varyantının Names girdisi: HTTP'de OTel adı (bugünkü
// seçici alternasyonu), gRPC'de tam VM tabanı (tek yazım).
func (f redFamily) names() string {
	if f.Exact {
		return f.VMBase
	}
	return f.Probe
}

// vmCount — ailenin VM `_count` yazımı (tek; FE lib/redFamily.ts pini).
func (f redFamily) vmCount() string {
	return vmetrics.NameCandidates("count", f.VMBase)[0]
}

// metricName — metricSource okumaları için ad: VM'de tam taban (Exact), CH'de OTel adı.
func (f redFamily) metricName(src metricSource) string {
	if _, ok := src.(vmMetricSource); ok && f.Exact {
		return f.VMBase
	}
	return f.Probe
}

var grpcServerFaultRe = regexp.MustCompile(`^(` + grpcServerFaultMatch + `)$`)

// IsServerError — SAF: durum değeri sunucu hatası mı. HTTP: tam üç haneli
// 5xx (isHTTP5xx, 4xx DEĞİL); gRPC: pinli sunucu-hatası kümesi.
func (f redFamily) IsServerError(v string) bool {
	if f.Kind == redKindGRPC {
		return grpcServerFaultRe.MatchString(strings.TrimSpace(v))
	}
	return isHTTP5xx(v)
}

// errorNoteTR — SAF: notlardaki hata tanımı cümlesi.
func (f redFamily) errorNoteTR() string {
	if f.Kind == redKindGRPC {
		return "sunucu-hatası kodları " + grpcServerFaultMatch + " (UNKNOWN/DEADLINE_EXCEEDED/RESOURCE_EXHAUSTED/UNIMPLEMENTED/INTERNAL/UNAVAILABLE/DATA_LOSS; istemci kodları hata sayılmaz)"
	}
	return "5xx (4xx hata sayılmaz)"
}

// redFamilyPresence — çözümde VAR olan bir aile.
type redFamilyPresence struct {
	ID          string        `json:"id"`
	Kind        string        `json:"kind"`
	StatusLabel string        `json:"statusLabel,omitempty"` // kümede bulunan durum etiketi (VM yazımı)
	EnvScope    *httpEnvScope `json:"envScope,omitempty"`    // ortam terimi varsa: bu ailenin metriğine karşı çözüm
}

// redFamilyResolution — servis başına çözüm. State unknown ⇒ yokluk iddiası
// YOK (okunamadı ≠ yok); Primary o hâlde "http" (bugünkü sorgular koşar).
type redFamilyResolution struct {
	Service string             `json:"service"`
	State   string             `json:"state"` // known | unknown
	Reason  string             `json:"reason,omitempty"`
	Primary string             `json:"primary"` // http | grpc | none
	HTTP    *redFamilyPresence `json:"http,omitempty"`
	GRPC    *redFamilyPresence `json:"grpc,omitempty"`
	Both    bool               `json:"both,omitempty"`
	Tried   []string           `json:"tried"`
}

// redFamilyUnknown — SAF: okuma hatası → unknown çözüm (önbelleğe YAZILMAZ).
func redFamilyUnknown(service, what string, err error) redFamilyResolution {
	return redFamilyResolution{Service: service, State: "unknown", Primary: redKindHTTP, Tried: []string{},
		Reason: "VictoriaMetrics " + what + " BAŞARISIZ (okunamadı ≠ yok): " + err.Error()}
}

// redFamilyReader — çözücünün VM'den okudukları: metric_chart okuyucusu +
// anlık sorgu (vmMetricSource karşılar; testler sahte okuyucu verir).
type redFamilyReader interface {
	metricChartReader
	redFamilyLabelReader
	QueryPromQLInstant(ctx context.Context, query string, at time.Time) ([]chstore.SpanMetricSeries, error)
}

// redFamilyPresenceQuery — SAF: TEK anlık yoklama; ≥1 seri = servis bu
// aileyi bu saatte yazmış. Seçici metricChartSelector'dan (aynı yazım kuralı).
func redFamilyPresenceQuery(f redFamily, svcLabel, service string) string {
	v := metricChartVariant{Probe: f.Probe, Names: []string{f.names()}, Kind: "count"}
	return "sum(increase(" + metricChartSelector(metricChartQueryInput{Variant: v, Service: service, SvcLabel: svcLabel}, "") + "[1h]))"
}

// redFamilyKey — SAF: çözüm önbelleği anahtarı; TÜM girdiler (kaynak,
// servis, env, cluster, LabelMap'in servis/env/cluster etiketleri, 10 dk'ya
// yuvarlanmış uç) FNV ile — uzunluk-yalnız bileşen yok (v0.5.187).
func redFamilyKey(lm vmetrics.LabelMap, service, env, cluster string, to time.Time, src string) string {
	return "red-family:v1:" + fnvStr(src, service, env, cluster, lm.Service, lm.Env, lm.Cluster,
		fmt.Sprint(to.Truncate(httpEnvValuesSnap).Unix()))
}

// redFamilySvcLabel — SAF: servise kapsanmış etiket keşfinde kullanılacak servis
// etiketi: etiket haritasındaki (normalize) ad, yoksa VM'in varsayılanı.
func redFamilySvcLabel(lm vmetrics.LabelMap) string {
	if l := strings.TrimSpace(lm.Service); l != "" {
		return l
	}
	return "service_name"
}

// redFamilyLabelReader — çözücünün servise kapsanmış etiket keşfi (vmMetricSource).
type redFamilyLabelReader interface {
	MetricLabelNamesForServiceIn(ctx context.Context, metric, svcLabel, service string, from, to time.Time) ([]string, bool, error)
}

// redFamilyLabelSet — v0.10.1574 (prod, bsa-crm-contentmanagement-prod:
// /red-family → "http_server_request_duration_seconds_count: okunamadı
// (timeout)", gRPC aynı okumayla geldi). metricChartLabelSet filonun TAMAMININ
// histogramını 24 sa tarıyordu ({__name__=~…}, 2 sn bütçe); HTTP'nin seri
// sayısında bütçe yetmiyordu. Burada aynı pencere ve bütçe, ama
// {__name__=~…, <servis etiketi>="<servis>"} — tek servisin serileri.
// Önbellek anahtarı servisi ve etiketi taşır (tüm girdiler).
func (s *Server) redFamilyLabelSet(ctx context.Context, rd redFamilyReader, metric, svcLabel, service string, to time.Time) (map[string]bool, error) {
	key := "red-family-labels:v1:" + fnvStr(metric, svcLabel, service,
		fmt.Sprint(to.Truncate(httpEnvValuesSnap).Unix()), fmt.Sprint(int64(metricChartLabelsLookbk/time.Second)))
	var names []string
	if !s.metricChartCacheGet(ctx, key, &names) {
		lctx, cancel := context.WithTimeout(ctx, metricChartLabelTimeout)
		end := to.Truncate(httpEnvValuesSnap).Add(httpEnvValuesSnap)
		got, _, err := rd.MetricLabelNamesForServiceIn(lctx, metric, svcLabel, service, end.Add(-metricChartLabelsLookbk), end)
		cancel()
		if err != nil {
			return nil, err
		}
		names = got
		if names == nil {
			names = []string{}
		}
		s.metricChartCacheSet(ctx, key, names, metricChartLabelsTTL)
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set, nil
}

// redFamilyStatusLabel — SAF: kümedeki ilk durum etiketi ("" = yok).
func redFamilyStatusLabel(f redFamily, present map[string]bool) string {
	for _, l := range f.StatusLabels {
		if present[l] {
			return l
		}
	}
	return ""
}

// resolveServiceREDFamily — servisin RED ailelerini çözer (açıklama dosya
// başında). Hata → unknown çözüm + err; çağıran "yok" DEMEZ.
func (s *Server) resolveServiceREDFamily(ctx context.Context, rd redFamilyReader, service string, terms map[string]string, to time.Time) (redFamilyResolution, error) {
	at := to.Truncate(time.Minute)
	lm := rd.MetricLabelMap().Normalized()
	key := redFamilyKey(lm, service, terms[vmetrics.RoleEnv], terms[vmetrics.RoleCluster], at, metricSourceVM)
	var cached redFamilyResolution
	if s.metricChartCacheGet(ctx, key, &cached) {
		return cached, nil
	}
	res := redFamilyResolution{Service: service, State: "known", Primary: redKindNone, Tried: []string{}}
	// v0.10.1559 (operatör, prod: gRPC metrikleri VM'de var ama HTTP/RPC
	// sekmesinde gRPC bölümü hiç çıkmıyor; /red-family → state unknown,
	// reason timeout, tried []). Kök neden: bir ailenin okuma hatası tüm
	// çözümü bitiriyordu — HTTP etiket okuması zaman aşımına düşünce gRPC
	// aileleri HİÇ denenmiyordu. Artık hata AİLE BAŞINA: okunamayan aile
	// tried'a "okunamadı" diye yazılır, sıradaki denenir. Okunamayan HTTP
	// bugünkü gibi çizilir (varsayılan aile; unknown hâlinde de çiziliyordu);
	// yalnız HEPSİ okunamazsa unknown. Kısmi sonuç önbelleğe yazılmaz.
	var firstErr error
	failed := 0
	for _, f := range redFamilies {
		// v0.10.1574 — etiket keşfi SERVİSE kapsanır (redFamilyLabelSet); boş küme
		// = bu servis bu aileyi 24 sa'te yazmamış (filo geneli yokluk iddiası değil).
		step := "etiket okuması"
		labels, err := s.redFamilyLabelSet(ctx, rd, f.Probe, redFamilySvcLabel(lm), service, at)
		if err == nil && len(labels) == 0 {
			res.Tried = append(res.Tried, f.vmCount()+": metrik yok (bu servis için, son 24 sa)")
			continue
		}
		var sr vmetrics.LabelResolution
		if err == nil {
			sr = vmetrics.ResolveLabelRole(vmetrics.RoleService, lm, labels, true)
			if !sr.Applied() {
				res.Tried = append(res.Tried, f.vmCount()+": servis etiketi yok (service_name / LabelMap)")
				continue
			}
		}
		var ser []chstore.SpanMetricSeries
		if err == nil {
			step = "yoklama"
			ser, err = rd.QueryPromQLInstant(ctx, redFamilyPresenceQuery(f, sr.Label, service), at)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return redFamilyUnknown(service, "okuması ("+f.vmCount()+")", err), err
			}
			if firstErr == nil {
				firstErr = err
			}
			failed++
			res.Tried = append(res.Tried, f.vmCount()+": okunamadı ("+step+", "+string(sourcestate.Classify(err))+") — yok DEĞİL")
			if f.Kind == redKindHTTP {
				res.HTTP = &redFamilyPresence{ID: f.ID, Kind: f.Kind}
			}
			continue
		}
		if len(ser) == 0 {
			res.Tried = append(res.Tried, f.vmCount()+": bu servisin serisi yok (son 1 sa)")
			continue
		}
		p := &redFamilyPresence{ID: f.ID, Kind: f.Kind, StatusLabel: redFamilyStatusLabel(f, labels)}
		if len(terms) > 0 {
			sc, eerr := s.resolveMetricEnvScope(ctx, rd, f.Probe, terms, at)
			if eerr != nil {
				fs := httpEnvScopeFailed(eerr)
				p.EnvScope = &fs
			} else {
				p.EnvScope = &sc
			}
		}
		switch f.Kind {
		case redKindHTTP:
			res.HTTP = p
		case redKindGRPC:
			if res.GRPC == nil {
				res.GRPC = p
			} else {
				// rpc/dup: iki gRPC ailesi de yazılıyor — legacy birincil, bu aile
				// ÇİZİLMEZ (ms ↔ s tek sorguda toplanmasın).
				res.Tried = append(res.Tried, f.vmCount()+": VAR ama "+res.GRPC.ID+" birincil (iki gRPC ailesi birleştirilmez)")
			}
		}
	}
	switch {
	case res.HTTP != nil:
		res.Primary = redKindHTTP
	case res.GRPC != nil:
		res.Primary = redKindGRPC
	}
	res.Both = res.HTTP != nil && res.GRPC != nil
	if failed == len(redFamilies) {
		return redFamilyUnknown(service, "okuması", firstErr), firstErr
	}
	if failed > 0 {
		res.Reason = "Bazı aileler okunamadı (okunamadı ≠ yok); okunanlar çizildi: " + strings.Join(res.Tried, " · ")
		return res, nil
	}
	s.metricChartCacheSet(ctx, key, res, redFamilyTTL)
	return res, nil
}

// primaryFamily — SAF: çözümün birincil ailesi (none / unknown → HTTP: bugünkü sorgular).
func (r redFamilyResolution) primaryFamily() (redFamily, *redFamilyPresence) {
	switch r.Primary {
	case redKindGRPC:
		if r.GRPC != nil {
			f, _ := redFamilyByID(r.GRPC.ID)
			return f, r.GRPC
		}
	case redKindHTTP:
		if r.HTTP != nil {
			return redFamilies[0], r.HTTP
		}
	}
	return redFamilies[0], nil
}

// anyMetricPresent — SAF: en az bir ailenin metriği VM'de var (servis serisi
// olmasa da) — "histogram yok" ile "servisin serisi yok" ayrımı.
func (r redFamilyResolution) anyMetricPresent() bool {
	if r.HTTP != nil || r.GRPC != nil {
		return true
	}
	for _, t := range r.Tried {
		if !strings.Contains(t, ": metrik yok") {
			return true
		}
	}
	return false
}

// redFamilyIdentity — /metric-red ve /metric-throughput için gRPC kimliği:
// ilk VAR OLAN gRPC ailesi (src.MetricExists; VM'de tam taban, CH'de OTel
// adı). Yoksa ok=false + denenenler.
func redFamilyIdentity(ctx context.Context, src metricSource) (redFamily, string, bool, []string) {
	var tried []string
	for _, f := range grpcFamilies() {
		name := f.metricName(src)
		tried = append(tried, name)
		if ok, err := src.MetricExists(ctx, name); err == nil && ok {
			return f, name, true, tried
		}
	}
	return redFamily{}, "", false, tried
}
