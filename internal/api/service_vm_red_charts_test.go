package api

// service_vm_red_charts_test.go — v0.10.1415. Operatör: "bir servisle ilgili
// tam rapor çekerken CoSRE VictoriaMetrics'ten HTTP grafiklerini getirsin.
// Response time ve throughput."
//
// Pinler: servis raporu VM yapılandırılmış + histogram varken throughput ve
// yanıt süresi (+5xx) grafiklerini doğru PromQL ve raporun penceresiyle kartta
// basar, özetleri kanıtta; VM yok → grafik yok + dürüst not; varlık yoklaması
// boş (yalnız gRPC sunan servis) → grafik yok; okunamayan grafik "yok" demez;
// metric_chart çipleri kanıt zarflı ve önbellek isabetinde aynı (grafik bloğu
// ev_block YOK — kartta); servis sağlığı ve servis raporu AYNI kurucuyu kullanır.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// latErrReader — gecikme sorgusu hata veren sahte VM (okunamadı ≠ yok).
type latErrReader struct{ *redReader }

func (r latErrReader) QueryPromQLRange(ctx context.Context, q string, from, to time.Time, step, maxPts int) ([]chstore.SpanMetricSeries, error) {
	if strings.HasPrefix(q, "histogram_quantiles(") {
		return nil, errors.New("vm: query timeout")
	}
	return r.redReader.QueryPromQLRange(ctx, q, from, to, step, maxPts)
}

