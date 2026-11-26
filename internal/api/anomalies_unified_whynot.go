package api

// anomalies_unified_whynot.go — v0.10.2482 (Sinyaller: "neden Problem değil
// (henüz)"). Bağlanmamış anomaliler listesinin (?unattached=1) her satırı
// kapının KENDİ kararından kısa bir durum taşır (whyNot):
//   - ML satırı: ml.WhyNotProblemOf — üyelerin kanıtındaki grup / servis
//     düzeyi kararı + tek sinyal kolunun üye şartı (singleArmCheck);
//   - yalnız eski dedektör olayı: evaluator.LegacyPromotionStatus — süpürmenin
//     AYNI kapıları (promotionGate / subjectPromotionGate), kodlar "legacy_".
// Dedektör yeniden koşmaz; satır başına sorgu yok (ayarlar istek başına bir
// kez, satırlar zaten okunmuş). Cümle arayüzde (lib/whyNotProblem.ts, TR/EN).

import (
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/evaluator"
	"github.com/cilcenk/coremetry/internal/ml"
)

// whyNotLegacyPrefix — eski olay durum kodlarının öneki (FE ile aynı).
const whyNotLegacyPrefix = "legacy_"

// whyNotCtx — istek başına bir kez kurulan girdiler.
type whyNotCtx struct {
	Gate ml.GroupGateInput
	Live bool // ML grup → Problem anahtarı (GroupProblemsOn)
	Prom chstore.AnomalyPromotionConfig
	Subj chstore.AnomalySubjectsConfig
	Now  time.Time
}

// unifiedWhyNot — SAF: birleşik satırın durumu. ML üyelerinde kapı kararı
// varsa o; yoksa eski lider olayın terfi kapısı; ikisi de yoksa nil.
func unifiedWhyNot(u ml.UnifiedRow, items []chstore.MLEvent, legacy []chstore.AnomalyEvent, c whyNotCtx) *ml.WhyNotProblem {
	running := ml.UnifiedOpen(u.Status)
	if len(u.ML) > 0 {
		evs := make([]chstore.MLEvent, 0, len(u.ML))
		for _, j := range u.ML {
			if j >= 0 && j < len(items) {
				evs = append(evs, items[j])
			}
		}
		if w, ok := ml.WhyNotProblemOf(evs, ml.WhyNotInput{Gate: c.Gate, Live: c.Live, Running: running, Now: c.Now}); ok {
			return &w
		}
	}
	if u.LegLead < 0 || u.LegLead >= len(legacy) {
		return nil
	}
	ev := legacy[u.LegLead]
	at := c.Now
	if !running && ev.LastSeen > 0 {
		at = time.Unix(0, ev.LastSeen) // biten olay: kural son görüldüğü anda neyi karşılamadı
	}
	s := evaluator.LegacyPromotionStatus(ev, c.Prom, c.Subj, at)
	code := ml.WhyGatePassed
	if s.Unmet != "" {
		code = whyNotLegacyPrefix + s.Unmet
	}
	// Eski olayda kalan süre verilmez: susturma ve açık Problem çağıranda
	// (anomalyPromotionStep) — durum onları bilmez.
	return &ml.WhyNotProblem{Code: code, Running: running, Minutes: s.Minutes, Ratio: s.Ratio, Count: s.Count}
}
