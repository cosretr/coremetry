package ml

// svc_compare.go — v0.10.2077 (spec dilim 3): eski servis düzeyi
// Problem'leri (`svc-slowdown:<svc>`, `anomaly:<svc>:error_rate`) ile yeni
// gölge kararlarının (`svcShadow`) karşılaştırması. SAF: okumalar çağıranda
// (api/ml_svc_shadow.go). Eşleme: aynı servis + aralık örtüşmesi
// (±svcCompareSlack). Sütunlar spec'teki gibi: eski kural + açılış +
// öncelik, yeni olurdu + açılış + öncelik, fark (eşleşti / kaçırdı /
// fazladan / geç).

import (
	"sort"
	"strings"
	"time"
)

// svcCompareSlack — eşleşme toleransı ve "geç" eşiği.
const svcCompareSlack = 15 * time.Minute

// SvcCompareOld — eski Problem satırı (girdi).
type SvcCompareOld struct {
	Service, RuleID, Priority string
	Start, End                int64 // unix ns; End 0 = açık
}

// SvcCompareNew — yeni gölge kararı (girdi; olay başına).
type SvcCompareNew struct {
	Service, Detector, Priority string
	Start, End                  int64
}

// SvcCompareRow — tablo satırı.
type SvcCompareRow struct {
	Service     string `json:"service"`
	OldRule     string `json:"oldRule,omitempty"`
	OldStart    int64  `json:"oldStart,omitempty"`
	OldPriority string `json:"oldPriority,omitempty"`
	NewDetector string `json:"newDetector,omitempty"`
	NewStart    int64  `json:"newStart,omitempty"`
	NewPriority string `json:"newPriority,omitempty"`
	// Diff — matched | missed (eski var, yeni yok) | extra (yeni var, eski
	// yok) | late (yeni, eskiden > 15 dk sonra).
	Diff string `json:"diff"`
}

// SvcCompareOldFamily — SAF: kural eski servis düzeyi ailesinden mi.
func SvcCompareOldFamily(rule string) bool {
	return strings.HasPrefix(rule, "svc-slowdown:") || (strings.HasPrefix(rule, "anomaly:") && strings.HasSuffix(rule, ":error_rate"))
}

func overlaps(aS, aE, bS, bE int64, now time.Time, slack time.Duration) bool {
	if aE == 0 {
		aE = now.UnixNano()
	}
	if bE == 0 {
		bE = now.UnixNano()
	}
	return aS <= bE+int64(slack) && bS <= aE+int64(slack)
}

// CompareSvcShadow — SAF: eski × yeni → satırlar (en yeni önce). Her eski
// satır en erken örtüşen yeni kararla eşleşir; yeni karar birden çok eskiyle
// eşleşebilir (hata + yavaşlama aynı anda). Eşleşmeyen yeni = fazladan.
func CompareSvcShadow(olds []SvcCompareOld, news []SvcCompareNew, now time.Time) []SvcCompareRow {
	sort.Slice(news, func(i, j int) bool { return news[i].Start < news[j].Start })
	used := make([]bool, len(news))
	var out []SvcCompareRow
	for _, o := range olds {
		row := SvcCompareRow{Service: o.Service, OldRule: o.RuleID, OldStart: o.Start, OldPriority: o.Priority, Diff: "missed"}
		for i, n := range news {
			if n.Service != o.Service || !overlaps(o.Start, o.End, n.Start, n.End, now, svcCompareSlack) {
				continue
			}
			used[i] = true
			row.NewDetector, row.NewStart, row.NewPriority = n.Detector, n.Start, n.Priority
			row.Diff = "matched"
			if n.Start > o.Start+int64(svcCompareSlack) {
				row.Diff = "late"
			}
			break
		}
		out = append(out, row)
	}
	for i, n := range news {
		if !used[i] {
			out = append(out, SvcCompareRow{Service: n.Service, NewDetector: n.Detector, NewStart: n.Start, NewPriority: n.Priority, Diff: "extra"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return rowStart(out[i]) > rowStart(out[j]) })
	return out
}

func rowStart(r SvcCompareRow) int64 {
	if r.OldStart > 0 && (r.NewStart == 0 || r.OldStart < r.NewStart) {
		return r.OldStart
	}
	return r.NewStart
}
