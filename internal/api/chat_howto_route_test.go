package api

import (
	"os"
	"strings"
	"testing"
	"time"
)

// v0.10.2140 — CoSRE router boşlukları D7 (docs/cosre/router-gaps-audit.md
// I, J, K). Belirti (prod /ai, 7 gün): "trace detaile hangi menüden
// gideceğim", "tablo insertlerini hangi adımda görebilirim", nöbet çizelgesi
// ve tarih aralıklı değer araması deterministik kademeden kaçıp genel cevaba
// / serbest döngüye düşüyordu. Yalnız v2; v1 yolu değişmez.

func TestHasHowToNavShape(t *testing.T) {
	cases := map[string]bool{
		"trace detaile hangi menüden gideceğim":       true,
		"tablo insertlerini hangi adımda görebilirim": true,
		"trace id'yi nereye yazacağım":                true,
		"loglara nereden bakabilirim":                 true,
		"svc-a hata oranı nereden görürüm":            true,
		"öznitelikle trace nasıl ararım":              true,
		"trace detayına nasıl giderim":                true,
		// veri soruları — yol tarifi DEĞİL
		"svc-a nereden çağrılıyor":      false,
		"hata nereden geliyor":          false,
		"hata nerede görülüyor":         false,
		"svc-a nasıl":                   false,
		"svc-a sağlığı nasıl":           false,
		"hangi adımda yavaşlıyor":       false,
		"nereye istek atıyor":           false,
		"svc-a hangi servisi çağırıyor": false,
	}
	for q, want := range cases {
		n := normalizeGuidedMsg(q)
		if got := hasHowToNavShape(n, guidedTokens(n)); got != want {
			t.Errorf("%q → %v, istenen %v", q, got, want)
		}
	}
}

func TestHasOncallAsk(t *testing.T) {
	cases := map[string]bool{
		"16 kasım haftası nöbetçileri":    true,
		"bu hafta nöbetçi kim":            true,
		"nöbet çizelgesi nerede":          true,
		"on-call kim":                     true,
		"gece nöbetinde ne oldu":          false, // vardiya özeti
		"nöbet devrinde hata arttı mı":    false,
		"svc-a kim tarafından çağrılıyor": false,
	}
	for q, want := range cases {
		n := normalizeGuidedMsg(q)
		if got := hasOncallAsk(n, guidedTokens(n)); got != want {
			t.Errorf("%q → %v, istenen %v", q, got, want)
		}
	}
}

func TestV2AttrValueAsk(t *testing.T) {
	cases := []struct {
		q, val string
		ok     bool
	}{
		{"siparis no 73066 olan trace'leri bul", "73066", true},
		{"musteri numarası: AB-1200 için trace ara", "AB-1200", true},
		{"order id 99812 trace getir", "99812", true},
		{"trace id nedir, trace ara", "", false},            // değer rakamsız
		{"siparis no 73066 neden yavaş", "", false},         // trace sözcüğü yok
		{"siparis no 73066 trace'i neden yavaş", "", false}, // arama fiili yok
		{"en yavaş trace'leri bul", "", false},
	}
	for _, c := range cases {
		n := normalizeGuidedMsg(c.q)
		val, ok := v2AttrValueAsk(c.q, guidedTokens(n))
		if ok != c.ok || val != c.val {
			t.Errorf("%q → (%q, %v), istenen (%q, %v)", c.q, val, ok, c.val, c.ok)
		}
	}
}

