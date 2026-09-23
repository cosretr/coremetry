package chstore

import (
	"context"
	"fmt"
	"time"
)

// MetricBaseline summarises the recent distribution of one
// alertable metric for one service (or globally when service
// is empty). Drives the "✨ suggest threshold" panel on the
// alert-rule editor — operators see what NORMAL looks like
// before they pick a threshold, instead of guessing 5 / 500ms
// and getting paged at 4am because the actual P99 baseline
// was 800ms.
//
// All fields are in the SAME unit the alert evaluator
// compares against, so the operator can paste a value
// directly into the threshold input:
//
//	error_rate    → percentage (0..100)
//	p50_ms / p95_ms / p99_ms / avg_ms → milliseconds
//	request_rate  → requests per second
//	error_count   → absolute count per window (5 min default)
type MetricBaseline struct {
	Metric      string  `json:"metric"`
	Service     string  `json:"service,omitempty"` // empty = all services
	P50         float64 `json:"p50"`
	P95         float64 `json:"p95"`
	P99         float64 `json:"p99"`
	Max         float64 `json:"max"`
	Mean        float64 `json:"mean"`
	SampleCount int64   `json:"sampleCount"` // # of spans (gecikme) / 5-dk kova (oranlar)
	// BucketSec — v0.10.877 (inceleme): oran/sayı dağılımlarının hesaplandığı kova
	// genişliği (service_summary_5m → 300). 866 öncesi 60 s ham dakikaydı; 5-dk
	// ortalama spiky serviste dakika tepesinin altında kalır — öneri UI'ı bunu
	// SÖYLER, gizlemez. Gecikme dalı span-düzeyi t-digest: 0 (kova yok).
	BucketSec int64 `json:"bucketSec"`
	WindowSec int64 `json:"windowSec"` // lookback the percentiles were computed over
}

// GetMetricBaseline runs the right percentile query for the
// requested metric over the given lookback. Service filter is
// optional — global baselines help when the operator is
// adding a "warn on any service exceeding X" cross-service
// rule. Hard cap of 7 days; longer lookbacks were measured to
// add ~3s without changing the percentile values meaningfully
// (recent distribution dominates).
func (s *Store) GetMetricBaseline(
	ctx context.Context, service, metric string, lookback time.Duration,
) (*MetricBaseline, error) {
	if lookback <= 0 {
		lookback = 7 * 24 * time.Hour
	}
	if lookback > 7*24*time.Hour {
		lookback = 7 * 24 * time.Hour
	}
	out := &MetricBaseline{
		Metric:    metric,
		Service:   service,
		WindowSec: int64(lookback / time.Second),
	}
	// v0.10.866 (scale-audit 2026-09-23) — dört dal da HAM spans'ı 7 gün tarıyordu
	// (üst zaman sınırı yok, servis boşken tüm-servis quantile, tek fren 10 s):
	// invariant #3 ihlali — service_summary_5m bu toplamları 5-dk state olarak
	// zaten taşıyor. Gecikme: quantilesTDigestMerge(0.5,0.95,0.99,1) (q(1) ≈ max,
	// t-digest uç centroid'i); oran/sayı: kova başına merge'ler üstünde quantile.
	// SampleCount: gecikmede span, oranlarda 5-dk kova sayısı (eskiden dakika).
	to := time.Now()
	from := to.Add(-lookback)
	sqlText, latency, ok := baselineSQL(metric, service != "")
	if !ok {
		return nil, fmt.Errorf("baseline not supported for metric %q", metric)
	}
	args := []any{from, to}
	if service != "" {
		args = append(args, service)
	}
	row := s.conn.QueryRow(ctx, sqlText, args...)
	var n uint64
	if latency {
		var q []float64
		if err := row.Scan(&q, &out.Mean, &n); err != nil {
			return nil, fmt.Errorf("scan %s baseline: %w", metric, err)
		}
		if n > 0 && len(q) == 4 {
			out.P50, out.P95, out.P99, out.Max = q[0]/1e6, q[1]/1e6, q[2]/1e6, q[3]/1e6
		}
	} else if err := row.Scan(&out.P50, &out.P95, &out.P99, &out.Max, &out.Mean, &n); err != nil {
		return nil, fmt.Errorf("scan %s baseline: %w", metric, err)
	}
	if !latency {
		out.BucketSec = baselineBucketSec
	}
	if n == 0 { // boş pencere: quantile NaN döner; sıfır dürüst (FE n=0'ı "veri yok" okur)
		out.P50, out.P95, out.P99, out.Max, out.Mean = 0, 0, 0, 0, 0
	}
	out.SampleCount = int64(n)
	return out, nil
}

// baselineBucketSec — service_summary_5m kovası; oran/sayı dağılımlarının tabanı.
const baselineBucketSec = 300

// baselineWhere — service_summary_5m penceresi: [from, to) + isteğe bağlı servis.
func baselineWhere(withService bool) string {
	w := " WHERE time_bucket >= ? AND time_bucket < ?"
	if withService {
		w += " AND service_name = ?"
	}
	return w
}

// baselineSQL — SAF (v0.10.866): metrik → MV sorgusu. latency=true ise satır
// (q Array(Float64), mean, n); değilse (p50, p95, p99, max, mean, n).
func baselineSQL(metric string, withService bool) (sqlText string, latency, ok bool) {
	switch metric {
	case "p50_ms", "p95_ms", "p99_ms", "avg_ms":
		return `SELECT quantilesTDigestMerge(0.5, 0.95, 0.99, 1)(duration_q_state) AS q,
		       ifNull(sumMerge(duration_sum_state) / nullIf(countMerge(span_count_state), 0), 0) / 1e6 AS mean,
		       countMerge(span_count_state) AS n
		FROM service_summary_5m` + baselineWhere(withService) + `
		SETTINGS max_execution_time = 10`, true, true
	case "error_rate":
		return baselineBucketSQL("countMerge(error_count_state) / nullIf(countMerge(span_count_state), 0) * 100", withService), false, true
	case "request_rate":
		return baselineBucketSQL("countMerge(span_count_state) / 300.0", withService), false, true
	case "error_count":
		return baselineBucketSQL("countMerge(error_count_state)", withService), false, true
	}
	return "", false, false
}

// baselineBucketSQL — kova başına değer → kovalar üstünde dağılım.
func baselineBucketSQL(expr string, withService bool) string {
	return `WITH per_bucket AS (
			SELECT time_bucket AS t, ` + expr + ` AS v
			FROM service_summary_5m` + baselineWhere(withService) + `
			GROUP BY t
		)
		SELECT quantile(0.5)(v), quantile(0.95)(v), quantile(0.99)(v), max(v), avg(v), count()
		FROM per_bucket
		SETTINGS max_execution_time = 10`
}
