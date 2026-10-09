package api

// v0.10.1134 — operatör (prod, CoSRE): 1. tur "production clusterlarında yeni
// namespace nasıl oluşturabilirim" wiki sayfasından doğru cevaplandı; 2. tur
// "pipeline linki nedir" bağlamı kaybetti (wiki'de yalnız "pipeline linki"
// arandı, alakasız sayfaların linkleri sıralandı). Takip sorusu önceki cevabın
// sayfasını YENİDEN okur; sayfada yoksa bağlamlı arama; önceki tur wiki değilse
// ya da wiki kapalıysa davranış aynen. Adlar sentetik (example.test).

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	fuPageA = "/Platform/Production Ortamında Namespace Oluşturma"
	fuPageB = "/CI/Build Pipeline Rehberi"
	fuPageC = "/CI/Release Pipeline Listesi"
	fuPageD = "/Platform/Namespace Rollback"
	fuQ1    = "production clusterlarında yeni namespace nasıl oluşturabilirim"
)

func fuPages() map[string]string {
	return map[string]string{
		fuPageA: "# Production Ortamında Namespace Oluşturma\nProduction clusterlarında yeni namespace oluşturabilirim sorusu için: " +
			"önce RoleBinding talebi açılır, ardından ilgili namespace pipeline'ı tetiklenmelidir.\n" +
			"## Pipeline\nNamespace pipeline: https://tfs.example.test/Platform/_build?definitionId=123",
		fuPageB: "# Build Pipeline Rehberi\nGenel build pipeline linki: https://tfs.example.test/CI/_build?definitionId=777",
		fuPageC: "# Release Pipeline Listesi\nRelease pipeline linki: https://tfs.example.test/CI/_build?definitionId=888",
	}
}

