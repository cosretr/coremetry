package api

// v0.10.1124 — operatör: "CoSRE wiki içeriğini LLM ile yorumlayamıyor" ve
// "senkron değilse wiki'yi aramıyor". Açık wiki sorusu kademesi: boş yerel
// indeks + canlı isabet → sahte model sayfa METNİNİ bağlamda görür; işaretsiz
// telemetri sorusu kademeye girmez; bulunamazsa açık "Wikide bulunamadı";
// canlı modda Search yoksa açık mesaj. Adlar sentetik (example.test).

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// ── sahte wiki.API (on-prem şekli yeterli; HTTP katmanı devops testlerinde) ──

type fakeWikiAPI struct {
	mu          sync.Mutex
	pages       map[string]string // path → content (proje Platform, wiki w1)
	unavailable bool
	searches    []string
	gets        int
}

func (f *fakeWikiAPI) Configured() bool { return true }
func (f *fakeWikiAPI) ListWikiProjects(context.Context) ([]string, error) {
	return []string{"Platform"}, nil
}
func (f *fakeWikiAPI) ListWikis(context.Context, string) ([]devops.WikiInfo, error) {
	return []devops.WikiInfo{{ID: "w1", Name: "Platform.wiki", Project: "Platform"}}, nil
}
func (f *fakeWikiAPI) WikiPageTree(context.Context, string, string, int) ([]devops.WikiPageRef, bool, error) {
	return nil, false, nil
}
func (f *fakeWikiAPI) WikiItemVersions(context.Context, devops.WikiInfo) (map[string]string, error) {
	return nil, errors.New("yok")
}
func (f *fakeWikiAPI) GetWikiPage(_ context.Context, _, _, path, _ string) (devops.WikiPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	c, ok := f.pages[path]
	if !ok {
		return devops.WikiPage{}, errors.New("http 404")
	}
	return devops.WikiPage{Path: path, Content: c}, nil
}

// SearchWiki — ADO anlamı: boşluk AND, " OR " OR; alt dize eşleşmesi (katlanmış).
func (f *fakeWikiAPI) SearchWiki(_ context.Context, text, _ string, _ int) ([]devops.WikiSearchHit, devops.WikiSearchInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, text)
	if f.unavailable {
		return nil, devops.WikiSearchInfo{Class: devops.WikiSearchUnavailable, HTTPStatus: 404}, devops.ErrWikiSearchUnavailable
	}
	isOr := strings.Contains(text, " OR ")
	needles := strings.Fields(text)
	if isOr {
		needles = strings.Split(text, " OR ")
	}
	var out []devops.WikiSearchHit
	for path, c := range f.pages {
		n := 0
		for _, nd := range needles {
			if strings.Contains(wiki.Fold(c+" "+path), wiki.Fold(nd)) {
				n++
			}
		}
		if (isOr && n > 0) || (!isOr && n == len(needles)) {
			out = append(out, devops.WikiSearchHit{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: path})
		}
	}
	return out, devops.WikiSearchInfo{Class: devops.WikiSearchOK, HTTPStatus: 200, APIVersion: "7.0", Hits: len(out)}, nil
}
func (f *fakeWikiAPI) WikiPageWebURL(project, wikiName, path, _ string) string {
	return "https://devops.example.test/" + project + "/_wiki/wikis/" + wikiName + "?pagePath=" + path
}

// ── bellek-içi wiki.Store (boş indeks; yazılanı tutar) ──

type fakeWikiStore struct {
	mu       sync.Mutex
	settings map[string][]byte
	pages    map[string]wiki.PageRecord
	upserts  int
}

