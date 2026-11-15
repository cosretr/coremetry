package mcptools

// v0.10.1271 — CoSRE adım 2: query_metrics / query_traces / query_logs
//
// Tablo testleri: argüman doğrulama/kurtarma, aile → depo yönlendirmesi,
// tavanlar (seri/nokta/limit/pencere), env yayılımı, boş → "veri yok", kanıt
// zarfı alanları, Thanos'ta cluster yokken yapısal hata. Arka uçların hepsi
// sahte; metricsrc yönlendiricisi GERÇEK (Resolver + sahte VM/Thanos).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/metricsrc"
	"github.com/cilcenk/coremetry/internal/thanos"
)

var cqAnchor = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func cqCtx() context.Context { return WithAnchor(context.Background(), cqAnchor) }

func cqRaw(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ── sahte arka uçlar ────────────────────────────────────────────────────

type cqRuntime struct {
	heap, gc []chstore.CapacitySample
	calls    int
}

func (r *cqRuntime) JVMHeapPodUsage(context.Context, time.Time, time.Time) ([]chstore.CapacitySample, error) {
	r.calls++
	return r.heap, nil
}

func (r *cqRuntime) JVMGCPodPause(context.Context, time.Time, time.Time) ([]chstore.CapacitySample, error) {
	r.calls++
	return r.gc, nil
}

type cqVM struct {
	cqRuntime
	configured bool
}

func (v *cqVM) Configured() bool { return v.configured }

type cqThanos struct {
	clusters map[string]thanos.ClusterConfig
	samples  []thanos.Sample
	err      error
	exprs    []string
}

func (f *cqThanos) ClusterByRef(ref string) (thanos.ClusterConfig, bool) {
	c, ok := f.clusters[ref]
	return c, ok
}

func (f *cqThanos) InstantSamples(_ context.Context, _ thanos.ClusterConfig, expr string) ([]thanos.Sample, error) {
	f.exprs = append(f.exprs, expr)
	return f.samples, f.err
}

var cqClusterA = thanos.ClusterConfig{ID: "c-a", Name: "cluster-a", URL: "http://thanos", Enabled: true,
	ThanosLabelName: "cluster", ThanosLabelValue: "prod-a"}

var cqClusterB = thanos.ClusterConfig{ID: "c-b", Name: "cluster-b", URL: "http://thanos", Enabled: true,
	ThanosLabelName: "cluster", ThanosLabelValue: "prod-b"}

type cqEnv struct {
	vm      *cqVM
	ch      *cqRuntime
	th      *cqThanos
	d       Deps
	b       qmBackends
	spanF   []chstore.SpanMetricBatchFilter
	spanRes map[string][]chstore.SpanMetricSeries
	spanErr error
	svcCl   []string
}

func newCQEnv(vmConfigured bool) *cqEnv {
	e := &cqEnv{
		vm: &cqVM{configured: vmConfigured},
		ch: &cqRuntime{},
		th: &cqThanos{clusters: map[string]thanos.ClusterConfig{
			"c-a": cqClusterA, "cluster-a": cqClusterA, "c-b": cqClusterB, "cluster-b": cqClusterB,
		}},
	}
	router := metricsrc.New(metricsrc.Backends{VM: e.vm, CH: e.ch, Thanos: e.th})
	e.d = Deps{MetricRouter: router, Clusters: func() []ClusterRef {
		return []ClusterRef{
			{ID: "c-a", Name: "cluster-a", SpanValues: []string{"ocp-a"}},
			{ID: "c-b", Name: "cluster-b", SpanValues: []string{"ocp-b"}},
		}
	}}
	e.b = qmBackends{
		router: router,
		spanRED: func(_ context.Context, f chstore.SpanMetricBatchFilter) (map[string][]chstore.SpanMetricSeries, int, error) {
			e.spanF = append(e.spanF, f)
			return e.spanRes, 60, e.spanErr
		},
		clusterValues: func(context.Context, string, time.Time, time.Time) ([]string, error) { return e.svcCl, nil },
		podSelector: func(_ context.Context, svc, _ string, _ time.Time) string {
			if svc == "" {
				return ""
			}
			return "^(" + svc + "-7d9f6c5b4-a1b2c)$"
		},
	}
	return e
}

func (e *cqEnv) run(t *testing.T, args map[string]any) cosreResult {
	t.Helper()
	r, err := runQueryMetrics(cqCtx(), e.d, e.b, cqRaw(t, args))
	if err != nil {
		t.Fatalf("runQueryMetrics: %v", err)
	}
	return r
}

func cqSeries(groups, points int) map[string][]chstore.SpanMetricSeries {
	out := map[string][]chstore.SpanMetricSeries{}
	for _, agg := range []string{"rate", "error_rate", "p99", "p50", "p95", "errors"} {
		for g := 0; g < groups; g++ {
			s := chstore.SpanMetricSeries{GroupKey: []string{fmt.Sprintf("g%02d", g)}}
			for p := 0; p < points; p++ {
				s.Points = append(s.Points, chstore.SpanMetricPoint{Time: int64(p) * 60e9, Value: float64(g + 1)})
			}
			out[agg] = append(out[agg], s)
		}
	}
	return out
}

// ── argüman kurtarma ────────────────────────────────────────────────────

func TestCoerceEnum(t *testing.T) {
	cases := []struct {
		in, want string
		warn     bool
	}{
		{"", "red", false},
		{"RED", "red", false},
		{" latency ", "latency", false},
		{"cpu", "pod_cpu", false},
		{"hata", "errors", false},
		{"gecikme", "latency", false},
		{"bogus_family", "red", true},
	}
	for _, c := range cases {
		got, w := coerceEnum("family", c.in, qmFamilies, qmFamilySynonyms, "red")
		if got != c.want || (w != "") != c.warn {
			t.Errorf("coerceEnum(%q) = %q, warn=%q; want %q warn=%v", c.in, got, w, c.want, c.warn)
		}
	}
}

func TestCosreWindow(t *testing.T) {
	toMs := cqAnchor.Add(-time.Hour).UnixMilli()
	cases := []struct {
		name     string
		args     map[string]any
		maxS     int
		wantSpan time.Duration
		wantTo   time.Time
		warn     string
	}{
		{"default 1h", map[string]any{}, 604800, time.Hour, cqAnchor, ""},
		{"preset 6h", map[string]any{"window": "6h"}, 604800, 6 * time.Hour, cqAnchor, ""},
		{"synonym 1d", map[string]any{"window": "1d"}, 604800, 24 * time.Hour, cqAnchor, ""},
		{"unknown preset", map[string]any{"window": "3 weeks"}, 604800, time.Hour, cqAnchor, "bilinmiyor"},
		{"preset over cap", map[string]any{"window": "7d"}, 86400, 24 * time.Hour, cqAnchor, "kırpıldı"},
		{"abs ms", map[string]any{"from_ms": toMs - 1800e3, "to_ms": toMs}, 604800, 30 * time.Minute, time.UnixMilli(toMs).UTC(), ""},
		{"abs camelCase string", map[string]any{"fromMs": fmt.Sprint(toMs - 1800e3), "toMs": fmt.Sprint(toMs)}, 604800, 30 * time.Minute, time.UnixMilli(toMs).UTC(), ""},
		{"abs seconds salvaged", map[string]any{"from_ms": (toMs - 600e3) / 1000, "to_ms": toMs / 1000}, 604800, 10 * time.Minute, time.UnixMilli(toMs).UTC(), "saniye"},
		{"abs over cap", map[string]any{"from_ms": toMs - 30*86400e3, "to_ms": toMs}, 604800, 7 * 24 * time.Hour, time.UnixMilli(toMs).UTC(), "en fazla"},
		{"abs inverted", map[string]any{"from_ms": toMs, "to_ms": toMs - 1000}, 604800, time.Hour, cqAnchor, "geçersiz"},
		{"only from", map[string]any{"from_ms": toMs}, 604800, time.Hour, cqAnchor, "birlikte"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _ := parseCosreArgs(cqRaw(t, c.args))
			from, to, _, warns := cosreWindow(cqCtx(), a, c.maxS, "1h")
			if to.Sub(from) != c.wantSpan || !to.Equal(c.wantTo) {
				t.Errorf("window = [%v, %v] (%v); want span %v ending %v", from, to, to.Sub(from), c.wantSpan, c.wantTo)
			}
			joined := strings.Join(warns, "|")
			if c.warn == "" && joined != "" {
				t.Errorf("unexpected warnings: %s", joined)
			}
			if c.warn != "" && !strings.Contains(joined, c.warn) {
				t.Errorf("warnings %q lack %q", joined, c.warn)
			}
		})
	}
}

