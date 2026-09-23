package chstore

import (
	"context"
	"log"
	"strings"
	"time"
)

// service_cluster_mv.go — v0.10.883 (Dynatrace paritesi #8, dilim 3).
// /api/services/{name}/clusters: ham spans (GetServiceClusterBreakdown, 50
// cluster) yerine service_env_summary_5m'den — p50/p95/p99 tdigest'ten,
// cluster başına çağrı serisi 5-dk kovalardan (≤288 nokta; üstü 5 dk katı
// kaba adıma iner). MV pencereyi kapsamıyorsa (EnvSummaryCovers) çağıran ham
// yola düşer; cevap `source` ile hangisinin okunduğunu söyler.
//
// cluster: MV clusterDeriveExpr ile doğar (ham yol s.clusterExpr — kolon varsa
// MATERIALIZED, aynı türetme) → iki yol aynı adları üretir.

const clusterSeriesMaxPoints = 288 // 24 sa × 5 dk

// clusterSeriesStep — SAF: pencereyi ≤ clusterSeriesMaxPoints noktaya sığdıran,
// 5 dk'nın katı adım (sn). 5 dk altına inmez (MV kovası).
func clusterSeriesStep(window time.Duration) int64 {
	step := int64(300)
	if window <= 0 {
		return step
	}
	need := int64(window.Seconds()) / clusterSeriesMaxPoints
	if need > step {
		step = ((need + 299) / 300) * 300
	}
	return step
}

// fillBuckets — SAF: [from, to) kafesine oturtur, eksik kova 0 (bilinen sıfır:
// %100 saklıyoruz, örnekleme yok — spanmetric zeroFill doktrini).
func fillBuckets(points map[int64]uint64, from, to time.Time, stepSec int64) []uint64 {
	if stepSec <= 0 || !to.After(from) {
		return nil
	}
	start := from.Unix() / stepSec * stepSec
	out := make([]uint64, 0, (to.Unix()-start)/stepSec+1)
	for b := start; b < to.Unix(); b += stepSec {
		out = append(out, points[b])
	}
	return out
}

func serviceClusterBreakdownMVSQL() string {
	return `
		SELECT cluster,
		       countMerge(span_count_state)                                AS span_count,
		       countIfMerge(error_count_state)                             AS error_count,
		       sumMerge(duration_sum_state) / nullIf(span_count, 0) / 1e6 AS avg_ms,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 1) / 1e6 AS p50_ms,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99_ms
		FROM service_env_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ? AND service_name = ?
		GROUP BY cluster
		HAVING cluster != ''
		ORDER BY span_count DESC
		LIMIT 50
		SETTINGS max_execution_time = 10, ` + mvQuantileMemSettings
}

func serviceClusterSeriesMVSQL(n int) string {
	holders := strings.TrimSuffix(strings.Repeat("?,", n), ",")
	return `
		SELECT cluster,
		       toUnixTimestamp(toStartOfInterval(time_bucket, INTERVAL ? SECOND)) AS b,
		       countMerge(span_count_state)                                       AS c
		FROM service_env_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ? AND service_name = ? AND cluster IN (` + holders + `)
		GROUP BY cluster, b
		ORDER BY cluster, b
		LIMIT 20000
		SETTINGS max_execution_time = 10`
}

// GetServiceClusterBreakdownSourced — MV kapsıyorsa MV (+ p50/p95 + seri),
// yoksa ham; ikinci dönüş "mv" | "spans". Eski GetServiceClusterBreakdown
// imzası korunur (kaynağı düşürür).
func (s *Store) GetServiceClusterBreakdownSourced(ctx context.Context, service string, from, to time.Time) ([]ServiceClusterStat, string, error) {
	if service != "" && s.EnvSummaryCovers(ctx, from) {
		rows, err := s.serviceClusterBreakdownMV(ctx, service, from, to)
		if err == nil {
			return rows, "mv", nil
		}
		log.Printf("[chstore] service_env_summary_5m cluster kırılımı düştü, ham yol: %v", err)
	}
	rows, err := s.GetServiceClusterBreakdown(ctx, service, from, to)
	return rows, "spans", err
}

func (s *Store) serviceClusterBreakdownMV(ctx context.Context, service string, from, to time.Time) ([]ServiceClusterStat, error) {
	from = alignBucketStart(from)
	if to.IsZero() {
		to = time.Now()
	}
	rows, err := s.telemetryReadConn().Query(ctx, serviceClusterBreakdownMVSQL(), from, to, service)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServiceClusterStat{}
	for rows.Next() {
		var r ServiceClusterStat
		var avg *float64
		var p50, p95, p99 float64 // Array(Float32) scan tuzağı yok: arrayElement (summary.go:682 deseni)
		if err := rows.Scan(&r.Cluster, &r.SpanCount, &r.ErrorCount, &avg, &p50, &p95, &p99); err != nil {
			return nil, err
		}
		r.AvgMs = safeF(avg)
		r.P50Ms, r.P95Ms, r.P99Ms = p50, p95, p99
		if r.SpanCount > 0 {
			r.ErrorRate = float64(r.ErrorCount) / float64(r.SpanCount) * 100
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	step := clusterSeriesStep(to.Sub(from))
	args := []any{step, from, to, service}
	names := make([]string, 0, len(out))
	for _, r := range out {
		args = append(args, r.Cluster)
		names = append(names, r.Cluster)
	}
	srows, err := s.telemetryReadConn().Query(ctx, serviceClusterSeriesMVSQL(len(names)), args...)
	if err != nil {
		return out, nil // seri süs: kırılım kalır, seri boş
	}
	defer srows.Close()
	points := map[string]map[int64]uint64{}
	for srows.Next() {
		var cl string
		var b uint32
		var c uint64
		if err := srows.Scan(&cl, &b, &c); err != nil {
			break
		}
		if points[cl] == nil {
			points[cl] = map[int64]uint64{}
		}
		points[cl][int64(b)] = c
	}
	for i := range out {
		out[i].Series = fillBuckets(points[out[i].Cluster], from, to, step)
	}
	return out, nil
}
