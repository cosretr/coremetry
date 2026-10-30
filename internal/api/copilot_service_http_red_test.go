package api

// copilot_service_http_red_test.go — v0.10.1391. Operatör (prod, "… sağlığı
// nasıl?" cevabı): "Prod sağlığı güzel ama VictoriaMetrics getirmedi, en
// azından HTTP metriclerimi getirebilirdi. Onun yerine gRPC getirdi. Aslında
// metrikler OTel collector'dan geliyor."
//
// Pinler: grafik kaynağı seçimi (VM var → HTTP RED blokları; VM yok / histogram
// yok / okunamadı → span yedeği + dürüst not), üç PromQL (şablonlardan, bayt
// bayt), önbellek anahtarı tüm girdileri hash'ler, kanıt + blok eşliği, demet
// kablolaması (VM'de span çiti ve tarama operasyon grafiği YOK), salt-okur.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestPickServiceChartSource(t *testing.T) {
	ok := svcHTTPREDProbe{VM: true, MetricPresent: true, SvcLabelOK: true, RateSeries: 1}
	with := func(f func(*svcHTTPREDProbe)) svcHTTPREDProbe { p := ok; f(&p); return p }
	cases := []struct {
		name   string
		p      svcHTTPREDProbe
		useVM  bool
		reason string
	}{
		{"VM var + histogram + servis serisi → VM HTTP RED", ok, true, ""},
		{"VM yapılandırılmamış → span", svcHTTPREDProbe{}, false, "VictoriaMetrics yapılandırılmamış"},
		{"etiket okuması hatası ≠ yok", with(func(p *svcHTTPREDProbe) { p.LabelErr = errors.New("vm down") }), false, "okunamadı ≠ yok"},
		{"histogram yok → span", with(func(p *svcHTTPREDProbe) { p.MetricPresent = false }), false, "VictoriaMetrics'te bulunamadı"},
		{"servis etiketi yok → span", with(func(p *svcHTTPREDProbe) { p.SvcLabelOK = false }), false, "servis etiketi bulunamadı"},
		{"istek hızı sorgusu hatası ≠ yok", with(func(p *svcHTTPREDProbe) { p.RateErr = errors.New("timeout") }), false, "sorgusu BAŞARISIZ (okunamadı ≠ yok)"},
		{"servisin serisi yok (gRPC-only servis) → span", with(func(p *svcHTTPREDProbe) { p.RateSeries = 0 }), false, "bu servisin HTTP sunucu histogramı serisi bu pencerede yok"},
	}
	for _, c := range cases {
		useVM, reason := pickServiceChartSource(c.p)
		if useVM != c.useVM || (c.reason == "" && reason != "") || !strings.Contains(reason, c.reason) {
			t.Errorf("%s: got (%v, %q)", c.name, useVM, reason)
		}
	}
}

