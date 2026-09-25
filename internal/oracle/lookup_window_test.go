package oracle

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.917 — operator-reported: özel SQL'in 15 dk'lık kendi penceresinden
// gelen 14 dk'lık trace, "son 5 dk" test penceresi yüzünden CH'de aranmıyordu.
func TestLookupWindow(t *testing.T) {
	to := time.Date(2026, 9, 25, 18, 12, 0, 0, time.UTC)
	from := to.Add(-5 * time.Minute)
	pad := 5 * time.Minute
	at := func(d time.Duration) chstore.OracleErrorRow { return chstore.OracleErrorRow{Time: to.Add(d)} }
	cases := []struct {
		name           string
		rows           []chstore.OracleErrorRow
		wantLo, wantHi time.Time
	}{
		{"satır yok → pencere + pad", nil, from.Add(-pad), to.Add(pad)},
		{"pencere içi satır değiştirmez", []chstore.OracleErrorRow{at(-2 * time.Minute)}, from.Add(-pad), to.Add(pad)},
		{"14 dk önceki satır kapsanır (operatör vakası)", []chstore.OracleErrorRow{at(-14 * time.Minute)}, to.Add(-14*time.Minute - pad), to.Add(pad)},
		{"sıfır damga yok sayılır", []chstore.OracleErrorRow{{}}, from.Add(-pad), to.Add(pad)},
		{"geleceğe kaymış damga üst sınırı açar", []chstore.OracleErrorRow{at(3 * time.Minute)}, from.Add(-pad), to.Add(3*time.Minute + pad)},
		{"genişleme 6 saatle sınırlı", []chstore.OracleErrorRow{at(-30 * time.Hour)}, to.Add(-lookupWindowMax - pad), to.Add(pad)},
	}
	for _, c := range cases {
		lo, hi := lookupWindow(c.rows, from, to, pad)
		if !lo.Equal(c.wantLo) || !hi.Equal(c.wantHi) {
			t.Errorf("%s: got [%v, %v] want [%v, %v]", c.name, lo, hi, c.wantLo, c.wantHi)
		}
	}
}
