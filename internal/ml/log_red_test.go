package ml

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// v0.10.1747 — ML log dilim 1 saf çekirdek: judge (yön, dwell, eksik veri,
// taban eşiği, susma), takvim günleri, ingest payı, backfill planı, seri toplama.
func TestJudge(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.MinBaselineSamples = 5
	base := []float64{100, 102, 98, 101, 99, 100, 103, 97}
	cases := []struct {
		name   string
		cur    []float64
		upOnly bool
		fire   bool
		dir    string
		silent bool
		reason string
	}{
		{"up sustained", []float64{300, 320}, false, true, "up", false, ""},
		{"down sustained", []float64{10, 5}, false, true, "down", false, ""},
		{"silent", []float64{0, 0}, false, true, "down", true, ""},
		{"down ignored upOnly", []float64{10, 5}, true, false, "", false, "dwell: kova eşiği geçmedi"},
		{"short spike no dwell", []float64{300, 100}, false, false, "", false, "dwell: kova eşiği geçmedi"},
		{"direction flip", []float64{300, 5}, false, false, "", false, "dwell kovaları yön değiştirdi"},
		{"not enough cur", []float64{300}, false, false, "", false, "dwell: yeterli kova yok"},
	}
	for _, c := range cases {
		j := judge(c.cur, base, cfg, c.upOnly)
		if j.Fire != c.fire || j.Direction != c.dir || j.Silent != c.silent || (!c.fire && j.Reason != c.reason) {
			t.Errorf("%s: %+v", c.name, j)
		}
	}
	if j := judge([]float64{300, 300}, base[:3], cfg, false); j.Fire || j.Reason == "" {
		t.Fatalf("eksik veri sessiz olmalı: %+v", j)
	}
	if j := judge([]float64{30, 30}, []float64{1, 1, 1, 1, 1, 1}, cfg, false); j.Fire {
		t.Fatalf("taban hacmi eşiğin altında sessiz olmalı: %+v", j)
	}
}

func TestBaselineDatesAndOffDay(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Istanbul")
	// 2026-10-28 Çarşamba
	at := time.Date(2026, 10, 28, 10, 0, 0, 0, loc)
	dates, class := baselineDates(at, 7, chstore.AnomalyCalendarConfig{}, loc)
	if class != "weekday" || len(dates) != 5 || dates[0] != "2026-10-27" {
		t.Fatalf("weekday dates: %v class=%s", dates, class)
	}
	// Pazar: 7 günde tek pazar → <3 eşleşme → tüm günler
	sun := time.Date(2026, 11, 1, 10, 0, 0, 0, loc)
	d2, c2 := baselineDates(sun, 7, chstore.AnomalyCalendarConfig{}, loc)
	if c2 != "sunday" || len(d2) != 7 {
		t.Fatalf("sunday fallback: %v class=%s", d2, c2)
	}
	if !isOffDay("holiday") || !isOffDay("half_day") || isOffDay("weekday") {
		t.Fatal("off-day sınıfları")
	}
}

func TestLastCompleteBucketAndBackfill(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 7, 30, 0, time.UTC)
	// lag 120 sn → 12:05:30 → kova 12:05 → son tam kova 12:00
	if got := lastCompleteBucket(now, 300, 120); !got.Equal(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("last complete: %v", got)
	}
	// lag 0 → 12:05 kova başı → son tam 12:00; lag 600 → 11:57 → kova 11:55 → son tam 11:50
	if got := lastCompleteBucket(now, 300, 600); !got.Equal(time.Date(2026, 10, 31, 11, 50, 0, 0, time.UTC)) {
		t.Fatalf("lag 600: %v", got)
	}
	plan := backfillHours(now, 21, 0)
	if len(plan) != 504 || !plan[0][0].Equal(now.Truncate(time.Hour).Add(-21*24*time.Hour)) || plan[503][1] != now.Truncate(time.Hour) {
		t.Fatalf("plan: %d %v", len(plan), plan[0])
	}
	if rest := backfillHours(now, 21, plan[99][0].Unix()); len(rest) != 404 || rest[0][0] != plan[100][0] {
		t.Fatalf("resume: %d", len(rest))
	}
}

func TestSeriesFromRows(t *testing.T) {
	rows := []logstore.MLLogRow{
		{Service: "orders-api", Level: "INFO", Bucket: 100, Count: 10},
		{Service: "orders-api", Level: "ERROR", Bucket: 100, Count: 3},
		{Service: "orders-api", Level: "error", Bucket: 400, Count: 1},
	}
	s := seriesFromRows(rows, chstore.DefaultMLConfig().ErrorLevels)
	if s[chstore.MLSeriesKey("orders-api", partTotal)][100] != 13 || s[chstore.MLSeriesKey("orders-api", partError)][100] != 3 || s[chstore.MLSeriesKey("orders-api", partError)][400] != 1 {
		t.Fatalf("series: %+v", s)
	}
}

func TestLogREDEnabled(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	d := NewLogRED()
	if d.Enabled(cfg) {
		t.Fatal("varsayılan kapalı")
	}
	cfg.Enabled, cfg.Detectors[detErrorRate] = true, true
	if !d.Enabled(cfg) {
		t.Fatal("error_rate açıkken bileşik açık")
	}
}

// v0.10.1765 — canlı pencere yalnız son tam kova + bir öncekisi.
func TestLiveWindow(t *testing.T) {
	last := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	from, to := liveWindow(last, 5*time.Minute)
	if !from.Equal(last.Add(-5*time.Minute)) || !to.Equal(last.Add(5*time.Minute)) {
		t.Fatalf("%v %v", from, to)
	}
}

// v0.10.2076 — log hacmi artışı mutlak tabanı: küçük sayılardaki sıçrama
// (20 → 200 satır) olay değil; düşüş yönü etkilenmez.
func TestLogVolumeImpactFloor(t *testing.T) {
	cases := []struct {
		name string
		j    Judgement
		want bool
	}{
		{"20 → 200: z yüksek ama etki küçük", Judgement{Fire: true, Direction: "up", Median: 20, Observed: 200}, false},
		{"taban + 999", Judgement{Fire: true, Direction: "up", Median: 500, Observed: 1499}, false},
		{"taban + 1000", Judgement{Fire: true, Direction: "up", Median: 500, Observed: 1500}, true},
		{"büyük sıçrama", Judgement{Fire: true, Direction: "up", Median: 4000, Observed: 30000}, true},
		{"düşüş etkilenmez", Judgement{Fire: true, Direction: "down", Median: 4000, Observed: 100}, true},
	}
	for _, c := range cases {
		if got := logVolumeImpact(c.j); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}