func TestV2LongSearchWindow(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, loc)
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, loc) }
	cases := []struct {
		q        string
		ok       bool
		from, to time.Time
		label    string
	}{
		{"7 eylülden 15 eylüle kadar trace ara", true, d(2026, 9, 7), d(2026, 9, 16), "7 Eylül – 15 Eylül"},
		{"7 Eylül - 15 Eylül arası", true, d(2026, 9, 7), d(2026, 9, 16), "7 Eylül – 15 Eylül"},
		{"7-15 eylül arası", true, d(2026, 9, 7), d(2026, 9, 16), "7 Eylül – 15 Eylül"},
		{"28 aralıktan 3 ocağa kadar", true, d(2025, 12, 28), d(2026, 1, 4), "28 Aralık – 3 Ocak"},
		{"1 aralıktan 5 aralığa", true, d(2025, 12, 1), d(2025, 12, 6), "1 Aralık – 5 Aralık"}, // gelecek → geçen yıl
		{"son 7 günde trace ara", true, now.AddDate(0, 0, -7), now, "son 7 gün"},
		{"son 2 hafta", true, now.AddDate(0, 0, -14), now, "son 2 hafta"},
		// 24 saate sığan / okunamayan → mevcut yol
		{"son 1 gün", false, time.Time{}, time.Time{}, ""},
		{"son 6 saat", false, time.Time{}, time.Time{}, ""},
		{"7 eylülden 7 eylüle", false, time.Time{}, time.Time{}, ""},
		{"7 eylül 10-12 arası", false, time.Time{}, time.Time{}, ""},
		{"dün", false, time.Time{}, time.Time{}, ""},
	}
	for _, c := range cases {
		w, ok := v2LongSearchWindow(c.q, now, loc)
		if ok != c.ok {
			t.Errorf("%q → ok=%v, istenen %v (%+v)", c.q, ok, c.ok, w)
			continue
		}
		if ok && (!w.From.Equal(c.from) || !w.To.Equal(c.to) || w.Label != c.label) {
			t.Errorf("%q → %s..%s %q, istenen %s..%s %q", c.q, w.From, w.To, w.Label, c.from, c.to, c.label)
		}
	}
}

func TestV2HowToRoute(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		in    guidedRoute
		q     string
		svc   string
		want  guidedIntent
		label string
	}{
		{"yol sorusu, rota yok", guidedRoute{}, "trace detaile hangi menüden gideceğim", "", guidedHowTo, "how_to"},
		{"yol sorusu, find_entity", guidedRoute{Intent: guidedFindEntity}, "loglara nereden bakabilirim", "", guidedHowTo, "how_to"},
		{"yol sorusu veri rotasında, konu var", guidedRoute{Intent: guidedServiceHealth, Service: "svc-a"}, "svc-a hata oranı nereden görürüm", "svc-a", guidedHowTo, "how_to"},
		{"yol sorusu veri rotasında, konu yok → aynen", guidedRoute{Intent: guidedServiceHealth, Service: "svc-a"}, "svc-a nereden bakılır", "svc-a", guidedServiceHealth, "service_health"},
		{"güçlü rota değişmez", guidedRoute{Intent: guidedServiceTopology, Service: "svc-a"}, "svc-a bağımlılıklarını nereden görürüm", "svc-a", guidedServiceTopology, "service_topology"},
		{"servissiz open_page + v2 konusu", guidedRoute{Intent: guidedOpenPage, Page: "overview"}, "trace detay sayfasını açar mısın", "", guidedHowTo, "how_to"},
		{"servissiz open_page, v1 konusu → aynen (servis sorulur)", guidedRoute{Intent: guidedOpenPage, Page: "overview"}, "service detail sayfasını açar mısın", "", guidedOpenPage, "open_page"},
		{"servisli open_page aynen", guidedRoute{Intent: guidedOpenPage, Service: "svc-a", Page: "overview"}, "svc-a trace detay sayfasını açar mısın", "svc-a", guidedOpenPage, "open_page"},
		{"nöbet", guidedRoute{}, "16 kasım haftası nöbetçileri", "", guidedHowTo, guidedNotAvailableRoute},
		{"nöbet sözcüğü vardiya rotasını ezmez", guidedRoute{Intent: guidedShiftSummary}, "gece nöbetçi kim, ne oldu", "", guidedShiftSummary, "shift_summary"},
		{"değer araması 24 sa içinde", guidedRoute{}, "siparis no 73066 olan trace'leri bul", "", guidedTraceSearch, "trace_search"},
		{"değer araması uzun aralık", guidedRoute{}, "siparis no 73066 için 7 eylülden 15 eylüle kadar trace ara", "", guidedTraceSearch, guidedTraceSearchLinkRoute},
		{"düz soru aynen", guidedRoute{Intent: guidedProblems}, "açık problemler neler", "", guidedProblems, "problems"},
	}
	for _, c := range cases {
		got, _ := v2HowToRoute(c.in, c.q, normalizeGuidedMsg(c.q), c.svc, true, now, time.UTC)
		if got.Intent != c.want || guidedRouteLabel(got) != c.label {
			t.Errorf("%s: %q → %s (%s), istenen %s (%s)", c.name, c.q, got.Intent, guidedRouteLabel(got), c.want, c.label)
		}
		// v1: hiçbir satır değişmez.
		if v1, changed := v2HowToRoute(c.in, c.q, normalizeGuidedMsg(c.q), c.svc, false, now, time.UTC); changed || v1.Intent != c.in.Intent {
			t.Errorf("%s: v1 yolu değişti → %s", c.name, v1.Intent)
		}
	}
	r, _ := v2HowToRoute(guidedRoute{}, "svc-a siparis no 73066 trace ara", "svc-a siparis no 73066 trace ara", "svc-a", true, now, time.UTC)
	if r.SearchText != "73066" || r.Service != "svc-a" || r.LinkOnly != nil {
		t.Errorf("değer araması alanları: %+v", r)
	}
}

