package api

// copilot_metric_chart_test.go — v0.10.1386: metric_chart rotası (TR/EN
// pozitifler, fiilsiz negatifler, "kuyruk"), istatistik/sıçrama, takip çipleri
// (kendi başına yönlenir), önbellek anahtarı (tüm girdiler), çip ↔ blok ref
// eşliği, PromQL editörü linki, demet (sahte VM), salt-okur kapısı.

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

var (
	mcServices = []string{"orders-api", "checkout", "payments"}
	mcEnvs     = []string{"prod-eu-a", "prod-eu-b", "uat"}
)

func TestRouteMetricChart(t *testing.T) {
	cases := []struct {
		q, ctxSvc        string
		intent           guidedIntent
		svc, tpl, env    string
		groupBy          string
		envAsked         bool
		askIntent        guidedIntent
		wantChartRouting bool
	}{
		{q: "orders-api'nin JVM heap kullanımı son 6 saat grafik", intent: guidedMetricChart, svc: "orders-api", tpl: mcJVMHeap},
		{q: "checkout pod'larının CPU'sunu çiz", intent: guidedMetricChart, svc: "checkout", tpl: mcCPU},
		{q: "kafka consumer lag trendi", intent: guidedMetricChart, tpl: mcKafkaLag},
		{q: "5xx oranı cluster bazında grafik", intent: guidedMetricChart, tpl: mcHTTP5xx, groupBy: "env"},
		{q: "plot orders-api p99 latency", intent: guidedMetricChart, svc: "orders-api", tpl: mcHTTPP99},
		{q: "checkout p95 gecikme grafiği", intent: guidedMetricChart, svc: "checkout", tpl: mcHTTPP95},
		{q: "orders-api gc grafiği", intent: guidedMetricChart, svc: "orders-api", tpl: mcJVMGC},
		{q: "payments bağlantı havuzu grafiğini çiz", intent: guidedMetricChart, svc: "payments", tpl: mcDBPool},
		{q: "orders-api thread sayısı grafiği", intent: guidedMetricChart, svc: "orders-api", tpl: mcThreads},
		{q: "checkout bellek kullanımını çiz", intent: guidedMetricChart, svc: "checkout", tpl: mcMemory},
		{q: "checkout istek hızı grafiği", intent: guidedMetricChart, svc: "checkout", tpl: mcHTTPRPS},
		{q: "istek hızı grafiği ortam bazında", intent: guidedMetricChart, tpl: mcHTTPRPS, groupBy: "env"},
		{q: "orders-api heap grafiği prod-eu-b ortamında çiz", intent: guidedMetricChart, svc: "orders-api", tpl: mcJVMHeap, env: "prod-eu-b", envAsked: true},
		{q: "chart checkout error rate by cluster", intent: guidedMetricChart, svc: "checkout", tpl: mcHTTP5xx, groupBy: "env"},
		// Servis isteyen şablon, servis yok → ekran servisi ya da "hangisi?".
		{q: "JVM heap grafiği", ctxSvc: "checkout", intent: guidedMetricChart, svc: "checkout", tpl: mcJVMHeap},
		{q: "JVM heap grafiği", intent: guidedAskService, askIntent: guidedMetricChart, tpl: mcJVMHeap},
		// Negatifler — fiil yok: bugünkü yollar.
		// v0.10.1432 — fiilsiz heap sorusu pod_health'ten kaynak incelemesine (jvm ailesi) taşındı.
		{q: "orders-api heap kullanımı nasıl", intent: guidedResourceInvestigation, svc: "orders-api"},
		{q: "checkout db bağlantı havuzu nasıl", intent: guidedDBHealth, svc: "checkout"},
		{q: "orders-api p99 nasıl", intent: guidedServiceHealth, svc: "orders-api"},
	}
	for _, c := range cases {
		r := routeGuidedIntent(c.q, mcServices, mcEnvs, nil, c.ctxSvc)
		if r.Intent != c.intent || r.Service != c.svc {
			t.Errorf("%q → %s/%q, want %s/%q", c.q, r.Intent, r.Service, c.intent, c.svc)
			continue
		}
		if c.intent != guidedMetricChart && c.intent != guidedAskService {
			if r.ChartTemplate != "" {
				t.Errorf("%q: negatif rota şablon taşımamalı: %+v", c.q, r)
			}
			continue
		}
		if r.ChartTemplate != c.tpl || r.ChartGroupBy != c.groupBy || r.Env != c.env || r.ChartEnvAsked != c.envAsked || r.AskIntent != c.askIntent {
			t.Errorf("%q: rota %+v", c.q, r)
		}
		if !hasGuidedSignal(normalizeGuidedMsg(c.q)) {
			t.Errorf("%q: sıfır-maliyet kapısı geçmeli", c.q)
		}
	}
	// "kuyruk" bir sinyal değil (operatör dilinde iş listesi).
	for _, q := range []string{"kuyruk grafiği çiz", "kuyruğu çiz", "gcp grafiği", "grafana nasıl kurulur"} {
		if r := routeGuidedIntent(q, mcServices, mcEnvs, nil, ""); r.Intent == guidedMetricChart || r.ChartTemplate != "" {
			t.Errorf("%q metric_chart'a yönlenmemeli: %+v", q, r)
		}
	}
	// Çift şekli kendi yolunda.
	if r := routeGuidedIntent("orders-api'dan payments'e giden istekler grafiği", mcServices, mcEnvs, nil, ""); r.Intent == guidedMetricChart {
		t.Errorf("çift şekli metric_chart'a düşmemeli: %+v", r)
	}
	// Çok aday → ask_service, çip aynı şablona yönlenir.
	ask := routeGuidedIntent("orders heap grafiği", []string{"orders-api", "orders-worker"}, mcEnvs, nil, "")
	if ask.Intent != guidedAskService || ask.AskIntent != guidedMetricChart || len(ask.ServiceOptions) != 2 {
		t.Fatalf("çok aday: %+v", ask)
	}
	for _, chip := range guidedSuggestions(ask) {
		r := routeGuidedIntent(chip, []string{"orders-api", "orders-worker"}, mcEnvs, nil, "")
		if r.Intent != guidedMetricChart || r.ChartTemplate != mcJVMHeap || r.Service == "" {
			t.Errorf("ask çipi %q → %+v", chip, r)
		}
	}
	// Kapsam (çip tıklaması) ask turunu servise bağlar, şablon korunur.
	sc := applyChatScopeRoute(&chatScope{Services: []string{"orders-api"}}, ask)
	if sc.Intent != guidedMetricChart || sc.Service != "orders-api" || sc.ChartTemplate != mcJVMHeap {
		t.Errorf("kapsam: %+v", sc)
	}
}

