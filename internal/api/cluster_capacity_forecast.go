package api

// cluster_capacity_forecast.go — v0.10.912 (Dynatrace paritesi #6 dilim 4
// Karar 2; spec + mockup Onay 2026-09-25). Clusters › Overview "CPU used" /
// "Memory used" KPI kartlarına "kaç gün kaldı" çipi.
//
// Özet ucunun (60 s önbellek) içinde iki ek Thanos sorgusu: son 7 günün
// küme-toplamı CPU / bellek serisi (mevcut ResourceTrend, stepForWindow →
// 30 dk adım, ~336 nokta), paralel, her biri kendi bütçesiyle. Limit özetteki
// kapasite (allocatable toplamı); kapasite yoksa çip yok. Kapılar: ≥12 nokta,
// ≥6 sa yayılım, R² ≥ 0.6, ufuk ≤30 gün. Gündüz/gece dalgası R²'yi
// düşürebilir — "uyum zayıf" dürüstçe söylenir (mevsimsel düzeltme ayrı adım).
// Hosts BİLEREK yok (6 sa pencere, disk yok, pod-limit paydası).

import (
	"context"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/forecast"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const (
	clusterFcWindow    = 7 * 24 * time.Hour
	clusterFcBudget    = 8 * time.Second
	clusterFcMinPoints = 12
	clusterFcMinSpanS  = 6 * 3600
	clusterFcMinR2     = 0.6
	clusterFcMaxDays   = 30.0
)

// CapacityForecastDays — kart çipinin verisi (gün birimi).
type CapacityForecastDays struct {
	Status     string  `json:"status"` // ok | at_limit | none
	Days       float64 `json:"days,omitempty"`
	LoDays     float64 `json:"loDays,omitempty"`
	HiDays     float64 `json:"hiDays,omitempty"`
	HiOpen     bool    `json:"hiOpen,omitempty"`
	Wide       bool    `json:"wide,omitempty"`
	R2         float64 `json:"r2,omitempty"`
	Points     int     `json:"points"`
	StepMin    int     `json:"stepMin,omitempty"`
	WindowDays int     `json:"windowDays"`
	Reason     string  `json:"reason,omitempty"`
}

// clusterSummaryWithForecast — özet + çipler (gömülü alanlar düz kalır).
type clusterSummaryWithForecast struct {
	thanos.ClusterSummary
	CPUForecast *CapacityForecastDays `json:"cpuForecast,omitempty"`
	MemForecast *CapacityForecastDays `json:"memForecast,omitempty"`
}

// capacityForecastFromSeries — SAF: Thanos serisi (Bucket = unix sn) + kapasite.
func capacityForecastFromSeries(pts []thanos.ValuePoint, capacity float64) *CapacityForecastDays {
	out := &CapacityForecastDays{Status: string(forecast.StatusNone), Points: len(pts), WindowDays: int(clusterFcWindow / (24 * time.Hour))}
	if len(pts) >= 2 {
		out.StepMin = int((pts[1].Bucket - pts[0].Bucket) / 60)
	}
	if capacity <= 0 {
		out.Reason = "kapasite bilinmiyor"
		return out
	}
	fp := make([]forecast.Point, len(pts))
	for i, p := range pts {
		fp[i] = forecast.Point{TSec: p.Bucket, V: p.Value}
	}
	r := forecast.Fit(fp, capacity, forecast.Opts{
		MinPoints: clusterFcMinPoints, MinSpanS: clusterFcMinSpanS,
		MinR2: clusterFcMinR2, HorizonS: clusterFcMaxDays * 86400,
	})
	out.Status, out.R2 = string(r.Status), r.R2
	switch r.Status {
	case forecast.StatusOK:
		out.Days = r.ETASec / 86400
		if r.HasBand {
			out.LoDays = r.ETALoSec / 86400
			if math.IsInf(r.ETAHiSec, 1) {
				out.HiOpen = true
			} else {
				out.HiDays = r.ETAHiSec / 86400
			}
			out.Wide = r.Wide()
		}
	case forecast.StatusNone:
		switch {
		case strings.HasPrefix(r.Reason, "ufuk dışı"):
			out.Reason = "30 günden uzak"
		case strings.HasPrefix(r.Reason, "az örnek"), strings.HasPrefix(r.Reason, "dar aralık"):
			out.Reason = "az veri (Thanos tarihçesi kısa)"
		default:
			out.Reason = r.Reason
		}
	}
	return out
}

// clusterCapacityForecasts — iki trend paralel; hata → o çip nil (kart eski hâliyle).
func (s *Server) clusterCapacityForecasts(ctx context.Context, cfg thanos.ClusterConfig, sum thanos.ClusterSummary) (cpu, mem *CapacityForecastDays) {
	now := time.Now()
	from := now.Add(-clusterFcWindow)
	run := func(metric string, capacity float64) *CapacityForecastDays {
		if capacity <= 0 {
			return nil
		}
		tctx, cancel := context.WithTimeout(ctx, clusterFcBudget)
		defer cancel()
		series, err := s.thanos.ResourceTrend(tctx, cfg, metric, false, from, now)
		if err != nil || len(series) == 0 {
			return nil
		}
		return capacityForecastFromSeries(series[0].Points, capacity)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); cpu = run("cpu", sum.CPUCapacityCores) }()
	go func() { defer wg.Done(); mem = run("mem", sum.MemCapacityBytes) }()
	wg.Wait()
	return cpu, mem
}
