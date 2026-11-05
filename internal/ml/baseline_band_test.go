package ml

// v0.10.1813 — bant çekirdeği (audit §4.1): son 4–8 haftanın medyanı, tek
// kötü haftaya dayanıklı, en az 4 hafta kuralı.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func weeksObs(q50s ...float32) []chstore.MLBaselineObs {
	base := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	out := make([]chstore.MLBaselineObs, len(q50s))
	for i, v := range q50s { // i=0 en yeni hafta
		out[i] = chstore.MLBaselineObs{Service: "orders-api", Part: "_total", Slot: 61, WeekStart: base.AddDate(0, 0, -7*(i+1)),
			Q10: v * 0.5, Q50: v, Q90: v * 1.5, Q99: v * 2, Calls: uint32(v), Buckets: 12}
	}
	return out
}

func TestBaselineBand(t *testing.T) {
	cases := []struct {
		name   string
		obs    []chstore.MLBaselineObs
		ok     bool
		weeks  int
		q50    float64
		q90    float64
		madq50 float64
	}{
		{"4 hafta = asgari, kurulur", weeksObs(100, 110, 90, 100), true, 4, 100, 150, 5},
		{"3 hafta < asgari, kurulmaz", weeksObs(100, 110, 90), false, 3, 0, 0, 0},
		{"tek kötü hafta (1000×) bandı bozmaz", weeksObs(100, 100000, 100, 100, 100, 100), true, 6, 100, 150, 0},
		{"8'den fazla: en yeni 8, eski uç düşer", weeksObs(100, 100, 100, 100, 100, 100, 100, 100, 9e6, 9e6, 9e6), true, 8, 100, 150, 0},
		{"çift sayı: orta ikisinin ortalaması", weeksObs(100, 200, 300, 400), true, 4, 250, 375, 100},
	}
	for _, c := range cases {
		b, ok := BaselineBand(c.obs, BandMinWeeks, BandMaxWeeks)
		if ok != c.ok || b.Weeks != c.weeks {
			t.Errorf("%s: ok=%v weeks=%d", c.name, ok, b.Weeks)
			continue
		}
		if ok && (b.Q50 != c.q50 || b.Q90 != c.q90 || b.MADQ50 != c.madq50) {
			t.Errorf("%s: q50=%v q90=%v mad=%v", c.name, b.Q50, b.Q90, b.MADQ50)
		}
	}
}

func TestBaselineBandSkipsEmptyAndDuplicateWeeks(t *testing.T) {
	obs := weeksObs(100, 100, 100, 100)
	empty := obs[0]
	empty.Buckets, empty.Q50 = 0, 1e9 // kovasız hafta sayılmaz
	dup := obs[1]
	dup.Q50 = 1e9 // aynı hafta ikinci kez: ilki kalır
	b, ok := BaselineBand(append([]chstore.MLBaselineObs{empty, obs[1], dup}, obs[2:]...), BandMinWeeks, BandMaxWeeks)
	if ok || b.Weeks != 3 {
		t.Fatalf("kovasız + tekrar eden hafta sayılmamalı: ok=%v weeks=%d", ok, b.Weeks)
	}
}

func TestBaselineBandsGroups(t *testing.T) {
	a := weeksObs(100, 100, 100, 100)
	other := weeksObs(7, 7, 7)
	for i := range other {
		other[i].Service = "payments-mobile"
	}
	got := BaselineBands(append(a, other...), BandMinWeeks, BandMaxWeeks)
	if len(got) != 1 || got[BandKey{Service: "orders-api", Part: "_total", Slot: 61}].Q50 != 100 {
		t.Fatalf("yalnız kurulabilen bant: %+v", got)
	}
}
