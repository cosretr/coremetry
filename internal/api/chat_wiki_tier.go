package api

// chat_wiki_tier.go — v0.10.1124: AÇIK wiki sorusu kademesi.
//
// Operatör: "CoSRE wiki içeriğini LLM ile yorumlayamıyor". Uçtan uca iz
// (copilot_chat.go kademe sırası guided > drawer > RAG > niyet > döngü):
//
//  1. guided: "svc-orders runbook'u nedir" servis adı taşır → hasGuidedSignal
//     → ask_service rotası telemetri anlatımıyla cevaplar; wiki'ye hiç gelmez.
//  2. RAG: wiki yarısı LiveOnStale + 0.5 tabanı. İndeks tazeyse yerel sonuç
//     zayıf olsa da canlı arama yok; taban altı → hiç.
//  3. niyet: prod varsayılanı on_no_loop — eşleşmeyen soru öneri çipleri /
//     genel cevapla BİTER, serbest döngü (search_wiki / read_wiki_page) HİÇ
//     koşmaz. Dış MCP yapılandırılmışsa araçlar zaten düşer.
//
// Sonuç: wiki içeriğini modele veren tek yol (RAG) çoğu soruda ya hiç
// ulaşılmıyor ya da tabanda eleniyordu. Bu kademe guided'dan ÖNCE koşar ama
// YALNIZ soru açıkça wiki'yi işaret ediyorsa (wikiQuestionCue): canlı yedek
// LiveOnWeak, sayfa metni modele verilir (baskın sayfada ~6000 karaktere dek)
// ve model özetler/yorumlar. Bulunamazsa açıkça "Wikide bulunamadı" — sessizce
// telemetri cevabına düşmez. Telemetri sorusu (işaretsiz) bayt bayt eski yolda.
//
// Güvenlik: araçsız TEK anlatım çağrısı (dış MCP aracı yok → exfil kapısı
// gereksiz); yalnız oturum kullanıcısı (API token'ı değil — RAG wiki yarısıyla
// aynı kapı).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	// wikiTierFloor — açık wiki sorusunda parçanın bağlama girme tabanı
	// (operatör wiki'yi açıkça sordu; RAG'ın 0.5'i telemetri sorusunu korur,
	// burada o risk yok — yine de tek tesadüfi eşleşmeyi eler).
	wikiTierFloor = 0.3
	// wikiTierMaxChunks — bağlama giren en çok parça (baskın sayfa yoksa).
	wikiTierMaxChunks = 4
	// wikiDominantRunes — baskın sayfanın bağlama giren metin tavanı.
	wikiDominantRunes = 6000
	// wikiDominance — en iyi sayfa skoru ikinciyi bu katla geçiyorsa baskın.
	wikiDominance = 1.35
)

// wikiStrongCues / wikiWeakCues — katlanmış (wiki.Fold) sözcük ÖNEKLERİ.
//
// GÜÇLÜ işaret yalnız AÇIK wiki/doküman sözcüğü: wiki bulunamazsa bile cevap
// "Wikide bulunamadı" (soru telemetriye kaçmaz). ZAYIF işaret ("how to",
// "nasıl yaparım", "doküman", "docs") telemetri sorularında da geçer ("how do
// I see errors for svc-orders"): yalnız en iyi isabet RAG tabanını
// (ragWikiFloor, 0.5) geçerse kademe cevaplar; değilse akış guided/RAG'a
// DOKUNULMADAN sürer (v0.10.1124 inceleme F2).
var (
	wikiStrongCues = []string{"wiki", "runbook", "playbook", "prosedur", "procedure", "kilavuz", "howto", "how-to", "dokumantasyon"}
	wikiWeakCues   = []string{"dokuman", "docs"}
	wikiCuePhrases = []string{"how to", "how do i", "how do we"}
	// wikiNasilVerbs — "nasıl" + yap- fiili ("nasıl yapılır/yaparız/yaparım…").
	wikiNasilVerbs = []string{"yapilir", "yapilacak", "yapariz", "yaparim", "yapabilirim", "yapabiliriz", "yapmali", "yapmaliyim"}
)

