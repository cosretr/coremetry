package chstore

// ml_volume.go — v0.10.2242, anomali kartı Dilim B (docs/ml/anomaly-card-audit.md
// §10 B): ML telemetri hacmi dedektörünün (ml.svc_telemetry_volume) tek CH
// okuması. Servis × kova başına iki sayı, ikisi de MV'den (ham spans yok):
//
//   - spans:   service_summary_5m countMerge(span_count_state) — servisin
//     yazdığı TÜM span'ler;
//   - entries: spanmetrics_1m countMerge(calls_state), giriş span'i
//     (EntrySpanKindsWhere: server + consumer) — istek sayısı.
//
// Oran spans / entries "istek başına span"dır; tail sampling trace düzeyinde
// olduğu için iki sayı aynı oranda örneklenir, oran örneklemeden bağımsızdır.
// Giriş span'i olmayan serviste entries = 0 (LEFT JOIN, join_use_nulls=0).

import (
	"context"
	"fmt"
	"time"
)

// MLVolumeCap — okuma tavanı (servis × kova satırı).
const MLVolumeCap = 20000

// MLVolumeRow — bir servisin bir kovadaki span ve giriş span'i sayısı.
type MLVolumeRow struct {
	Service string
	Bucket  int64 // unix sn, kova başı
	Spans   uint64
	Entries uint64
}

// mlVolumeSQL — SAF (test): iki MV aynı kova hizasında birleşir. service
// boş = tüm servisler (tik); dolu = tek servis (olay kaydı, ORDER BY öneki).
func mlVolumeSQL(oneService bool) string {
	svc := "service_name != ''"
	if oneService {
		svc = "service_name = ?"
	}
	return fmt.Sprintf(`
		SELECT s.service_name, s.b, s.spans, e.entries
		FROM (
			SELECT service_name, toUnixTimestamp(toStartOfInterval(time_bucket, toIntervalSecond(?))) AS b,
			       toUInt64(countMerge(span_count_state)) AS spans
			FROM service_summary_5m
			WHERE time_bucket >= ? AND time_bucket < ? AND %[1]s
			GROUP BY service_name, b
		) AS s
		LEFT JOIN (
			SELECT service_name, toUnixTimestamp(toStartOfInterval(time_bucket, toIntervalSecond(?))) AS b,
			       toUInt64(countMerge(calls_state)) AS entries
			FROM spanmetrics_1m
			WHERE time_bucket >= ? AND time_bucket < ? AND %[1]s AND `+EntrySpanKindsWhere+`
			GROUP BY service_name, b
		) AS e ON s.service_name = e.service_name AND s.b = e.b
		ORDER BY s.service_name, s.b
		LIMIT ?
		SETTINGS max_execution_time = 15, join_use_nulls = 0`, svc)
}

// MLServiceVolume — [from, to) penceresinde servis × kova (bucketSec) span ve
// giriş span'i sayısı. service "" = tüm servisler.
func (s *Store) MLServiceVolume(ctx context.Context, service string, from, to time.Time, bucketSec int) ([]MLVolumeRow, error) {
	if bucketSec < 300 {
		bucketSec = 300 // service_summary_5m çözünürlüğü
	}
	args := []any{bucketSec, from.UTC(), to.UTC()}
	if service != "" {
		args = append(args, service)
	}
	args = append(args, bucketSec, from.UTC(), to.UTC())
	if service != "" {
		args = append(args, service)
	}
	args = append(args, MLVolumeCap)
	sql := s.weighSQL(mlVolumeSQL(service != ""), from, "service_summary_5m", "spanmetrics_1m") // v0.10.2435 — sampling-weight Phase 2
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLVolumeRow
	for rows.Next() {
		var r MLVolumeRow
		var b uint32
		if err := rows.Scan(&r.Service, &b, &r.Spans, &r.Entries); err != nil {
			return nil, err
		}
		r.Bucket = int64(b)
		out = append(out, r)
	}
	return out, rows.Err()
}