// ── yönlendirme ─────────────────────────────────────────────────────────

func TestQueryMetricsFamilyRouting(t *testing.T) {
	cases := []struct {
		family       string
		vmConfigured bool
		wantSource   string
		wantKind     string
	}{
		{"red", false, evSourceCH, evKindSummary},
		{"red", true, evSourceCH, evKindSummary}, // metricRED dikişi bağlı değil → CH span (VM yolu TestQueryMetricsAppREDViaVM)
		{"latency", true, evSourceCH, evKindSummary},
		{"throughput", false, evSourceCH, evKindSummary},
		{"errors", false, evSourceCH, evKindSummary},
		{"jvm_heap", true, evSourceVM, evKindSummary},
		{"jvm_heap", false, evSourceCH, evKindSummary},
		{"jvm_gc", true, evSourceVM, evKindSummary},
		// Operatör (2026-10-17): pod CPU/bellek VM'de olmayabilir; OpenShift
		// Prometheus = Thanos. VM yapılandırılmış olsa da Thanos.
		{"pod_cpu", true, evSourceThanos, evKindPromQL},
		{"pod_cpu", false, evSourceThanos, evKindPromQL},
		{"pod_memory", true, evSourceThanos, evKindPromQL},
		{"pod_restarts", true, evSourceThanos, evKindPromQL},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/vm=%v", c.family, c.vmConfigured), func(t *testing.T) {
			e := newCQEnv(c.vmConfigured)
			e.spanRes = cqSeries(1, 5)
			heap := []chstore.CapacitySample{{Instance: "checkout", Subkey: "checkout-1", Usage: 50, Limit: 100}}
			e.vm.heap, e.vm.gc, e.ch.heap, e.ch.gc = heap, heap, heap, heap
			e.th.samples = []thanos.Sample{{Labels: map[string]string{"namespace": "shop", "pod": "checkout-1"}, Value: 0.5}}
			r := e.run(t, map[string]any{"service": "checkout", "family": c.family, "cluster": "cluster-a"})
			if !r.OK || r.Evidence == nil {
				t.Fatalf("not ok: %+v", r.Error)
			}
			if r.Evidence.Source != c.wantSource || r.Evidence.QueryKind != c.wantKind {
				t.Errorf("source=%s kind=%s; want %s/%s", r.Evidence.Source, r.Evidence.QueryKind, c.wantSource, c.wantKind)
			}
			switch c.wantSource {
			case evSourceThanos:
				if len(e.th.exprs) != 1 || e.vm.calls+e.ch.calls+len(e.spanF) != 0 {
					t.Errorf("pod family must hit ONLY Thanos: thanos=%d vm/ch=%d span=%d", len(e.th.exprs), e.vm.calls+e.ch.calls, len(e.spanF))
				}
			case evSourceVM:
				if e.vm.calls != 1 || e.ch.calls != 0 {
					t.Errorf("want VM runtime read: vm=%d ch=%d", e.vm.calls, e.ch.calls)
				}
			default:
				if c.family == "jvm_heap" && (e.ch.calls != 1 || e.vm.calls != 0) {
					t.Errorf("want CH runtime read: vm=%d ch=%d", e.vm.calls, e.ch.calls)
				}
			}
		})
	}
}

