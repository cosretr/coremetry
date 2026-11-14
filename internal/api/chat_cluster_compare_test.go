package api

// v0.10.1374 (operator-reported, CoSRE küme kıyası) — bir endpoint iki bölgeden
// servis ediliyor, DB çoğunlukla birinde; soru (adlar SENTETİK):
//
//   "*/shop/orders/detail* endpointi hem kuzey(eu-a, eu-b) hem güney(tr-c)
//    kümelerinde çağrılıyor, tekrar eden DB/servis çağrılarına bak"
//
// v0.10.1373 bu soruyu tüm ortamlarda okuyup söylüyordu, KIYASLAMIYORDU.
//
// SÖZLEŞME:
//   - endpoint/yol hedefi mesajın andığı ≥2 canlı ortamı taşır (endpointEnvSet;
//     yol parçası ortam sayılmaz) → küme kıyası; <2 → nil, bayt bayt eski yol.
//   - ortam başına RED TEK ham sorgu (chstore RouteREDByEnv/PathREDByEnv),
//     pencere ≤6 sa, yalnız tek serviste; yol-yalnız hedefte yalnız somut yol.
//   - ortam başına ≤5 en yavaş trace; ≤4 ortamda ≤2 trace'in şekli (en yavaş 3
//     + en hızlı 1 kıyas çifti korunur).
//   - renderClusterCompareTR: en yavaş işareti + oran, "veri yok (bu
//     pencerede)", "RED OKUNAMADI (<sınıf>)", "RED ölçülemedi"; hipotez YALNIZ
//     tekrar sayıları ±%20 içinde VE çağrı başına DB ortalaması oranı ≥2.
//   - seed(): kıyas metni ÜÇÜNCÜ sentetik çift (kırpma tavanı); summary()
//     değişmez; ClusterCompare nil → mesajlar ve prompt bayt bayt eski.
//   - v0.10.1376: başlık RED kaynağını söyler — "kaynak: metrik (VM, …, <etiket>)"
//     ya da "kaynak: trace (örneklenmiş) — …" (+ zorunlu düşüşte "(<not>)");
//     metrik satırında ortam adı eşlenen değerle ("eu-a (metrik: prod-eu-a)"),
//     durum etiketi yoksa "hata bilinmiyor", eşlenmeyen ortam "metrikte
//     eşleşen ortam değeri yok"; iki kaynak bir blokta karışmaz.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

const (
	ccTraceA = "0af7651916cd43dd8448eb211c80319c"
	ccTraceC = "9f3a2b1c4d5e6f708192a3b4c5d6e7f8"
)

var ccTo = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// v0.10.1376 — trace kaynağı başlık eki ve KURAL satırı (altın).
const (
	ccTraceHeader = " · kaynak: trace (örneklenmiş) — sayılar gerçek trafiğin bir kısmı; oranlar ve p50/p95 anlamlı"
	ccRuleLine    = "KURAL: Küme satırları HESAPLANMIŞ ölçümdür — sayıları aynen aktar; OKUNAMADI / veri yok / ölçülemedi yazan küme için sayı UYDURMA; Hipotez satırı yoksa bölgeler arası gecikme iddiasını kesin dille kurma; kaynak trace (örneklenmiş) ise çağrı sayısını gerçek trafik diye sunma.\n"
)

// ccShape — tekrar eden tek SELECT'li N+1 şekli.
func ccShape(spans, dbN int, dbSumNs int64, repName string, repN int, repAvgNs int64) traceShapeStats {
	return traceShapeStats{Spans: spans, WallNs: 2e9, DB: shapeShare{Count: dbN, SumNs: dbSumNs, Share: float64(dbSumNs) / 2e9},
		Repeats: []shapeRepeat{{Name: repName, Count: repN, SumNs: int64(repN) * repAvgNs, AvgNs: repAvgNs}}}
}

