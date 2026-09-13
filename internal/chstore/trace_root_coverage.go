package chstore

// trace_root_coverage.go — v0.10.712 (operatör prod ölçümü 2026-09-13: son
// 1 saatte 3.33M trace'in yalnız %51'inde TAM kök span var). Hangi giriş
// servisleri köksüz trace üretiyor? Kaynak trace_summary_5m'in iki state'i:
// root_service_state (parent_id boş + name dolu + service dolu = tam kök)
// ve entry_service_state (en erken server/consumer span'in servisi).
// GROUP BY trace_id pencere boyu koşar (MV, bucket-sınırlı): pencere ≤ 1 s
// ve tavanlı (25 s, spill ayarları); admin teşhisi, isteğe bağlı.

import (
	"context"
	"time"
)

const (
	traceRootCoverageMaxRows = 200
	traceRootCoverageMaxExec = 25
)

// TraceRootCoverageRow — giriş servisi başına kök kapsaması. EntryService
// boş = trace'te hiç server/consumer span yok ("giriş servisi de yok").
type TraceRootCoverageRow struct {
	EntryService string `json:"entryService"`
	Traces       uint64 `json:"traces"`
	WithRoot     uint64 `json:"withRoot"`
}

// traceRootCoverageSQL — SAF (şekil testi): MV, iki zaman sınırı, GROUP BY
// trace_id → giriş servisi; köksüzü en çok üreten önce; LIMIT + tavan.
func traceRootCoverageSQL() string {
	return `
		SELECT entry, count() AS traces, countIf(has_root) AS with_root
		FROM (
		  SELECT trace_id,
		         argMaxIfMerge(root_service_state) != '' AS has_root,
		         argMinIfMerge(entry_service_state)       AS entry
		  FROM trace_summary_5m
		  WHERE time_bucket >= ? AND time_bucket < ?
		  GROUP BY trace_id
		)
		GROUP BY entry
		ORDER BY traces - with_root DESC, traces DESC
		LIMIT ` + itoa(traceRootCoverageMaxRows) + `
		SETTINGS max_execution_time = ` + itoa(traceRootCoverageMaxExec) + `, ` + tracesSpillSettings
}

// traceRootCoverageRawSQL — v0.10.713 (operatör: "1 dakika olabilir mi").
// 5 dk kovaları 1 dakikayı ifade edemez; kısa pencere ham spans'ten AYNI
// tanımla okunur: tam kök = parent boş + ad dolu + servis dolu; giriş =
// en erken server/consumer span'in servisi ('unknown'/boş dışarıda).
// Pencere ≤ 5 dk olduğundan tarama sınırlı; aynı LIMIT/tavan/spill.
func traceRootCoverageRawSQL() string {
	return `
		SELECT entry, count() AS traces, countIf(has_root) AS with_root
		FROM (
		  SELECT trace_id,
		         max((parent_id = '' OR parent_id = '0000000000000000') AND name != '' AND service_name != '') AS has_root,
		         argMinIf(service_name, time,
		           (kind = 'server' OR kind = 'consumer') AND service_name != '' AND service_name != 'unknown') AS entry
		  FROM spans
		  WHERE time >= ? AND time < ?
		  GROUP BY trace_id
		)
		GROUP BY entry
		ORDER BY traces - with_root DESC, traces DESC
		LIMIT ` + itoa(traceRootCoverageMaxRows) + `
		SETTINGS max_execution_time = ` + itoa(traceRootCoverageMaxExec) + `, ` + tracesSpillSettings
}

// traceRootCoverageUsesRaw — SAF: 5 dk altı pencere ham spans (MV kovası
// 5 dk); 5 dk ve üstü MV.
func traceRootCoverageUsesRaw(rangeS int) bool { return rangeS < 300 }

// traceRootCoverageWindow — SAF: [now−range, now). MV yolunda alt uç 5 dk
// grid'e kırpılır; ham yolda tam pencere (1 dk = 1 dk). range 1 dk..1 sa
// kelepçeli (pencere GROUP BY trace_id'nin maliyetini belirler — prod'da
// 1 saat ≈ 3.3M grup).
func traceRootCoverageWindow(now time.Time, rangeS int) (from, to time.Time) {
	if rangeS < 60 {
		rangeS = 60
	}
	if rangeS > 3600 {
		rangeS = 3600
	}
	to = now.UTC()
	from = to.Add(-time.Duration(rangeS) * time.Second)
	if !traceRootCoverageUsesRaw(rangeS) {
		from = from.Truncate(5 * time.Minute)
	}
	return from, to
}

// TraceRootCoverage — son rangeS için giriş servisi başına kök kapsaması.
// Döndürülen source: "spans" (kısa pencere) | "mv".
func (s *Store) TraceRootCoverage(ctx context.Context, rangeS int) ([]TraceRootCoverageRow, string, error) {
	from, to := traceRootCoverageWindow(time.Now(), rangeS)
	sql, source := traceRootCoverageSQL(), "mv"
	if traceRootCoverageUsesRaw(rangeS) {
		sql, source = traceRootCoverageRawSQL(), "spans"
	}
	rows, err := s.telemetryReadConn().Query(ctx, sql, from, to)
	if err != nil {
		return nil, source, err
	}
	defer rows.Close()
	out := []TraceRootCoverageRow{}
	for rows.Next() {
		var r TraceRootCoverageRow
		if err := rows.Scan(&r.EntryService, &r.Traces, &r.WithRoot); err != nil {
			return nil, source, err
		}
		out = append(out, r)
	}
	return out, source, rows.Err()
}
