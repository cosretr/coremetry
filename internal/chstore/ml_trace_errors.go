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
			       argMaxIfMerge(error_exemplar_state) AS tid
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
	q := fmt.Sprintf(mlTraceErrorsSQL, s.spanmetricsSourceFor("spanmetrics_1m"))
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
