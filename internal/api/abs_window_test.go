package api

import (
	"github.com/cilcenk/coremetry/internal/tzdefault"
	"os"
	"strings"
	"testing"
	"time"
)

// abs_window_test.go — v0.10.437 (CoSRE router boşlukları D6).

func TestExtractAbsoluteWindows(t *testing.T) {
	ist := time.FixedZone("UTC+3", 3*3600)
	now := time.Date(2026, 9, 6, 15, 0, 0, 0, ist)
	at := func(y int, m time.Month, d, h, mi int) time.Time { return time.Date(y, m, d, h, mi, 0, 0, ist) }
	cases := []struct {
		msg  string
		want []absWindow
	}{
		{"08/08/2026 saat 04-08 ile 08-09 arası checkout servis süreleri", []absWindow{{at(2026, 8, 8, 4, 0), at(2026, 8, 8, 8, 0)}, {at(2026, 8, 8, 8, 0), at(2026, 8, 8, 9, 0)}}},
		{"2026-08-08 04:00-08:30 arası hatalar", []absWindow{{at(2026, 8, 8, 4, 0), at(2026, 8, 8, 8, 30)}}},
		{"8 ağustos 2026 tüm gün nasıldı", []absWindow{{at(2026, 8, 8, 0, 0), at(2026, 8, 9, 0, 0)}}},
		{"bugün 09 ile 10 arası", []absWindow{{at(2026, 9, 6, 9, 0), at(2026, 9, 6, 10, 0)}}},
		// gelecekteki saat → dün; gece sarması → ertesi gün
		{"22-02 arası neler oldu", []absWindow{{at(2026, 9, 5, 22, 0), at(2026, 9, 6, 2, 0)}}},
		{"07/08/2026 22-02 arası", []absWindow{{at(2026, 8, 7, 22, 0), at(2026, 8, 8, 2, 0)}}},
		{"08/08/2026 04-08 ile 09/08/2026 04-08 kıyas", []absWindow{{at(2026, 8, 8, 4, 0), at(2026, 8, 8, 8, 0)}, {at(2026, 8, 9, 4, 0), at(2026, 8, 9, 8, 0)}}},
		{"son 2 saatte checkout nasıl", nil},
		{"checkout p95 nedir", nil},
		// v0.10.443 — çıplak sayı çiftleri pencere değil.
		{"son 1-2 saatte checkout nasıl", nil},
		{"5-10 dakika içinde toparlandı mı", nil},
		{"3-4 istek gelmiş", nil},
		{"saat 4-8 arası neler oldu", []absWindow{{at(2026, 9, 6, 4, 0), at(2026, 9, 6, 8, 0)}}},
	}
	for _, c := range cases {
		got, ok := extractAbsoluteWindows(c.msg, now, ist)
		if (c.want == nil) != !ok {
			t.Errorf("%q → ok=%v %+v", c.msg, ok, got)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%q → %d pencere, want %d: %+v", c.msg, len(got), len(c.want), got)
			continue
		}
		for i := range got {
			if !got[i].From.Equal(c.want[i].From) || !got[i].To.Equal(c.want[i].To) {
				t.Errorf("%q [%d] → %s–%s, want %s–%s", c.msg, i, got[i].From, got[i].To, c.want[i].From, c.want[i].To)
			}
		}
	}
	if !looksLikeAbsoluteWindow("08/08/2026 04-08") || looksLikeAbsoluteWindow("checkout nasıl") || looksLikeAbsoluteWindow("son 1-2 saatte checkout nasıl") {
		t.Fatal("kapı")
	}
	if chatLocation(180).String() != "UTC+3" || chatLocation(0) != time.UTC || chatLocation(9999) != time.UTC {
		t.Fatal("konum")
	}
	// v0.10.445 — IANA adı DST'yi bilir: Berlin'de 8 Ağustos 04:00 = 02:00Z (CEST), ofset +60 olsaydı 03:00Z olurdu.
	berlin := chatLocationNamed("Europe/Berlin", 60)
	if berlin.String() != "Europe/Berlin" {
		t.Fatalf("IANA konumu: %s", berlin)
	}
	if wins, ok := extractAbsoluteWindows("08/08/2026 04-08 arası", now, berlin); !ok || wins[0].From.UTC().Hour() != 2 {
		t.Fatalf("DST: %+v", wins)
	}
	if chatLocationNamed("Not/AZone", 180).String() != "UTC+3" || chatLocationNamed("../etc", 0) != time.UTC || chatLocationNamed("", 330).String() != "UTC+5:30" {
		t.Fatal("geçersiz/boş ad ofsete düşer")
	}
	// v0.10.746 — ad yok/çözülmedi VE ofset 0 = dilim gönderilmemiş → sunucu
	// varsayılanı (COREMETRY_TZ; test ortamında env yok → UTC). UTC tarayıcı
	// "UTC" ADINI gönderir ve yukarıda çözülür, bu basamağa düşmez.
	if chatLocationNamed("", 0) != tzdefault.Location() || chatLocationNamed("../etc", 0) != tzdefault.Location() || chatLocationNamed("UTC", 0) != time.UTC {
		t.Fatal("dilimsiz istek sunucu varsayılanına inmeli")
	}
	// v0.10.444 — yarım saatlik ofsetler dakikayla etiketlenir.
	if chatLocation(330).String() != "UTC+5:30" || chatLocation(-210).String() != "UTC-3:30" || chatLocation(345).String() != "UTC+5:45" {
		t.Fatalf("yarım saat etiketi: %s %s", chatLocation(330), chatLocation(-210))
	}
	if l := absWindowLabel(absWindow{at(2026, 8, 8, 4, 0), at(2026, 8, 8, 8, 0)}, ist); l != "08/08 04:00–08:00" {
		t.Fatalf("etiket: %s", l)
	}
	if txt := absWindowText("08/08/2026 saat 04-08 ile 08-09 arası"); txt != "08/08/2026 ile 04-08 ile 08-09" {
		t.Fatalf("pencere metni: %q", txt)
	}
}

