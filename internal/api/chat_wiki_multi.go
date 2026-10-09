package api

// chat_wiki_multi.go — v0.10.1136: CoSRE wiki cevaplarının ÇOK-KAYNAKLI okuması.
//
// Operatör: "wiki cevapları tek sayfaya/tek parçaya sıkışıyor; prosedür ve
// tablolar ortadan kesiliyor". Eski bağlam: baskın sayfada ilk ~6000 karakter
// (cevap sayfanın sonundaysa hiç görünmüyordu), değilse ≤4 çıplak parça ×
// 1500 karakter (komşu adımlar ve aynı bölümün devamı yok).
//
// Yeni bağlam:
//  1. SAYFA seçimi: tabanı geçen isabetler sayfa başına gruplanır (skor
//     sırası), göreli taban (en iyinin wikiRelFloor katı) altı düşer, en çok
//     wikiMaxPages sayfa. İki aşamalı okumada (chat_wiki_select.go) model
//     adayların başlık/kesitlerinden 1–5 sayfa seçer.
//  2. GENİŞLETME: her sayfada isabet parçası + komşuları (önceki/sonraki) +
//     aynı başlık bölümünün geri kalanı (alt başlıklar dahil), öncelik
//     sırasıyla bütçeye sığdığı kadar; belge sırasında, tekrarsız birleşir,
//     boşluklar "…" ile işaretlenir. Baskın sayfada ve model-seçimli
//     sayfalarda sayfanın geri kalanı da (isabete yakınlık sırasıyla) girer —
//     sayfa sığıyorsa TAMAMI.
//  3. BÜTÇE (wikiBudgetFor): pencere biliniyorsa (pencere − completion −
//     ek yük) × 3.0 karakter — elle ayarı da kapaklar (pencere kazanır);
//     bilinmiyorsa elle ayar ya da 8k varsayımıyla kapaklı 16000. Bütçe
//     ÇIKTININ TAMAMINI (işaret, başlık, "…" satırları dahil) kapsar; sığmayan
//     sayfa düşer. Sayfalara skorla orantılı, sayfa başı tavanlı dağıtılır (en
//     iyi sayfa en büyük pay); kullanılmayan pay sonraki sayfalara akar. RAG
//     kademesi bütçenin wikiRAGShare'ini kullanır (doküman parçaları da taşır).
//  4. BİÇİM: her kaynak "[n] Wiki · <temiz başlık>" + <wiki_data> çiti
//     (ilk satırı "Sayfa: <başlık>"); n = çipin "Kaynak n"i (sourceNumbers —
//     sayfa başına tek numara).
//
// Wiki kapalıyken bu dosyanın hiçbir yolu koşmaz (çağıranlar wikiKB/Enabled
// kapısının arkasında) — telemetri/çekmece/API-token kapıları değişmedi.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	// wikiMaxPages — skor sırasıyla okunan en çok sayfa.
	wikiMaxPages = 5
	// wikiRelFloor — sayfa skoru en iyi sayfanın bu katının altındaysa düşer
	// (mutlak taban wikiTierFloor'a ek; uzak kuyruk bütçeyi yemesin).
	wikiRelFloor = 0.5
	// wikiMinPageRunes — bir sayfaya ayrılan en küçük pay (isabet parçası
	// en azından kırpılmış hâliyle girsin).
	wikiMinPageRunes = 600

	// Bütçe (karakter = rune). Pencere jetonla, bağlam karakterle ölçülür.
	wikiBudgetDefault = 16000 // model penceresi bilinmiyorsa (varsayılan pencereyle kapaklı)
	wikiBudgetAutoMax = 24000 // otomatik bütçenin tavanı (büyük pencerede)
	// wikiBudgetFloor — pencere tavanı bundan küçük çıkarsa bile en az bu
	// kadar (completion + ek yükü pencereyi dolduran model zaten düzgün
	// cevaplayamaz; tek isabet parçası kırpılmış girsin).
	wikiBudgetFloor = 1500
	// wikiOverheadTokens — sistem prompt'u, soru, önceki tur, çit/başlık
	// satırları için pencereden ayrılan pay.
	wikiOverheadTokens = 1500
	// wikiCharsPerToken — TUTUCU karakter/jeton (Türkçe ekli metin, kod
	// blokları; 3.5 yerine 3.0 — tahmin taşarsa pencere aşılır).
	wikiCharsPerToken = 3.0
	// wikiAssumedWindow — model penceresi bilinmiyorken OTOMATİK bütçenin
	// varsaydığı pencere (8k yerel model + 4096 completion güvende kalsın).
	// Elle ayar bilinmeyen modelde bununla kapaklanmaz (operatör modelini bilir).
	wikiAssumedWindow = 8192
	// wikiRAGShare — RAG kademesinin wiki yarısı bütçenin bu kadarını kullanır.
	wikiRAGShare = 0.5
)

