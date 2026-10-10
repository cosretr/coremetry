package api

// chat_wiki_select.go — v0.10.1136: İKİ AŞAMALI wiki okuması (sayfa seçimi).
//
// Operatör: skor sırası soruyu en iyi cevaplayan sayfaları her zaman öne
// almıyor (başlığı soruyla örtüşen ama içi boş sayfa, ya da sorunun ikinci
// yarısını cevaplayan düşük skorlu sayfa). Çözüm: aramadan sonra ≤10 aday
// sayfanın YALNIZ başlığı, bölüm yolu ve ~200 karakterlik kesiti (çitli,
// veri) + soru + önceki tur modele verilir; model okunacak 1–5 sayfayı JSON
// ile seçer ({"pages":[…]}, niyet sınıflandırıcısının JSON deseni). Sonra
// YALNIZ seçilen sayfalar tam okunur (chat_wiki_multi.go, sayfa başı daha
// büyük pay) ve anlatılır.
//
// Sağlamlık: çağrı ≤wikiSelectTimeout (istemci zaman aşımı daha kısaysa o);
// hata / zaman aşımı / geçersiz ya da boş çıktı → skor tabanlı çok-kaynak
// seçimi (wikiScorePages). Liste dışı numaralar yok sayılır. Tabanı geçen
// tek aday sayfa varsa seçim KOŞMAZ (ek çağrı yok). Ayar
// (wiki_knowledge.disablePageSelect) kapalıysa skor tabanlı.
//
// Kapsam: açık wiki kademesi ve takip sorusunun bağlamlı araması (b).
// RAG kademesinin wiki yarısı ve netleştirme kurtarması tek geçişli kalır.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	// wikiSelectMaxCandidates — seçime sunulan en çok aday sayfa.
	wikiSelectMaxCandidates = 10
	// wikiSelectMaxPick — modelin seçebileceği en çok sayfa.
	wikiSelectMaxPick = 5
	// wikiSelectSnippetRunes — aday kesitinin tavanı.
	wikiSelectSnippetRunes = 200
	// wikiSelectTimeout — seçim çağrısının üst sınırı.
	wikiSelectTimeout = 8 * time.Second
	// wikiSelectStepPrefix — görünür adım çipi.
	wikiSelectStepPrefix = "wiki_select · "
)

// wikiSelectFn — seçim çağrısı (test dikişi: sahte model kullanıcı bloğunu görür).
var wikiSelectFn = func(s *Server, ctx context.Context, system, user string) (string, error) {
	if s == nil || s.copilot == nil {
		return "", errors.New("AI yapılandırılmamış")
	}
	return s.copilotExplainJSONSurface(ctx, "wiki-select", system, user, wikiSelectSchema())
}

func wikiSelectSchema() map[string]any {
	return objSchema(map[string]any{
		"pages": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
	})
}

// wikiSelectTimeoutFor — SAF: wikiSelectTimeout, istemci zaman aşımı daha kısaysa o.
func wikiSelectTimeoutFor(client time.Duration) time.Duration {
	if client > 0 && client < wikiSelectTimeout {
		return client
	}
	return wikiSelectTimeout
}

// buildWikiSelectUser — SAF: seçim çağrısının kullanıcı bloğu. Kesitler çitli
// (veri); başlık tek satır, çit etiketsiz.
func buildWikiSelectUser(question, prior string, pages []wikiPage) string {
	var b strings.Builder
	b.WriteString(prior)
	b.WriteString("SORU: " + question + "\n\nADAY SAYFALAR:\n")
	for i, p := range pages {
		h := p.top()
		head := cleanWikiTitle(h.Heading)
		if strings.TrimSpace(h.Heading) == "" {
			head = "—"
		}
		snip := clipRunes(strings.Join(strings.Fields(h.Text), " "), wikiSelectSnippetRunes)
		// Başlık ve bölüm yolu da sayfa verisi: çitin İÇİNDE (çit dışında yalnız numara).
		fmt.Fprintf(&b, "[%d]\n%s\n\n", i+1, fenceWikiData("Başlık: "+cleanWikiTitle(h.Title)+"\nBölüm: "+head+"\n"+snip))
	}
	return b.String()
}

// parseWikiSelect — SAF: modelin JSON'u → 0-tabanlı aday sıraları (modelin
// sırası, tekil, liste dışı yok sayılır, en çok wikiSelectMaxPick). Geçerli
// numara yoksa ok=false (çağıran skor sırasına düşer).
func parseWikiSelect(raw string, n int) ([]int, bool) {
	return parseWikiSelectN(raw, n, wikiSelectMaxPick)
}