func newFakeWikiStore() *fakeWikiStore {
	return &fakeWikiStore{settings: map[string][]byte{}, pages: map[string]wiki.PageRecord{}}
}
func (m *fakeWikiStore) GetSetting(_ context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings[k], nil
}
func (m *fakeWikiStore) PutSetting(_ context.Context, k string, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[k] = v
	return nil
}
func (m *fakeWikiStore) WikiPageIndex(context.Context) ([]wiki.PageMeta, error) { return nil, nil }
func (m *fakeWikiStore) UpsertWikiPage(_ context.Context, p wiki.PageRecord, _ []wiki.ChunkRecord, _ uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pages[p.WikiID+p.Path] = p
	m.upserts++
	return nil
}
func (m *fakeWikiStore) DeleteWikiPage(context.Context, wiki.PageMeta) error { return nil }
func (m *fakeWikiStore) WikiTermStats(_ context.Context, terms []string, _ string) (wiki.Stats, error) {
	return wiki.Stats{DF: make([]uint64, len(terms))}, nil // boş indeks
}
func (m *fakeWikiStore) WikiCandidates(context.Context, wiki.CandidateQuery, string, int) ([]wiki.Candidate, error) {
	return nil, nil
}
func (m *fakeWikiStore) WikiSemantic(context.Context, []float32, string, int) ([]wiki.SemHit, error) {
	return nil, nil
}
func (m *fakeWikiStore) GetWikiPage(_ context.Context, project, ref, path string) (*wiki.PageRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pages {
		if p.Project == project && (p.WikiID == ref || p.WikiName == ref) && p.Path == path {
			cp := p
			return &cp, nil
		}
	}
	return nil, nil
}
func (m *fakeWikiStore) WikiCounts(context.Context) (uint64, uint64, error) { return 0, 0, nil }
func (m *fakeWikiStore) UpsertWikiChunks(context.Context, wiki.PageRecord, []wiki.ChunkRecord, uint32) error {
	return nil
}
func (m *fakeWikiStore) WikiPageChunks(context.Context, string, string) ([]wiki.ChunkRecord, error) {
	return nil, nil
}

const runbookText = "# Yeniden başlatma\n## Adımlar\nkubectl rollout restart deploy/svc-orders -n orders komutunu çalıştırın; ardından ERR-1042 oranını izleyin."

// withWikiService — test süresince global wiki servisini kurar.
func withWikiService(t *testing.T, cfg wiki.Config, api *fakeWikiAPI) (*wiki.Service, *fakeWikiStore) {
	t.Helper()
	st := newFakeWikiStore()
	svc := wiki.New(st, func() wiki.API { return api }, nil)
	svc.Configure(cfg)
	prev := wikiKB()
	SetWiki(svc)
	t.Cleanup(func() { SetWiki(prev) })
	return svc, st
}

// withFakeNarrator — sahte model: bağlamı yakalar, sabit anlatım döner.
func withFakeNarrator(t *testing.T, reply string) *[]string {
	t.Helper()
	var seen []string
	prev := wikiNarrateFn
	wikiNarrateFn = func(_ *Server, _ context.Context, system, user string) (string, error) {
		seen = append(seen, system+"\n----\n"+user)
		return reply, nil
	}
	t.Cleanup(func() { wikiNarrateFn = prev })
	return &seen
}

func sessionCtx() context.Context {
	return auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-1", Role: auth.RoleViewer})
}

type emitted struct {
	kind string
	v    any
}

func collectEmit() (*[]emitted, func(string, any)) {
	var out []emitted
	return &out, func(k string, v any) { out = append(out, emitted{k, v}) }
}

func answerOf(t *testing.T, ev []emitted) map[string]any {
	t.Helper()
	for _, e := range ev {
		if e.kind == "answer" {
			m, _ := e.v.(map[string]any)
			return m
		}
	}
	t.Fatalf("answer olayı yok: %+v", ev)
	return nil
}

func TestWikiTierEmptyIndexLiveHitsNarratesContent(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}}
	_, st := withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := withFakeNarrator(t, "svc-orders'ı yeniden başlatmak için rollout restart çalıştırılır.")
	ev, emit := collectEmit()
	msgs := []copilot.ChatMessage{{Role: "user", Text: "svc-orders runbook'unda yeniden başlatma nasıl anlatılıyor?"}}
	handled, ok := (&Server{}).wikiChatAnswer(sessionCtx(), emit, msgs, wikiTierContext{})
	if !handled || !ok {
		t.Fatalf("açık wiki sorusu kademede cevaplanmalı: handled=%v ok=%v %+v", handled, ok, *ev)
	}
	if len(*seen) != 1 || !strings.Contains((*seen)[0], "kubectl rollout restart deploy/svc-orders") {
		t.Fatalf("model sayfa METNİNİ bağlamda görmeli: %q", *seen)
	}
	if strings.Contains((*seen)[0], "Restart svc-orders\n") {
		t.Error("bağlamda sayfa ADI olmamalı")
	}
	a := answerOf(t, *ev)
	if !strings.Contains(a["text"].(string), "rollout restart") {
		t.Errorf("cevap anlatım olmalı: %v", a["text"])
	}
	links, _ := a["links"].([]guidedAnswerLink)
	if len(links) != 1 || !strings.HasPrefix(links[0].Href, "https://devops.example.test/") {
		t.Errorf("kaynak çipi: %+v", a["links"])
	}
	if st.upserts == 0 {
		t.Error("karma modda canlı okunan sayfa yerel depoya yazılmalı")
	}
	if len(api.searches) == 0 || strings.Contains(wiki.Fold(api.searches[0]), "nasil") {
		t.Errorf("canlı sorgu soru sözcüksüz olmalı: %q", api.searches)
	}
}

