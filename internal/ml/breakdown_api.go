package ml

// breakdown_api.go — v0.10.2388 (Problem detayı "Hata nerede" bölümü,
// api/problem_breakdown.go). Bölüm rotaların "olağan" hata oranını ve hangi
// değerin "sapan" olduğunu dedektörün KENDİ tanımlarıyla söylemeli; ikisi de
// bu pakette yaşıyor ve dışa kapalıydı. Kopyalamak yerine iki ince dışa açım:
// tanım tek yerde kalır (taban dilimi log_red.go baselineDatesKind, eşik
// impact_rule.go impactRules), api yalnız okur.

import (
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// BaselineSlice — bir anın mevsimsel taban dilimi: aynı takvim sınıfındaki
// günler (yerel tarih) × o anın yerel saati ±1.
type BaselineSlice struct {
	Dates    []string
	HourFrom int
	HourTo   int
	TZ       string
	// Seasonal — false: sınıf eşleşmesi 3 günün altında kaldı, tüm günlere
	// düşüldü (baselineDatesKind).
	Seasonal bool
}

// SeasonalBaselineSlice — SAF: dedektör tikinin taban dilimi (trace_red.go
// Tick: baselineDates + saat ±1, gün sınırında kırpılır), `at` anı için.
func SeasonalBaselineSlice(at time.Time, cfg chstore.MLConfig, sens chstore.AnomalySensitivityConfig) BaselineSlice {
	cal, loc := calendarFor(sens)
	dates, _, seasonal := baselineDatesKind(at, cfg.BaselineDays, cal, loc)
	h := at.In(loc).Hour()
	return BaselineSlice{Dates: dates, HourFrom: max(h-1, 0), HourTo: min(h+1, 23), TZ: loc.String(), Seasonal: seasonal}
}

// FailureRateImpactThreshold — SAF: etki kuralının hata oranı eşiği (0–1);
// taban = olağan oran. Servis Problem'ini açan kuralın AYNISI (tek tanım).
func FailureRateImpactThreshold(base float64) float64 {
	return impactRules[impactFailureRate].threshold(base)
}
