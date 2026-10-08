package api

// v0.10.1126 — operatör (prod): "BSA cache refresh nasıl girilir" / "Config
// cache refresh nasıl girilir?" / "Coremetry adresleri" wiki'ye uğramadı;
// guided "Hangi servisi kastettin? Adaylar: …" ile bitirdi. Sözleşme:
//   - geniş nasıl-yapılır işareti ZAYIF (taban kapılı); telemetri "nasıl"ı değil;
//   - wiki isabeti → wiki cevabı (sahte model <wiki_data> görür) + netleştirme
//     çipleri "Telemetri için:" altında korunur;
//   - isabet yok → bugünkü netleştirme bayt bayt aynı;
//   - wiki kapalı / token / bağlamlı sohbet → ctx ve olaylar bayt bayt aynı.
// Adlar sentetik (svc-orders, bsa-*, example.test).

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

var rescueSvcs = []string{"bsaorder-api", "bsaledger-api", "bsagateway", "svc-orders", "svc-payments", "coremetry-api", "coremetry-ui"}

const bsaRunbook = "# BSA cache refresh\n## Adımlar\nBSA yönetim ekranında Cache → Refresh menüsüne girilir; refresh tetiklenir ve bsa cache durumu izlenir."
const coremetryAddrPage = "# Coremetry adresleri\nProd: https://coremetry.example.test — UAT: https://coremetry-uat.example.test adresleri."

// guidedTestServer — guided router'ın ad listeleri önbellekten gelir (depo yok).
func guidedTestServer(t *testing.T) *Server {
	t.Helper()
	mc := newMemCache()
	ctx := context.Background()
	b, _ := json.Marshal(rescueSvcs)
	_ = mc.Set(ctx, "copilot:guided:svcnames", b, time.Hour)
	_ = mc.Set(ctx, "copilot:guided:envnames", []byte("[]"), time.Hour)
	_ = mc.Set(ctx, "copilot:guided:teamcat:v2", []byte("[]"), time.Hour)
	prev := wikiProbeNames
	wikiProbeNames = func(*Server, context.Context, bool) ([]string, []string, []string) { return rescueSvcs, nil, nil }
	t.Cleanup(func() { wikiProbeNames = prev })
	return &Server{cache: mc}
}

// runChatTiers — copilotChat'in wiki → kurtarma → guided sırası (SSE'siz).
func runChatTiers(t *testing.T, s *Server, ctx context.Context, q string, tc wikiTierContext) []emitted {
	t.Helper()
	ev, emit := collectEmit()
	msgs := []copilot.ChatMessage{{Role: "user", Text: q}}
	if h, _ := s.wikiChatAnswer(ctx, emit, msgs, tc); h {
		return *ev
	}
	ctx, emit = s.armDisambigRescue(ctx, emit, msgs, tc)
	s.copilotChatGuided(ctx, emit, msgs, tc.Service, "", tc.Explain, 0, tc.Trace, "", time.Time{}, 0, "")
	return *ev
}

func rescueAnswerText(t *testing.T, ev []emitted) string {
	t.Helper()
	s, _ := answerOf(t, ev)["text"].(string)
	return s
}

func TestWikiHowToCue(t *testing.T) {
	for _, q := range []string{
		"BSA cache refresh nasıl girilir", "Config cache refresh nasıl girilir?", "Coremetry adresleri",
		"vpn nasıl açılır", "yeni kullanıcı nasıl eklenir", "alarm nasıl tanımlanır", "job nasıl çalıştırılır",
		"sertifika nasıl yenilenir", "kuyruk nasıl temizlenir", "loglara nasıl erişebilirim", "bunu nasıl yaparım",
		"deploy için ne yapmalı", "release adımları", "token nasıl alınır", "bu servisten kim sorumlu",
		"svc-orders sahibi kim", "How can I rotate the cert", "steps to restart kafka",
		"who owns svc-payments", "how should we roll back", "rapor nasıl görülür", "loglara nasıl gireriz",
	} {
		if s, w := wikiQuestionCue(q); !s && !w {
			t.Errorf("nasıl-yapılır işareti bekleniyordu: %q", q)
		}
	}
	for _, q := range []string{
		"svc-orders hata oranı nasıl", "latency nasıl", "nasıl gidiyor", "son 1 saatte nasıl",
		"svc-orders nasıl görünüyor", "payments p95 nasıl değişti", "svc-orders hata oranı nedir",
		"how does svc-orders look", "nasıl bir trend var",
		// v0.10.1126 inceleme: telemetri ele geçirilmesin.
		"svc-orders erişim hatası", "hata nerede", "trafik nasıl gelir", "cpu nasıl yükselir", "nasıl düzelir",
		"neden yavaş?", "latency nasıl", "hata oranı nasıl", "grafana nerede", "prod erişimi",
		"where is the latency coming from", "hata nasıl artar", "p99 nasıl azalır", "svc-orders nasıl çalışır",
	} {
		if s, w := wikiQuestionCue(q); s || w {
			t.Errorf("telemetri sorusu işaret taşımamalı: %q (strong=%v weak=%v)", q, s, w)
		}
	}
}

