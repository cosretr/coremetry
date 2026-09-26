package mcptools

// v0.10.944 — query_metric (yükseltilmiş) ve list_metric_labels, gerçek bir
// vmetrics.Service'e bağlı httptest VM stub'ıyla (vmetrics/client_test.go
// emsali). Birim testi yerine TEL testi: kusur sınıfı katmanlar arası kopukluk
// (vmetrics bayrağı çözer → kaynak iletir → araç duruma çevirir) ve her
// katmanın kendi testi yine geçerdi. Sentetik adlar: checkout / payments /
// prod / uat / cluster-a.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// mtVMStub — /api/v1/labels, /api/v1/label/<k>/values, /api/v1/query_range.
type mtVMStub struct {
	mu      sync.Mutex
	labels  []string
	values  []string
	status  int           // ≠0 → her uç bu HTTP koduyla döner
	partial bool          // query_range isPartial
	series  int           // döndürülecek seri sayısı (varsayılan 1)
	staleBy time.Duration // en yeni örnek = end − staleBy
	delay   time.Duration // cevap gecikmesi (zaman aşımı testi)
	queries []string
	calls   int

	// labelsPartial — v0.10.944 — /api/v1/labels ve /api/v1/label/*/values
	// cevaplarında isPartial (vmselect etiket uçlarında da döndürür).
	labelsPartial bool
	// labelsStatus — v0.10.944 — ≠0 → YALNIZ etiket keşfi uçları
	// (/api/v1/labels, /api/v1/label/*/values) bu HTTP koduyla döner.
	labelsStatus int
}

func (s *mtVMStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.delay > 0 {
			select {
			case <-time.After(s.delay):
			case <-r.Context().Done():
				return
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.calls++
		if s.status != 0 {
			w.WriteHeader(s.status)
			_, _ = w.Write([]byte("stub refused"))
			return
		}
		if s.labelsStatus != 0 && (r.URL.Path == "/api/v1/labels" || strings.HasPrefix(r.URL.Path, "/api/v1/label/")) {
			w.WriteHeader(s.labelsStatus)
			_, _ = w.Write([]byte(`{"status":"error","errorType":"422","error":"too many timeseries: -search.maxLabelsAPISeries"}`))
			return
		}
		switch {
		case r.URL.Path == "/api/v1/labels":
			b, _ := json.Marshal(append([]string{"__name__"}, s.labels...))
			_, _ = fmt.Fprintf(w, `{"status":"success","isPartial":%v,"data":%s}`, s.labelsPartial, b)
		case strings.HasPrefix(r.URL.Path, "/api/v1/label/"):
			b, _ := json.Marshal(s.values)
			_, _ = fmt.Fprintf(w, `{"status":"success","isPartial":%v,"data":%s}`, s.labelsPartial, b)
		case r.URL.Path == "/api/v1/query_range":
			s.queries = append(s.queries, r.URL.Query().Get("query"))
			start, _ := strconv.ParseFloat(r.URL.Query().Get("start"), 64)
			end, _ := strconv.ParseFloat(r.URL.Query().Get("end"), 64)
			step, _ := strconv.Atoi(strings.TrimSuffix(r.URL.Query().Get("step"), "s"))
			if step <= 0 {
				step = 60
			}
			n := s.series
			if n == 0 {
				n = 1
			}
			last := end - s.staleBy.Seconds()
			var res []string
			for i := 0; i < n; i++ {
				var vals []string
				for ts := start + float64(step); ts <= last; ts += float64(step) {
					vals = append(vals, fmt.Sprintf(`[%.3f,"%d"]`, ts, i+1))
				}
				res = append(res, fmt.Sprintf(`{"metric":{"k8s_pod_name":"checkout-%02d"},"values":[%s]}`, i, strings.Join(vals, ",")))
			}
			_, _ = fmt.Fprintf(w, `{"status":"success","isPartial":%v,"data":{"resultType":"matrix","result":[%s]}}`,
				s.partial, strings.Join(res, ","))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *mtVMStub) lastQuery() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queries) == 0 {
		return ""
	}
	return s.queries[len(s.queries)-1]
}

func mtVMDeps(t *testing.T, stub *mtVMStub, lm vmetrics.LabelMap) Deps {
	t.Helper()
	vm := vmetrics.New()
	vm.Configure(vmetrics.Settings{Enabled: true, BaseURL: stub.server(t).URL, LabelMap: lm})
	return Deps{Metrics: vm}
}

// mtRun — aracı doğrudan çağırır (ToolList'e entegratör bağlar) ve sonucu
// modelin gördüğü JSON'a çevirip geri okur.
func mtRun(t *testing.T, ctx context.Context, tool mcp.Tool, args string) (map[string]any, error) {
	t.Helper()
	res, err := tool.Handler(ctx, json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("sonuç JSON'a çevrilemedi: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

func mtState(t *testing.T, out map[string]any) (string, map[string]any) {
	t.Helper()
	src, ok := out["source"].(map[string]any)
	if !ok {
		t.Fatalf("source zarfı yok: %v", out)
	}
	st, _ := src["state"].(string)
	return st, src
}

func mtNotes(src map[string]any) string {
	var parts []string
	if ns, ok := src["notes"].([]any); ok {
		for _, n := range ns {
			parts = append(parts, fmt.Sprint(n))
		}
	}
	return strings.Join(parts, " | ")
}

var mtOTLPLabels = []string{"service_name", "k8s_namespace_name", "k8s_pod_name", "http_route"}

// ── Etiket doğrulaması ────────────────────────────────────────────────────

func TestQueryMetricValidatesLabelsAgainstDiscovery(t *testing.T) {
	stub := &mtVMStub{labels: mtOTLPLabels}
	d := mtVMDeps(t, stub, vmetrics.LabelMap{})
	tool := queryMetricInvestigateTool(d)

	// Gerçek etiket (noktalı yazım promLabel'la çözülür) + rol adıyla group_by.
	out, err := mtRun(t, context.Background(), tool,
		`{"name":"http_server_request_duration_seconds_count","service":"checkout","labels":{"http.route":"/pay"},"group_by":"pod","aggregation":"sum"}`)
	if err != nil {
		t.Fatal(err)
	}
	q := stub.lastQuery()
	for _, want := range []string{`service_name="checkout"`, `http_route="/pay"`, `by (k8s_pod_name)`} {
		if !strings.Contains(q, want) {
			t.Fatalf("ifadede %s yok: %s", want, q)
		}
	}
	if st, _ := mtState(t, out); st != "ok" {
		t.Fatalf("state = %s", st)
	}
	if m := out["mapping"].(map[string]any); m["service"] != "service_name (convention)" || m["pod"] != "k8s_pod_name (discovered)" {
		t.Fatalf("mapping: %v", m)
	}
	// v0.10.944 — `_seconds_count` + sum gözlem SAYISIDIR: ailenin "s" birimi
	// sonuca yazılmaz (ResultUnit).
	if u, ok := out["unit"]; ok {
		t.Fatalf("sayı sonucu birim taşıyor: %v", u)
	}
	s0 := out["series"].([]any)[0].(map[string]any)
	if lbl := s0["labels"].(map[string]any); lbl["k8s_pod_name"] != "checkout-00" {
		t.Fatalf("seri etiketleri: %v", lbl)
	}

	// Metrikte olmayan etiket → argüman hatası (bad_args), sorgu GİTMEZ.
	before := len(stub.queries)
	_, err = mtRun(t, context.Background(), tool, `{"name":"m","service":"checkout","labels":{"tenant":"x"}}`)
	if err == nil {
		t.Fatal("bilinmeyen etiket kabul edildi")
	}
	if c := mcp.ClassifyToolError(err).Error; c != mcp.ToolErrBadArgs {
		t.Fatalf("sınıf = %s (%v)", c, err)
	}
	if !strings.Contains(err.Error(), "http_route") {
		t.Fatalf("hata gerçek etiketleri listelemiyor: %v", err)
	}
	if len(stub.queries) != before {
		t.Fatal("doğrulanmamış etiketle sorgu gönderildi")
	}
}

func TestQueryMetricRejectsTraceIDLabels(t *testing.T) {
	stub := &mtVMStub{labels: append([]string{"trace_id"}, mtOTLPLabels...)}
	d := mtVMDeps(t, stub, vmetrics.LabelMap{})
	tool := queryMetricInvestigateTool(d)
	for _, args := range []string{
		`{"name":"m","labels":{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}}`,
		`{"name":"m","labels":{"traceId":"x"}}`,
		`{"name":"m","group_by":["span.id"]}`,
		`{"name":"m","group_by":"otel_trace_id"}`,
	} {
		_, err := mtRun(t, context.Background(), tool, args)
		if err == nil {
			t.Fatalf("%s: trace/span kimliği etiket olarak kabul edildi", args)
		}
		te := mcp.ClassifyToolError(err)
		if te.Error != mcp.ToolErrBadArgs || !strings.Contains(err.Error(), "get_exemplar_traces") {
			t.Fatalf("%s: sınıf=%s metin=%v", args, te.Error, err)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("argüman reddi kaynağa gitti (%d çağrı)", stub.calls)
	}
}

// ── env: LabelMap ile uygulanan vs uygulanamayan (partial) ────────────────

func TestQueryMetricEnvMapping(t *testing.T) {
	t.Run("configured", func(t *testing.T) {
		stub := &mtVMStub{labels: append([]string{"env"}, mtOTLPLabels...)}
		out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{Env: "env"})),
			`{"name":"m","service":"checkout","env":"prod"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stub.lastQuery(), `env="prod"`) {
			t.Fatalf("env süzgeci ifadeye girmedi: %s", stub.lastQuery())
		}
		if m := out["mapping"].(map[string]any); m["env"] != "env (configured)" {
			t.Fatalf("mapping.env = %v", m["env"])
		}
		if st, _ := mtState(t, out); st != "ok" {
			t.Fatalf("state = %s", st)
		}
	})
	t.Run("discovered", func(t *testing.T) {
		stub := &mtVMStub{labels: append([]string{"deployment_environment_name"}, mtOTLPLabels...)}
		out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
			`{"name":"m","service":"checkout","env":"uat"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stub.lastQuery(), `deployment_environment_name="uat"`) {
			t.Fatalf("keşfedilen env etiketi uygulanmadı: %s", stub.lastQuery())
		}
		if m := out["mapping"].(map[string]any); m["env"] != "deployment_environment_name (discovered)" {
			t.Fatalf("mapping.env = %v", m["env"])
		}
	})
	t.Run("unapplied → partial", func(t *testing.T) {
		stub := &mtVMStub{labels: mtOTLPLabels}
		out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
			`{"name":"m","service":"checkout","env":"prod"}`)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stub.lastQuery(), "prod") {
			t.Fatalf("eşlenemeyen env ifadeye sızdı: %s", stub.lastQuery())
		}
		st, src := mtState(t, out)
		if st != "partial" {
			t.Fatalf("state = %s, beklenen partial", st)
		}
		if !strings.Contains(mtNotes(src), "env süzgeci uygulanamadı") {
			t.Fatalf("not yok: %v", src)
		}
		if m := out["mapping"].(map[string]any); m["env"] != "none" {
			t.Fatalf("mapping.env = %v", m["env"])
		}
		for _, a := range out["applied_filters"].([]any) {
			if strings.Contains(fmt.Sprint(a), "prod") {
				t.Fatalf("uygulanmayan süzgeç applied_filters'ta: %v", a)
			}
		}
	})
}

// ── Kaynak durumları: 401, 503, isPartial, tavan, gecikme ─────────────────

func TestQueryMetricSourceStates(t *testing.T) {
	for _, tc := range []struct {
		name string
		stub *mtVMStub
		want string
		flag string
	}{
		{"401 → unauthorized", &mtVMStub{status: http.StatusUnauthorized}, "unauthorized", ""},
		{"403 → unauthorized", &mtVMStub{status: http.StatusForbidden}, "unauthorized", ""},
		{"503 → unreachable", &mtVMStub{status: http.StatusServiceUnavailable}, "unreachable", ""},
		{"isPartial → partial", &mtVMStub{labels: mtOTLPLabels, partial: true}, "partial", "isPartial"},
		{">20 seri → truncated", &mtVMStub{labels: mtOTLPLabels, series: 25}, "truncated", "25 seriden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, tc.stub, vmetrics.LabelMap{})),
				`{"name":"jvm_memory_used_bytes","service":"checkout","group_by":["k8s_pod_name"],"range_s":3600}`)
			if err != nil {
				t.Fatalf("kaynak hatası Go hatası olarak döndü (durum olmalıydı): %v", err)
			}
			st, src := mtState(t, out)
			if st != tc.want {
				t.Fatalf("state = %s, beklenen %s (%v)", st, tc.want, src)
			}
			if src["backend"] != vmetrics.BackendName || src["source"] != "metrics" {
				t.Fatalf("kaynak kimliği: %v", src)
			}
			if tc.flag != "" && !strings.Contains(mtNotes(src), tc.flag) {
				t.Fatalf("not %q yok: %s", tc.flag, mtNotes(src))
			}
		})
	}
	// Tavan ayrıntısı: 20 seri döner, total 25, en büyük alan önce.
	stub := &mtVMStub{labels: mtOTLPLabels, series: 25}
	out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
		`{"name":"jvm_memory_used_bytes","service":"checkout","group_by":["k8s_pod_name"]}`)
	if err != nil {
		t.Fatal(err)
	}
	series := out["series"].([]any)
	if len(series) != mtMaxSeries || out["total_series"].(float64) != 25 {
		t.Fatalf("seri tavanı: len=%d total=%v", len(series), out["total_series"])
	}
	if first := series[0].(map[string]any)["labels"].(map[string]any)["k8s_pod_name"]; first != "checkout-24" {
		t.Fatalf("alan sıralaması: ilk seri %v", first)
	}
}

