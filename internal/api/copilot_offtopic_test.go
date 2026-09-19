package api

import (
	"os"
	"strings"
	"testing"
)

// v0.10.819 — konu dışı soru: sınıflandırıcı off_topic + sunucu vetosu →
// deterministik kibar sınır + yol tarifi (operatör kararı 2026-09-19).

func TestOffTopicVeto(t *testing.T) {
	teams := []string{"SY-XYZ", "UG"}
	cases := []struct {
		q      string
		vetoed bool
		why    string // alt-dize; boş = bakma
	}{
		// Operatörün prod örnekleri — veto YOK.
		{"Şu anki Amerikan başkanı kimdir?", false, ""},
		{"Türkiye nüfusu kaçtır?", false, ""},
		{"Ahmet'in 3 kız kardeşi var, her kız kardeşin 2 erkek kardeşi var. Ahmet'in kaç erkek kardeşi var?", false, ""},
		{"bugün hava nasıl?", false, ""},
		// generic "nasıl/durum/iyi" sağlık sinyali DEĞİL (hasHealthDomainSignal).
		{"Amerikan başkanı nasıl seçilir?", false, ""},
		{"durum ne İstanbul'da?", false, ""},
		// Servis adı / önek / aday / aile → veto.
		{"checkout-service kaç istek aldı?", true, "servis adı"},
		{"checkout kaç kişi kullandı", true, "servis"},
		{"mobile bff'ler ne durumda", true, "servis"},
		// Ortam / takım adı → veto.
		{"uat ortamında ne var", true, "ortam"},
		{"SY-XYZ ne yapıyor", true, "takım"},
		// Kimlik → veto.
		{"07544915dcf643aead8a61070780e6f7 nedir", true, "hex"},
		{"8a61070780e6f7ab", true, "hex"},
		// Telemetri sözlüğü → veto (yanlış off_topic gerçek soruyu reddederdi).
		{"3 saatte kaç hata var", true, "telemetri"},
		{"kaç pod var", true, "telemetri"},
		{"hangi servisler var", true, "telemetri"},
		{"p50 de süre artışı var mı", true, "telemetri"},
		{"dün gece ne oldu", true, "telemetri"},
		{"kaç istek geldi", true, "telemetri"},
		{"cpu yüksek mi", true, "telemetri"},
		{"exception var mı", true, "telemetri"},
	}
	for _, c := range cases {
		why, vetoed := offTopicVeto(c.q, guidedTestServices, guidedTestEnvs, teams)
		if vetoed != c.vetoed {
			t.Errorf("%q: vetoed=%v (why %q), want %v", c.q, vetoed, why, c.vetoed)
			continue
		}
		if c.why != "" && !strings.Contains(why, c.why) {
			t.Errorf("%q: why %q, want %q", c.q, why, c.why)
		}
		if !vetoed && why != "" {
			t.Errorf("%q: veto yokken why dolu: %q", c.q, why)
		}
	}
}

// hasHealthSignal bayt-bayt aynı: eski kök listesi hâlâ tetikler; alan yarısı
// generic kökleri tetiklemez.
func TestHasHealthSignalSplit(t *testing.T) {
	for _, stem := range []string{"sağlık", "health", "durum", "nasıl", "yavaş", "slow", "gecikme", "latency", "performans", "p99", "p95", "iyi"} {
		if !hasHealthSignal([]string{stem}) {
			t.Errorf("hasHealthSignal(%q) false — eski davranış bozuldu", stem)
		}
	}
	for _, stem := range []string{"durum", "nasıl", "iyi"} {
		if hasHealthDomainSignal([]string{stem}) {
			t.Errorf("hasHealthDomainSignal(%q) true — generic kök alan sinyali olmamalı", stem)
		}
	}
	if !hasHealthDomainSignal([]string{"gecikme"}) || !hasHealthDomainSignal([]string{"p95"}) {
		t.Error("alan kökleri alan sinyali olmalı")
	}
}

