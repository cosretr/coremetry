package api

// v0.10.1136 — CoSRE wiki çok-kaynaklı okuma + iki aşamalı sayfa seçimi.
// Sözleşmeler: 3–5 farklı sayfa (taban + sayfa düzeyi tekillik), isabetin
// komşuları ve bölümü belge sırasında tekrarsız, bütçe modelden/ayardan ve
// kelepçeli, RAG yarısı küçük pay, canlı mod CH'ye yazmaz, bağlam [n] = çip
// sırası, model seçimi başarısızsa skor sırası. Adlar sentetik (example.test).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// ── yardımcılar ─────────────────────────────────────────────────────────────

// mpPage — "# başlık" + n bölüm; her bölüm tek parça (≤2000 rune) ve kendi
// işaretini taşır: <tag>-S<i>.
func mpPage(tag string, sections ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", tag)
	for i, s := range sections {
		fmt.Fprintf(&b, "## %s\n%s-S%d %s\n\n", s, tag, i, strings.Repeat("svc-orders adım açıklaması. ", 8))
	}
	return b.String()
}

// seedWikiPages — yerel depoya sayfalar (karma mod: PageChunks → ReadPage →
// GetWikiPage → BuildChunks) ve her sayfanın parçaları.
func seedWikiPages(st *fakeWikiStore, pages map[string]string) map[string][]wiki.ChunkRecord {
	out := map[string][]wiki.ChunkRecord{}
	for path, c := range pages {
		title := wiki.PageTitle(path)
		st.pages["w1"+path] = wiki.PageRecord{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: path,
			Title: title, URL: "https://devops.example.test/Platform/_wiki/wikis/Platform.wiki?pagePath=" + path, Content: c}
		out[path] = wiki.BuildChunks(title, c)
	}
	return out
}

func mpHit(path string, chunks []wiki.ChunkRecord, idx uint32, score float64) wiki.Hit {
	c := chunks[idx]
	return wiki.Hit{ChunkRef: wiki.ChunkRef{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: path,
		Title: wiki.PageTitle(path), URL: "https://devops.example.test/Platform/_wiki/wikis/Platform.wiki?pagePath=" + path,
		Heading: c.Heading, Idx: idx, Text: c.Text}, Score: score}
}

// withFakeSelector — sahte seçim modeli: kullanıcı bloğunu yakalar.
func withFakeSelector(t *testing.T, fn func(ctx context.Context, user string) (string, error)) *[]string {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	prev := wikiSelectFn
	wikiSelectFn = func(_ *Server, ctx context.Context, system, user string) (string, error) {
		mu.Lock()
		seen = append(seen, user)
		mu.Unlock()
		if system != copilot.SystemPromptWikiSelect() {
			t.Errorf("seçim çağrısı seçim prompt'uyla gitmeli")
		}
		return fn(ctx, user)
	}
	t.Cleanup(func() { wikiSelectFn = prev })
	return &seen
}

// mpFixture — 7 sayfa: P1..P5 tabanı geçer (P1 iki isabetli), P6 taban (0.3)
// altı, P7 göreli tabanın (0.5×0.9) altı ama mutlak tabanın üstü (aday).
func mpFixture(t *testing.T, cfg wiki.Config) (*wiki.Service, *fakeWikiStore, []wiki.Hit) {
	t.Helper()
	pages := map[string]string{}
	for i := 1; i <= 7; i++ {
		pages[fmt.Sprintf("/Runbooks/P%d", i)] = mpPage(fmt.Sprintf("P%d", i), "Giriş", "Kurulum", "Adımlar", "Doğrulama", "Ek")
	}
	pages["/Runbooks/P3"] = mpPage("P3", "Giriş", "Kurulum", "Adımlar", "Doğrulama", "Ek") + "## Son\nSON-BÖLÜM-P3 kapanış.\n"
	cfg.Enabled = true
	svc, st := withWikiService(t, cfg, &fakeWikiAPI{pages: map[string]string{}})
	ch := seedWikiPages(st, pages)
	p := func(i int) string { return fmt.Sprintf("/Runbooks/P%d", i) }
	hits := []wiki.Hit{
		mpHit(p(1), ch[p(1)], 2, 0.9),
		mpHit(p(1), ch[p(1)], 2, 0.88), // aynı parça (tekil)
		mpHit(p(1), ch[p(1)], 4, 0.85),
		mpHit(p(2), ch[p(2)], 1, 0.8),
		mpHit(p(3), ch[p(3)], 0, 0.7),
		mpHit(p(4), ch[p(4)], 3, 0.6),
		mpHit(p(5), ch[p(5)], 2, 0.5),
		mpHit(p(7), ch[p(7)], 2, 0.4),
		mpHit(p(6), ch[p(6)], 2, 0.2),
	}
	return svc, st, hits
}

// ── sayfa seçimi ────────────────────────────────────────────────────────────

