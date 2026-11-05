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
//	live   — dilim 4 (v0.10.1800).

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

// promoteSubjectAnomalies — özne terfi süpürmesi (bkz. dosya başı).
func (e *Evaluator) promoteSubjectAnomalies(ctx context.Context, pcfg chstore.AnomalyPromotionConfig, muted map[string]bool, now time.Time) {
	scfg := e.store.AnomalySensitivity().Subjects
	if scfg.PromotionMode != chstore.SubjectPromotionShadow {
		return
	}
	events, err := e.store.ListAnomalyEvents(ctx, chstore.ListAnomalyEventsFilter{
		Kind:       chstore.SubjectAnomalyKind,
		ActiveOnly: true,
		SinceNs:    now.Add(-time.Hour).UnixNano(),
		Limit:      subjectSweepLimit,
	})
	if err != nil {
		log.Printf("[evaluator] özne terfisi: olaylar okunamadı: %v", err)
		return
	}
	if len(events) >= subjectSweepLimit {
		log.Printf("[evaluator] özne terfisi: okuma tavanı (%d) doldu — en eski olaylar bu süpürmede değerlendirilmedi", subjectSweepLimit)
	}
	gate := func(ev chstore.AnomalyEvent) bool { return subjectPromotionGate(ev, scfg, pcfg, now) }
	cands := groupSubjectEvents(events, muted, gate)
	marks, n := subjectShadowMarks(events, cands, muted, gate)
	if len(marks) == 0 {
		return
	}
	if err := e.store.UpsertAnomalyEvents(ctx, marks); err != nil {
		log.Printf("[evaluator] özne terfisi (gölge): %d iz yazılamadı: %v", len(marks), err)
		return
	}
	log.Printf("[evaluator] özne terfisi (gölge): %d aday Problem açardı, %d olaya iz düşüldü", n, len(marks))
}