func TestQueryMetricDelayedDetection(t *testing.T) {
	stale := &mtVMStub{labels: mtOTLPLabels, staleBy: 30 * time.Minute}
	out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stale, vmetrics.LabelMap{})),
		`{"name":"jvm_memory_used_bytes","service":"checkout","range_s":7200,"step_s":60}`)
	if err != nil {
		t.Fatal(err)
	}
	st, src := mtState(t, out)
	if st != "delayed" || !strings.Contains(mtNotes(src), "veri gecikmiş") {
		t.Fatalf("gecikme yakalanmadı: state=%s notes=%s", st, mtNotes(src))
	}
	fresh := &mtVMStub{labels: mtOTLPLabels}
	out, err = mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, fresh, vmetrics.LabelMap{})),
		`{"name":"jvm_memory_used_bytes","service":"checkout","range_s":7200,"step_s":60}`)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := mtState(t, out); st != "ok" {
		t.Fatalf("taze veri gecikmiş sayıldı: %s", st)
	}
	// Geçmişte biten pencere (son 10 dk'da değil) gecikme SAYILMAZ.
	old := &mtVMStub{labels: mtOTLPLabels, staleBy: 30 * time.Minute}
	from := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(-46 * time.Hour).UTC().Format(time.RFC3339)
	out, err = mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, old, vmetrics.LabelMap{})),
		`{"name":"jvm_memory_used_bytes","service":"checkout","from_iso":"`+from+`","to_iso":"`+to+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := mtState(t, out); st == "delayed" {
		t.Fatal("geçmiş pencere gecikmiş sayıldı")
	}
}

