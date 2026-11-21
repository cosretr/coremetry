package chstore

// op_latency_series.go — v0.10.1299 (operatör 2026-10-17: "Bu sorgu metric
// grafiği 5 dakika aralıklı olduğu için sağlıklı değil. Aynı veriye Traces
// sayfasından bakınca süre grafiği düzgün gözüküyor").
//
// CoSRE sohbet grafiği (service_health_sweep operasyon bulgusu) önceden
// operation_summary_5m sparkline'ını çiziyordu: 5 dk kova p99'u, 3 saatte 36
// nokta, düşük hacimli operasyonda kova başına bir avuç span → 2–7 ms arası
// testere. Traces sayfasının şeridi ise ince (≤ ~100 nokta) tDigest kovaları
// okur. Bu okuyucu AYNI ince ailenin operasyon boyutlu MV'sinden okur:
// spanmetrics_1m (service_name, name, …, 1 dk; quantilesTDigestState(0.5, 0.9,
// 0.95, 0.99)). Adım dakikanın katıdır; daha kaba adım state BİRLEŞTİRİR
// (toStartOfInterval + quantilesTDigestMerge) — nokta SEÇİLMEZ.
//
// MV forward-only: pencere spanmetrics kapsamından (ya da 30 g TTL'den) önce
// başlıyorsa ErrOpLatencyNotCovered — çağıran 5 dk sparkline'a düşer (ek
// sorgu yok). Ham spans'a ASLA düşmez (CLAUDE.md MV-first).

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrOpLatencyNotCovered — pencere spanmetrics_1m kapsamında değil.
var ErrOpLatencyNotCovered = errors.New("spanmetrics_1m bu pencereyi kapsamıyor")

// OpLatencyPoint — tek kova (TS = kova başı, unix saniye).
type OpLatencyPoint struct {
	TS     int64
	Calls  uint64
	Errors uint64
	P50Ms  float64
	P99Ms  float64
}

// opLatencyTTL — spanmetrics_1m TTL'i (store.go DDL: 30 DAY); spanmetricTiers
// ile aynı sayı.
const opLatencyTTL = 30 * 24 * time.Hour

// opLatencySeriesSQL — SAF: okuma SQL'i (bound WHERE + LIMIT + max_execution_time).
// Bind sırası: service, name, from, to.
func opLatencySeriesSQL(source string, stepSec int) string {
	return fmt.Sprintf(`
		SELECT
		    toInt64(toUnixTimestamp(toStartOfInterval(time_bucket, INTERVAL %d SECOND))) AS tb,
		    countMerge(calls_state) AS calls,
		    countMerge(error_state) AS errs,
		    arrayMap(x -> toFloat64(x / 1e6), quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state)) AS qs
		FROM %s
		WHERE service_name = ? AND name = ? AND time_bucket >= ? AND time_bucket < ?
		GROUP BY tb
		ORDER BY tb
		LIMIT 2000
		SETTINGS max_execution_time = 10, %s`, stepSec, source, mvQuantileMemSettings)
}

// opLatencyCovered — SAF: pencere başı MV kapsamında ve TTL içinde mi?
func opLatencyCovered(from, coverageStart, now time.Time) bool {
	return !from.Before(coverageStart) && !from.Before(now.Add(-opLatencyTTL))
}

// OperationLatencySeries — operasyonun p50/p99 + çağrı/hata serisi, stepSec (60'ın
// katı) kovalarında, spanmetrics_1m'den. Boş kovalar dönmez.
func (s *Store) OperationLatencySeries(ctx context.Context, service, op string, from, to time.Time, stepSec int) ([]OpLatencyPoint, error) {
	if stepSec < 60 || stepSec%60 != 0 {
		return nil, fmt.Errorf("op latency: adım %d s 60'ın katı değil", stepSec)
	}
	if !opLatencyCovered(from, s.spanmetricsCoverageStart(ctx), time.Now()) {
		return nil, ErrOpLatencyNotCovered
	}
	sql := s.weighSQL(opLatencySeriesSQL(s.spanmetricsSourceFor("spanmetrics_1m"), stepSec), from, "spanmetrics_1m") // v0.10.2435 — sampling-weight Phase 2
	rows, err := s.conn.Query(ctx, sql,
		service, op, from.Truncate(time.Minute), to)
	if err != nil {
		return nil, fmt.Errorf("op latency (spanmetrics_1m): %w", err)
	}
	defer rows.Close()
	var out []OpLatencyPoint
	for rows.Next() {
		var p OpLatencyPoint
		var qs []float64
		if err := rows.Scan(&p.TS, &p.Calls, &p.Errors, &qs); err != nil {
			return nil, err
		}
		if len(qs) == 4 {
			p.P50Ms, p.P99Ms = safeF(&qs[0]), safeF(&qs[3])
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
