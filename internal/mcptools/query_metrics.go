package mcptools

// query_metrics.go — v0.10.1271 (CoSRE adım 2): AİLE tabanlı metrik okuma,
// PromQL'siz. query_metric'ten (ad + etiket eşlemeli serbest metrik) farkı:
// model metrik adı / etiket / PromQL BİLMEZ; bir aile seçer, sunucu aileyi
// sabit bir okuyucuya ve depoya bağlar.
//
// YÖNLENDİRME (aile → okuyucu → depo):
//
//	red | latency | throughput | errors
//	    → metricsrc.FamilyApp. Operatör brief'i: uygulama/HTTP RED metrikleri
//	      VictoriaMetrics'te. VM yapılandırılmışsa (Decide → BackendVM) Service
//	      Overview METRİK kipinin AYNI okuması (api buildServiceMetricRED:
//	      httpMetricIdentity + QueryMetricRate/CountRate + 5xx durum
//	      etiketi + histogram_quantile; Deps.ServiceMetricRED) → VictoriaMetrics,
//	      kanıtta çalışan MetricsQL. VM bu servis için HTTP metriği DÖNDÜRMEZSE
//	      (boş, hata değil) ClickHouse span RED'ine geçilir ve uyarı yazılır;
//	      VM HATASI yapısal backend_error'dır (sessiz geri düşüş yok). Durum
//	      etiketi yoksa hata oranı Overview gibi BİLİNMİYOR (0 değil, span'den
//	      de doldurulmaz). VM yoksa ya da group_by/cluster istendiyse (Overview
//	      metrik kipi servis düzeyindedir) → span-türevli RED,
//	      chstore.QuerySpanMetricMulti (MV/rollup önce, sınırlı ham yol) →
//	      ClickHouse.
//	jvm_heap | jvm_gc
//	    → metricsrc.FamilyOTelRuntime → Resolver.RuntimePods (VM yapılandırılmışsa
//	      VictoriaMetrics, değilse ClickHouse metric_points).
//	pod_cpu | pod_memory | pod_restarts
//	    → metricsrc.FamilyPodSaturation → Thanos (cAdvisor / kube-state-metrics),
//	      cluster ZORUNLU, matcher'lı (Resolver.ThanosInstant). Operatör
//	      (2026-10-17): "CPU/bellek VM'de olmayabilir; OpenShift'te Prometheus =
//	      Thanos" → VM yapılandırılmış olsa da bu aileler ASLA VM/CH
//	      kubeletstats'a (FamilyOTelInfra) düşmez; cluster çözülemezse yapısal
//	      needs_cluster hatası döner.
//
// TAVANLAR: ≤20 seri / satır (mtMaxSeries), ≤120 nokta (mtMaxPoints), pencere
// ≤7 g, okuma bütçesi ≤10 s.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/metricsrc"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const queryMetricsName = "query_metrics"

// qmFamilies — enum (şema ve doğrulama aynı listeden).
var qmFamilies = []string{"red", "latency", "throughput", "errors", "jvm_heap", "jvm_gc", "pod_cpu", "pod_memory", "pod_restarts"}

var qmFamilySynonyms = map[string]string{
	"rate": "throughput", "rps": "throughput", "trafik": "throughput", "istek": "throughput", "requests": "throughput",
	"error": "errors", "hata": "errors", "error_rate": "errors", "hatalar": "errors",
	"p99": "latency", "p95": "latency", "gecikme": "latency", "duration": "latency", "süre": "latency", "yavaşlık": "latency",
	"heap": "jvm_heap", "jvm": "jvm_heap", "memory_heap": "jvm_heap", "gc": "jvm_gc",
	"cpu": "pod_cpu", "memory": "pod_memory", "mem": "pod_memory", "bellek": "pod_memory", "ram": "pod_memory",
	"restart": "pod_restarts", "restarts": "pod_restarts", "oom": "pod_restarts",
	"health": "red", "sağlık": "red",
}

var qmGroupBy = []string{"none", "pod", "route"}

var qmGroupBySynonyms = map[string]string{
	"": "", "host": "pod", "instance": "pod", "pods": "pod", "endpoint": "route", "operation": "route",
	"http.route": "route", "routes": "route", "all": "none", "total": "none",
}

// qmKind — ailenin okuma yolu.
type qmKind int

const (
	qmSpans qmKind = iota
	qmRuntime
	qmPod
)

func qmKindOf(family string) qmKind {
	switch family {
	case "jvm_heap", "jvm_gc":
		return qmRuntime
	case "pod_cpu", "pod_memory", "pod_restarts":
		return qmPod
	}
	return qmSpans
}

// qmMetricsrcFamily — query_metrics ailesi → metricsrc ailesi (span RED için
// FamilyUnknown: metricsrc'nin değil, span hattının konusu).
func qmMetricsrcFamily(family string) metricsrc.Family {
	switch qmKindOf(family) {
	case qmRuntime:
		return metricsrc.FamilyOTelRuntime
	case qmPod:
		return metricsrc.FamilyPodSaturation
	}
	return metricsrc.FamilyUnknown
}

// qmAgg — span ailesinin bir serisi.
type qmAgg struct{ name, agg, unit string }

