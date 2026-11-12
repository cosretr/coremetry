package evaluator

// anomaly_subject_sweep.go — v0.10.1799 (Davis paritesi FAZ 3 dilim 3):
// özne olaylarının terfi süpürmesi. promoteStrongAnomalies'in sonunda, genel
// döngüden sonra koşar; kip anomaly_sensitivity.subjects.promotionMode.
//
//	off    — hiçbir okuma yok (FAZ 1 davranışı).
//	shadow — kapıyı geçen aday Problem AÇMAZ; adayın olay satırları
//	         promotion='shadow' ile yeniden yazılır (UpsertAnomalyEvents →
//	         MergeAnomalyCarry: bölüm alanları taşınır, iz bölüm içinde
//	         yapışkan). Karar 6: önce gölgede ölç.
//	live   — v0.10.1800: aday Problem AÇAR (anomalyPromotionStep: açık
//	         Problem varsa tazele — rotalar listesi yenilenir, bildirim YOK;
//	         yoksa kapı geçtiyse aç + incident'a bağla (karar 7) + bildir).
//	         Şiddet: tepe ≥ criticalPeakRatio → critical, yaş tabanına
//	         kelepçeli (effectiveSeverity). Açılış üyelerine promotion='live'.
//	         `-batch` kuralı olay üretiminde (subjects_eval.go); burada ek
//	         kural yok.

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// subjectSweepLimit — süpürmenin özne olayı okuma tavanı (aktif, son 1 sa).
// Özneler servis başına üst-N ile sınırlı (karar 1); tavan aşılırsa en yeni
// last_seen kazanır ve log'a düşer.
const subjectSweepLimit = 5000

// subjectShadowMarks — SAF: gölge kipinde iz düşülecek olaylar. Endpoint:
// açılış üyesi olan (kapıyı geçen) adayların açılış üyeleri; db/queue:
// kapıyı geçen aktif + susturulmamış olay. Zaten "shadow" izli satır yeniden
// yazılmaz (süpürme başına yazım yalnız YENİ izler). İkinci dönüş: aday
// sayısı (açılırdı denen Problem sayısı).
func subjectShadowMarks(events []chstore.AnomalyEvent, cands []subjectCandidate, muted map[string]bool, gate func(chstore.AnomalyEvent) bool) ([]chstore.AnomalyEvent, int) {
	var out []chstore.AnomalyEvent
	n := 0
	mark := func(ev chstore.AnomalyEvent) {
		if ev.Promotion == chstore.AnomalyPromotionShadow {
			return
		}
		ev.Promotion = chstore.AnomalyPromotionShadow
		out = append(out, ev)
	}
	for _, c := range cands {
		if len(c.Opening) == 0 {
			continue
		}
		n++
		for _, ev := range c.Opening {
			mark(ev)
		}
	}
	for _, ev := range events {
		if !chstore.IsSubjectAnomaly(ev) || ev.SubjectKind == chstore.AnomalySubjectEndpoint ||
			ev.Status != "active" || muted[ev.ID] || !gate(ev) {
			continue
		}
		n++
		mark(ev)
	}
	return out, n
}

// subjectLiveMarks — SAF (v0.10.1800): canlı açılışta iz düşülecek olaylar
// (zaten "live" izli olan yeniden yazılmaz).
func subjectLiveMarks(opening []chstore.AnomalyEvent) []chstore.AnomalyEvent {
	var out []chstore.AnomalyEvent
	for _, ev := range opening {
		if ev.Promotion == chstore.AnomalyPromotionLive {
			continue
		}
		ev.Promotion = chstore.AnomalyPromotionLive
		out = append(out, ev)
	}
	return out
}