func TestQueryMetricsSpanAggsPerFamily(t *testing.T) {
	want := map[string]string{"red": "rate,error_rate,p99", "latency": "p50,p95,p99", "throughput": "rate", "errors": "errors,error_rate"}
	for fam, aggs := range want {
		e := newCQEnv(false)
		e.spanRes = cqSeries(1, 3)
		e.run(t, map[string]any{"service": "checkout", "family": fam})
		var got []string
		for _, s := range e.spanF[0].Aggs {
			got = append(got, s.Aggregation)
		}
		if strings.Join(got, ",") != aggs {
			t.Errorf("%s aggs = %v, want %s", fam, got, aggs)
		}
	}
}

// ── Thanos cluster zorunluluğu ──────────────────────────────────────────

func TestQueryMetricsThanosClusterRequired(t *testing.T) {
	cases := []struct {
		name       string
		args       map[string]any
		svcCl      []string
		noThanos   bool
		wantOK     bool
		wantCode   string
		wantCands  int
		wantWarn   string
		wantScoped string
	}{
		{"no cluster, span has none", map[string]any{"service": "checkout", "family": "pod_cpu"}, nil, false, false, cosreErrNeedsCluster, 2, "", ""},
		{"resolved from spans", map[string]any{"service": "checkout", "family": "pod_memory"}, []string{"ocp-b"}, false, true, "", 0, "span'lerinden çözüldü", `cluster="prod-b"`},
		{"ambiguous spans", map[string]any{"service": "checkout", "family": "pod_cpu"}, []string{"ocp-a", "ocp-b"}, false, false, cosreErrNeedsCluster, 2, "", ""},
		{"explicit cluster", map[string]any{"service": "checkout", "family": "pod_restarts", "cluster": "cluster-a"}, nil, false, true, "", 0, "", `cluster="prod-a"`},
		{"unknown cluster", map[string]any{"service": "checkout", "family": "pod_cpu", "cluster": "nope"}, nil, false, false, cosreErrNeedsCluster, 2, "", ""},
		{"cluster-wide without service", map[string]any{"family": "pod_cpu", "cluster": "c-a"}, nil, false, true, "", 0, "", `cluster="prod-a"`},
		{"thanos not configured", map[string]any{"service": "checkout", "family": "pod_cpu", "cluster": "cluster-a"}, nil, true, false, cosreErrNotConfigured, 0, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newCQEnv(true)
			e.svcCl = c.svcCl
			e.th.samples = []thanos.Sample{{Labels: map[string]string{"namespace": "shop", "pod": "checkout-1"}, Value: 3}}
			if c.noThanos {
				e.d.Clusters = nil
			}
			r := e.run(t, c.args)
			if r.OK != c.wantOK {
				t.Fatalf("ok=%v want %v (err=%+v)", r.OK, c.wantOK, r.Error)
			}
			if !c.wantOK {
				if r.Error == nil || r.Error.Code != c.wantCode || len(r.Error.Candidates) != c.wantCands {
					t.Fatalf("error = %+v; want code %s with %d candidates", r.Error, c.wantCode, c.wantCands)
				}
				if c.wantCode == cosreErrNeedsCluster && !strings.Contains(r.Error.Message, "cluster gerekli") {
					t.Errorf("message must ask for the cluster: %q", r.Error.Message)
				}
				if e.vm.calls+e.ch.calls != 0 {
					t.Error("pod family fell back to VM/CH — forbidden")
				}
				return
			}
			if !strings.Contains(r.Evidence.Query, c.wantScoped) {
				t.Errorf("evidence query lacks cluster matcher %s: %s", c.wantScoped, r.Evidence.Query)
			}
			if c.wantWarn != "" && !strings.Contains(strings.Join(r.Warnings, "|"), c.wantWarn) {
				t.Errorf("warnings %v lack %q", r.Warnings, c.wantWarn)
			}
		})
	}
}