func ccEnv(env string, red chstore.OpRED, rows []chstore.TraceRow, shapes ...traceShapeStats) clusterEnvEvidence {
	e := clusterEnvEvidence{Env: env, RED: red, rows: rows}
	for _, r := range rows {
		e.Traces = append(e.Traces, opTraceRow{TraceID: r.TraceID, Service: r.ServiceName, DurationMs: r.DurationMs, Error: r.HasError})
	}
	if len(shapes) > 0 {
		e.Drilled = true
		for i, sh := range shapes {
			e.drills = append(e.drills, clusterDrill{Row: rows[i], D: traceDrill{TraceID: rows[i].TraceID, Shape: sh}})
		}
	}
	return e
}

// ccProdLike — operatörün şekli: tr-c'de aynı ×46 SELECT, çağrı başına DB 20 ms vs 3 ms.
func ccProdLike() clusterCompareEvidence {
	return clusterCompareEvidence{Label: "GET /shop/orders/detail", Service: "orders-api-prod", RangeS: 3 * 3600,
		From: ccTo.Add(-3 * time.Hour), To: ccTo,
		Envs: []clusterEnvEvidence{
			ccEnv("eu-a", chstore.OpRED{Calls: 1234, Errors: 1, P50Ms: 120, P95Ms: 480, Measured: true},
				[]chstore.TraceRow{{TraceID: ccTraceA, ServiceName: "orders-api-prod", DurationMs: 910}},
				ccShape(60, 46, 46*3e6, "SELECT orders", 44, 3e6)),
			ccEnv("eu-b", chstore.OpRED{}, nil),
			ccEnv("tr-c", chstore.OpRED{Calls: 310, Errors: 1, P50Ms: 610, P95Ms: 2100, Measured: true},
				[]chstore.TraceRow{{TraceID: ccTraceC, ServiceName: "orders-api-prod", DurationMs: 2300, HasError: true}},
				ccShape(148, 92, 92*20e6, "SELECT orders", 46, 19e6)),
		}}
}