func TestMetricChartStats(t *testing.T) {
	mk := func(vs ...float64) [][2]float64 {
		out := make([][2]float64, len(vs))
		for i, v := range vs {
			out[i] = [2]float64{float64(1_700_000_000_000 + int64(i)*60_000), v}
		}
		return out
	}
	cases := []struct {
		name         string
		pts          [][2]float64
		trend, spike string
		max, last    float64
	}{
		{"yatay", mk(10, 10, 10, 10, 10, 10), "flat", "", 10, 10},
		{"yükseliyor", mk(10, 10, 12, 14, 18, 20), "up", "", 20, 20},
		{"düşüyor", mk(20, 20, 15, 12, 10, 10), "down", "", 20, 10},
		{"son değer sıçraması", mk(10, 10, 10, 10, 10, 40), "up", "last", 40, 40},
		{"tepe sıçraması", mk(10, 10, 60, 10, 10, 10), "flat", "max", 60, 10},
		{"sıfırdan yükseliş", mk(0, 0, 0, 0, 5, 5), "up", "last", 5, 5},
		{"tek nokta", mk(7), "flat", "", 7, 7},
	}
	for _, c := range cases {
		st, ok := metricChartStats(c.pts)
		if !ok || st.Trend != c.trend || st.Spike != c.spike || st.Max != c.max || st.Last != c.last {
			t.Errorf("%s: %+v", c.name, st)
		}
	}
	if _, ok := metricChartStats(nil); ok {
		t.Error("boş seri ok=false")
	}
	txt := metricChartStatsTR([]mcptools.EvChartSeries{{Name: "pod-a", Unit: "By", Points: mk(1<<20, 1<<20, 1<<20, 1<<20, 1<<20, 5<<20)}}, "By", time.UTC)
	for _, w := range []string{"GRAFİK ÖZETİ (deterministik; model noktaları yeniden sayma)", "pod-a", "1 MiB", "5 MiB", "SIÇRAMA", "eğilim yükseliyor"} {
		if !strings.Contains(txt, w) {
			t.Errorf("özet %q içermeli:\n%s", w, txt)
		}
	}
	for _, c := range []struct {
		v    float64
		unit string
		want string
	}{
		{1536, "By", "1.5 KiB"}, {3 << 30, "By", "3 GiB"}, {12.345, "%", "12.3%"}, {0.25, "s", "250 ms"}, {2.5, "s", "2.5 s"},
		{1500, "ms", "1.5 s"}, {42, "", "42"}, {12500, "", "12.5k"}, {0.126, "", "0.13"},
	} {
		if got := fmtMetricChartValue(c.v, c.unit); got != c.want {
			t.Errorf("fmt(%v,%q)=%q, want %q", c.v, c.unit, got, c.want)
		}
	}
}