// Yerinde netleştirme: BSA sorusu bugün guided'da ask_service'e düşüyor.
func TestBSAQuestionRoutesToAskService(t *testing.T) {
	r := routeGuidedIntent("BSA cache refresh nasıl girilir", rescueSvcs, nil, nil, "")
	if r.Intent != guidedAskService || len(r.ServiceOptions) < 2 {
		t.Fatalf("ön koşul: guided netleştirme rotası (%s %v)", r.Intent, r.ServiceOptions)
	}
}

func TestBSAHowToWithWikiHitAnswersFromWikiKeepsChips(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/BSA cache refresh": bsaRunbook}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := withFakeNarrator(t, "Cache → Refresh menüsüne girilir.")
	s := guidedTestServer(t)
	ev := runChatTiers(t, s, sessionCtx(), "BSA cache refresh nasıl girilir", wikiTierContext{})
	if len(*seen) != 1 || !strings.Contains((*seen)[0], "<wiki_data>") || !strings.Contains((*seen)[0], "Refresh menüsüne") {
		t.Fatalf("model wiki bağlamını görmeli: %v", *seen)
	}
	a := answerOf(t, ev)
	txt := a["text"].(string)
	if !strings.HasPrefix(txt, "Cache → Refresh menüsüne girilir.") || !strings.Contains(txt, telemetryChipsHeading+"Hangi servisi kastettin?") {
		t.Errorf("wiki cevabı + Telemetri için netleştirmesi: %q", txt)
	}
	chips, _ := a["suggestions"].([]string)
	if len(chips) < 2 || !strings.Contains(strings.Join(chips, "|"), "bsa") {
		t.Errorf("netleştirme çipleri korunmalı: %v", chips)
	}
	if links, _ := a["links"].([]guidedAnswerLink); len(links) == 0 || !strings.Contains(links[0].Href, "devops.example.test") {
		t.Errorf("wiki link çipi: %+v", a["links"])
	}
	if len(api.searches) == 0 || len(api.searches) > 2 { // AND (+ en çok bir OR)
		t.Errorf("tek canlı arama turu: %v", api.searches)
	}
}