func TestRenderClusterCompareTR(t *testing.T) {
	got := renderClusterCompareTR(ccProdLike())
	want := "KÜME KIYASI — GET /shop/orders/detail · orders-api-prod · son 3sa (09:00 – 12:00 UTC)" + ccTraceHeader + "\n" + // v0.10.1376
		"- eu-a: 1.234 çağrı · hata %0,1 · p50 120 ms · p95 480 ms\n" +
		"- eu-b: veri yok (bu pencerede)\n" +
		"- tr-c: 310 çağrı · hata %0,3 · p50 610 ms · p95 2,1 s ← en yavaş (p95 ×4,4 / eu-a)\n" +
		"En yavaş trace'ler (küme · trace · süre · şekil):\n" +
		"- eu-a · " + ccTraceA + " · 910 ms · 60 span · DB 46 span / 138 ms (%7) · tekrar: SELECT orders ×44 (ort 3 ms)\n" +
		"- eu-b · bu pencerede trace yok\n" +
		"- tr-c · " + ccTraceC + " · 2,3 s · hatalı · 148 span · DB 92 span / 1,8 s (%92) · tekrar: SELECT orders ×46 (ort 19 ms)\n" +
		"Hipotez (hesaplanmış): tekrar sayısı benzer — SELECT orders: tr-c ×46, eu-a ×44; çağrı başına DB ortalaması tr-c 20 ms, eu-a 3 ms (×6,7) → fark, tekrar sayısı × bölgeler arası gecikme ile uyumlu (DB/kaynak hızlı küme eu-a tarafında olabilir).\n" +
		ccRuleLine // v0.10.1376
	if got != want {
		t.Fatalf("golden:\n%s\nwant:\n%s", got, want)
	}

	// RED okunamadı (tek sorgu → her ortam), trace okunamadı, kırılım okunamadı,
	// pencere kırpıldı.
	ev := ccProdLike()
	ev.Clamped = true
	ev.RangeS = 6 * 3600
	ev.From = ccTo.Add(-6 * time.Hour)
	for i := range ev.Envs {
		ev.Envs[i].REDErr = "timeout"
	}
	ev.Envs[1].TraceErr = "timeout"
	ev.Envs[0].drills[0].D = traceDrill{TraceID: ccTraceA, Err: context.DeadlineExceeded}
	got = renderClusterCompareTR(ev)
	for _, w := range []string{
		"son 6sa (06:00 – 12:00 UTC) — sohbet penceresi 6 sa ile sınırlandı" + ccTraceHeader + "\n", // v0.10.1376
		"- eu-a: RED OKUNAMADI (timeout)\n", "- eu-b: RED OKUNAMADI (timeout)\n", "- tr-c: RED OKUNAMADI (timeout)\n",
		"- eu-b · trace OKUNAMADI (timeout)\n",
		"- eu-a · " + ccTraceA + " · 910 ms · span'ler OKUNAMADI (timeout)\n",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("%q içermeli:\n%s", w, got)
		}
	}
	if strings.Contains(got, "en yavaş (") || strings.Contains(got, "Hipotez (hesaplanmış)") {
		t.Errorf("RED okunamadı / şekil yok → işaret ve hipotez yok:\n%s", got)
	}

	// RED ölçülemedi (yol-yalnız, servis belirsiz) — her ortam satırı söyler, sayı yok.
	ev = ccProdLike()
	ev.Service, ev.REDNote = "", "servis tek adaya çözülmedi; servissiz ham RED sorgusu koşmaz — trace'lere bakıldı"
	got = renderClusterCompareTR(ev)
	for _, w := range []string{"· servis belirsiz · son 3sa", "RED ölçülemedi: servis tek adaya çözülmedi", "- eu-a: RED ölçülemedi\n", "- tr-c: RED ölçülemedi\n"} {
		if !strings.Contains(got, w) {
			t.Errorf("%q içermeli:\n%s", w, got)
		}
	}
	if strings.Contains(got, "çağrı · hata") || strings.Contains(got, "← en yavaş") {
		t.Errorf("RED ölçülmediyse sayı / işaret yok:\n%s", got)
	}
	// RED yokken hipotez trace süresiyle sıralanır (tr-c 2,3 s en yavaş) — yine hesaplanmış.
	if !strings.Contains(got, "Hipotez (hesaplanmış): tekrar sayısı benzer — SELECT orders: tr-c ×46, eu-a ×44") {
		t.Errorf("trace süresiyle sıralı hipotez:\n%s", got)
	}

	// Kırılım atlanan ortam.
	ev = ccProdLike()
	ev.Envs[1] = clusterEnvEvidence{Env: "eu-b", RED: chstore.OpRED{Calls: 5, P50Ms: 100, P95Ms: 200, Measured: true},
		Traces: []opTraceRow{{TraceID: "x1", DurationMs: 300}, {TraceID: "x2", DurationMs: 250}}}
	if got = renderClusterCompareTR(ev); !strings.Contains(got, "- eu-b · 2 trace, en yavaş 300 ms · kırılım atlandı (en çok 4 küme kırılır)\n") ||
		!strings.Contains(got, "← en yavaş (p95 ×10,5 / eu-b)") {
		t.Errorf("kırılım atlandı satırı / oran:\n%s", got)
	}
}

func TestClusterHypothesisGating(t *testing.T) {
	if h := clusterHypothesisTR(ccProdLike()); !strings.HasPrefix(h, "Hipotez (hesaplanmış)") {
		t.Fatalf("taban hipotez kurulmalı: %q", h)
	}
	for name, mut := range map[string]func(*clusterCompareEvidence){
		"tekrar ±%20 dışında": func(ev *clusterCompareEvidence) {
			ev.Envs[0].drills[0].D.Shape = ccShape(60, 46, 46*3e6, "SELECT orders", 30, 3e6)
		},
		"DB oranı <2": func(ev *clusterCompareEvidence) {
			ev.Envs[0].drills[0].D.Shape = ccShape(60, 46, 46*12e6, "SELECT orders", 44, 12e6)
		},
		"tekrar adı hızlı kümede yok": func(ev *clusterCompareEvidence) {
			ev.Envs[0].drills[0].D.Shape = ccShape(60, 46, 46*3e6, "SELECT customers", 44, 3e6)
		},
		"hızlı kümede DB yok": func(ev *clusterCompareEvidence) {
			ev.Envs[0].drills[0].D.Shape.DB = shapeShare{}
		},
		"tek kümede şekil": func(ev *clusterCompareEvidence) {
			ev.Envs[0].drills = nil
		},
		"kırılım okunamadı": func(ev *clusterCompareEvidence) {
			ev.Envs[2].drills[0].D.Err = errors.New("boom")
		},
	} {
		ev := ccProdLike()
		mut(&ev)
		if h := clusterHypothesisTR(ev); h != "" {
			t.Errorf("%s: hipotez kurulmamalı, %q", name, h)
		}
		if strings.Contains(renderClusterCompareTR(ev), "Hipotez (hesaplanmış)") {
			t.Errorf("%s: blokta hipotez satırı olmamalı", name)
		}
	}
	// Sınır: tam %20 fark kabul, 2,0 oran kabul.
	ev := ccProdLike()
	ev.Envs[0].drills[0].D.Shape = ccShape(60, 40, 40*10e6, "SELECT orders", 40, 10e6)
	ev.Envs[2].drills[0].D.Shape = ccShape(148, 50, 50*20e6, "SELECT orders", 50, 19e6)
	if h := clusterHypothesisTR(ev); !strings.Contains(h, "(×2,0)") {
		t.Errorf("sınır değerler hipotezi kurmalı: %q", h)
	}
}