func TestMetricChartSeriesCaps(t *testing.T) {
	var in []chstore.SpanMetricSeries
	for i := 0; i < 25; i++ {
		s := chstore.SpanMetricSeries{GroupKey: []string{`k8s_pod_name="pod-` + string(rune('a'+i)) + `"`}}
		for j := 0; j < 130; j++ {
			s.Points = append(s.Points, chstore.SpanMetricPoint{Time: int64(j) * 60 * 1e9, Value: float64(i)})
		}
		in = append(in, s)
	}
	out, trunc := metricChartSeries(in, "By", "orders-api")
	if !trunc || len(out) != metricChartMaxSeries || len(out[0].Points) != metricChartMaxPoints {
		t.Fatalf("tavan: n=%d pts=%d trunc=%v", len(out), len(out[0].Points), trunc)
	}
	if out[0].Name != "pod-y" || out[0].Points[0][0] != float64(10*60*1000) {
		t.Errorf("tepe değere göre sıralı ve son 120 nokta: %s %v", out[0].Name, out[0].Points[0])
	}
	if got := metricChartSeriesName(nil, "orders-api"); got != "orders-api" {
		t.Errorf("kırılımsız ad: %q", got)
	}
	if got := metricChartSeriesName([]string{`topic="orders"`, `client_id="c1"`}, "x"); got != "orders · c1" {
		t.Errorf("çok etiket: %q", got)
	}
}

func TestMetricChartCacheKeyHashesAllInputs(t *testing.T) {
	from := time.Unix(1_760_000_000, 0)
	to := from.Add(time.Hour)
	base := []any{"vm", mcJVMHeap, "sum(x)", "orders-api", `env="a"`, "", from, to, 30}
	key := func(a []any) string {
		return metricChartCacheKey(a[0].(string), a[1].(string), a[2].(string), a[3].(string), a[4].(string), a[5].(string), a[6].(time.Time), a[7].(time.Time), a[8].(int))
	}
	k0 := key(base)
	if !strings.HasPrefix(k0, "cosre-mchart:v1:") {
		t.Fatalf("önek: %s", k0)
	}
	variants := [][]any{}
	for i := range base {
		v := append([]any(nil), base...)
		switch x := v[i].(type) {
		case string:
			v[i] = x + "z"
		case time.Time:
			v[i] = x.Add(2 * time.Minute)
		case int:
			v[i] = x + 15
		}
		variants = append(variants, v)
	}
	seen := map[string]int{k0: -1}
	for i, v := range variants {
		k := key(v)
		if j, dup := seen[k]; dup {
			t.Errorf("girdi %d değişince anahtar değişmedi (çakışma %d)", i, j)
		}
		seen[k] = i
	}
	// Aynı dakika içinde aynı anahtar (≤60 sn önbellek).
	if key(base) != metricChartCacheKey("vm", mcJVMHeap, "sum(x)", "orders-api", `env="a"`, "", from.Add(10*time.Second), to.Add(10*time.Second), 30) {
		t.Error("dakikaya oturtma")
	}
	// Ayraçlı birleşim: alan sınırı kayması çakışma üretmez.
	if metricChartCacheKey("vm", "ab", "c", "", "", "", from, to, 1) == metricChartCacheKey("vm", "a", "bc", "", "", "", from, to, 1) {
		t.Error("alan sınırı kayması çakıştı")
	}
}

