package chstore

// ml_trace_errors.go — v0.10.2018 (T-err, anomali tekilleştirme ek dilimi):
// ML `trace_error_count` dedektörünün iki CH okuması.
//
// Operatör (2026-11-12): collector HATALI trace'leri örneklemez (tamamı
// CH'de); tail sampling yalnız başarılı trace'lerde. Bu yüzden hata SPAN
// SAYISI CH'de tamdır, ama toplam çağrı / hata ORANI / gecikme CH'den
// hesaplanamaz (payda örneklenmiş). Burada yalnız MUTLAK hata sayısı okunur.
//
// MV-first: `spanmetrics_1m` (ORDER BY service_name, name, kind,
// status_code, http_route, time_bucket; error_state = countIfState,
// error_exemplar_state = argMaxIfState(trace_id, duration, status='error')).
// Ham `spans` OKUNMAZ; örnek trace kimliği de MV'nin exemplar state'inden.
// Giriş span'i ilkesi: kind IN ('server','consumer').

import (
	"context"
	"fmt"
	"time"
)

// mlTraceErrorsSQL — hata sayısı + en çok 3 örnek trace, servis × operasyon
// × hedef kova. İç sorgu dakika kovasında birleştirir (exemplar dakika
// başına bir), dış sorgu hedef kovaya toplar.
const mlTraceErrorsSQL = `
		SELECT service_name, name, toUnixTimestamp(toStartOfInterval(time_bucket, toIntervalSecond(?))) AS b,
		       sum(e) AS errors, arraySlice(groupArrayIf(tid, tid != ''), 1, 3) AS samples
		FROM (
			SELECT service_name, name, time_bucket,
			       countMerge(error_state)             AS e,
			       %s AS tid
			FROM %s
			WHERE time_bucket >= ? AND time_bucket < ?
			  AND status_code = 'error'
			  AND ` + EntrySpanKindsWhere + `
			GROUP BY service_name, name, time_bucket
		)
		GROUP BY service_name, name, b
		HAVING errors > 0
		ORDER BY errors DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`

// MLTraceErrorsCap — okuma tavanı (servis × operasyon × kova satırı).
const MLTraceErrorsCap = 20000

// MLTraceErrorRow — bir servis × operasyon × kova hata sayısı.
type MLTraceErrorRow struct {
	Service, Operation string
	Bucket             int64 // unix sn, hedef kova başı
	Errors             uint64
	Samples            []string // ≤ 3 örnek trace kimliği
}

// MLTraceErrors — [from, to) penceresinde hata span sayıları.
func (s *Store) MLTraceErrors(ctx context.Context, from, to time.Time, bucketSec int) ([]MLTraceErrorRow, error) {
	_, tidExpr := smExemplarMerges("spanmetrics_1m")
	q := fmt.Sprintf(mlTraceErrorsSQL, tidExpr, s.spanmetricsSourceFor("spanmetrics_1m"))
	q = s.weighSQL(q, from, "spanmetrics_1m") // v0.10.2452 — sampling weight
	rows, err := s.telemetryReadConn().Query(ctx, q, bucketSec, from.UTC(), to.UTC(), MLTraceErrorsCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLTraceErrorRow
	for rows.Next() {
		var r MLTraceErrorRow
		var b uint32
		if err := rows.Scan(&r.Service, &r.Operation, &b, &r.Errors, &r.Samples); err != nil {
			return nil, err
		}
		r.Bucket = int64(b)
		out = append(out, r)
	}
	return out, rows.Err()
}

// mlTraceErrorMinutesSQL — v0.10.2223 (H7): tek servis × operasyonun dakika
// başına hata span sayısı (olay kaydı snapshot'ı). Aynı MV, aynı süzgeç;
// ORDER BY öneki (service_name, name) → dar okuma.
const mlTraceErrorMinutesSQL = `
		SELECT toUnixTimestamp(time_bucket) AS t, countMerge(error_state) AS e
		FROM %s
		WHERE service_name = ? AND name = ?
		  AND time_bucket >= ? AND time_bucket < ?
		  AND status_code = 'error'
		  AND ` + EntrySpanKindsWhere + `
		GROUP BY t
		ORDER BY t
		LIMIT ?
		SETTINGS max_execution_time = 10`

// MLTraceErrorMinutesCap — dakika satırı tavanı (≈ 12 sa).
const MLTraceErrorMinutesCap = 720

// MLTraceErrorMinutes — [from, to) penceresinde dakika başına hata span
// sayısı (Time = dakika başı, unix ns). Hatasız dakika satır üretmez (= 0).
func (s *Store) MLTraceErrorMinutes(ctx context.Context, service, operation string, from, to time.Time) ([]SpanMetricPoint, error) {
	q := fmt.Sprintf(mlTraceErrorMinutesSQL, s.spanmetricsSourceFor("spanmetrics_1m"))
	q = s.weighSQL(q, from, "spanmetrics_1m") // v0.10.2452
	rows, err := s.telemetryReadConn().Query(ctx, q, service, operation, from.UTC(), to.UTC(), MLTraceErrorMinutesCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpanMetricPoint
	for rows.Next() {
		var t uint32
		var e uint64
		if err := rows.Scan(&t, &e); err != nil {
			return nil, err
		}
		out = append(out, SpanMetricPoint{Time: int64(t) * 1e9, Value: float64(e)})
	}
	return out, rows.Err()
}
