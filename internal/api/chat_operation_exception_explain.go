package api

// chat_operation_exception_explain.go — v0.10.1399 (operator-reported):
// "Mesela bir operasyonu soruyorum ama bana kafka timeout vs diyor ama
// exceptions açıp explain ettirdiğimde daha mantıklı güzel cevap veriyor.
// Chatten de sorduğumda böyle bakıp buna cevap verebilir."
//
// Kök sebep: "<OP> operasyonu neden hata veriyor?" sorusunda operasyon
// ön-getirmesi (chat_operation_prefetch.go) Exceptions grubunu modele TEK
// kompakt satır olarak veriyordu (renderExceptionGroupsTR), örnek trace'leri
// ise hatalı-önce + süreye göre sıralayıp en yavaş hatalının kırılımını
// "hatayı aynen aktar" kuralıyla ekliyordu (endpointDrillPhase →
// pickDrillTraces → composeDrillEvidenceTR). Tesadüfi bir yan hata (log
// yayınının PRODUCER span'inde Kafka TimeoutException) cevabı domine etti.
// Exceptions sayfasındaki ✨ açıklama ise AYNI grubu kendi girdi kurucusuyla
// (exceptionExplainInput → anomaly.Build{,Oracle}ExceptionExplainInput:
// çağıran span + SQL, örnek trace logları, patlama/sürekli akış) ve kendi
// sistem prompt'uyla anlatıyordu — farklı girdi, farklı cevap.
//
// Şimdi: soru hata-neden şekilliyse (opErrorWhyAsk) ve ön-getirme Exceptions
// grubu bulduysa, cevap sohbet akışının İÇİNDE (çekmece yok — operatör:
// ayrı açıklama çekmecesi CoSRE'den kopuk hissettiriyor) Exceptions
// açıklamasının BİREBİR çekirdeğiyle üretilir: aynı girdi kurucusu
// (drawerExceptionInput → exceptionExplainInput), aynı sistem prompt'u
// (exceptionExplainSystem), aynı önbellek anahtarı (explainCacheKey(system,
// in.User, "")) — düğme ve sohbet aynı cevabı paylaşır (emsal
// trace_explain_unified.go). Yüzey "explain-exception:chat" (aisurface
// kaydı; evalset Exception ailesi). Grup seçimi deterministik
// (pickExplainGroup: P1 → son saat → toplam → son görülme; oylar ASLA).
// Grup yok / okunamadı / girdi kurulamadı / soru hata-neden değil → akış
// bayt bayt eski (ön-getirme özetiyle serbest döngü).
//
// v0.10.1411 (operator-reported, aynı söz) — 1399'un bilinen sınırı: aday
// havuzu yalnız ön-getirmenin GÖSTERDİĞİ en yeni 5 gruptu (son görülmeye göre,
// okuma tavanı 20). Daha eski ama daha önemli bir P1 grubu 5'in dışında
// kalınca cevap küçük, yeni bir grubu anlatıyordu. Şimdi hata-neden sorusunda
// ve gösterilenden fazla grup varken (More > 0) aynı kapsamın AÇIK grupları
// state tablosundan sınırlı okunur (exExplainPoolFilter: open, oluşum azalan,
// LIMIT 100, FINAL + max_execution_time — ListExceptionGroups), gösterilen 5
// ile birleşir (explainCandidates) ve pickExplainGroup bu kümeye uygulanır.
// Gösterilen "en yeni 5" listesi (model + adım çipi) değişmez; cevap, künye,
// grup linki ve diğer-grup çipleri aynı sıralı kümeden. Havuz okunamazsa ya da
// gerekmezse (More == 0: gösterilen küme zaten tam küme) seçim bayt bayt eski.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// opExceptionExplainTool — sohbet içi açıklamanın adım çipi.
	opExceptionExplainTool = "exception_explain"
	// opExceptionExplainInputTimeout — girdi kurucusunun tavanı (kurucunun
	// kendi okumaları 5–6 sn tavanlı; bu dış sınır). LLM çağrısı alışverişin
	// tavanını (chatExchangeTimeout, ≥180 sn) kullanır.
	opExceptionExplainInputTimeout = 30 * time.Second
	// opExceptionExplainOthersMax — diğer grupların çip tavanı.
	opExceptionExplainOthersMax = 3
	// opExceptionExplainPoolLimit — v0.10.1411: açıklama aday havuzunun
	// (kapsamın açık grupları) CH okuma tavanı.
	opExceptionExplainPoolLimit = 100
)