func TestWikiTierDominantPageReadDepth(t *testing.T) {
	long := runbookText + "\n" + strings.Repeat("Ek adım: svc-orders kontrol listesi satırı. ", 120)
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": long}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := withFakeNarrator(t, "tamam")
	_, emit := collectEmit()
	msgs := []copilot.ChatMessage{{Role: "user", Text: "wikide svc-orders restart adımları"}}
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, msgs, wikiTierContext{}); !h {
		t.Fatal("kademe cevaplamalı")
	}
	ctxPart := (*seen)[0][strings.Index((*seen)[0], "BAĞLAM:"):]
	if n := len([]rune(ctxPart)); n < 3000 || n > wikiDominantRunes+800 {
		t.Errorf("baskın sayfada okuma derinliği ~%d olmalı, bağlam %d rune", wikiDominantRunes, n)
	}
}

func TestWikiTierNonWikiQuestionUnchanged(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := withFakeNarrator(t, "x")
	ev, emit := collectEmit()
	for _, q := range []string{"svc-orders hata oranı nedir", "son 1 saatte en yavaş endpoint hangisi", "payments p95 neden yükseldi"} {
		h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, []copilot.ChatMessage{{Role: "user", Text: q}}, wikiTierContext{})
		if h {
			t.Errorf("işaretsiz telemetri sorusu wiki kademesine girmemeli: %q", q)
		}
	}
	if len(*ev) != 0 || len(*seen) != 0 || len(api.searches) != 0 {
		t.Errorf("işaretsiz soruda hiçbir olay/arama olmamalı: %+v %v", *ev, api.searches)
	}
	// API token'ı: kademe kapalı (RAG wiki yarısıyla aynı kapı).
	tok := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "token:t1", Role: auth.RoleAdmin})
	if h, _ := (&Server{}).wikiChatAnswer(tok, emit, []copilot.ChatMessage{{Role: "user", Text: "svc-orders runbook"}}, wikiTierContext{}); h {
		t.Error("API token'ı wiki kademesini kullanamaz")
	}
}

func TestWikiTierNotFoundIsExplicit(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	seen := withFakeNarrator(t, "x")
	ev, emit := collectEmit()
	h, ok := (&Server{}).wikiChatAnswer(sessionCtx(), emit, []copilot.ChatMessage{{Role: "user", Text: "kafka sertifika yenileme runbook"}}, wikiTierContext{})
	if !h || !ok {
		t.Fatal("güçlü işaretli soru bulunamasa da kademede bitmeli (telemetriye kaçmaz)")
	}
	if a := answerOf(t, *ev); !strings.HasPrefix(a["text"].(string), "Wikide bulunamadı") {
		t.Errorf("açık 'Wikide bulunamadı': %v", a["text"])
	}
	if len(*seen) != 0 {
		t.Error("bağlam yokken model çağrılmamalı")
	}
	// Zayıf işaret (doküman) + sonuç yok → yüklü doküman RAG'ına bırakılır.
	ev2, emit2 := collectEmit()
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit2, []copilot.ChatMessage{{Role: "user", Text: "dokümanda kafka sertifika"}}, wikiTierContext{}); h || len(*ev2) > 1 {
		t.Errorf("zayıf işarette boş sonuç akışı RAG'a sürmeli: %v %+v", h, *ev2)
	}
}

func TestWikiTierLiveModeSearchUnavailable(t *testing.T) {
	api := &fakeWikiAPI{unavailable: true, pages: map[string]string{}}
	withWikiService(t, wiki.Config{Enabled: true, Mode: wiki.ModeLive}, api)
	withFakeNarrator(t, "x")
	ev, emit := collectEmit()
	h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, []copilot.ChatMessage{{Role: "user", Text: "svc-orders runbook"}}, wikiTierContext{})
	if !h {
		t.Fatal("kademe cevaplamalı")
	}
	if a := answerOf(t, *ev); !strings.Contains(a["text"].(string), wiki.NoteSearchUnavailableLive) {
		t.Errorf("canlı modda Search yok mesajı: %v", a["text"])
	}
}