// Çıktıdaki her zaman UTC RFC3339 — girdi başka bir ofsette verilse bile.
func TestQueryMetricOutputsUTCISO(t *testing.T) {
	stub := &mtVMStub{labels: mtOTLPLabels}
	out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
		`{"name":"jvm_memory_used_bytes","service":"checkout","from_iso":"2026-09-26T13:00:00+03:00","to_iso":"2026-09-26T14:00:00+03:00"}`)
	if err != nil {
		t.Fatal(err)
	}
	w := out["window"].(map[string]any)
	if w["from_iso"] != "2026-09-26T10:00:00Z" || w["to_iso"] != "2026-09-26T11:00:00Z" {
		t.Fatalf("pencere UTC değil: %v", w)
	}
	_, src := mtState(t, out)
	if src["fromIso"] != "2026-09-26T10:00:00Z" {
		t.Fatalf("source penceresi: %v", src)
	}
	pts := out["series"].([]any)[0].(map[string]any)["points"].([]any)
	if len(pts) == 0 || len(pts) > mtMaxPoints {
		t.Fatalf("nokta sayısı: %d", len(pts))
	}
	for _, p := range pts {
		ts := p.(map[string]any)["t_iso"].(string)
		pt, err := time.Parse(time.RFC3339, ts)
		if err != nil || !strings.HasSuffix(ts, "Z") || pt.Before(time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)) {
			t.Fatalf("t_iso UTC RFC3339 değil ya da pencere dışı: %q", ts)
		}
	}
}

// ── ClickHouse yedeği ve not_configured ──────────────────────────────────

type mtFakeCH struct {
	got    chstore.MetricQueryFilter
	keys   []string
	series []chstore.SpanMetricSeries
	err    error
	// sinces — v0.10.944 — okuyucuların aldığı `since` (MetricAttrKeys,
	// MetricLabelValues sırasıyla): keşfin hangi aralığı okuduğu pinlenir.
	sinces []time.Duration
}

func (f *mtFakeCH) ListMetricNames(context.Context, string, string, int, int) ([]chstore.MetricInfo, int, error) {
	return nil, 0, nil
}

func (f *mtFakeCH) QueryMetric(_ context.Context, q chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
	f.got = q
	return f.series, f.err
}

func (f *mtFakeCH) MetricAttrKeys(_ context.Context, _, _ string, since time.Duration) ([]string, error) {
	f.sinces = append(f.sinces, since)
	return f.keys, nil
}

func (f *mtFakeCH) MetricLabelValues(_ context.Context, _, _ string, since time.Duration, _ string, _ int) ([]string, error) {
	f.sinces = append(f.sinces, since)
	return []string{"prod", "uat"}, nil
}

func (f *mtFakeCH) MetricBackend() string { return "clickhouse" }

func TestQueryMetricClickHouseFallback(t *testing.T) {
	base := time.Now().Add(-10 * time.Minute).Truncate(time.Minute)
	ch := &mtFakeCH{keys: []string{"http.route"}, series: []chstore.SpanMetricSeries{{
		GroupKey: []string{"/pay"},
		Points:   []chstore.SpanMetricPoint{{Time: base.UnixNano(), Value: 1}, {Time: base.Add(time.Minute).UnixNano(), Value: 2}},
	}}}
	out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: ch}),
		`{"name":"http.server.request.duration","service":"checkout","env":"prod","namespace":"payments","group_by":["http.route"],"range_s":900}`)
	if err != nil {
		t.Fatal(err)
	}
	st, src := mtState(t, out)
	if src["backend"] != "clickhouse" || st == "not_configured" {
		t.Fatalf("CH yedeği çalışmadı: %v", src)
	}
	if ch.got.Service != "checkout" {
		t.Fatalf("servis kolon kısayoluna gitmedi: %+v", ch.got)
	}
	keys := map[string]string{}
	for _, f := range ch.got.Filters {
		keys[f.Key] = f.Values[0]
	}
	if keys["deployment.environment"] != "prod" || keys["resource.k8s.namespace.name"] != "payments" {
		t.Fatalf("CH rol anahtarları: %+v", ch.got.Filters)
	}
	if m := out["mapping"].(map[string]any); m["env"] != "deployment.environment (convention)" {
		t.Fatalf("mapping.env = %v", m["env"])
	}
	if s := out["series"].([]any); len(s) != 1 || s[0].(map[string]any)["labels"].(map[string]any)["http.route"] != "/pay" {
		t.Fatalf("seri: %v", s)
	}

	// Kaynak hatası CH yolunda da DURUMdur.
	ch.err = errors.New("dial tcp 10.0.0.1:9000: connect: connection refused")
	out, err = mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: ch}), `{"name":"m"}`)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := mtState(t, out); st != "unreachable" {
		t.Fatalf("state = %s", st)
	}

	// Hiçbir kaynak yok → not_configured (Go hatası değil).
	out, err = mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{}), `{"name":"m"}`)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := mtState(t, out); st != "not_configured" {
		t.Fatalf("state = %s", st)
	}
}

// ── İptal, istek reddi, argüman sınıfları ────────────────────────────────