// parseWikiSelectN — parseWikiSelect, seçim tavanı açık (v0.10.1150 Derin: 8).
func parseWikiSelectN(raw string, n, maxPick int) ([]int, bool) {
	t := strings.TrimSpace(raw)
	t = strings.TrimPrefix(strings.TrimPrefix(t, "```json"), "```")
	t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "```"))
	var nums []any
	if i, j := strings.Index(t, "{"), strings.LastIndex(t, "}"); i >= 0 && j > i {
		var obj struct {
			Pages []any `json:"pages"`
		}
		if json.Unmarshal([]byte(t[i:j+1]), &obj) != nil {
			return nil, false
		}
		nums = obj.Pages
	} else if json.Unmarshal([]byte(t), &nums) != nil {
		return nil, false
	}
	var out []int
	seen := map[int]bool{}
	for _, v := range nums {
		var k int
		switch x := v.(type) {
		case float64:
			if x != float64(int(x)) {
				continue
			}
			k = int(x)
		case string:
			if _, err := fmt.Sscanf(strings.TrimSpace(x), "%d", &k); err != nil {
				continue
			}
		default:
			continue
		}
		if k < 1 || k > n || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k-1)
		if len(out) >= maxPick {
			break
		}
	}
	return out, len(out) > 0
}

// wikiSelectStep — SAF: görünür adım etiketi.
func wikiSelectStep(n int, chosen []wikiPage) string {
	if len(chosen) == 0 {
		return fmt.Sprintf("%s%d aday → seçim kullanılamadı, skor sırası", wikiSelectStepPrefix, n)
	}
	titles := make([]string, 0, len(chosen))
	for _, p := range chosen {
		titles = append(titles, cleanWikiTitle(p.top().Title))
	}
	return fmt.Sprintf("%s%d aday → seçilen: %s", wikiSelectStepPrefix, n, strings.Join(titles, ", "))
}

// wikiSelectPages — iki aşamalı seçimin LLM adımı. ok=false → çağıran skor
// tabanlı seçime düşer. pages ≥2 olmalı (tek adayda çağrılmaz).
func (s *Server) wikiSelectPages(ctx context.Context, emit func(string, any), question, prior string, pages []wikiPage) ([]wikiPage, bool) {
	if len(pages) < 2 {
		return nil, false
	}
	dm := deepModeFrom(ctx) // v0.10.1150 — kapalıyken 10 aday / 5 seçim / eski prompt
	cands := pages
	if len(cands) > dm.wikiCandidates() {
		cands = cands[:dm.wikiCandidates()]
	}
	// Kök kimliği TAŞIMAZ (niyet sınıflandırıcısının emsali): aynı exchange
	// altında ikinci ai_calls satırı geri bildirim JOIN'lerini ikiye
	// katlamasın. v0.10.1153 — ama boş da değil: çocuk kimlik
	// ("kök:wiki-select") /ai'da turun altında gruplanır (eskiden turla
	// ilişkisiz görünüyordu).
	m := copilot.MetaFromContext(ctx)
	m.ExchangeID = aisurface.ChildExchangeID(m.ExchangeID, aisurface.WikiSelect)
	var client time.Duration
	if s != nil && s.copilot != nil {
		client = s.copilot.ClientTimeout()
	}
	cctx, cancel := context.WithTimeout(copilot.WithMeta(ctx, m), wikiSelectTimeoutFor(client))
	raw, err := wikiSelectFn(s, cctx, dm.wikiSelectPrompt(), buildWikiSelectUser(question, prior, cands))
	cancel()
	var idx []int
	ok := false
	if err == nil {
		idx, ok = parseWikiSelectN(raw, len(cands), dm.wikiMaxPages())
	}
	if !ok {
		emit("step", map[string]string{"label": wikiSelectStep(len(cands), nil)})
		return nil, false
	}
	chosen := make([]wikiPage, 0, len(idx))
	for _, i := range idx {
		chosen = append(chosen, cands[i])
	}
	emit("step", map[string]string{"label": wikiSelectStep(len(cands), chosen)})
	return chosen, true
}

// wikiPlanPages — çok-kaynak okumanın sayfa planı: iki aşamalı seçim (açıksa
// ve ≥2 aday varsa) ya da skor tabanlı seçim. selected=true → model seçti.
func (s *Server) wikiPlanPages(ctx context.Context, w *wiki.Service, hits []wiki.Hit, question, prior string, emit func(string, any)) (pages []wikiPage, selected bool) {
	dm := deepModeFrom(ctx) // v0.10.1150 — Derin: 15 aday, en çok 8 sayfa
	cands := wikiGroupPages(hits, wikiTierFloor, dm.wikiCandidates())
	if emit != nil && w != nil && !w.Config().DisablePageSelect && len(cands) > 1 {
		if chosen, ok := s.wikiSelectPages(ctx, emit, question, prior, cands); ok {
			return chosen, true
		}
	}
	return wikiScorePagesN(cands, dm.wikiMaxPages()), false
}
