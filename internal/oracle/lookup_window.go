package oracle

import (
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// lookupWindow — v0.10.917 (operator-reported: "trace bulmasına rağmen
// bulunamadı diyor"). CH trace araması poll/test penceresiyle ([from, to],
// özel kipte ayardaki WindowMin) sınırlıydı; ama özel SQL penceresini
// KENDİ SYSDATE aralığıyla seçiyor (ör. son 15 dk) ve satırlar o
// pencereden eski olabiliyor. 14 dk önceki bir trace, "son 5 dk ± 5 dk"
// aramasının dışında kalıp "bulunamadı" sayılıyordu.
//
// Pencere satırların KENDİ zaman damgalarını kapsayacak şekilde genişler
// (sıfır damga yok sayılır), sonra pad eklenir. CH maliyeti için genişleme
// `to`dan en çok lookupWindowMax geriye gider.
const lookupWindowMax = 6 * time.Hour

func lookupWindow(rows []chstore.OracleErrorRow, from, to time.Time, pad time.Duration) (time.Time, time.Time) {
	lo, hi := from, to
	for _, r := range rows {
		if r.Time.IsZero() {
			continue
		}
		if r.Time.Before(lo) {
			lo = r.Time
		}
		if r.Time.After(hi) {
			hi = r.Time
		}
	}
	if floor := to.Add(-lookupWindowMax); lo.Before(floor) {
		lo = floor
	}
	if lo.After(from) {
		lo = from
	}
	return lo.Add(-pad), hi.Add(pad)
}