func TestWikiMultiPageSelectionFloorDedupe(t *testing.T) {
	_, _, hits := mpFixture(t, wiki.Config{})
	cands := wikiGroupPages(hits, wikiTierFloor, wikiSelectMaxCandidates)
	if len(cands) != 6 {
		t.Fatalf("taban altı P6 düşer, P7 aday kalır: %d aday", len(cands))
	}
	if len(cands[0].Hits) != 2 || cands[0].Hits[1].Idx != 4 {
		t.Errorf("aynı sayfanın isabetleri tek sayfada, aynı parça tekil: %+v", cands[0].Hits)
	}
	got := wikiScorePages(cands)
	if len(got) != wikiMaxPages {
		t.Fatalf("skor tabanlı seçim 5 sayfa: %d", len(got))
	}
	for i, p := range got {
		if want := fmt.Sprintf("/Runbooks/P%d", i+1); p.top().Path != want {
			t.Errorf("sayfa %d = %s, want %s (skor sırası)", i, p.top().Path, want)
		}
	}
	// wikiTierSelect: aday kümesi (≤10 sayfa, taban üstü).
	if n := len(wikiGroupPages(wikiTierSelect(hits), 0, 0)); n != 6 {
		t.Errorf("wikiTierSelect 6 aday sayfa vermeli: %d", n)
	}
	// RAG yarısı: en çok 3 sayfa, en iyi skor taban altıysa hiç.
	if n := len(wikiGroupPages(ragWikiSelect(hits), 0, 0)); n != ragWikiMaxPages {
		t.Errorf("RAG wiki yarısı %d sayfa: %d", ragWikiMaxPages, n)
	}
}

// Çok sayfalı anlatım: 5 sayfa numaralı, başlıklı ve çitli; çipler ve
// bağlantılar AYNI sırada; okunmayan sayfa çipte yok.
func TestWikiMultiSourcePromptNumberedInChipOrder(t *testing.T) {
	svc, _, hits := mpFixture(t, wiki.Config{DisablePageSelect: true})
	seen := withFakeNarrator(t, "Kurulum [1] ve doğrulama [3].")
	ans, used, err := (&Server{}).wikiNarratedAnswer(sessionCtx(), svc, "svc-orders runbook kurulum", "", wikiTierSelect(hits), func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 1 {
		t.Fatalf("tek anlatım çağrısı: %d", len(*seen))
	}
	user := (*seen)[0][strings.Index((*seen)[0], "\n----\n"):] // yalnız kullanıcı bloğu
	last := -1
	for i := 1; i <= 5; i++ {
		head := fmt.Sprintf("[%d] Wiki · P%d\n<wiki_data>\n", i, i)
		at := strings.Index(user, head)
		if at < 0 || at < last {
			t.Fatalf("kaynak %d başlıkla çitli ve sırada olmalı (%q)", i, head)
		}
		last = at
	}
	if strings.Contains(user, "Wiki · P6") || strings.Contains(user, "Wiki · P7") {
		t.Error("taban altı / göreli taban altı sayfa bağlama girmemeli")
	}
	if strings.Count(user, "<wiki_data>") != 5 || strings.Count(user, "</wiki_data>") != 5 {
		t.Error("her kaynak tek çit")
	}
	srcs, _ := ans["sources"].([]chatSource)
	links, _ := ans["links"].([]guidedAnswerLink)
	if len(srcs) != 5 || len(links) != 5 {
		t.Fatalf("5 çip + 5 bağlantı: %d %d", len(srcs), len(links))
	}
	for i := range srcs {
		want := fmt.Sprintf("P%d", i+1)
		if srcs[i].Label != fmt.Sprintf("Kaynak %d", i+1) || srcs[i].Doc != "Wiki · "+want || links[i].Label != "Wiki · "+want || srcs[i].Ref != links[i].Href {
			t.Errorf("çip %d sırası [n] ile aynı olmalı: %+v / %+v", i, srcs[i], links[i])
		}
	}
	if !strings.Contains(srcs[0].Doc, "P1") || len(srcs[0].Sections) != 2 {
		t.Errorf("P1'in iki isabeti tek çipte: %+v", srcs[0])
	}
	if len(wikiPageRefs(used)) != 5 {
		t.Errorf("hafızaya okunan 5 sayfa: %d", len(wikiPageRefs(used)))
	}
}

// ── genişletme ──────────────────────────────────────────────────────────────

func mpChunks() []wiki.ChunkRecord {
	heads := []string{"", "Kurulum", "Kurulum", "Kurulum › Adımlar", "Kurulum › Adımlar", "Erişim", "Erişim", "Ek", "Ek", "Ek"}
	out := make([]wiki.ChunkRecord, len(heads))
	for i, h := range heads {
		out[i] = wiki.ChunkRecord{Idx: uint32(i), Heading: h, Text: fmt.Sprintf("C%d %s", i, strings.Repeat("x", 90))}
	}
	return out
}

func idxsOf(cs []wiki.ChunkRecord) []uint32 {
	out := make([]uint32, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Idx)
	}
	return out
}

