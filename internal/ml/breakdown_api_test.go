package ml

// breakdown_api_test.go — v0.10.2388: Problem detayı "Hata nerede" bölümünün
// (api/problem_breakdown.go) okuduğu iki dışa açım dedektörün KENDİ tanımını
// verir: taban dilimi (aynı takvim sınıfı günler × ±1 saat) ve etki kuralının
// hata oranı eşiği.

import (
	"math"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSeasonalBaselineSlice(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	sens := chstore.DefaultAnomalySensitivity()
	sens.SeasonalTimezone = "UTC"
	_, loc := calendarFor(sens)
	cases := []struct {
		name   string
		at     time.Time
		hf, ht int
	}{
		{"gün ortası", time.Date(2026, 11, 18, 14, 20, 0, 0, time.UTC), 13, 15},
		{"gece yarısı: alt sınır 0", time.Date(2026, 11, 18, 0, 5, 0, 0, time.UTC), 0, 1},
		{"günün son saati: üst sınır 23", time.Date(2026, 11, 18, 23, 59, 0, 0, time.UTC), 22, 23},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SeasonalBaselineSlice(c.at, cfg, sens)
			wantDates, _ := baselineDates(c.at, cfg.BaselineDays, sens.Calendar, loc)
			if got.HourFrom != c.hf || got.HourTo != c.ht || got.TZ != loc.String() {
				t.Fatalf("dilim %+v, want saat %d–%d tz %s", got, c.hf, c.ht, loc)
			}
			if len(got.Dates) == 0 || len(got.Dates) != len(wantDates) {
				t.Fatalf("gün sayısı %d, dedektörünki %d", len(got.Dates), len(wantDates))
			}
			today := c.at.In(loc).Format("2006-01-02")
			for i, d := range got.Dates {
				if d != wantDates[i] {
					t.Fatalf("gün %d: %s, dedektörünki %s", i, d, wantDates[i])
				}
				if d == today {
					t.Fatalf("taban olay gününü içermemeli: %s", d)
				}
			}
		})
	}
}

func TestFailureRateImpactThreshold(t *testing.T) {
	cases := []struct{ base, want float64 }{
		{0, 0.01}, {0.004, 0.014}, {0.05, 0.075}, {math.NaN(), 0.01}, {-1, 0.01},
	}
	for _, c := range cases {
		if got := FailureRateImpactThreshold(c.base); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("taban %v → eşik %v, want %v", c.base, got, c.want)
		}
		// Tek tanım: etki kuralının kendi eşiği.
		if got, rule := FailureRateImpactThreshold(c.base), impactRules[impactFailureRate].threshold(c.base); got != rule {
			t.Errorf("taban %v: dışa açım %v ≠ kural %v", c.base, got, rule)
		}
	}
}
