package chstore

// trace_preview.go — v0.10.1883 (Log Search Faz 3 dilim 3.4; audit §4 "Toplu
// önizleme okuması"). /logs sayfasındaki trace çiplerinin TEK ClickHouse
// sorgusu: trace_summary_5m (MV; ORDER BY (time_bucket, trace_id)), dar zaman
// penceresi + `trace_id IN (…)`, GROUP BY trace_id, LIMIT + max_execution_time.
//
// Neden MV, ham spans değil: spans'taki trace bloom filtresi 100 kimlikte
// bloğu ~%63 olasılıkla geçirir (audit §4 hesap); MV'de zaman tabanı budar
// (v0.9.278 ölçümü: 1,15M → 99K okunan satır). Servis SAYISI MV'de yok (dilim
// 3.9) — çip göstermez, uydurmaz.

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// TracePreviewMaxIDs — tek istekteki kimlik tavanı (audit §4: ≤200).
const TracePreviewMaxIDs = 200

// tracePreviewSQL — SAF: sorgu metni (test pinler: MV, zaman tabanı, LIMIT, tavan).
func (s *Store) tracePreviewSQL(n int) string {
	holders := strings.TrimSuffix(strings.Repeat("?,", n), ",")
	return `
		SELECT trace_id,
		       ` + s.traceDisplaySvcExpr() + `                      AS root_service,
		       argMaxIfMerge(root_name_state)                       AS root_name,
		       toUnixTimestamp64Nano(minMerge(trace_start_state))   AS start_ns,
		       toInt64(maxMerge(trace_end_state))                   AS end_ns,
		       countMerge(span_count_state)                         AS span_count,
		       countMerge(error_count_state)                        AS error_count
		FROM trace_summary_5m
		WHERE time_bucket >= ? AND time_bucket <= ? AND trace_id IN (` + holders + `)
		GROUP BY trace_id
		LIMIT ` + strconv.Itoa(TracePreviewMaxIDs) + `
		SETTINGS max_execution_time = 3`
}

// TracePreviews — kimlik → özet (bulunanlar). Kimlikler küçük harf 32-hex
// olmalı (çağıran normalize eder); tavan üstü kırpılır. Pencere [from, to]
// 5 dk kovaya hizalı olmalı (çağıran).
func (s *Store) TracePreviews(ctx context.Context, ids []string, from, to time.Time) (map[string]TraceAggregateStub, error) {
	out := map[string]TraceAggregateStub{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > TracePreviewMaxIDs {
		ids = ids[:TracePreviewMaxIDs]
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, from.UTC(), to.UTC())
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.conn.Query(ctx, s.tracePreviewSQL(len(ids)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var st TraceAggregateStub
		var startNs, endNs int64
		if err := rows.Scan(&id, &st.RootService, &st.RootName, &startNs, &endNs, &st.SpanCount, &st.ErrorCount); err != nil {
			return nil, err
		}
		if st.SpanCount == 0 {
			continue
		}
		st.StartTimeNs, st.EndTimeNs = startNs, endNs
		st.DurationMs = float64(endNs-startNs) / 1e6
		if st.DurationMs < 0 {
			st.DurationMs = 0
		}
		out[id] = st
	}
	return out, rows.Err()
}