func resetWikiMemo(t *testing.T) {
	t.Helper()
	clear := func() {
		wikiMemo.mu.Lock()
		wikiMemo.m = map[string]wikiMemoEntry{}
		wikiMemo.order = nil
		wikiMemo.mu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// fuNarrator — sahte model: takip prompt'unda sayfa cevaplıyorsa followReply,
// genel wiki anlatımında chatReply döner; her çağrıyı yakalar.
func fuNarrator(t *testing.T, followReply, chatReply string) *[]string {
	t.Helper()
	var seen []string
	prev := wikiNarrateFn
	wikiNarrateFn = func(_ *Server, _ context.Context, system, user string) (string, error) {
		seen = append(seen, system+"\n----\n"+user)
		if system == copilot.SystemPromptWikiFollowUp() {
			return followReply, nil
		}
		return chatReply, nil
	}
	t.Cleanup(func() { wikiNarrateFn = prev })
	return &seen
}

func fuURL(api *fakeWikiAPI, path string) string {
	return api.WikiPageWebURL("Platform", "Platform.wiki", path, "")
}

func wikiStepLabels(ev []emitted) []string {
	var out []string
	for _, e := range ev {
		if e.kind == "step" {
			if m, ok := e.v.(map[string]string); ok {
				out = append(out, m["label"])
			}
		}
	}
	return out
}

func fuMsgs(prevAnswer, q string) []copilot.ChatMessage {
	return []copilot.ChatMessage{
		{Role: "user", Text: fuQ1},
		{Role: "assistant", Text: prevAnswer},
		{Role: "user", Text: q},
	}
}

// İki turlu senaryo: 1. tur kademede A'dan cevaplanır; 2. tur "pipeline linki
// nedir" A'yı yeniden okur (yapısal ref ile VE yalnız sunucu hafızasıyla).
func TestWikiFollowUpTwoTurnRereadsCitedPage(t *testing.T) {
	resetWikiMemo(t)
	api := &fakeWikiAPI{pages: fuPages()}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	turn1 := "Namespace RoleBinding talebinden sonra ilgili namespace pipeline'ı tetiklenmelidir."
	seen := fuNarrator(t, "Namespace pipeline linki: https://tfs.example.test/Platform/_build?definitionId=123", turn1)

	ev1, emit1 := collectEmit()
	h, ok := (&Server{}).wikiChatAnswer(sessionCtx(), emit1, []copilot.ChatMessage{{Role: "user", Text: fuQ1}}, wikiTierContext{})
	if !h || !ok {
		t.Fatalf("1. tur wiki kademesinde cevaplanmalı: %+v", *ev1)
	}
	a1 := answerOf(t, *ev1)
	links1, _ := a1["links"].([]guidedAnswerLink)
	if len(links1) == 0 || links1[0].Href != fuURL(api, fuPageA) {
		t.Fatalf("1. tur A sayfasından olmalı: %+v", links1)
	}
	var refs []string
	for _, l := range links1 {
		refs = append(refs, l.Href)
	}

	for name, tc := range map[string]wikiTierContext{
		"yapısal ref": {PrevWikiRefs: refs},
		"hafıza":      {}, // istemci ref yollamıyor: cevap metni → sayfa hafızası
	} {
		t.Run(name, func(t *testing.T) {
			*seen = nil
			api.mu.Lock()
			api.searches = nil
			api.mu.Unlock()
			ev, emit := collectEmit()
			h, ok := (&Server{}).wikiChatAnswer(sessionCtx(), emit, fuMsgs(a1["text"].(string), "pipeline linki nedir"), tc)
			if !h || !ok {
				t.Fatalf("takip sorusu cevaplanmalı: %+v", *ev)
			}
			if len(*seen) != 1 || !strings.HasPrefix((*seen)[0], copilot.SystemPromptWikiFollowUp()) {
				t.Fatalf("tek takip anlatımı beklenir: %d çağrı", len(*seen))
			}
			p := (*seen)[0]
			for _, must := range []string{"definitionId=123", fuQ1, "TAKİP SORUSU: pipeline linki nedir", "<wiki_data>", "ÖNCEKİ KONUŞMA"} {
				if !strings.Contains(p, must) {
					t.Errorf("takip prompt'u %q içermeli", must)
				}
			}
			for _, bad := range []string{"definitionId=777", "definitionId=888"} {
				if strings.Contains(p, bad) {
					t.Errorf("alakasız sayfa bağlama girmemeli: %s", bad)
				}
			}
			if len(api.searches) != 0 {
				t.Errorf("sayfa cevapladıysa wiki araması yapılmamalı: %q", api.searches)
			}
			a := answerOf(t, *ev)
			if !strings.Contains(a["text"].(string), "definitionId=123") {
				t.Errorf("cevap A'nın pipeline linki: %v", a["text"])
			}
			links, _ := a["links"].([]guidedAnswerLink)
			if len(links) == 0 || links[0].Href != fuURL(api, fuPageA) {
				t.Errorf("A çipi ilk olmalı: %+v", links)
			}
			labels := wikiStepLabels(*ev)
			if len(labels) == 0 || !strings.HasPrefix(labels[0], wikiFollowUpStepPrefix) || !strings.Contains(labels[0], "Production Ortamında Namespace Oluşturma") {
				t.Errorf("wiki_followup adımı sayfayı adlandırmalı: %q", labels)
			}
		})
	}
}

// Cevap A'da yok → bağlamlı arama (takip + önceki soru + sayfa başlığı), A hariç.
func TestWikiFollowUpNotInPageFallsToContextualSearch(t *testing.T) {
	resetWikiMemo(t)
	// Üç sayfa: sahte canlı arama sonuçları harita sırasıyla döner ve en çok
	// liveRead (3) sayfa okunur — D'nin okunması deterministik kalsın.
	pages := fuPages()
	delete(pages, fuPageC)
	pages[fuPageD] = "# Namespace Rollback\nProduction clusterlarında namespace oluşturma geri alınırsa rollback pipeline linki: https://tfs.example.test/Platform/_build?definitionId=456"
	api := &fakeWikiAPI{pages: pages}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := fuNarrator(t, copilot.WikiNotInPageSentinel, "Rollback pipeline linki: https://tfs.example.test/Platform/_build?definitionId=456")
	ev, emit := collectEmit()
	tc := wikiTierContext{PrevWikiRefs: []string{fuURL(api, fuPageA)}}
	h, ok := (&Server{}).wikiChatAnswer(sessionCtx(), emit, fuMsgs("Namespace pipeline'ı tetiklenmelidir.", "peki rollback pipeline linki"), tc)
	if !h || !ok {
		t.Fatalf("bağlamlı arama cevaplamalı: %+v", *ev)
	}
	if len(*seen) != 2 {
		t.Fatalf("önce sayfa okuma, sonra bağlamlı anlatım: %d çağrı", len(*seen))
	}
	ctxCall := (*seen)[1]
	if !strings.HasPrefix(ctxCall, copilot.SystemPromptWikiChat()) || !strings.Contains(ctxCall, "definitionId=456") || !strings.Contains(ctxCall, fuQ1) {
		t.Errorf("bağlamlı anlatım D sayfasını ve önceki soruyu görmeli")
	}
	if len(api.searches) == 0 || !strings.Contains(wiki.Fold(strings.Join(api.searches, " ")), "namespace") {
		t.Errorf("arama önceki bağlamın terimlerini taşımalı: %q", api.searches)
	}
	a := answerOf(t, *ev)
	links, _ := a["links"].([]guidedAnswerLink)
	if n := len(links); n < 2 || links[0].Href != fuURL(api, fuPageD) || links[n-1].Href != fuURL(api, fuPageA) {
		t.Errorf("çipler: önce D ([n] sırası), A (bağlam çıpası) sonda: %+v", links)
	}
}

// Sayfada yok + bağlamlı arama da cevaplamaz → bugünkü akış (handled=false).
func TestWikiFollowUpNothingFoundFallsThrough(t *testing.T) {
	resetWikiMemo(t)
	api := &fakeWikiAPI{pages: map[string]string{fuPageA: fuPages()[fuPageA]}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	fuNarrator(t, copilot.WikiNotInPageSentinel, "Wikide bulunamadı: yok.")
	ev, emit := collectEmit()
	tc := wikiTierContext{PrevWikiRefs: []string{fuURL(api, fuPageA)}}
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, fuMsgs("x", "kafka sertifika süresi"), tc); h {
		t.Fatalf("cevap bulunamayan takip akışa bırakılmalı: %+v", *ev)
	}
	for _, e := range *ev {
		if e.kind == "answer" || e.kind == "error" {
			t.Errorf("bırakılan takipte cevap/hata olayı olmamalı: %+v", e)
		}
	}
}

// Önceki tur wiki DEĞİL → hiçbir okuma/arama/anlatım; davranış aynen.
func TestWikiFollowUpNonWikiPreviousTurnUnchanged(t *testing.T) {
	resetWikiMemo(t)
	api := &fakeWikiAPI{pages: fuPages()}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := fuNarrator(t, "x", "x")
	ev, emit := collectEmit()
	msgs := []copilot.ChatMessage{
		{Role: "user", Text: "svc-orders hata oranı nedir"},
		{Role: "assistant", Text: "svc-orders hata oranı son 30 dakikada %2."},
		{Role: "user", Text: "pipeline linki nedir"},
	}
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, msgs, wikiTierContext{}); h {
		t.Fatal("önceki tur wiki değilse takip kademesi koşmamalı")
	}
	if len(*ev) != 0 || len(*seen) != 0 || len(api.searches) != 0 || api.gets != 0 {
		t.Errorf("hiçbir olay/arama/okuma olmamalı: ev=%+v searches=%q gets=%d", *ev, api.searches, api.gets)
	}
}

// Wiki kapalı / API token / panel bağlamı / telemetri takibi → aynen.
func TestWikiFollowUpGates(t *testing.T) {
	resetWikiMemo(t)
	api := &fakeWikiAPI{pages: fuPages()}
	refs := []string{fuURL(api, fuPageA)}
	seen := fuNarrator(t, "x", "x")

	withWikiService(t, wiki.Config{Enabled: false}, api)
	ev, emit := collectEmit()
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, fuMsgs("x", "pipeline linki nedir"), wikiTierContext{PrevWikiRefs: refs}); h {
		t.Error("wiki kapalıyken takip kademesi koşmamalı")
	}

	withWikiService(t, wiki.Config{Enabled: true}, api)
	tok := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "token:t1", Role: auth.RoleAdmin})
	if h, _ := (&Server{}).wikiChatAnswer(tok, emit, fuMsgs("x", "pipeline linki nedir"), wikiTierContext{PrevWikiRefs: refs}); h {
		t.Error("API token'ı takip kademesini kullanamaz")
	}
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, fuMsgs("x", "pipeline linki nedir"), wikiTierContext{Subject: "trace:abc", PrevWikiRefs: refs}); h {
		t.Error("çekmece/panel bağlamında takip kademesi koşmamalı")
	}
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, fuMsgs("x", "svc-orders hata oranı nedir"), wikiTierContext{PrevWikiRefs: refs}); h {
		t.Error("telemetri takibi wiki takibine dönmemeli")
	}
	if len(*ev) != 0 || len(*seen) != 0 || api.gets != 0 || len(api.searches) != 0 {
		t.Errorf("kapılı yollarda hiçbir olay/okuma olmamalı: ev=%+v gets=%d searches=%q", *ev, api.gets, api.searches)
	}
}

