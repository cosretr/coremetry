package chstore

import (
	"testing"
	"time"
)

// v0.10.2477 — şerit çizgisi adımı: pencere → adım; MV granülaritesine
// (dar rollup taban adımı) saygı + nokta tavanı.
func TestStripLineStep(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	cases := []struct {
		name     string
		from, to time.Time
		pts      int
		want     int
	}{
		{"kapalı (0 nokta)", ago(time.Hour), now, 0, 0},
		{"15 dk / 240 → 10 s taban (15'lik basamak yok)", ago(15 * time.Minute), now, 240, 10},
		{"1 sa / 240 → 15 s ham → 20 s", ago(time.Hour), now, 240, 20},
		{"1 sa / 120 → 30 s", ago(time.Hour), now, 120, 30},
		{"3 sa / 240 → 45 s ham → 60 s", ago(3 * time.Hour), now, 240, 60},
		{"6 sa / 240 → 90 s ham → 120 s", ago(6 * time.Hour), now, 240, 120},
		{"24 sa / 240 → 360 s ham → 600 s", ago(24 * time.Hour), now, 240, 600},
		{"7 g / 240 → 2520 s ham → 3600 s", ago(7 * 24 * time.Hour), now, 240, 3600},
		{"tavan: 1000 nokta istense de 240", ago(time.Hour), now, 1000, 20},
		{"30 g → gün katı değil, 3 sa basamağı", ago(30 * 24 * time.Hour), now, 240, 10800},
		{"300 g → gün katı", ago(300 * 24 * time.Hour), now, 240, 172800},
		// Pencere başı 10 s katmanının (7 g) dışında → 1 dk katmanı: 30 s
		// basamağı rollup'a oturmazdı (ham yol) — 60 s'ye çıkar.
		{"8 g önce 1 sa'lik pencere → 1 dk taban", ago(8 * 24 * time.Hour), ago(8*24*time.Hour - time.Hour), 240, 60},
		{"20 g önce 1 sa → 5 dk taban", ago(20 * 24 * time.Hour), ago(20*24*time.Hour - time.Hour), 240, 300},
		{"100 g önce 1 sa → 1 sa taban", ago(100 * 24 * time.Hour), ago(100*24*time.Hour - time.Hour), 240, 3600},
		{"boş pencere → taban", now, now, 240, 10},
	}
	for _, c := range cases {
		got := StripLineStep(c.from, c.to, now, c.pts)
		if got != c.want {
			t.Errorf("%s: StripLineStep = %d, beklenen %d", c.name, got, c.want)
			continue
		}
		if got == 0 {
			continue
		}
		// Değişmezler: taban adımın katı, nokta tavanı aşılmaz.
		base := stripLineBaseSec(c.from, now)
		if got%base != 0 || got < base {
			t.Errorf("%s: adım %d taban %d'in katı değil", c.name, got, base)
		}
		if win := c.to.Sub(c.from).Seconds(); win > 0 && int(win)/got > StripLineMaxPoints {
			t.Errorf("%s: %d nokta > tavan %d", c.name, int(win)/got, StripLineMaxPoints)
		}
		// Seçilen adım dar rollup katmanına oturur (fast-path açık kalır).
		if _, ok := pickNarrowRollupTier(got, c.from, now); !ok {
			t.Errorf("%s: adım %d hiçbir dar rollup katmanına oturmuyor", c.name, got)
		}
	}
}
