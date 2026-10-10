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
// LiveOnWeak, sayfa metni modele verilir (v0.10.1136: 3–5 sayfanın genişletilmiş bölümleri, chat_wiki_multi.go)
// ve model özetler/yorumlar. Bulunamazsa açıkça "Wikide bulunamadı" — sessizce
// telemetri cevabına düşmez. Telemetri sorusu (işaretsiz) bayt bayt eski yolda.
//
// Güvenlik: araçsız TEK anlatım çağrısı (dış MCP aracı yok → exfil kapısı
// gereksiz); yalnız oturum kullanıcısı (API token'ı değil — RAG wiki yarısıyla
// aynı kapı).

import (
	"context"
	"errors"
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
	// wikiTierSearchLimit / wikiTierPerPage — kademenin araması: ≤10 aday
	// sayfaya yetecek isabet (iki aşamalı seçim; chat_wiki_select.go).
	wikiTierSearchLimit = 30
	wikiTierPerPage     = 3
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
	// wikiNasilVerbs — "nasıl" + yap- fiili ("nasıl yapılır/yaparız/yaparım…");
	// v0.10.1126'ten beri sonek kuralının (wikiNasilVerb) açık listesi.
	wikiNasilVerbs = []string{"yapilir", "yapilacak", "yapariz", "yaparim", "yapabilirim", "yapabiliriz", "yapmali", "yapmaliyim"}
)

// wikiQuestionCue — SAF: soru açıkça wiki'yi işaret ediyor mu (güçlü / zayıf;
// güçlü varsa zayıf false).
func wikiQuestionCue(q string) (strong, weak bool) {
	f := wiki.Fold(q)
	words := strings.FieldsFunc(f, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
	})
	for _, w := range words {
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
	}
	// v0.10.1126 — geniş nasıl-yapılır / bilgi işareti (chat_wiki_howto.go);
	// "how to/how do i/how do we" ve "nasıl" + yap- fiili onun alt kümesi.
	if wikiHowToCue(words) {
		weak = true
	}
	if strong {
		weak = false
	}
	return strong, weak
}