func TestClusterComparePureHelpers(t *testing.T) {
	// Ortam kümesi: canlı katalog, sınırlı, yol parçası ortam değil.
	envs := []string{"eu-b", "eu-a", "tr-c"}
	if got := endpointEnvSet(wrapMsg2, "/shop/.../detail", envs); !reflect.DeepEqual(got, []string{"eu-a", "eu-b", "tr-c"}) {
		t.Errorf("endpointEnvSet = %v", got)
	}
	if got := endpointEnvSet("*/eu-a/orders* hem eu-b hem tr-c'de yavaş", "/eu-a/orders", envs); !reflect.DeepEqual(got, []string{"eu-b", "tr-c"}) {
		t.Errorf("yol parçası ortam sayılmamalı: %v", got)
	}
	if got := endpointEnvSet("/shop/orders/detail tr-c'de yavaş", "/shop/orders/detail", envs); got != nil {
		t.Errorf("tek ortam → nil: %v", got)
	}
	if got := endpointEnvSet(wrapMsg2, "/shop/.../detail", nil); got != nil {
		t.Errorf("katalog yok → nil: %v", got)
	}
	// Kıyas kapısı.
	for tg, want := range map[*opTarget]bool{
		{Endpoint: true, Envs: []string{"eu-a", "tr-c"}}: true,
		{HasRoute: true, Envs: []string{"eu-a", "tr-c"}}: true,
		{Endpoint: true, Envs: []string{"eu-a"}}:         false,
		{HasSpan: true, Envs: []string{"eu-a", "tr-c"}}:  false, // endpoint dışı operasyon
	} {
		if got := wantsClusterCompare(*tg); got != want {
			t.Errorf("wantsClusterCompare(%+v) = %v", *tg, got)
		}
	}
	// Pencere ≤6 sa.
	if f, c := clusterWindow(ccTo.Add(-24*time.Hour), ccTo); !f.Equal(ccTo.Add(-6*time.Hour)) || !c {
		t.Errorf("24 sa → 6 sa kırpılmalı: %v %v", f, c)
	}
	if f, c := clusterWindow(ccTo.Add(-time.Hour), ccTo); !f.Equal(ccTo.Add(-time.Hour)) || c {
		t.Errorf("1 sa aynen: %v %v", f, c)
	}
	// Etiket.
	for tg, want := range map[*opTarget]string{
		{Name: "GET /shop/x", Method: "GET", HasSpan: true}:                   "GET /shop/x",
		{Name: "/shop/x", Route: "/shop/{id}", Method: "GET", HasRoute: true}: "GET /shop/{id}",
		{Name: "/shop/.../x", SearchPath: "/shop/.../x"}:                      "/shop/.../x",
	} {
		if got := clusterLabel(*tg); got != want {
			t.Errorf("clusterLabel = %q, want %q", got, want)
		}
	}
	// RED planı: servis şart; yol-yalnızda somut yol.
	for _, c := range []struct {
		t        opTarget
		svc      string
		mode     string
		noteWant string
	}{
		{opTarget{HasRoute: true, Route: "/shop/x"}, "orders-api-prod", "route", ""},
		{opTarget{HasSpan: true, Name: "GET /shop/x"}, "orders-api-prod", "route", ""},
		{opTarget{HasRoute: true, Route: "/shop/x"}, "", "", "servis tek adaya çözülmedi"},
		{opTarget{SearchPath: "/shop/orders/detail"}, "orders-api-prod", "path", ""},
		{opTarget{SearchPath: "/shop/.../detail"}, "orders-api-prod", "", "elips/joker"},
		{opTarget{SearchPath: "/users/:id/orders"}, "orders-api-prod", "", "elips/joker"},
	} {
		mode, note := clusterREDPlan(c.t, c.svc)
		if mode != c.mode || (c.noteWant == "") != (note == "") || !strings.Contains(note, c.noteWant) {
			t.Errorf("clusterREDPlan(%+v, %q) = (%q, %q)", c.t, c.svc, mode, note)
		}
	}
	// Trace süzgeci: ortam + hedef süzgeci, ≤5, süre azalan, sayım yok.
	from := ccTo.Add(-3 * time.Hour)
	f := clusterTraceFilter(opTarget{HasRoute: true, Route: "/shop/x"}, "orders-api-prod", "tr-c", from, ccTo)
	if f.Env != "tr-c" || f.Service != "orders-api-prod" || f.Limit != clusterTraceLimit || f.Sort != "duration" || f.Order != "desc" || f.CountMode != "skip" ||
		!reflect.DeepEqual(f.Filters, opRouteFilter("/shop/x")) || f.Search != "" || !f.From.Equal(from) || !f.To.Equal(ccTo) {
		t.Errorf("rota süzgeci: %+v", f)
	}
	if f := clusterTraceFilter(opTarget{HasSpan: true, HasRoute: true, Name: "GET /shop/x", Route: "/shop/x"}, "", "eu-a", from, ccTo); len(f.Filters) != 1 || f.Filters[0].Key != "name" {
		t.Errorf("span adı önce: %+v", f.Filters)
	}
	if f := clusterTraceFilter(opTarget{SearchPath: "/shop/.../detail"}, "", "eu-a", from, ccTo); f.Search != "/shop/ /detail" || f.Filters != nil {
		t.Errorf("yol-yalnız Search: %+v", f)
	}
	// Kırılım ortamları: ≤4 hepsi; fazlası en yavaş 3 + en hızlı 1, ortam sırasıyla.
	mk := func(p95s ...float64) []clusterEnvEvidence {
		var out []clusterEnvEvidence
		for i, p := range p95s {
			e := clusterEnvEvidence{Env: string(rune('a' + i)), RED: chstore.OpRED{Calls: 10, P95Ms: p, Measured: true}}
			if p > 0 {
				e.rows = []chstore.TraceRow{{TraceID: "t", DurationMs: p}}
			}
			out = append(out, e)
		}
		return out
	}
	if got := clusterDrillEnvs(mk(100, 0, 300)); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Errorf("trace'siz ortam kırılmaz: %v", got)
	}
	if got := clusterDrillEnvs(mk(100, 500, 300, 50, 900, 200)); !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Errorf("en yavaş 3 + en hızlı 1: %v", got)
	}
	// Biçimler.
	for n, want := range map[uint64]string{0: "0", 999: "999", 1234: "1.234", 1234567: "1.234.567"} {
		if got := fmtTRCount(n); got != want {
			t.Errorf("fmtTRCount(%d) = %q", n, got)
		}
	}
	if got := fmtTRErrRate(chstore.OpRED{Calls: 100000, Errors: 1}); got != "%<0,1" {
		t.Errorf("çok küçük hata oranı: %q", got)
	}
	if got := fmtTRErrRate(chstore.OpRED{Calls: 100}); got != "%0,0" {
		t.Errorf("hatasız: %q", got)
	}
}