// TestServiceHTTPREDPromQLGolden — üç grafik, şablonlardan, bayt bayt.
func TestServiceHTTPREDPromQLGolden(t *testing.T) {
	cnt, bkt := mcSel("count", mcHTTPMetric), mcSel("bucket", mcHTTPMetric)
	sel := `service_name="orders-api"`
	got := svcHTTPREDQueries("orders-api", "service_name", "", "http_response_status_code", 60)
	want := []svcHTTPREDQuery{
		{Kind: svcHTTPREDRate, Title: "orders-api · HTTP istek hızı · VictoriaMetrics (örneklenmemiş)", Unit: "req/s", Name: "req/s",
			Query: `sum (rate({` + cnt + `, ` + sel + `}[60s]))`},
		{Kind: svcHTTPRED5xx, Title: "orders-api · HTTP 5xx oranı · VictoriaMetrics (örneklenmemiş)", Unit: "%", Name: "5xx %",
			Query: `100 * (sum (rate({` + cnt + `, ` + sel + `, http_response_status_code=~"5.."}[60s])) or 0 * sum (rate({` + cnt + `, ` + sel + `}[60s]))) / sum (rate({` + cnt + `, ` + sel + `}[60s]))`},
		{Kind: svcHTTPREDLatency, Title: "orders-api · HTTP gecikme p95/p99 · VictoriaMetrics (örneklenmemiş)", Unit: "s", Name: "gecikme",
			Query: `histogram_quantiles("phi", 0.95, 0.99, sum by (le) (rate({` + bkt + `, ` + sel + `}[60s])))`},
	}
	if len(got) != len(want) {
		t.Fatalf("%d grafik, beklenen %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("grafik %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
	if !strings.Contains(cnt, "http_server_request_duration_seconds_count") || !strings.Contains(bkt, "http_server_request_duration_seconds_bucket") {
		t.Errorf("seçici OTel collector yazımını taşımalı: %s / %s", cnt, bkt)
	}
	// Ortam eşleştiricisi her seçiciye; durum etiketi yoksa 5xx grafiği YOK.
	env := svcHTTPREDQueries("orders-api", "service_name", `deployment_environment_name="prod-eu-a"`, "", 120)
	if len(env) != 2 || env[0].Kind != svcHTTPREDRate || env[1].Kind != svcHTTPREDLatency {
		t.Fatalf("durum etiketi yok → rate + latency: %+v", env)
	}
	if env[0].Query != `sum (rate({`+cnt+`, `+sel+`, deployment_environment_name="prod-eu-a"}[120s]))` {
		t.Errorf("ortamlı rate: %s", env[0].Query)
	}
	// Servis değeri kaçışlanır.
	if q := svcHTTPREDQueries(`x"} or vector(1) #`, "service_name", "", "", 60)[0].Query; !strings.Contains(q, `service_name="x\"} or vector(1) #"`) {
		t.Errorf("kaçış: %s", q)
	}
}

func TestSvcHTTPREDCacheKeyHashesAllInputs(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	base := svcHTTPREDCacheKey("rate", "q", "orders-api", "", from, to, 30)
	variants := []string{
		svcHTTPREDCacheKey("5xx", "q", "orders-api", "", from, to, 30),
		svcHTTPREDCacheKey("rate", "q2", "orders-api", "", from, to, 30),
		svcHTTPREDCacheKey("rate", "q", "payment-service", "", from, to, 30),
		svcHTTPREDCacheKey("rate", "q", "orders-api", `env="a"`, from, to, 30),
		svcHTTPREDCacheKey("rate", "q", "orders-api", "", from.Add(-time.Minute), to, 30),
		svcHTTPREDCacheKey("rate", "q", "orders-api", "", from, to.Add(time.Minute), 30),
		svcHTTPREDCacheKey("rate", "q", "orders-api", "", from, to, 60),
	}
	seen := map[string]bool{base: true}
	for i, k := range variants {
		if seen[k] {
			t.Errorf("girdi %d anahtarı değiştirmedi: %s", i, k)
		}
		seen[k] = true
	}
	if base != svcHTTPREDCacheKey("rate", "q", "orders-api", "", from, to, 30) || !strings.HasPrefix(base, "cosre-mchart:v1:") {
		t.Errorf("deterministik ve ad alanlı olmalı: %s", base)
	}
}

// redReader — sorgu türüne göre seri döndüren sahte VM (rate / 5xx / gecikme).
type redReader struct {
	fakeChartReader
	rate, fiveXX, lat []chstore.SpanMetricSeries
	rateErr           error
	mu                sync.Mutex
	qs                []string
}

func (r *redReader) QueryPromQLRange(_ context.Context, q string, _, _ time.Time, _, _ int) ([]chstore.SpanMetricSeries, error) {
	r.mu.Lock()
	r.qs = append(r.qs, q)
	r.mu.Unlock()
	switch {
	case strings.HasPrefix(q, "histogram_quantiles("):
		return r.lat, nil
	case strings.HasPrefix(q, "100 * "):
		return r.fiveXX, nil
	}
	return r.rate, r.rateErr
}

// v0.10.1429 — aile yoklaması: rate serisi varsa HTTP ailesi VAR (anlık sorgu
// range sorgusu sayılmaz; qs yalnız grafik sorgularını sayar).
func (r *redReader) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	r.mu.Lock()
	r.instants = append(r.instants, q)
	r.mu.Unlock()
	return r.rate, nil
}

func svcREDPts(from time.Time, vs ...float64) []chstore.SpanMetricPoint {
	var out []chstore.SpanMetricPoint
	for i, v := range vs {
		out = append(out, chstore.SpanMetricPoint{Time: from.Add(time.Duration(i) * time.Minute).UnixNano(), Value: v})
	}
	return out
}

// TestServiceChartSourceSelection — VM var → üç HTTP RED grafiği; VM yok /
// histogram yok / servisin serisi yok → span yedeği + dürüst gerekçe.
func TestServiceChartSourceSelection(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 0, 30, 0, time.UTC)
	from := to.Add(-time.Hour)
	full := func() *redReader {
		return &redReader{
			fakeChartReader: fakeChartReader{labels: map[string][]string{mcHTTPMetric: {"deployment_environment_name", "http_response_status_code", "http_route", "service_name"}},
				envVals: map[string][]string{"deployment_environment_name": {"prod-eu-a", "prod-eu-b"}}},
			rate:   []chstore.SpanMetricSeries{{Points: svcREDPts(from, 120, 130, 125, 140)}},
			fiveXX: []chstore.SpanMetricSeries{{Points: svcREDPts(from, 0.1, 0.2, 0.1, 0.4)}},
			lat: []chstore.SpanMetricSeries{
				{GroupKey: []string{`phi="0.95"`}, Points: svcREDPts(from, 0.08, 0.09, 0.08, 0.1)},
				{GroupKey: []string{`phi="0.99"`}, Points: svcREDPts(from, 0.2, 0.25, 0.22, 0.3)},
			},
		}
	}
	s := &Server{}

	rd := full()
	res := s.readServiceHTTPRED(context.Background(), rd, true, "orders-api", "eu-a", from, to, 3600)
	if !res.UseVM || len(res.Charts) != 3 || len(rd.qs) != 3 {
		t.Fatalf("VM var: useVM=%v charts=%d queries=%d reason=%q", res.UseVM, len(res.Charts), len(rd.qs), res.Reason)
	}
	kinds := []string{}
	for _, c := range res.Charts {
		kinds = append(kinds, c.Kind)
		if c.Err != nil || len(c.Series) == 0 || !strings.HasPrefix(c.Href, "/metrics?") || !strings.Contains(c.Query, `deployment_environment_name="prod-eu-a"`) {
			t.Errorf("grafik %s: err=%v seri=%d href=%q q=%s", c.Kind, c.Err, len(c.Series), c.Href, c.Query)
		}
	}
	if strings.Join(kinds, ",") != "rate,5xx,latency" || res.Env != "eu-a" {
		t.Errorf("sıra/ortam: %v %q", kinds, res.Env)
	}
	names := map[string]bool{}
	for _, se := range res.Charts[2].Series {
		names[se.Name] = true
	}
	if !names["p95"] || !names["p99"] {
		t.Errorf("gecikme serileri p95/p99 adlı olmalı: %v", names)
	}
	if res.To != to.Truncate(time.Minute) || res.To.Sub(res.From) != time.Hour {
		t.Errorf("cevapla aynı pencere (dakikaya oturur): %v–%v", res.From, res.To)
	}

	// VM yok → span, sorgu yok.
	rd = full()
	res = s.readServiceHTTPRED(context.Background(), rd, false, "orders-api", "", from, to, 3600)
	if res.UseVM || !strings.Contains(res.Reason, "yapılandırılmamış") || len(rd.qs) != 0 {
		t.Errorf("VM yok: %+v q=%v", res, rd.qs)
	}
	// Histogram yok → span + dürüst not, sorgu yok.
	rd = full()
	rd.labels = map[string][]string{}
	res = s.readServiceHTTPRED(context.Background(), rd, true, "orders-api", "", from, to, 3600)
	if res.UseVM || !strings.Contains(res.Reason, "bulunamadı") || len(rd.qs) != 0 {
		t.Errorf("histogram yok: %+v q=%v", res, rd.qs)
	}
	// Servisin serisi yok (ör. yalnız gRPC sunan servis ama gRPC histogramı da yok) →
	// yoklama anlık sorguda (v0.10.1429 aile çözücüsü), range sorgusu YOK, span.
	rd = full()
	rd.rate = nil
	res = s.readServiceHTTPRED(context.Background(), rd, true, "orders-api", "", from, to, 3600)
	if res.UseVM || !strings.Contains(res.Reason, "serisi bu pencerede yok") || len(rd.qs) != 0 || len(res.Charts) != 0 || len(rd.instants) != 1 {
		t.Errorf("seri yok: %+v q=%v", res, rd.qs)
	}
	// Etiket okuması hatası ≠ yok.
	rd = full()
	rd.labelErr = errors.New("vm down")
	res = s.readServiceHTTPRED(context.Background(), rd, true, "orders-api", "", from, to, 3600)
	if res.UseVM || !strings.Contains(res.Reason, "okunamadı ≠ yok") {
		t.Errorf("etiket hatası: %+v", res)
	}
	// Durum etiketi yok → 5xx grafiği YOK, not; rate + gecikme çizilir.
	rd = full()
	rd.labels = map[string][]string{mcHTTPMetric: {"service_name"}}
	res = s.readServiceHTTPRED(context.Background(), rd, true, "orders-api", "", from, to, 3600)
	if !res.UseVM || len(res.Charts) != 2 || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "5xx oranı ölçülemez") {
		t.Errorf("durum etiketi yok: %+v", res)
	}
}

