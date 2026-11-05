package ml

// baseline_band.go — v0.10.1813, öğrenme döngüsü Faz 1.2 (audit
// docs/anomaly/learning-loop-audit.md §4.1): ml_baseline_obs'un haftalık
// gözlemlerinden bant. Bant = aynı slotun son 4–8 haftasının q10/q50/q90
// medyanı; "p99 çizgisi" = haftalık q99 medyanı. Medyan: tek kötü hafta
// bandı bozmaz. Bu dilimde ÇAĞIRAN YOK (günlük iş 1.3, karar çekirdeği 1.6).
// Sınıf karışımı çağıranın işi: okuma (MLBaselineBand) tek sınıf döndürür.

import (
	"sort"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// BandMinWeeks — bandın kurulması için en az hafta (audit §4.1 "4–8").
	BandMinWeeks = 4
	// BandMaxWeeks — en yeni bu kadar hafta kullanılır; daha eskisi düşer.
	BandMaxWeeks = 8
)

// Band — bir (seri, slot) bandı.
type Band struct {
	Q10, Q50, Q90, Q99 float64
	// MADQ50 — haftalık q50'lerin MAD'ı (ölçeklenmemiş); §4.5 z paydasının
	// "1,4826·MAD_hafta" terimi.
	MADQ50 float64
	// Calls — haftalık çağrı medyanı (profil sınıflandırıcısı, §4.4).
	Calls float64
	Weeks int // kullanılan hafta
}

// BandKey — bant anahtarı.
type BandKey struct {
	Service, Part string
	Slot          uint8
}

// BaselineBand — SAF: tek (seri, slot) için haftalık gözlemler → bant.
// Kovasız hafta (buckets = 0) sayılmaz; aynı hafta iki kez gelirse
// (FINAL dışı okuma) ilki kalır. ok=false: minWeeks'ten az hafta.
func BaselineBand(obs []chstore.MLBaselineObs, minWeeks, maxWeeks int) (Band, bool) {
	if minWeeks < 1 {
		minWeeks = 1
	}
	if maxWeeks < minWeeks {
		maxWeeks = minWeeks
	}
	use := make([]chstore.MLBaselineObs, 0, len(obs))
	for _, o := range obs {
		if o.Buckets > 0 {
			use = append(use, o)
		}
	}
	sort.SliceStable(use, func(i, j int) bool { return use[i].WeekStart.After(use[j].WeekStart) })
	seen := map[int64]bool{}
	var q10, q50, q90, q99, calls []float64
	for _, o := range use {
		w := o.WeekStart.Unix()
		if seen[w] {
			continue
		}
		seen[w] = true
		q10 = append(q10, float64(o.Q10))
		q50 = append(q50, float64(o.Q50))
		q90 = append(q90, float64(o.Q90))
		q99 = append(q99, float64(o.Q99))
		calls = append(calls, float64(o.Calls))
		if len(q50) == maxWeeks {
			break
		}
	}
	b := Band{Weeks: len(q50)}
	if b.Weeks < minWeeks {
		return b, false
	}
	b.Q10, _ = medianMAD(q10)
	b.Q50, b.MADQ50 = medianMAD(q50)
	b.Q90, _ = medianMAD(q90)
	b.Q99, _ = medianMAD(q99)
	b.Calls, _ = medianMAD(calls)
	return b, true
}

// BaselineBands — SAF: okuma sonucunu (seri, slot) başına gruplar; yalnız
// kurulabilen bantları döner.
func BaselineBands(obs []chstore.MLBaselineObs, minWeeks, maxWeeks int) map[BandKey]Band {
	groups := map[BandKey][]chstore.MLBaselineObs{}
	for _, o := range obs {
		k := BandKey{Service: o.Service, Part: o.Part, Slot: o.Slot}
		groups[k] = append(groups[k], o)
	}
	out := make(map[BandKey]Band, len(groups))
	for k, g := range groups {
		if b, ok := BaselineBand(g, minWeeks, maxWeeks); ok {
			out[k] = b
		}
	}
	return out
}