// ── bütçe ───────────────────────────────────────────────────────────────────

// wikiWindowCap — SAF: pencereye sığan wiki bağlamı (karakter) —
// (pencere − completion − ek yük) × wikiCharsPerToken, en az wikiBudgetFloor.
func wikiWindowCap(windowTokens, completionTokens int) int {
	if completionTokens <= 0 {
		completionTokens = copilot.DefaultMaxTokens()
	}
	c := int(float64(windowTokens-completionTokens-wikiOverheadTokens) * wikiCharsPerToken)
	if c < wikiBudgetFloor {
		return wikiBudgetFloor
	}
	return c
}

// wikiBudgetFor — SAF: wiki bağlam bütçesi (karakter).
//   - pencere biliniyor: tavan = wikiWindowCap; elle ayar varsa
//     min(elle, tavan) — PENCERE HER ZAMAN KAZANIR; yoksa min(tavan, wikiBudgetAutoMax).
//   - pencere bilinmiyor: elle ayar varsa o (aralıkta kelepçeli); yoksa
//     min(wikiBudgetDefault, wikiWindowCap(wikiAssumedWindow, completion)).
func wikiBudgetFor(manual, windowTokens, completionTokens int) int {
	if manual > 0 {
		manual = clampInt(manual, wiki.MinContextChars, wiki.MaxContextChars)
	}
	if windowTokens > 0 {
		capChars := wikiWindowCap(windowTokens, completionTokens)
		if manual > 0 {
			return min(manual, capChars)
		}
		return min(capChars, wikiBudgetAutoMax)
	}
	if manual > 0 {
		return manual
	}
	return min(wikiBudgetDefault, wikiWindowCap(wikiAssumedWindow, completionTokens))
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// wikiBudget — çağrının wiki bağlam bütçesi: yüzeyin (wiki-chat / rag-chat)
// ÇÖZÜLEN profilindeki modelin penceresi ve completion bütçesi + Ayarlar'daki
// elle değer (wikiBudgetFor; pencere biliniyorsa elle değeri de kapaklar).
func (s *Server) wikiBudget(ctx context.Context, w *wiki.Service, surface string) int {
	manual := 0
	if w != nil {
		manual = w.Config().ContextChars
	}
	window, completion := wikiModelLimitsFn(s, ctx, surface)
	return wikiBudgetFor(manual, window, completion)
}

// wikiModelLimitsFn — yüzeyin modelinin penceresi (bilinmiyorsa 0) ve
// completion bütçesi (test dikişi: sahte 8k model).
var wikiModelLimitsFn = func(s *Server, ctx context.Context, surface string) (window, completion int) {
	completion = copilot.DefaultMaxTokens()
	if s == nil || s.copilot == nil {
		return 0, completion
	}
	m := copilot.MetaFromContext(ctx)
	m.Surface = surface
	sctx := copilot.WithMeta(ctx, m)
	return modelcaps.ContextWindow(s.copilot.ModelFor(sctx)), s.copilot.MaxTokensFor(sctx)
}

// ── sayfa seçimi ────────────────────────────────────────────────────────────

// wikiPage — bir sayfanın isabetleri (skor sırası; ilk isabet sayfanın skoru).
type wikiPage struct {
	Hits []wiki.Hit
}

func (p wikiPage) top() wiki.Hit  { return p.Hits[0] }
func (p wikiPage) score() float64 { return p.Hits[0].Score }
func (p wikiPage) anchors() []uint32 {
	out := make([]uint32, 0, len(p.Hits))
	for _, h := range p.Hits {
		out = append(out, h.Idx)
	}
	return out
}

// wikiGroupPages — SAF: skor sıralı isabetler → sayfalar (ilk görülme sırası,
// sayfa düzeyinde tekil). floor altı isabet atılır; en çok max sayfa.
func wikiGroupPages(h []wiki.Hit, floor float64, maxPages int) []wikiPage {
	var out []wikiPage
	at := map[string]int{}
	for _, x := range h {
		if x.Score < floor {
			continue
		}
		if i, ok := at[x.PageKey()]; ok {
			dup := false
			for _, y := range out[i].Hits {
				if y.Idx == x.Idx {
					dup = true
				}
			}
			if !dup {
				out[i].Hits = append(out[i].Hits, x)
			}
			continue
		}
		if maxPages > 0 && len(out) >= maxPages {
			continue
		}
		at[x.PageKey()] = len(out)
		out = append(out, wikiPage{Hits: []wiki.Hit{x}})
	}
	return out
}

// wikiScorePages — SAF: skor tabanlı çok-kaynak seçimi — göreli taban altı
// sayfa düşer, en çok wikiMaxPages.
func wikiScorePages(p []wikiPage) []wikiPage {
	if len(p) == 0 {
		return nil
	}
	top := p[0].score()
	out := make([]wikiPage, 0, wikiMaxPages)
	for _, x := range p {
		if len(out) >= wikiMaxPages {
			break
		}
		if len(out) > 0 && x.score() < wikiRelFloor*top {
			continue
		}
		out = append(out, x)
	}
	return out
}

// wikiPagesHits — SAF: sayfaların isabetleri, sayfa sırasıyla düz liste
// (kaynak/çip sırası = sayfa sırası).
func wikiPagesHits(p []wikiPage) []wiki.Hit {
	var out []wiki.Hit
	for _, x := range p {
		out = append(out, x.Hits...)
	}
	return out
}

// ── bütçe dağıtımı ──────────────────────────────────────────────────────────

// wikiPageCapShare — SAF: sayfa başına tavan (toplam bütçenin payı). Tek
// sayfa: tamamı. Model-seçimli okuma daha az sayfa okur → sayfa başı daha
// büyük pay. Baskın sayfa: %70. Skor tabanlı çok sayfa: %45.
func wikiPageCapShare(n int, dominant, selected bool) float64 {
	switch {
	case n <= 1:
		return 1
	case selected:
		return math.Min(0.8, 1.6/float64(n))
	case dominant:
		return 0.7
	}
	return 0.45
}

// wikiPageAllot — SAF: i. sayfanın payı = kalan × skor_i / Σ(kalan sayfaların
// skoru), sayfa tavanıyla sınırlı, en az wikiMinPageRunes. Baskın sayfanın
// ilk payı doğrudan tavandır.
func wikiPageAllot(remaining int, scores []float64, i int, capShare float64, total int, dominant bool) int {
	capRunes := int(capShare * float64(total))
	var a int
	if dominant && i == 0 {
		a = capRunes
	} else {
		sum := 0.0
		for _, s := range scores[i:] {
			sum += math.Max(s, 0.01)
		}
		a = int(float64(remaining) * math.Max(scores[i], 0.01) / sum)
	}
	if a > capRunes {
		a = capRunes
	}
	if a < wikiMinPageRunes { // taban — yine de kalanı (toplamı) aşmaz
		a = wikiMinPageRunes
	}
	if a > remaining {
		a = remaining
	}
	if a < 0 {
		a = 0
	}
	return a
}

// wikiPlanBudgets — SAF: her sayfanın payı (her sayfa payının TAMAMINI
// kullanırsa). Test ve belgeleme için; asıl kurucu kullanılmayan payı
// sonraki sayfalara akıtır.
func wikiPlanBudgets(total int, scores []float64, dominant, selected bool) []int {
	capShare := wikiPageCapShare(len(scores), dominant, selected)
	out := make([]int, len(scores))
	rem := total
	for i := range scores {
		out[i] = wikiPageAllot(rem, scores, i, capShare, total, dominant)
		rem -= out[i]
	}
	return out
}

// ── genişletme ──────────────────────────────────────────────────────────────

// inWikiSection — SAF: parça bölümün (başlık yolu H) içinde mi (alt başlıklar dahil).
func inWikiSection(heading, h string) bool {
	return heading == h || (h != "" && strings.HasPrefix(heading, h+wiki.HeadingSep))
}

// wikiExpandPage — SAF: sayfanın idx-sıralı parçalarından isabetlerin
// çevresi. Öncelik: isabet parçaları (skor sırası) → komşuları (önceki/
// sonraki) → aynı başlık bölümünün geri kalanı (isabete yakınlık sırası) →
// whole ise sayfanın geri kalanı. Bütçeye sığan alınır (ilk isabet kırpılarak
// da olsa girer); çıktı belge sırasında, tekrarsız.
func wikiExpandPage(chunks []wiki.ChunkRecord, anchors []uint32, budget int, whole bool) []wiki.ChunkRecord {
	pos := make(map[uint32]int, len(chunks))
	for i, c := range chunks {
		pos[c.Idx] = i
	}
	var order []int
	seen := map[int]bool{}
	push := func(i int) {
		if i >= 0 && i < len(chunks) && !seen[i] {
			seen[i] = true
			order = append(order, i)
		}
	}
	var aPos []int
	for _, a := range anchors {
		if p, ok := pos[a]; ok {
			aPos = append(aPos, p)
			push(p)
		}
	}
	if len(aPos) == 0 {
		return nil
	}
	for _, p := range aPos {
		push(p - 1)
		push(p + 1)
	}
	for _, p := range aPos {
		h := chunks[p].Heading
		for d := 1; ; d++ {
			l, r := p-d, p+d
			lin := l >= 0 && inWikiSection(chunks[l].Heading, h)
			rin := r < len(chunks) && inWikiSection(chunks[r].Heading, h)
			if !lin && !rin {
				break
			}
			if lin {
				push(l)
			}
			if rin {
				push(r)
			}
		}
	}
	if whole {
		rest := make([]int, 0, len(chunks))
		for i := range chunks {
			if !seen[i] {
				rest = append(rest, i)
			}
		}
		dist := func(i int) int {
			best := len(chunks)
			for _, p := range aPos {
				if d := absInt(i - p); d < best {
					best = d
				}
			}
			return best
		}
		sort.SliceStable(rest, func(a, b int) bool { return dist(rest[a]) < dist(rest[b]) })
		order = append(order, rest...)
	}
	used := 0
	take := map[int]string{}
	for _, i := range order {
		t := strings.TrimSpace(chunks[i].Text)
		if t == "" {
			continue
		}
		n := len([]rune(t))
		if used+n > budget {
			if len(take) == 0 { // ilk (en iyi) isabet her durumda (kırpılmış) girer
				t = clipRunes(t, budget)
				take[i] = t
				used += len([]rune(t))
			}
			continue
		}
		take[i] = t
		used += n
	}
	idx := make([]int, 0, len(take))
	for i := range take {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]wiki.ChunkRecord, 0, len(idx))
	for _, i := range idx {
		out = append(out, wiki.ChunkRecord{Idx: chunks[i].Idx, Heading: chunks[i].Heading, Text: take[i]})
	}
	return out
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// hitsAsChunks — SAF: sayfa parçaları okunamadığında isabetlerin kendisi
// (idx sırası, tekil) — eski parça bağlamının karşılığı.
func hitsAsChunks(h []wiki.Hit) []wiki.ChunkRecord {
	seen := map[uint32]bool{}
	out := make([]wiki.ChunkRecord, 0, len(h))
	for _, x := range h {
		if seen[x.Idx] {
			continue
		}
		seen[x.Idx] = true
		out = append(out, wiki.ChunkRecord{Idx: x.Idx, Heading: x.Heading, Text: x.Text})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Idx < out[j].Idx })
	return out
}