// TestEmitServiceHTTPRED — grafik başına kanıt çipi (PromQL + Metrics'te aç) ve
// aynı ref'li veri grafiği bloğu; kanıt kaynağı açıkça söyler, çit YAZMAZ.
func TestEmitServiceHTTPRED(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	rd := &redReader{
		fakeChartReader: fakeChartReader{labels: map[string][]string{mcHTTPMetric: {"http_response_status_code", "service_name"}}},
		rate:            []chstore.SpanMetricSeries{{Points: svcREDPts(from, 120, 130, 125, 400)}},
		fiveXX:          []chstore.SpanMetricSeries{{Points: svcREDPts(from, 0, 0, 0.5, 2)}},
		lat:             []chstore.SpanMetricSeries{{GroupKey: []string{`phi="0.99"`}, Points: svcREDPts(from, 0.2, 0.2, 0.3, 0.9)}},
	}
	s := &Server{}
	res := s.readServiceHTTPRED(context.Background(), rd, true, "orders-api", "", from, to, 3600)
	ev := &mcEvents{}
	text := s.emitServiceHTTPRED(ev.emit(), "orders-api", 3600, res, time.UTC)
	if len(ev.blocks) != 3 || len(ev.steps) != 3 {
		t.Fatalf("3 blok + 3 step-result: %d / %d", len(ev.blocks), len(ev.steps))
	}
	for i, st := range ev.steps {
		ce := st["evidence"].(mcptools.ChatEvidence)
		p := ev.blocks[i].Payload.(mcptools.EvChartPayload)
		if ce.Source != "VictoriaMetrics" || ce.QueryKind != "promql" || ce.Query != res.Charts[i].Query || p.EvidenceRef != ce.Ref || ce.Ref == "" ||
			!strings.HasPrefix(ce.Href, "/metrics?") || p.DeepLink != ce.Href || !strings.Contains(p.Title, "VictoriaMetrics (örneklenmemiş)") {
			t.Errorf("çip/blok %d: %+v / %+v", i, ce, p)
		}
	}
	for _, w := range []string{"kaynak: VictoriaMetrics, örneklenmemiş", "PromQL: sum (rate(", "req/s: min", "5xx %: min", "p99: min", "SIÇRAMA", "ÖRNEKLENMEMİŞ", "grafik bloğu YAZMA"} {
		if !strings.Contains(text, w) {
			t.Errorf("kanıt %q içermeli:\n%s", w, text)
		}
	}
	if strings.Contains(text, "```chart") || len(serverChartScopes(text)) != 0 {
		t.Error("VM kipinde span çiti olmamalı")
	}
	if n := svcHTTPREDNoChartNoteTR("VictoriaMetrics yapılandırılmamış"); !strings.Contains(n, "yalnız VictoriaMetrics") || !strings.Contains(n, "yapılandırılmamış") {
		t.Errorf("grafiksiz not: %s", n)
	}
}