func TestOpPrefetchSeedWithClusterCompare(t *testing.T) {
	base := &opPrefetch{Operation: "GET /shop/orders/detail", MatchKind: "endpoint_exact", RangeS: 3600, argsJSON: `{"operation":"GET /shop/orders/detail"}`,
		From: ccTo.Add(-time.Hour), To: ccTo, endpoint: true, Services: []string{"orders-api-prod"}}
	msgs := []copilot.ChatMessage{{Role: "user", Text: wrapMsg2}}
	before, sumBefore := base.seed(msgs), base.summary()
	if len(before) != 3 || base.clusterPromptTR() != "" || (*opPrefetch)(nil).clusterPromptTR() != "" {
		t.Fatalf("kıyas yoksa tek çift ve ek yok: %d", len(before))
	}
	ev := ccProdLike()
	base.ClusterCompare = &ev
	base.clusterText = renderClusterCompareTR(ev)
	base.clusterArgsJSON = `{"envs":["eu-a","eu-b","tr-c"],"range_s":10800,"route":"GET /shop/orders/detail"}`
	if base.summary() != sumBefore {
		t.Fatal("küme kıyası summary()'ye girmez")
	}
	if !strings.Contains(base.clusterPromptTR(), "KÜME KIYASI") {
		t.Fatal("kıyas varken serbest döngü eki")
	}
	got := base.seed(msgs)
	if len(got) != 5 || !reflect.DeepEqual(got[:3], before) {
		t.Fatalf("kıyas üçüncü çift olarak SONA eklenir: %d", len(got))
	}
	if tc := got[3].ToolCalls; len(tc) != 1 || tc[0].ID != "cosre-cluster-compare" || tc[0].Name != "cluster_compare" || string(tc[0].Input) != base.clusterArgsJSON {
		t.Fatalf("kıyas çağrısı: %+v", got[3])
	}
	if tr := got[4].ToolResults; len(tr) != 1 || tr[0].CallID != opClusterToolID || tr[0].Content != base.clusterText {
		t.Fatalf("kıyas sonucu: %+v", got[4])
	}
	// Kırılımla birlikte sıra: özet, kırılım, kıyas.
	base.Drill, base.drillArgsJSON = "KIRILIM\n", `{"traces":1}`
	if got := base.seed(msgs); len(got) != 7 || got[3].ToolCalls[0].Name != opDrillTool || got[5].ToolCalls[0].Name != opClusterTool {
		t.Fatalf("sıra özet → kırılım → kıyas: %d", len(got))
	}
	// Kırpma tavanı.
	base.clusterText = strings.Repeat("ş", chatToolResultMaxRunes+10)
	if c := base.seed(msgs)[6].ToolResults[0].Content; utf8.RuneCountInString(c) != chatToolResultMaxRunes+utf8.RuneCountInString(sprintfTruncNote(chatToolResultMaxRunes, 10)) {
		t.Fatalf("kıyas içeriği model tavanına kırpılmalı: %d", utf8.RuneCountInString(c))
	}
	// Kıyas yok → küme alanları JSON'a girmez (kart yükü bayt bayt eski).
	if b, _ := json.Marshal(&opPrefetch{}); strings.Contains(string(b), "clusterCompare") {
		t.Fatalf("nil kıyas JSON'a girmemeli: %s", b)
	}
}