// promoteSubjectAnomalies — özne terfi süpürmesi (bkz. dosya başı). Dönüş:
// aktif endpoint adaylarının rule_id kümesi (çözülme geçişi için); nil =
// bilinmiyor (kip off ya da okuma hatası) → çözülme geçişi özne grup
// Problem'lerine dokunmaz (bayat süpürme kapatır).
func (e *Evaluator) promoteSubjectAnomalies(ctx context.Context, pcfg chstore.AnomalyPromotionConfig, muted map[string]bool, now time.Time) map[string]bool {
	scfg := e.store.AnomalySensitivity().Subjects
	if scfg.PromotionMode != chstore.SubjectPromotionShadow && scfg.PromotionMode != chstore.SubjectPromotionLive {
		return nil
	}
	events, err := e.store.ListAnomalyEvents(ctx, chstore.ListAnomalyEventsFilter{
		Kind:       chstore.SubjectAnomalyKind,
		ActiveOnly: true,
		SinceNs:    now.Add(-time.Hour).UnixNano(),
		Limit:      subjectSweepLimit,
	})
	if err != nil {
		log.Printf("[evaluator] özne terfisi: olaylar okunamadı: %v", err)
		return nil
	}
	if len(events) >= subjectSweepLimit {
		log.Printf("[evaluator] özne terfisi: okuma tavanı (%d) doldu — en eski olaylar bu süpürmede değerlendirilmedi", subjectSweepLimit)
	}
	gate := func(ev chstore.AnomalyEvent) bool { return subjectPromotionGate(ev, scfg, pcfg, now) }
	activeGroups := activeSubjectGroupRules(events, muted)
	if scfg.PromotionMode == chstore.SubjectPromotionLive {
		e.promoteSubjectsLive(ctx, events, muted, gate, scfg, pcfg, now)
		return activeGroups
	}
	cands := groupSubjectEvents(events, muted, gate)
	marks, n := subjectShadowMarks(events, cands, muted, gate)
	if len(marks) == 0 {
		return activeGroups
	}
	if err := e.store.UpsertAnomalyEvents(ctx, marks); err != nil {
		log.Printf("[evaluator] özne terfisi (gölge): %d iz yazılamadı: %v", len(marks), err)
		return activeGroups
	}
	log.Printf("[evaluator] özne terfisi (gölge): %d aday Problem açardı, %d olaya iz düşüldü", n, len(marks))
	return activeGroups
}

// promoteSubjectsLive — canlı kip: aday başına aç / tazele (v0.10.1800).
func (e *Evaluator) promoteSubjectsLive(ctx context.Context, events []chstore.AnomalyEvent, muted map[string]bool,
	gate func(chstore.AnomalyEvent) bool, scfg chstore.AnomalySubjectsConfig, pcfg chstore.AnomalyPromotionConfig, now time.Time) {
	esc := e.escalationCfg(ctx)
	lazy := e.store.LazyOpenSnapshot()
	snapWarned := false
	var marks []chstore.AnomalyEvent
	opened := 0
	for _, c := range subjectLiveCandidates(events, muted, gate) {
		var open *chstore.Problem
		step := anomalyPromotionStep(true, false, len(c.Opening) > 0, func() bool {
			snap, snapErr := lazy.Get(ctx)
			if snapErr != nil && !snapWarned {
				log.Printf("[evaluator] özne terfisi: açık Problem anlık görüntüsü: %v", snapErr)
				snapWarned = true
			}
			open = snap.ByKey(c.RuleID, c.Key.Service)
			return open != nil
		})
		if step != promoteCreate && step != promoteRefresh {
			continue
		}
		p := subjectGroupProblem(c, open, scfg, pcfg, now)
		p.Severity = effectiveSeverity(p.Severity, now.Sub(time.Unix(0, p.StartedAt)), esc)
		if err := e.store.UpsertProblem(ctx, p); err != nil {
			log.Printf("[evaluator] özne terfisi %s: %v", c.RuleID, err)
			continue
		}
		if step != promoteCreate {
			continue // tazeleme: yeni rota listeye girer, bildirim YOK
		}
		opened++
		marks = append(marks, subjectLiveMarks(c.Opening)...)
		log.Printf("[evaluator] ÖZNE TERFİSİ: %s → %s", p.RuleName, p.Severity)
		if e.notifier != nil {
			go e.notifier.SendProblemAlert(context.Background(), p)
		}
	}
	if len(marks) > 0 {
		if err := e.store.UpsertAnomalyEvents(ctx, marks); err != nil {
			log.Printf("[evaluator] özne terfisi (canlı): %d iz yazılamadı: %v", len(marks), err)
		}
	}
	if opened > 0 {
		log.Printf("[evaluator] özne terfisi (canlı): %d Problem açıldı", opened)
	}
}