// TestServiceHealthBundleChartWiring — demet: tarama operasyon grafiği HER
// ZAMAN düşer (v0.10.1453), span çiti HİÇ yok; VM çizemiyorsa yalnız not.
func TestServiceHealthBundleChartWiring(t *testing.T) {
	b, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *Server) guidedServiceHealthBundle(")
	j := strings.Index(src[i:], "\n}\n")
	body := stripGoCommentsAPI(src[i : i+j])
	for _, w := range []string{"s.startServiceHTTPRED(ctx, service, env, from, to, rangeS)", "httpRED := waitHTTPRED()",
		"sw.res.DropOperationCharts()", "s.emitServiceHTTPRED(emit, service, rangeS, httpRED, loc)", "svcHTTPREDNoChartNoteTR(httpRED.Reason)"} {
		if !strings.Contains(body, w) {
			t.Errorf("guidedServiceHealthBundle %q taşımalı", w)
		}
	}
	if d := strings.Index(body, "sw.res.DropOperationCharts()"); d < 0 || strings.Contains(body[max(0, d-120):d], "if httpRED.UseVM {") {
		t.Error("tarama operasyon grafiği koşulsuz düşmeli (v0.10.1453)")
	}
	if strings.Contains(body, "chartFence(") {
		t.Error("servis sağlığında span grafiği çiti olmamalı (v0.10.1453 — yalnız VictoriaMetrics)")
	}
	if strings.Index(body, "httpRED := waitHTTPRED()") > strings.Index(body, "json.Marshal(sw.res)") {
		t.Error("VM sonucu tarama bloklarından ÖNCE beklenmeli")
	}
}