func TestQueryMetricCancellationStopsImmediately(t *testing.T) {
	stub := &mtVMStub{labels: mtOTLPLabels}
	d := mtVMDeps(t, stub, vmetrics.LabelMap{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := mtRun(t, ctx, queryMetricInvestigateTool(d), `{"name":"m","service":"checkout"}`)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("iptal Go hatası olarak dönmeli: %v", err)
	}
	if len(stub.queries) != 0 {
		t.Fatal("iptalden sonra sorgu gönderildi")
	}
}

// Süre bütçesinin dolması iptal DEĞİLDİR: timeout DURUMU, Go hatası değil.
func TestQueryMetricDeadlineIsTimeoutState(t *testing.T) {
	stub := &mtVMStub{labels: mtOTLPLabels, delay: 2 * time.Second}
	d := mtVMDeps(t, stub, vmetrics.LabelMap{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	out, err := mtRun(t, ctx, queryMetricInvestigateTool(d), `{"name":"m","service":"checkout"}`)
	if err != nil {
		t.Fatalf("zaman aşımı Go hatası olarak döndü: %v", err)
	}
	if st, _ := mtState(t, out); st != "timeout" {
		t.Fatalf("state = %s, beklenen timeout", st)
	}
}

func TestQueryMetricUnfilteredPercentileIsBadArgs(t *testing.T) {
	stub := &mtVMStub{labels: mtOTLPLabels}
	_, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
		`{"name":"http.server.request.duration","aggregation":"p99"}`)
	if err == nil || !errors.Is(err, vmetrics.ErrUnfilteredBuckets) {
		t.Fatalf("filtresiz yüzdelik reddi bekleniyordu: %v", err)
	}
	if c := mcp.ClassifyToolError(err).Error; c != mcp.ToolErrBadArgs {
		t.Fatalf("sınıf = %s", c)
	}
}

func TestQueryMetricArgErrorsAreBadArgs(t *testing.T) {
	tool := queryMetricInvestigateTool(Deps{})
	for _, args := range []string{
		`{}`,
		`{"name":"m","aggregation":"p90"}`,
		`{"name":"m","from_iso":"2026-09-26T10:00:00Z"}`,
		`{"name":"m","from_iso":"dün","to_iso":"2026-09-26T10:00:00Z"}`,
		`{"name":"m","from_iso":"2026-09-01T00:00:00Z","to_iso":"2026-09-26T00:00:00Z"}`,
		`{"name":"m","labels":{"a":"1","b":"1","c":"1","d":"1","e":"1","f":"1","g":"1","h":"1","i":"1"}}`,
		`{"name":"m","labels":{"http_route":""}}`,
		`{"name":"m","group_by":42}`,
	} {
		_, err := mtRun(t, context.Background(), tool, args)
		if err == nil {
			t.Fatalf("%s: hata bekleniyordu", args)
		}
		if c := mcp.ClassifyToolError(err).Error; c != mcp.ToolErrBadArgs {
			t.Fatalf("%s: sınıf = %s (%v)", args, c, err)
		}
	}
}

// ── list_metric_labels ────────────────────────────────────────────────────

func TestListMetricLabelsVM(t *testing.T) {
	stub := &mtVMStub{labels: append([]string{"deployment_environment_name"}, mtOTLPLabels...), values: []string{"payments", "shipping", "inventory"}}
	d := mtVMDeps(t, stub, vmetrics.LabelMap{Service: "service_name"})
	tool := listMetricLabelsTool(d)

	out, err := mtRun(t, context.Background(), tool, `{"name":"jvm_memory_used_bytes"}`)
	if err != nil {
		t.Fatal(err)
	}
	if labels := out["labels"].([]any); len(labels) != 5 {
		t.Fatalf("etiketler: %v", labels)
	}
	roles := out["mapping"].(map[string]any)
	if roles["service"] != "service_name (configured)" || roles["env"] != "deployment_environment_name (discovered)" || roles["version"] != "none" {
		t.Fatalf("roller: %v", roles)
	}
	if st, _ := mtState(t, out); st != "ok" {
		t.Fatalf("state = %s", st)
	}

	// Rol adıyla değer listesi + has_more.
	out, err = mtRun(t, context.Background(), tool, `{"name":"jvm_memory_used_bytes","label":"namespace","limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if out["label"] != "k8s_namespace_name" || len(out["values"].([]any)) != 2 || out["values_has_more"] != true {
		t.Fatalf("değerler: %v", out)
	}
	if st, _ := mtState(t, out); st != "truncated" {
		t.Fatalf("kesik değer listesi state = %s", st)
	}

	// Olmayan etiket → bad_args.
	if _, err := mtRun(t, context.Background(), tool, `{"name":"jvm_memory_used_bytes","label":"tenant"}`); err == nil ||
		mcp.ClassifyToolError(err).Error != mcp.ToolErrBadArgs {
		t.Fatalf("olmayan etiket: %v", err)
	}

	// 401 → unauthorized durumu.
	denied := &mtVMStub{status: http.StatusUnauthorized}
	out, err = mtRun(t, context.Background(), listMetricLabelsTool(mtVMDeps(t, denied, vmetrics.LabelMap{})), `{"name":"m"}`)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := mtState(t, out); st != "unauthorized" {
		t.Fatalf("state = %s", st)
	}
}

func TestListMetricLabelsClickHouseAndUnconfigured(t *testing.T) {
	out, err := mtRun(t, context.Background(), listMetricLabelsTool(Deps{Metrics: &mtFakeCH{keys: []string{"http.route"}}}),
		`{"name":"m","label":"env"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out["label"] != "deployment.environment" || len(out["values"].([]any)) != 2 {
		t.Fatalf("CH değerleri: %v", out)
	}
	if _, src := mtState(t, out); src["backend"] != "clickhouse" {
		t.Fatalf("backend: %v", src)
	}
	out, err = mtRun(t, context.Background(), listMetricLabelsTool(Deps{}), `{"name":"m"}`)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := mtState(t, out); st != "not_configured" {
		t.Fatalf("state = %s", st)
	}
}

// ── SAF yardımcılar ───────────────────────────────────────────────────────

func TestMtIsTraceIDLabel(t *testing.T) {
	for k, want := range map[string]bool{
		"trace_id": true, "traceId": true, "TraceId": true, "trace.id": true, "otel_trace_id": true,
		"span_id": true, "parent_span_id": true, "spanID": true,
		"http_route": false, "trace_state": false, "k8s_pod_name": false, "tracer": false,
	} {
		if got := mtIsTraceIDLabel(k); got != want {
			t.Errorf("%s: %v, beklenen %v", k, got, want)
		}
	}
}

func TestMtDelayed(t *testing.T) {
	end := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	pts := func(lastStart time.Time) []chstore.SpanMetricSeries {
		return []chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{{Time: lastStart.UnixNano(), Value: 1}}}}
	}
	for _, tc := range []struct {
		name   string
		series []chstore.SpanMetricSeries
		step   int
		now    time.Time
		want   bool
	}{
		{"taze (son kova pencere sonunda)", pts(end.Add(-time.Minute)), 60, end, false},
		{"eşikte (120 s taban)", pts(end.Add(-3 * time.Minute)), 60, end, false},
		{"gecikmiş", pts(end.Add(-10 * time.Minute)), 60, end, true},
		{"büyük adımda eşik 2×step", pts(end.Add(-20 * time.Minute)), 600, end, false},
		{"pencere eski (son 10 dk değil)", pts(end.Add(-30 * time.Minute)), 60, end.Add(time.Hour), false},
		{"to_iso gelecekte, veri şimdiye kadar taze", pts(end.Add(-2 * time.Hour).Add(-time.Minute)), 60, end.Add(-2 * time.Hour), false},
		{"duvar saati bilinmiyor", pts(end.Add(-30 * time.Minute)), 60, time.Time{}, false},
		{"seri yok", nil, 60, end, false},
	} {
		if got, _ := mtDelayed(tc.series, end, tc.step, tc.now); got != tc.want {
			t.Errorf("%s: %v, beklenen %v", tc.name, got, tc.want)
		}
	}
	// Bir seri tazeyse gecikme yok ("her serinin" en yenisi eski olmalı).
	mixed := append(pts(end.Add(-30*time.Minute)), pts(end.Add(-time.Minute))...)
	if got, _ := mtDelayed(mixed, end, 60, end); got {
		t.Error("taze seri varken gecikme işaretlendi")
	}
}

// v0.10.944 — taban ceil(pencere/118): adım hizası kenarda en çok 2 kova
// ekler, pencere/120'ye yükseltilen adım 121–122 kova üretip sıradan sorguyu
// "truncated" yapıyordu. Tavan pencere uzunluğu: VM'de step rollup
// penceresidir, büyük step_s tüm retention'ı tarardı.
func TestMtEffectiveStep(t *testing.T) {
	from := time.Unix(0, 0)
	for _, tc := range []struct {
		name          string
		win           time.Duration
		in, want      int
		raised, lower bool
	}{
		{"otomatik", 2 * time.Hour, 0, 0, false, false},
		{"2 sa, 1 s → ceil(7200/118)=62", 2 * time.Hour, 1, 62, true, false},
		{"geçerli adım korunur", 2 * time.Hour, 300, 300, false, false},
		{"1800 s, 15 s → 16 (15 ile 121 kova)", 30 * time.Minute, 15, 16, true, false},
		{"30 dk, dev step_s → pencere uzunluğu", 30 * time.Minute, 100000000, 1800, false, true},
		{"30 dk, step_s = pencere → aynen", 30 * time.Minute, 1800, 1800, false, false},
	} {
		s, r, l := mtEffectiveStep(from, from.Add(tc.win), tc.in)
		if s != tc.want || r != tc.raised || l != tc.lower {
			t.Errorf("%s: got (%d, %v, %v), beklenen (%d, %v, %v)", tc.name, s, r, l, tc.want, tc.raised, tc.lower)
		}
	}
	// Garanti: taban adımla CH (floor(W/s)+2) ve VM (ceil(W/s)+1) kova sayısı ≤ 120.
	for _, w := range []int{900, 1800, 3600, 7200, 86400, 604800} {
		s, _, _ := mtEffectiveStep(from, from.Add(time.Duration(w)*time.Second), 1)
		if ch, vm := w/s+2, (w+s-1)/s+1; ch > mtMaxPoints || vm > mtMaxPoints {
			t.Errorf("pencere %d s, adım %d: CH %d / VM %d kova > %d", w, s, ch, vm, mtMaxPoints)
		}
	}
}

// Kompakt/tam açıklama kapısı (short_desc_test) entegrasyondan ÖNCE burada da
// ölçülür: ToolList'e bağlandığında katalog testi ilk koşuda kırılmasın.
func TestMetricToolsDescriptions(t *testing.T) {
	for _, tl := range []mcp.Tool{queryMetricInvestigateTool(Deps{}), listMetricLabelsTool(Deps{})} {
		n := len(tl.ShortDescription)
		if n < shortDescMinBytes || n > shortDescMaxBytes || n >= len(tl.Description) {
			t.Errorf("%s: kompakt açıklama %d B (sınır %d–%d, tam metin %d B)", tl.Name, n, shortDescMinBytes, shortDescMaxBytes, len(tl.Description))
		}
		if tl.MinRole != "" {
			t.Errorf("%s: MinRole %q — salt-okunur araç viewer düzeyinde olmalı", tl.Name, tl.MinRole)
		}
		if _, ok := tl.InputSchema["properties"].(map[string]any)["env"]; tl.Name == "query_metric" && !ok {
			t.Errorf("query_metric env argümanı taşımıyor")
		}
	}
	env := queryMetricInvestigateTool(Deps{}).InputSchema["properties"].(map[string]any)["env"].(map[string]any)
	if !strings.Contains(env["description"].(string), "uat") {
		t.Error("env açıklaması değer sözlüğünü (uat) öğretmiyor — TestEnvArgAdditive'in withEnv kuralı")
	}
}

// ── v0.10.944 düzeltmeleri ────────────────────────────────────────────────

// mtMarshal — sonucu modele/önizlemeye giden JSON metnine çevirir.
func mtMarshal(t *testing.T, res any) string {
	t.Helper()
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// mtPrefixes — iki tüketicinin gördüğü baş: adım önizlemesi (clipStepPreview,
// 4096 bayt) ve model bütçesi (clampToolResultForModel, 6000 rune).
func mtPrefixes(s string) map[string]string {
	preview := s
	if len(preview) > 4096 {
		preview = preview[:4096]
	}
	model := s
	if r := []rune(model); len(r) > 6000 {
		model = string(r[:6000])
	}
	return map[string]string{"önizleme 4096 B": preview, "model 6000 rune": model}
}

// v0.10.944 — sonuç SIRALI: map[string]any'de json.Marshal anahtarları
// sıralıyordu ve iri `series` dizisi `source`'tan önce yazılıyordu; baştan
// kesen iki tüketici (model 6000 rune, önizleme 4 KB) durum ve notları hiç
// görmüyordu. 20 seri × 120 nokta, iki yoldan.
func TestQueryMetricStatusBeforeBulk(t *testing.T) {
	t.Run("ClickHouse", func(t *testing.T) {
		base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
		var series []chstore.SpanMetricSeries
		for i := 0; i < mtMaxSeries; i++ {
			var pts []chstore.SpanMetricPoint
			for j := 0; j < mtMaxPoints; j++ {
				pts = append(pts, chstore.SpanMetricPoint{Time: base.Add(time.Duration(j) * time.Minute).UnixNano(), Value: 1234.56789 + float64(i*j)})
			}
			series = append(series, chstore.SpanMetricSeries{GroupKey: []string{fmt.Sprintf("checkout-%02d", i)}, Points: pts})
		}
		ch := &mtFakeCH{series: series}
		res, err := queryMetricInvestigateTool(Deps{Metrics: ch}).Handler(context.Background(), json.RawMessage(
			`{"name":"jvm_memory_used_bytes","service":"checkout","group_by":["resource.k8s.pod.name"],"from_iso":"2026-09-20T10:00:00Z","to_iso":"2026-09-20T12:00:00Z"}`))
		if err != nil {
			t.Fatal(err)
		}
		s := mtMarshal(t, res)
		if len(s) < 20000 {
			t.Fatalf("test sonucu yeterince iri değil (%d B)", len(s))
		}
		if i := strings.Index(s, `"source":`); i < 0 || i >= 1024 {
			t.Fatalf(`"source" %d. baytta (≥1024): %.200s`, i, s)
		}
		for name, p := range mtPrefixes(s) {
			if !strings.Contains(p, `"state":"ok"`) || !strings.Contains(p, `"total_series":20`) {
				t.Fatalf("%s durumu taşımıyor", name)
			}
		}
	})
	t.Run("VictoriaMetrics env eşlenemedi + isPartial", func(t *testing.T) {
		stub := &mtVMStub{labels: mtOTLPLabels, series: mtMaxSeries, partial: true}
		res, err := queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})).Handler(context.Background(), json.RawMessage(
			`{"name":"jvm_memory_used_bytes","service":"checkout","env":"prod","group_by":["k8s_pod_name"],"range_s":7200,"step_s":60}`))
		if err != nil {
			t.Fatal(err)
		}
		s := mtMarshal(t, res)
		if len(s) < 20000 {
			t.Fatalf("test sonucu yeterince iri değil (%d B)", len(s))
		}
		if i := strings.Index(s, `"source":`); i < 0 || i >= 1024 {
			t.Fatalf(`"source" %d. baytta (≥1024)`, i)
		}
		for name, p := range mtPrefixes(s) {
			for _, want := range []string{`"state":"partial"`, "env süzgeci uygulanamadı", "isPartial=true"} {
				if !strings.Contains(p, want) {
					t.Fatalf("%s %q taşımıyor", name, want)
				}
			}
		}
		if !strings.HasSuffix(s, "]}") || strings.LastIndex(s, `"series":`) < strings.Index(s, `"source":`) {
			t.Fatal("series en sonda değil")
		}
	})
}

