package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// service_metric_red_test.go — v0.10.337: Overview RED'i metrikten.
// Sahte kaynak metricsource_fake_test.go'daki fakeEPSource (aynı paket;
// v0.10.1361 — /api/endpoints/metric silinince oraya taşındı).

// redFixtureSource — v0.10.1350: `keys` = VERİDE taşınan durum anahtarları.
// Durum adayıyla gruplanan rate sorgusu taşınıyorsa kod başına seri (200:
// ilk yarı 9.5, ikinci yarı 10; 500: ilk yarı 0.5; 400: 0.2 — hata değil),
// taşınmıyorsa durum sütunu "" olan tek seri; grupsuz (yedek) sorgu tek seri.
func redFixtureSource(unit string, keys []string) *fakeEPSource {
	from, _ := epFixtureWindow()
	step := 60
	src := &fakeEPSource{name: "vm", unit: unit, keys: keys}
	src.rateFn = func(f chstore.MetricQueryFilter, mode string) ([]chstore.SpanMetricSeries, error) {
		if mode != "rate" {
			return nil, nil
		}
		sk := statusGroupKey(f.GroupBy)
		if sk == "" {
			return []chstore.SpanMetricSeries{epConst(nil, from, step, 60, 10)}, nil // 10 req/s
		}
		carried := false
		for _, k := range keys {
			carried = carried || k == sk
		}
		if !carried {
			return []chstore.SpanMetricSeries{epConst([]string{""}, from, step, 60, 10)}, nil
		}
		ok := epConst([]string{"200"}, from, step, 60, 9.3)
		for i := 30; i < 60; i++ {
			ok.Points[i].Value = 9.8
		}
		e5 := epConst([]string{"500"}, from, step, 60, 0.5) // 0.5 hata/sn
		e5.Points = e5.Points[:30]                          // ikinci yarıda hata yok
		e4 := epConst([]string{"400"}, from, step, 60, 0.2) // 4xx: çağrı, hata DEĞİL
		return []chstore.SpanMetricSeries{ok, e5, e4}, nil
	}
	src.queryFn = func(f chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
		v := map[string]float64{"p50": 0.1, "p95": 0.5, "p99": 1.0, "avg": 0.2}[f.Aggregation]
		return []chstore.SpanMetricSeries{epConst(nil, from, step, 60, v)}, nil
	}
	return src
}

func TestBuildServiceMetricRED(t *testing.T) {
	from, to := epFixtureWindow()
	src := redFixtureSource("s", []string{"http.response.status_code"})
	id := httpMetricIdentity{Metric: "http_server_request_duration_seconds_count", RTMetric: "http_server_request_duration_seconds", Instrument: "histogram", Service: "cart", MatchedBy: "service_name"}
	p := serviceMetricREDPlan{Service: "cart", From: from, To: to, Mdp: 300, RateWin: 180}
	resp, err := buildServiceMetricRED(context.Background(), src, id, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"rate", "error_rate", "p50", "p95", "p99", "avg"} {
		if len(resp.Series[k]) != 1 || len(resp.Series[k][0].Points) == 0 {
			t.Fatalf("seri %q eksik: %+v", k, resp.Series[k])
		}
	}
	rate := resp.Series["rate"][0].Points
	if rate[0].Value != 10 || resp.StepSeconds != 60 {
		t.Fatalf("rate/adım: %v %d", rate[0].Value, resp.StepSeconds)
	}
	er := resp.Series["error_rate"][0].Points
	if len(er) != 60 || er[0].Value != 5 || er[59].Value != 0 {
		t.Fatalf("error_rate YÜZDE ve hizalı: n=%d ilk=%v son=%v", len(er), er[0].Value, er[59].Value)
	}
	if resp.Series["p99"][0].Points[0].Value != 1000 || resp.Series["p50"][0].Points[0].Value != 100 || resp.Series["avg"][0].Points[0].Value != 200 {
		t.Fatalf("s → ms: p99=%v p50=%v avg=%v", resp.Series["p99"][0].Points[0].Value, resp.Series["p50"][0].Points[0].Value, resp.Series["avg"][0].Points[0].Value)
	}
	if resp.ErrorsUnknown || !resp.LatencyUnitKnown || resp.StatusKey != "http.response.status_code" || resp.Source != "vm" {
		t.Fatalf("zarf: %+v", resp)
	}
	// histogram → count rate, rate modu; değer sorguları rtMetric adıyla.
	// v0.10.1350 — tek rate sorgusu durumla gruplanır; ayrı 5xx sorgusu yok.
	if len(src.calls) != 1+4 || src.calls[0] != "countrate:rate" || !strings.HasSuffix(src.calls[1], ":http_server_request_duration_seconds") {
		t.Fatalf("sorgu dizisi: %v", src.calls)
	}
	if g := strings.Join(src.groupBys[0], ","); g != "http.response.status_code" || hasStatusFilter(src.filters[0]) {
		t.Fatalf("rate durumla gruplanmalı, 5xx süzgeci yok: %s %+v", g, src.filters[0])
	}
	if len(resp.Series["rate"]) != 1 || len(rate) != 60 || rate[59].Value != 10 {
		t.Fatalf("rate = durumların toplamı, tek seri: %+v", resp.Series["rate"])
	}
	// rate penceresi + nokta bütçesi geçer
	if !strings.Contains(resp.Note, "5xx") || !strings.Contains(resp.Note, "birim s") {
		t.Fatalf("not: %s", resp.Note)
	}
}

