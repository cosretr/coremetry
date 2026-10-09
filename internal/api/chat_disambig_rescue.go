package api

// chat_disambig_rescue.go — v0.10.1126: netleştirme kurtarması.
//
// Operatör (prod): "BSA cache refresh nasıl girilir" → guided "nasıl"ı sağlık
// sinyali, "BSA"yı servis parçası saydı ve cevap YALNIZ "Hangi servisi
// kastettin? Adaylar: …" oldu (copilot_guided.go router → guidedAskService →
// guidedAskServiceEvidence/askServiceAnswerTR → runGuidedRoute DirectAnswer).
// "Coremetry adresleri" de katalog adaylarına (find_entity / entity scan)
// düştü. Bunlar bilgi/nasıl-yapılır sorusu; cevap wiki'de.
//
// İki parça:
//
//  1. guided/niyet yolu bir cevabı YALNIZ varlık netleştirmesiyle bitirecekse
//     o noktalar markDisambiguation(ctx) der (dize eşleştirme yok: kararı
//     üreten kod işaretler). armDisambigRescue'nun sardığı emit, işaretli
//     "answer" olayını yakalar; wiki bu çağırana açıksa soruyu wiki'de arar
//     (LiveOnWeak, wiki.LiveBudget tavanı, geri çekilmeye saygılı). En iyi
//     isabet ≥ ragWikiFloor → wiki anlatımı (kademeyle AYNI yol) + altında
//     "Telemetri için:" ve özgün netleştirme metni/çipleri. İsabet yoksa
//     özgün olay DEĞİŞMEDEN geçer.
//  2. Açık wiki kademesi (chat_wiki_tier.go) bir soruyu wiki'den cevapladığında
//     guided router'ın AYNI soruya netleştirme rotası verip vermediğine bakar
//     (guidedDisambigProbe — router'ın kendisi, kopyası değil); veriyorsa
//     aynı "Telemetri için:" bloğu eklenir.
//
// Telemetri koruması (v0.10.1126 inceleme): kurtarma soru telemetri sinyali
// (sağlık/hata/yavaşlık/neden/kıyas/problem/mutlak pencere) taşıyorsa HİÇ
// kurulmaz; ask_service'in sağlık/kök-neden/pencere-kıyası soruları işaretlenmez
// (bunlar gerçekten servis seçimi bekleyen telemetri sorularıdır). İsabet
// yalnız sıra skoruyla değil KANITLA geçer (wiki.Hit.EvidencedAt: yerel skor
// ≥ taban ya da canlı isabetin kök kapsamı ≥ taban).
//
// Gecikme: kurtarma YALNIZ işaretli netleştirmede ve alışveriş başına EN ÇOK
// bir kez arar; soru wiki işareti taşıyorsa (kademe zaten aradı) hiç aramaz.
// Kapı kademeyle aynı: API token'ı yok, panel/çekmece bağlamı yok, wiki
// kapalıysa ctx ve emit DOKUNULMADAN döner (bayt bayt eski davranış).

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// disambigMark — guided'ın "bu cevap yalnız netleştirme" işareti (ctx'te).
type disambigMark struct{ on atomic.Bool }

type disambigMarkKey struct{}

// markDisambiguation — netleştirme cevabını üreten kod çağırır; kurtarma
// kurulmamışsa (wiki kapalı, token, bağlamlı sohbet) etkisiz.
func markDisambiguation(ctx context.Context) {
	if m, ok := ctx.Value(disambigMarkKey{}).(*disambigMark); ok {
		m.on.Store(true)
	}
}

// disambigRescue — sarılı emit'in durumu.
type disambigRescue struct {
	s        *Server
	ctx      context.Context
	w        *wiki.Service
	emit     func(string, any)
	question string
	prior    string // v0.10.1134 — önceki soru/cevap (wikiPriorTurns)
	mark     *disambigMark
	tried    atomic.Bool
}

