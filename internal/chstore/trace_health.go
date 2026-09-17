package chstore

// trace_health.go — v0.10.757 "Trace hattı sağlığı" paneli (trace bütünlüğü
// denetimi Q1/Q2/Q3 özet ölçüleri; operatör onayı 2026-09-17). Üç okuma,
// hepsi MV üzerinde ve sınırlı:
//   - StoredSpanBuckets: service_summary_5m'den 5 dk kovası başına saklanan
//     span (kabul-edilen pod sayaçlarının CH tarafı; mutabakat pod-içi).
//   - OperationNameQuality: operation_summary_5m'den çıplak HTTP fiili adlı
//     span payı, boş ad, servis başına ayrık ad sayısı (Q3).
//   - TraceMVGapDayList: MV kapsam haritasındaki boş günler (Q1).

import (
	"context"
	"sort"
	"time"
)

// StoredSpanBucket — 5 dk kovası.
type StoredSpanBucket struct {
	TimeNs int64  `json:"t"`
	Spans  uint64 `json:"spans"`
}

// storedSpanBucketsSQL — SAF: iki zaman sınırı, GROUP BY kova, bütçe.
func storedSpanBucketsSQL() string {
	return `
		SELECT time_bucket, countMerge(span_count_state) AS spans
		FROM service_summary_5m
		WHERE time_bucket >= toDateTime(?, 'UTC') AND time_bucket < toDateTime(?, 'UTC')
		GROUP BY time_bucket
		ORDER BY time_bucket
		LIMIT 2000
		SETTINGS max_execution_time = 5`
}

// StoredSpanBuckets — [from, to) penceresinde kova başına saklanan span.
func (s *Store) StoredSpanBuckets(ctx context.Context, from, to time.Time) ([]StoredSpanBucket, error) {
	rows, err := s.telemetryReadConn().Query(ctx, storedSpanBucketsSQL(), from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StoredSpanBucket{}
	for rows.Next() {
		var t time.Time
		var n uint64
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out = append(out, StoredSpanBucket{TimeNs: t.UnixNano(), Spans: n})
	}
	return out, rows.Err()
}

// OperationNameQuality — Q3 ölçüleri (24 sa, operation_summary_5m).
type OperationNameQuality struct {
	TotalSpans      uint64                   `json:"totalSpans"`
	BareMethodSpans uint64                   `json:"bareMethodSpans"` // ad yalnız HTTP fiili ("POST")
	EmptyNameSpans  uint64                   `json:"emptyNameSpans"`
	DistinctNames   uint64                   `json:"distinctNames"`
	TopCardinality  []ServiceNameCardinality `json:"topCardinality"` // servis başına ayrık ad, ilk 10
}

// ServiceNameCardinality — gömülü id'li adların işareti: ayrık ad sayısı
// yüksek servis.
type ServiceNameCardinality struct {
	Service       string `json:"service"`
	DistinctNames uint64 `json:"distinctNames"`
}

// bareHTTPMethodRe — templater.httpMethods ile aynı küme (FE
// lib/opDisplayName ile de); tek yazım burada SQL'e gömülü.
const bareHTTPMethodRe = `^(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|TRACE|CONNECT)$`

// operationNameQualitySQL — SAF: iç GROUP BY name (ayrık ad ~10k), dış
// toplamlar; tek zaman sınırı (since), bütçe.
func operationNameQualitySQL() string {
	return `
		SELECT sum(spans) AS total,
		       sumIf(spans, match(name, '` + bareHTTPMethodRe + `')) AS bare,
		       sumIf(spans, name = '') AS empty_name,
		       count() AS distinct_names
		FROM (
			SELECT name, countMerge(span_count_state) AS spans
			FROM operation_summary_5m
			WHERE time_bucket >= toDateTime(?, 'UTC')
			GROUP BY name
		)
		SETTINGS max_execution_time = 10`
}

// operationNameCardinalitySQL — SAF: servis başına ayrık ad, ilk 10.
func operationNameCardinalitySQL() string {
	return `
		SELECT service_name, uniqExact(name) AS n
		FROM operation_summary_5m
		WHERE time_bucket >= toDateTime(?, 'UTC')
		GROUP BY service_name
		ORDER BY n DESC
		LIMIT 10
		SETTINGS max_execution_time = 10`
}

// OperationNameQuality — since'ten bu yana.
func (s *Store) OperationNameQuality(ctx context.Context, since time.Time) (OperationNameQuality, error) {
	var q OperationNameQuality
	if err := s.telemetryReadConn().QueryRow(ctx, operationNameQualitySQL(), since.Unix()).
		Scan(&q.TotalSpans, &q.BareMethodSpans, &q.EmptyNameSpans, &q.DistinctNames); err != nil {
		return q, err
	}
	rows, err := s.telemetryReadConn().Query(ctx, operationNameCardinalitySQL(), since.Unix())
	if err != nil {
		return q, err
	}
	defer rows.Close()
	q.TopCardinality = []ServiceNameCardinality{}
	for rows.Next() {
		var c ServiceNameCardinality
		if err := rows.Scan(&c.Service, &c.DistinctNames); err != nil {
			return q, err
		}
		q.TopCardinality = append(q.TopCardinality, c)
	}
	return q, rows.Err()
}

// TraceMVGapDayList — MV kapsam haritasındaki boş günler, sıralı (yyyy-mm-dd).
func (s *Store) TraceMVGapDayList(ctx context.Context) []string {
	gaps := s.traceMVGapDays(ctx)
	out := make([]string, 0, len(gaps))
	for d, gap := range gaps {
		if gap {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}