func TestWikiExpandPageNeighboursAndSection(t *testing.T) {
	cs := mpChunks()
	// İsabet 2 (Kurulum): komşular 1,3 + bölümün geri kalanı (alt başlık 4 dahil); 0 ve 5+ yok.
	got := idxsOf(wikiExpandPage(cs, []uint32{2}, 100000, false))
	if fmt.Sprint(got) != "[1 2 3 4]" {
		t.Errorf("komşu + bölüm, belge sırası: %v", got)
	}
	// İki isabet + yinelenen idx: tekrarsız, belge sırası.
	got = idxsOf(wikiExpandPage(cs, []uint32{6, 2, 6}, 100000, false))
	if fmt.Sprint(got) != "[1 2 3 4 5 6 7]" {
		t.Errorf("iki isabet tekrarsız birleşir: %v", got)
	}
	// Dar bütçe: önce isabet, sonra komşular (bölümün uzağı düşer).
	got = idxsOf(wikiExpandPage(cs, []uint32{2}, 3*94, false))
	if fmt.Sprint(got) != "[1 2 3]" {
		t.Errorf("bütçe önceliği isabet → komşu: %v", got)
	}
	// whole: sayfanın tamamı sığıyorsa hepsi.
	if got = idxsOf(wikiExpandPage(cs, []uint32{8}, 100000, true)); len(got) != len(cs) {
		t.Errorf("baskın/seçilen sayfa tamamı: %v", got)
	}
	// İsabet bütçeden büyükse kırpılarak yine girer.
	one := wikiExpandPage(cs, []uint32{2}, 10, false)
	if len(one) != 1 || len([]rune(one[0].Text)) != 11 {
		t.Errorf("ilk isabet kırpılmış girer: %+v", one)
	}
	// idx uyuşmazsa boş (kurucu isabetlere düşer).
	if wikiExpandPage(cs, []uint32{99}, 1000, false) != nil {
		t.Error("bilinmeyen idx → nil")
	}
	// Render: başlık değişimi "## …", aralık "…".
	r := renderWikiChunks(wikiExpandPage(cs, []uint32{2, 8}, 100000, false))
	if !strings.Contains(r, "## Kurulum\nC1") || !strings.Contains(r, "## Kurulum › Adımlar\nC3") || !strings.Contains(r, "…\n\n## Ek\nC7") {
		t.Errorf("render: %q", r)
	}
	if strings.Count(r, "C2 ") != 1 {
		t.Error("parça tekrarı")
	}
}

// ── bütçe ───────────────────────────────────────────────────────────────────

// withModelLimits — sahte model penceresi/completion (wikiModelLimitsFn).
func withModelLimits(t *testing.T, window, completion int) {
	t.Helper()
	prev := wikiModelLimitsFn
	wikiModelLimitsFn = func(*Server, context.Context, string) (int, int) { return window, completion }
	t.Cleanup(func() { wikiModelLimitsFn = prev })
}

func TestWikiBudgetWindowCapsManual(t *testing.T) {
	cases := []struct{ manual, window, completion, want int }{
		{0, 0, 4096, 7788},              // bilinmeyen model: 16000, 8k varsayımıyla kapaklı ((8192-4096-1500)×3)
		{0, 0, 1024, wikiBudgetDefault}, // küçük completion: 16000 sığar
		{0, 8192, 4096, 7788},           // 8k model
		{48000, 8192, 4096, 7788},       // PENCERE KAZANIR — elle değer kapaklanır
		{0, 131072, 4096, wikiBudgetAutoMax},
		{20000, 131072, 4096, 20000}, // büyük pencerede elle değer
		{48000, 131072, 4096, 48000},
		{0, 4096, 4096, wikiBudgetFloor},      // pencere completion'a gidiyor → taban
		{1000, 0, 4096, wiki.MinContextChars}, // bilinmeyen modelde elle değer (aralıkta)
		{90000, 0, 4096, wiki.MaxContextChars},
		{0, 8192, 0, 7788}, // completion bilinmiyor → varsayılan 4096
	}
	for _, c := range cases {
		if got := wikiBudgetFor(c.manual, c.window, c.completion); got != c.want {
			t.Errorf("wikiBudgetFor(%d,%d,%d)=%d want %d", c.manual, c.window, c.completion, got, c.want)
		}
	}
	// Sunucu: ayar + yüzeyin modeli.
	svc, _ := withWikiService(t, wiki.Config{Enabled: true, ContextChars: 9000}, &fakeWikiAPI{})
	withModelLimits(t, 131072, 4096)
	if got := (&Server{}).wikiBudget(sessionCtx(), svc, "wiki-chat"); got != 9000 {
		t.Errorf("büyük pencerede elle bütçe: %d", got)
	}
	withModelLimits(t, 8192, 4096)
	if got := (&Server{}).wikiBudget(sessionCtx(), svc, "rag-chat"); got != 7788 {
		t.Errorf("8k modelde elle bütçe pencereyle kapaklı: %d", got)
	}
}