func TestApplyAbsoluteWindowsAndRender(t *testing.T) {
	ist := time.FixedZone("UTC+3", 3*3600)
	w1 := absWindow{time.Date(2026, 8, 8, 4, 0, 0, 0, ist), time.Date(2026, 8, 8, 8, 0, 0, 0, ist)}
	w2 := absWindow{time.Date(2026, 8, 8, 8, 0, 0, 0, ist), time.Date(2026, 8, 8, 9, 0, 0, 0, ist)}
	now := time.Now()
	// Tek pencere: rota aynı, çıpa ve uzunluk pencere.
	r, to, rs, label := applyAbsoluteWindows(guidedRoute{Intent: guidedServiceHealth, Service: "checkout"}, []absWindow{w1}, "", now, 1800, ist, "x")
	if r.Intent != guidedServiceHealth || !to.Equal(w1.To) || rs != 4*3600 || !strings.HasPrefix(label, "pencere: 08/08 04:00–08:00") {
		t.Fatalf("tek pencere: %+v %s %d %q", r, to, rs, label)
	}
	// İki pencere + servis → window_compare; servissiz + bağlam → bağlam; hiçbiri → sor.
	r, _, _, label = applyAbsoluteWindows(guidedRoute{Intent: guidedNone, Service: "checkout"}, []absWindow{w1, w2}, "", now, 1800, ist, "08/08/2026 ile 04-08 ile 08-09")
	if r.Intent != guidedWindowCompare || r.Service != "checkout" || len(r.Windows) != 2 || !strings.HasPrefix(label, "kıyas: ") {
		t.Fatalf("kıyas: %+v %q", r, label)
	}
	r, _, _, _ = applyAbsoluteWindows(guidedRoute{}, []absWindow{w1, w2}, "payments", now, 1800, ist, "t")
	if r.Intent != guidedWindowCompare || r.Service != "payments" {
		t.Fatalf("bağlam servisi: %+v", r)
	}
	r, _, _, _ = applyAbsoluteWindows(guidedRoute{}, []absWindow{w1, w2}, "", now, 1800, ist, "08/08/2026 ile 04-08 ile 08-09")
	if r.Intent != guidedAskService || r.AskIntent != guidedWindowCompare || r.WindowText == "" {
		t.Fatalf("servissiz sor: %+v", r)
	}
	// Çip pencere metnini taşır ve pencereler yeniden çıkarılır.
	r.ServiceOptions = []string{"checkout-service"}
	chips := guidedSuggestions(r)
	if len(chips) != 1 || !strings.Contains(chips[0], "08/08/2026 ile 04-08 ile 08-09") {
		t.Fatalf("çip: %v", chips)
	}
	if wins, ok := extractAbsoluteWindows(chips[0], now, ist); !ok || len(wins) != 2 {
		t.Fatalf("çipten pencereler: %v %+v", ok, wins)
	}
	// Linkler kendi range'iyle; render deterministik.
	links := guidedAnswerLinkTargets(guidedRoute{Intent: guidedWindowCompare, Service: "checkout", Windows: []absWindow{w1, w2}})
	if len(links) != 2 || !strings.HasPrefix(links[0].Href, "/service?name=checkout&range=custom:") || !strings.Contains(links[1].Label, "08/08 08:00–09:00") {
		t.Fatalf("linkler: %+v", links)
	}
	ev := renderWindowCompareTR("checkout", []absWindow{w1, w2}, []aiRED{{Spans: 1000, Rate: 2, ErrorRate: 1.5, ErrorCount: 15, P50Ms: 40, P95Ms: 200, P99Ms: 900}, {Spans: 500, Rate: 1, ErrorRate: 3, ErrorCount: 15, P50Ms: 60, P95Ms: 400, P99Ms: 1500}}, ist)
	for _, want := range []string{"Pencere 1 08/08 04:00–08:00: 1000 span", "p95 200 ms", "p99 1.50 s", "trafik -50%", "p95 +100%", "hata oranı 1.50 → 3.00 puan"} {
		if !strings.Contains(ev, want) {
			t.Errorf("kanıt %q içermeli:\n%s", want, ev)
		}
	}
	if !strings.Contains(renderWindowCompareTR("x", []absWindow{w1, w2}, []aiRED{{}, {}}, ist), "span verisi yok") {
		t.Fatal("boş pencereler dürüst")
	}
	for _, sg := range guidedSuggestions(guidedRoute{Intent: guidedWindowCompare, Service: "checkout"}) {
		if routeGuidedIntent(sg, []string{"checkout"}, nil, nil, "").Intent == guidedNone {
			t.Errorf("öneri yönlenmeli: %q", sg)
		}
	}
}