func TestBSAHowToWithoutWikiHitKeepsTodaysDisambiguation(t *testing.T) {
	q := "BSA cache refresh nasıl girilir"
	base := runChatTiers(t, guidedTestServer(t), sessionCtx(), q, wikiTierContext{}) // wiki yok (global nil)
	api := &fakeWikiAPI{pages: map[string]string{"/Mimari": "# Mimari\nÖdeme akışı svc-payments ve svc-ledger arasında."}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := withFakeNarrator(t, "x")
	got := runChatTiers(t, guidedTestServer(t), sessionCtx(), q, wikiTierContext{})
	if !reflect.DeepEqual(base, got) {
		t.Fatalf("isabet yokken netleştirme bayt bayt aynı olmalı:\nbase=%+v\ngot =%+v", base, got)
	}
	if !strings.HasPrefix(rescueAnswerText(t, got), "Hangi servisi kastettin?") || len(*seen) != 0 {
		t.Errorf("bugünkü netleştirme: %q", rescueAnswerText(t, got))
	}
	if n := len(api.searches); n == 0 || n > 2 {
		t.Errorf("kademe bir kez arar, kurtarma tekrar aramaz: %v", api.searches)
	}
}

// İşaretsiz soru guided'da netleştirmeye düşerse kurtarma devreye girer.
func TestDisambiguationRescueWithWikiHit(t *testing.T) {
	q := "bsa servisini bul"
	if s, w := wikiQuestionCue(q); s || w {
		t.Fatal("ön koşul: işaretsiz soru")
	}
	base := runChatTiers(t, guidedTestServer(t), sessionCtx(), q, wikiTierContext{})
	baseAns := answerOf(t, base)
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/BSA cache refresh": bsaRunbook}}
	withWikiService(t, wiki.Config{Enabled: true, Mode: wiki.ModeLive}, api)
	seen := withFakeNarrator(t, "Refresh menüsünden tetiklenir.")
	ev := runChatTiers(t, guidedTestServer(t), sessionCtx(), q, wikiTierContext{})
	if len(*seen) != 1 || !strings.Contains((*seen)[0], "<wiki_data>") {
		t.Fatalf("kurtarma wiki bağlamıyla anlatmalı: %v", *seen)
	}
	answers := 0
	for _, e := range ev {
		if e.kind == "answer" {
			answers++
		}
	}
	a := answerOf(t, ev)
	if answers != 1 || !strings.HasPrefix(a["text"].(string), "Refresh menüsünden") ||
		!strings.Contains(a["text"].(string), telemetryChipsHeading+baseAns["text"].(string)) {
		t.Fatalf("tek cevap: wiki + özgün netleştirme metni: %d %q", answers, a["text"])
	}
	if !reflect.DeepEqual(a["suggestions"], baseAns["suggestions"]) {
		t.Errorf("özgün çipler: %v vs %v", a["suggestions"], baseAns["suggestions"])
	}
	if len(api.searches) == 0 || len(api.searches) > 2 {
		t.Errorf("tek canlı arama turu: %v", api.searches)
	}
}

func TestDisambiguationRescueWithoutHitIsUnchanged(t *testing.T) {
	q := "bsa servisini bul"
	base := runChatTiers(t, guidedTestServer(t), sessionCtx(), q, wikiTierContext{})
	api := &fakeWikiAPI{pages: map[string]string{"/Mimari": "# Mimari\nÖdeme akışı svc-payments."}}
	withWikiService(t, wiki.Config{Enabled: true, Mode: wiki.ModeLive}, api)
	seen := withFakeNarrator(t, "x")
	got := runChatTiers(t, guidedTestServer(t), sessionCtx(), q, wikiTierContext{})
	if !reflect.DeepEqual(base, got) || len(*seen) != 0 {
		t.Fatalf("isabet yokken olaylar aynı olmalı:\nbase=%+v\ngot =%+v", base, got)
	}
}

func TestCoremetryAddressesWithHitAnswersFromWiki(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Platform/Coremetry adresleri": coremetryAddrPage}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := withFakeNarrator(t, "Prod: https://coremetry.example.test")
	ev := runChatTiers(t, guidedTestServer(t), sessionCtx(), "Coremetry adresleri", wikiTierContext{})
	if len(*seen) != 1 || !strings.Contains((*seen)[0], "coremetry-uat.example.test") {
		t.Fatalf("wiki bağlamı: %v", *seen)
	}
	if !strings.HasPrefix(rescueAnswerText(t, ev), "Prod: https://coremetry.example.test") {
		t.Errorf("wiki cevabı: %q", rescueAnswerText(t, ev))
	}
}

// Telemetri soruları: wiki açıkken de kademe girmez, arama yok; sarılı emit
// işaretsiz (netleştirme olmayan) telemetri cevabını AYNEN geçirir. (Guided
// telemetri yolunun kendisi depo ister; burada kademe + sarmal sınanır.)
func TestTelemetryQuestionsUnchangedWithWiki(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := withFakeNarrator(t, "x")
	for _, q := range []string{"svc-orders hata oranı nasıl", "svc-orders latency nasıl", "svc-orders nasıl gidiyor", "svc-orders son 1 saatte nasıl"} {
		ev, emit := collectEmit()
		msgs := []copilot.ChatMessage{{Role: "user", Text: q}}
		if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, msgs, wikiTierContext{}); h || len(*ev) != 0 {
			t.Errorf("%q: wiki kademesi girmemeli (%+v)", q, *ev)
		}
		_, wrapped := (&Server{}).armDisambigRescue(sessionCtx(), emit, msgs, wikiTierContext{})
		ans := map[string]any{"text": "svc-orders hata oranı %0.4", "suggestions": []string{"svc-orders hata logları?"}}
		wrapped("step", map[string]string{"label": "get_service_health"})
		wrapped("answer", ans)
		if len(*ev) != 2 || !reflect.DeepEqual((*ev)[1].v, ans) {
			t.Errorf("%q: telemetri cevabı aynen geçmeli: %+v", q, *ev)
		}
	}
	if len(api.searches) != 0 || len(*seen) != 0 {
		t.Errorf("telemetri sorusunda wiki araması/anlatımı olmamalı: %v", api.searches)
	}
}