// Uçtan uca: 8k model + 4096 completion + 48000 elle ayar ve bilinmeyen model
// (otomatik) — anlatım prompt'unun TAMAMI (sistem + önceki tur + soru +
// bağlam) ~3 karakter/jeton tahminiyle pencereye sığar; tüm kademelerde.
func TestWikiPromptFitsWindow(t *testing.T) {
	cases := []struct {
		name           string
		manual, window int
	}{
		{"8k model, elle 48000", 48000, 8192},
		{"bilinmeyen model, otomatik", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, _, hits := mpFixture(t, wiki.Config{ContextChars: c.manual, DisablePageSelect: true})
			_ = svc
			withModelLimits(t, c.window, 4096)
			window := c.window
			if window == 0 {
				window = wikiAssumedWindow
			}
			fits := func(label, prompt string) {
				t.Helper()
				if est := int(float64(runeLen(prompt))/wikiCharsPerToken) + 4096; est > window {
					t.Errorf("%s: tahmini %d jeton (completion dahil) > pencere %d", label, est, window)
				}
			}
			// Uzun önceki tur (tavanlı) + uzun sayfalar.
			msgs := []copilot.ChatMessage{
				{Role: "user", Text: strings.Repeat("önceki soru ", 200)},
				{Role: "assistant", Text: strings.Repeat("önceki cevap ", 400)},
				{Role: "user", Text: "svc-orders runbook kurulum adımları nelerdir"},
			}
			seen := withFakeNarrator(t, "ok")
			if _, _, err := (&Server{}).wikiNarratedAnswer(sessionCtx(), wikiKB(), msgs[2].Text, wikiPriorTurns(msgs), wikiTierSelect(hits), nil); err != nil {
				t.Fatal(err)
			}
			fits("kademe", strings.Replace((*seen)[0], "\n----\n", "", 1))
			// RAG wiki yarısı (doküman parçaları kendi tavanıyla ayrıca gelir; burada yalnız wiki payı).
			rctx, _ := (&Server{}).ragWikiContextFor(sessionCtx(), ragWikiSelect(hits), numberSources(nil))
			fits("rag", copilot.SystemPromptRAGChatWiki()+wikiPriorTurns(msgs)+rctx)
			// Takip (a).
			recs := []*wiki.PageRecord{{Title: "A", URL: "https://devops.example.test/a", Content: strings.Repeat("a ", 30000)},
				{Title: "B", URL: "https://devops.example.test/b", Content: strings.Repeat("b ", 30000)}}
			u := buildWikiFollowUpUser(wikiPriorTurns(msgs), "linki nedir", recs,
				numberSources([]chatSource{wikiRecSource(recs[0]), wikiRecSource(recs[1])}), (&Server{}).wikiBudget(sessionCtx(), wikiKB(), "wiki-chat"))
			fits("takip", copilot.SystemPromptWikiFollowUp()+u)
		})
	}
}

