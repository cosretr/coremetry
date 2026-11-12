package ml

// baseline_kind.go — v0.10.2065 (Davis S5, docs/anomaly/davis-ml-gap.md satır
// 10): olay kanıtına evidence.baselineKind — tabanın ne olduğu:
//
//	seasonal  aynı takvim sınıfındaki günler × ±1 saat (normal yol)
//	recent    sınıf eşleşmesi < 3 gün → son N günün tümü (baselineDates
//	          düşüşü; ML bunu daha önce yazmıyordu) ya da kırılma noktasında
//	          önceki seviye
//	none      zaman tabanı yok: pod'lar arası karşılaştırma ya da ilk kez
//	          görülen şablon / hata kodu
//
// Tek yer: recorder tikinde, dedektör adayları toplandıktan sonra (dedektör
// kendi türünü yazdıysa korunur). Ek sorgu yok — takvim saf hesap. Yalnız
// katlı teknik ayrıntıda gösterilir (technicalOf "Beklenen").

import (
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	baselineSeasonal = "seasonal"
	baselineRecent   = "recent"
	baselineNone     = "none"
)

// baselineKindOf — SAF: olayın taban türü.
func baselineKindOf(ev chstore.MLEvent, days int, cal chstore.AnomalyCalendarConfig, loc *time.Location) string {
	switch ev.Detector {
	case detTracePodOutlier, detPodOutlier, detRareTemplate, detRareErrorCode, detNewTemplate:
		return baselineNone
	case detChangePoint, detTraceChangePoint:
		return baselineRecent
	}
	at := time.Unix(0, ev.LastSeen).Add(-time.Duration(ev.WindowSec) * time.Second)
	if _, _, seasonal := baselineDatesKind(at, days, cal, loc); seasonal {
		return baselineSeasonal
	}
	return baselineRecent
}

// withBaselineKind — SAF: kanıta baselineKind ekler (varsa dokunmaz).
func withBaselineKind(ev chstore.MLEvent, days int, cal chstore.AnomalyCalendarConfig, loc *time.Location) chstore.MLEvent {
	if evStr(evidenceMap(ev.Evidence), "baselineKind") != "" {
		return ev
	}
	ev.Evidence = mergeEvidence(ev.Evidence, map[string]any{"baselineKind": baselineKindOf(ev, days, cal, loc)})
	return ev
}

// baselineKindTR — teknik ayrıntı metni ("" = eski olay, tür yok).
var baselineKindTR = map[string]string{
	baselineSeasonal: "taban: mevsimsel (aynı takvim sınıfı)",
	baselineRecent:   "taban: son günlerin tümü (takvim sınıfı yetersiz ya da önceki seviye)",
	baselineNone:     "taban: zaman tabanı yok (pod'lar arası ya da ilk kez görülen)",
}