func TestPodFamilyQueryTemplates(t *testing.T) {
	cases := []struct {
		fam, podRe string
		byPod      bool
		offset     time.Duration
		want       []string
	}{
		{thanos.PodFamilyCPU, "(checkout)-.*", true, 0, []string{"topk(20, sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{", `pod=~"(checkout)-.*"`, "[3600s])"}},
		{thanos.PodFamilyMemory, "", false, 0, []string{"sum(max_over_time(container_memory_working_set_bytes{", `pod!=""`}},
		{thanos.PodFamilyRestarts, "x-.*", true, 2 * time.Hour, []string{"increase(kube_pod_container_status_restarts_total{", "offset 7200s"}},
	}
	for _, c := range cases {
		q, err := thanos.PodFamilyQuery(c.fam, "", c.podRe, time.Hour, c.offset, c.byPod, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range c.want {
			if !strings.Contains(q, w) {
				t.Errorf("%s query %q lacks %q", c.fam, q, w)
			}
		}
	}
	if _, err := thanos.PodFamilyQuery("disk", "", "", time.Hour, 0, true, 20); err == nil {
		t.Error("unknown family must error")
	}
}

// ── tavanlar ────────────────────────────────────────────────────────────

func TestQueryMetricsCaps(t *testing.T) {
	t.Run("span series and points", func(t *testing.T) {
		e := newCQEnv(false)
		e.spanRes = cqSeries(30, 500)
		r := e.run(t, map[string]any{"service": "checkout", "family": "red", "group_by": "route"})
		series := r.Data.(map[string]any)["series"].([]qmSeries)
		if len(series) > mtMaxSeries || len(series) != 18 { // 3 agg × 6 grup
			t.Fatalf("series = %d, want 18 (≤%d)", len(series), mtMaxSeries)
		}
		for _, s := range series {
			if len(s.Points) > mtMaxPoints {
				t.Fatalf("points = %d > %d", len(s.Points), mtMaxPoints)
			}
		}
		if series[0].Group != "g29" { // en büyük alan önce
			t.Errorf("first group = %s, want g29", series[0].Group)
		}
		if !strings.Contains(strings.Join(r.Warnings, "|"), "en büyük") {
			t.Errorf("truncation must be warned: %v", r.Warnings)
		}
		if f := e.spanF[0]; f.MaxDataPoints > mtMaxPoints || len(f.GroupBy) != 1 || f.GroupBy[0] != "http.route" {
			t.Errorf("batch filter = %+v", f)
		}
	})
	t.Run("thanos rows", func(t *testing.T) {
		e := newCQEnv(true)
		for i := 0; i < 40; i++ {
			e.th.samples = append(e.th.samples, thanos.Sample{Labels: map[string]string{"pod": fmt.Sprintf("p%02d", i)}, Value: float64(i)})
		}
		r := e.run(t, map[string]any{"service": "checkout", "family": "pod_cpu", "cluster": "cluster-a"})
		rows := r.Data.(map[string]any)["rows"].([]qmRow)
		if len(rows) != mtMaxSeries || rows[0].Pod != "p39" || *r.Evidence.Rows != mtMaxSeries {
			t.Fatalf("rows=%d first=%s", len(rows), rows[0].Pod)
		}
		if !strings.Contains(e.th.exprs[0], "topk(20,") {
			t.Errorf("thanos query must carry topk(20): %s", e.th.exprs[0])
		}
	})
	t.Run("window clamp 7d", func(t *testing.T) {
		e := newCQEnv(false)
		e.spanRes = cqSeries(1, 3)
		to := cqAnchor.UnixMilli()
		r := e.run(t, map[string]any{"service": "checkout", "from_ms": to - 40*86400e3, "to_ms": to})
		if span := r.Evidence.Window.ToMs - r.Evidence.Window.FromMs; span != 7*86400e3 {
			t.Errorf("window span = %d ms, want 7d", span)
		}
	})
}

// ── env yayılımı + boş + kanıt ──────────────────────────────────────────

func TestQueryMetricsEnvClusterPropagation(t *testing.T) {
	e := newCQEnv(false)
	e.spanRes = cqSeries(2, 4)
	r := e.run(t, map[string]any{"service": "checkout", "env": "prod", "cluster": "cluster-a", "groupBy": "pod"})
	got := filtersSummary(e.spanF[0].Filters)
	for _, w := range []string{"service.name = 'checkout'", "deployment.environment = 'prod'", "cluster = 'ocp-a'"} {
		if !strings.Contains(got, w) {
			t.Errorf("filters %q lack %q", got, w)
		}
	}
	if e.spanF[0].GroupBy[0] != "host.name" {
		t.Errorf("group_by pod → host.name, got %v", e.spanF[0].GroupBy)
	}
	ev := r.Evidence
	if ev.Env != "prod" || ev.Cluster != "cluster-a" || ev.Series == nil || *ev.Series != 6 || ev.DurationMs < 0 {
		t.Errorf("evidence = %+v", ev)
	}
	if ev.Window.ToMs != cqAnchor.UnixMilli() || ev.Window.FromMs != cqAnchor.Add(-time.Hour).UnixMilli() {
		t.Errorf("evidence window = %+v", ev.Window)
	}
	if !strings.Contains(ev.Query, "deployment.environment = 'prod'") {
		t.Errorf("evidence query must show the env filter: %s", ev.Query)
	}
	if r.Request["env"] != "prod" || r.Request["group_by"] != "pod" {
		t.Errorf("request echo = %v", r.Request)
	}
}

func TestQueryMetricsEmptyAndErrors(t *testing.T) {
	e := newCQEnv(false)
	r := e.run(t, map[string]any{"service": "checkout", "family": "latency", "env": "uat"})
	if !r.OK || !r.Empty || !strings.HasPrefix(r.Message, "veri yok: ") || !strings.Contains(r.Message, "checkout") || !strings.Contains(r.Message, "env=uat") {
		t.Fatalf("empty result = %+v", r)
	}
	if r.Data != nil || *r.Evidence.Series != 0 {
		t.Errorf("empty must not carry data: %+v", r.Data)
	}

	r = e.run(t, map[string]any{"family": "red"})
	if r.OK || r.Error == nil || r.Error.Code != cosreErrBadArgs || r.Error.Need[0] != "service" {
		t.Fatalf("missing service = %+v", r.Error)
	}

	e.spanErr = errors.New("code: 159, timeout exceeded")
	r = e.run(t, map[string]any{"service": "checkout"})
	if r.OK || r.Error.Code != cosreErrTimeout || r.Evidence == nil {
		t.Fatalf("backend timeout = %+v", r)
	}

	e.spanErr = nil
	e.spanRes = cqSeries(1, 2)
	r = e.run(t, map[string]any{"service": "checkout", "family": "nonsense", "group_by": "zone"})
	if !r.OK || r.Request["family"] != "red" || r.Request["group_by"] != "none" || len(r.Warnings) != 2 {
		t.Fatalf("coercion = %+v %v", r.Request, r.Warnings)
	}
}

func TestQueryMetricsJVMRows(t *testing.T) {
	e := newCQEnv(true)
	e.vm.heap = []chstore.CapacitySample{
		{Instance: "checkout", Subkey: "c-1", Usage: 40, Limit: 100},
		{Instance: "checkout", Subkey: "c-2", Usage: 90, Limit: 100},
		{Instance: "other", Subkey: "o-1", Usage: 99, Limit: 100},
	}
	r := e.run(t, map[string]any{"service": "checkout", "family": "heap", "env": "prod"})
	rows := r.Data.(map[string]any)["rows"].([]qmRow)
	if len(rows) != 2 || rows[0].Pod != "c-2" || rows[0].LimitPct != 90 {
		t.Fatalf("rows = %+v", rows)
	}
	if !strings.Contains(strings.Join(r.Warnings, "|"), "env JVM") {
		t.Errorf("unapplied env must be warned: %v", r.Warnings)
	}
}

// ── query_traces ────────────────────────────────────────────────────────

type cqTraces struct {
	got     []chstore.TraceFilter
	rows    []chstore.TraceRow
	redEnv  []string
	covers  bool
	listErr error
}

func (c *cqTraces) backends() qtBackends {
	return qtBackends{
		traces: func(_ context.Context, f chstore.TraceFilter) ([]chstore.TraceRow, uint64, bool, error) {
			c.got = append(c.got, f)
			return c.rows, 0, false, c.listErr
		},
		windowRED: func(_ context.Context, _, env string, _, _ time.Time) (chstore.ServiceWindowRED, error) {
			c.redEnv = append(c.redEnv, env)
			return chstore.ServiceWindowRED{Spans: 200, Errors: 10, P99Ms: 812.345678}, nil
		},
		envCovers: func(context.Context, time.Time) bool { return c.covers },
	}
}

func TestQueryTraces(t *testing.T) {
	rows := make([]chstore.TraceRow, 30)
	for i := range rows {
		rows[i] = chstore.TraceRow{TraceID: fmt.Sprintf("%032d", i), ServiceName: "checkout", DurationMs: float64(i), StartTime: 1e15}
	}
	cases := []struct {
		name  string
		args  map[string]any
		rows  []chstore.TraceRow
		check func(t *testing.T, r cosreResult, c *cqTraces)
	}{
		{"limit clamp + defaults", map[string]any{"service": "checkout", "limit": 500}, rows, func(t *testing.T, r cosreResult, c *cqTraces) {
			if c.got[0].Limit != qtMaxLimit || len(r.Data.(map[string]any)["traces"].([]qtTrace)) != qtMaxLimit {
				t.Errorf("limit = %d", c.got[0].Limit)
			}
			if c.got[0].Sort != "time" || c.got[0].HasError {
				t.Errorf("defaults: %+v", c.got[0])
			}
			if !strings.Contains(strings.Join(r.Warnings, "|"), "tavan") {
				t.Errorf("clamp must be warned: %v", r.Warnings)
			}
			if r.Evidence.QueryKind != evKindSQL || !strings.Contains(r.Evidence.Query, "FROM service_summary_5m") ||
				!strings.Contains(r.Evidence.Query, "service_name = 'checkout'") || strings.Contains(r.Evidence.Query, "?") {
				t.Errorf("evidence query = %s", r.Evidence.Query)
			}
			tot := r.Data.(map[string]any)["serviceTotals"].(*qtSummary)
			if tot.ErrorRatePct != 5 || tot.P99Ms != 812.3 {
				t.Errorf("totals = %+v", tot)
			}
		}},
		{"enums + env", map[string]any{"service": "checkout", "status": "hata", "sortBy": "duration", "minDurationMs": "250", "env": "prod"}, rows[:3], func(t *testing.T, r cosreResult, c *cqTraces) {
			f := c.got[0]
			if !f.HasError || f.Sort != "duration" || f.MinMs != 250 || f.Env != "prod" {
				t.Errorf("filter = %+v", f)
			}
			if len(c.redEnv) != 0 {
				t.Errorf("env MV not covering → totals must be skipped, got %v", c.redEnv)
			}
			if r.Evidence.Env != "prod" || *r.Evidence.Rows != 3 {
				t.Errorf("evidence = %+v", r.Evidence)
			}
		}},
		{"unknown enum coerced", map[string]any{"service": "checkout", "status": "weird", "sort_by": "random"}, rows[:1], func(t *testing.T, r cosreResult, c *cqTraces) {
			if r.Request["status"] != "any" || r.Request["sort_by"] != "recent" || len(r.Warnings) != 2 {
				t.Errorf("request=%v warnings=%v", r.Request, r.Warnings)
			}
		}},
		{"operation narrows window", map[string]any{"service": "checkout", "operation": "GET /pay", "window": "24h", "sort_by": "slowest"}, rows[:1], func(t *testing.T, r cosreResult, c *cqTraces) {
			f := c.got[0]
			if f.To.Sub(f.From) > time.Hour || len(f.Filters) != 1 || f.Filters[0].Key != "name" {
				t.Errorf("filter = %+v", f)
			}
			if _, ok := r.Data.(map[string]any)["serviceTotals"]; ok {
				t.Error("operation-scoped call must not return service totals")
			}
		}},
		{"window clamp 24h", map[string]any{"service": "checkout", "window": "7d"}, rows[:1], func(t *testing.T, r cosreResult, c *cqTraces) {
			if c.got[0].To.Sub(c.got[0].From) != 24*time.Hour {
				t.Errorf("window = %v", c.got[0].To.Sub(c.got[0].From))
			}
		}},
		{"empty", map[string]any{"service": "checkout", "status": "error"}, nil, func(t *testing.T, r cosreResult, c *cqTraces) {
			if !r.Empty || !strings.HasPrefix(r.Message, "veri yok: ") || !strings.Contains(r.Message, "hatalı") {
				t.Errorf("empty = %+v", r)
			}
		}},
		{"missing service", map[string]any{"status": "error"}, nil, func(t *testing.T, r cosreResult, c *cqTraces) {
			if r.OK || r.Error.Code != cosreErrBadArgs || len(c.got) != 0 {
				t.Errorf("error = %+v", r.Error)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &cqTraces{rows: tc.rows}
			r, err := runQueryTraces(cqCtx(), c.backends(), cqRaw(t, tc.args))
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, r, c)
		})
	}
	// env + MV kapsıyor → toplam env'li okunur.
	c := &cqTraces{rows: rows[:1], covers: true}
	if _, err := runQueryTraces(cqCtx(), c.backends(), cqRaw(t, map[string]any{"service": "checkout", "env": "uat"})); err != nil {
		t.Fatal(err)
	}
	if len(c.redEnv) != 1 || c.redEnv[0] != "uat" {
		t.Errorf("env totals = %v", c.redEnv)
	}
}

// ── query_logs ──────────────────────────────────────────────────────────

type cqESStore struct{ *stubLogStore }

func (cqESStore) Backend() string { return "elasticsearch" }

func TestSanitizeLogText(t *testing.T) {
	cases := []struct{ in, wantSearch, wantPlain string }{
		{"connection refused", `"connection refused"`, "connection refused"},
		{`level:error" OR service.name:*`, `"level:error OR service.name:*"`, "level:error OR service.name:*"},
		{"a\\b\n\tc", `"a b c"`, "a b c"},
		{`  "" `, "", ""},
		{strings.Repeat("x", 500), `"` + strings.Repeat("x", qlMaxTextRunes) + `"`, strings.Repeat("x", qlMaxTextRunes)},
	}
	for _, c := range cases {
		s, p := sanitizeLogText(c.in)
		if s != c.wantSearch || p != c.wantPlain {
			t.Errorf("sanitize(%q) = %q / %q; want %q / %q", c.in, s, p, c.wantSearch, c.wantPlain)
		}
	}
}

func TestQueryLogs(t *testing.T) {
	logs := make([]*logstore.LogRecord, 60)
	for i := range logs {
		logs[i] = &logstore.LogRecord{Timestamp: int64(i) * 1e9, Severity: 17, Body: "boom " + strings.Repeat("y", 400), ServiceName: "checkout"}
	}
	cases := []struct {
		name  string
		args  map[string]any
		page  *logstore.Page
		es    bool
		check func(t *testing.T, r cosreResult, f logstore.Filter)
	}{
		{"filters + caps + env", map[string]any{"service": "checkout", "severity": "error", "env": "prod", "cluster": "cluster-a", "text": `timeout" OR *`, "limit": 999}, &logstore.Page{Logs: logs}, false, func(t *testing.T, r cosreResult, f logstore.Filter) {
			if f.SeverityMin != qlOTelErrorFloor || f.Env != "prod" || f.Cluster != "cluster-a" || f.Limit != qlMaxLimit || f.Search != `"timeout OR *"` {
				t.Errorf("filter = %+v", f)
			}
			if f.To.Sub(f.From) != time.Hour {
				t.Errorf("default window = %v", f.To.Sub(f.From))
			}
			rows := r.Data.(map[string]any)["logs"].([]qlRow)
			if len(rows) != qlMaxLimit || len([]rune(rows[0].Body)) > qlBodyMaxRunes+1 {
				t.Errorf("rows = %d body=%d", len(rows), len([]rune(rows[0].Body)))
			}
			if r.Evidence.Source != evSourceCH || r.Evidence.Env != "prod" || r.Evidence.Cluster != "cluster-a" || *r.Evidence.Rows != qlMaxLimit {
				t.Errorf("evidence = %+v", r.Evidence)
			}
			if !strings.Contains(r.Evidence.Query, "severity_number>=17") || !strings.Contains(r.Evidence.Query, `body:"timeout OR *"`) {
				t.Errorf("evidence query = %s", r.Evidence.Query)
			}
		}},
		{"es source + unapplied severity", map[string]any{"service": "checkout", "severity": "warning"}, &logstore.Page{Logs: logs[:2], UnappliedFilters: []string{logstore.FilterSeverity}, EnvUnapplied: true}, true, func(t *testing.T, r cosreResult, f logstore.Filter) {
			if f.SeverityMin != qlOTelWarnFloor || r.Evidence.Source != evSourceES {
				t.Errorf("sev=%d source=%s", f.SeverityMin, r.Evidence.Source)
			}
			un, _ := r.Data.(map[string]any)["unapplied"].([]string)
			if strings.Join(un, ",") != "env,severity" && !strings.HasPrefix(strings.Join(un, ","), "env,severity") {
				t.Errorf("unapplied = %v", un)
			}
			if !strings.Contains(strings.Join(r.Warnings, "|"), "severity filtresi uygulanamadı") {
				t.Errorf("warnings = %v", r.Warnings)
			}
		}},
		{"trace id only + window clamp", map[string]any{"traceId": strings.Repeat("AB", 16), "window": "7d"}, &logstore.Page{Logs: logs[:1]}, false, func(t *testing.T, r cosreResult, f logstore.Filter) {
			if f.TraceID != strings.Repeat("ab", 16) || f.To.Sub(f.From) != 24*time.Hour {
				t.Errorf("filter = %+v", f)
			}
		}},
		{"empty", map[string]any{"service": "checkout", "text": "nope"}, &logstore.Page{}, false, func(t *testing.T, r cosreResult, f logstore.Filter) {
			if !r.OK || !r.Empty || !strings.HasPrefix(r.Message, "veri yok: ") || !strings.Contains(r.Message, "metin=nope") {
				t.Errorf("empty = %+v", r)
			}
		}},
		{"missing service and trace", map[string]any{"severity": "error"}, nil, false, func(t *testing.T, r cosreResult, f logstore.Filter) {
			if r.OK || r.Error.Code != cosreErrBadArgs {
				t.Errorf("error = %+v", r.Error)
			}
		}},
		{"bad trace id salvaged", map[string]any{"service": "checkout", "trace_id": "xyz", "severity": "loud"}, &logstore.Page{Logs: logs[:1]}, false, func(t *testing.T, r cosreResult, f logstore.Filter) {
			if f.TraceID != "" || f.SeverityMin != 0 || len(r.Warnings) != 2 {
				t.Errorf("filter=%+v warnings=%v", f, r.Warnings)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubLogStore{page: tc.page}
			var st logstore.Store = stub
			if tc.es {
				st = cqESStore{stub}
			}
			r, err := runQueryLogs(cqCtx(), st, cqRaw(t, tc.args))
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, r, stub.gotFilter)
		})
	}
	r, _ := runQueryLogs(cqCtx(), nil, cqRaw(t, map[string]any{"service": "x"}))
	if r.OK || r.Error.Code != cosreErrNotConfigured {
		t.Errorf("nil store = %+v", r.Error)
	}
}

// ── şema / katalog ──────────────────────────────────────────────────────

func TestCosreToolsSchemaAndEnvelopeOrder(t *testing.T) {
	tools := ToolList(Deps{})
	for _, name := range []string{"query_metrics", "query_traces", "query_logs"} {
		tool := toolByName(t, tools, name)
		props := schemaProps(t, tool)
		for _, p := range []string{"service", "window", "env", "from_ms", "to_ms"} {
			if _, ok := props[p]; !ok {
				t.Errorf("%s: schema lacks %s", name, p)
			}
		}
		if tool.MinRole != "" {
			t.Errorf("%s: read-only tool must be viewer-level", name)
		}
		if !strings.Contains(tool.Description, "evidence") {
			t.Errorf("%s: description must promise the evidence envelope", name)
		}
	}
	// Chat + MCP: ikisinde de var.
	inChat := map[string]bool{}
	for _, tl := range ChatToolList(Deps{}) {
		inChat[tl.Name] = true
	}
	for _, n := range []string{"query_metrics", "query_traces", "query_logs"} {
		if !inChat[n] || chatOnlyTools[n] || externalOnlyTools[n] {
			t.Errorf("%s must be offered to chat AND MCP", n)
		}
	}
	// Zarf sırası: durum önce, veri en sonda (sohbet baştan keser).
	b, _ := json.Marshal(cosreResult{Tool: "x", OK: true, Request: map[string]any{}, Evidence: &cosreEvidence{}, Data: map[string]any{"rows": 1}})
	s := string(b)
	if !(strings.Index(s, `"ok"`) < strings.Index(s, `"evidence"`) && strings.Index(s, `"evidence"`) < strings.Index(s, `"data"`)) {
		t.Errorf("envelope order wrong: %s", s)
	}
}

func TestInlineSQL(t *testing.T) {
	got := inlineSQL("SELECT 1 FROM t WHERE a = ? AND b >= ? AND c = ?", "o'k", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), 7)
	want := `SELECT 1 FROM t WHERE a = 'o\'k' AND b >= '2026-01-02 03:04:05' AND c = 7`
	if got != want {
		t.Errorf("inlineSQL = %q, want %q", got, want)
	}
}