// armDisambigRescue — kurtarmayı kurar. Kapı kapalıysa ctx ve emit AYNEN döner.
func (s *Server) armDisambigRescue(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, tc wikiTierContext) (context.Context, func(string, any)) {
	if !wikiTierAllowed(tc) {
		return ctx, emit
	}
	w := wikiKB()
	if w == nil || !w.Enabled() || !sourceCodeCallerAllowed(auth.FromContext(ctx)) {
		return ctx, emit
	}
	q := strings.TrimSpace(lastUserText(msgs))
	if q == "" {
		return ctx, emit
	}
	if guidedTelemetrySignal(q) {
		return ctx, emit // telemetri sorusu: netleştirme gerçekten servis seçimi
	}
	if strong, weak := wikiQuestionCue(q); strong || weak {
		// Kademe bu soruyu ZATEN aradı ve iyi isabet bulamadı (bulsaydı
		// cevaplardı): ikinci bir canlı arama gecikmeden başka şey eklemez.
		return ctx, emit
	}
	m := &disambigMark{}
	ctx = context.WithValue(ctx, disambigMarkKey{}, m)
	r := &disambigRescue{s: s, ctx: ctx, w: w, emit: emit, question: q, prior: wikiPriorTurns(msgs), mark: m}
	return ctx, r.emitEvent
}

func (r *disambigRescue) emitEvent(kind string, v any) {
	if kind == "answer" && r.mark.on.Swap(false) {
		if orig, ok := v.(map[string]any); ok && r.rescue(orig) {
			return
		}
	}
	r.emit(kind, v)
}

// rescue — wiki iyi isabet verirse wiki cevabını (+ özgün netleştirme) basar.
func (r *disambigRescue) rescue(orig map[string]any) bool {
	if r.tried.Swap(true) {
		return false // alışveriş başına en çok bir arama
	}
	sctx, cancel := context.WithTimeout(r.ctx, wiki.LiveBudget)
	res, err := r.w.SearchWith(sctx, r.question, "", wiki.SearchOptions{Limit: 6, PerPage: 3, Live: wiki.LiveOnWeak})
	cancel()
	if err != nil {
		return false
	}
	hits := wikiTierSelect(res.Hits)
	if len(hits) == 0 || !hits[0].EvidencedAt(ragWikiFloor) {
		return false
	}
	r.emit("step", map[string]string{"label": "kurum wiki'si"})
	ans, err := r.s.wikiNarratedAnswer(r.ctx, r.w, r.question, r.prior, hits)
	if err != nil {
		return false // anlatım başarısız → özgün netleştirme aynen
	}
	text, _ := orig["text"].(string)
	chips, _ := orig["suggestions"].([]string)
	links, _ := orig["links"].([]guidedAnswerLink)
	withTelemetryChips(ans, text, chips, links)
	if t, _ := ans["text"].(string); !wikiDeclined(t) {
		r.s.rememberWikiAnswer(r.ctx, t, wikiPageRefs(hits))
	}
	r.emit("answer", ans)
	return true
}

// telemetryChipsHeading — wiki cevabının altındaki netleştirme bloğu başlığı.
const telemetryChipsHeading = "**Telemetri için:** "

// withTelemetryChips — SAF: wiki cevabına özgün netleştirmeyi ekler (metin
// "Telemetri için:" altında, çipler suggestions, linkler wiki linklerinden sonra).
func withTelemetryChips(ans map[string]any, text string, chips []string, links []guidedAnswerLink) {
	if t := strings.TrimSpace(text); t != "" {
		cur, _ := ans["text"].(string)
		ans["text"] = cur + "\n\n" + telemetryChipsHeading + t
	}
	if len(chips) > 0 {
		ans["suggestions"] = chips
	}
	if len(links) > 0 {
		wl, _ := ans["links"].([]guidedAnswerLink)
		ans["links"] = dedupLinksByHref(append(append([]guidedAnswerLink(nil), wl...), links...))
	}
}