func TestPromQLEditorHrefSafeLink(t *testing.T) {
	q := `topk(20, sum by (k8s_pod_name) ({__name__="jvm_memory_used_bytes", service_name="orders-api"}))`
	href := mcptools.PromQLEditorHref(q, "vm", "", 1_760_000_000_000, 1_760_003_600_000)
	if !strings.HasPrefix(href, "/metrics?") || strings.HasPrefix(href, "//") {
		t.Fatalf("kök-göreli /metrics linki: %s", href)
	}
	u, err := url.Parse(href)
	if err != nil {
		t.Fatal(err)
	}
	v := u.Query()
	if v.Get("promql") != q || v.Get("src") != "vm" || v.Get("editor") != "1" || v.Get("tab") != "promql" || v.Get("range") != "custom:1760000000000-1760003600000" {
		t.Errorf("parametreler: %v", v)
	}
	for _, bad := range []string{
		mcptools.PromQLEditorHref(q, "vm", "", 0, 10),
		mcptools.PromQLEditorHref(q, "vm", "", 10, 10),
		mcptools.PromQLEditorHref(q, "ch", "", 1, 10),
		mcptools.PromQLEditorHref("", "vm", "", 1, 10),
		mcptools.PromQLEditorHref(q, "thanos", "", 1, 10),
	} {
		if bad != "" {
			t.Errorf("bozuk girdi link üretmemeli: %s", bad)
		}
	}
	links := metricChartLinks(guidedRoute{Intent: guidedMetricChart, ChartTemplate: mcCPU, Service: "checkout", ChartHref: href})
	if len(links) != 2 || links[0].Href != href || links[1].Href != "/service?name=checkout&tab=infra" {
		t.Errorf("linkler: %+v", links)
	}
	if l := metricChartLinks(guidedRoute{Intent: guidedMetricChart, ChartTemplate: mcKafkaLag}); len(l) != 1 || l[0].Href != "/metrics" {
		t.Errorf("sorgusuz link: %+v", l)
	}
}

func TestEmitGuidedEvidenceResultRefs(t *testing.T) {
	var mu sync.Mutex
	var events []struct {
		kind string
		p    any
	}
	emit := withStepIDs(func(k string, p any) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, struct {
			kind string
			p    any
		}{k, p})
	})
	emitGuidedContextStep(emit, "bağlam")
	emitGuidedContextStep(emit, "bağlam 2")
	n := emitGuidedStep(emit, "metric_chart", `{}`)
	ev := emitGuidedEvidenceResult(emit, n, "metric_chart", "metin", mcptools.ChatEvidence{Tool: "metric_chart", Href: "/metrics?x=1"},
		&mcptools.EvChartPayload{Title: "t", Series: []mcptools.EvChartSeries{{Name: "a", Points: [][2]float64{{1, 2}}}}})
	if n != 3 || ev.Ref != "ev3" {
		t.Fatalf("kimlik %d ref %q", n, ev.Ref)
	}
	var stepRef, blockRef string
	for _, e := range events {
		switch e.kind {
		case "step-result":
			m := e.p.(map[string]any)
			stepRef = m["evidence"].(mcptools.ChatEvidence).Ref
			if m["href"] != "/metrics?x=1" || m["i"] != 3 {
				t.Errorf("step-result: %+v", m)
			}
		case "ev_block":
			b := e.p.(mcptools.EvidenceBlock)
			if b.Type != "chart" {
				t.Errorf("blok türü %s", b.Type)
			}
			blockRef = b.Payload.(mcptools.EvChartPayload).EvidenceRef
		}
	}
	if stepRef != "ev3" || blockRef != "ev3" {
		t.Errorf("çip ↔ blok ref eşliği: step=%q block=%q", stepRef, blockRef)
	}
	// Kimliksiz emit: hiçbir şey yayınlanmaz.
	silent := 0
	emitGuidedEvidenceResult(func(string, any) { silent++ }, 0, "metric_chart", "x", mcptools.ChatEvidence{}, nil)
	if silent != 0 {
		t.Error("kimliksiz emit yayınladı")
	}
}