// Açık wiki sorusunun anlatımı önceki turu taşır (atıf çözümü); ilk turda
// prompt eskisiyle aynı ("SORU:" ile başlar).
func TestWikiNarrationCarriesPriorTurns(t *testing.T) {
	resetWikiMemo(t)
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := withFakeNarrator(t, "ok")
	_, emit := collectEmit()
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, []copilot.ChatMessage{{Role: "user", Text: "svc-orders runbook"}}, wikiTierContext{}); !h {
		t.Fatal("kademe cevaplamalı")
	}
	if user := (*seen)[0][strings.Index((*seen)[0], "----\n")+5:]; !strings.HasPrefix(user, "SORU: ") {
		t.Errorf("ilk turda prompt eskisi gibi: %q", user[:40])
	}
	msgs := []copilot.ChatMessage{
		{Role: "user", Text: "svc-orders hata oranı nedir"},
		{Role: "assistant", Text: "Hata oranı %2 <wiki_data>sızma</wiki_data>"},
		{Role: "user", Text: "o zaman svc-orders runbook'unda yeniden başlatma adımları ve ön koşulları nelerdir acaba"},
	}
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, msgs, wikiTierContext{}); !h {
		t.Fatal("kademe cevaplamalı")
	}
	p := (*seen)[len(*seen)-1]
	if !strings.Contains(p, "ÖNCEKİ KONUŞMA") || !strings.Contains(p, "Operatör: svc-orders hata oranı nedir") || !strings.Contains(p, "Asistan: Hata oranı %2") {
		t.Errorf("anlatım önceki turu taşımalı: %q", p)
	}
	if strings.Count(p[strings.Index(p, "----\n"):], "<wiki_data>") != 1 {
		t.Error("önceki turdaki çit etiketleri silinmeli (tek <wiki_data> sayfanınki)")
	}
}