// exExplainPoolFilter — SAF (v0.10.1411): aday havuzu okumasının süzgeci.
// Ön-getirmenin kapsamı (ex_message birebir / servis, aynı pencere örtüşmesi),
// yalnız AÇIK durumlar ("open" = new/acknowledged/regressed — Exceptions
// sayfasının kovası), oluşum azalan (öncelik Go'da hesaplanır; tavan dolarsa
// hacimli gruplar kalır, en yeniler zaten gösterilen 5'te).
func exExplainPoolFilter(sc exScope) chstore.ExceptionGroupFilter {
	f := chstore.ExceptionGroupFilter{Service: sc.Service, Messages: sc.Messages, State: "open",
		Sort: "occurrences", Dir: "desc", Limit: opExceptionExplainPoolLimit}
	if !sc.From.IsZero() {
		f.ActiveFromNs = sc.From.UnixNano()
	}
	if !sc.To.IsZero() {
		f.ActiveToNs = sc.To.UnixNano()
	}
	return f
}

// collectExplainPool — v0.10.1411: kapsamın açık grupları (örnek okuması
// YOK — seçim yalnız öncelik/son saat/toplam/son görülme ister). Hata →
// nil + hata (çağıran bugünkü 5'li havuza düşer).
func collectExplainPool(ctx context.Context, rd exGroupsReader, sc exScope, h exGroupHooks) ([]exGroupLine, error) {
	if len(sc.Messages) == 0 && strings.TrimSpace(sc.Service) == "" {
		return nil, errors.New("kapsam yok")
	}
	lctx, cancel := context.WithTimeout(ctx, exGroupListTimeout)
	rows, err := rd.ListExceptionGroups(lctx, exExplainPoolFilter(sc))
	cancel()
	if err != nil {
		return nil, err
	}
	out := make([]exGroupLine, 0, len(rows))
	for _, g := range rows {
		out = append(out, exGroupLineFrom(g, h))
	}
	return out, nil
}

// opExplainPoolWanted — SAF (v0.10.1411): havuz okunur mu? Yalnız hata-neden
// sorusunda, liste okunduysa ve gösterilenin dışında grup varsa (More > 0);
// aksi hâlde gösterilen küme zaten tam küme — ek okuma yok, seçim bayt bayt eski.
func opExplainPoolWanted(errorAsk bool, ev exceptionGroupsEvidence) bool {
	return errorAsk && ev.Err == "" && len(ev.Groups) > 0 && ev.More > 0
}

// exceptionExplainPool — sunucunun gerçek okuyucusuyla; okunamazsa nil.
func (s *Server) exceptionExplainPool(ctx context.Context, sc exScope) []exGroupLine {
	if s == nil || s.store == nil {
		return nil
	}
	pool, err := collectExplainPool(ctx, s.store, sc, serverExGroupHooks())
	if err != nil {
		return nil
	}
	return pool
}

// mergeExplainPool — SAF (v0.10.1411): gösterilen gruplar ∪ havuz, parmak
// izine göre tekil; gösterilen satır kazanır (örnek mesajı taşır). pool nil →
// gösterilen dilim AYNEN (bugünkü havuz).
func mergeExplainPool(shown, pool []exGroupLine) []exGroupLine {
	if pool == nil {
		return shown
	}
	out := make([]exGroupLine, 0, len(shown)+len(pool))
	seen := make(map[string]bool, len(shown)+len(pool))
	for _, src := range [][]exGroupLine{shown, pool} {
		for _, g := range src {
			if seen[g.Fingerprint] {
				continue
			}
			seen[g.Fingerprint] = true
			out = append(out, g)
		}
	}
	return out
}