// TestClusterCompareUnderModelClamp — azami blok (6 ortam, 4 ortamda 2'şer
// kırılım, azami şekil satırları, atlanan 2 ortam, hipotez, uzun rota) model
// tavanının ALTINDA.
func TestClusterCompareUnderModelClamp(t *testing.T) {
	long := strings.Repeat("Ş", 200)
	maxShape := traceShapeStats{Spans: 9999, WallNs: 9e9, DB: shapeShare{Count: 9000, SumNs: 8e9, Share: 0.9},
		Client: shapeShare{Count: 900, SumNs: 5e8, Share: 0.05}, DistinctPeers: 99,
		Repeats: []shapeRepeat{{Name: long, Count: 4000, AvgNs: 2e6}, {Name: long, Count: 3000, AvgNs: 2e6}, {Name: long, Count: 2000, AvgNs: 2e6}, {Name: long, Count: 1000, AvgNs: 2e6}}}
	ev := clusterCompareEvidence{Label: "POST /" + strings.Repeat("segment/", 25) + "detail", Service: "orders-api-prod-" + strings.Repeat("x", 40),
		RangeS: 6 * 3600, Clamped: true, From: ccTo.Add(-6 * time.Hour), To: ccTo}
	for i := 0; i < envSetMax; i++ {
		env := "env-" + strings.Repeat("e", 30) + string(rune('a'+i))
		rows := []chstore.TraceRow{{TraceID: ccTraceA, DurationMs: 99999, HasError: true}, {TraceID: ccTraceC, DurationMs: 88888, HasError: true}}
		red := chstore.OpRED{Calls: 999999999, Errors: 99999999, P50Ms: 99999, P95Ms: 999999 + float64(i), Measured: true}
		if i < clusterDrillEnvMax {
			ev.Envs = append(ev.Envs, ccEnv(env, red, rows, maxShape, maxShape))
		} else {
			ev.Envs = append(ev.Envs, ccEnv(env, red, rows))
		}
	}
	txt := renderClusterCompareTR(ev)
	if n := strings.Count(txt, ccTraceA); n != clusterDrillEnvMax {
		t.Fatalf("%d kırılım satırı beklenir, %d:\n%s", clusterDrillEnvMax, n, txt)
	}
	if n := utf8.RuneCountInString(txt); n >= chatToolResultMaxRunes {
		t.Fatalf("küme kıyası metni %d rune, tavan %d", n, chatToolResultMaxRunes)
	}
	if _, cut := clampToolResultForModel(txt); cut {
		t.Fatal("kırpıldı")
	}
}