// TestServiceHTTPREDReadOnly — salt-okur; tek range sorgusu çağrı yeri.
func TestServiceHTTPREDReadOnly(t *testing.T) {
	b, err := os.ReadFile("copilot_service_http_red.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	for _, w := range []string{"Upsert", "Insert", "Save", "audit", "Vote", "vote", "Feedback", "feedback", "Exec(", "ALTER ", "DELETE",
		"s.store.", "copilotStreamSurface", "copilotExplain", "s.copilot", "http.Post", "NewRequest", "os.Getenv", "label_replace", "/api/v1/admin", "delete_series"} {
		if strings.Contains(src, w) {
			t.Errorf("%q içermemeli (salt-okur)", w)
		}
	}
	if n := strings.Count(src, ".QueryPromQLRange("); n != 1 {
		t.Errorf("TEK range sorgusu çağrı yeri bekleniyordu, %d", n)
	}
	// v0.10.1415 — grafik başına görünüm ortak kurucuya taşındı (service_vm_red_charts.go).
	vb, err := os.ReadFile("service_vm_red_charts.go")
	if err != nil {
		t.Fatal(err)
	}
	both := src + stripGoCommentsAPI(string(vb))
	// v0.10.1429 — etiket keşfi ve ortam çözümü aile çözücüsünün içinde (red_family.go).
	// v0.10.1689 — etiket kümesi servise kapsanır; kapsamsız keşif prod VM'de zaman aşımı.
	if strings.Contains(src, "s.metricChartLabelSet(") {
		t.Error("kapsamsız metricChartLabelSet çağrılmamalı (v0.10.1689)")
	}
	for _, w := range []string{"s.redFamilyLabelSet(ctx, rd, fam.Probe, redFamilySvcLabel(lm), service, qTo)", "s.resolveServiceREDFamily(", "svcHTTPREDCacheKey(q.Kind, q.Query, service, envMatcher, from, to, step)", "metricChartStatsTR("} {
		if !strings.Contains(both, w) {
			t.Errorf("%q taşımalı (v0.10.1386 makinesi yeniden kullanılır)", w)
		}
	}
}

// TestSweepSpanKindTR — tarama kanıtında giden çağrı açıkça söylenir (anlatım
// servisin giden gRPC çağrısını gelen trafiği sanmasın).
func TestSweepSpanKindTR(t *testing.T) {
	for k, want := range map[string]string{"": "", "CLIENT": " (giden çağrı · CLIENT)", "PRODUCER": " (giden çağrı · PRODUCER)",
		"SERVER": " (gelen · SERVER)", "CONSUMER": " (gelen · CONSUMER)", "INTERNAL": " (INTERNAL)"} {
		if got := sweepSpanKindTR(k); got != want {
			t.Errorf("%q: got %q want %q", k, got, want)
		}
	}
	sw := mcptools.SweepResult{Findings: []mcptools.SweepFinding{{Rank: 1, Kind: "operation", Name: "inventory.StockService/Reserve", SpanKind: "CLIENT", Level: "warn", Reasons: []string{"hata %12"}}}}
	if txt := renderSweepEvidenceTR(sw); !strings.Contains(txt, "operasyon inventory.StockService/Reserve (giden çağrı · CLIENT) [warn]") {
		t.Errorf("tarama kanıtı:\n%s", txt)
	}
}
