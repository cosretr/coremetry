package chstore

// sysstats_disk_history.go — v0.10.911 (parite #6 dilim 4): açık self-disk-eta
// problemi YOKKEN disk satırına tarihçe tahmini — son 7 günün saatlik serisi
// (self_disk_series.go, rollup_metrics_1h) + forecast.Fit. Açık problem varsa
// rozet onu gösterir (alarmla tutarlı; attachDiskForecast önce koşar).
// Zarf 60 s serveCached; disk başına tek sorgu, ≤ diskHistoryMaxDisks.

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/forecast"
)

const (
	diskHistoryWindow   = 7 * 24 * time.Hour
	diskHistoryMaxDisks = 16
	diskHistoryBudget   = 4 * time.Second
	// Kapılar: ≥6 saatlik nokta, ≥6 sa yayılım, R² ≥ 0.6, ufuk ≤ 30 gün
	// (7 günlük pencereden 30 gün dürüst; daha uzağı "30 günden uzak").
	diskHistoryMinPoints = 6
	diskHistoryMinSpanS  = 6 * 3600
	diskHistoryMinR2     = 0.6
	diskHistoryMaxDays   = 30.0
)

// diskForecastFromHistory — SAF: saatlik seri + kapasite → rozet verisi.
func diskForecastFromHistory(pts []SpanMetricPoint, capacity float64) *DiskForecast {
	out := &DiskForecast{Status: string(forecast.StatusNone), Points: len(pts), WindowDays: int(diskHistoryWindow / (24 * time.Hour))}
	if capacity <= 0 {
		out.Reason = "kapasite bilinmiyor"
		return out
	}
	fp := make([]forecast.Point, 0, len(pts))
	for _, p := range pts {
		fp = append(fp, forecast.Point{TSec: p.Time / 1e9, V: p.Value})
	}
	if len(fp) > 0 {
		if span := fp[len(fp)-1].TSec - fp[0].TSec; span < diskHistoryMinSpanS {
			out.Reason = fmt.Sprintf("tarihçe birikiyor (%d saat; en az 6 saat gerekli)", span/3600)
			return out
		}
	}
	r := forecast.Fit(fp, capacity, forecast.Opts{
		MinPoints: diskHistoryMinPoints, MinSpanS: diskHistoryMinSpanS,
		MinR2: diskHistoryMinR2, HorizonS: diskHistoryMaxDays * 86400,
	})
	out.Status, out.R2 = string(r.Status), r.R2
	switch r.Status {
	case forecast.StatusOK:
		out.Days = r.ETASec / 86400
		out.Critical = out.Days < SelfDiskCriticalDays
		if r.HasBand {
			out.LoDays = r.ETALoSec / 86400
			if math.IsInf(r.ETAHiSec, 1) {
				out.HiOpen = true
			} else {
				out.HiDays = r.ETAHiSec / 86400
			}
			out.Wide = r.Wide()
		}
	case forecast.StatusAtLimit:
		out.Critical = true
	default:
		switch {
		case strings.HasPrefix(r.Reason, "ufuk dışı"):
			out.Reason = "30 günden uzak"
		case strings.HasPrefix(r.Reason, "az örnek"):
			out.Reason = "tarihçe birikiyor"
		default:
			out.Reason = r.Reason
		}
	}
	return out
}

// attachDiskHistory — problemsiz disklere tarihçe tahmini (soft-fail).
func (s *Store) attachDiskHistory(ctx context.Context, disks []DiskStat, now time.Time) {
	n := 0
	for i := range disks {
		d := &disks[i]
		if d.Forecast != nil || d.TotalBytes == 0 {
			continue
		}
		if n >= diskHistoryMaxDisks {
			return
		}
		n++
		hctx, cancel := context.WithTimeout(ctx, diskHistoryBudget)
		pts, err := s.SelfDiskHistory(hctx, s.SelfDiskSeriesService(d.Host, d.Name), diskHistoryWindow, now)
		cancel()
		if err != nil {
			continue
		}
		d.Forecast = diskForecastFromHistory(pts, float64(d.TotalBytes))
	}
}