// list_metric_labels — 200 etiket adı + isPartial: durum listeden ÖNCE.
func TestListMetricLabelsStatusBeforeBulk(t *testing.T) {
	var labels []string
	for i := 0; i < mtMaxLabelNames; i++ {
		labels = append(labels, fmt.Sprintf("synthetic_attribute_label_name_%03d", i))
	}
	stub := &mtVMStub{labels: labels, labelsPartial: true}
	res, err := listMetricLabelsTool(mtVMDeps(t, stub, vmetrics.LabelMap{})).Handler(context.Background(), json.RawMessage(`{"name":"jvm_memory_used_bytes"}`))
	if err != nil {
		t.Fatal(err)
	}
	s := mtMarshal(t, res)
	if len(s) < 6000 {
		t.Fatalf("test sonucu yeterince iri değil (%d B)", len(s))
	}
	if i := strings.Index(s, `"source":`); i < 0 || i >= 1024 {
		t.Fatalf(`"source" %d. baytta (≥1024)`, i)
	}
	for name, p := range mtPrefixes(s) {
		for _, want := range []string{`"state":"partial"`, "liste eksik olabilir", `"total":200`} {
			if !strings.Contains(p, want) {
				t.Fatalf("%s %q taşımıyor", name, want)
			}
		}
	}
}