func d7Answer(t *testing.T, route guidedRoute) map[string]any {
	t.Helper()
	var ans map[string]any
	n := 0
	emit := func(kind string, v any) {
		n++
		if kind == "answer" {
			ans, _ = v.(map[string]any)
		}
	}
	handled, ok := (&Server{}).guidedD7DirectAnswer(emit, route)
	if !handled || !ok || ans == nil || n != 1 {
		t.Fatalf("tek answer olayı bekleniyordu (handled=%v ok=%v olay=%d)", handled, ok, n)
	}
	return ans
}

func TestOncallAnswerIsHonestAndBare(t *testing.T) {
	ans := d7Answer(t, guidedRoute{Intent: guidedHowTo, NotAvailable: "oncall"})
	text, _ := ans["text"].(string)
	if !strings.HasPrefix(text, "Nöbet çizelgesi Coremetry'de yok") {
		t.Errorf("metin: %q", text)
	}
	for _, k := range []string{"links", "suggestions", "open"} { // karar 1: kaynak yok, how_to çipi yok
		if _, has := ans[k]; has {
			t.Errorf("nöbet cevabı %q taşımamalı", k)
		}
	}
}

func TestTraceSearchLinkOnlyAnswer(t *testing.T) {
	from := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	route := guidedRoute{Intent: guidedTraceSearch, Service: "svc a", SearchText: "73066 x",
		LinkOnly: &traceLinkOnly{From: from, To: to, Label: "7 Eylül – 15 Eylül"}}
	ans := d7Answer(t, route)
	if _, has := ans["open"]; has {
		t.Error("TEK TIK: link-yalnız cevap `open` taşımamalı")
	}
	text, _ := ans["text"].(string)
	if !strings.Contains(text, "24 saatlik") || !strings.Contains(text, "7 Eylül – 15 Eylül") || !strings.Contains(text, "burada aramadım") {
		t.Errorf("metin: %q", text)
	}
	links, _ := ans["links"].([]guidedAnswerLink)
	if len(links) != 1 {
		t.Fatalf("tek link: %v", links)
	}
	href := links[0].Href
	for _, want := range []string{"/traces?search=73066+x", "service=svc+a", "range=custom"} {
		if !strings.Contains(href, want) {
			t.Errorf("href %q içinde %q yok", href, want)
		}
	}
	if !strings.Contains(href, "1788739200000") && !strings.Contains(href, "1788") { // 2026-09-07 UTC ms öneki
		t.Errorf("href pencereyi taşımıyor: %q", href)
	}
	// Bayraksız rota doğrudan cevap değil (mevcut demet koşar).
	if h, _ := (&Server{}).guidedD7DirectAnswer(func(string, any) {}, guidedRoute{Intent: guidedTraceSearch, SearchText: "x"}); h {
		t.Error("LinkOnly yokken trace_search doğrudan cevaplanmamalı")
	}
	if h, _ := (&Server{}).guidedD7DirectAnswer(func(string, any) {}, guidedRoute{Intent: guidedHowTo}); h {
		t.Error("NotAvailable yokken how_to kılavuz yolunda kalmalı")
	}
}

