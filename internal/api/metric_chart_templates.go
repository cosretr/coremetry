package api

// metric_chart_templates.go — v0.10.1386 (CoSRE metrik grafiği; operatör:
// "VictoriaMetrics sorgulayıp grafik getirse mesela"). SAF şablon kataloğu +
// PromQL kurucu; tablo testli (metric_chart_templates_test.go).
//
// Neden şablon (model değil): PromQL'i küçük modele yazdırmak R3'ün işi ve
// varsayılan KAPALI (D6). Burada her soru kapalı bir kataloğa oturur; sorgu
// metni deterministik ve önbellek anahtarına girer. Yer tutucular:
//
//	{sel}    tam seçici: __name__ adayları (vmetrics.NameCandidates/NameSelector —
//	         yazım kuralı tek yerde), servis etiketi (LabelMap.Service ya da
//	         keşif; konvansiyon service_name), ortam eşleştiricisi
//	         (resolveHTTPEnvScope matcher'ı — ASLA sessizce düşmez; demet
//	         uygulanmadığını kanıtta söyler), varyantın sabit eşleştiricisi
//	{sel5xx} {sel} + durum kodu etiketi =~"5.." (http_response_status_code,
//	         yoksa http_status_code — keşfedilen kümeden)
//	{by}     " by (a, b)" ya da "" (kırılımsız tek seri)
//	{byle}   histogram_quantile'ın "le[, a, b]" kırılımı
//	{win}    rate penceresi = max(4×adım, 60) sn
//
// Kırılımlı ifade topk(20, …) ile sarılır (D3: ≤20 seri; demet sonrasında da
// keser ve Truncated yazar). cAdvisor pod CPU/bellek (container_*) BURADA YOK
// (D5): o seri Thanos'ta, cpu/memory şablonları uygulamanın kendi process
// metriğini çizer ve kanıt Infrastructure sekmesini gösterir.