// v0.10.944 — servis bir KİMLİK etiketine (job) eşlenince değer biçimi
// bilinmiyor ("<namespace>/<servis>", ortam eksiz ad): servis→metrik
// eşleyicisinin JobServiceRegex deseni =~ ile uygulanır, tam = DEĞİL.
func TestQueryMetricServiceIdentityLabelRegex(t *testing.T) {
	ksm := []string{"job", "instance", "namespace", "pod"}
	t.Run("job + namespace öneki", func(t *testing.T) {
		stub := &mtVMStub{labels: ksm}
		out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
			`{"name":"http_requests_total","service":"checkout","aggregation":"sum"}`)
		if err != nil {
			t.Fatal(err)
		}
		if m := out["mapping"].(map[string]any); m["service"] != "job (discovered)" {
			t.Fatalf("mapping.service = %v", m["service"])
		}
		q := stub.lastQuery()
		if !strings.Contains(q, `job=~"^(.*/)?(checkout)$"`) || strings.Contains(q, `job="checkout"`) {
			t.Fatalf("kimlik etiketi deseni uygulanmadı: %s", q)
		}
		af := fmt.Sprint(out["applied_filters"])
		if !strings.Contains(af, `job=~"^(.*/)?(checkout)$"`) {
			t.Fatalf("applied_filters =~ biçimini göstermiyor: %s", af)
		}
		st, src := mtState(t, out)
		if st != "ok" || !strings.Contains(mtNotes(src), "değer biçimi doğrulanmadı") {
			t.Fatalf("state=%s notes=%s", st, mtNotes(src))
		}
	})
	t.Run("ortam ekli ad, env yok → eksiz hâl de + partial", func(t *testing.T) {
		stub := &mtVMStub{labels: ksm}
		out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
			`{"name":"http_requests_total","service":"checkout-uat","aggregation":"sum"}`)
		if err != nil {
			t.Fatal(err)
		}
		if q := stub.lastQuery(); !strings.Contains(q, `(checkout-uat|checkout)`) {
			t.Fatalf("eksiz alternatif yok: %s", q)
		}
		st, src := mtState(t, out)
		if st != "partial" || !strings.Contains(mtNotes(src), "ortam eki soyulmuş ad (checkout)") {
			t.Fatalf("state=%s notes=%s", st, mtNotes(src))
		}
	})
	t.Run("labels[job] tam değer desene İKİNCİ koşul olarak eklenir", func(t *testing.T) {
		stub := &mtVMStub{labels: ksm}
		out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
			`{"name":"http_requests_total","service":"checkout","labels":{"job":"shop/checkout"},"aggregation":"sum"}`)
		if err != nil {
			t.Fatal(err)
		}
		q := stub.lastQuery()
		if !strings.Contains(q, `job=~"^(.*/)?(checkout)$"`) || !strings.Contains(q, `job="shop/checkout"`) {
			t.Fatalf("iki koşul birlikte yok (sessizce atlandı): %s", q)
		}
		if n := len(out["applied_filters"].([]any)); n != 2 {
			t.Fatalf("applied_filters: %v", out["applied_filters"])
		}
	})
	t.Run("service_name konvansiyonu tam eşleşme kalır", func(t *testing.T) {
		stub := &mtVMStub{labels: append([]string{"job"}, mtOTLPLabels...)}
		if _, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
			`{"name":"http_requests_total","service":"checkout","aggregation":"sum"}`); err != nil {
			t.Fatal(err)
		}
		if q := stub.lastQuery(); !strings.Contains(q, `service_name="checkout"`) || strings.Contains(q, "=~") {
			t.Fatalf("konvansiyon tam eşleşmesi bozuldu: %s", q)
		}
	})
}

// v0.10.944 — aynı rol için iki aday yazım (deployment_environment_name +
// deployment_environment): yalnız ilki süzülür, sonuç partial ve eşleme her
// yazımı söyler (MetricsQL iki etiket adını VEYA'layamaz).
func TestQueryMetricEnvAlternativesPartial(t *testing.T) {
	stub := &mtVMStub{labels: append([]string{"deployment_environment_name", "deployment_environment"}, mtOTLPLabels...)}
	out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
		`{"name":"m","service":"checkout","env":"prod"}`)
	if err != nil {
		t.Fatal(err)
	}
	if q := stub.lastQuery(); !strings.Contains(q, `deployment_environment_name="prod"`) {
		t.Fatalf("ilk yazım süzülmedi: %s", q)
	}
	st, src := mtState(t, out)
	notes := mtNotes(src)
	if st != "partial" || !strings.Contains(notes, "env birden çok etikette: deployment_environment_name, deployment_environment") {
		t.Fatalf("state=%s notes=%s", st, notes)
	}
	if m := out["mapping"].(map[string]any); m["env"] != "deployment_environment_name (discovered; ayrıca: deployment_environment)" {
		t.Fatalf("mapping.env = %v", m["env"])
	}
	// list_metric_labels aynı eşlemeyi gösterir (mtMappingOf ortak).
	lo, err := mtRun(t, context.Background(), listMetricLabelsTool(mtVMDeps(t, stub, vmetrics.LabelMap{})), `{"name":"m"}`)
	if err != nil {
		t.Fatal(err)
	}
	if m := lo["mapping"].(map[string]any); !strings.Contains(fmt.Sprint(m["env"]), "ayrıca: deployment_environment") {
		t.Fatalf("list_metric_labels mapping.env = %v", m["env"])
	}
}

// v0.10.944 — tam sayı basamakları ASLA kırpılmaz (epoch-saniye gauge'ı,
// büyük sayaç); kesir 6 anlamlı basamağa yuvarlanır.
func TestMtRound(t *testing.T) {
	for _, tc := range []struct {
		in, want float64
	}{
		{1790001234, 1790001234},
		{1790004999, 1790004999},
		{123456789012, 123456789012},
		{12345678, 12345678},
		{-1790001234, -1790001234},
		{0.1234567891, 0.123457},
		{3.14159265, 3.14159},
		{0, 0},
	} {
		if got := mtRound(tc.in); got != tc.want {
			t.Errorf("mtRound(%v) = %v, beklenen %v", tc.in, got, tc.want)
		}
	}
	if mtRound(1790001234) == mtRound(1790004999) {
		t.Error("iki farklı epoch saniyesi aynı değere yuvarlandı")
	}
}

