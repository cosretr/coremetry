package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/thanos"
)

// v0.10.912 (parite #6 dilim 4 Karar 2) — Clusters KPI "kaç gün": temiz büyüme
// + band, adım dakikası, her "yok" nedeni, tavanda.
func TestCapacityForecastFromSeries(t *testing.T) {
	series := func(n int, start, perDay float64) []thanos.ValuePoint {
		out := make([]thanos.ValuePoint, n)
		for i := range out {
			out[i] = thanos.ValuePoint{Bucket: int64(1_790_000_000 + i*1800), Value: start + perDay*float64(i)/48}
		}
		return out
	}
	// 400 çekirdekten günde 4 çekirdek → 512'ye: 7 gün sonra 428 → ~21 gün.
	f := capacityForecastFromSeries(series(336, 400, 4), 512)
	if f.Status != "ok" || f.Days < 19 || f.Days > 23 || f.R2 < 0.99 || f.StepMin != 30 || f.WindowDays != 7 || f.Points != 336 {
		t.Fatalf("temiz büyüme: %+v", f)
	}
	if f.LoDays <= 0 || f.LoDays > f.Days || (!f.HiOpen && f.HiDays < f.Days) {
		t.Fatalf("band: %+v", f)
	}
	cases := []struct {
		name, status, reason string
		pts                  []thanos.ValuePoint
		cap                  float64
	}{
		{"kapasite yok", "none", "kapasite bilinmiyor", series(336, 400, 4), 0},
		{"az veri", "none", "az veri (Thanos tarihçesi kısa)", series(6, 400, 4), 512},
		{"düz", "none", "düz", series(336, 400, 0), 512},
		{"30 günden uzak", "none", "30 günden uzak", series(336, 100, 1), 512},
		{"tavanda", "at_limit", "", series(336, 520, 1), 512},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := capacityForecastFromSeries(c.pts, c.cap)
			if g.Status != c.status || g.Reason != c.reason {
				t.Fatalf("%s / %q, beklenen %s / %q", g.Status, g.Reason, c.status, c.reason)
			}
		})
	}
}