func qmSpanAggs(family string) []qmAgg {
	switch family {
	case "latency":
		return []qmAgg{{"p50", "p50", "ms"}, {"p95", "p95", "ms"}, {"p99", "p99", "ms"}}
	case "throughput":
		return []qmAgg{{"rate", "rate", "req/s"}}
	case "errors":
		return []qmAgg{{"errors", "errors", "count/bucket"}, {"error_rate", "error_rate", "%"}}
	}
	return []qmAgg{{"rate", "rate", "req/s"}, {"error_rate", "error_rate", "%"}, {"p99", "p99", "ms"}}
}

// MetricREDResult — Service Overview metrik kipi RED okumasının sonucu (api
// serviceMetricREDResponse'un modele dönük alt kümesi + çalışan sorgular).
type MetricREDResult struct {
	Source           string // "vm" | "ch" — okumayı cevaplayan seam
	Found            bool   // servis için HTTP throughput metriği çözüldü
	Metric           string
	Series           map[string][]chstore.SpanMetricSeries // rate · error_rate (%) · p50/p95/p99/avg (ms)
	ErrorsUnknown    bool                                  // durum-kodu etiketi yok → hata oranı bilinmiyor
	LatencyUnitKnown bool
	InfraExcluded    bool // v0.10.2454 — tesisat rotaları (health / scrape) toplamdan dışlandı
	EnvApplied       bool
	Partial          []string
	Queries          []string // çalışan MetricsQL (vmetrics.QueryRecorder)
	Tried            []string
}

// ServiceMetricREDReader — api'nin Overview metrik kipi adaptörü.
type ServiceMetricREDReader func(ctx context.Context, service, env string, from, to time.Time, mdp int) (MetricREDResult, error)

// qmBackends — okuma dikişleri (testte sahte).
type qmBackends struct {
	metricRED     ServiceMetricREDReader
	spanRED       func(ctx context.Context, f chstore.SpanMetricBatchFilter) (map[string][]chstore.SpanMetricSeries, int, error)
	router        MetricRouter
	runtime       chstore.RuntimePodReader // router nil iken
	clusterValues func(ctx context.Context, service string, from, to time.Time) ([]string, error)
	// podSelector — v0.10.2239 (Entity S4): servisin o cluster'daki pod
	// seçicisi (resolver komşuları, tam adlar); "" = bilinmiyor.
	podSelector func(ctx context.Context, service, clusterID string, at time.Time) string
}

func qmBackendsFrom(d Deps) qmBackends {
	b := qmBackends{router: d.MetricRouter, runtime: d.RuntimePods, metricRED: d.ServiceMetricRED}
	if d.Store != nil {
		st := d.Store
		b.spanRED = st.QuerySpanMetricMulti
		if b.runtime == nil {
			b.runtime = st
		}
		b.clusterValues = func(ctx context.Context, service string, from, to time.Time) ([]string, error) {
			rows, _, err := st.GetServiceClusterBreakdownSourced(ctx, service, from, to)
			out := make([]string, 0, len(rows))
			for _, r := range rows {
				out = append(out, r.Cluster)
			}
			return out, err
		}
	}
	b.podSelector = d.ServicePodSelector
	return b
}

// qmPodAt — SAF: pod kümesinin anı. Pencere "şimdi"de bitiyorsa sıfır (canlı
// dizin), geçmişteyse pencere sonu (entity_seen_5m servis öneki).
func qmPodAt(to time.Time) time.Time {
	if wallNow().Sub(to) < 2*time.Minute {
		return time.Time{}
	}
	return to
}

// qmPoint / qmSeries — çıktı şekli.
type qmPoint struct {
	T int64   `json:"t"` // unix ms (kova başı)
	V float64 `json:"v"`
}

type qmStats struct {
	Avg  float64 `json:"avg"`
	Max  float64 `json:"max"`
	Last float64 `json:"last"`
}

type qmSeries struct {
	Name   string    `json:"name"`
	Group  string    `json:"group,omitempty"`
	Unit   string    `json:"unit"`
	Stats  qmStats   `json:"stats"`
	Points []qmPoint `json:"points"`
}