// v0.10.944 — etiket keşfi isPartial: görülmeyen etiket "bu metrikte yok"
// diye REDDEDİLMEZ; doğrulanmadan uygulanır ve sonuç partial. Keşif tamken
// eski ret (bad_args) aynen durur.
func TestQueryMetricLabelDiscoveryPartial(t *testing.T) {
	args := `{"name":"jvm_memory_used_bytes","labels":{"k8s_pod_name":"checkout-1"}}`
	t.Run("isPartial → doğrulanmadan uygulanır", func(t *testing.T) {
		stub := &mtVMStub{labels: []string{"service_name", "http_route"}, labelsPartial: true}
		out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})), args)
		if err != nil {
			t.Fatalf("kısmi keşifte etiket reddedildi: %v", err)
		}
		st, src := mtState(t, out)
		if st != "partial" || !strings.Contains(mtNotes(src), "k8s_pod_name doğrulanamadı, olduğu gibi uygulandı") {
			t.Fatalf("state=%s notes=%s", st, mtNotes(src))
		}
		if af := fmt.Sprint(out["applied_filters"]); !strings.Contains(af, `k8s_pod_name="checkout-1"`) {
			t.Fatalf("applied_filters: %s", af)
		}
		if q := stub.lastQuery(); !strings.Contains(q, `k8s_pod_name="checkout-1"`) {
			t.Fatalf("eşleştirici ifadede yok: %s", q)
		}
	})
	t.Run("isPartial yok → bad_args (regresyon pini)", func(t *testing.T) {
		stub := &mtVMStub{labels: []string{"service_name", "http_route"}}
		_, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})), args)
		if err == nil || mcp.ClassifyToolError(err).Error != mcp.ToolErrBadArgs {
			t.Fatalf("tam keşifte bilinmeyen etiket reddedilmeli: %v", err)
		}
	})
	t.Run("isPartial boş küme → 'seri yok' DEĞİL, servis konvansiyonu", func(t *testing.T) {
		stub := &mtVMStub{labelsPartial: true}
		out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{})),
			`{"name":"m","service":"checkout","env":"prod"}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(stub.queries) == 0 {
			t.Fatal("kısmi boş keşif erken 'boş' döndü; sorgu gitmedi")
		}
		if q := stub.lastQuery(); !strings.Contains(q, `service_name="checkout"`) {
			t.Fatalf("servis konvansiyonu uygulanmadı: %s", q)
		}
		st, src := mtState(t, out)
		if st != "partial" || !strings.Contains(mtNotes(src), "görülmemiş olabilir") {
			t.Fatalf("state=%s notes=%s", st, mtNotes(src))
		}
	})
	t.Run("list_metric_labels: kısmi ad/değer listesi → partial", func(t *testing.T) {
		stub := &mtVMStub{labels: []string{"service_name"}, values: []string{"a", "b"}, labelsPartial: true}
		tool := listMetricLabelsTool(mtVMDeps(t, stub, vmetrics.LabelMap{}))
		out, err := mtRun(t, context.Background(), tool, `{"name":"m"}`)
		if err != nil {
			t.Fatal(err)
		}
		if st, src := mtState(t, out); st != "partial" || strings.Contains(mtNotes(src), "hiç seri taşımıyor") {
			t.Fatalf("ad listesi: state=%s notes=%s", st, mtNotes(src))
		}
		// Kısmi listede görülmeyen etiketin değerleri okunur (ret değil).
		out, err = mtRun(t, context.Background(), tool, `{"name":"m","label":"k8s_pod_name"}`)
		if err != nil {
			t.Fatalf("kısmi listede görülmeyen etiket reddedildi: %v", err)
		}
		if st, _ := mtState(t, out); st != "partial" || out["label"] != "k8s_pod_name" || len(out["values"].([]any)) != 2 {
			t.Fatalf("değer listesi: state=%s out=%v", st, out)
		}
	})
}

// v0.10.944 — CH keşfi duvar saatinden geriye bakar: geçmiş bir pencerede
// etiket KESİN reddedilmez ("doğrulanamadı" notu), canlı pencere katı kalır.
func TestQueryMetricClickHouseHistoricalWindowLenient(t *testing.T) {
	ch := &mtFakeCH{keys: []string{"http.route"}, series: []chstore.SpanMetricSeries{{
		GroupKey: nil, Points: []chstore.SpanMetricPoint{{Time: time.Now().Add(-7 * 24 * time.Hour).UnixNano(), Value: 1}},
	}}}
	to := time.Now().Add(-7 * 24 * time.Hour).UTC().Truncate(time.Second)
	from := to.Add(-time.Hour)
	out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: ch}),
		`{"name":"m","labels":{"old.label":"x"},"from_iso":"`+from.Format(time.RFC3339)+`","to_iso":"`+to.Format(time.RFC3339)+`"}`)
	if err != nil {
		t.Fatalf("geçmiş pencerede etiket kesin reddedildi: %v", err)
	}
	found := false
	for _, f := range ch.got.Filters {
		if f.Key == "old.label" && f.Values[0] == "x" {
			found = true
		}
	}
	if !found {
		t.Fatalf("old.label süzgeci uygulanmadı: %+v", ch.got.Filters)
	}
	if _, src := mtState(t, out); !strings.Contains(mtNotes(src), "doğrulanamadı") {
		t.Fatalf("doğrulanamadı notu yok: %s", mtNotes(src))
	}
	if len(ch.sinces) != 1 || ch.sinces[0] != time.Hour {
		t.Fatalf("geçmiş pencerede keşif since = %v (beklenen 1h taban)", ch.sinces)
	}

	// Canlı pencere (range_s) katı kalır: bilinmeyen etiket bad_args.
	live := &mtFakeCH{keys: []string{"http.route"}}
	_, err = mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: live}),
		`{"name":"m","labels":{"tenant":"x"},"range_s":900}`)
	if err == nil || !strings.Contains(err.Error(), "etiket geçersiz") || mcp.ClassifyToolError(err).Error != mcp.ToolErrBadArgs {
		t.Fatalf("canlı pencerede bilinmeyen etiket reddedilmeli: %v", err)
	}
	if len(live.sinces) != 1 || live.sinces[0] < time.Hour {
		t.Fatalf("canlı keşif since = %v", live.sinces)
	}
}

// mtFakeVM — Go düzeyinde VM kabiliyetleri: sorgu süzgecini YAKALAR (HTTP
// stub'ı MaxDataPoints'i göremez).
type mtFakeVM struct {
	labels []string
	got    chstore.MetricQueryFilter
	points int
	step   int
}

func (f *mtFakeVM) ListMetricNames(context.Context, string, string, int, int) ([]chstore.MetricInfo, int, error) {
	return nil, 0, nil
}

func (f *mtFakeVM) QueryMetric(context.Context, chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
	return nil, nil
}

func (f *mtFakeVM) MetricBackend() string { return vmetrics.BackendName }

func (f *mtFakeVM) MetricLabelNamesIn(context.Context, string, time.Time, time.Time) ([]string, bool, error) {
	return f.labels, false, nil
}

func (f *mtFakeVM) QueryMetricDetailed(_ context.Context, q chstore.MetricQueryFilter) (vmetrics.QueryDetail, error) {
	f.got = q
	var pts []chstore.SpanMetricPoint
	for i := 0; i < f.points; i++ {
		pts = append(pts, chstore.SpanMetricPoint{Time: q.From.Add(time.Duration(i*f.step) * time.Second).UnixNano(), Value: 1})
	}
	return vmetrics.QueryDetail{Series: []chstore.SpanMetricSeries{{Points: pts}}, Step: f.step}, nil
}

// v0.10.944 — adım bütçesi: MaxDataPoints = 118 (hiza payı 2 kova) ve tam
// 120 noktalık seri "truncated" DEĞİL.
func TestQueryMetricStepBudget(t *testing.T) {
	vm := &mtFakeVM{labels: mtOTLPLabels, points: mtMaxPoints, step: 16}
	out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: vm}),
		`{"name":"jvm_memory_used_bytes","service":"checkout","range_s":1800}`)
	if err != nil {
		t.Fatal(err)
	}
	if vm.got.MaxDataPoints != mtMaxPoints-2 {
		t.Fatalf("MaxDataPoints = %d, beklenen %d", vm.got.MaxDataPoints, mtMaxPoints-2)
	}
	if st, src := mtState(t, out); st != "ok" {
		t.Fatalf("120 noktalık seri state = %s (%s)", st, mtNotes(src))
	}
	// CH yolu da aynı bütçeyi taşır.
	ch := &mtFakeCH{}
	if _, err := mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: ch}), `{"name":"m","range_s":1800}`); err != nil {
		t.Fatal(err)
	}
	if ch.got.MaxDataPoints != mtStepBudget {
		t.Fatalf("CH MaxDataPoints = %d", ch.got.MaxDataPoints)
	}
	// Pencereden büyük step_s pencereye iner ve not düşülür.
	vm2 := &mtFakeVM{labels: mtOTLPLabels, points: 1, step: 1800}
	out, err = mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: vm2}),
		`{"name":"jvm_memory_used_bytes","service":"checkout","range_s":1800,"step_s":100000000}`)
	if err != nil {
		t.Fatal(err)
	}
	if vm2.got.StepSeconds != 1800 {
		t.Fatalf("step_s pencereye inmedi: %d", vm2.got.StepSeconds)
	}
	if _, src := mtState(t, out); !strings.Contains(mtNotes(src), "step_s pencere uzunluğuna (1800 s) indirildi") {
		t.Fatalf("indirme notu yok: %s", mtNotes(src))
	}
}

// v0.10.944 — CH list_metric_labels: okuyucular "şimdi"ye göreli; çıpalı
// pencerede okuma çıpalı başlangıçtan şimdiye genişler ve window OKUNAN
// aralığı söyler (çıpalı pencere ayrıca anchored_window).
func TestListMetricLabelsClickHouseAnchoredWidening(t *testing.T) {
	ch := &mtFakeCH{keys: []string{"http.route"}}
	ctx := WithAnchor(context.Background(), time.Now().Add(-72*time.Hour))
	out, err := mtRun(t, ctx, listMetricLabelsTool(Deps{Metrics: ch}), `{"name":"m","label":"pod"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.sinces) != 2 {
		t.Fatalf("okuyucu çağrıları: %v", ch.sinces)
	}
	for _, s := range ch.sinces {
		if s < 96*time.Hour {
			t.Fatalf("okuma çıpalı pencereyi kapsamıyor: since = %v", s)
		}
	}
	w := out["window"].(map[string]any)
	toISO, err := time.Parse(time.RFC3339, w["to_iso"].(string))
	if err != nil || time.Since(toISO) > time.Minute || time.Until(toISO) > time.Minute {
		t.Fatalf("window.to_iso şimdi değil: %v", w)
	}
	if _, ok := out["anchored_window"].(map[string]any); !ok {
		t.Fatalf("anchored_window yok: %v", out)
	}
	st, src := mtState(t, out)
	if !strings.Contains(mtNotes(src), "şimdiye genişletildi") {
		t.Fatalf("genişletme notu yok: %s", mtNotes(src))
	}
	if st == "partial" {
		t.Fatalf("7 günlük tavan içinde partial: %s", mtNotes(src))
	}
	if src["toIso"] != w["to_iso"] {
		t.Fatalf("source penceresi okunan aralık değil: %v / %v", src["toIso"], w["to_iso"])
	}

	// 7 günden eski çıpa: okuma tavanda kalır → partial.
	old := &mtFakeCH{keys: []string{"http.route"}}
	out, err = mtRun(t, WithAnchor(context.Background(), time.Now().Add(-10*24*time.Hour)), listMetricLabelsTool(Deps{Metrics: old}), `{"name":"m"}`)
	if err != nil {
		t.Fatal(err)
	}
	if st, src := mtState(t, out); st != "partial" || !strings.Contains(mtNotes(src), "7 günden eski") {
		t.Fatalf("kapsam dışı çıpa: state=%s notes=%s", st, mtNotes(src))
	}
	if old.sinces[0] != mtMaxWindow {
		t.Fatalf("since tavanı: %v", old.sinces[0])
	}

	// Çıpasız 7 g pencere: ms taşması partial üretmez.
	full := &mtFakeCH{keys: []string{"http.route"}}
	out, err = mtRun(t, context.Background(), listMetricLabelsTool(Deps{Metrics: full}), `{"name":"m","range_s":604800}`)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := mtState(t, out); st == "partial" {
		t.Fatal("çıpasız 7 g pencere partial okundu")
	}
	if _, ok := out["anchored_window"]; ok {
		t.Fatal("çıpasız çağrıda anchored_window var")
	}
}

