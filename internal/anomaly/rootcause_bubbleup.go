package anomaly

// rootcause_bubbleup.go — v0.10.1577 — dış skill denetimi 2026-09-19 V1,
// dilim 2 (operatör onayı 2026-10-29). BubbleUp (Honeycomb: "hangi attribute
// değerinde yoğunlaşıyor") CoSRE demetinde ve /rootcause panelinde vardı;
// arka planda üretilen problem açıklaması (AISummary → bildirim maili) onu
// hiç görmüyordu. Bu dosya sentezleyicinin derin kanıtına BubbleUp ekler.
//
// MALİYET — neden bu kadar sıkı: kıyas ham `spans` taraması (attribute
// anahtar/değerleri için MV yok; /rootcause, hakem kataloğu, CoSRE ve MCP
// aynı duruşta). Sentezleyici her 30 s her derin çıpaya gatherDeepEvidence
// koşar; aynı yere düz bir çağrı çıpa başına saatte 120 tarama demekti.
// Sözleşme:
//   - Bölüm (problem id + StartedAt) başına BİR tarama; sonuç DeepEvidence'a
//     kalıcı yazılır ve sonraki tiklerde TAŞINIR (taşımanın kaynağı kalıcı
//     hipotez — tik başına TEK GetHypotheses okuması; bellek içi memo YOK,
//     yoksa lider değişiminde yeniden taranırdı).
//   - İlk tarama 10 dk'lık pencere dolmadan koştuysa pencere dolunca EN ÇOK
//     bir tazeleme; okuma düştüyse ≥5 dk sonra EN ÇOK bir yeniden deneme
//     (bubbleUpMaxAttempts = 2), sonra "okunamadı" dürüstçe kalır.
//   - Tik başına en çok synthesizerBubbleScansPerTick (2) tarama, her biri
//     rca.BubbleUpTimeout (8 s) tavanlı → tik duvarı en kötü +16 s; lider
//     kirası kendi goroutine'inde yenilenir. Kararlı durum 0 tarama/tik.
//   - Pencere [StartedAt, StartedAt+rca.ExtrasWindow] (10 dk) — hakem
//     kataloğu ve CoSRE çıpalı planıyla bayt-aynı; servis süzgeçli.
//   - Kıyas türü chstore.ProblemBubbleMode: none (runtime:, db-capacity:,
//     db-health:, monitor:, self-, servis olmayan özne) → HİÇ okuma yok.