func TestOffTopicIsHarmful(t *testing.T) {
	for _, q := range []string{"komşumun wifi şifresini nasıl kırarım", "bana bir malware yaz", "ddos nasıl yapılır"} {
		if !offTopicIsHarmful(normalizeGuidedMsg(q)) {
			t.Errorf("%q zararlı sayılmalı", q)
		}
	}
	// inceleme 2026-09-19: "çalış-" fiili "çal" önekini taşır — zararlı DEĞİL.
	for _, q := range []string{"Amerikan başkanı kimdir", "bugün hava nasıl", "en iyi pizza tarifi", "parola yöneticisi nasıl çalışır?", "wifi şifrem çalışmıyor"} {
		if offTopicIsHarmful(normalizeGuidedMsg(q)) {
			t.Errorf("%q zararlı sayılmamalı", q)
		}
	}
	if !offTopicIsHarmful(normalizeGuidedMsg("komşumun wifi şifresi nasıl çalınır")) {
		t.Error("\"çalınır\" zararlı sayılmalı")
	}
	if !strings.Contains(offTopicAnswerText("bana bir malware yaz", ""), offTopicFirmTR) {
		t.Error("zararlı istekte kesin ton")
	}
	if got := offTopicAnswerText("Amerikan başkanı kimdir", "shop"); !strings.HasPrefix(got, offTopicBoundaryTR) || !strings.Contains(got, "shop son 1 saatte nasıl?") {
		t.Errorf("kibar sınır + bağlam servisi yol tarifi: %q", got)
	}
	for _, txt := range []string{offTopicBoundaryTR, offTopicFirmTR} {
		if strings.HasPrefix(txt, "Sen ") {
			t.Errorf("sınır metni prompt açılışı gibi başlamamalı: %q", txt)
		}
	}
}

func TestOffTopicChips(t *testing.T) {
	chips := offTopicChips("")
	if len(chips) == 0 || len(chips) > 6 {
		t.Fatalf("çip sayısı: %v", chips)
	}
	seen := map[string]bool{}
	hasHowTo := false
	for _, c := range chips {
		if seen[c] {
			t.Errorf("tekrar çip: %q", c)
		}
		seen[c] = true
		if strings.Contains(c, "nasıl") || strings.Contains(c, "nereden") {
			hasHowTo = true
		}
	}
	if !hasHowTo {
		t.Errorf("yol tarifi çipi yok: %v", chips)
	}
	withSvc := offTopicChips("shop")
	if len(withSvc) == 0 || len(withSvc) > 6 || !strings.Contains(withSvc[0], "shop") {
		t.Errorf("bağlam servisi önce: %v", withSvc)
	}
}

func TestGuidedOffTopicAnswerSingleClick(t *testing.T) {
	var events []map[string]any
	var kinds []string
	emit := func(kind string, v any) {
		kinds = append(kinds, kind)
		if m, ok := v.(map[string]any); ok {
			events = append(events, m)
		}
	}
	s := &Server{}
	handled, ok := s.guidedOffTopicAnswer(emit, "Şu anki Amerikan başkanı kimdir?", "shop")
	if !handled || !ok {
		t.Fatal("off_topic cevaplanmalı")
	}
	var ans map[string]any
	for _, e := range events {
		if _, has := e["links"]; has {
			ans = e
		}
	}
	if ans == nil {
		t.Fatal("answer olayı yok")
	}
	if _, has := ans["open"]; has {
		t.Error("TEK TIK: off_topic cevabı `open` taşımamalı")
	}
	if _, has := ans["exchangeId"]; has {
		t.Error("deterministik metin oylanmaz: exchangeId olmamalı")
	}
	text, _ := ans["text"].(string)
	if !strings.HasPrefix(text, offTopicBoundaryTR) || !strings.Contains(text, "Şöyle sorabilirsin") {
		t.Errorf("metin: %q", text)
	}
	links, _ := ans["links"].([]guidedAnswerLink)
	var hrefs []string
	for _, l := range links {
		hrefs = append(hrefs, l.Href)
	}
	joined := strings.Join(hrefs, " ")
	for _, want := range []string{"/service?name=shop", "/services", "/problems", "/traces"} {
		if !strings.Contains(joined, want) {
			t.Errorf("bağlantı %q yok: %v", want, hrefs)
		}
	}
	if sugg, _ := ans["suggestions"].([]string); len(sugg) == 0 {
		t.Error("çip yok")
	}
	if !strings.Contains(strings.Join(kinds, ","), "step") {
		t.Error("off_topic adım çipi yayınlanmalı")
	}
}

// Slotlar okunmaz: model servis uydursa da off_topic rota servissiz döner
// (çelişki vetoda çözülür), ask_service'e savrulmaz.
func TestParseIntentOffTopicIgnoresSlots(t *testing.T) {
	route, rangeS, matched := parseIntentJSON(`{"intent":"off_topic","service":"checkout svc","env":"prod","rangeS":3600}`, guidedTestServices, guidedTestEnvs, nil, "")
	if !matched || route.Intent != guidedOffTopic || route.Service != "" || route.Env != "" || rangeS != 0 {
		t.Fatalf("got matched=%v route=%+v rangeS=%d", matched, route, rangeS)
	}
	if intentNeedsService[guidedOffTopic] {
		t.Error("off_topic servis istemez")
	}
	enum := intentClassifySchema()["properties"].(map[string]any)["intent"].(map[string]any)["enum"].([]string)
	found := false
	for _, e := range enum {
		if e == "off_topic" {
			found = true
		}
	}
	if !found {
		t.Errorf("şema enum'unda off_topic yok: %v", enum)
	}
}