// wikiTierContext — sohbet isteğinin panel/çekmece bağlamı (kademe kapısı).
type wikiTierContext struct {
	Explain, Subject, Trace, PageTraceID, Service string
	// PrevWikiRefs — v0.10.1134: istemcinin yolladığı önceki asistan turunun
	// wiki kaynak href'leri (context.wikiRefs; takip sorusu, chat_wiki_followup.go).
	// Kapı alanı DEĞİL: wikiTierAllowed yalnız panel/çekmece alanlarına bakar.
	PrevWikiRefs []string
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

// wikiTierSelect — SAF: tabanı geçen isabetler, en çok wikiSelectMaxCandidates
// farklı sayfadan (v0.10.1136: aday kümesi; hangi sayfaların okunacağına
// wikiPlanPages karar verir — model seçimi ya da skor tabanlı ≤5 sayfa).
func wikiTierSelect(h []wiki.Hit) []wiki.Hit {
	return wikiTierSelectN(h, wikiSelectMaxCandidates)
}

// wikiTierSelectN — wikiTierSelect, aday tavanı açık (v0.10.1150 Derin: 15).
func wikiTierSelectN(h []wiki.Hit, maxPages int) []wiki.Hit {
	return wikiPagesHits(wikiGroupPages(h, wikiTierFloor, maxPages))
}

// wikiTierSearch — kademe araması + aday kümesi; Derin kipte (ctx) daha geniş
// havuz. Kapalıyken Limit/aday tavanı eski sabitler (v0.10.1150).
func wikiTierSearch(ctx context.Context, w *wiki.Service, question, project string, live wiki.LiveMode) (wiki.SearchResult, []wiki.Hit, error) {
	dm := deepModeFrom(ctx)
	res, err := w.SearchWith(ctx, question, project, wiki.SearchOptions{Limit: dm.wikiSearchLimit(), PerPage: wikiTierPerPage, Live: live})
	return res, wikiTierSelectN(res.Hits, dm.wikiCandidates()), err
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
	// v0.10.1134 — önceki tur wiki cevabıysa ve soru eliptik/geri atıflıysa
	// ("pipeline linki nedir") önce önceki sayfa yeniden okunur, sonra bağlamlı
	// arama (chat_wiki_followup.go). Cevaplamazsa aşağıdaki akış AYNEN.
	if h, fok := s.wikiFollowUpAnswer(ctx, emit, w, msgs, tc.PrevWikiRefs, question); h {
		return true, fok
	}
	strong, weak := wikiQuestionCue(question)
	if !strong && !weak {
		// v0.10.1143 — işaretsiz soru: İÇERİK yoklaması (chat_wiki_probe.go;
		// tek yerel lexical arama, kapı geçmezse hiçbir olay yok).
		return s.wikiProbeAnswer(ctx, emit, w, msgs, question)
	}
	if !strong && s.wikiWeakCueVetoed(ctx, question) {
		// v0.10.1126 inceleme — servis adı + telemetri sinyali: guided'ın sorusu.
		return false, false
	}
	res, hits, err := wikiTierSearch(ctx, w, question, "", wiki.LiveOnWeak) // v0.10.1150 — Derin: geniş havuz
	noTerms := errors.Is(err, wiki.ErrNoTerms)
	if err != nil && !noTerms && ctx.Err() != nil {
		return false, false
	}
	if !strong && (len(hits) == 0 || !hits[0].EvidencedAt(ragWikiFloor)) {
		// v0.10.1126 — canlı isabette sıra skoru yetmez, kök kapsamı da ≥ taban.
		// Zayıf işaret + iyi isabet yok → kademe SESSİZ: hiçbir olay basılmaz,
		// akış guided/RAG'a bayt bayt eski hâliyle sürer.
		return false, false
	}
	emit("step", map[string]string{"label": "kurum wiki'si"})
	exID := copilot.MetaFromContext(ctx).ExchangeID
	if len(hits) == 0 {
		emit("answer", map[string]any{"text": wikiNotFoundText(res, noTerms), "exchangeId": exID,
			"sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
		return true, true
	}
	ans, used, err := s.wikiNarratedAnswer(ctx, w, question, wikiPriorTurns(msgs), hits, emit)
	if err != nil {
		emit("error", map[string]string{"error": err.Error()})
		return true, false
	}
	// v0.10.1126 — soru guided'da "hangisini kastettin?"e de oturuyorsa
	// (router'ın kendi kararı, chat_disambig_rescue.go) adaylar kaybolmaz:
	// cevabın altına "Telemetri için:" + çipler.
	if text, chips, links, ok := s.guidedDisambigProbe(ctx, question); ok {
		withTelemetryChips(ans, text, chips, links)
	}
	if text, _ := ans["text"].(string); !wikiDeclined(text) {
		s.rememberWikiAnswer(ctx, text, wikiPageRefs(used))
	}
	emit("answer", ans)
	return true, true
}

// wikiNarratedAnswer — wiki isabetlerinden araçsız TEK anlatım çağrısı ve
// cevap yükü (açık wiki kademesi, takip (b) ve netleştirme kurtarması ortak;
// v0.10.1126). v0.10.1134: prior — son soru/cevap çifti (wikiPriorTurns;
// boşsa prompt bayt bayt eski) ki "bu", "o pipeline" gibi atıflar çözülsün.
//
// v0.10.1136 — çok-kaynaklı okuma: sayfa planı (selectEmit != nil ise iki
// aşamalı seçim, değilse/başarısızsa skor tabanlı ≤5 sayfa), sayfa başına
// genişletilmiş bölümler, modelin penceresinden türeyen bütçe. Dönen used:
// GERÇEKTEN okunan sayfaların isabetleri (çipler ve hafıza bunlardan).
func (s *Server) wikiNarratedAnswer(ctx context.Context, w *wiki.Service, question, prior string, hits []wiki.Hit, selectEmit func(string, any)) (map[string]any, []wiki.Hit, error) {
	pages, selected := s.wikiPlanPages(ctx, w, hits, question, prior, selectEmit)
	budget := s.wikiBudget(ctx, w, "wiki-chat")
	wctx, in := s.wikiReadContext(ctx, w, pages, budget, selected, numberSources(wikiHitSources(wikiPagesHits(pages))))
	// Bütçeye sığmayan (sondaki) sayfalar düştü: çipler yalnız bağlama girenler
	// (önek → numaralar değişmez).
	used := wikiPagesHits(in)
	sources := wikiHitSources(used)
	user := prior + "SORU: " + question + "\n\nBAĞLAM:\n" + wctx
	raw, err := wikiNarrateFn(s, ctx, deepModeFrom(ctx).answerPrefix()+copilot.SystemPromptWikiChat(), user) // v0.10.1150 — kapalıyken ""
	if err != nil {
		return nil, nil, err
	}
	ans := map[string]any{
		"text":       strings.TrimSpace(raw),
		"exchangeId": copilot.MetaFromContext(ctx).ExchangeID,
		// v0.10.1127: sayfa başına tek çip ("Kaynak 1", "Kaynak 2" …) —
		// aynı sayfanın parçaları özdeş "Kaynak §1" çipleri üretiyordu.
		// v0.10.1136: sıra = bağlamdaki [n] sırası = okunan sayfa sırası.
		"sources": dedupeChatSources(sources),
		"links":   ragWikiLinks(used),
	}
	// v0.10.1137 — cevaptaki URL'lerin doğrulama listesi: modele verilen
	// wiki bağlamı (wctx; önceki tur HARİÇ) + çipler + kaynaklar.
	withAllowedLinks(ans, wctx)
	return ans, used, nil
}