func reportHTTPReader(from time.Time) *redReader {
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

func TestServiceReportHTTPCharts(t *testing.T) {
	to := time.Date(2026, 10, 20, 9, 0, 40, 0, time.UTC)
	from := to.Add(-time.Hour)
	cnt, bkt := mcSel("count", mcHTTPMetric), mcSel("bucket", mcHTTPMetric)
	sel := `service_name="orders-api", deployment_environment_name="prod-eu-a"`
	wantQ := map[string]string{
		svcHTTPREDRate:    `sum (rate({` + cnt + `, ` + sel + `}[120s]))`,
		svcHTTPREDLatency: `histogram_quantiles("phi", 0.95, 0.99, sum by (le) (rate({` + bkt + `, ` + sel + `}[120s])))`, // adım 30 sn → rate penceresi max(4×adım, 60 sn)
	}
	cases := []struct {
		name      string
		isVM      bool
		mutate    func(*redReader) redFamilyReader
		kinds     string // kart grafiklerinin sırası
		queries   int    // VM'e giden sorgu sayısı
		note      string // kart notu (boş = not yok)
		evWant    []string
		evNot     []string
		sourcesVM bool
	}{
		{name: "VM + histogram → throughput, yanıt süresi, 5xx (kartta, özet kanıtta)", isVM: true, kinds: "rate,latency,5xx", queries: 3,
			evWant: []string{"HTTP GRAFİKLERİ (orders-api, son 1sa; VictoriaMetrics, ÖRNEKLENMEMİŞ", "KANIT: HTTP throughput — req/s: min",
				"KANIT: HTTP yanıt süresi — p95: min", "KANIT: HTTP yanıt süresi — p99: min", "KANIT: HTTP 5xx oranı — 5xx %: min", "KURAL (HTTP)"},
			evNot: []string{"```chart", "PromQL:"}, sourcesVM: true},
		{name: "VM yapılandırılmamış → grafik yok + dürüst not, sorgu yok", isVM: false, queries: 0, note: "VictoriaMetrics yapılandırılmamış",
			evWant: []string{"HTTP GRAFİKLERİ: YOK", "VictoriaMetrics yapılandırılmamış", "span verisinden (örneklenmiş"}, evNot: []string{"KANIT: HTTP", "```chart"}},
		{name: "histogram yok → grafik yok", isVM: true, queries: 0, note: "bulunamadı",
			mutate: func(r *redReader) redFamilyReader { r.labels = map[string][]string{}; return r },
			evWant: []string{"HTTP GRAFİKLERİ: YOK", "bulunamadı"}, evNot: []string{"KANIT: HTTP"}},
		{name: "varlık yoklaması boş (servisin serisi yok) → yoklama anlık sorguda (v0.10.1429), range sorgusu yok, grafik yok", isVM: true, queries: 0, note: "serisi bu pencerede yok",
			mutate: func(r *redReader) redFamilyReader { r.rate = nil; return r },
			evWant: []string{"HTTP GRAFİKLERİ: YOK", "serisi bu pencerede yok"}, evNot: []string{"KANIT: HTTP"}},
		{name: "etiket okuması hatası ≠ yok", isVM: true, queries: 0, note: "okunamadı ≠ yok",
			mutate: func(r *redReader) redFamilyReader { r.labelErr = errors.New("vm down"); return r },
			evWant: []string{"HTTP GRAFİKLERİ: YOK", "okunamadı ≠ yok"}},
		{name: "yanıt süresi sorgusu hatası → o grafik yok, OKUNAMADI cümlesi; diğerleri çizilir", isVM: true, kinds: "rate,5xx", queries: 2,
			mutate:    func(r *redReader) redFamilyReader { return latErrReader{r} },
			evWant:    []string{"KANIT: HTTP throughput — req/s: min", "HTTP yanıt süresi: VictoriaMetrics sorgusu OKUNAMADI (vm: query timeout) — 'veri yok' DEMEK DEĞİL"},
			sourcesVM: true},
	}
	s := &Server{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := reportHTTPReader(from)
			var rd redFamilyReader = rr
			if c.mutate != nil {
				rd = c.mutate(rr)
			}
			res := s.readServiceHTTPRED(context.Background(), rd, c.isVM, "orders-api", "eu-a", from, to, 3600)
			views := reportHTTPViews("orders-api", 3600, res, time.UTC)
			charts, note := reportHTTPCharts(res, views)
			ev := renderReportHTTPTR("orders-api", 3600, res, views)

			var kinds []string
			for _, ch := range charts {
				kinds = append(kinds, ch.Kind)
				if w, ok := wantQ[ch.Kind]; ok && ch.Query != w {
					t.Errorf("%s PromQL:\n got %s\nwant %s", ch.Kind, ch.Query, w)
				}
				if ch.Window.ToNs != to.Truncate(time.Minute).UnixNano() || ch.Window.ToNs-ch.Window.FromNs != int64(time.Hour) {
					t.Errorf("%s pencere raporunkiyle aynı olmalı (dakikaya oturur): %+v", ch.Kind, ch.Window)
				}
				if !strings.HasPrefix(ch.DeepLink, "/metrics?") || ch.EvidenceRef != "" || len(ch.Series) == 0 || ch.Unit == "" ||
					!strings.Contains(ch.Title, "VictoriaMetrics (örneklenmemiş)") {
					t.Errorf("%s kart grafiği: %+v", ch.Kind, ch)
				}
			}
			if got := strings.Join(kinds, ","); got != c.kinds {
				t.Errorf("kart grafikleri %q, beklenen %q", got, c.kinds)
			}
			rr.mu.Lock()
			nq := len(rr.qs)
			rr.mu.Unlock()
			if nq != c.queries {
				t.Errorf("VM sorgu sayısı %d, beklenen %d", nq, c.queries)
			}
			if (c.note == "") != (note == "") || !strings.Contains(note, c.note) {
				t.Errorf("kart notu %q, beklenen %q", note, c.note)
			}
			for _, w := range c.evWant {
				if !strings.Contains(ev, w) {
					t.Errorf("kanıt %q içermeli:\n%s", w, ev)
				}
			}
			for _, w := range c.evNot {
				if strings.Contains(ev, w) {
					t.Errorf("kanıt %q içermemeli:\n%s", w, ev)
				}
			}
			if got := reportHTTPSourcesTR(charts) != ""; got != c.sourcesVM {
				t.Errorf("kaynak satırı VM eki %v, beklenen %v", got, c.sourcesVM)
			}
			if len(ev) > serviceReportHTTPSection {
				t.Errorf("HTTP bölümü %d bayt > tavan %d (özet satırları kesilir)", len(ev), serviceReportHTTPSection)
			}
		})
	}
}