type qmRow struct {
	Pod       string  `json:"pod"`
	Namespace string  `json:"namespace,omitempty"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	LimitPct  float64 `json:"limitPct,omitempty"` // jvm_heap: used / limit %
}

// qmReq — çözülmüş istek (zarfın `request` alanı).
type qmReq struct {
	service, family, groupBy, env, cluster, window string
	from, to                                       time.Time
}

func (q qmReq) echo() map[string]any {
	m := map[string]any{"family": q.family, "group_by": q.groupBy, "window": q.window,
		"from_ms": q.from.UnixMilli(), "to_ms": q.to.UnixMilli()}
	if q.service != "" {
		m["service"] = q.service
	}
	if q.env != "" {
		m["env"] = q.env
	}
	if q.cluster != "" {
		m["cluster"] = q.cluster
	}
	return m
}

func queryMetricsTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "query_metrics",
		ShortDescription: "Aile ile metrik, PromQL yok: red/latency/throughput/errors, jvm_heap/jvm_gc, pod_cpu/pod_memory/pod_restarts (Thanos).⟦ Adla metrik: query_metric.|⟧", // v0.10.1302 — ⟦full|simple⟧
		Description: "Family-based metric read — NO PromQL, NO metric names. Pick `family` and the server builds the query " +
			"from a fixed template and routes it: red|latency|throughput|errors → the service Overview's HTTP metric RED from " +
			"VictoriaMetrics when it is the metric store (falls back to span-derived RED from ClickHouse, with a warning, when VM " +
			"has no HTTP metric for the service; error rate is reported unavailable when the metric has no status label), else " +
			"span-derived RED from ClickHouse; jvm_heap|jvm_gc → OTel JVM runtime (VictoriaMetrics when configured, else ClickHouse), " +
			"one row per pod; pod_cpu|pod_memory|pod_restarts → Thanos cAdvisor/kube-state-metrics, cluster-scoped (cluster is resolved " +
			"from the service's spans when omitted; if it cannot be resolved the result is error.code=needs_cluster with candidates — ask " +
			"the operator, never guess). service is required except for pod_* with a cluster. group_by: none|pod|route (route = http.route, span families only). " +
			"window: 15m|1h|6h|24h|7d (default 1h) or from_ms+to_ms (unix ms). Caps: ≤20 series/rows, ≤120 points, ≤7 days, 10 s. " +
			"Unknown enum values are coerced to the default and reported in `warnings`. Every result carries `evidence` " +
			"{source, query (the executed PromQL / SQL / reader summary), window, env, cluster, rows|series, durationMs}; " +
			"empty=true + message 'veri yok: …' means nothing matched — not 'no problem'. Errors are structured {code, message}. " +
			"For an arbitrary metric by name use query_metric; for trace samples query_traces; for logs query_logs.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":  map[string]any{"type": "string", "description": "Exact service name (list_services)."},
				"family":   map[string]any{"type": "string", "enum": qmFamilies, "description": "What to read. Default red (rate + error_rate + p99)."},
				"window":   map[string]any{"type": "string", "enum": cosreWindowEnum, "description": "Lookback ending at the chat anchor. Default 1h."},
				"from_ms":  map[string]any{"type": "integer", "description": "Absolute start, unix ms (with to_ms). Overrides window."},
				"to_ms":    map[string]any{"type": "integer", "description": "Absolute end, unix ms (with from_ms)."},
				"env":      map[string]any{"type": "string", "description": "Deployment environment (e.g. prod, uat, int, prep)."},
				"cluster":  map[string]any{"type": "string", "description": "Cluster id or name (list_clusters). Required by pod_* unless resolvable from the service."},
				"group_by": map[string]any{"type": "string", "enum": qmGroupBy, "description": "none (default for span families) | pod (default for jvm_*/pod_*) | route."},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			return runQueryMetrics(ctx, d, qmBackendsFrom(d), raw)
		},
	}
}

// runQueryMetrics — aracın gövdesi (Deps + dikişler; testler doğrudan çağırır).
func runQueryMetrics(ctx context.Context, d Deps, b qmBackends, raw json.RawMessage) (cosreResult, error) {
	a, warnings := parseCosreArgs(raw)
	var q qmReq
	var w string
	q.family, w = coerceEnum("family", a.str("family", "metric", "kind"), qmFamilies, qmFamilySynonyms, "red")
	if w != "" {
		warnings = append(warnings, w)
	}
	kind := qmKindOf(q.family)
	defGroup := "none"
	if kind != qmSpans {
		defGroup = "pod"
	}
	q.groupBy, w = coerceEnum("group_by", a.str("group_by", "groupby", "by"), qmGroupBy, qmGroupBySynonyms, defGroup)
	if w != "" {
		warnings = append(warnings, w)
	}
	if q.groupBy == "" {
		q.groupBy = defGroup
	}
	if q.groupBy == "route" && kind != qmSpans {
		warnings = append(warnings, "group_by=route yalnız span aileleri (red/latency/throughput/errors) için; pod kullanıldı")
		q.groupBy = "pod"
	}
	q.service = a.str("service", "service_name", "servis")
	q.env = a.str("env", "environment", "ortam")
	q.cluster = a.str("cluster")
	var ww []string
	q.from, q.to, q.window, ww = cosreWindow(ctx, a, int(mtMaxWindow/time.Second), "1h")
	warnings = append(warnings, ww...)

	if q.service == "" && !(kind == qmPod && q.cluster != "") {
		r := cosreFail(queryMetricsName, q.echo(), warnings, cosreErrBadArgs, "service gerekli (list_services ile tam adı bul); yalnız pod_* aileleri cluster ile servissiz çalışır")
		r.Error.Need = []string{"service"}
		return r, nil
	}
	switch kind {
	case qmRuntime:
		return qmRunRuntime(ctx, b, q, warnings)
	case qmPod:
		return qmRunPod(ctx, d, b, q, warnings)
	}
	return qmRunApp(ctx, d, b, q, warnings)
}

// ── uygulama/HTTP RED: VictoriaMetrics (Overview metrik kipi) ───────────

// qmVMAggs — VM metrik kipinin aileye düşen serileri. errors ailesinde VM
// hata SAYISI serisi taşımaz (Overview yalnız yüzdeyi çizer).
func qmVMAggs(family string) []qmAgg {
	switch family {
	case "errors":
		return []qmAgg{{"error_rate", "error_rate", "%"}}
	}
	return qmSpanAggs(family)
}

// qmHasPoints — SAF: herhangi bir seride nokta var mı.
func qmHasPoints(ser []chstore.SpanMetricSeries) bool {
	for _, s := range ser {
		if len(s.Points) > 0 {
			return true
		}
	}
	return false
}

func qmRunApp(ctx context.Context, d Deps, b qmBackends, q qmReq, warnings []string) (cosreResult, error) {
	useVM := b.router != nil && b.metricRED != nil &&
		b.router.Resolve(metricsrc.FamilyApp, metricsrc.Scope{Service: q.service, Env: q.env}).Backend == metricsrc.BackendVM
	if !useVM {
		return qmRunSpans(ctx, d, b, q, warnings)
	}
	if q.groupBy != "none" || q.cluster != "" {
		warnings = append(warnings, "VictoriaMetrics metrik kipi servis düzeyindedir (Overview ile aynı); group_by/cluster için ClickHouse span RED okundu")
		return qmRunSpans(ctx, d, b, q, warnings)
	}
	ev := &cosreEvidence{Source: evSourceVM, QueryKind: evKindPromQL, Window: evWin(q.from, q.to), Env: q.env}
	elapsed := stopwatch()
	rctx, cancel := context.WithTimeout(ctx, cosreBudget)
	res, err := b.metricRED(rctx, q.service, q.env, q.from, q.to, mtStepBudget)
	cancel()
	ev.DurationMs = elapsed()
	ev.Query = strings.Join(res.Queries, "\n")
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return cosreResult{}, ctx.Err()
		}
		r := cosreFail(queryMetricsName, q.echo(), warnings, cosreClassify(err), cosreErrMsg("VictoriaMetrics okuması başarısız", err))
		r.Evidence = ev
		return r, nil
	}
	if res.Source != "" && res.Source != "vm" {
		// Seam VM dışını seçti (ayar çağrı arasında değişti) — span yolu.
		return qmRunSpans(ctx, d, b, q, warnings)
	}
	if !res.Found || !qmHasPoints(res.Series["rate"]) {
		why := "bu servis için HTTP metriği yok"
		if res.Found {
			why = res.Metric + " bu pencerede servis için seri döndürmedi"
		}
		warnings = append(warnings, "kaynak VictoriaMetrics → ClickHouse span RED'e geçti: VictoriaMetrics'te "+why+" (Overview'un 'metrik bulunamadı — span gösteriliyor' durumu)")
		return qmRunSpans(ctx, d, b, q, warnings)
	}
	if q.family == "latency" && !res.LatencyUnitKnown {
		warnings = append(warnings, "kaynak VictoriaMetrics → ClickHouse span RED'e geçti: "+res.Metric+" süre birimi bilinmiyor (Overview gibi P50/P95/P99 span'den)")
		return qmRunSpans(ctx, d, b, q, warnings)
	}
	var aggs []qmAgg
	var unavailable []string
	for _, ag := range qmVMAggs(q.family) {
		switch {
		case ag.name == "error_rate" && res.ErrorsUnknown:
			unavailable = append(unavailable, ag.name)
			warnings = append(warnings, "eksik: failure rate (durum etiketi yok) — "+res.Metric+" metriğinde http.response.status_code / http.status_code etiketi yok; hata oranı BİLİNMİYOR (0 değil)")
		case (ag.name == "p50" || ag.name == "p95" || ag.name == "p99") && !res.LatencyUnitKnown:
			unavailable = append(unavailable, ag.name)
			warnings = append(warnings, "eksik: "+ag.name+" (birim bilinmiyor)")
		default:
			aggs = append(aggs, ag)
		}
	}
	if q.env != "" && !res.EnvApplied {
		warnings = append(warnings, "env filtresi VictoriaMetrics'te ifade edilemedi — seriler TÜM ortamları kapsıyor")
	}
	for _, p := range res.Partial {
		warnings = append(warnings, "kısmi: "+p)
	}
	var series []qmSeries
	if len(aggs) > 0 {
		var total int
		series, total = qmShapeSpanSeries(res.Series, aggs, mtMaxSeries, mtMaxPoints)
		if total > len(series) {
			warnings = append(warnings, fmt.Sprintf("%d seriden en büyük %d'si döndü (tavan %d)", total, len(series), mtMaxSeries))
		}
	}
	ev.Series = intPtr(len(series))
	out := cosreResult{Tool: queryMetricsName, OK: true, Request: q.echo(), Warnings: warnings, Evidence: ev}
	data := map[string]any{"metric": res.Metric, "series": series}
	if len(unavailable) > 0 {
		data["unavailable"] = unavailable
	}
	if series == nil {
		data["series"] = []qmSeries{}
	}
	out.Data = data
	return out, nil
}

// ── span RED aileleri (ClickHouse) ──────────────────────────────────────

func qmRunSpans(ctx context.Context, d Deps, b qmBackends, q qmReq, warnings []string) (cosreResult, error) {
	if b.spanRED == nil {
		return cosreFail(queryMetricsName, q.echo(), warnings, cosreErrNotConfigured, "ClickHouse span okuyucusu bağlı değil"), nil
	}
	filters := []chstore.FilterExpr{{Key: "service.name", Op: "=", Values: []string{q.service}}}
	if q.env != "" {
		filters = append(filters, chstore.FilterExpr{Key: "deployment.environment", Op: "=", Values: []string{q.env}})
	}
	clusterName := ""
	if q.cluster != "" {
		fe, name, cw := spanClusterFilter(d, q.cluster)
		filters = append(filters, fe)
		clusterName = name
		if cw != "" {
			warnings = append(warnings, cw)
		}
	}
	var groupBy []string
	switch q.groupBy {
	case "route":
		groupBy = []string{"http.route"}
	case "pod":
		groupBy = []string{"host.name"}
	}
	aggs := qmSpanAggs(q.family)
	specs := make([]chstore.SpanMetricAggSpec, len(aggs))
	for i, ag := range aggs {
		specs[i] = chstore.SpanMetricAggSpec{Name: ag.name, Aggregation: ag.agg, Field: "duration_ms"}
	}
	f := chstore.SpanMetricBatchFilter{Filters: filters, GroupBy: groupBy, From: q.from, To: q.to, MaxDataPoints: mtStepBudget, Aggs: specs}
	elapsed := stopwatch()
	rctx, cancel := context.WithTimeout(ctx, cosreBudget)
	res, step, err := b.spanRED(rctx, f)
	cancel()
	ev := &cosreEvidence{Source: evSourceCH, QueryKind: evKindSummary, Window: evWin(q.from, q.to), Env: q.env, Cluster: clusterName,
		Query: qmSpanQueryText(aggs, filters, groupBy, q.from, q.to, step)}
	ev.DurationMs = elapsed()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return cosreResult{}, ctx.Err()
		}
		r := cosreFail(queryMetricsName, q.echo(), warnings, cosreClassify(err), cosreErrMsg("ClickHouse okuması başarısız", err))
		r.Evidence = ev
		return r, nil
	}
	series, total := qmShapeSpanSeries(res, aggs, mtMaxSeries, mtMaxPoints)
	if total > len(series) {
		warnings = append(warnings, fmt.Sprintf("%d seriden en büyük %d'si döndü (tavan %d)", total, len(series), mtMaxSeries))
	}
	ev.Series = intPtr(len(series))
	out := cosreResult{Tool: queryMetricsName, OK: true, Request: q.echo(), Warnings: warnings, Evidence: ev}
	if len(series) == 0 {
		out.Empty = true
		out.Message = "veri yok: " + scopeTR([2]string{"servis", q.service}, [2]string{"aile", q.family}, [2]string{"env", q.env}, [2]string{"cluster", clusterName}) +
			" için " + windowTR(q.from, q.to) + " penceresinde span bulunamadı (ClickHouse)"
		return out, nil
	}
	out.Data = map[string]any{"stepSeconds": step, "series": series}
	return out, nil
}

// qmSpanQueryText — SAF: span RED okumasının deterministik özeti (yol MV /
// rollup / ham, okuyucunun içinde seçilir).
func qmSpanQueryText(aggs []qmAgg, filters []chstore.FilterExpr, groupBy []string, from, to time.Time, step int) string {
	sel := make([]string, len(aggs))
	for i, a := range aggs {
		sel[i] = a.agg
	}
	s := "QuerySpanMetricMulti[service MV → rollup → spans] SELECT " + strings.Join(sel, ", ") +
		" WHERE " + filtersSummary(filters) +
		" AND time >= " + sqlLiteral(from) + " AND time < " + sqlLiteral(to)
	if len(groupBy) > 0 {
		s += " GROUP BY " + strings.Join(groupBy, ", ")
	}
	if step > 0 {
		s += fmt.Sprintf(" STEP %ds", step)
	}
	return s
}

// qmShapeSpanSeries — SAF: agg × grup serilerini tavana indir. Grup kümesi
// İLK serinin (rate / p50 / errors) alanına göre sıralanır ve tüm agg'lerde
// AYNI gruplar tutulur (grup başına tutarlı tablo). Noktalar ≤maxPoints.
func qmShapeSpanSeries(res map[string][]chstore.SpanMetricSeries, aggs []qmAgg, maxSeries, maxPoints int) ([]qmSeries, int) {
	groupKey := func(s chstore.SpanMetricSeries) string { return strings.Join(s.GroupKey, "|") }
	area := map[string]float64{}
	total := 0
	for i, ag := range aggs {
		for _, s := range res[ag.name] {
			total++
			k := groupKey(s)
			if _, ok := area[k]; !ok {
				area[k] = 0 // yalnız sonraki agg'de görülen grup da sıralamaya girer
			}
			if i == 0 {
				for _, p := range s.Points {
					area[k] += p.Value
				}
			}
		}
	}
	groups := sortedKeys(area)
	sort.SliceStable(groups, func(i, j int) bool { return area[groups[i]] > area[groups[j]] })
	maxGroups := maxSeries / len(aggs)
	if maxGroups < 1 {
		maxGroups = 1
	}
	if len(groups) > maxGroups {
		groups = groups[:maxGroups]
	}
	keep := map[string]int{}
	for i, g := range groups {
		keep[g] = i
	}
	var out []qmSeries
	for _, g := range groups {
		for _, ag := range aggs {
			for _, s := range res[ag.name] {
				if groupKey(s) != g {
					continue
				}
				pts := thinEvery(s.Points, maxPoints)
				qs := qmSeries{Name: ag.name, Group: g, Unit: ag.unit, Points: make([]qmPoint, 0, len(pts))}
				for _, p := range pts {
					qs.Points = append(qs.Points, qmPoint{T: p.Time / 1e6, V: round4(p.Value)})
				}
				qs.Stats = qmSeriesStats(s.Points)
				out = append(out, qs)
			}
		}
	}
	return out, total
}

// qmSeriesStats — SAF: TAM seri üzerinden (seyreltmeden önce) özet.
func qmSeriesStats(pts []chstore.SpanMetricPoint) qmStats {
	if len(pts) == 0 {
		return qmStats{}
	}
	sum, mx := 0.0, pts[0].Value
	for _, p := range pts {
		sum += p.Value
		if p.Value > mx {
			mx = p.Value
		}
	}
	return qmStats{Avg: round4(sum / float64(len(pts))), Max: round4(mx), Last: round4(pts[len(pts)-1].Value)}
}

// ── JVM runtime aileleri (VM / CH) ──────────────────────────────────────

func qmRunRuntime(ctx context.Context, b qmBackends, q qmReq, warnings []string) (cosreResult, error) {
	reader := b.runtime
	source, backendTxt := evSourceCH, "ClickHouse metric_points"
	if b.router != nil {
		reader = b.router.RuntimePods()
		if b.router.Resolve(qmMetricsrcFamily(q.family), metricsrc.Scope{Service: q.service, Env: q.env}).Backend == metricsrc.BackendVM {
			source, backendTxt = evSourceVM, "VictoriaMetrics"
		}
	}
	if reader == nil {
		return cosreFail(queryMetricsName, q.echo(), warnings, cosreErrNotConfigured, "JVM runtime okuyucusu bağlı değil"), nil
	}
	if q.env != "" {
		warnings = append(warnings, "env JVM pod okumasında uygulanamaz (okuyucu ortam boyutu taşımıyor) — pod'lar ortamdan bağımsız listelendi")
	}
	if q.cluster != "" {
		warnings = append(warnings, "cluster JVM pod okumasında uygulanamaz — yok sayıldı")
	}
	if q.groupBy == "none" {
		warnings = append(warnings, "jvm_* aileleri pod başına satır döndürür; group_by=none için özet `summary` alanında")
	}
	ev := &cosreEvidence{Source: source, QueryKind: evKindSummary, Window: evWin(q.from, q.to), Env: q.env, Query: qmRuntimeQueryText(q.family, backendTxt, q.service, q.from, q.to)}
	elapsed := stopwatch()
	rctx, cancel := context.WithTimeout(ctx, cosreBudget)
	var samples []chstore.CapacitySample
	var err error
	if q.family == "jvm_heap" {
		samples, err = reader.JVMHeapPodUsage(rctx, q.from, q.to)
	} else {
		samples, err = reader.JVMGCPodPause(rctx, q.from, q.to)
	}
	cancel()
	ev.DurationMs = elapsed()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return cosreResult{}, ctx.Err()
		}
		r := cosreFail(queryMetricsName, q.echo(), warnings, cosreClassify(err), cosreErrMsg(source+" okuması başarısız", err))
		r.Evidence = ev
		return r, nil
	}
	rows, total := qmRuntimeRows(samples, q.service, q.family, mtMaxSeries)
	if total > len(rows) {
		warnings = append(warnings, fmt.Sprintf("%d pod'dan en yüksek %d'i döndü", total, len(rows)))
	}
	ev.Rows = intPtr(len(rows))
	out := cosreResult{Tool: queryMetricsName, OK: true, Request: q.echo(), Warnings: warnings, Evidence: ev}
	if len(rows) == 0 {
		out.Empty = true
		out.Message = "veri yok: " + scopeTR([2]string{"servis", q.service}, [2]string{"aile", q.family}) + " için " +
			windowTR(q.from, q.to) + " penceresinde JVM pod metriği bulunamadı (" + source + ") — servis OTel JVM metriği göndermiyor olabilir"
		return out, nil
	}
	out.Data = map[string]any{"pods": total, "summary": qmRowsSummary(rows), "rows": rows}
	return out, nil
}

// qmRuntimeQueryText — SAF: okuyucu özeti.
func qmRuntimeQueryText(family, backend, service string, from, to time.Time) string {
	what := "JVMHeapPodUsage: avg(jvm.memory.used{jvm.memory.type=\"heap\"}) / avg(jvm.memory.limit) BY service, pod"
	if family == "jvm_gc" {
		what = "JVMGCPodPause: increase(jvm.gc.duration_sum) / increase(jvm.gc.duration_count) BY service, pod"
	}
	return what + " FROM " + backend + " WHERE service = " + sqlQuote(service) +
		" AND time >= " + sqlLiteral(from) + " AND time < " + sqlLiteral(to)
}

// qmRuntimeRows — SAF: filo satırlarından servisin pod'ları, değere göre
// azalan, tavanlı.
func qmRuntimeRows(in []chstore.CapacitySample, service, family string, max int) ([]qmRow, int) {
	var rows []qmRow
	for _, s := range in {
		if !strings.EqualFold(s.Instance, service) {
			continue
		}
		r := qmRow{Pod: s.Subkey}
		if family == "jvm_heap" {
			r.Value, r.Unit = round4(s.Usage), "bytes"
			if s.Limit > 0 {
				r.LimitPct = round4(100 * s.Usage / s.Limit)
			}
		} else {
			r.Value, r.Unit = round4(s.Usage), "ms"
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].LimitPct != rows[j].LimitPct {
			return rows[i].LimitPct > rows[j].LimitPct
		}
		if rows[i].Value != rows[j].Value {
			return rows[i].Value > rows[j].Value
		}
		return rows[i].Pod < rows[j].Pod
	})
	total := len(rows)
	if len(rows) > max {
		rows = rows[:max]
	}
	return rows, total
}

// qmRowsSummary — SAF: satırların deterministik özeti (model saymasın).
func qmRowsSummary(rows []qmRow) map[string]any {
	if len(rows) == 0 {
		return map[string]any{}
	}
	sum, mx := 0.0, rows[0]
	for _, r := range rows {
		sum += r.Value
		if r.Value > mx.Value {
			mx = r
		}
	}
	m := map[string]any{"count": len(rows), "sum": round4(sum), "avg": round4(sum / float64(len(rows))), "max": mx.Value, "maxPod": mx.Pod, "unit": rows[0].Unit}
	if rows[0].LimitPct > 0 {
		m["maxLimitPct"] = rows[0].LimitPct
		m["maxLimitPctPod"] = rows[0].Pod
	}
	return m
}

// ── pod aileleri (Thanos, cluster zorunlu) ──────────────────────────────

func qmPodFamily(family string) (string, string) {
	switch family {
	case "pod_memory":
		return thanos.PodFamilyMemory, "bytes"
	case "pod_restarts":
		return thanos.PodFamilyRestarts, "count"
	}
	return thanos.PodFamilyCPU, "cores"
}

// qmResolveCluster — cluster referansı: verilmişse o; değilse servisin span
// cluster değerleri → kayıtlı Remote Cluster'lar. Tek eşleşme → o; aksi
// yapısal needs_cluster hatası.
func qmResolveCluster(ctx context.Context, d Deps, b qmBackends, q qmReq) (string, string, *cosreError) {
	if q.cluster != "" {
		return q.cluster, "", nil
	}
	var all []ClusterRef
	if d.Clusters != nil {
		all = d.Clusters()
	}
	need := &cosreError{Code: cosreErrNeedsCluster, Need: []string{"cluster"}, Candidates: clusterNames(all)}
	if b.clusterValues == nil || q.service == "" {
		need.Message = "cluster gerekli: pod metrikleri Thanos'ta cluster kapsamlı okunur — cluster ver (list_clusters)"
		return "", "", need
	}
	vals, err := b.clusterValues(ctx, q.service, q.from, q.to)
	if err != nil && len(vals) == 0 {
		need.Message = "cluster gerekli: servisin cluster'ı span'lerden çözülemedi (" + truncateRunes(err.Error(), 120) + ") — cluster ver"
		return "", "", need
	}
	var mapped []ClusterRef
	seen := map[string]bool{}
	for _, v := range vals {
		for _, c := range all {
			match := strings.EqualFold(c.Name, v) || strings.EqualFold(c.ID, v)
			for _, sv := range c.SpanValues {
				if strings.EqualFold(sv, v) {
					match = true
				}
			}
			if match && !seen[c.ID+"\x00"+c.Name] {
				seen[c.ID+"\x00"+c.Name] = true
				mapped = append(mapped, c)
			}
		}
	}
	switch len(mapped) {
	case 1:
		ref := mapped[0].ID
		if ref == "" {
			ref = mapped[0].Name
		}
		return ref, fmt.Sprintf("cluster servisin span'lerinden çözüldü: %s", mapped[0].Name), nil
	case 0:
		need.Message = fmt.Sprintf("cluster gerekli: %s servisinin span'lerinde kayıtlı bir Remote Cluster'a eşlenen cluster değeri yok — cluster ver", q.service)
	default:
		need.Candidates = clusterNames(mapped)
		need.Message = fmt.Sprintf("cluster gerekli: %s birden çok cluster'da çalışıyor (%s) — hangisi?", q.service, strings.Join(need.Candidates, ", "))
	}
	return "", "", need
}

// qmPodWindowMaxNoService — servissiz (cluster geneli) pod okumasının pencere
// tavanı: tüm cluster'ın cAdvisor serileri üzerinde range-vector pahalı.
const qmPodWindowMaxNoService = 24 * time.Hour

func qmRunPod(ctx context.Context, d Deps, b qmBackends, q qmReq, warnings []string) (cosreResult, error) {
	thanosOK := b.router != nil && d.Clusters != nil && len(d.Clusters()) > 0
	if !thanosOK {
		return cosreFail(queryMetricsName, q.echo(), warnings, cosreErrNotConfigured,
			"Thanos Remote Cluster yapılandırılmamış: pod CPU/bellek/restart Thanos'tan okunur (VM/ClickHouse'a düşülmez)"), nil
	}
	ref, note, cerr := qmResolveCluster(ctx, d, b, q)
	if cerr != nil {
		return cosreResult{Tool: queryMetricsName, OK: false, Request: q.echo(), Warnings: warnings, Error: cerr}, nil
	}
	if note != "" {
		warnings = append(warnings, note)
	}
	route := b.router.Resolve(qmMetricsrcFamily(q.family), metricsrc.Scope{Service: q.service, Env: q.env, Cluster: ref})
	if !route.ClusterScoped() {
		var all []ClusterRef
		if d.Clusters != nil {
			all = d.Clusters()
		}
		return cosreResult{Tool: queryMetricsName, OK: false, Request: q.echo(), Warnings: warnings, Error: &cosreError{
			Code: cosreErrNeedsCluster, Need: []string{"cluster"}, Candidates: clusterNames(all),
			Message: "cluster gerekli: " + route.Reason,
		}}, nil
	}
	q.cluster = route.Cluster.Name
	if q.env != "" {
		warnings = append(warnings, "env Thanos pod metriklerinde etiket değil — pod kümesi servis adından, kapsam cluster'dan seçildi")
	}
	if q.service == "" && q.to.Sub(q.from) > qmPodWindowMaxNoService {
		q.from = q.to.Add(-qmPodWindowMaxNoService)
		warnings = append(warnings, "servissiz (cluster geneli) pod okuması en fazla 24h; son 24 saat okundu")
	}
	podRe := ""
	if q.service != "" {
		if b.podSelector != nil {
			podRe = b.podSelector(ctx, q.service, route.Cluster.EffectiveID(), qmPodAt(q.to))
		}
		if podRe == "" { // boş seçici cluster geneli olurdu — Thanos'a gidilmez
			return cosreResult{Tool: queryMetricsName, OK: true, Empty: true, Request: q.echo(), Warnings: warnings,
				Message: "pod bilinmiyor: entity deposunda " + q.service + " servisinin " + route.Cluster.Name +
					" cluster'ında pod'u yok (span ya da Kubernetes eşlemesi yok) — Thanos sorgulanmadı"}, nil
		}
	}
	var offset time.Duration
	if now := wallNow(); now.Sub(q.to) >= time.Minute {
		offset = now.Sub(q.to).Truncate(time.Second)
	}
	fam, unit := qmPodFamily(q.family)
	expr, err := thanos.PodFamilyQuery(fam, route.Cluster.NamespaceFilter, podRe, q.to.Sub(q.from), offset, q.groupBy != "none", mtMaxSeries)
	if err != nil {
		return cosreFail(queryMetricsName, q.echo(), warnings, cosreErrBadArgs, err.Error()), nil
	}
	scoped, _ := route.ScopedQuery(expr)
	ev := &cosreEvidence{Source: evSourceThanos, QueryKind: evKindPromQL, Query: scoped, Window: evWin(q.from, q.to), Env: q.env, Cluster: route.Cluster.Name}
	elapsed := stopwatch()
	rctx, cancel := context.WithTimeout(ctx, cosreBudget)
	samples, _, err := b.router.ThanosInstant(rctx, qmMetricsrcFamily(q.family), ref, expr)
	cancel()
	ev.DurationMs = elapsed()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return cosreResult{}, ctx.Err()
		}
		r := cosreFail(queryMetricsName, q.echo(), warnings, cosreClassify(err), cosreErrMsg("Thanos okuması başarısız", err))
		r.Evidence = ev
		return r, nil
	}
	rows := qmPodRows(samples, unit, mtMaxSeries)
	ev.Rows = intPtr(len(rows))
	out := cosreResult{Tool: queryMetricsName, OK: true, Request: q.echo(), Warnings: warnings, Evidence: ev}
	if len(rows) == 0 {
		out.Empty = true
		out.Message = "veri yok: " + scopeTR([2]string{"servis", q.service}, [2]string{"aile", q.family}, [2]string{"cluster", route.Cluster.Name}) +
			" için " + windowTR(q.from, q.to) + " penceresinde Thanos serisi bulunamadı"
		return out, nil
	}
	out.Data = map[string]any{"summary": qmRowsSummary(rows), "rows": rows}
	return out, nil
}

// qmPodRows — SAF: Thanos örnekleri → satırlar (azalan, tavanlı). byPod=false
// sorgusu etiketsiz tek örnek döndürür → pod "(toplam)".
func qmPodRows(samples []thanos.Sample, unit string, max int) []qmRow {
	rows := make([]qmRow, 0, len(samples))
	for _, s := range samples {
		pod := s.Labels["pod"]
		if pod == "" {
			pod = "(toplam)"
		}
		rows = append(rows, qmRow{Pod: pod, Namespace: s.Labels["namespace"], Value: round4(s.Value), Unit: unit})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Value != rows[j].Value {
			return rows[i].Value > rows[j].Value
		}
		return rows[i].Pod < rows[j].Pod
	})
	if len(rows) > max {
		rows = rows[:max]
	}
	return rows
}