func TestWikiBudgetAllocation(t *testing.T) {
	scores := []float64{0.9, 0.6, 0.5, 0.4, 0.3}
	plan := wikiPlanBudgets(16000, scores, false, false)
	sum := 0
	for i, a := range plan {
		sum += a
		if a > int(0.45*16000) {
			t.Errorf("sayfa %d tavanı aşıyor: %d", i, a)
		}
		if i > 0 && a > plan[0] {
			t.Errorf("en iyi sayfa en büyük payı almalı: %v", plan)
		}
	}
	if sum > 16000 {
		t.Errorf("toplam bütçe aşıldı: %d", sum)
	}
	if d := wikiPlanBudgets(16000, []float64{0.9, 0.4}, true, false); d[0] != int(0.7*16000) {
		t.Errorf("baskın sayfa payı: %v", d)
	}
	if wikiPageCapShare(2, false, true) <= wikiPageCapShare(2, false, false) || wikiPageCapShare(1, false, false) != 1 {
		t.Error("seçimli okuma sayfa başı daha büyük pay; tek sayfa tamamı")
	}
	// Sayfa tabanı (600) bile toplamı aşmaz.
	if p := wikiPlanBudgets(1000, []float64{0.9, 0.8, 0.7}, false, false); p[0]+p[1]+p[2] > 1000 {
		t.Errorf("taban toplamı aşmamalı: %v", p)
	}
	// Kurucu: kullanılmayan pay sonraki sayfaya akar; ÇIKTININ TAMAMI (işaret,
	// "Sayfa:", "## …" satırları dahil) bütçeyi aşmaz.
	long := make([]wiki.ChunkRecord, 40)
	for i := range long {
		long[i] = wiki.ChunkRecord{Idx: uint32(i), Heading: "Bölüm", Text: strings.Repeat("y", 500)}
	}
	short := []wiki.ChunkRecord{{Idx: 0, Text: "kısa sayfa"}}
	pages := []wikiPage{
		{Hits: []wiki.Hit{{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/s", Title: "S"}, Score: 0.9}}},
		{Hits: []wiki.Hit{{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/l", Title: "L", Idx: 20}, Score: 0.8}}},
	}
	for _, budget := range []int{1500, 4000, 8000} {
		ctx, n := buildWikiMultiContext(pages, [][]wiki.ChunkRecord{short, long}, budget, false, numberSources(nil))
		if runeLen(ctx) > budget || n != 2 {
			t.Errorf("bütçe %d: çıktı %d rune, %d sayfa", budget, runeLen(ctx), n)
		}
		if y := strings.Count(ctx, "y"); y < int(0.45*float64(budget))-1000 {
			t.Errorf("bütçe %d: uzun sayfa payına dek dolmalı: %d", budget, y)
		}
	}
	// Bütçe biterse sondaki sayfalar düşer (önek): numaralar/çipler bunlardan.
	many := []wikiPage{pages[1], pages[1], pages[1]}
	many[1] = wikiPage{Hits: []wiki.Hit{{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/l2", Title: "L2", Idx: 20}, Score: 0.8}}}
	many[2] = wikiPage{Hits: []wiki.Hit{{ChunkRef: wiki.ChunkRef{WikiID: "w", Path: "/l3", Title: "L3", Idx: 20}, Score: 0.8}}}
	ctx, n := buildWikiMultiContext(many, [][]wiki.ChunkRecord{long, long, long}, 700, false, numberSources(nil))
	if runeLen(ctx) > 700 || n < 1 || n == 3 {
		t.Errorf("dar bütçede sayfa düşmeli: %d rune, %d sayfa", runeLen(ctx), n)
	}
}

// RAG kademesi bütçenin yarısını, wiki kademesi tamamını kullanır.
func TestWikiBudgetTierShare(t *testing.T) {
	sections := make([]string, 30)
	for i := range sections {
		sections[i] = fmt.Sprintf("Bölüm %d", i)
	}
	_, st := withWikiService(t, wiki.Config{Enabled: true, ContextChars: 8000}, &fakeWikiAPI{})
	withModelLimits(t, 131072, 4096)
	ch := seedWikiPages(st, map[string]string{"/Runbooks/Uzun": mpPage("U", sections...)})
	hits := []wiki.Hit{mpHit("/Runbooks/Uzun", ch["/Runbooks/Uzun"], 15, 0.9)}
	rctx, used := (&Server{}).ragWikiContextFor(sessionCtx(), hits, numberSources(nil))
	full, _ := (&Server{}).wikiReadContext(sessionCtx(), wikiKB(), wikiGroupPages(hits, 0, 0), (&Server{}).wikiBudget(sessionCtx(), wikiKB(), "wiki-chat"), false, numberSources(nil))
	if r := runeLen(rctx); r > 4000 || r < 3000 || len(used) != 1 {
		t.Errorf("RAG wiki yarısı ≤4000 (8000×0.5): %d", r)
	}
	if f := runeLen(full); f > 8000 || f < 6500 {
		t.Errorf("wiki kademesi ≤8000: %d", f)
	}
}

// ── canlı mod ───────────────────────────────────────────────────────────────

// textChunkStore — embedding'siz parça okumasını (wiki.ChunkTextReader) sunan sahte depo.
type textChunkStore struct {
	*fakeWikiStore
	mu       sync.Mutex
	chunks   map[string][]wiki.ChunkRecord
	textRead int
}

func (m *textChunkStore) WikiPageChunkTexts(_ context.Context, wikiID, path string) ([]wiki.ChunkRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.textRead++
	return m.chunks[wikiID+path], nil
}

func (m *textChunkStore) WikiPageChunks(context.Context, string, string) ([]wiki.ChunkRecord, error) {
	panic("sohbet yolu embedding'li parça okumasını kullanmamalı")
}

// Karma mod: sohbet embedding'siz okumayı kullanır; kapsam her yolda denetlenir.
func TestWikiPageChunksTextReaderAndScope(t *testing.T) {
	st := &textChunkStore{fakeWikiStore: newFakeWikiStore(), chunks: map[string][]wiki.ChunkRecord{}}
	content := mpPage("H", "Giriş", "Kurulum")
	st.pages["w1/Runbooks/H"] = wiki.PageRecord{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: "/Runbooks/H", Title: "H", Content: content}
	st.chunks["w1/Runbooks/H"] = []wiki.ChunkRecord{{Idx: 1, Heading: "Kurulum", Text: "b"}, {Idx: 0, Heading: "Giriş", Text: "a", Embedding: []float32{1}}}
	svc := wiki.New(st, func() wiki.API { return &fakeWikiAPI{} }, nil)
	svc.Configure(wiki.Config{Enabled: true})
	cs, err := svc.PageChunks(context.Background(), "Platform", "w1", "Platform.wiki", "/Runbooks/H")
	if err != nil || len(cs) != 2 || cs[0].Idx != 0 || cs[0].Embedding != nil || st.textRead != 1 {
		t.Fatalf("embedding'siz okuma, idx sırası: %v %+v reads=%d", err, cs, st.textRead)
	}
	// Wiki izin listesi dışı (ad biliniyor) → kapsam dışı, okuma yok.
	svc.Configure(wiki.Config{Enabled: true, Wikis: []string{"Other.wiki"}})
	if _, err := svc.PageChunks(context.Background(), "Platform", "w1", "Platform.wiki", "/Runbooks/H"); !errors.Is(err, wiki.ErrOutOfScope) {
		t.Errorf("izin listesi dışı wiki: %v", err)
	}
	// Ad bilinmiyor + izin listesi var → saklı parça okunmaz; kayıt adıyla denetlenir.
	if _, err := svc.PageChunks(context.Background(), "Platform", "w1", "", "/Runbooks/H"); !errors.Is(err, wiki.ErrOutOfScope) || st.textRead != 1 {
		t.Errorf("adsız istekte kayıt adıyla kapsam: %v reads=%d", err, st.textRead)
	}
	// Proje izin listesi dışı.
	svc.Configure(wiki.Config{Enabled: true, Projects: []string{"Payments"}})
	if _, err := svc.PageChunks(context.Background(), "Platform", "w1", "Platform.wiki", "/Runbooks/H"); !errors.Is(err, wiki.ErrOutOfScope) {
		t.Errorf("izin listesi dışı proje: %v", err)
	}
}

func TestWikiPageChunksLiveModeNoCHWrites(t *testing.T) {
	content := mpPage("L", "Giriş", "Kurulum", "Adımlar")
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Canli": content}}
	svc, st := withWikiService(t, wiki.Config{Enabled: true, Mode: wiki.ModeLive}, api)
	cs, err := svc.PageChunks(context.Background(), "Platform", "w1", "Platform.wiki", "/Runbooks/Canli")
	if err != nil || len(cs) != 3 {
		t.Fatalf("canlı modda parçalar API'den: %v %d", err, len(cs))
	}
	gets := api.gets
	if cs2, _ := svc.PageChunks(context.Background(), "Platform", "w1", "Platform.wiki", "/Runbooks/Canli"); len(cs2) != 3 || api.gets != gets {
		t.Errorf("ikinci okuma bellek önbelleğinden: gets %d→%d", gets, api.gets)
	}
	if st.upserts != 0 {
		t.Errorf("canlı mod CH'ye yazmamalı: %d", st.upserts)
	}
	for i, c := range cs {
		if c.Idx != uint32(i) || c.Tokens != nil || c.Embedding != nil {
			t.Errorf("idx sırası, jeton/embedding yok: %+v", c)
		}
	}
	// Uçtan uca: canlı mod kademesinde isabetin komşu bölümleri de bağlamda.
	seen := withFakeNarrator(t, "ok")
	withFakeSelector(t, func(context.Context, string) (string, error) { return "", errors.New("kullanılmamalı") })
	_, emit := collectEmit()
	if h, ok := (&Server{}).wikiChatAnswer(sessionCtx(), emit, []copilot.ChatMessage{{Role: "user", Text: "L runbook kurulum"}}, wikiTierContext{}); !h || !ok {
		t.Fatal("canlı mod kademede cevaplamalı")
	}
	for _, m := range []string{"L-S0", "L-S1", "L-S2"} {
		if !strings.Contains((*seen)[0], m) {
			t.Errorf("canlı modda tek sayfanın tamamı bağlamda olmalı: %s yok", m)
		}
	}
	if st.upserts != 0 {
		t.Error("canlı mod CH'ye yazmamalı")
	}
}

// ── iki aşamalı seçim ───────────────────────────────────────────────────────

func TestWikiSelectStageUsed(t *testing.T) {
	svc, _, hits := mpFixture(t, wiki.Config{})
	sel := withFakeSelector(t, func(ctx context.Context, user string) (string, error) {
		if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > wikiSelectTimeout {
			t.Errorf("seçim çağrısı ≤%s sınırlı olmalı", wikiSelectTimeout)
		}
		return "```json\n{\"pages\": [3, 1, 3]}\n```", nil
	})
	seen := withFakeNarrator(t, "Son bölüm [1].")
	ev, emit := collectEmit()
	ans, used, err := (&Server{}).wikiNarratedAnswer(sessionCtx(), svc, "P3 son bölüm nedir", "", wikiTierSelect(hits), emit)
	if err != nil {
		t.Fatal(err)
	}
	if len(*sel) != 1 {
		t.Fatalf("tek seçim çağrısı: %d", len(*sel))
	}
	su := (*sel)[0]
	if strings.Count(su, "<wiki_data>") != 6 || strings.Count(su, "</wiki_data>") != 6 || !strings.Contains(su, "[6]\n<wiki_data>\nBaşlık: P7\nBölüm: ") || strings.Contains(su, "] Başlık") {
		t.Errorf("6 aday; başlık ve bölüm çitin İÇİNDE, dışarıda yalnız numara: %q", su)
	}
	if strings.Contains(su, "SON-BÖLÜM-P3") || strings.Contains(su, "P1-S4") {
		t.Error("seçim yalnız kısa kesit görmeli, sayfa metnini değil")
	}
	for _, block := range strings.Split(su, "<wiki_data>\n")[1:] {
		body := block[:strings.Index(block, "\n</wiki_data>")]
		if n := len([]rune(body[strings.LastIndex(body, "\n")+1:])); n > wikiSelectSnippetRunes+1 {
			t.Errorf("kesit ≤%d rune: %d", wikiSelectSnippetRunes, n)
		}
	}
	user := (*seen)[0]
	if !strings.Contains(user, "[1] Wiki · P3\n") || !strings.Contains(user, "[2] Wiki · P1\n") || strings.Contains(user, "Wiki · P2") {
		t.Errorf("yalnız seçilen sayfalar, modelin sırasıyla")
	}
	if !strings.Contains(user, "SON-BÖLÜM-P3") || !strings.Contains(user, "P3-S4") || !strings.Contains(user, "P1-S0") {
		t.Error("seçilen sayfaların TAM metni anlatıma ulaşmalı")
	}
	labels := wikiStepLabels(*ev)
	if len(labels) != 1 || labels[0] != "wiki_select · 6 aday → seçilen: P3, P1" {
		t.Errorf("görünür adım: %v", labels)
	}
	srcs, _ := ans["sources"].([]chatSource)
	if len(srcs) != 2 || srcs[0].Doc != "Wiki · P3" || srcs[1].Doc != "Wiki · P1" || len(wikiPageRefs(used)) != 2 {
		t.Errorf("çipler seçilen sayfalar, [n] sırası: %+v", srcs)
	}
}

func TestWikiSelectStageFallbacks(t *testing.T) {
	cases := map[string]func(context.Context, string) (string, error){
		"geçersiz":   func(context.Context, string) (string, error) { return "Bence ikinci sayfa.", nil },
		"boş":        func(context.Context, string) (string, error) { return `{"pages": []}`, nil },
		"liste dışı": func(context.Context, string) (string, error) { return `{"pages": [0, 9, -1]}`, nil },
		"zaman aşımı": func(context.Context, string) (string, error) {
			return "", context.DeadlineExceeded
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _, hits := mpFixture(t, wiki.Config{})
			sel := withFakeSelector(t, fn)
			seen := withFakeNarrator(t, "ok")
			ev, emit := collectEmit()
			if _, _, err := (&Server{}).wikiNarratedAnswer(sessionCtx(), svc, "svc-orders runbook", "", wikiTierSelect(hits), emit); err != nil {
				t.Fatal(err)
			}
			if len(*sel) != 1 {
				t.Fatalf("seçim denenmeli: %d", len(*sel))
			}
			for i := 1; i <= 5; i++ {
				if !strings.Contains((*seen)[0], fmt.Sprintf("[%d] Wiki · P%d\n", i, i)) {
					t.Errorf("skor tabanlı 5 sayfaya düşmeli: [%d] eksik", i)
				}
			}
			if l := wikiStepLabels(*ev); len(l) != 1 || !strings.Contains(l[0], "6 aday → seçim kullanılamadı") {
				t.Errorf("adım: %v", l)
			}
		})
	}
}

func TestWikiSelectSkippedSingleCandidateAndSettingOff(t *testing.T) {
	svc, _, hits := mpFixture(t, wiki.Config{})
	sel := withFakeSelector(t, func(context.Context, string) (string, error) { return `{"pages":[1]}`, nil })
	seen := withFakeNarrator(t, "ok")
	ev, emit := collectEmit()
	// Tabanı geçen tek aday sayfa → ek çağrı yok.
	if _, _, err := (&Server{}).wikiNarratedAnswer(sessionCtx(), svc, "q runbook", "", hits[:3], emit); err != nil {
		t.Fatal(err)
	}
	if len(*sel) != 0 || len(*ev) != 0 {
		t.Errorf("tek adayda seçim koşmamalı: %d çağrı, %v", len(*sel), *ev)
	}
	// Ayar kapalı → skor tabanlı, çağrı yok.
	svc.Configure(wiki.Config{Enabled: true, DisablePageSelect: true})
	if _, _, err := (&Server{}).wikiNarratedAnswer(sessionCtx(), svc, "q runbook", "", wikiTierSelect(hits), emit); err != nil {
		t.Fatal(err)
	}
	if len(*sel) != 0 || !strings.Contains((*seen)[1], "[5] Wiki · P5") {
		t.Errorf("ayar kapalıyken skor tabanlı 5 sayfa, seçim yok: %d", len(*sel))
	}
	// Kurtarma yolu (selectEmit nil) seçim yapmaz.
	svc.Configure(wiki.Config{Enabled: true})
	if _, _, err := (&Server{}).wikiNarratedAnswer(sessionCtx(), svc, "q runbook", "", wikiTierSelect(hits), nil); err != nil || len(*sel) != 0 {
		t.Errorf("selectEmit nil → seçim yok: %d", len(*sel))
	}
}

func TestParseWikiSelect(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{`{"pages":[2,1]}`, "[1 0]", true},
		{"```json\n{\"pages\": [3]}\n```", "[2]", true},
		{`Seçimim: {"pages": ["2", 2, 4]}`, "[1 3]", true},
		{`[1, 5]`, "[0 4]", true},
		{`{"pages":[1,2,3,4,5,1]}`, "[0 1 2 3 4]", true},
		{`{"pages":[1.5, 7, 0]}`, "[]", false},
		{`{"pages":[]}`, "[]", false},
		{`hiçbiri`, "[]", false},
		{``, "[]", false},
	}
	for _, c := range cases {
		got, ok := parseWikiSelect(c.raw, 5)
		if fmt.Sprint(got) != c.want && !(len(got) == 0 && c.want == "[]") || ok != c.ok {
			t.Errorf("parseWikiSelect(%q) = %v,%v want %s,%v", c.raw, got, ok, c.want, c.ok)
		}
	}
	if wikiSelectTimeoutFor(3*time.Second) != 3*time.Second || wikiSelectTimeoutFor(0) != wikiSelectTimeout || wikiSelectTimeoutFor(time.Minute) != wikiSelectTimeout {
		t.Error("seçim zaman aşımı ≤8 sn, istemci daha kısaysa o")
	}
}

// Kaynak pinleri: seçim açık kademe + takip (b)'de; kurtarma ve RAG tek geçişli.
func TestWikiSelectWiring(t *testing.T) {
	read := func(f string) string {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		return stripGoCommentsAPI(string(b))
	}
	if !strings.Contains(read("chat_wiki_tier.go"), "s.wikiNarratedAnswer(ctx, w, question, wikiPriorTurns(msgs), hits, emit)") {
		t.Error("açık wiki kademesi seçimi kullanmalı")
	}
	if !strings.Contains(read("chat_wiki_followup.go"), "s.wikiNarratedAnswer(ctx, w, question, prior, hits, emit)") {
		t.Error("takip (b) seçimi kullanmalı")
	}
	if !strings.Contains(read("chat_disambig_rescue.go"), "r.prior, hits, nil)") {
		t.Error("netleştirme kurtarması seçimsiz")
	}
	rag := read("rag.go")
	if strings.Contains(rag, "wikiPlanPages") || strings.Contains(rag, "wikiSelectPages") || !strings.Contains(rag, "s.ragWikiContextFor(ctx, wikiHits, num)") {
		t.Error("RAG wiki yarısı tek geçişli, ragWikiContextFor ile")
	}
	if !strings.Contains(rag, "copilot.SystemPromptRAGChatWiki()") {
		t.Error("RAG wiki isabetinde wiki eki prompt'u")
	}
}

// ── ayar ────────────────────────────────────────────────────────────────────

func TestWikiContextCharsSettingValidationAndAudit(t *testing.T) {
	for _, c := range []struct {
		v  int
		ok bool
	}{{0, true}, {4000, true}, {48000, true}, {16000, true}, {3999, false}, {48001, false}, {-1, false}} {
		if err := (wiki.Config{ContextChars: c.v}).Validate(); (err == nil) != c.ok {
			t.Errorf("Validate(%d) err=%v", c.v, err)
		}
	}
	if n := (wiki.Config{ContextChars: 100}).Normalize().ContextChars; n != wiki.MinContextChars {
		t.Errorf("Normalize alt kelepçe: %d", n)
	}
	if n := (wiki.Config{ContextChars: 1 << 20}).Normalize().ContextChars; n != wiki.MaxContextChars {
		t.Errorf("Normalize üst kelepçe: %d", n)
	}
	// PUT: aralık dışı → 400 (sessiz kelepçe yok), ayar değişmez.
	svc, _ := withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{})
	admin := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-admin", Role: auth.RoleAdmin})
	for _, v := range []int{1000, 60000} {
		body, _ := json.Marshal(wiki.Config{Enabled: true, ContextChars: v})
		rr := httptest.NewRecorder()
		(&Server{}).putWikiConfig(rr, httptest.NewRequest(http.MethodPut, "/api/wiki/config", bytes.NewReader(body)).WithContext(admin))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "4000") {
			t.Errorf("contextChars=%d → 400: %d %s", v, rr.Code, rr.Body.String())
		}
	}
	if svc.Config().ContextChars != 0 {
		t.Error("geçersiz PUT ayarı değiştirmemeli")
	}
	// Audit ayrıntısı yeni alanları taşır.
	var d map[string]any
	if err := json.Unmarshal([]byte(wikiAuditDetails(wiki.Config{Enabled: true, ContextChars: 12000, DisablePageSelect: true})), &d); err != nil {
		t.Fatal(err)
	}
	if d["contextChars"] != float64(12000) || d["pageSelect"] != false {
		t.Errorf("audit ayrıntısı: %v", d)
	}
	if !strings.Contains(stripGoCommentsAPI(mustRead(t, "wiki.go")), `s.audit(r, "settings.wiki.update", "settings", wiki.SettingsKey, wikiAuditDetails(ws.Config()))`) {
		t.Error("PUT audit'i wikiAuditDetails ile yazılmalı")
	}
	// Görünüm: aralık + otomatik değer.
	view := (&Server{}).wikiConfigView(context.Background())
	def, _ := view["defaults"].(map[string]int)
	if def["contextCharsMin"] != 4000 || def["contextCharsMax"] != 48000 || def["contextCharsAuto"] != wikiBudgetFor(0, 0, copilot.DefaultMaxTokens()) {
		t.Errorf("defaults: %v", def)
	}
}

// Takip (a): yeniden okunan sayfalar bütçeden pay alır ve "[n] Wiki · başlık" ile çitli.
func TestWikiFollowUpBudgetedBlocks(t *testing.T) {
	if l := wikiFollowUpLimits(1, 16000); len(l) != 1 || l[0] != 16000 {
		t.Errorf("tek sayfa bütçenin tamamı: %v", l)
	}
	if l := wikiFollowUpLimits(2, 16000); l[0] != 10400 || l[1] != 5600 {
		t.Errorf("iki sayfa 65/35: %v", l)
	}
	recs := []*wiki.PageRecord{
		{Title: "Namespace Oluşturma", URL: "https://devops.example.test/a", Content: strings.Repeat("q", 9000)},
		{Title: "Rollback", URL: "https://devops.example.test/b", Content: strings.Repeat("z", 9000)},
	}
	src := []chatSource{wikiRecSource(recs[0]), wikiRecSource(recs[1])}
	u := buildWikiFollowUpUser("", "linki nedir", recs, numberSources(src), 8000)
	if !strings.Contains(u, "[1] Wiki · Namespace Oluşturma (önceki cevabın kaynağı)\n<wiki_data>") || !strings.Contains(u, "[2] Wiki · Rollback") {
		t.Errorf("başlıklı numaralı çit: %q", u[:120])
	}
	if q, z := strings.Count(u, "q"), strings.Count(u, "z"); q < 4900 || q > 5200 || z < 2600 || z > 2800 || runeLen(u)-len("TAKİP SORUSU: linki nedir\n\nSAYFA:\n") > 8000 {
		t.Errorf("pay ~65/35 ve blokların tamamı ≤8000: q=%d z=%d toplam=%d", strings.Count(u, "q"), strings.Count(u, "z"), runeLen(u))
	}
}