func TestBuildServiceMetricRED_UnknownsAreAbsentNotZero(t *testing.T) {
	from, to := epFixtureWindow()
	src := redFixtureSource("", nil)
	id := httpMetricIdentity{Metric: "http.server.request.duration", RTMetric: "http.server.request.duration", Instrument: "histogram", Service: "cart"}
	p := serviceMetricREDPlan{Service: "cart", From: from, To: to, Env: "uat"}
	resp, err := buildServiceMetricRED(context.Background(), src, id, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.Series["error_rate"]; ok || !resp.ErrorsUnknown {
		t.Fatalf("durum etiketi yok → error_rate serisi HİÇ olmamalı: %+v", resp.Series)
	}
	for _, k := range []string{"p50", "p95", "p99", "avg"} {
		if _, ok := resp.Series[k]; ok {
			t.Fatalf("birim bilinmiyor → %s serisi olmamalı", k)
		}
	}
	if resp.LatencyUnitKnown || !resp.EnvAmbiguous {
		t.Fatalf("bayraklar: %+v", resp)
	}
	// v0.10.1350 — iki aday sırayla yoklanır, ikisi de boş ⇒ absent
	if len(src.calls) != 2 || resp.StatusState != statusLabelAbsent {
		t.Fatalf("yalnız iki yoklama sorgusu atılmalı: %v %s", src.calls, resp.StatusState)
	}
	if r := resp.Series["rate"]; len(r) != 1 || r[0].Points[0].Value != 10 {
		t.Fatalf("absent'ta rate yoklama serisinden: %+v", r)
	}
	for _, w := range []string{"Failure rate BİLİNMİYOR: metrikte durum-kodu etiketi yok", "Süre birimi BİLİNMİYOR", "TÜM ortamları"} {
		if !strings.Contains(resp.Note, w) {
			t.Fatalf("not %q içermeli: %s", w, resp.Note)
		}
	}
}

func TestErrorRatePercent(t *testing.T) {
	from, _ := epFixtureWindow()
	rate := []chstore.SpanMetricSeries{epConst(nil, from, 60, 3, 4)}
	rate[0].Points[2].Value = 0 // rate 0 → nokta atlanır
	errs := []chstore.SpanMetricSeries{epConst(nil, from, 60, 1, 1)}
	out := errorRatePercent(rate, errs)
	if len(out) != 1 || len(out[0].Points) != 2 || out[0].Points[0].Value != 25 || out[0].Points[1].Value != 0 {
		t.Fatalf("yüzde: %+v", out)
	}
	if got := errorRatePercent(nil, errs); len(got) != 0 {
		t.Fatalf("rate yok → boş: %+v", got)
	}
	if s := seriesStep(rate); s != 60 {
		t.Fatalf("adım: %d", s)
	}
}

func TestServiceMetricREDKeyCoversEveryInput(t *testing.T) {
	from, to := epFixtureWindow()
	base := serviceMetricREDPlan{Service: "cart", From: from, To: to, Mdp: 300, RateWin: 180, Env: "uat"}
	k0 := serviceMetricREDKey(base, "vm", "0", "-")
	muts := []func(p *serviceMetricREDPlan){
		func(p *serviceMetricREDPlan) { p.Service = "pay" },
		func(p *serviceMetricREDPlan) { p.To = p.To.Add(time.Hour) },
		func(p *serviceMetricREDPlan) { p.Mdp = 600 },
		func(p *serviceMetricREDPlan) { p.RateWin = 60 },
		func(p *serviceMetricREDPlan) { p.Env = "prod" },
	}
	for i, m := range muts {
		p := base
		m(&p)
		if serviceMetricREDKey(p, "vm", "0", "-") == k0 {
			t.Fatalf("girdi %d anahtara girmiyor", i)
		}
	}
	if serviceMetricREDKey(base, "ch", "0", "-") == k0 || serviceMetricREDKey(base, "vm", "1", "-") == k0 {
		t.Fatal("depo + dışlama özeti anahtarda olmalı")
	}
	// v0.10.1377 — LabelMap'in env/cluster etiket özeti de anahtarda.
	if serviceMetricREDKey(base, "vm", "0", fnvStr("env_x", "")) == k0 {
		t.Fatal("LabelMap özeti anahtarda olmalı")
	}
}

func TestServiceMetricREDRouteIsInTheRegistry(t *testing.T) {
	if _, ok := extraRouteRegistrars["service-metric-red"]; !ok {
		t.Fatal("service-metric-red defterde değil")
	}
	if src := readRepoFile(t, "api.go"); strings.Contains(src, "metric-red") {
		t.Fatal("api.go bu rotayı bilmemeli")
	}
}

// v0.10.842 — Operator-reported: /service?name=<batch-servis>&range=3h
// "TypeError: Cannot read properties of null (reading 'map')" ile hata
// sınırına düştü (test ortamı). Kök neden: pencerede hiç istek yoksa (boşta
// batch işi) rate serisinin her noktası 0 → errorRatePercent hepsini atlar,
// out.Points nil kalır → JSON'a `"points":null` yazılır → Overview vals()
// null üstünde .map çağırır. Sözleşme: seri VARSA points bir dizidir ([]),
// asla null — /api-route ev kuralı (nil dilim → []T{}).
func TestErrorRatePercent_IdleWindowPointsNeverNull(t *testing.T) {
	from, _ := epFixtureWindow()
	rate := []chstore.SpanMetricSeries{epConst(nil, from, 60, 3, 0)} // boşta: rate hep 0
	errs := []chstore.SpanMetricSeries{epConst(nil, from, 60, 3, 0)}
	out := errorRatePercent(rate, errs)
	if len(out) != 1 {
		t.Fatalf("tek seri beklenir: %+v", out)
	}
	if out[0].Points == nil {
		t.Fatalf("points nil: JSON'a null yazılır, frontend .map üstünde düşer")
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"points":[]`) {
		t.Fatalf("JSON points boş dizi olmalı: %s", b)
	}
}

// v0.10.1350 — Operator-reported (prod): metrik kipinde "durum-kodu etiketi
// yok" derken HTTP Metrics sekmesi (VM) aynı servis için 200/400 çiziyordu
// (kök neden: tek, filo genelinde, 7 g /api/v1/labels taraması; hata/kısmi ⇒
// nil ⇒ "yok"). (a) MetricPresentKeys nil, veri kod taşıyor ⇒ error_rate var.
func TestBuildServiceMetricRED_StatusFromData_v0101350(t *testing.T) {
	from, to := epFixtureWindow()
	src := redFixtureSource("s", []string{"http.response.status_code"})
	src.keys = nil
	id := httpMetricIdentity{Metric: "m_count", RTMetric: "http_server_request_duration_seconds", Instrument: "histogram", Service: "cart", MatchedBy: "service_name"}
	resp, err := buildServiceMetricRED(context.Background(), src, id, serviceMetricREDPlan{Service: "cart", From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ErrorsUnknown || resp.StatusState != statusLabelPresent || resp.StatusKey != "http.response.status_code" || src.presentCalls != 0 {
		t.Fatalf("veri durum taşıyor → present, etiket taraması yok: %+v (present çağrı %d)", resp, src.presentCalls)
	}
	if er := resp.Series["error_rate"]; len(er) != 1 || er[0].Points[0].Value != 5 {
		t.Fatalf("error_rate veriden (500/(200+500+400) = %%5): %+v", er)
	}
	if !strings.Contains(resp.Note, "Failure rate = http_response_status_code 5xx (4xx hata sayılmaz)") {
		t.Fatalf("not: %s", resp.Note)
	}
}

// (b) durum sorgusu hata ⇒ unknown, rate grupsuz yedekten; not "yok" demez.
func TestBuildServiceMetricRED_StatusQueryErrorIsUnknown_v0101350(t *testing.T) {
	from, to := epFixtureWindow()
	src := redFixtureSource("s", []string{"http.response.status_code"})
	inner := src.rateFn
	src.rateFn = func(f chstore.MetricQueryFilter, mode string) ([]chstore.SpanMetricSeries, error) {
		if statusGroupKey(f.GroupBy) != "" {
			return nil, errors.New("vm: partial response")
		}
		return inner(f, mode)
	}
	id := httpMetricIdentity{Metric: "m_count", RTMetric: "http_server_request_duration_seconds", Instrument: "histogram", Service: "cart"}
	resp, err := buildServiceMetricRED(context.Background(), src, id, serviceMetricREDPlan{Service: "cart", From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.ErrorsUnknown || resp.StatusState != statusLabelUnknown || !strings.Contains(resp.StatusReason, "partial") {
		t.Fatalf("hata ⇒ unknown: %+v", resp)
	}
	if _, ok := resp.Series["error_rate"]; ok {
		t.Fatalf("bilinmeyen hata serisi uydurulmaz: %+v", resp.Series["error_rate"])
	}
	if r := resp.Series["rate"]; len(r) != 1 || r[0].Points[0].Value != 10 {
		t.Fatalf("rate grupsuz yedekten: %+v", r)
	}
	if !strings.Contains(resp.Note, "durum sorgusu başarısız (vm: partial response)") || strings.Contains(resp.Note, "durum-kodu etiketi yok") {
		t.Fatalf("not: %s", resp.Note)
	}
}

func TestSumSeriesByTime_v0101350(t *testing.T) {
	from, _ := epFixtureWindow()
	a := epConst([]string{"200"}, from, 60, 3, 2)
	b := epConst([]string{"500"}, from, 60, 2, 1)
	out := sumSeriesByTime([]chstore.SpanMetricSeries{b, a})
	if len(out) != 1 || len(out[0].Points) != 3 || out[0].Points[0].Value != 3 || out[0].Points[2].Value != 2 || out[0].Points[0].Time >= out[0].Points[1].Time {
		t.Fatalf("toplam + zaman sırası: %+v", out)
	}
	if got := sumSeriesByTime(nil); got == nil || len(got) != 0 {
		t.Fatalf("boş → []: %+v", got)
	}
}

// v0.10.1360 — not metni iki kavramı AYIRIR: grafik serileri adım başına
// histogram_quantile(rate[180s]); Overview karoları pencere değeri
// (increase[W], HTTP Metrics windowQueries ile aynı). Eski "P50/P95/P99
// histogram_quantile" ifadesi karonun da adım değeri olduğunu ima ediyordu.
func TestServiceMetricREDNote_WindowTilesWording_v0101360(t *testing.T) {
	r := serviceMetricREDResponse{
		Source: "vm", Metric: "http.server.request.duration", MatchedBy: "service_name",
		MetricExists: true, LatencyUnitKnown: true, LatencyUnit: "s",
		StatusState: statusLabelPresent, StatusKey: "http.response.status_code",
	}
	note := serviceMetricREDNote(r)
	want := "grafik serileri adım başına histogram_quantile(rate[180s]); karolar pencere değeri (increase[W], HTTP/GRPC ile aynı)"
	if !strings.Contains(note, want) || !strings.Contains(note, "birim s") {
		t.Fatalf("not %q içermeli: %s", want, note)
	}
	// Birim bilinmiyorsa yüzdelik metni hiç yazılmaz (karo span'a düşer).
	r.LatencyUnitKnown = false
	if n := serviceMetricREDNote(r); strings.Contains(n, "increase[W]") {
		t.Fatalf("birim yokken pencere ifadesi olmamalı: %s", n)
	}
}

// v0.10.1361 — latencyScaleMs endpoints_metric.go'dan taşındı; birim
// sözleşmesinin doğrudan pini o dosyanın testleriyle gitmesin. Her birim
// ayrı satır (CLAUDE.md: değer+birim şablonunda HER birimi test et).
func TestLatencyScaleMs(t *testing.T) {
	cases := []struct {
		unit, metric string
		scale        float64
		known        bool
		from         string
	}{
		{"s", "m", 1000, true, "source"},
		{" Seconds ", "m", 1000, true, "source"},
		{"ms", "m", 1, true, "source"},
		{"milliseconds", "m", 1, true, "source"},
		{"us", "m", 0.001, true, "source"},
		{"microseconds", "m", 0.001, true, "source"},
		{"ns", "m", 1e-6, true, "source"},
		{"nanoseconds", "m", 1e-6, true, "source"},
		{"", "http_server_request_duration_seconds", 1000, true, "name"},
		{"", "rpc_duration_milliseconds", 1, true, "name"},
		{"ms", "http_server_request_duration_seconds", 1, true, "source"}, // kaynak birimi addan önce
		{"", "http.server.request.duration", 1, false, ""},
		{"By", "m", 1, false, ""},
	}
	for _, c := range cases {
		scale, known, from := latencyScaleMs(c.unit, c.metric)
		if scale != c.scale || known != c.known || from != c.from {
			t.Errorf("latencyScaleMs(%q, %q) = (%v, %v, %q) want (%v, %v, %q)",
				c.unit, c.metric, scale, known, from, c.scale, c.known, c.from)
		}
	}
}