// TestClusterCompareWiring — kablolama pinleri: endpoint anma aşaması ortam
// kümesini hedefe yazar (iki dönüş noktası), ön-getirme kıyası kırılımla
// paralel başlatıp kırılımdan SONRA toplar, prompt eki Exceptions ekinin hemen
// ardında; hedef ortamsızken kıyas başlamaz (yol bayt bayt eski).
func TestClusterCompareWiring(t *testing.T) {
	src := readRepoFile(t, "chat_operation_prefetch.go")
	res := funcBody(src, "resolveEndpointMention")
	if strings.Count(res, "Envs = endpointEnvSet(raw, path, envs)") != 2 {
		t.Fatalf("resolveEndpointMention ortam kümesini iki dönüşte de yazmalı:\n%s", res)
	}
	fp := funcBody(src, "opPrefetchForTarget")
	i, j, k := strings.Index(fp, "cc := s.startClusterCompare(pctx, emit, t, svc, from, to)"), strings.Index(fp, "s.endpointDrillPhase(pctx, emit, p, t, svc, merged, tFrom, to)"), strings.Index(fp, "cc.finish(emit, p)")
	if i < 0 || j < 0 || k < 0 || !(i < j && j < k) || k > strings.Index(fp, `emit("block"`) {
		t.Fatalf("küme kıyası: başlat → kırılım → topla → kart sırası bozuk (%d %d %d)", i, j, k)
	}
	chat := readRepoFile(t, "copilot_chat.go")
	if !strings.Contains(chat, "opPre.exceptionsPromptTR() + // v0.10.1331 — operasyonun Exceptions grubu yoksa \"\"\n\t\topPre.clusterPromptTR() +") {
		t.Fatal("clusterPromptTR exceptionsPromptTR'nin hemen ardında olmalı")
	}
	if strings.Count(chat, "clusterPromptTR()") != 1 {
		t.Fatal("clusterPromptTR tek yerde")
	}
	// Ortamsız hedef: kıyas yok, nil no-op.
	s := &Server{store: &chstore.Store{}}
	if run := s.startClusterCompare(context.Background(), func(string, any) { t.Fatal("emit olmamalı") }, opTarget{Endpoint: true, Envs: []string{"eu-a"}}, "orders-api-prod", ccTo.Add(-time.Hour), ccTo); run != nil {
		t.Fatal("tek ortamda kıyas başlamamalı")
	}
	var p opPrefetch
	(*clusterCompareRun)(nil).finish(func(string, any) { t.Fatal("emit olmamalı") }, &p)
	if p.ClusterCompare != nil || p.clusterText != "" {
		t.Fatal("nil koşu paketi değiştirmemeli")
	}
}