// ── uygulama/HTTP RED: VictoriaMetrics (Overview metrik kipi) ───────────
//
// Coordinator düzeltmesi (2026-10-17): red/latency/throughput/errors VM ana
// metrik deposuyken Overview metrik kipinin okumasından gelir; VM boşsa uyarıyla
// CH span RED'e geçilir, VM hatası backend_error, durum etiketi yoksa hata oranı
// bilinmiyor.

func cqVMRED(rateGroups int, errorsUnknown, unitKnown bool) MetricREDResult {
	ser := cqSeries(rateGroups, 5)
	m := map[string][]chstore.SpanMetricSeries{"rate": ser["rate"], "p50": ser["p50"], "p95": ser["p95"], "p99": ser["p99"]}
	if !errorsUnknown {
		m["error_rate"] = ser["error_rate"]
	}
	return MetricREDResult{Source: "vm", Found: rateGroups > 0, Metric: "http_server_request_duration_seconds_count",
		Series: m, ErrorsUnknown: errorsUnknown, LatencyUnitKnown: unitKnown, EnvApplied: true,
		Queries: []string{`sum(rate(http_server_request_duration_seconds_count{service_name="checkout"}[3m]))`}}
}

func TestQueryMetricsAppREDViaVM(t *testing.T) {
	cases := []struct {
		name        string
		vmConfig    bool
		args        map[string]any
		res         MetricREDResult
		err         error
		wantOK      bool
		wantSource  string
		wantCode    string
		wantWarn    string
		wantSeries  string // virgüllü ad listesi
		wantUnavail string
		wantSpanRd  bool
	}{
		{"vm configured + red → VM", true, map[string]any{"service": "checkout"}, cqVMRED(1, false, true), nil,
			true, evSourceVM, "", "", "rate,error_rate,p99", "", false},
		{"vm empty (no metric) → CH with warning", true, map[string]any{"service": "checkout"}, MetricREDResult{Source: "vm"}, nil,
			true, evSourceCH, "", "VictoriaMetrics → ClickHouse", "", "", true},
		{"vm metric but no points → CH", true, map[string]any{"service": "checkout", "family": "throughput"}, cqVMRED(0, false, true), nil,
			true, evSourceCH, "", "ClickHouse span RED'e geçti", "", "", true},
		{"vm error → backend_error, no fallback", true, map[string]any{"service": "checkout"}, MetricREDResult{Queries: []string{"q1"}}, errors.New("vm 502 bad gateway"),
			false, evSourceVM, cosreErrBackend, "", "", "", false},
		{"no status label → error rate unavailable", true, map[string]any{"service": "checkout", "family": "errors"}, cqVMRED(1, true, true), nil,
			true, evSourceVM, "", "failure rate (durum etiketi yok)", "", "error_rate", false},
		{"red without status label keeps rate + p99", true, map[string]any{"service": "checkout"}, cqVMRED(1, true, true), nil,
			true, evSourceVM, "", "durum etiketi yok", "rate,p99", "error_rate", false},
		{"latency unit unknown → CH (Overview tile falls to span)", true, map[string]any{"service": "checkout", "family": "latency"}, cqVMRED(1, false, false), nil,
			true, evSourceCH, "", "birimi bilinmiyor", "", "", true},
		{"group_by route → CH span (VM mode is service-level)", true, map[string]any{"service": "checkout", "group_by": "route"}, cqVMRED(1, false, true), nil,
			true, evSourceCH, "", "servis düzeyindedir", "", "", true},
		{"vm not configured → CH, VM reader untouched", false, map[string]any{"service": "checkout"}, cqVMRED(1, false, true), nil,
			true, evSourceCH, "", "", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newCQEnv(c.vmConfig)
			e.spanRes = cqSeries(1, 5)
			vmCalls := 0
			e.b.metricRED = func(_ context.Context, service, _ string, _, _ time.Time, mdp int) (MetricREDResult, error) {
				vmCalls++
				if service != "checkout" || mdp > mtMaxPoints {
					t.Errorf("metricRED args service=%q mdp=%d", service, mdp)
				}
				return c.res, c.err
			}
			r := e.run(t, c.args)
			if r.OK != c.wantOK || r.Evidence == nil || r.Evidence.Source != c.wantSource {
				t.Fatalf("ok=%v source=%v err=%+v; want ok=%v source=%s", r.OK, r.Evidence, r.Error, c.wantOK, c.wantSource)
			}
			if c.wantCode != "" && (r.Error == nil || r.Error.Code != c.wantCode) {
				t.Fatalf("error = %+v, want %s", r.Error, c.wantCode)
			}
			if c.wantCode != "" && len(e.spanF) != 0 {
				t.Error("VM error must not fall back to ClickHouse")
			}
			if (len(e.spanF) > 0) != c.wantSpanRd {
				t.Errorf("span read = %d, want %v", len(e.spanF), c.wantSpanRd)
			}
			if !c.vmConfig && vmCalls != 0 {
				t.Error("VM reader called while VM not configured")
			}
			if c.wantWarn != "" && !strings.Contains(strings.Join(r.Warnings, "|"), c.wantWarn) {
				t.Errorf("warnings %v lack %q", r.Warnings, c.wantWarn)
			}
			if c.wantSource == evSourceVM {
				if r.Evidence.QueryKind != evKindPromQL || !strings.Contains(r.Evidence.Query, c.res.Queries[0]) {
					t.Errorf("evidence query = %q", r.Evidence.Query)
				}
			}
			if c.wantSeries != "" {
				var names []string
				for _, s := range r.Data.(map[string]any)["series"].([]qmSeries) {
					names = append(names, s.Name)
				}
				if strings.Join(names, ",") != c.wantSeries {
					t.Errorf("series = %v, want %s", names, c.wantSeries)
				}
			}
			if c.wantUnavail != "" {
				un, _ := r.Data.(map[string]any)["unavailable"].([]string)
				if strings.Join(un, ",") != c.wantUnavail {
					t.Errorf("unavailable = %v, want %s", un, c.wantUnavail)
				}
			}
		})
	}
	// env VM'e taşınır; ifade edilemezse uyarı.
	e := newCQEnv(true)
	var gotEnv string
	res := cqVMRED(1, false, true)
	res.EnvApplied = false
	e.b.metricRED = func(_ context.Context, _, env string, _, _ time.Time, _ int) (MetricREDResult, error) {
		gotEnv = env
		return res, nil
	}
	r := e.run(t, map[string]any{"service": "checkout", "env": "prod"})
	if gotEnv != "prod" || r.Evidence.Env != "prod" || !strings.Contains(strings.Join(r.Warnings, "|"), "TÜM ortamları") {
		t.Errorf("env propagation: got=%q evidence=%q warnings=%v", gotEnv, r.Evidence.Env, r.Warnings)
	}
}

