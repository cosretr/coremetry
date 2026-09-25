package chstore

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/config"
)

// v0.10.911 (parite #6 dilim 4) — kalıcı disk serisi: seri kimliği yazan
// (evaluator, host dolu) ve okuyan (sysstats, tek düğümde host=”) için AYNI;
// stat edilemeyen disk yazılmaz; tarihçe tahmini kapıları + neden cümleleri.
func TestSelfDiskSeriesServiceAndPoints(t *testing.T) {
	single := &Store{cfg: config.CHConfig{}}
	if a, b := single.SelfDiskSeriesService("ch-host-1", "default"), single.SelfDiskSeriesService("", "default"); a != b || a != "coremetry-self/default" {
		t.Fatalf("tek düğümde yazan/okuyan aynı kimlik: %q / %q", a, b)
	}
	cluster := &Store{cfg: config.CHConfig{ClusterName: "c1"}}
	if got := cluster.SelfDiskSeriesService("ch-1", "default"); got != "coremetry-self/ch-1/default" {
		t.Fatalf("küme kimliği: %q", got)
	}
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	pts := cluster.SelfDiskPoints([]DiskFree{
		{Host: "ch-1", Disk: "default", Free: 40, Total: 100},
		{Host: "ch-2", Disk: "default", Free: 0, Total: 0},     // stat edilemedi
		{Host: "ch-3", Disk: "default", Free: 200, Total: 100}, // bozuk
	}, now)
	if len(pts) != 1 {
		t.Fatalf("yalnız geçerli disk yazılmalı: %d", len(pts))
	}
	p := pts[0]
	if p.Metric != SelfDiskUsedMetric || p.Instrument != "gauge" || p.Value != 60 || p.ServiceName != "coremetry-self/ch-1/default" || p.SeriesFingerprint == 0 || !p.Time.Equal(now) {
		t.Fatalf("nokta: %+v", p)
	}
}

func TestDiskForecastFromHistory(t *testing.T) {
	gib := float64(uint64(1) << 30)
	hourly := func(hours int, start, perDay float64) []SpanMetricPoint {
		out := make([]SpanMetricPoint, 0, hours)
		for h := 0; h < hours; h++ {
			out = append(out, SpanMetricPoint{Time: int64(h) * 3600 * 1e9, Value: start + perDay*float64(h)/24})
		}
		return out
	}
	// 60 GiB'tan günde 2 GiB → 100 GiB'a ~20 gün (7 gün tarihçe sonrası ~13 gün kalır).
	f := diskForecastFromHistory(hourly(7*24, 60*gib, 2*gib), 100*gib)
	if f.Status != "ok" || f.Days < 12 || f.Days > 14 || f.Critical || f.R2 < 0.99 || f.WindowDays != 7 || f.ProblemID != "" {
		t.Fatalf("temiz büyüme: %+v", f)
	}
	cases := []struct {
		name   string
		pts    []SpanMetricPoint
		cap    float64
		status string
		reason string
	}{
		{"tarihçe birikiyor", hourly(3, 60*gib, 2*gib), 100 * gib, "none", "tarihçe birikiyor (2 saat; en az 6 saat gerekli)"},
		{"düz", hourly(48, 60*gib, 0), 100 * gib, "none", "düz"},
		{"azalıyor", hourly(48, 60*gib, -1*gib), 100 * gib, "none", "azalıyor"},
		{"30 günden uzak", hourly(48, 10*gib, 0.1*gib), 100 * gib, "none", "30 günden uzak"},
		{"kapasite yok", hourly(48, 10*gib, 1*gib), 0, "none", "kapasite bilinmiyor"},
		{"tavanda", hourly(48, 101*gib, 1*gib), 100 * gib, "at_limit", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := diskForecastFromHistory(c.pts, c.cap)
			if g.Status != c.status || g.Reason != c.reason {
				t.Fatalf("%s / %q, beklenen %s / %q", g.Status, g.Reason, c.status, c.reason)
			}
		})
	}
	if g := diskForecastFromHistory(hourly(48, 101*gib, 1*gib), 100*gib); !g.Critical {
		t.Fatal("tavanda kritik")
	}
}