// TestServiceReportHTTPSteps — metric_chart çipi kanıt zarflı (PromQL kopyala +
// "Metrics'te aç"), grafik bloğu YAYINLANMAZ (kart çizer); önbellek isabetinde
// aynı zarf yeni kimlikle yeniden yayınlanır; JSON'da kanıt korunur.
func TestServiceReportHTTPSteps(t *testing.T) {
	to := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	s := &Server{}
	res := s.readServiceHTTPRED(context.Background(), reportHTTPReader(from), true, "orders-api", "", from, to, 3600)
	views := reportHTTPViews("orders-api", 3600, res, time.UTC)
	ev := &mcEvents{}
	rec := &reportRecorder{emit: ev.emit()}
	recordReportHTTPSteps(rec, views)
	if len(ev.blocks) != 0 {
		t.Fatalf("rapor kipinde ev_block yayınlanmamalı (grafik kartta): %d", len(ev.blocks))
	}
	if len(ev.steps) != 3 || len(rec.steps) != 3 {
		t.Fatalf("3 metric_chart çipi: events=%d kayıt=%d", len(ev.steps), len(rec.steps))
	}
	check := func(steps []map[string]any) {
		for i, st := range steps {
			ce, ok := st["evidence"].(mcptools.ChatEvidence)
			if !ok || ce.Source != "VictoriaMetrics" || ce.QueryKind != "promql" || ce.Query != views[i].Query || ce.Ref == "" ||
				!strings.HasPrefix(ce.Href, "/metrics?") || st["href"] != ce.Href || st["tool"] != "metric_chart" {
				t.Errorf("çip %d: %+v", i, st)
			}
		}
	}
	check(ev.steps)
	// Önbellek: JSON gidiş-dönüş + yeniden yayın.
	body, err := json.Marshal(serviceReportCached{Evidence: "x", Steps: rec.steps})
	if err != nil {
		t.Fatal(err)
	}
	var back serviceReportCached
	if err := json.Unmarshal(body, &back); err != nil || len(back.Steps) != 3 || back.Steps[0].Evidence == nil || back.Steps[0].Evidence.Ref != "" {
		t.Fatalf("kayıt kanıtı korumalı (ref yayın anında): %v %+v", err, back.Steps)
	}
	ev2 := &mcEvents{}
	emit2 := ev2.emit()
	emitGuidedContextStep(emit2, "önbellek: aynı servis raporu (≤60 sn)")
	replayReportSteps(emit2, back.Steps)
	ev2.steps = ev2.steps[1:] // v0.10.2480 — bağlam çipinin bitiş sonucu (context:true)
	if len(ev2.blocks) != 0 || len(ev2.steps) != 3 {
		t.Fatalf("yeniden yayın: blok=%d step-result=%d", len(ev2.blocks), len(ev2.steps))
	}
	check(ev2.steps)
	if r0, r1 := ev.steps[0]["evidence"].(mcptools.ChatEvidence).Ref, ev2.steps[0]["evidence"].(mcptools.ChatEvidence).Ref; r0 == r1 {
		t.Errorf("isabette ref yeni adım kimliğinden kurulmalı: %s = %s", r0, r1)
	}
	// Okunamayan grafik çipi hata olarak.
	bad := []svcHTTPREDView{{Kind: svcHTTPREDLatency, Args: "{}", Text: "x", Err: errors.New("timeout")}}
	ev3 := &mcEvents{}
	rec3 := &reportRecorder{emit: ev3.emit()}
	recordReportHTTPSteps(rec3, bad)
	if len(ev3.steps) != 1 || ev3.steps[0]["ok"] != false || rec3.steps[0].Evidence != nil || rec3.steps[0].Err != "timeout" {
		t.Fatalf("hata çipi: %+v / %+v", ev3.steps, rec3.steps)
	}
}