// v0.10.2239 (Entity S4): pod ailesinin seçicisi resolver komşularından; depoda
// pod yoksa Thanos'a cluster geneli gidilmez, dürüst "pod bilinmiyor".
func TestQueryMetricsPodSelectorFromResolver(t *testing.T) {
	e := newCQEnv(false)
	e.th.samples = []thanos.Sample{{Labels: map[string]string{"namespace": "shop", "pod": "checkout-7d9f6c5b4-a1b2c"}, Value: 0.5}}
	r := e.run(t, map[string]any{"service": "checkout", "family": "pod_cpu", "cluster": "cluster-a"})
	if !r.OK || len(e.th.exprs) != 1 || !strings.Contains(e.th.exprs[0], `pod=~"^(checkout-7d9f6c5b4-a1b2c)$"`) {
		t.Fatalf("tam ad seçicisi: ok=%v exprs=%v", r.OK, e.th.exprs)
	}
	e = newCQEnv(false)
	e.b.podSelector = func(context.Context, string, string, time.Time) string { return "" }
	r = e.run(t, map[string]any{"service": "checkout", "family": "pod_cpu", "cluster": "cluster-a"})
	if !r.OK || !r.Empty || len(e.th.exprs) != 0 || !strings.Contains(r.Message, "pod bilinmiyor") {
		t.Fatalf("bilinmiyor → Thanos yok: %+v exprs=%v", r, e.th.exprs)
	}
}