// v0.10.944 (inceleme) — guided window_compare route.Env'i yok sayıyordu:
// service_summary_5m ortam taşımaz, prod + uat birleşiyordu ve kanıt bunu
// söylemiyordu. Ortamlı okuma (service_env_summary_5m) ya da açık not.
func TestWindowCompareEvidenceEnv(t *testing.T) {
	ist := time.FixedZone("TRT", 3*3600)
	w1 := absWindow{From: time.Date(2026, 9, 25, 10, 0, 0, 0, ist), To: time.Date(2026, 9, 25, 11, 0, 0, 0, ist)}
	w2 := absWindow{From: time.Date(2026, 9, 26, 10, 0, 0, 0, ist), To: time.Date(2026, 9, 26, 11, 0, 0, 0, ist)}
	reds := []aiRED{{Spans: 100, P99Ms: 900}, {Spans: 120, P99Ms: 1200}}
	wins := []absWindow{w1, w2}

	// env yok: eski kanıt + kaynak satırı birebir.
	ev, src := windowCompareEvidenceTR("checkout", "", false, wins, reds, ist)
	if ev != renderWindowCompareTR("checkout", wins, reds, ist) || !strings.HasPrefix(src, "service_summary_5m (iki pencere: ") || strings.Contains(src, "ortam") {
		t.Errorf("env'siz kanıt değişmemeli: %q", src)
	}
	// env istendi ama MV kapsamıyor: birleşik okuma + açık not.
	ev, src = windowCompareEvidenceTR("checkout", "uat", false, wins, reds, ist)
	if !strings.HasPrefix(ev, `Not: RED değerleri tüm ortamların toplamı — "uat" ortam süzgeci uygulanmadı`) || !strings.Contains(ev, "farkı uat ortamına atfetme") {
		t.Errorf("uygulanmayan env notu yok:\n%s", ev)
	}
	if !strings.HasSuffix(src, ", ortam: uat (uygulanmadı — tüm ortamlar)") {
		t.Errorf("kaynak satırı: %q", src)
	}
	// env + kapsama: iki pencere de ortamlı; kaynak notu okunan tabloyu söyler.
	ev, src = windowCompareEvidenceTR("checkout", "uat", true, wins, reds, ist)
	if strings.Contains(ev, "uygulanmadı") || !strings.Contains(ev, `yalnız "uat" ortamından`) ||
		!strings.Contains(ev, "sayılar service_env_summary_5m ön-toplamından") || strings.Contains(ev, "sayılar service_summary_5m") {
		t.Errorf("ortamlı kanıt:\n%s", ev)
	}
	if !strings.HasPrefix(src, "service_env_summary_5m (ortam: uat; iki pencere: ") {
		t.Errorf("ortamlı kaynak satırı: %q", src)
	}
}

// Kaynaktan pin: bundle env'i ya İKİ pencerede uygular ya hiç (karışık
// okuma yok) ve çipte yalnız uygulanan süzgeç görünür.
func TestWindowCompareBundleEnvIsAllOrNothing(t *testing.T) {
	b, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *Server) guidedWindowCompareBundle(")
	if i < 0 {
		t.Fatal("bundle yok")
	}
	body := src[i:]
	if j := strings.Index(body, "\n}\n"); j > 0 {
		body = body[:j]
	}
	for _, want := range []string{"s.store.EnvSummaryCovers(ctx, earliest)", "s.store.ServiceEnvWindowRED(", "withEnvArg(stepArgs, route.Env)", "windowCompareEvidenceTR("} {
		if !strings.Contains(body, want) {
			t.Errorf("bundle %q içermeli", want)
		}
	}
	// kapsama kararı döngüden ÖNCE, tek kez (pencere başına ayrı karar karışık okuma üretirdi)
	if strings.Index(body, "scoped :=") > strings.Index(body, "for i, w := range route.Windows") {
		t.Error("ortam kararı pencere döngüsünden önce verilmeli")
	}
}