// guidedTelemetrySignal — SAF: soru telemetri sinyali taşıyor mu (sağlık,
// hata, yavaşlık/gecikme, neden, kıyas, problem, mutlak pencere).
func guidedTelemetrySignal(question string) bool {
	norm := normalizeGuidedMsg(question)
	toks := guidedTokens(norm)
	return hasHealthSignal(toks) || hasErrorSignal(toks) || hasSlowTraceSignal(norm) || hasWhySignal(toks) ||
		hasCompareSignal(toks) || hasProblemSignal(toks) || looksLikeAbsoluteWindow(question)
}

// disambigAskRescuable — SAF: ask_service netleştirmesi kurtarılabilir mi.
// Sağlık / kök-neden / pencere-kıyası soruları servis seçimini bekler.
func disambigAskRescuable(ask guidedIntent) bool {
	switch ask {
	case guidedRootCause, guidedServiceHealth, guidedWindowCompare:
		return false
	}
	return true
}

// wikiProbeNames — router'ın canlı ad listeleri (test dikişi). guidedServiceNames
// / guidedEnvNames / guidedTeamNames 60 sn önbellekli; ISKALAMADA depodan okur
// (sohbet başına en çok bir katalog okuması). Takım listesi yalnız teams=true.
var wikiProbeNames = func(s *Server, ctx context.Context, teams bool) (svcNames, envNames, teamNames []string) {
	if s == nil || s.cache == nil || s.store == nil {
		return nil, nil, nil
	}
	svcNames, envNames = s.guidedServiceNames(ctx), s.guidedEnvNames(ctx)
	if teams {
		teamNames = s.guidedTeamNames(ctx)
	}
	return svcNames, envNames, teamNames
}

// wikiWeakCueVetoed — v0.10.1126 inceleme: zayıf işaret, soru telemetri
// sinyali (hasGuidedSignal) ya da sahiplik kalıbı ("sahibi kim" → find_entity
// kartı) taşıyor VE servis adı çözülüyorsa hiç uygulanmaz. Ad listesi yalnız
// sinyal varken okunur.
func (s *Server) wikiWeakCueVetoed(ctx context.Context, question string) bool {
	norm := normalizeGuidedMsg(question)
	if !hasGuidedSignal(norm) && !wikiOwnershipCue(strings.Fields(wiki.Fold(question))) {
		return false
	}
	svcs, envs, _ := wikiProbeNames(s, ctx, false)
	return len(svcs) > 0 && extractServiceEntity(norm, svcs, envs) != ""
}

// guidedDisambigProbe — guided router'ın bu soruya netleştirme rotası verip
// vermediği (rota ÇALIŞTIRILMAZ: bundle/anlatım yok; yalnız önbellekli ad
// listeleri — ıskalamada depodan bir okuma). Kapı guided'ın sinyal kapısının
// bağlamsız yarısı; rota router'ın kendisi.
func (s *Server) guidedDisambigProbe(ctx context.Context, question string) (text string, chips []string, links []guidedAnswerLink, ok bool) {
	norm := normalizeGuidedMsg(question)
	if !hasGuidedSignal(norm) && !mayNameTeam(norm) && !hasFindSignal(guidedTokens(norm)) {
		return "", nil, nil, false
	}
	svcs, envs, teams := wikiProbeNames(s, ctx, true)
	if len(svcs) == 0 {
		return "", nil, nil, false
	}
	route := routeGuidedIntent(question, svcs, envs, teams, "")
	if len(route.ServiceOptions) < 2 {
		return "", nil, nil, false
	}
	switch route.Intent {
	case guidedAskService:
		return askServiceAnswerTR(route.ServiceOptions), guidedSuggestions(route), nil, true
	case guidedFindEntity:
		return renderFindEntityAsk(route.FindQuery, route.ServiceOptions), guidedSuggestions(route), nil, true
	}
	return "", nil, nil, false
}