func TestMetricChartFollowupsRoute(t *testing.T) {
	r := guidedRoute{Intent: guidedMetricChart, Service: "orders-api", Env: "prod-eu-a", ChartTemplate: mcJVMHeap,
		ChartEnvOptions: []string{"prod-eu-b", "uat"}, ChartRangeS: 3600, ChartHref: "/metrics?editor=1"}
	fu := guidedFollowups(r, nil)
	if len(fu) != guidedFollowupMax {
		t.Fatalf("takip sayısı %d: %+v", len(fu), fu)
	}
	kinds := []string{}
	for _, c := range fu {
		kinds = append(kinds, c.Kind)
		switch c.Kind {
		case fuKindMetricEnv:
			got := routeGuidedIntent(c.Text, mcServices, mcEnvs, nil, "")
			if got.Intent != guidedMetricChart || got.ChartTemplate != mcJVMHeap || got.Service != "orders-api" || got.Env != c.Scope.Env || !got.ChartEnvAsked {
				t.Errorf("ortam çipi %q → %+v", c.Text, got)
			}
			if !strings.HasPrefix(c.Label, "Aynı metriği ") || c.Scope.Services[0] != "orders-api" {
				t.Errorf("ortam çipi etiketi/kapsamı: %+v", c)
			}
		case fuKindMetricWindow:
			norm := normalizeGuidedMsg(c.Text)
			m, ok := detectContextMutation(norm, guidedTokens(norm), ChatContext{LastRoute: &r}, false)
			if !ok || m.Kind != "window" || m.RangeS != 86400 {
				t.Errorf("pencere çipi %q kip değil: %+v", c.Text, m)
			}
			mr, rs, _, ok := applyContextMutation(ChatContext{LastRoute: &r}, m)
			if !ok || rs != 86400 || mr.Intent != guidedMetricChart || mr.ChartTemplate != mcJVMHeap {
				t.Errorf("pencere kipi rotayı klonlamalı: %+v", mr)
			}
		case fuKindSlow:
			if got := routeGuidedIntent(c.Text, mcServices, mcEnvs, nil, ""); got.Intent != guidedSlowTraces || got.Service != "orders-api" {
				t.Errorf("yavaş trace çipi %q → %+v", c.Text, got)
			}
		}
	}
	if strings.Join(kinds, ",") != "metric_env,metric_env,window,slow" {
		t.Errorf("sıra: %v", kinds)
	}
	// 24 sa penceresinde genişlet yok; link çipi sığar.
	r2 := r
	r2.ChartRangeS, r2.ChartEnvOptions = 86400, nil
	fu2 := guidedFollowups(r2, nil)
	if len(fu2) != 2 || fu2[0].Kind != fuKindSlow || fu2[1].Kind != fuKindMetricLink || fu2[1].Href != "/metrics?editor=1" {
		t.Errorf("24 sa takipleri: %+v", fu2)
	}
	// Filo: servis çipi yok, ortam çipi servissiz yönlenir.
	fl := guidedRoute{Intent: guidedMetricChart, ChartTemplate: mcKafkaLag, ChartEnvOptions: []string{"uat"}, ChartRangeS: 3600}
	for _, c := range guidedFollowups(fl, nil) {
		if c.Kind == fuKindMetricEnv {
			if got := routeGuidedIntent(c.Text, mcServices, mcEnvs, nil, ""); got.Intent != guidedMetricChart || got.Env != "uat" || got.Service != "" {
				t.Errorf("filo ortam çipi %q → %+v", c.Text, got)
			}
		}
	}
	if got := metricChartEnvOptions([]string{"prod-eu-a", "prod-eu-b", "uat", "dev"}, "prod-eu-a", 2); strings.Join(got, ",") != "prod-eu-b,uat" {
		t.Errorf("ortam seçenekleri: %v", got)
	}
	for _, s := range guidedSuggestions(r) {
		if got := routeGuidedIntent(s, mcServices, mcEnvs, nil, ""); got.Intent == guidedNone {
			t.Errorf("öneri %q yönlenmiyor", s)
		}
	}
}

// fakeChartReader — v0.10.1386: etiket keşfi + ortam değerleri + range sorgusu.
type fakeChartReader struct {
	lm       vmetrics.LabelMap
	labels   map[string][]string // probe → etiketler
	labelErr error
	envVals  map[string][]string // etiket → HTTP metriğindeki değerler
	series   []chstore.SpanMetricSeries
	queryErr error
	// v0.10.1429 — aile çözücüsünün anlık yoklaması (red_family.go): nil →
	// series (varsa aile var); instantErr → çözüm unknown.
	instant    []chstore.SpanMetricSeries
	instantErr error

	mu       sync.Mutex
	queries  []string
	steps    []int
	instants []string
	// v0.10.1574 — aile çözücüsünün servise kapsanmış etiket keşfi: "<etiket>=<servis>" kaydı.
	scopedLabelCalls []string
	unscopedCalls    int
}

// MetricLabelNamesForServiceIn — v0.10.1574: servise kapsanmış etiket keşfi (aynı labels haritası).
func (f *fakeChartReader) MetricLabelNamesForServiceIn(_ context.Context, metric, svcLabel, service string, _, _ time.Time) ([]string, bool, error) {
	f.mu.Lock()
	f.scopedLabelCalls = append(f.scopedLabelCalls, svcLabel+"="+service)
	f.mu.Unlock()
	if f.labelErr != nil {
		return nil, false, f.labelErr
	}
	return f.labels[metric], false, nil
}