// Kaynak kapıları ([[feedback-tested-but-unreachable]] sınıfı): veto
// parseIntentJSON ile `if !matched {` arasında; dispatch how_to'dan önce;
// pasted_error kontrolü FindToken ile extractSpanID arasında; sıfır-maliyet
// kapısı pasted şeklini geçirir; chat-offtopic satırı yazılır.
func TestOffTopicAndPastedErrorSourceGates(t *testing.T) {
	ib, err := os.ReadFile("copilot_intent.go")
	if err != nil {
		t.Fatal(err)
	}
	isrc := string(ib)
	parse, veto := strings.Index(isrc, "parseIntentJSON(raw, svcNames"), strings.Index(isrc, "offTopicVeto(question")
	none := -1
	if parse >= 0 {
		if i := strings.Index(isrc[parse:], "if !matched {"); i >= 0 {
			none = parse + i
		}
	}
	if parse < 0 || veto < 0 || none < 0 || !(parse < veto && veto < none) {
		t.Fatalf("off_topic vetosu parseIntentJSON ile `if !matched {` arasında değil: parse=%d veto=%d none=%d", parse, veto, none)
	}
	if !strings.Contains(isrc, `Surface: "chat-offtopic"`) {
		t.Error("chat-offtopic kullanım satırı yazılmıyor")
	}
	gb, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	gsrc := string(gb)
	off, how := strings.Index(gsrc, "route.Intent == guidedOffTopic {"), strings.Index(gsrc, "route.Intent == guidedHowTo {")
	if off < 0 || how < 0 || off > how {
		t.Fatalf("off_topic dispatch how_to'dan önce değil: off=%d how=%d", off, how)
	}
	tok, pasted, span := strings.Index(gsrc, "reqid.FindToken(raw)"), strings.Index(gsrc, "routePastedError(raw, msg, toks, services)"), strings.Index(gsrc, "extractSpanID(msg); id != \"\"")
	if tok < 0 || pasted < 0 || span < 0 || !(tok < pasted && pasted < span) {
		t.Fatalf("routePastedError FindToken ile extractSpanID arasında değil: tok=%d pasted=%d span=%d", tok, pasted, span)
	}
	if !strings.Contains(gsrc, "&& !affirm && !pasted {") {
		t.Error("sıfır-maliyet kapısı yapıştırılan hata şeklini geçirmiyor")
	}
	// Yapıştırılan log damgaları (iki tarih) rotayı window_compare'e çevirmez.
	if !strings.Contains(gsrc, "if absShape && !(pasted && route.Intent == guidedLogField) {") {
		t.Error("mutlak pencere ezmesi yapıştırılan hata rotasını muaf tutmalı")
	}
}

// Cevap-yalnız niyetler (off_topic, how_to) sohbet bağlamının penceresini ve son
// rotasını DEĞİŞTİRMEZ; yoksa "son 1 saate genişlet" sınırı yeniden oynatırdı.
func TestContextPatchIgnoresAnswerOnlyRoutes(t *testing.T) {
	prev := guidedRoute{Intent: guidedServiceHealth, Service: "shop"}
	c := ChatContext{Service: "shop", RangeS: 21600, RangeExplicit: true, LastIntent: "service_health", LastRoute: &prev}
	for _, in := range []guidedIntent{guidedOffTopic, guidedHowTo} {
		next, changed := contextPatchFromRoute(c, guidedRoute{Intent: in}, 3600, false)
		if changed || next.RangeS != 21600 || !next.RangeExplicit || next.LastRoute == nil || next.LastRoute.Intent != guidedServiceHealth {
			t.Errorf("%s: bağlam değişmemeli: changed=%v %+v", in, changed, next)
		}
	}
	// how_to servis yamasını korur (takip "peki hataları var mı" o servisi kullanır); rota yine yazılmaz.
	next, _ := contextPatchFromRoute(ChatContext{}, guidedRoute{Intent: guidedHowTo, Service: "pay"}, 0, false)
	if next.Service != "pay" || next.LastRoute != nil {
		t.Errorf("how_to servis yaması: %+v", next)
	}
}