// Kapı: wiki kapalı / token / bağlamlı sohbet → ctx ve emit DOKUNULMAZ.
func TestDisambigRescueGateUnchanged(t *testing.T) {
	msgs := []copilot.ChatMessage{{Role: "user", Text: "bsa servisini bul"}}
	s := &Server{}
	check := func(name string, ctx context.Context, tc wikiTierContext) {
		t.Helper()
		got, _ := s.armDisambigRescue(ctx, func(string, any) {}, msgs, tc)
		if got != ctx {
			t.Errorf("%s: kurtarma kurulmamalı (ctx değişti)", name)
		}
	}
	check("wiki yok", sessionCtx(), wikiTierContext{})
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/BSA cache refresh": bsaRunbook}}
	withWikiService(t, wiki.Config{Enabled: false}, api)
	check("wiki kapalı", sessionCtx(), wikiTierContext{})
	withWikiService(t, wiki.Config{Enabled: true}, api)
	check("API token'ı", auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "token:t1", Role: auth.RoleAdmin}), wikiTierContext{})
	check("servis paneli", sessionCtx(), wikiTierContext{Service: "svc-orders"})
	check("çekmece", sessionCtx(), wikiTierContext{Explain: "x"})
	if got, _ := s.armDisambigRescue(sessionCtx(), func(string, any) {}, msgs, wikiTierContext{}); got == sessionCtx() {
		t.Error("açık kapıda kurtarma kurulmalı")
	}
	// Wiki kapalıyken (Enabled:false) guided olayları wiki'siz kurulumla aynı.
	withWikiService(t, wiki.Config{Enabled: false}, api)
	off := runChatTiers(t, guidedTestServer(t), sessionCtx(), "BSA cache refresh nasıl girilir", wikiTierContext{})
	SetWiki(nil)
	none := runChatTiers(t, guidedTestServer(t), sessionCtx(), "BSA cache refresh nasıl girilir", wikiTierContext{})
	if !reflect.DeepEqual(off, none) || len(api.searches) != 0 {
		t.Errorf("wiki kapalı → bayt bayt aynı: %+v vs %+v (%v)", off, none, api.searches)
	}
}

// Netleştirme işareti yalnız kurulu kurtarmada etkili; işaretsiz cevap geçer.
func TestDisambigMarkOnlyRescuesMarkedAnswer(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/BSA cache refresh": bsaRunbook}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	withFakeNarrator(t, "x")
	markDisambiguation(context.Background()) // kurulu değil → etkisiz, panik yok
	ev, emit := collectEmit()
	_, wrapped := (&Server{}).armDisambigRescue(sessionCtx(), emit, []copilot.ChatMessage{{Role: "user", Text: "bsa servisini bul"}}, wikiTierContext{})
	orig := map[string]any{"text": "svc-orders sağlıklı"}
	wrapped("answer", orig)
	if len(*ev) != 1 || !reflect.DeepEqual((*ev)[0].v, orig) || len(api.searches) != 0 {
		t.Errorf("işaretsiz cevap aynen geçmeli, arama yok: %+v %v", *ev, api.searches)
	}
}