func (f *fakeChartReader) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instants = append(f.instants, q)
	if f.instantErr != nil {
		return nil, f.instantErr
	}
	if f.instant != nil {
		return f.instant, nil
	}
	return f.series, nil
}

func (f *fakeChartReader) MetricLabelNamesIn(_ context.Context, metric string, _, _ time.Time) ([]string, bool, error) {
	f.mu.Lock()
	f.unscopedCalls++
	f.mu.Unlock()
	if f.labelErr != nil {
		return nil, false, f.labelErr
	}
	return f.labels[metric], false, nil
}

func (f *fakeChartReader) MetricLabelValuesIn(_ context.Context, _, label string, _, _ time.Time, _ int) ([]string, bool, error) {
	return f.envVals[label], false, nil
}

func (f *fakeChartReader) MetricLabelMap() vmetrics.LabelMap { return f.lm }

func (f *fakeChartReader) QueryPromQLRange(_ context.Context, q string, _, _ time.Time, step, _ int) ([]chstore.SpanMetricSeries, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	f.steps = append(f.steps, step)
	return f.series, f.queryErr
}

type mcEvents struct {
	mu     sync.Mutex
	kinds  []string
	blocks []mcptools.EvidenceBlock
	steps  []map[string]any
}

func (e *mcEvents) emit() func(string, any) {
	return withStepIDs(func(k string, p any) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.kinds = append(e.kinds, k)
		if b, ok := p.(mcptools.EvidenceBlock); ok {
			e.blocks = append(e.blocks, b)
		}
		if m, ok := p.(map[string]any); ok && k == "step-result" {
			e.steps = append(e.steps, m)
		}
	})
}