// Kaynak durumu sözlüğü sourcestate ile aynı (ayrı bir yazım yok).
var _ = sourcestate.Partial

// TestQueryMetricCHAcceptsColumnKeys — v0.10.944 regresyonu: ClickHouse metrik
// yolunda metric_points'in kolon-destekli anahtarları (host.name, service.name,
// deployment.environment[.name]) keşif listesinde görünmez; strict modda
// bad_args alıyordu. Kabul edilir, eşanlamlılar kanonik anahtara iner; uydurma
// etiket yine reddedilir.
func TestQueryMetricCHAcceptsColumnKeys(t *testing.T) {
	base := time.Now().Add(-10 * time.Minute).Truncate(time.Minute)
	newCH := func() *mtFakeCH {
		return &mtFakeCH{keys: []string{"http.route"}, series: []chstore.SpanMetricSeries{{
			GroupKey: []string{"node-a"},
			Points:   []chstore.SpanMetricPoint{{Time: base.UnixNano(), Value: 1}},
		}}}
	}
	ch := newCH()
	out, err := mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: ch}),
		`{"name":"system.cpu.utilization","group_by":["host.name"],"labels":{"host.name":"node-a"},"range_s":900}`)
	if err != nil {
		t.Fatalf("host.name reddedilmemeli: %v", err)
	}
	if fmt.Sprint(ch.got.GroupBy) != "[host.name]" || len(ch.got.Filters) != 1 || ch.got.Filters[0].Key != "host.name" || ch.got.Filters[0].Values[0] != "node-a" {
		t.Fatalf("host.name gruplama/süzgeç: %+v", ch.got)
	}
	if st, src := mtState(t, out); st == "error" || strings.Contains(fmt.Sprint(src["notes"]), "görülmedi") {
		t.Fatalf("kolon anahtarı 'görülmedi' notu almamalı: %s %v", st, src["notes"])
	}
	ch = newCH()
	if _, err := mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: ch}),
		`{"name":"system.cpu.utilization","labels":{"service.name":"checkout","deployment.environment.name":"prod"},"range_s":900}`); err != nil {
		t.Fatal(err)
	}
	if ch.got.Service != "checkout" || len(ch.got.Filters) != 1 || ch.got.Filters[0].Key != "deployment.environment" {
		t.Fatalf("service.name → Service, env eşanlamlısı kanonik: %+v", ch.got)
	}
	// Eşanlamlı çakışması sessiz boş sonuç değil, hata.
	_, err = mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: newCH()}),
		`{"name":"system.cpu.utilization","service":"checkout","labels":{"service.name":"payments"},"range_s":900}`)
	if err == nil || !strings.Contains(err.Error(), "aynı anahtar (service_name)") {
		t.Fatalf("service + labels{service.name} çakışması hata olmalı: %v", err)
	}
	// Uydurma etiket strict modda yine reddedilir; mesaj kolon anahtarlarını söyler.
	_, err = mtRun(t, context.Background(), queryMetricInvestigateTool(Deps{Metrics: newCH()}),
		`{"name":"system.cpu.utilization","labels":{"foo.bar":"x"},"range_s":900}`)
	if err == nil || mcp.ClassifyToolError(err).Error != mcp.ToolErrBadArgs || !strings.Contains(err.Error(), "host.name") {
		t.Fatalf("foo.bar reddedilmeli (bad_args, kolon anahtarları ilanlı): %v", err)
	}
	// list_metric_labels CH'de kolon anahtarlarını ilan eder.
	lo, err := mtRun(t, context.Background(), listMetricLabelsTool(Deps{Metrics: newCH()}), `{"name":"system.cpu.utilization"}`)
	if err != nil {
		t.Fatal(err)
	}
	if ck := fmt.Sprint(lo["column_keys"]); !strings.Contains(ck, "host.name") || !strings.Contains(ck, "service.name") {
		t.Fatalf("column_keys: %v", lo["column_keys"])
	}
}

// TestQueryMetricVMDiscoveryFailureDegrades — v0.10.944 regresyonu: etiket
// keşfinin hatası (labels-API seri limiti, 422) query_range'i engellemez;
// eşleme konvansiyonla yapılır, sonuç kısmi ve not "keşif başarısız" der.
func TestQueryMetricVMDiscoveryFailureDegrades(t *testing.T) {
	stub := &mtVMStub{labels: mtOTLPLabels, labelsStatus: http.StatusUnprocessableEntity, series: 2}
	tool := queryMetricInvestigateTool(mtVMDeps(t, stub, vmetrics.LabelMap{}))
	out, err := mtRun(t, context.Background(), tool, `{"name":"process_cpu_seconds_total","range_s":900}`)
	if err != nil {
		t.Fatal(err)
	}
	st, src := mtState(t, out)
	if st != "partial" || len(stub.queries) != 1 {
		t.Fatalf("keşif hatası sorguyu engellememeli: state=%s query_range=%d", st, len(stub.queries))
	}
	if c, _ := out["count"].(float64); c <= 0 {
		t.Fatalf("seri dönmeli: %v", out["count"])
	}
	n := mtNotes(src)
	if !strings.Contains(n, "etiket keşfi başarısız") || strings.Contains(n, "isPartial") {
		t.Fatalf("not: %s", n)
	}
	// Servis konvansiyonla eşlenir; doğrulanmamış group_by uygulanır ve adı notta.
	out, err = mtRun(t, context.Background(), tool, `{"name":"process_cpu_seconds_total","service":"checkout","group_by":["k8s.pod.name"],"range_s":900}`)
	if err != nil {
		t.Fatal(err)
	}
	if q := stub.lastQuery(); !strings.Contains(q, `service_name="checkout"`) || !strings.Contains(q, "k8s_pod_name") {
		t.Fatalf("sorgu: %s", q)
	}
	if m := out["mapping"].(map[string]any); m["service"] != "service_name (convention)" {
		t.Fatalf("mapping.service = %v", m["service"])
	}
	_, src = mtState(t, out)
	if n := mtNotes(src); !strings.Contains(n, "etiket keşfi başarısız") || !strings.Contains(n, "k8s_pod_name doğrulanmadan uygulandı") {
		t.Fatalf("doğrulanmamış group_by notu: %s", n)
	}
}