// explainCandidates — v0.10.1411: açıklama seçiminin aday kümesi (gösterilen ∪
// havuz). Sohbet içi açıklama, takip çipi ve kırılım atlama kararı AYNI kümeden
// seçer — çip ile cevap aynı grubu söyler.
func (p *opPrefetch) explainCandidates() []exGroupLine {
	if p == nil || p.Exceptions == nil {
		return nil
	}
	return mergeExplainPool(p.Exceptions.Groups, p.explainPool)
}

// opErrorWhyAsk — SAF: soru operasyonun NEDEN hata verdiğini mi soruyor?
// "neden/niye hata", "hata veriyor/alıyor/veriyo", "why … error/fail(ing)".
// Gecikme ("yavaş", "p99", "gecikme"), sayı/oran ("kaç", "oranı"), log
// ("hata logları") ve topoloji soruları hayır — onlar bugünkü yollarında kalır.
func opErrorWhyAsk(msg string) bool {
	norm := normalizeGuidedMsg(msg)
	toks := guidedTokens(norm)
	if len(toks) == 0 || hasTopologySignal(toks) || hasLogSignal(toks) {
		return false
	}
	if hasSlowTraceSignal(norm) || tokenHasPrefix(toks, "yavaş", "yavas", "slow", "gecikme", "latency", "p99", "p95", "p50", "takıl", "takil") {
		return false
	}
	if tokenHasPrefix(toks, "oran", "rate", "kaç", "kac", "sayı", "sayi", "count", "trend") {
		return false
	}
	if !tokenHasPrefix(toks, "hata", "error", "fail", "başarısız", "basarisiz", "exception", "istisna") {
		return false
	}
	if hasWhySignal(toks) {
		return true
	}
	// "hata veriyor / alıyor / atıyor" — fiil şekli (gündelik "veriyo" dahil).
	return tokenHasPrefix(toks, "veriyo", "alıyo", "aliyo", "atıyo", "atiyo", "fırlatıyo", "patlıyo", "patliyo")
}

// exGroupLabel — SAF: grubun kısa adı (Oracle → hata kodu, span → tip).
func exGroupLabel(g exGroupLine) string {
	if g.Code != "" {
		return g.Code
	}
	return g.Type
}

// pickExplainGroup — SAF, deterministik: açıklanacak grup + kalanlar.
// ignored/resolved düşer; sıra exceptionPriorityRank (P1 önce), son 1 sa
// sayısı (azalan), toplam oluşum (azalan), son görülme (azalan), parmak izi
// (artan — eşitlikte determinizm). Oylar (👍/👎) hiçbir zaman girmez.
func pickExplainGroup(groups []exGroupLine) (top exGroupLine, others []exGroupLine, ok bool) {
	cand := make([]exGroupLine, 0, len(groups))
	for _, g := range groups {
		switch strings.ToLower(strings.TrimSpace(g.State)) {
		case "ignored", "resolved":
			continue
		}
		if strings.TrimSpace(g.Fingerprint) == "" {
			continue
		}
		cand = append(cand, g)
	}
	if len(cand) == 0 {
		return exGroupLine{}, nil, false
	}
	sort.SliceStable(cand, func(i, j int) bool {
		a, b := cand[i], cand[j]
		if ra, rb := exceptionPriorityRank(a.Priority), exceptionPriorityRank(b.Priority); ra != rb {
			return ra < rb
		}
		if a.LastHour != b.LastHour {
			return a.LastHour > b.LastHour
		}
		if a.Occurrences != b.Occurrences {
			return a.Occurrences > b.Occurrences
		}
		if a.LastSeen != b.LastSeen {
			return a.LastSeen > b.LastSeen
		}
		return a.Fingerprint < b.Fingerprint
	})
	return cand[0], cand[1:], true
}