func TestMetricChartRunBundle(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 0, 30, 0, time.UTC)
	from := to.Add(-6 * time.Hour)
	pts := func(vs ...float64) []chstore.SpanMetricPoint {
		var out []chstore.SpanMetricPoint
		for i, v := range vs {
			out = append(out, chstore.SpanMetricPoint{Time: from.Add(time.Duration(i) * 3 * time.Minute).UnixNano(), Value: v})
		}
		return out
	}
	rd := &fakeChartReader{
		labels:  map[string][]string{"jvm.memory.used": {"deployment_environment_name", "jvm_memory_type", "k8s_pod_name", "service_name"}},
		envVals: map[string][]string{"deployment_environment_name": {"prod-eu-a", "prod-eu-b"}},
		series: []chstore.SpanMetricSeries{
			{GroupKey: []string{`k8s_pod_name="orders-api-7f9c-a"`}, Points: pts(100<<20, 110<<20, 120<<20, 300<<20)},
			{GroupKey: []string{`k8s_pod_name="orders-api-7f9c-b"`}, Points: pts(90<<20, 90<<20, 90<<20, 90<<20)},
		},
	}
	route := &guidedRoute{Intent: guidedMetricChart, Service: "orders-api", Env: "eu-a", ChartTemplate: mcJVMHeap, ChartEnvAsked: true}
	ev := &mcEvents{}
	s := &Server{}
	text, src, err := s.metricChartRun(context.Background(), ev.emit(), route, rd, true, from, to, 21600, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(rd.queries) != 1 || rd.steps[0] != 180 {
		t.Fatalf("TEK sorgu, adım 180: %v %v", rd.queries, rd.steps)
	}
	q := rd.queries[0]
	for _, w := range []string{"topk(20, sum by (k8s_pod_name)", `service_name="orders-api"`, `deployment_environment_name="prod-eu-a"`, `jvm_memory_type="heap"`} {
		if !strings.Contains(q, w) {
			t.Errorf("sorgu %q içermeli: %s", w, q)
		}
	}
	for _, w := range []string{"METRİK GRAFİĞİ (JVM heap kullanımı", "PromQL: " + q, "GRAFİK ÖZETİ", "orders-api-7f9c-a", "SIÇRAMA", "grafik bloğu YAZMA", "Ortam süzgeci metrikte"} {
		if !strings.Contains(text, w) {
			t.Errorf("kanıt %q içermeli:\n%s", w, text)
		}
	}
	if !strings.Contains(src, "VictoriaMetrics") {
		t.Errorf("kaynak: %s", src)
	}
	if len(ev.blocks) != 1 || len(ev.steps) != 1 {
		t.Fatalf("blok %d, step-result %d", len(ev.blocks), len(ev.steps))
	}
	p := ev.blocks[0].Payload.(mcptools.EvChartPayload)
	ce := ev.steps[0]["evidence"].(mcptools.ChatEvidence)
	if p.EvidenceRef == "" || p.EvidenceRef != ce.Ref || ce.Query != q || ce.QueryKind != "promql" || ce.Source != "VictoriaMetrics" || *ce.Series != 2 || ce.Env != "eu-a" {
		t.Errorf("kanıt/blok: %+v / ref %q", ce, p.EvidenceRef)
	}
	if ce.Window.ToMs != to.Truncate(time.Minute).UnixMilli() || ce.Window.ToMs-ce.Window.FromMs != 6*3600*1000 {
		t.Errorf("pencere dakikaya oturur, uzunluk korunur: %+v", ce.Window)
	}
	if route.ChartHref == "" || route.ChartHref != ce.Href || !strings.HasPrefix(route.ChartHref, "/metrics?") || p.DeepLink != route.ChartHref {
		t.Errorf("href: %q / %q", route.ChartHref, ce.Href)
	}
	if route.ChartRangeS != 21600 {
		t.Errorf("ChartRangeS %d", route.ChartRangeS)
	}

	// Soruda anılan ortam çözülemiyor → grafik YOK, sorgu yok.
	rd2 := &fakeChartReader{labels: rd.labels, envVals: rd.envVals}
	text, _, _ = s.metricChartRun(context.Background(), (&mcEvents{}).emit(), &guidedRoute{Service: "orders-api", Env: "ap-z", ChartTemplate: mcJVMHeap, ChartEnvAsked: true}, rd2, true, from, to, 3600, time.UTC)
	if len(rd2.queries) != 0 || !strings.Contains(text, "grafik YOK") || !strings.Contains(text, "ap-z") {
		t.Errorf("çözülemeyen ortam: sorgu=%v\n%s", rd2.queries, text)
	}
	// Ekrandan devralınan ortam çözülemiyor → süzgeçsiz çizer ve SÖYLER.
	rd3 := &fakeChartReader{labels: rd.labels, envVals: rd.envVals, series: rd.series}
	text, _, _ = s.metricChartRun(context.Background(), (&mcEvents{}).emit(), &guidedRoute{Service: "orders-api", Env: "ap-z", ChartTemplate: mcJVMHeap}, rd3, true, from, to, 3600, time.UTC)
	if len(rd3.queries) != 1 || strings.Contains(rd3.queries[0], "deployment_environment_name") || !strings.Contains(text, "UYGULANMADI") {
		t.Errorf("devralınan ortam: %v\n%s", rd3.queries, text)
	}
	// Metrik yok → denenen yazımlar, sorgu yok.
	rd4 := &fakeChartReader{labels: map[string][]string{}}
	text, _, _ = s.metricChartRun(context.Background(), (&mcEvents{}).emit(), &guidedRoute{Service: "orders-api", ChartTemplate: mcThreads}, rd4, true, from, to, 3600, time.UTC)
	if len(rd4.queries) != 0 || !strings.Contains(text, "BULUNAMADI") || !strings.Contains(text, "jvm_thread_count") {
		t.Errorf("metrik yok:\n%s", text)
	}
	// Etiket okuması hatası ≠ yok.
	rd5 := &fakeChartReader{labelErr: errors.New("vm down")}
	text, _, _ = s.metricChartRun(context.Background(), (&mcEvents{}).emit(), &guidedRoute{Service: "orders-api", ChartTemplate: mcThreads}, rd5, true, from, to, 3600, time.UTC)
	if !strings.Contains(text, "okunamadı ≠ yok") {
		t.Errorf("okuma hatası:\n%s", text)
	}
	// 5xx: durum etiketi yok → dürüst.
	rd6 := &fakeChartReader{labels: map[string][]string{mcHTTPMetric: {"service_name"}}}
	text, _, _ = s.metricChartRun(context.Background(), (&mcEvents{}).emit(), &guidedRoute{Service: "checkout", ChartTemplate: mcHTTP5xx}, rd6, true, from, to, 3600, time.UTC)
	if len(rd6.queries) != 0 || !strings.Contains(text, "durum kodu etiketi yok") {
		t.Errorf("5xx durum etiketi:\n%s", text)
	}
	// Sorgu hatası → step-result ok=false, demet düşmez.
	rd7 := &fakeChartReader{labels: rd.labels, queryErr: errors.New("timeout")}
	e7 := &mcEvents{}
	text, _, err = s.metricChartRun(context.Background(), e7.emit(), &guidedRoute{Service: "orders-api", ChartTemplate: mcJVMHeap}, rd7, true, from, to, 3600, time.UTC)
	if err != nil || !strings.Contains(text, "BAŞARISIZ") || len(e7.steps) != 1 || e7.steps[0]["ok"] != false {
		t.Errorf("sorgu hatası: err=%v steps=%+v\n%s", err, e7.steps, text)
	}
	// Seri yok → kanıt çipi var (sorgu + link), blok yok.
	rd8 := &fakeChartReader{labels: rd.labels}
	e8 := &mcEvents{}
	text, _, _ = s.metricChartRun(context.Background(), e8.emit(), &guidedRoute{Service: "orders-api", ChartTemplate: mcJVMHeap}, rd8, true, from, to, 3600, time.UTC)
	if !strings.Contains(text, "SERİ YOK") || len(e8.blocks) != 0 || len(e8.steps) != 1 || e8.steps[0]["evidence"] == nil {
		t.Errorf("seri yok: blocks=%d steps=%+v\n%s", len(e8.blocks), e8.steps, text)
	}
}