import (
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// v0.10.1386 — şablon kimlikleri.
const (
	mcJVMHeap  = "jvm_heap"
	mcJVMGC    = "jvm_gc"
	mcCPU      = "cpu"
	mcMemory   = "memory"
	mcThreads  = "threads"
	mcDBPool   = "db_pool"
	mcKafkaLag = "kafka_lag"
	mcHTTP5xx  = "http_5xx"
	mcHTTPRPS  = "http_rps"
	mcHTTPP99  = "http_p99"
	mcHTTPP95  = "http_p95"
)

// v0.10.1386 — tavanlar (D1/D3).
const (
	metricChartMaxSeries = 20
	metricChartMaxPoints = 120
	metricChartMinStepS  = 15
	metricChartMinWinS   = 60
)

// mcHTTPMetric — RED şablonlarının histogramı (OTel semconv adı; VM yazımı
// http_server_request_duration_seconds_* adaylardan, httpEnvMetric ile aynı aile).
const mcHTTPMetric = "http.server.request.duration"

// mcStatusLabels — durum kodu etiketi adayları, tercih sırasıyla
// (service_metric_red.go: http.response.status_code / http.status_code).
var mcStatusLabels = []string{"http_response_status_code", "http_status_code"}

// metricChartVariant — şablonun bir metrik ailesi. Demet varyantları sırayla
// dener: Probe metriğinin etiket kümesi boş değilse (ve NeedLabel varsa o
// etiket kümede ise) varyant seçilir; ilk uyan kazanır, TEK sorgu koşar.
type metricChartVariant struct {
	Probe     string
	Names     []string
	Kind      string // gauge | counter | count | sum | bucket (vmetrics.NameCandidates)
	NeedLabel string
	Match     string   // sabit ek eşleştirici (ör. jvm_memory_type="heap")
	Expr      string   // yer tutuculu ifade
	By        []string // varyantın kendi kırılımı (keşfedilen kümede olanlar uygulanır)
	Unit      string   // boş → şablonun birimi
	// Fleet — metrik servis etiketi taşımaz (ör. kafka-exporter): servis
	// süzgeci UYGULANMAZ ve kanıt bunu söyler.
	Fleet bool
}

// metricChartTemplate — katalog kalemi.
type metricChartTemplate struct {
	ID    string
	Title string // kanıt/grafik başlığı
	// Phrase — kendi başına metric_chart'a geri yönlenen çip ifadesi
	// ("JVM heap grafiği"); ask_service ve ortam takip çipleri bunu kullanır.
	Phrase   string
	Unit     string
	NeedsSvc bool
	ByPod    bool   // varsayılan kırılım: pod etiketi
	SpanAgg  string // RED şablonu: VM yoksa span spec-chart agg'ı (D4); "" = RED değil
	Infra    bool   // cAdvisor notu + Infrastructure linki (D5)
	Variants []metricChartVariant
}

// metricChartCatalogue — v0.10.1386: sabit katalog (sıra = rapor sırası).
var metricChartCatalogue = []metricChartTemplate{
	{ID: mcJVMHeap, Title: "JVM heap kullanımı", Phrase: "JVM heap grafiği", Unit: "By", NeedsSvc: true, ByPod: true,
		Variants: []metricChartVariant{
			{Probe: "jvm.memory.used", Names: []string{"jvm.memory.used"}, NeedLabel: "jvm_memory_type", Match: `jvm_memory_type="heap"`, Expr: "sum{by} ({sel})"},
			{Probe: "jvm.memory.used", Names: []string{"jvm.memory.used"}, NeedLabel: "area", Match: `area="heap"`, Expr: "sum{by} ({sel})"},
			{Probe: "jvm.memory.used", Names: []string{"jvm.memory.used"}, NeedLabel: "type", Match: `type="heap"`, Expr: "sum{by} ({sel})"},
		}},
	{ID: mcJVMGC, Title: "GC'de geçen süre", Phrase: "GC süresi grafiği", Unit: "%", NeedsSvc: true, ByPod: true,
		Variants: []metricChartVariant{
			{Probe: "jvm.gc.duration", Names: []string{"jvm.gc.duration"}, Kind: "sum", Expr: "100 * sum{by} (rate({sel}[{win}]))"},
			{Probe: "jvm.gc.pause", Names: []string{"jvm.gc.pause"}, Kind: "sum", Expr: "100 * sum{by} (rate({sel}[{win}]))"},
		}},
	{ID: mcCPU, Title: "CPU kullanımı (process)", Phrase: "CPU grafiği", Unit: "%", NeedsSvc: true, ByPod: true, Infra: true,
		Variants: []metricChartVariant{
			{Probe: "jvm.cpu.recent_utilization", Names: []string{"jvm.cpu.recent_utilization"}, Expr: "100 * max{by} ({sel})"},
			{Probe: "process.cpu.utilization", Names: []string{"process.cpu.utilization"}, Expr: "100 * max{by} ({sel})"},
			{Probe: "process.cpu.usage", Names: []string{"process.cpu.usage"}, Expr: "100 * max{by} ({sel})"},
		}},
	{ID: mcMemory, Title: "Bellek kullanımı (process)", Phrase: "bellek grafiği", Unit: "By", NeedsSvc: true, ByPod: true, Infra: true,
		Variants: []metricChartVariant{
			{Probe: "process.memory.usage", Names: []string{"process.memory.usage"}, Expr: "max{by} ({sel})"},
			{Probe: "process_resident_memory_bytes", Names: []string{"process_resident_memory_bytes"}, Expr: "max{by} ({sel})"},
		}},
	{ID: mcThreads, Title: "JVM thread sayısı", Phrase: "thread sayısı grafiği", Unit: "", NeedsSvc: true, ByPod: true,
		Variants: []metricChartVariant{
			{Probe: "jvm.thread.count", Names: []string{"jvm.thread.count"}, Expr: "sum{by} ({sel})"},
			{Probe: "jvm.threads.count", Names: []string{"jvm.threads.count"}, Expr: "sum{by} ({sel})"},
			{Probe: "jvm_threads_live_threads", Names: []string{"jvm_threads_live_threads"}, Expr: "sum{by} ({sel})"},
		}},
	{ID: mcDBPool, Title: "DB bağlantı havuzu", Phrase: "bağlantı havuzu grafiği", Unit: "", NeedsSvc: true,
		Variants: []metricChartVariant{
			{Probe: "db.client.connection.count", Names: []string{"db.client.connection.count"}, Expr: "sum{by} ({sel})", By: []string{"db_client_connection_state"}},
			{Probe: "db.client.connections.usage", Names: []string{"db.client.connections.usage"}, Expr: "sum{by} ({sel})", By: []string{"state"}},
			{Probe: "hikaricp_connections_active", Names: []string{"hikaricp_connections_active"}, Expr: "sum{by} ({sel})", By: []string{"pool"}},
		}},
	{ID: mcKafkaLag, Title: "Kafka consumer lag", Phrase: "Kafka consumer lag grafiği", Unit: "",
		Variants: []metricChartVariant{
			{Probe: "kafka.consumer.records_lag_max", Names: []string{"kafka.consumer.records_lag_max"}, Expr: "max{by} ({sel})", By: []string{"topic", "client_id"}},
			{Probe: "kafka_consumergroup_lag", Names: []string{"kafka_consumergroup_lag"}, Expr: "max{by} ({sel})", By: []string{"consumergroup", "topic"}, Fleet: true},
		}},
	{ID: mcHTTP5xx, Title: "HTTP 5xx oranı", Phrase: "5xx oranı grafiği", Unit: "%", SpanAgg: "error_rate",
		Variants: []metricChartVariant{
			{Probe: mcHTTPMetric, Names: []string{mcHTTPMetric}, Kind: "count",
				Expr: "100 * (sum{by} (rate({sel5xx}[{win}])) or 0 * sum{by} (rate({sel}[{win}]))) / sum{by} (rate({sel}[{win}]))"},
		}},
	{ID: mcHTTPRPS, Title: "HTTP istek hızı (req/s)", Phrase: "istek hızı grafiği", Unit: "", SpanAgg: "rate",
		Variants: []metricChartVariant{
			{Probe: mcHTTPMetric, Names: []string{mcHTTPMetric}, Kind: "count", Expr: "sum{by} (rate({sel}[{win}]))"},
		}},
	{ID: mcHTTPP99, Title: "HTTP p99 gecikme", Phrase: "p99 gecikme grafiği", Unit: "s", NeedsSvc: true, SpanAgg: "p99",
		Variants: []metricChartVariant{
			{Probe: mcHTTPMetric, Names: []string{mcHTTPMetric}, Kind: "bucket", Expr: "histogram_quantile(0.99, sum by ({byle}) (rate({sel}[{win}])))"},
		}},
	{ID: mcHTTPP95, Title: "HTTP p95 gecikme", Phrase: "p95 gecikme grafiği", Unit: "s", NeedsSvc: true, SpanAgg: "p95",
		Variants: []metricChartVariant{
			{Probe: mcHTTPMetric, Names: []string{mcHTTPMetric}, Kind: "bucket", Expr: "histogram_quantile(0.95, sum by ({byle}) (rate({sel}[{win}])))"},
		}},
}

// metricChartTemplateByID — katalog araması; yoksa ok=false.
func metricChartTemplateByID(id string) (metricChartTemplate, bool) {
	for _, t := range metricChartCatalogue {
		if t.ID == id {
			return t, true
		}
	}
	return metricChartTemplate{}, false
}

// metricChartStepS — SAF (D3): ≤120 nokta; adım = ⌈aralık/120⌉, en az 15 sn.
func metricChartStepS(rangeS int64) int {
	step := int((rangeS + metricChartMaxPoints - 1) / metricChartMaxPoints)
	if step < metricChartMinStepS {
		step = metricChartMinStepS
	}
	return step
}

// metricChartWinS — SAF: rate penceresi = max(4×adım, 60) sn.
func metricChartWinS(step int) int {
	if w := 4 * step; w > metricChartMinWinS {
		return w
	}
	return metricChartMinWinS
}

// metricChartQueryInput — kurucunun girdisi (demet çözer, kurucu SAF kalır).
type metricChartQueryInput struct {
	Variant     metricChartVariant
	Service     string // "" ya da Fleet → servis süzgeci yok
	SvcLabel    string
	EnvMatcher  string // resolveHTTPEnvScope matcher'ı; "" = süzgeç yok
	By          []string
	StatusLabel string // yalnız {sel5xx} taşıyan ifade
	// ErrMatch — v0.10.1429: {sel5xx}'in durum regex gövdesi; boş = "5.."
	// (HTTP, bayt bayt eski). gRPC ailesi sunucu-hatası kodlarını verir
	// (red_family.go grpcServerFaultMatch).
	ErrMatch string
	WinS     int
	// SvcValues — v0.10.1719 (briefing kalem 2): servis KÜMESİ süzgeci
	// `svc=~"a|b"` (vmetrics.EnvMatcher: QuoteMeta + dize kaçışı); dolu iken
	// Service yok sayılır. GroupedNoTopK: By kırılımı topk(20) ile SARILMAZ
	// (çağıran seri sayısını küme tavanıyla sınırlar; topk aralık sorgusunda
	// nokta başına seçer, servis serisi delikli gelirdi).
	SvcValues     []string
	GroupedNoTopK bool
}

// metricChartSelector — SAF: `{__name__…, svc="…", env…, sabit}`.
func metricChartSelector(in metricChartQueryInput, extra string) string {
	v := in.Variant
	parts := []string{vmetrics.NameSelector(vmetrics.NameCandidates(v.Kind, v.Names...))}
	switch {
	case v.Fleet || in.SvcLabel == "":
	case len(in.SvcValues) > 0:
		parts = append(parts, vmetrics.EnvMatcher(in.SvcLabel, in.SvcValues))
	case in.Service != "":
		parts = append(parts, in.SvcLabel+"="+vmetrics.PromStrTS(in.Service))
	}
	if m := strings.TrimSpace(in.EnvMatcher); m != "" {
		parts = append(parts, m)
	}
	if v.Match != "" {
		parts = append(parts, v.Match)
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// buildMetricChartPromQL — SAF: varyant + çözülmüş etiketler → TEK PromQL.
// Kırılımlı ifade topk(20, …) ile sarılır.
func buildMetricChartPromQL(in metricChartQueryInput) string {
	by, byle := "", "le"
	if len(in.By) > 0 {
		by = " by (" + strings.Join(in.By, ", ") + ")"
		byle = "le, " + strings.Join(in.By, ", ")
	}
	sel5xx := ""
	if in.StatusLabel != "" {
		m := in.ErrMatch
		if m == "" {
			m = httpServerFaultMatch
		}
		sel5xx = metricChartSelector(in, in.StatusLabel+`=~"`+m+`"`)
	}
	q := strings.NewReplacer(
		"{sel5xx}", sel5xx,
		"{sel}", metricChartSelector(in, ""),
		"{byle}", byle,
		"{by}", by,
		"{win}", fmt.Sprintf("%ds", in.WinS),
	).Replace(in.Variant.Expr)
	if len(in.By) > 0 && !in.GroupedNoTopK {
		q = fmt.Sprintf("topk(%d, %s)", metricChartMaxSeries, q)
	}
	return q
}

// metricChartTriedNames — SAF: şablonun TÜM varyantlarının yazım adayları
// (dürüst "bulunamadı" cevabının "denenen yazımlar" listesi; en çok max).
func metricChartTriedNames(t metricChartTemplate, max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range t.Variants {
		for _, c := range vmetrics.NameCandidates(v.Kind, v.Names...) {
			if seen[c] {
				continue
			}
			seen[c] = true
			if len(out) >= max {
				return out
			}
			out = append(out, c)
		}
	}
	return out
}

// pickMetricChartVariant — SAF: keşfedilen etiket kümeleri (Probe → küme)
// üzerinden ilk uyan varyant. Küme yok/boş = metrik yok; NeedLabel kümede
// değilse varyant atlanır.
func pickMetricChartVariant(t metricChartTemplate, present map[string]map[string]bool) (metricChartVariant, map[string]bool, bool) {
	for _, v := range t.Variants {
		set := present[v.Probe]
		if len(set) == 0 {
			continue
		}
		if v.NeedLabel != "" && !set[v.NeedLabel] {
			continue
		}
		return v, set, true
	}
	return metricChartVariant{}, nil, false
}

// metricChartStatusLabel — SAF: kümedeki ilk durum kodu etiketi ("" = yok).
func metricChartStatusLabel(present map[string]bool) string {
	for _, l := range mcStatusLabels {
		if present[l] {
			return l
		}
	}
	return ""
}

// metricChartByLabels — SAF: kırılım. groupBy "env" → ortam etiketi (kümede
// varsa); "pod" ya da şablon ByPod → pod etiketi; varyantın kendi kırılımı
// kümede olanlarıyla eklenir. Uygulanamayan kırılım note olarak döner.
func metricChartByLabels(t metricChartTemplate, v metricChartVariant, groupBy, podLabel, envLabel string, present map[string]bool) ([]string, string) {
	var by []string
	note := ""
	switch {
	case groupBy == "env":
		if envLabel != "" && present[envLabel] {
			by = append(by, envLabel)
		} else {
			note = "ortam kırılımı istendi ama metrik ortam etiketi taşımıyor — kırılımsız çizildi"
		}
	case groupBy == "pod" || (t.ByPod && groupBy == ""):
		if podLabel != "" && present[podLabel] {
			by = append(by, podLabel)
		} else if groupBy == "pod" || t.ByPod {
			note = "pod etiketi bulunamadı (LabelMap.Pod / k8s_pod_name / pod) — pod kırılımı yok"
		}
	}
	for _, l := range v.By {
		if present[l] {
			by = append(by, l)
		}
	}
	return by, note
}