// TestServiceReportHTTPEvidencePlacement — HTTP bölümü özetin (HÜKÜM + SORU
// ŞEKLİ) hemen altında, OPERASYONLAR'dan önce; tavanlar korunur. Kart yükü
// JSON'u FE şekliyle (ChatServiceReportHTTPChart: EvChartPayload alanları düz).
func TestServiceReportHTTPEvidencePlacement(t *testing.T) {
	to := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	s := &Server{}
	res := s.readServiceHTTPRED(context.Background(), reportHTTPReader(from), true, "orders-api", "", from, to, 3600)
	views := reportHTTPViews("orders-api", 3600, res, time.UTC)
	d := reportFixture()
	d.HTTP = renderReportHTTPTR("orders-api", 3600, res, views)
	out := renderServiceReportTR(d)
	h, sh, ops := strings.Index(out, "HTTP GRAFİKLERİ ("), strings.Index(out, "SORU ŞEKLİ:"), strings.Index(out, "OPERASYONLAR (")
	if h < 0 || sh < 0 || ops < 0 || !(sh < h && h < ops) {
		t.Fatalf("HTTP bölümü SORU ŞEKLİ ile OPERASYONLAR arasında olmalı (%d %d %d):\n%s", sh, h, ops, out)
	}
	if !strings.Contains(out, "KURAL: HÜKÜM deterministik") || len(out) > serviceReportEvidenceMax+64 {
		t.Errorf("tavan / KURAL satırı: %d bayt", len(out))
	}
	if strings.Contains(renderServiceReportTR(reportFixture()), "HTTP GRAFİKLERİ") {
		t.Error("HTTP alanı boşsa bölüm yok (eski şekil)")
	}
	charts, _ := reportHTTPCharts(res, views)
	p := buildServiceReportPayload(d, reportRED{}, nil)
	p.HTTPCharts = charts
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, w := range []string{`"httpCharts":[{"title":"orders-api · HTTP istek hızı · VictoriaMetrics (örneklenmemiş)","evidenceRef":"","deepLink":"/metrics?`,
		`"kind":"rate","unit":"req/s","query":"sum (rate(`, `"kind":"latency","unit":"s"`, `"window":{"fromNs":`} {
		if !strings.Contains(js, w) {
			t.Errorf("kart JSON %q içermeli:\n%s", w, js)
		}
	}
	p.HTTPCharts = nil
	if b, _ := json.Marshal(p); strings.Contains(string(b), "httpCharts") || strings.Contains(string(b), "httpChartsNote") {
		t.Error("grafik/not yoksa alanlar yazılmaz (omitempty)")
	}
}

