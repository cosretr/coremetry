package ml

// promote.go — ML terfi kopya yolu (K4, v0.10.1761). Terfi = gölge olayın
// anomaly_events'e KOPYA satırı (kind "ml:<dedektör>"); ml_events satırı
// yerinde değişmez, yalnız stage=promoted işaretlenir. Kapı PromotionGate
// (ana + dedektör + terfi anahtarı, karne örneği + isabet, takvim dilimi);
// hepsi varsayılan KAPALI → hiçbir terfi anahtarı açık değilken tik başına
// sıfır ek okuma. Tekilleştirme (audit §3): aynı servis + çakışan pencerede
// elastic_ml satırı varsa terfi yok, dup_of doldurulur.

import (
	"context"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const promotedKindPrefix = "ml:"

// anyPromotionOn — SAF: en az bir dedektörün terfi anahtarı açık mı.
func anyPromotionOn(cfg chstore.MLConfig) bool {
	if !cfg.Enabled {
		return false
	}
	for det, p := range cfg.Promotion {
		if p.Enabled && cfg.Detectors[det] {
			return true
		}
	}
	return false
}

// promotionDecision — SAF: terfi edilsin mi; elastic_ml çakışmasında dupOf.
func promotionDecision(ev chstore.MLEvent, cfg chstore.MLConfig, score chstore.MLDetectorScore, tz string, existing []chstore.AnomalyEvent) (bool, string) {
	if ev.DupOf != "" || ev.Stage == chstore.MLStagePromote || !cfg.PromotionAllowed(ev.Detector, score, tz) {
		return false, ""
	}
	for _, a := range existing {
		if a.Kind == "elastic_ml" && a.Service == ev.Service && a.StartedAt <= ev.LastSeen && a.LastSeen >= ev.StartedAt {
			return false, a.ID
		}
	}
	return true, ""
}

// promotedCopy — SAF: gölge olay → anomaly_events satırı.
func promotedCopy(ev chstore.MLEvent) chstore.AnomalyEvent {
	part := eventPart(ev)
	return chstore.AnomalyEvent{ID: ev.ID, Kind: promotedKindPrefix + ev.Detector, Pattern: part, Service: ev.Service,
		StartedAt: ev.StartedAt, LastSeen: ev.LastSeen, PeakRatio: ev.Score, CurrentRatio: ev.Score, CurrentCount: uint64(max(ev.Observed, 0)),
		Sample: fmt.Sprintf("ML %s · gözlenen %.3g / taban %.3g · skor %.1f", ev.Detector, ev.Observed, ev.Baseline, ev.Score)}
}

// promote — recorder tik sonu. Terfi anahtarı kapalıyken hemen döner.
func (r *Recorder) promote(ctx context.Context, cfg chstore.MLConfig, kept []chstore.MLEvent) error {
	if !anyPromotionOn(cfg) || len(kept) == 0 {
		return nil
	}
	rows, err := r.store.MLScorecard(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		return err
	}
	scores := map[string]chstore.MLDetectorScore{}
	for _, s := range rows {
		scores[s.Detector] = s
	}
	tz := r.store.AnomalySensitivityForDetectors().Calendar.Timezone
	svcSet := map[string]bool{}
	earliest := kept[0].StartedAt
	for _, ev := range kept {
		svcSet[ev.Service] = true
		earliest = min(earliest, ev.StartedAt)
	}
	svcs := make([]string, 0, len(svcSet))
	for s := range svcSet {
		svcs = append(svcs, s)
	}
	existing, err := r.store.ListAnomalyEvents(ctx, chstore.ListAnomalyEventsFilter{SinceNs: earliest - int64(time.Hour), Services: svcs, Limit: 500})
	if err != nil {
		return err
	}
	var firstErr error
	for _, ev := range kept {
		ok, dup := promotionDecision(ev, cfg, scores[ev.Detector], tz, existing)
		if dup != "" {
			ev.DupOf = dup
		} else if ok {
			if err := r.store.UpsertAnomalyEvent(ctx, promotedCopy(ev)); err != nil {
				firstErr = err
				continue
			}
			ev.Stage = chstore.MLStagePromote
			global.promoted.Add(1)
		} else {
			continue
		}
		if err := r.store.UpsertMLEvent(ctx, ev); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
