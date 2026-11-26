package evaluator

// promotion_shortfall.go — v0.10.2482 (Sinyaller: "neden Problem değil
// (henüz)"). Eski dedektör olayının terfi kapıları (promotionGate,
// subjectPromotionGate) karşılanmayan İLK şartı söyleyen SAF çekirdeğe
// bölündü; kapılar bu çekirdeğin "şart kalmadı" cevabıdır (karar AYNI).
// Sürdürme her kapıda en son denetlenir: PromoteUnmetShort döndüyse diğer
// bütün sayısal şartlar şimdi karşılanıyor ve Wait kalan süredir.
//
// LegacyPromotionStatus Sinyaller satırının durumunu kurar (api). Susturma
// ve "açık Problem'i var" çağıranda (anomalyPromotionStep) — durum onları
// bilmez; bu yüzden eski olayda kalan süre arayüzde söylenmez.

import (
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Karşılanmayan şart kodları.
const (
	PromoteUnmetOff          = "promotion_off" // terfi ana anahtarı / özne kipi kapalı
	PromoteUnmetNotWorsening = "not_worsening" // özne: kötüleşme (metrik + yön) değil
	PromoteUnmetBelowRatio   = "below_ratio"   // bölüm tepesi oranın altında
	PromoteUnmetBelowCount   = "below_count"   // sayım-yalnız olay: sayım tabanının altında
	PromoteUnmetMinCount     = "min_count"     // hacim tabanı (MinCount) altında
	PromoteUnmetShort        = "short"         // yalnız sürdürme dolmadı
	PromoteUnmetSubject      = "subject"       // genel kapı özne olayını değerlendirmez
)

// PromotionShortfall — kapının cevabı. Unmet "" = kapı geçer.
type PromotionShortfall struct {
	Unmet   string
	Ratio   float64       // below_ratio: gereken oran
	Count   float64       // below_count / min_count: gereken sayım
	Minutes int           // short: sürdürme penceresi (dk, yukarı yuvarlı)
	Wait    time.Duration // short: kalan süre
}

// sustainShortfall — SAF: sürdürme şartı (now − StartedAt ≥ sec).
func sustainShortfall(ev chstore.AnomalyEvent, sec int, now time.Time) PromotionShortfall {
	need := time.Duration(sec) * time.Second
	if el := now.Sub(time.Unix(0, ev.StartedAt)); el < need {
		return PromotionShortfall{Unmet: PromoteUnmetShort, Minutes: (sec + 59) / 60, Wait: need - el}
	}
	return PromotionShortfall{}
}

// promotionShortfall — SAF: promotionGate'in çekirdeği (bkz. promotionGate).
func promotionShortfall(ev chstore.AnomalyEvent, cfg chstore.AnomalyPromotionConfig, now time.Time) PromotionShortfall {
	if chstore.IsSubjectAnomaly(ev) {
		return PromotionShortfall{Unmet: PromoteUnmetSubject}
	}
	if chstore.IsCountOnlyAnomaly(ev) {
		if ev.CurrentCount < cfg.CountOnlyMinCount || ev.CurrentCount < cfg.MinCount {
			return PromotionShortfall{Unmet: PromoteUnmetBelowCount, Count: float64(max(cfg.CountOnlyMinCount, cfg.MinCount))}
		}
		return sustainShortfall(ev, cfg.MinSustainedSec, now)
	}
	if ev.PeakRatio < cfg.MinPeakRatio {
		return PromotionShortfall{Unmet: PromoteUnmetBelowRatio, Ratio: cfg.MinPeakRatio}
	}
	if ev.CurrentCount < cfg.MinCount {
		return PromotionShortfall{Unmet: PromoteUnmetMinCount, Count: float64(cfg.MinCount)}
	}
	return sustainShortfall(ev, cfg.MinSustainedSec, now)
}

// LegacyPromotionStatus — SAF: eski olayın terfi kapısı durumu; süpürmenin
// AYNI kapıları (genel: promotionGate, özne: subjectPromotionGate) ve ana
// anahtarları (pcfg.Enabled, özne kipi off).
func LegacyPromotionStatus(ev chstore.AnomalyEvent, pcfg chstore.AnomalyPromotionConfig, scfg chstore.AnomalySubjectsConfig, now time.Time) PromotionShortfall {
	if !pcfg.Enabled {
		return PromotionShortfall{Unmet: PromoteUnmetOff}
	}
	if chstore.IsSubjectAnomaly(ev) {
		if scfg.PromotionMode != chstore.SubjectPromotionShadow && scfg.PromotionMode != chstore.SubjectPromotionLive {
			return PromotionShortfall{Unmet: PromoteUnmetOff}
		}
		return subjectPromotionShortfall(ev, scfg, pcfg, now)
	}
	return promotionShortfall(ev, pcfg, now)
}