// TestServiceVMREDChartsSharedHelper — kaynak pini: servis sağlığı ve servis
// raporu AYNI okuma (startServiceHTTPRED) ve AYNI grafik kurucusunu
// (svcHTTPREDViews) kullanır; rapor VM okumasını önbellek kontrolünden SONRA,
// Faz 1'den ÖNCE başlatır (paralel) ve sonucu hükümden sonra bekler.
func TestServiceVMREDChartsSharedHelper(t *testing.T) {
	read := func(f string) string {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		return stripGoCommentsAPI(string(b))
	}
	fnBody := func(src, sig string) string {
		i := strings.Index(src, sig)
		if i < 0 {
			t.Fatalf("%q bulunamadı", sig)
		}
		j := strings.Index(src[i:], "\n}\n")
		return src[i : i+j]
	}
	red, rep, vm, guided := read("copilot_service_http_red.go"), read("copilot_service_report.go"), read("service_vm_red_charts.go"), read("copilot_guided.go")

	if b := fnBody(red, "func (s *Server) emitServiceHTTPRED("); !strings.Contains(b, "svcHTTPREDViews(service, rangeS, res, loc)") {
		t.Error("servis sağlığı (emitServiceHTTPRED) ortak kurucuyu kullanmalı")
	}
	if b := fnBody(vm, "func reportHTTPViews("); !strings.Contains(b, "svcHTTPREDViews(service, rangeS, res, loc)") {
		t.Error("servis raporu ortak kurucuyu kullanmalı")
	}
	if strings.Count(red+rep+vm, "mcptools.ChatEvidence{Tool: \"metric_chart\"") != 1 || !strings.Contains(vm, "mcptools.ChatEvidence{Tool: \"metric_chart\"") {
		t.Error("VM HTTP grafik kanıtı TEK yerde kurulur (service_vm_red_charts.go) — kopya yok")
	}
	hb := fnBody(guided, "func (s *Server) guidedServiceHealthBundle(")
	if !strings.Contains(hb, "s.startServiceHTTPRED(ctx, service, env, from, to, rangeS)") || !strings.Contains(hb, "s.emitServiceHTTPRED(emit, service, rangeS, httpRED, loc)") {
		t.Error("servis sağlığı 1391 okumasını ve yayınını korumalı")
	}
	if !strings.Contains(guided, "s.guidedServiceReportBundle(ctx, emit, route, from, to, rangeS, loc)") {
		t.Error("rapor çağrısı kullanıcının saat dilimini geçirmeli (grafik özeti saatleri)")
	}
	rb := fnBody(rep, "func (s *Server) guidedServiceReportBundle(")
	cache, start, ph1 := strings.Index(rb, "s.serviceReportFromCache("), strings.Index(rb, "s.startServiceHTTPRED(bctx, svc, env, from, to, rangeS)"), strings.Index(rb, "wg.Add(5)")
	if cache < 0 || start < 0 || ph1 < 0 || !(cache < start && start < ph1) {
		t.Errorf("VM okuması önbellekten sonra, Faz 1'den önce başlamalı (paralel): cache=%d start=%d faz1=%d", cache, start, ph1)
	}
	wait, verdict, render := strings.Index(rb, "httpRes := waitHTTP()"), strings.Index(rb, "serviceReportVerdict(red, view, ex, drills)"), strings.Index(rb, "evidence := renderServiceReportTR(data)")
	if wait < 0 || !(verdict < wait && wait < render) {
		t.Errorf("VM sonucu hükümden sonra, kanıt metninden önce beklenmeli: %d %d %d", verdict, wait, render)
	}
	for _, w := range []string{"reportHTTPViews(svc, rangeS, httpRes, loc)", "recordReportHTTPSteps(rec, httpViews)", "reportHTTPCharts(httpRes, httpViews)",
		"data.HTTP = renderReportHTTPTR(", "report.HTTPCharts, report.HTTPChartsNote = httpCharts, httpNote", "reportHTTPSourcesTR(httpCharts)"} {
		if !strings.Contains(rb, w) {
			t.Errorf("rapor demeti %q taşımalı", w)
		}
	}
	if strings.Contains(rb, "chartFence(") || strings.Contains(rb, "svcHTTPREDNoChartNoteTR(") {
		t.Error("rapor span grafiği UYDURMAZ (1391'in span yedeği yalnız servis sağlığında)")
	}
}

// TestServiceVMREDChartsReadOnly — yeni dosya salt-okur; VM okuması yok (okuma
// copilot_service_http_red.go'da, tek çağrı yeri).
func TestServiceVMREDChartsReadOnly(t *testing.T) {
	b, err := os.ReadFile("service_vm_red_charts.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	for _, w := range []string{"Upsert", "Insert", "Save", "audit", "Vote", "vote", "Feedback", "feedback", "Exec(", "ALTER ", "DELETE",
		"s.store.", "copilotStreamSurface", "copilotExplain", "s.copilot", "http.Post", "NewRequest", "os.Getenv", ".QueryPromQLRange(", "go func"} {
		if strings.Contains(src, w) {
			t.Errorf("%q içermemeli (salt-okur, okuma/goroutine yok)", w)
		}
	}
}
