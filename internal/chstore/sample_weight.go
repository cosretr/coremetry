package chstore

// sample_weight.go — v0.10.2430: spans.sample_weight probe + the admin
// card's stored-distribution read (docs/architecture/sampling-weight.md).

import (
	"context"
	"fmt"
	"log"
	"time"
)

// reprobeSampleWeightCol — metadata probe (the op_group v0.10.834 shape: a
// transient error reads "unknown" → false, INSERT stays column-less).
func (s *Store) reprobeSampleWeightCol(ctx context.Context) {
	target, err := s.resolvedStorageName(ctx, "spans")
	if err == nil {
		var present bool
		present, err = s.columnPresentAllHosts(ctx, target, "sample_weight")
		if err == nil && present {
			if !s.hasSampleWeightCol.Swap(true) {
				log.Printf("[chstore] `sample_weight` present on %s — span INSERT writes it", target)
			}
			return
		}
	}
	s.hasSampleWeightCol.Store(false)
	log.Printf("[chstore] `sample_weight` not present on spans storage (err: %v) — INSERT omits it; rows read DEFAULT 1", err)
}

// HasSampleWeightCol — admin card: is the weight being stored at all.
func (s *Store) HasSampleWeightCol() bool { return s.hasSampleWeightCol.Load() }

// StoredWeightRow — one weight value in the stored window.
type StoredWeightRow struct {
	Weight float64 `json:"weight"`
	Spans  uint64  `json:"spans"`
}

// sampleWeightDistSQL — the window is short (≤ 15 min) and the read touches
// one near-constant Float32 column: a verification read, opt-in from the
// admin card (the RawSpanCounts precedent, trace_health_raw.go). Not an
// aggregate surface — Phase 2 puts weighted sums into the span MVs.
const sampleWeightDistSQL = `
	SELECT toFloat64(sample_weight) AS w, count() AS n
	FROM spans
	WHERE time >= toDateTime64(?, 9, 'UTC') AND time < toDateTime64(?, 9, 'UTC')
	GROUP BY w
	ORDER BY n DESC
	LIMIT 50
	SETTINGS max_execution_time = 10`

// MaxSampleWeightWindow — cap for the stored-distribution read.
const MaxSampleWeightWindow = 15 * time.Minute

// StoredSampleWeights — weight distribution of spans stored in [from, to).
func (s *Store) StoredSampleWeights(ctx context.Context, from, to time.Time) ([]StoredWeightRow, error) {
	if !s.hasSampleWeightCol.Load() {
		return nil, fmt.Errorf("spans.sample_weight column not present")
	}
	if to.Sub(from) > MaxSampleWeightWindow {
		from = to.Add(-MaxSampleWeightWindow)
	}
	rows, err := s.telemetryReadConn().Query(ctx, sampleWeightDistSQL, chDateTime64Arg(from), chDateTime64Arg(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StoredWeightRow{}
	for rows.Next() {
		var r StoredWeightRow
		if err := rows.Scan(&r.Weight, &r.Spans); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
