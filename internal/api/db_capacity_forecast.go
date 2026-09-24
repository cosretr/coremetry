package api

// db_capacity_forecast.go — v0.10.909 (Dynatrace paritesi #6 dilim 3; spec
// Onay 2026-09-24). DB kapasite gösterge kartlarına (Oracle sessions /
// processes, Postgres backends, MySQL connections) "kaç saat kaldı".
//
// İstek anında, şema değişikliği olmadan: evaluator'ın kullandığı UsageTrend
// okuyucusu (son 2 saat, 5 dk kova) + forecast.Fit (dilim 1 çekirdeği). Panel
// uçlarının mevcut 30 s önbelleğinin arkasında; yeni rota/polling yok. Kapılar
// evaluator capacityETA ile AYNI sayılar (≥6 nokta, ≥30 dk, R² ≥ 0.6, ufuk
// ≤24 sa) — 2 saatlik pencereden daha uzağa projeksiyon dürüst değil.
//
// Okuyucu seçimi evaluator.capacityReader ile aynı: VM yapılandırılmışsa VM
// (5 dk MAX), değilse CH (5 dk avg) — kaynak title'da yazılır.

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/forecast"
)

const (
	dbForecastWindow    = 2 * time.Hour
	dbForecastMinPoints = 6
	dbForecastMinSpanS  = 30 * 60
	dbForecastMinR2     = 0.6
	dbForecastMaxHours  = 24.0
	dbForecastBudget    = 5 * time.Second
)

// trendReader — chstore.Store ve vmetrics.Service ortak metodu.
type trendReader interface {
	UsageTrend(ctx context.Context, usageMetric, attrKey string, window time.Duration) (map[string][]chstore.CapacityTrendPoint, error)
}

func (s *Server) capacityTrendReader() (trendReader, string) {
	if s.vmetrics != nil && s.vmetrics.Configured() {
		return s.vmetrics, "vm"
	}
	if s.store == nil {
		return nil, ""
	}
	return s.store, "ch"
}

// pickTrendSeries — SAF: panelin instance'ına ait seri. Anahtar evaluator ile
// aynı (chstore.CapacityTrendKey, instanceExpr). Instance boş/"unknown" ise
// yalnız TEK seri varsa o; birden çok instance'ı birleştirmek uydurma olurdu.
func pickTrendSeries(series map[string][]chstore.CapacityTrendPoint, instance string) ([]chstore.CapacityTrendPoint, string) {
	inst := strings.TrimSpace(instance)
	if inst != "" && inst != "unknown" {
		if pts, ok := series[chstore.CapacityTrendKey(inst, "")]; ok {
			return pts, ""
		}
		return nil, "bu instance için seri yok"
	}
	if len(series) == 1 {
		for _, pts := range series {
			return pts, ""
		}
	}
	if len(series) == 0 {
		return nil, "seri yok"
	}
	return nil, "birden çok instance — panel instance'ı seçili değil"
}

// dbForecastFrom — SAF: seri + limit → kart verisi.
func dbForecastFrom(pts []chstore.CapacityTrendPoint, limit float64, source, reason string) *chstore.DBForecast {
	out := &chstore.DBForecast{Status: string(forecast.StatusNone), Source: source, WindowH: int(dbForecastWindow / time.Hour), Points: len(pts)}
	if reason != "" {
		out.Reason = reason
		return out
	}
	if limit <= 0 {
		out.Reason = "limit bilinmiyor"
		return out
	}
	fp := make([]forecast.Point, len(pts))
	for i, p := range pts {
		fp[i] = forecast.Point{TSec: p.TSec, V: p.Usage}
	}
	r := forecast.Fit(fp, limit, forecast.Opts{
		MinPoints: dbForecastMinPoints, MinSpanS: dbForecastMinSpanS,
		MinR2: dbForecastMinR2, HorizonS: dbForecastMaxHours * 3600,
	})
	out.Status, out.R2 = string(r.Status), r.R2
	switch r.Status {
	case forecast.StatusOK:
		out.Hours = r.ETASec / 3600
		if r.HasBand {
			out.LoHours = r.ETALoSec / 3600
			if math.IsInf(r.ETAHiSec, 1) {
				out.HiOpen = true
			} else {
				out.HiHours = r.ETAHiSec / 3600
			}
			out.Wide = r.Wide()
		}
	case forecast.StatusNone:
		out.Reason = forecastReasonTR(r.Reason)
	}
	return out
}

// forecastReasonTR — Fit nedeninin kart cümlesi ("ufuk dışı (…s)" → "24 saatten uzak").
func forecastReasonTR(reason string) string {
	switch {
	case strings.HasPrefix(reason, "ufuk dışı"):
		return "24 saatten uzak"
	case strings.HasPrefix(reason, "az örnek"), strings.HasPrefix(reason, "dar aralık"):
		return "az örnek (son 2 saat)"
	}
	return reason
}

// dbForecast — tek gösterge; hata/eksik okuyucu → nil (kart eski hâliyle).
func (s *Server) dbForecast(ctx context.Context, metric, instance string, limit float64) *chstore.DBForecast {
	rd, source := s.capacityTrendReader()
	if rd == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, dbForecastBudget)
	defer cancel()
	series, err := rd.UsageTrend(ctx, metric, "", dbForecastWindow)
	if err != nil {
		return nil
	}
	pts, reason := pickTrendSeries(series, instance)
	return dbForecastFrom(pts, limit, source, reason)
}