// opExceptionExplainFooterTR — SAF: cevabın sunucu yazımı künyesi.
func opExceptionExplainFooterTR(g exGroupLine, op string) string {
	parts := []string{"Exceptions grubu " + clipRunes(exGroupLabel(g), exGroupOpRunes)}
	if op = strings.TrimSpace(op); op != "" {
		parts = append(parts, clipRunes(op, exGroupOpRunes))
	}
	parts = append(parts, "Exceptions açıklamasıyla aynı kanıt")
	return "\n\nKaynak: " + strings.Join(parts, " · ")
}

// opExceptionExplainFollowups — SAF: diğer grupların açıklama çipleri (≤3,
// metin tekil) + ön-getirmenin operasyon topolojisi çipi.
func opExceptionExplainFollowups(others []exGroupLine, opFacts []followupFact) []chatFollowup {
	var out []chatFollowup
	seen := map[string]bool{}
	for _, g := range others {
		if len(out) >= opExceptionExplainOthersMax {
			break
		}
		c, ok := renderFollowup(followupFact{Kind: fuKindExceptionGroup, Code: exGroupLabel(g), Fingerprint: g.Fingerprint})
		if !ok || seen[c.Text] {
			continue
		}
		seen[c.Text] = true
		out = append(out, c)
	}
	for _, f := range opFacts {
		if f.Kind != fuKindOpTopology {
			continue
		}
		if c, ok := renderFollowup(f); ok && !seen[c.Text] {
			seen[c.Text] = true
			out = append(out, c)
		}
	}
	return out
}

// chatExceptionExplainInput — grup + Exceptions açıklamasının girdisi + sistem
// prompt'u. Girdi çekmece takibinin TEK kurucusundan (drawerExceptionInput →
// exceptionExplainInput; testte exceptionInputSeam), prompt dağıtıcının
// seçiminden (exceptionExplainSystem) — ✨ düğmesiyle aynı çift.
func (s *Server) chatExceptionExplainInput(ctx context.Context, fp string, loc *time.Location) (*chstore.ExceptionGroup, anomaly.ExceptionExplainInput, string, bool) {
	if exceptionInputSeam == nil && (s == nil || s.store == nil) {
		return nil, anomaly.ExceptionExplainInput{}, "", false
	}
	g, in, ok := s.drawerExceptionInput(ctx, fp, loc)
	if !ok || g == nil || strings.TrimSpace(in.User) == "" {
		return nil, anomaly.ExceptionExplainInput{}, "", false
	}
	return g, in, exceptionExplainSystem(g.Fingerprint), true
}