func TestWikiTierLiveModeNoCHWrites(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}}
	_, st := withWikiService(t, wiki.Config{Enabled: true, Mode: wiki.ModeLive}, api)
	seen := withFakeNarrator(t, "ok")
	_, emit := collectEmit()
	if h, ok := (&Server{}).wikiChatAnswer(sessionCtx(), emit, []copilot.ChatMessage{{Role: "user", Text: "svc-orders runbook"}}, wikiTierContext{}); !h || !ok {
		t.Fatal("canlı mod kademede cevaplamalı")
	}
	if st.upserts != 0 {
		t.Errorf("canlı mod CH'ye yazmamalı: %d", st.upserts)
	}
	if !strings.Contains((*seen)[0], "kubectl rollout restart") {
		t.Error("canlı modda da sayfa metni modele gitmeli")
	}
}

func TestWikiQuestionCue(t *testing.T) {
	cases := []struct {
		q            string
		strong, weak bool
	}{
		// Güçlü: yalnız açık wiki/doküman sözcükleri (inceleme F2).
		{"svc-orders runbook'u nedir", true, false},
		{"Wikide ödeme akışı nasıl anlatılıyor", true, false},
		{"restart prosedürü", true, false},
		{"kafka kılavuzu", true, false},
		{"dokümantasyonda ERR-1042", true, false},
		{"incident playbook", true, false},
		// Zayıf: telemetri sorularında da geçen kalıplar.
		{"rollback nasıl yapılır", false, true},
		{"How do I rotate the kafka cert", false, true},
		{"how to restart svc-orders", false, true},
		{"dokümanda ERR-1042", false, true},
		{"svc-orders'ta bunu nasıl yaparım", false, true},
		{"how do we compare to last week", false, true},
		// İşaretsiz.
		{"svc-orders hata oranı nedir", false, false},
		{"son deploy ne zaman", false, false},
		{"nasıl gidiyor", false, false},
	}
	for _, c := range cases {
		s, w := wikiQuestionCue(c.q)
		if s != c.strong || w != c.weak {
			t.Errorf("wikiQuestionCue(%q) = (%v,%v), want (%v,%v)", c.q, s, w, c.strong, c.weak)
		}
	}
}

func TestBuildWikiContextShapes(t *testing.T) {
	h := []wiki.Hit{
		{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/a", Title: "Gizli", Heading: "Adımlar", Text: "parça-a"}, Score: 0.9},
		{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/b", Heading: "Diğer", Text: "parça-b"}, Score: 0.5},
	}
	if !wikiDominantPage(h) {
		t.Error("0.9 ≥ 1.35×0.5 → baskın")
	}
	page := strings.Repeat("ş", wikiDominantRunes+500)
	got := buildWikiContext(h, page, numberSources(nil))
	if !strings.Contains(got, "[1] wiki (sayfanın tamamı") || !strings.Contains(got, "[2] wiki — Diğer") || strings.Contains(got, "Gizli") {
		t.Errorf("baskın sayfa bloğu + bir karşılaştırma parçası: %q", got[:120])
	}
	if strings.Count(got, "ş") != wikiDominantRunes {
		t.Errorf("baskın metin %d rune'da kesilmeli", wikiDominantRunes)
	}
	h[1].Score = 0.8
	if wikiDominantPage(h) {
		t.Error("yakın skorlar baskın değil")
	}
	// Önde iki doküman kaynağı (RAG): wiki blokları 3 ve 4 numarayı alır.
	if got := buildWikiContext(h, "", numberSources([]chatSource{{Doc: "a.pdf"}, {Doc: "b.pdf"}})); !strings.HasPrefix(got, "[3] wiki — Adımlar") || !strings.Contains(got, "[4] wiki — Diğer") {
		t.Errorf("parça bağlamı: %q", got)
	}
}

// Kaynak pini: açık wiki kademesi guided'dan ÖNCE ve handled'da döner.
func TestWikiTierBeforeGuided(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	wi := strings.Index(src, "s.wikiChatAnswer(ctx, emit, req.Messages, wikiTC); handled {")
	gi := strings.Index(src, "s.copilotChatGuided(ctx, emit,")
	if wi < 0 || gi < 0 || wi > gi {
		t.Fatalf("wiki kademesi guided'dan ÖNCE olmalı (wiki=%d guided=%d)", wi, gi)
	}
	if !strings.Contains(src[wi:gi], "return") {
		t.Error("wiki kademesi handled olduğunda dönmeli")
	}
}