func TestMetricChartNoVMDegrade(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s := &Server{}
	// RED şablonu + servis → span spec-chart çiti.
	text, src, _ := s.metricChartRun(context.Background(), (&mcEvents{}).emit(), &guidedRoute{Service: "checkout", ChartTemplate: mcHTTP5xx}, nil, false, to.Add(-time.Hour), to, 3600, time.UTC)
	if !strings.Contains(text, "```chart") || !strings.Contains(text, `"agg":"error_rate"`) || !strings.Contains(src, "span") {
		t.Errorf("RED düşüşü:\n%s", text)
	}
	if scopes := serverChartScopes(text); len(scopes) == 0 {
		t.Error("sunucu çiti izin listesine girmeli (model aktarabilsin)")
	}
	// RED olmayan → dürüst, çit yok.
	text, _, _ = s.metricChartRun(context.Background(), (&mcEvents{}).emit(), &guidedRoute{Service: "checkout", ChartTemplate: mcJVMHeap}, nil, false, to.Add(-time.Hour), to, 3600, time.UTC)
	if strings.Contains(text, "```chart") || !strings.Contains(text, "VictoriaMetrics yapılandırılmamış") {
		t.Errorf("VM yok heap:\n%s", text)
	}
	// Filo RED (servis yok) → spec-chart servis ister → dürüst.
	text, _, _ = s.metricChartRun(context.Background(), (&mcEvents{}).emit(), &guidedRoute{ChartTemplate: mcHTTP5xx}, nil, false, to.Add(-time.Hour), to, 3600, time.UTC)
	if strings.Contains(text, "```chart") {
		t.Errorf("servissiz spec-chart olmaz:\n%s", text)
	}
}

// TestMetricChartReadOnly — salt-okur: yazma/denetim/oy/model çağrısı yok; tek
// sorgu yolu QueryPromQLRange, etiket keşfi MetricLabelNamesIn.
func TestMetricChartReadOnly(t *testing.T) {
	for _, f := range []string{"copilot_metric_chart.go", "metric_chart_templates.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := stripGoCommentsAPI(string(b))
		for _, w := range []string{"Upsert", "Insert", "Save", "audit", "Vote", "vote", "Feedback", "feedback", "Exec(", "ALTER ", "DELETE",
			"s.store.", "copilotStreamSurface", "copilotExplain", "s.copilot", "http.Post", "NewRequest", "os.Getenv", "label_replace", "/api/v1/admin", "delete_series"} {
			if strings.Contains(src, w) {
				t.Errorf("%s %q içermemeli (salt-okur)", f, w)
			}
		}
	}
	b, _ := os.ReadFile("copilot_metric_chart.go")
	src := stripGoCommentsAPI(string(b))
	if n := strings.Count(src, ".QueryPromQLRange("); n != 1 {
		t.Errorf("TEK range sorgusu çağrı yeri bekleniyordu, %d", n)
	}
	for _, w := range []string{"context.WithTimeout(vmetrics.WithQueryRecorder(ctx, rec), metricChartQueryTimeout)", "metricChartCacheKey(metricSourceVM, t.ID, query, route.Service, envMatcher, route.ChartGroupBy, qFrom, qTo, step)", "s.resolveHTTPEnvScope("} {
		if !strings.Contains(src, w) {
			t.Errorf("demet %q taşımalı", w)
		}
	}
}