// renderWikiChunks — SAF: seçilen parçalar belge sırasında; başlık yolu
// değişince "## başlık", atlanan aralık "…".
func renderWikiChunks(cs []wiki.ChunkRecord) string {
	var b strings.Builder
	prevIdx, prevHead := -1, "\x00"
	for i, c := range cs {
		if i > 0 {
			if int(c.Idx) != prevIdx+1 {
				b.WriteString("\n\n…\n\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		if c.Heading != prevHead && c.Heading != "" {
			b.WriteString("## " + c.Heading + "\n")
		}
		b.WriteString(strings.TrimSpace(c.Text))
		prevIdx, prevHead = int(c.Idx), c.Heading
	}
	return b.String()
}

// cleanWikiTitle — SAF: başlık tek satır, çit etiketsiz, ≤120 rune (çit İÇİ).
func cleanWikiTitle(t string) string {
	t = strings.Join(strings.Fields(wikiDataCloseRe.ReplaceAllString(t, "")), " ")
	if t == "" {
		return "wiki"
	}
	return clipRunes(t, 120)
}

// wikiMarkerTitle — SAF: çit DIŞINDAKİ görünür "[n] Wiki · …" işaretinin
// başlığı: [ ] # * ` < > ve satır sonları atılır (sayfa başlığı veri; çit
// dışında biçim/talimat taşıyamasın), ≤80 rune.
func wikiMarkerTitle(t string) string {
	t = strings.Map(func(r rune) rune {
		switch r {
		case '[', ']', '#', '*', '`', '<', '>':
			return -1
		case '\n', '\r', '\t':
			return ' '
		}
		return r
	}, t)
	t = strings.Join(strings.Fields(t), " ")
	if t == "" {
		return "wiki"
	}
	return clipRunes(t, 80)
}

// wikiSourceBlock — SAF: tek kaynağın bağlam bloğu. Görünür işaret çit
// dışında ("[n] Wiki · <temiz başlık>"); sayfa başlığı çitin İLK satırı
// ("Sayfa: …"), bölüm başlıkları gövdede ("## …") — hepsi veri.
func wikiSourceBlock(n int, title, body string) string {
	inner := "Sayfa: " + cleanWikiTitle(title)
	if b := strings.TrimSpace(body); b != "" {
		inner += "\n" + b
	}
	return fmt.Sprintf("[%d] Wiki · %s\n%s\n\n", n, wikiMarkerTitle(title), fenceWikiData(inner))
}

// wikiMinUsefulRunes — bir sonraki sayfaya en az bu kadar metin payı
// kalmıyorsa o ve sonraki sayfalar bağlama girmez.
const wikiMinUsefulRunes = 200

func runeLen(s string) int { return len([]rune(s)) }

// buildWikiMultiContext — SAF: sayfaların bağlam blokları ve bağlama GİREN
// sayfa sayısı (önek; bütçe biterse sonraki sayfalar düşer). chunks[i] i.
// sayfanın idx-sıralı parçaları (nil/isabetsiz → isabetlerin kendisi).
// selected: model-seçimli okuma (sayfa başı büyük pay, sayfanın tamamı).
//
// Bütçe ÇIKTININ TAMAMINI kapsar: işaret/başlık/"## …"/"…" satırları ve
// sayfa tabanı dahil; blok sığmazsa metin payı küçültülüp yeniden kurulur
// (parça sınırından kesilir), yine sığmazsa sayfa (ilki hariç) düşer.
func buildWikiMultiContext(pages []wikiPage, chunks [][]wiki.ChunkRecord, budget int, selected bool, num sourceNumbers) (string, int) {
	if len(pages) == 0 {
		return "", 0
	}
	dominant := !selected && len(pages) > 1 && wikiDominantPage(wikiPagesHits(pages))
	capShare := wikiPageCapShare(len(pages), dominant, selected)
	scores := make([]float64, len(pages))
	for i, p := range pages {
		scores[i] = p.score()
	}
	var b strings.Builder
	remaining, included := budget, 0
	for i, p := range pages {
		n := num.of(wikiHitSource(p.top()))
		overhead := runeLen(wikiSourceBlock(n, p.top().Title, ""))
		if i > 0 && remaining-overhead < wikiMinUsefulRunes {
			break
		}
		var cs []wiki.ChunkRecord
		if i < len(chunks) {
			cs = chunks[i]
		}
		whole := selected || len(pages) == 1 || (dominant && i == 0)
		text := wikiPageAllot(remaining-overhead, scores, i, capShare, budget, dominant)
		var block string
		for try := 0; try < 8; try++ {
			chosen := wikiExpandPage(cs, p.anchors(), text, whole)
			if len(chosen) == 0 { // parçalar okunamadı / idx uyuşmadı → isabetler
				chosen = wikiExpandPage(hitsAsChunks(p.Hits), p.anchors(), text, true)
			}
			block = wikiSourceBlock(n, p.top().Title, renderWikiChunks(chosen))
			over := runeLen(block) - remaining
			if over <= 0 || text <= 0 {
				break
			}
			text -= over + 16 // "## başlık" / "…" satırlarının payı
			if text < 0 {
				text = 0
			}
		}
		if runeLen(block) > remaining && i > 0 {
			break
		}
		b.WriteString(block)
		remaining -= runeLen(block)
		included++
	}
	return b.String(), included
}

// wikiPageChunksFor — sayfaların parçaları (eşzamanlı; hata → nil, kurucu
// isabetlere düşer). Karma/senkron: tek-sayfa sınırlı, embedding'siz CH
// okuması; canlı: bellek önbelleği / API (wiki.Service.PageChunks).
func wikiPageChunksFor(ctx context.Context, w *wiki.Service, pages []wikiPage) [][]wiki.ChunkRecord {
	out := make([][]wiki.ChunkRecord, len(pages))
	if w == nil {
		return out
	}
	var wg sync.WaitGroup
	for i, p := range pages {
		wg.Add(1)
		go func(i int, h wiki.Hit) {
			defer wg.Done()
			if c, err := w.PageChunks(ctx, h.Project, h.WikiID, h.WikiName, h.Path); err == nil {
				out[i] = c
			}
		}(i, p.top())
	}
	wg.Wait()
	return out
}

// wikiReadContext — sayfaların genişletilmiş bağlamı (bütçeli) ve bağlama
// GİREN sayfalar (önek — numaralar ve çipler bunlardan).
func (s *Server) wikiReadContext(ctx context.Context, w *wiki.Service, pages []wikiPage, budget int, selected bool, num sourceNumbers) (string, []wikiPage) {
	text, n := buildWikiMultiContext(pages, wikiPageChunksFor(ctx, w, pages), budget, selected, num)
	return text, pages[:n]
}

// ragWikiContextFor — RAG kademesinin wiki yarısı: skor tabanlı, en çok
// ragWikiMaxPages sayfa, bütçenin wikiRAGShare'i (doküman parçaları da var).
// Dönen used: bağlama GİREN sayfaların isabetleri.
func (s *Server) ragWikiContextFor(ctx context.Context, hits []wiki.Hit, num sourceNumbers) (string, []wiki.Hit) {
	w := wikiKB()
	pages := wikiGroupPages(hits, 0, ragWikiMaxPages)
	budget := int(float64(s.wikiBudget(ctx, w, "rag-chat")) * wikiRAGShare)
	text, in := s.wikiReadContext(ctx, w, pages, budget, false, num)
	return text, wikiPagesHits(in)
}