// v0.10.1126 — "Aramayı test et" yanıtı tazelenmiş paylaşılan durumu taşır
// (kart "henüz denenmedi"de kalmasın).
func TestWikiTestSearchReturnsFreshSearchStatus(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	srv := &Server{auditQ: make(chan chstore.AuditEntry, 16)}
	rec := doWikiReq(srv.buildMux(), "POST", "/api/wiki/test-search", `{"query":"svc-orders restart"}`,
		&auth.Claims{UserID: "u-9", Role: auth.RoleAdmin, Email: "admin@example.test"})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Status struct {
			Search     string             `json:"search"`
			SearchLast *wiki.SearchStatus `json:"searchLast"`
		} `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status.Search != wiki.SearchAvailable || out.Status.SearchLast == nil || out.Status.SearchLast.APIVersion != "7.0" || out.Status.SearchLast.Hits == 0 {
		t.Fatalf("test yanıtı paylaşılan durumu taşımalı: %s", rec.Body.String())
	}
}

// v0.10.1126 inceleme — canlı ADO isabeti varken telemetri soruları guided'da
// kalır: kademe olay basmaz/aramaz, kurtarma kurulmaz (ctx aynı → guided aynı
// emit ve ctx'i görür, olay akışı yapı gereği özdeş).
func TestTelemetryQuestionsStayOnGuidedWithLiveHit(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{
		"/Runbooks/Yavaşlık": "# Neden yavaş\nsvc-orders yavaş olduğunda neden yavaş sorusu için erişim hatası ve latency adımları.",
		"/Runbooks/Erişim":   "# svc-orders erişim hatası\nsvc-orders erişim hatası alınırsa token yenilenir.",
	}}
	withWikiService(t, wiki.Config{Enabled: true, Mode: wiki.ModeLive}, api)
	seen := withFakeNarrator(t, "x")
	s := guidedTestServer(t)
	for _, q := range []string{"neden yavaş?", "svc-orders erişim hatası", "svc-orders sahibi kim", "svc-orders hatası nasıl düzeltilir"} {
		ev, emit := collectEmit()
		msgs := []copilot.ChatMessage{{Role: "user", Text: q}}
		if h, _ := s.wikiChatAnswer(sessionCtx(), emit, msgs, wikiTierContext{}); h || len(*ev) != 0 {
			t.Errorf("%q: wiki kademesi girmemeli (%+v)", q, *ev)
		}
		ctx := sessionCtx()
		if got, _ := s.armDisambigRescue(ctx, emit, msgs, wikiTierContext{}); got != ctx {
			t.Errorf("%q: kurtarma kurulmamalı", q)
		}
	}
	if len(api.searches) != 0 || len(*seen) != 0 {
		t.Errorf("telemetri sorusunda wiki araması olmamalı: %v", api.searches)
	}
}

// Sağlık/kök-neden/pencere-kıyası ask_service'i işaretlenmez.
func TestDisambigAskRescuable(t *testing.T) {
	for _, a := range []guidedIntent{guidedRootCause, guidedServiceHealth, guidedWindowCompare} {
		if disambigAskRescuable(a) {
			t.Errorf("%s kurtarılmamalı", a)
		}
	}
	if !disambigAskRescuable(guidedDeployImpact) || !disambigAskRescuable(guidedOpenPage) {
		t.Error("diğer ask_service niyetleri kurtarılabilir")
	}
}

// Canlı isabet yalnız SIRA skoruyla zayıf kademeyi geçmez (kapsam ≥ 0.5 şart).
func TestWeakCueLiveHitNeedsCoverage(t *testing.T) {
	h := wiki.Hit{Score: 0.9, Live: true, Coverage: 0.25}
	if h.EvidencedAt(ragWikiFloor) {
		t.Error("düşük kapsamlı canlı isabet kanıt değil")
	}
	h.Coverage = 0.75
	if !h.EvidencedAt(ragWikiFloor) {
		t.Error("kapsamlı canlı isabet geçmeli")
	}
	if !(wiki.Hit{Score: 0.6}).EvidencedAt(ragWikiFloor) || (wiki.Hit{Score: 0.4}).EvidencedAt(ragWikiFloor) {
		t.Error("yerel isabet skorla")
	}
	if !(wiki.Hit{Score: 0.9, Live: true, LocalScore: 0.55}).EvidencedAt(ragWikiFloor) {
		t.Error("canlıyla birleşen yerel ≥ taban geçmeli")
	}
	// Uçtan uca: ADO araması sayfayı alt-dize eşleşmesiyle döndürüyor (terimler başka sözcüklerin İÇİNDE) →
	// AND sorgusunun ilk sonucu, ama kapsam düşük → zayıf işaret düşer.
	api := &fakeWikiAPI{pages: map[string]string{"/Notlar": "# Notlar\nabsalom cachexia xrefreshx agirilirx"}}
	withWikiService(t, wiki.Config{Enabled: true, Mode: wiki.ModeLive}, api)
	seen := withFakeNarrator(t, "x")
	ev, emit := collectEmit()
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, []copilot.ChatMessage{{Role: "user", Text: "BSA cache refresh nasıl girilir"}}, wikiTierContext{}); h || len(*ev) != 0 || len(*seen) != 0 {
		t.Errorf("kapsamsız sıra-0 canlı isabet zayıf kademeyi geçmemeli: h=%v %+v", h, *ev)
	}
}
