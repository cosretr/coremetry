package notify

// stamp.go — v0.10.758: bildirim damgaları sunucu varsayılan diliminde
// (tzdefault: COREMETRY_TZ, imajda Europe/Istanbul — v0.10.746) ve konum
// ADIYLA. Eskiden beş şablon RFC3339 UTC ("…T00:05:46Z"), WhatsApp "15:04
// MST" basıyordu; operatör ekranı yerel saat gösterirken mail 3 saat
// geride okunuyordu (v0.10.745 CoSRE olayının insan-okur ikizi).
// Şekil FE'nin tek tarih-saat biçimiyle aynı: "dd.mm.yyyy HH:mm:ss".

import (
	"time"

	"github.com/cilcenk/coremetry/internal/tzdefault"
)

// stampIn — SAF.
func stampIn(ns int64, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return time.Unix(0, ns).In(loc).Format("02.01.2006 15:04:05") + " " + loc.String()
}

// clockIn — SAF: kısa şekil (WhatsApp).
func clockIn(ns int64, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return time.Unix(0, ns).In(loc).Format("15:04") + " " + loc.String()
}

func notifyStamp(ns int64) string { return stampIn(ns, tzdefault.Location()) }
func notifyClock(ns int64) string { return clockIn(ns, tzdefault.Location()) }
