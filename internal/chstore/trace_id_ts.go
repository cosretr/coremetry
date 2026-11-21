package chstore

// trace_id_ts.go — trace açılışının pencere araması (2026-11-21, operatör
// onaylı "Başla"; OTel collector ClickHouse exporter'ının
// <traces>_trace_id_ts tablosunun emsali).
//
// TraceWindow (v0.10.690) pencereyi trace_summary_5m'den buluyordu. O MV'nin
// ORDER BY'ı (time_bucket, trace_id): trace_id ile arama PK önekini
// KULLANAMAZ, 24 sa → 7 g → 90 g kademeleri her adımda o pencerenin TÜM
// trace_id kolonunu tarar (prod'da v0.9.578'in 3 sn zaman aşımları).
// trace_id_ts ORDER BY (trace_id, time_bucket) — arama tek PK aralığıdır:
// partisyon başına bir granül (index_granularity 2048), ıska 0 satır.
//
// Rollout: MV geriye DOLMAZ. İki kapı birlikte okuru doğru tutar:
//   - satır yok (eski trace / tablo henüz yok / hata) → ok=false, çağıran
//     bugünkü kademeli yola düşer;
//   - satır var ama trace'in başlangıcı MV'nin ilk kovasından en az
//     traceAggSlack sonra DEĞİL → MV trace'in ilk span'lerini görmemiş
//     olabilir (min yanlış geç) → kullanılmaz (sourceCovers, 60 sn prob).

import (
	"context"
	"time"
)

const traceIDTsMV = "trace_id_ts"

// traceIDTsLookupSQL — GROUP'suz: ıskada minMerge 1970, maxMerge 0 döner ve
// traceTimeBound ok=false verir. trace_id PK önekinde → zaman sınırı
// gerekmez (spans değil; tavan yine max_execution_time).
const traceIDTsLookupSQL = `
		SELECT minMerge(start_state), toInt64(maxMerge(end_state)) * 1000000000
		FROM ` + traceIDTsMV + `
		WHERE trace_id = ?
		LIMIT 1
		SETTINGS max_execution_time = 3`

// traceIDTsBound — SAF: aramanın sonucunu pencereye çevirir. covered,
// trace başlangıcından traceAggSlack önceki an MV'nin kapsamında mı.
func traceIDTsBound(start time.Time, endNanos int64, covered func(time.Time) bool) (lo, hi time.Time, ok bool) {
	lo, hi, ok = traceTimeBound(start, endNanos)
	if !ok || !covered(start.Add(-traceAggSlack)) {
		return time.Time{}, time.Time{}, false
	}
	return lo, hi, true
}

// traceWindowFromIDTs — trace_id_ts nokta araması. ok=false → çağıran
// kendi kademeli yoluna düşer (hata dahil: tablo eski bir node'da henüz yok).
func (s *Store) traceWindowFromIDTs(ctx context.Context, traceID string) (lo, hi time.Time, ok bool) {
	var start time.Time
	var endNanos int64
	if err := s.telemetryReadConn().QueryRow(ctx, traceIDTsLookupSQL, traceID).Scan(&start, &endNanos); err != nil {
		return time.Time{}, time.Time{}, false
	}
	return traceIDTsBound(start, endNanos, func(from time.Time) bool {
		return s.sourceCovers(ctx, priorSrcTraceIDTs, from)
	})
}
