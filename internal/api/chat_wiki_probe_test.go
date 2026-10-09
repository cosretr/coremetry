package api

// v0.10.1143 — operatör (prod): "Sık karşılaşılan cache refresh hataları
// nelerdir?" telemetriden (filo geneli açık problemler) cevaplandı; wiki'de
// "Cache Refresh Akışı" sayfası vardı. Sözleşme (İÇERİK tabanlı yoklama):
//   - işaretsiz soru, çapasız, servis adsız → TEK yerel lexical arama
//     (canlı ADO yok); kapı: kapsam ≥ wikiProbeCoverage + başlık/başlık yolu
//     eşleşmesi; genel telemetri sözcükleri kapsamaya sayılmaz;
//   - kapı geçerse wiki anlatımı + "Telemetri için:" rota çipi;
//   - kapı geçmezse HİÇBİR olay yok (bayt bayt eski yol);
//   - telemetri soruları ("Açık problemler nelerdir?", "svc-orders hataları
//     neler", "son 1 saatte hata oranı") hiç aramaz;
//   - guidedProblems filo sıfır-eşleşmesinde yalnız bağlantı ipucu.
// Adlar sentetik (svc-orders, example.test).

import (
	"context"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// ── yerel indeksli sahte depo (CH countEqual'ının ikizi) ──

type indexedWikiStore struct {
	*fakeWikiStore
	imu     sync.Mutex
	recs    []wiki.PageRecord
	chunks  [][]wiki.ChunkRecord
	queries int // WikiTermStats + WikiCandidates çağrıları
}

func newIndexedWikiStore(pages map[string]string) *indexedWikiStore {
	st := &indexedWikiStore{fakeWikiStore: newFakeWikiStore()}
	for path, content := range pages {
		rec := wiki.PageRecord{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: path,
			Title: wiki.PageTitle(path), URL: "https://devops.example.test/Platform/_wiki/wikis/Platform.wiki?pagePath=" + path, Content: content}
		st.recs = append(st.recs, rec)
		st.chunks = append(st.chunks, wiki.BuildChunks(rec.Title, content))
		st.pages[rec.WikiID+rec.Path] = rec
	}
	return st
}

func countTok(xs []string, t string) uint32 {
	var n uint32
	for _, x := range xs {
		if x == t {
			n++
		}
	}
	return n
}

func (m *indexedWikiStore) WikiTermStats(_ context.Context, terms []string, _ string) (wiki.Stats, error) {
	m.imu.Lock()
	defer m.imu.Unlock()
	m.queries++
	st := wiki.Stats{DF: make([]uint64, len(terms))}
	var dl float64
	for _, cs := range m.chunks {
		for _, c := range cs {
			st.N++
			dl += float64(len(c.Tokens))
			for i, t := range terms {
				if countTok(c.Tokens, t) > 0 {
					st.DF[i]++
				}
			}
		}
	}
	if st.N > 0 {
		st.AvgDL = dl / float64(st.N)
	}
	return st, nil
}

func (m *indexedWikiStore) WikiCandidates(_ context.Context, q wiki.CandidateQuery, _ string, _ int) ([]wiki.Candidate, error) {
	m.imu.Lock()
	defer m.imu.Unlock()
	m.queries++
	var out []wiki.Candidate
	for pi, cs := range m.chunks {
		p := m.recs[pi]
		for _, c := range cs {
			cd := wiki.Candidate{ChunkRef: wiki.ChunkRef{Project: p.Project, WikiID: p.WikiID, WikiName: p.WikiName,
				Path: p.Path, Title: p.Title, URL: p.URL, Heading: c.Heading, Idx: c.Idx, Text: c.Text},
				TF: make([]uint32, len(q.Tokens)), HTF: make([]uint32, len(q.Tokens)), DL: uint32(len(c.Tokens))}
			any := false
			for i, t := range q.Tokens {
				cd.TF[i], cd.HTF[i] = countTok(c.Tokens, t), countTok(c.HeadTokens, t)
				any = any || cd.TF[i] > 0 || cd.HTF[i] > 0
			}
			if any {
				out = append(out, cd)
			}
		}
	}
	return out, nil
}

func (m *indexedWikiStore) searchQueries() int {
	m.imu.Lock()
	defer m.imu.Unlock()
	return m.queries
}

// withIndexedWiki — yerel indeksli wiki servisi (karma mod; ADO sahte ve sayaçlı).
func withIndexedWiki(t *testing.T, pages map[string]string) (*indexedWikiStore, *fakeWikiAPI) {
	t.Helper()
	st := newIndexedWikiStore(pages)
	api := &fakeWikiAPI{pages: pages}
	svc := wiki.New(st, func() wiki.API { return api }, nil)
	svc.Configure(wiki.Config{Enabled: true})
	prev := wikiKB()
	SetWiki(svc)
	t.Cleanup(func() { SetWiki(prev) })
	return st, api
}

const cacheRefreshPage = "# Cache Refresh Akışı\nBSA cache refresh işlemi yönetim ekranından tetiklenir ve tüm düğümlerde cache yeniden yüklenir.\n" +
	"## Sık karşılaşılan hatalar\n- CR-401: refresh yetki belirteci süresi dolmuş; yeniden giriş yapın.\n- CR-503: cache düğümü yanıt vermiyor; düğümü yeniden başlatın ve refresh'i tekrarlayın."

var probeOtherPages = map[string]string{
	"/Platform/Ödeme akışı":    "# Ödeme akışı\nÖdeme isteği svc-payments üzerinden ledger servisine gider.",
	"/Platform/Kafka konuları": "# Kafka konuları\norders-events konusu sipariş olaylarını taşır; tüketici grubu orders-consumer.",
	"/Platform/Sertifikalar":   "# Sertifikalar\nTLS sertifikaları her yıl yenilenir; yenileme adımları burada.",
}

func probePages(extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range probeOtherPages {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

const cacheRefreshQ = "Sık karşılaşılan cache refresh hataları nelerdir?"

// runProbeTier — copilotChat'in yoklama kurulumu + wiki kademesi (SSE'siz).
func runProbeTier(t *testing.T, s *Server, ctx context.Context, q string, tc wikiTierContext) (context.Context, []emitted, bool) {
	t.Helper()
	ev, emit := collectEmit()
	msgs := []copilot.ChatMessage{{Role: "user", Text: q}}
	ctx = s.armWikiProbe(ctx, msgs, tc)
	h, _ := s.wikiChatAnswer(ctx, emit, msgs, tc)
	return ctx, *ev, h
}

func TestWikiProbeTerms(t *testing.T) {
	cases := []struct {
		q    string
		want []string
	}{
		{cacheRefreshQ, []string{"cache", "refresh"}},
		{"Açık problemler nelerdir?", nil},
		{"svc-orders hataları neler", []string{"svc-orders", "svc", "orders"}},
		{"son 1 saatte hata oranı", []string{"1", "saatte"}},
		{"yaygın kafka consumer lag sorunları", []string{"kafka", "consumer", "lag"}},
		{"hatalar", nil},
		{"en yavaş trace'leri göster", nil},
	}
	for _, c := range cases {
		if got := wikiProbeTerms(c.q); !reflect.DeepEqual(got, c.want) {
			t.Errorf("wikiProbeTerms(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestWikiProbeTelemetryAnchor(t *testing.T) {
	for _, c := range []struct {
		q    string
		want bool
	}{
		{cacheRefreshQ, false},
		{"cache refresh akışı nasıl işler", false},
		{"son 1 saatte hata oranı", true},
		{"bugünkü cache refresh hataları", true},
		{"dün gece cache refresh hataları", true},
		{"şu an cache refresh hataları", true},
		{"cache refresh 4bf92f3577b34da6a3ce929d0e0e4736 trace'i", true},
		{"P-3f9a2 cache refresh problemi", true},
		{"cache refresh 500ms üzeri", true},
		{"cache refresh hata oranı %5", true},
		{"latency > 250 olan cache refresh", true},
		{"cache refresh 3 üzeri tekrar", true},
		{"CR-401 hatası ne demek", false},
	} {
		if got := wikiProbeTelemetryAnchor(c.q); got != c.want {
			t.Errorf("wikiProbeTelemetryAnchor(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestWikiProbeStrongGate(t *testing.T) {
	h := func(cov float64, head, live bool) wiki.Hit {
		return wiki.Hit{TermCoverage: cov, HeadMatch: head, Live: live, Score: cov}
	}
	for _, c := range []struct {
		name string
		hits []wiki.Hit
		want bool
	}{
		{"boş", nil, false},
		{"tam kapsam + başlık", []wiki.Hit{h(1, true, false)}, true},
		{"taban üstü + başlık", []wiki.Hit{h(wikiProbeCoverage, true, false)}, true},
		{"taban altı", []wiki.Hit{h(wikiProbeCoverage-0.01, true, false)}, false},
		{"başlık eşleşmesi yok", []wiki.Hit{h(1, false, false)}, false},
		{"canlı isabet", []wiki.Hit{h(1, true, true)}, false},
		{"yalnız EN İYİ isabet sayılır", []wiki.Hit{h(0.5, true, false), h(1, true, false)}, false},
	} {
		if got := wikiProbeStrong(c.hits); got != c.want {
			t.Errorf("%s: wikiProbeStrong = %v, want %v", c.name, got, c.want)
		}
	}
}

// Ön koşul: bu soru bugün guided'da filo geneli guidedProblems'e düşüyor.
func TestCacheRefreshQuestionRoutesToFleetProblems(t *testing.T) {
	if s, w := wikiQuestionCue(cacheRefreshQ); s || w {
		t.Fatal("ön koşul: soru wiki işareti taşımıyor")
	}
	r := routeGuidedIntent(cacheRefreshQ, rescueSvcs, nil, nil, "")
	if r.Intent != guidedProblems || r.Service != "" {
		t.Fatalf("ön koşul: filo geneli guidedProblems (%s %q)", r.Intent, r.Service)
	}
}

func TestWikiProbeCacheRefreshAnswersFromWiki(t *testing.T) {
	st, api := withIndexedWiki(t, probePages(map[string]string{"/Platform/Cache Refresh Akışı": cacheRefreshPage}))
	seen := withFakeNarrator(t, "Sık görülen hatalar CR-401 ve CR-503'tür [1].")
	_, ev, h := runProbeTier(t, guidedTestServer(t), sessionCtx(), cacheRefreshQ, wikiTierContext{})
	if !h {
		t.Fatal("yoklama kapıyı geçmeli ve kademe cevaplamalı")
	}
	var kinds []string
	for _, e := range ev {
		kinds = append(kinds, e.kind)
	}
	if !reflect.DeepEqual(kinds, []string{"step", "answer"}) {
		t.Fatalf("olay sırası: %v", kinds)
	}
	if lbl := ev[0].v.(map[string]string)["label"]; lbl != "kurum wiki'si" {
		t.Errorf("adım: %q", lbl)
	}
	if len(*seen) != 1 || !strings.Contains((*seen)[0], "CR-503") || !strings.Contains((*seen)[0], "<wiki_data>") {
		t.Fatalf("model sayfa metnini görmeli: %v", *seen)
	}
	a := answerOf(t, ev)
	txt := a["text"].(string)
	if !strings.HasPrefix(txt, "Sık görülen hatalar") || !strings.Contains(txt, telemetryChipsHeading+"Açık problemler") {
		t.Errorf("wiki cevabı + Telemetri için çipi: %q", txt)
	}
	if chips, _ := a["suggestions"].([]string); !reflect.DeepEqual(chips, []string{"Açık problemleri göster"}) {
		t.Errorf("telemetri çipi: %v", a["suggestions"])
	}
	links, _ := a["links"].([]guidedAnswerLink)
	var hrefs []string
	for _, l := range links {
		hrefs = append(hrefs, l.Href)
	}
	if len(hrefs) < 2 || !strings.Contains(hrefs[0], "Cache Refresh") || !strings.Contains(strings.Join(hrefs, " "), "/problems") {
		t.Errorf("wiki kaynak çipi + Problemler bağlantısı: %v", hrefs)
	}
	if len(api.searches) != 0 {
		t.Errorf("yoklama canlı ADO araması yapmamalı: %v", api.searches)
	}
	if q := st.searchQueries(); q != 2 { // tek arama = WikiTermStats + WikiCandidates
		t.Errorf("tek yerel arama bekleniyordu, CH okuması: %d", q)
	}
}

// Kapı geçmeyen durumlar: hiçbir olay yok (bayt bayt eski yol).
func TestWikiProbeSilentWhenGateFails(t *testing.T) {
	cases := []struct {
		name  string
		pages map[string]string
	}{
		{"eşleşen sayfa yok", probePages(nil)},
		// Gövde tüm içerik terimlerini taşıyor ama başlık/başlık yolunda hiçbiri yok.
		{"başlıkta terim yok", probePages(map[string]string{"/Platform/Operasyon notları": "# Operasyon notları\n## Genel\nCache refresh sırasında hata görülürse ekibe haber verin."})},
	}
	for _, c := range cases {
		st, api := withIndexedWiki(t, c.pages)
		seen := withFakeNarrator(t, "x")
		ctx, ev, h := runProbeTier(t, guidedTestServer(t), sessionCtx(), cacheRefreshQ, wikiTierContext{})
		if h || len(ev) != 0 || len(*seen) != 0 {
			t.Errorf("%s: olay basılmamalı: h=%v ev=%+v", c.name, h, ev)
		}
		if len(api.searches) != 0 || st.searchQueries() != 2 {
			t.Errorf("%s: tek yerel arama, canlı yok: ado=%v ch=%d", c.name, api.searches, st.searchQueries())
		}
		// Sıfır-eşleşme ipucu aynı sonucu kullanır: ikinci arama yok, metin aynen.
		markFleetZeroMatch(ctx)
		txt, links := guidedTestServer(t).wikiZeroMatchHint(ctx, cacheRefreshQ, "T", nil)
		if txt != "T" || links != nil || st.searchQueries() != 2 {
			t.Errorf("%s: ipucu değişmemeli ve yeniden aramamalı (%q %v %d)", c.name, txt, links, st.searchQueries())
		}
	}
}

// Telemetri soruları: wiki açık ve eşleşen sayfa varken de hiç aranmaz.
func TestWikiProbeTelemetryQuestionsNeverSearch(t *testing.T) {
	st, api := withIndexedWiki(t, probePages(map[string]string{
		"/Platform/Cache Refresh Akışı": cacheRefreshPage,
		"/Platform/Açık problemler":     "# Açık problemler\nAçık problemler triage ekranında listelenir.",
		"/Platform/svc-orders hataları": "# svc-orders hataları\nsvc-orders hata kodları ve hata oranı eşikleri.",
	}))
	seen := withFakeNarrator(t, "x")
	for _, q := range []string{
		"Açık problemler nelerdir?", "svc-orders hataları neler", "son 1 saatte hata oranı",
		"bugünkü hatalar", "hata oranı en yüksek servisler", "svc-orders son 1 saat hataları",
		"son 1 saatte cache refresh hataları", "şu an cache refresh hataları",
	} {
		_, ev, h := runProbeTier(t, guidedTestServer(t), sessionCtx(), q, wikiTierContext{})
		if h || len(ev) != 0 {
			t.Errorf("%q: kademe girmemeli (%+v)", q, ev)
		}
	}
	if st.searchQueries() != 0 || len(api.searches) != 0 || len(*seen) != 0 {
		t.Errorf("telemetri sorusunda arama olmamalı: ch=%d ado=%v", st.searchQueries(), api.searches)
	}
}

// Kapı: token / panel-çekmece bağlamı / wiki kapalı → kurulum yok, arama yok.
func TestWikiProbeGatesUnchanged(t *testing.T) {
	st, _ := withIndexedWiki(t, probePages(map[string]string{"/Platform/Cache Refresh Akışı": cacheRefreshPage}))
	withFakeNarrator(t, "x")
	msgs := []copilot.ChatMessage{{Role: "user", Text: cacheRefreshQ}}
	s := guidedTestServer(t)
	tok := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "token:t1", Role: auth.RoleAdmin})
	for _, c := range []struct {
		name string
		ctx  context.Context
		tc   wikiTierContext
	}{
		{"API token'ı", tok, wikiTierContext{}},
		{"servis paneli", sessionCtx(), wikiTierContext{Service: "svc-orders"}},
		{"çekmece", sessionCtx(), wikiTierContext{Explain: "x"}},
		{"trace", sessionCtx(), wikiTierContext{Trace: "4bf92f3577b34da6a3ce929d0e0e4736"}},
	} {
		if got := s.armWikiProbe(c.ctx, msgs, c.tc); got != c.ctx {
			t.Errorf("%s: yoklama kurulmamalı", c.name)
		}
		ev, emit := collectEmit()
		if h, _ := s.wikiChatAnswer(c.ctx, emit, msgs, c.tc); h || len(*ev) != 0 {
			t.Errorf("%s: kademe girmemeli", c.name)
		}
	}
	if st.searchQueries() != 0 {
		t.Errorf("kapalı kapıda arama: %d", st.searchQueries())
	}
	SetWiki(nil)
	if got := s.armWikiProbe(sessionCtx(), msgs, wikiTierContext{}); wikiProbeFromCtx(got) != nil {
		t.Error("wiki yokken kurulmamalı")
	}
}

// Ters yön: guidedProblems filo sıfır-eşleşmesi → yalnız bağlantı ipucu,
// yalnız kapı geçerse.
func TestWikiZeroMatchHint(t *testing.T) {
	q := "cache refresh hataları neler"
	withIndexedWiki(t, probePages(map[string]string{"/Platform/Cache Refresh Akışı": cacheRefreshPage}))
	s := guidedTestServer(t)
	msgs := []copilot.ChatMessage{{Role: "user", Text: q}}

	ctx := s.armWikiProbe(sessionCtx(), msgs, wikiTierContext{})
	if txt, links := s.wikiZeroMatchHint(ctx, q, "T", nil); txt != "T" || links != nil {
		t.Errorf("işaretsiz: aynen dönmeli (%q %v)", txt, links)
	}
	markFleetZeroMatch(ctx)
	txt, links := s.wikiZeroMatchHint(ctx, q, "T", nil)
	if txt != "T\n\n"+wikiProbeZeroHintPrefix+"Cache Refresh Akışı" || len(links) != 1 || !strings.Contains(links[0].Href, "Cache Refresh") {
		t.Errorf("işaretli + kapı geçti: %q %+v", txt, links)
	}

	withIndexedWiki(t, probePages(nil))
	ctx = s.armWikiProbe(sessionCtx(), msgs, wikiTierContext{})
	markFleetZeroMatch(ctx)
	if txt, links := s.wikiZeroMatchHint(ctx, q, "T", nil); txt != "T" || links != nil {
		t.Errorf("kapı geçmedi: aynen dönmeli (%q %v)", txt, links)
	}
	// Kurulum yok (token) → işaret etkisiz.
	markFleetZeroMatch(sessionCtx())
}

// Çipler kendi rotalarına gider ve yoklamayı tetiklemez (döngü yok).
func TestWikiProbeRouteChipsRouteBack(t *testing.T) {
	for _, intent := range []guidedIntent{guidedProblems, guidedSlowTraces, guidedLogErrors, guidedDeployImpact} {
		_, chip, ok := wikiProbeRouteChip(guidedRoute{Intent: intent})
		if !ok {
			t.Fatalf("%s: çip bekleniyordu", intent)
		}
		if r := routeGuidedIntent(chip, rescueSvcs, nil, nil, ""); r.Intent != intent {
			t.Errorf("%q → %s, want %s", chip, r.Intent, intent)
		}
		if s, w := wikiQuestionCue(chip); s || w {
			t.Errorf("%q wiki işareti taşımamalı", chip)
		}
		if !wikiProbeTelemetryAnchor(chip) && len(wikiProbeTerms(chip)) >= wikiProbeMinTerms {
			t.Errorf("%q yoklamayı tetiklememeli (%v)", chip, wikiProbeTerms(chip))
		}
	}
	if _, _, ok := wikiProbeRouteChip(guidedRoute{Intent: guidedProblems, Service: "svc-orders"}); ok {
		t.Error("servisli rota çip üretmemeli")
	}
}

// Kaynak pini: sıfır-eşleşme işareti ÜRETİM noktasında, ipucu cevap
// yükünden önce; yoklama kurulumu wiki kademesinden önce.
func TestWikiProbeWiringPinned(t *testing.T) {
	read := func(f string) string {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		return stripGoCommentsAPI(string(b))
	}
	g := read("copilot_guided.go")
	if !strings.Contains(g, "if len(probs) == 0 && service == \"\" {\n\t\tmarkFleetZeroMatch(ctx)") {
		t.Error("guidedProblemsBundle sıfır-eşleşmeyi işaretlemeli")
	}
	hi, ai := strings.Index(g, "s.wikiZeroMatchHint(ctx, question, answer, links)"), strings.Index(g, "\"suggestions\": guidedSuggestions(route),\n\t\t\"links\":       dedupLinksByHref(links),")
	if hi < 0 || ai < 0 || hi > ai {
		t.Errorf("ipucu cevap yükünden önce (hint=%d answer=%d)", hi, ai)
	}
	c := read("copilot_chat.go")
	pi, wi := strings.Index(c, "ctx = s.armWikiProbe(ctx, req.Messages, wikiTC)"), strings.Index(c, "s.wikiChatAnswer(ctx, emit, req.Messages, wikiTC)")
	if pi < 0 || wi < 0 || pi > wi {
		t.Errorf("yoklama kurulumu wiki kademesinden önce (arm=%d tier=%d)", pi, wi)
	}
}