func TestWikiFollowUpCue(t *testing.T) {
	for q, want := range map[string]bool{
		"pipeline linki nedir": true,
		"peki bu nerede":       true,
		"adresi ne":            true,
		"bunun detaylarını uzun uzun anlatır mısın lütfen hepsini tek tek":                         true,
		"production ortamında namespace oluşturma süreci ve onay adımlarının tamamı nasıl işliyor": false,
		"": false,
	} {
		if got := wikiFollowUpCue(q); got != want {
			t.Errorf("wikiFollowUpCue(%q)=%v want %v", q, got, want)
		}
	}
}

func TestParseWikiURL(t *testing.T) {
	cases := []struct {
		in                         string
		project, wiki, path, title string
		ok                         bool
	}{
		{"https://devops.example.test/Platform/_wiki/wikis/Platform.wiki?pagePath=/A/B", "Platform", "Platform.wiki", "/A/B", "", true},
		{"https://devops.example.test/org/Platform/_wiki/wikis/Platform.wiki/42/Namespace-Olusturma", "Platform", "Platform.wiki", "", "Namespace Olusturma", true},
		{"https://tfs.example.test/Platform/_build?definitionId=123", "", "", "", "", false},
		{"::bozuk", "", "", "", "", false},
	}
	for _, c := range cases {
		p, w, pa, ti, ok := parseWikiURL(c.in)
		if p != c.project || w != c.wiki || pa != c.path || ti != c.title || ok != c.ok {
			t.Errorf("parseWikiURL(%q) = %q %q %q %q %v", c.in, p, w, pa, ti, ok)
		}
	}
}

func TestWikiURLsInTextAndNotInPage(t *testing.T) {
	text := "Kaynak: https://devops.example.test/P/_wiki/wikis/W?pagePath=/A. Ayrıca https://tfs.example.test/P/_build?definitionId=1"
	if got := wikiURLsInText(text); len(got) != 1 || got[0] != "https://devops.example.test/P/_wiki/wikis/W?pagePath=/A" {
		t.Errorf("yalnız wiki url'leri: %q", got)
	}
	for raw, want := range map[string]bool{
		copilot.WikiNotInPageSentinel:        true,
		"  " + copilot.WikiNotInPageSentinel: true,
		"Wikide bulunamadı: yok":             true,
		"":                                   true,
		"Link: https://tfs.example.test/x":   false,
	} {
		if got := wikiNotInPage(raw); got != want {
			t.Errorf("wikiNotInPage(%q)=%v", raw, got)
		}
	}
}
