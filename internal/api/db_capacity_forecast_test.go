package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.909 (parite #6 dilim 3) — DB gösterge kartı "kaç saat kaldı":
// seri seçimi (instance / tek seri / belirsiz), evaluator kapıları, band,
// neden cümleleri.
func trend(start, perHour float64, hours int) []chstore.CapacityTrendPoint {
	var out []chstore.CapacityTrendPoint
	for i := 0; i <= hours*12; i++ {
		out = append(out, chstore.CapacityTrendPoint{TSec: int64(i * 300), Usage: start + perHour*float64(i)/12})
	}
	return out
}

func TestPickTrendSeries(t *testing.T) {
	a, b := trend(10, 1, 2), trend(20, 1, 2)
	two := map[string][]chstore.CapacityTrendPoint{"db1": a, "db2": b}
	if pts, why := pickTrendSeries(two, "db2"); len(pts) != len(b) || pts[0].Usage != 20 || why != "" {
		t.Fatalf("instance seçimi: %v %q", len(pts), why)
	}
	if pts, why := pickTrendSeries(two, ""); pts != nil || why == "" {
		t.Fatal("instance'sız çoklu seri birleştirilmemeli")
	}
	if pts, _ := pickTrendSeries(map[string][]chstore.CapacityTrendPoint{"db1": a}, "unknown"); len(pts) != len(a) {
		t.Fatal("tek seri → o")
	}
	if pts, why := pickTrendSeries(two, "db9"); pts != nil || why != "bu instance için seri yok" {
		t.Fatalf("olmayan instance: %q", why)
	}
	if _, why := pickTrendSeries(nil, ""); why != "seri yok" {
		t.Fatalf("boş: %q", why)
	}
}

func TestDBForecastFrom(t *testing.T) {
	// %78'den saatte 4 → limit 100: ~3.5 sa (evaluator capacity_eta_test ile aynı vaka).
	f := dbForecastFrom(trend(78, 4, 2), 100, "vm", "")
	if f.Status != "ok" || f.Hours < 3.2 || f.Hours > 3.8 || f.R2 < 0.99 || f.Source != "vm" || f.WindowH != 2 || f.Points != 25 {
		t.Fatalf("temiz büyüme: %+v", f)
	}
	if f.LoHours <= 0 || f.LoHours > f.Hours || (!f.HiOpen && f.HiHours < f.Hours) {
		t.Fatalf("band ETA'yı sarmalı: %+v", f)
	}
	cases := []struct {
		name   string
		pts    []chstore.CapacityTrendPoint
		limit  float64
		reason string
		status string
		want   string
	}{
		{"düz", trend(50, 0, 2), 100, "", "none", "düz"},
		{"azalıyor", trend(90, -3, 2), 100, "", "none", "azalıyor"},
		{"uzak ufuk", trend(50, 0.5, 2), 100, "", "none", "24 saatten uzak"},
		{"az örnek", trend(78, 4, 2)[:4], 100, "", "none", "az örnek (son 2 saat)"},
		{"limit yok", trend(78, 4, 2), 0, "", "none", "limit bilinmiyor"},
		{"seri seçilemedi", nil, 100, "seri yok", "none", "seri yok"},
		{"tavanda", trend(101, 1, 2), 100, "", "at_limit", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dbForecastFrom(c.pts, c.limit, "ch", c.reason)
			if got.Status != c.status || got.Reason != c.want {
				t.Fatalf("%s / %q, beklenen %s / %q", got.Status, got.Reason, c.status, c.want)
			}
		})
	}
	noisy := trend(78, 2, 2)
	for i := range noisy {
		if i%2 == 0 {
			noisy[i].Usage += 8
		} else {
			noisy[i].Usage -= 8
		}
	}
	if g := dbForecastFrom(noisy, 100, "ch", ""); g.Status != "none" || g.Reason == "" || g.R2 >= 0.6 {
		t.Fatalf("gürültülü: %+v", g)
	}
}