// v2 kılavuz cevabı: yeni konu, link çipi, `open` YOK; v1 aynı soruda yeni
// konuyu görmez.
func TestGuidedHowToV2TopicsSingleClick(t *testing.T) {
	answer := func(v2 bool, q string) map[string]any {
		var ans map[string]any
		emit := func(kind string, v any) {
			if kind == "answer" {
				ans, _ = v.(map[string]any)
			}
		}
		(&Server{}).guidedHowToAnswerFor(emit, guidedRoute{Intent: guidedHowTo}, q, "", v2)
		return ans
	}
	for q, title := range map[string]string{
		"trace detaile hangi menüden gideceğim":       "NASIL: Trace detayına gitmek",
		"tablo insertlerini hangi adımda görebilirim": "NASIL: Veritabanı çağrılarını",
		"öznitelikle trace nasıl ararım":              "özniteliğe göre aramak",
	} {
		ans := answer(true, q)
		text, _ := ans["text"].(string)
		if !strings.Contains(text, title) {
			t.Errorf("v2 %q → %q", q, text)
		}
		if _, has := ans["open"]; has {
			t.Errorf("v2 %q: `open` olmamalı (v0.10.809)", q)
		}
		if links, _ := ans["links"].([]guidedAnswerLink); len(links) == 0 {
			t.Errorf("v2 %q: link çipi yok", q)
		}
		if v1, _ := answer(false, q)["text"].(string); strings.Contains(v1, title) {
			t.Errorf("v1 %q yeni konuyu görmemeli: %q", q, v1)
		}
	}
	if text, _ := answer(true, "lorem ipsum")["text"].(string); !strings.Contains(text, "Trace detayına nasıl giderim?") {
		t.Errorf("v2 dizini yeni konuları listelemeli: %q", text)
	}
	if text, _ := answer(false, "lorem ipsum")["text"].(string); strings.Contains(text, "Trace detayına nasıl giderim?") {
		t.Errorf("v1 dizini değişmemeli: %q", text)
	}
}

// Kablolama: D7 düzeltmesi v2 kapılı çağrılıyor, sıfır-maliyet kapısı ve
// endpoint kartı şekli tanıyor, dispatch doğrudan cevabı kılavuzdan önce.
func TestV2HowToWiring(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	g := read("copilot_guided.go")
	for _, want := range []string{
		"!(v2 && v2HowToGate(norm))",
		"v2HowToRoute(route, question, norm, extractServiceFullName(norm, svcNames), v2,",
		"s.guidedD7DirectAnswer(emit, route)",
		"s.guidedHowToAnswerFor(emit, route, question, ctxService, cosreV2Marked(ctx))",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("copilot_guided.go: %q yok", want)
		}
	}
	if i, j := strings.Index(g, "s.guidedD7DirectAnswer(emit, route)"), strings.Index(g, "s.guidedHowToAnswerFor(emit, route"); i < 0 || j < 0 || i > j {
		t.Error("doğrudan cevap kılavuz dalından ÖNCE olmalı")
	}
	if !strings.Contains(read("chat_endpoint_pick.go"), "v2HowToGate(normalizeGuidedMsg(q))") {
		t.Error("endpoint kartı yol sorusunu atlamalı")
	}
}