// opExceptionExplainInChat — operasyonun en önemli Exceptions grubunu sohbet
// akışının içinde Exceptions açıklamasıyla anlatır: adım çipi → delta* →
// answer. handled=false → çağıran bugünkü akışa (serbest döngü) devam eder;
// adım çipi yayınlandıysa hata çipte görünür.
func (s *Server) opExceptionExplainInChat(ctx context.Context, emit func(string, any), p *opPrefetch, loc *time.Location) (handled, ok bool) {
	if s == nil || s.copilot == nil || p == nil || !p.errorAsk || p.Exceptions == nil || p.Exceptions.Err != "" {
		return false, false
	}
	top, others, found := pickExplainGroup(p.explainCandidates()) // v0.10.1411 — gösterilen ∪ açık havuz
	if !found {
		return false, false
	}
	label := exGroupLabel(top)
	argm := map[string]any{"operation": p.Operation, "group": label, "fingerprint": top.Fingerprint}
	if p.explainPool != nil { // v0.10.1411 — havuz okunmadıysa argümanlar bayt bayt eski
		argm["candidates"] = len(p.explainCandidates())
	}
	args, _ := json.Marshal(argm)
	n := emitGuidedStep(emit, opExceptionExplainTool, string(args))
	ictx, cancel := context.WithTimeout(ctx, opExceptionExplainInputTimeout)
	_, in, system, gok := s.chatExceptionExplainInput(ictx, top.Fingerprint, loc)
	cancel()
	if !gok {
		emitGuidedStepResult(emit, n, opExceptionExplainTool, "", errExceptionExplainInput)
		return false, false
	}
	emitGuidedStepResult(emit, n, opExceptionExplainTool, in.User, nil)

	// ✨ Explain root cause ile AYNI anahtar (copilot_exception.go, kod dalı kapalı).
	// v0.10.1400 — akış + önbellek gövdesi tek yerde (chat_explain_core.go
	// streamExplainCore; chatExplainTurn ve guidedTraceExplain de oradan).
	res, sok := s.streamExplainCore(ctx, emit, aisurface.ExplainExceptionChat, system, in.User, explainCacheKey(system, in.User, ""), false)
	if !sok {
		return true, false
	}
	xid := res.Xid
	text := trimExplainText(res) + opExceptionExplainFooterTR(top, p.Operation)
	// v0.10.1572 (chat_clarify_synth.go) — Oracle grubu + uygun hatalı trace →
	// cevabın sonuna düz metin sentez sorusu; cevap sonraki turda yorumlanır.
	// Bekleyen soru taşınamıyorsa (kullanıcısız / önbelleksiz) soru eklenmez.
	if chstore.IsOracleGroup(top.Fingerprint) && s.canRemember(chatContextFromCtx(ctx)) {
		if tid := pickSynthesisTrace(p.screen, p.Services, p.Traces); tid != "" {
			text += "\n\n" + traceAskTextTR(tid)
			noteTraceSynthAsk(ctx, tid, top.Fingerprint, p.Operation)
		}
	}
	// v0.10.1400 — grup linki ÇIPLAK grup sayfası: açıklama zaten bu turda;
	// `&ai=exception` artık sohbete yeni bir açıklama turu açar (çift tur olurdu).
	groupHref := top.Href
	if groupHref == "" {
		groupHref = top.ExplainHref
	}
	links := []guidedAnswerLink{{Label: "Exceptions grubu " + clipRunes(label, exGroupOpRunes), Href: groupHref}}
	if p.TracesHref != "" {
		links = append(links, guidedAnswerLink{Label: clipRunes(p.Operation, exGroupOpRunes) + " trace'leri", Href: p.TracesHref})
	}
	links = append(links, s.answerRequestIDLinks(ctx, text, top.Service)...)
	ans := withExplainCached(map[string]any{"text": text, "exchangeId": xid, "links": dedupLinksByHref(links)}, res)
	emit("answer", withAnswerFollowups(ans, opExceptionExplainFollowups(others, p.followupFacts())))
	return true, true
}

// errExceptionExplainInput — girdi kurulamadı (grup okunamadı / boş girdi).
var errExceptionExplainInput = errors.New("exception grubu okunamadı — açıklama kurulamadı")

// opDrillSkippedForErrorAsk — SAF: ön-getirmenin örnek trace kırılımı
// atlanır mı? Hata-neden sorusunda okunabilir Exceptions grubu varsa evet —
// cevap grubun açıklamasından gelir; kırılımın en yavaş hatalısı tesadüfi
// bir yan hatayı (log yayını PRODUCER timeout'u) öne çıkarıyordu.
func opDrillSkippedForErrorAsk(t opTarget, p *opPrefetch) bool {
	if !t.ErrorAsk || p == nil || p.Exceptions == nil || p.Exceptions.Err != "" {
		return false
	}
	_, _, ok := pickExplainGroup(p.explainCandidates()) // v0.10.1411
	return ok
}