import (
	"context"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rca"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

const (
	// synthesizerBubbleScansPerTick — tik başına en çok BubbleUp taraması.
	synthesizerBubbleScansPerTick = 2
	// bubbleUpMaxAttempts — bölüm başına en çok tarama (ilk + bir tazeleme
	// ya da bir yeniden deneme).
	bubbleUpMaxAttempts = 2
	// bubbleUpRetryAfter — okunamayan taramanın yeniden denenmesinden önce
	// beklenen süre (CH zorlanıyorsa 30 s sonra tekrar vurmak onu büyütür).
	bubbleUpRetryAfter = 5 * time.Minute
)

// bubbleUpReader — ServiceBubbleUp dikişi (testte sahte; üretimde store).
type bubbleUpReader interface {
	ServiceBubbleUp(ctx context.Context, service string, errorSubset bool, started, end time.Time) (*chstore.BubbleUpResult, error)
}

// bubbleReaderOrStore — runtimePodsOrStore deyimi: nil dikiş → store.
func (s *RootCauseSynthesizer) bubbleReaderOrStore() bubbleUpReader {
	if s.bubble != nil {
		return s.bubble
	}
	return s.store
}

type bubbleAction int

const (
	bubbleSkip  bubbleAction = iota // kanıt yok, okuma yok
	bubbleReuse                     // önceki tikin kanıtı aynen taşınır
	bubbleScan                      // tara (bütçeden düşer)
)

func (a bubbleAction) String() string {
	switch a {
	case bubbleReuse:
		return "reuse"
	case bubbleScan:
		return "scan"
	}
	return "skip"
}

// bubbleUpDecision — SAF (rootcause_bubbleup_test.go tablo). prior = kalıcı
// hipotezin DeepEvidence.BubbleUp'ı (yoksa nil); budgetLeft = bu tikte kalan
// tarama hakkı.
func bubbleUpDecision(p chstore.Problem, prior *chstore.BubbleUpEvidence, now time.Time, budgetLeft int) (bubbleAction, chstore.BubbleMode) {
	mode := chstore.ProblemBubbleMode(&p)
	if mode == chstore.BubbleModeNone {
		return bubbleSkip, mode
	}
	sameEpisode := prior != nil && prior.AnchorStartNs == p.StartedAt && prior.Mode == string(mode)
	if !sameEpisode {
		// Önceki bölümün kanıtı bu bölüme TAŞINMAZ (bayat yoğunlaşma yalan).
		if budgetLeft > 0 {
			return bubbleScan, mode
		}
		return bubbleSkip, mode
	}
	if prior.Attempts >= bubbleUpMaxAttempts {
		return bubbleReuse, mode
	}
	nowNs := now.UnixNano()
	var due bool
	switch prior.Status {
	case chstore.BubbleUpStatusUnreadable:
		due = nowNs-prior.ComputedNs >= bubbleUpRetryAfter.Nanoseconds()
	default:
		// İlk tarama pencere dolmadan koştuysa, pencere dolunca bir tazeleme.
		due = prior.ComputedNs < prior.ToNs && nowNs >= prior.ToNs
	}
	if due && budgetLeft > 0 {
		return bubbleScan, mode
	}
	return bubbleReuse, mode
}

// trimBubbleUp — kalıcı yazılacak kesit: render'ın göstereceği kadarı (en çok
// n attribute, yalnız ≥ rca.BubbleUpRenderMinScore ayrışan, her birinin EN
// ÜST değeri). chstore attribute'ları en üst puana göre azalan verir; sıra
// korunur. Oranlar chstore biriminde kalır (dönüşüm rca.TopBubbleUp'ta).
func trimBubbleUp(attrs []chstore.BubbleUpAttribute, n int) []chstore.BubbleUpAttribute {
	var out []chstore.BubbleUpAttribute
	for _, a := range attrs {
		if len(out) >= n {
			break
		}
		if len(a.Values) == 0 || a.Values[0].Score <= rca.BubbleUpRenderMinScore {
			continue
		}
		out = append(out, chstore.BubbleUpAttribute{Key: a.Key, Values: []chstore.BubbleUpValue{a.Values[0]}})
	}
	return out
}

// attachBubbleUp — kararı uygular, d.BubbleUp'ı doldurur (ya da nil bırakır).
// budget her taramada bir azalır.
func (s *RootCauseSynthesizer) attachBubbleUp(ctx context.Context, p chstore.Problem, d *chstore.DeepEvidence, prior *chstore.BubbleUpEvidence, budget *int, now time.Time) {
	action, mode := bubbleUpDecision(p, prior, now, *budget)
	switch action {
	case bubbleSkip:
		return
	case bubbleReuse:
		cp := *prior
		d.BubbleUp = &cp
		return
	}
	*budget--
	started := time.Unix(0, p.StartedAt)
	end := started.Add(rca.ExtrasWindow)
	bctx, cancel := context.WithTimeout(ctx, rca.BubbleUpTimeout)
	bu, err := s.bubbleReaderOrStore().ServiceBubbleUp(bctx, p.Service, mode == chstore.BubbleModeErrors, started, end)
	cancel()

	sameEpisode := prior != nil && prior.AnchorStartNs == p.StartedAt && prior.Mode == string(mode)
	attempts := uint8(1)
	if sameEpisode {
		attempts = prior.Attempts + 1
	}
	if err != nil || bu == nil {
		if ctx.Err() != nil {
			// Kapanış: deneme sayılmaz, önceki kanıt (varsa) aynen kalır.
			if sameEpisode {
				cp := *prior
				d.BubbleUp = &cp
			}
			return
		}
		if sameEpisode && prior.Status == chstore.BubbleUpStatusOK {
			// Tazeleme düştü: eksik pencerenin sonucu okunamadı'dan iyidir;
			// deneme sayılır, bir daha tazelenmez.
			cp := *prior
			cp.Attempts = attempts
			d.BubbleUp = &cp
			return
		}
		ev := &chstore.BubbleUpEvidence{
			Mode: string(mode), AnchorStartNs: p.StartedAt,
			FromNs: started.UnixNano(), ToNs: end.UnixNano(), ComputedNs: now.UnixNano(),
			Attempts: attempts, Status: chstore.BubbleUpStatusUnreadable,
		}
		if err != nil {
			ev.Error = string(sourcestate.Classify(err))
		}
		d.BubbleUp = ev
		return
	}
	d.BubbleUp = &chstore.BubbleUpEvidence{
		Mode: string(mode), AnchorStartNs: p.StartedAt,
		FromNs: started.UnixNano(), ToNs: end.UnixNano(), ComputedNs: now.UnixNano(),
		Attempts: attempts, Status: chstore.BubbleUpStatusOK,
		SelectionTotal: bu.SelectionTotal, BaselineTotal: bu.BaselineTotal,
		Attributes: trimBubbleUp(bu.Attributes, rca.BubbleUpRenderCap),
	}
}

// priorBubbleUps — tikin TEK kalıcı-hipotez okuması (taşımanın kaynağı).
// Yalnız tarama adayı (kıyas türü none olmayan) problemler sorulur; okuma
// düşerse nil — o tik "önceki yok" gibi davranır (bütçe yine 2 ile sınırlı).
func priorBubbleUps(hyps map[string]chstore.RootCauseHypothesis, id string) *chstore.BubbleUpEvidence {
	h, ok := hyps[id]
	if !ok || h.Deep == nil {
		return nil
	}
	return h.Deep.BubbleUp
}

// bubbleCandidateIDs — GetHypotheses'e gidecek kimlikler (SAF).
func bubbleCandidateIDs(problems []chstore.Problem) []string {
	var ids []string
	for i := range problems {
		if synthesizerSkipsProblem(problems[i]) || chstore.ProblemBubbleMode(&problems[i]) == chstore.BubbleModeNone {
			continue
		}
		ids = append(ids, problems[i].ID)
	}
	return ids
}

// renderBubbleUpEvidence — derin kanıt bloğunun BubbleUp satırları (metin
// gövdesi rca.RenderBubbleUpTR — CoSRE demetiyle tek gövde).
func renderBubbleUpEvidence(e *chstore.BubbleUpEvidence) string {
	if e == nil {
		return ""
	}
	mins := (e.ToNs - e.FromNs) / int64(time.Minute)
	r := rca.BubbleUpRender{
		ErrorFamily: e.Mode == string(chstore.BubbleModeErrors),
		Window:      "problemin açılışını izleyen " + strconv.FormatInt(mins, 10) + " dk",
		Result:      e.Result(),
	}
	if e.Status == chstore.BubbleUpStatusUnreadable {
		r.Unreadable = e.Error
	}
	return rca.RenderBubbleUpTR(r)
}