// wikiQuestionCue — SAF: soru açıkça wiki'yi işaret ediyor mu (güçlü / zayıf;
// güçlü varsa zayıf false).
func wikiQuestionCue(q string) (strong, weak bool) {
	f := wiki.Fold(q)
	words := strings.FieldsFunc(f, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
	})
	for i, w := range words {
		for _, c := range wikiStrongCues {
			if strings.HasPrefix(w, c) {
				strong = true
			}
		}
		if !strings.HasPrefix(w, "dokumantasyon") {
			for _, c := range wikiWeakCues {
				if strings.HasPrefix(w, c) {
					weak = true
				}
			}
		}
		if w == "nasil" && i+1 < len(words) {
			for _, v := range wikiNasilVerbs {
				if words[i+1] == v {
					weak = true
				}
			}
		}
	}
	norm := " " + strings.Join(words, " ") + " "
	for _, p := range wikiCuePhrases {
		if strings.Contains(norm, " "+p+" ") {
			weak = true
		}
	}
	if strong {
		weak = false
	}
	return strong, weak
}

// wikiTierContext — sohbet isteğinin panel/çekmece bağlamı (kademe kapısı).
type wikiTierContext struct {
	Explain, Subject, Trace, PageTraceID, Service string
}

// wikiTierAllowed — SAF (inceleme F1): panel, çekmece, exception ya da trace
// bağlamı taşıyan sohbet (açıklama, özne, trace, sayfadaki trace, servis
// bağlamı) wiki kademesine HİÇ girmez — o sorular bağlamın kanıtıyla
// cevaplanır; wiki yalnız bağlamsız CoSRE penceresinde öne alınır.
func wikiTierAllowed(c wikiTierContext) bool {
	for _, v := range []string{c.Explain, c.Subject, c.Trace, c.PageTraceID, c.Service} {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// wikiNarrateFn — anlatım çağrısı (test dikişi: sahte model bağlamı görür).
var wikiNarrateFn = func(s *Server, ctx context.Context, system, user string) (string, error) {
	return s.copilotStreamSurface(ctx, "wiki-chat", system, user, func(string) {})
}

// wikiTierSelect — SAF: tabanı geçen ilk N parça.
func wikiTierSelect(h []wiki.Hit) []wiki.Hit {
	out := make([]wiki.Hit, 0, wikiTierMaxChunks)
	for _, x := range h {
		if x.Score < wikiTierFloor {
			break
		}
		out = append(out, x)
		if len(out) >= wikiTierMaxChunks {
			break
		}
	}
	return out
}

// wikiDominantPage — SAF: en iyi sayfa baskın mı (tek sayfa ya da skoru
// ikinci sayfanın wikiDominance katı). Baskınsa bağlama o sayfanın daha
// uzun metni girer (okuma derinliği).
func wikiDominantPage(h []wiki.Hit) bool {
	if len(h) == 0 {
		return false
	}
	top := h[0].PageKey()
	for _, x := range h[1:] {
		if x.PageKey() != top {
			return h[0].Score >= wikiDominance*x.Score
		}
	}
	return true
}

// buildWikiContext — SAF: bağlam blokları. pageText doluysa (baskın sayfa)
// ilk blok o sayfanın ≤wikiDominantRunes metni, ardından BAŞKA sayfalardan
// en çok bir parça (karşılaştırma için); değilse parça başına ragWikiContext.
// Sayfa ADI verilmez (systemRAGChat ile aynı kural).
func buildWikiContext(h []wiki.Hit, pageText string, start int) string {
	var b strings.Builder
	n := start
	if pt := strings.TrimSpace(wikiDataCloseRe.ReplaceAllString(pageText, "")); pt != "" && len(h) > 0 {
		if r := []rune(pt); len(r) > wikiDominantRunes {
			pt = string(r[:wikiDominantRunes]) + "…"
		}
		fmt.Fprintf(&b, "[%d] wiki (sayfanın tamamı / ilk %d karakter)\n%s\n\n", n, wikiDominantRunes, fenceWikiData(pt))
		n++
		top := h[0].PageKey()
		for _, x := range h[1:] {
			if x.PageKey() != top {
				b.WriteString(ragWikiContext(n, x))
				break
			}
		}
		return b.String()
	}
	for _, x := range h {
		b.WriteString(ragWikiContext(n, x))
		n++
	}
	return b.String()
}

// wikiContextFor — isabetlerin bağlamı; baskın sayfa varsa tam metnini okur
// (yerel indeks ya da canlı önbellek; hata → parça bağlamı).
func (s *Server) wikiContextFor(ctx context.Context, w *wiki.Service, h []wiki.Hit, start int) string {
	page := ""
	if w != nil && wikiDominantPage(h) {
		if rec, err := w.ReadPage(ctx, h[0].Project, h[0].WikiID, h[0].Path); err == nil && rec != nil {
			page = rec.Content
		}
	}
	return buildWikiContext(h, page, start)
}

// wikiNotFoundText — SAF: wiki'de bulunamadı cevabı (neden + canlı not).
func wikiNotFoundText(res wiki.SearchResult, noTerms bool) string {
	switch {
	case res.SearchUnavailable && res.LiveOnly:
		return "Wikide bulunamadı: " + wiki.NoteSearchUnavailableLive + "."
	case noTerms:
		return "Wikide bulunamadı: soruda aranabilir bir terim yok — servis adı, hata kodu ya da konu sözcüğüyle sorun."
	}
	msg := "Wikide bulunamadı: bu soruyla eşleşen bir wiki sayfası yok."
	if strings.TrimSpace(res.LiveNote) != "" {
		// Ayrıntı (sınıf, http durumu) yalnız "Aramayı test et"te; sohbette genel not.
		msg += " (Azure DevOps araması şu an yanıt vermedi.)"
	}
	return msg
}

// wikiChatAnswer — açık wiki sorusu kademesi. handled=false → soru wiki'yi
// işaret etmiyor / wiki kapalı / çağıran token / (zayıf işarette) sonuç yok.
func (s *Server) wikiChatAnswer(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, tc wikiTierContext) (handled, ok bool) {
	if !wikiTierAllowed(tc) {
		return false, false
	}
	w := wikiKB()
	if w == nil || !w.Enabled() || !sourceCodeCallerAllowed(auth.FromContext(ctx)) {
		return false, false
	}
	question := strings.TrimSpace(lastUserText(msgs))
	if question == "" {
		return false, false
	}
	strong, weak := wikiQuestionCue(question)
	if !strong && !weak {
		return false, false
	}
	res, err := w.SearchWith(ctx, question, "", wiki.SearchOptions{Limit: 6, PerPage: 3, Live: wiki.LiveOnWeak})
	noTerms := errors.Is(err, wiki.ErrNoTerms)
	if err != nil && !noTerms && ctx.Err() != nil {
		return false, false
	}
	hits := wikiTierSelect(res.Hits)
	if !strong && (len(hits) == 0 || hits[0].Score < ragWikiFloor) {
		// Zayıf işaret + iyi isabet yok → kademe SESSİZ: hiçbir olay basılmaz,
		// akış guided/RAG'a bayt bayt eski hâliyle sürer.
		return false, false
	}
	emit("step", map[string]string{"label": "kurum wiki'si"})
	exID := copilot.MetaFromContext(ctx).ExchangeID
	if len(hits) == 0 {
		emit("answer", map[string]any{"text": wikiNotFoundText(res, noTerms), "exchangeId": exID,
			"sources": []any{}, "links": []guidedAnswerLink{}})
		return true, true
	}
	user := "SORU: " + question + "\n\nBAĞLAM:\n" + s.wikiContextFor(ctx, w, hits, 1)
	raw, err := wikiNarrateFn(s, ctx, copilot.SystemPromptWikiChat(), user)
	if err != nil {
		emit("error", map[string]string{"error": err.Error()})
		return true, false
	}
	type src struct {
		Doc   string  `json:"doc"`
		Ref   string  `json:"ref,omitempty"`
		Chunk uint32  `json:"chunk"`
		Score float64 `json:"score"`
	}
	sources := make([]src, 0, len(hits))
	for _, h := range hits {
		sources = append(sources, src{Doc: "Wiki · " + h.Title, Ref: h.URL, Chunk: h.Idx + 1, Score: h.Score})
	}
	emit("answer", map[string]any{
		"text":       strings.TrimSpace(raw),
		"exchangeId": exID,
		"sources":    sources,
		"links":      ragWikiLinks(hits),
	})
	return true, true
}
