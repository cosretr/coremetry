package vmetrics

// result_record.go — v0.10.1835 (Metrics Explorer Faz 1 dilim 1.1, audit
// docs/metrics/metrics-explore-dashboards-audit.md §3.1 satır 11-12).
//
// The HTTP metric surfaces need the NON-DATA half of a VictoriaMetrics answer —
// the MetricsQL actually sent, the step, the 1000-series backstop
// (promapi.MaxSeriesParsed) and VM's `isPartial` flag. Until this release only
// the MCP tools saw it (QueryMetricDetailed, investigate.go); the handlers
// called the chstore-identical seam methods, which return series only, so a
// truncated or partial answer reached the chart looking complete.
//
// Why a context recorder and not wider seam methods: the seam signatures are
// copied from *chstore.Store on purpose (drift = compile error). The
// QueryRecorder (query_record.go, v0.10.1271) already solved "read the
// executed expression without changing the signature" this way; this is the
// same pattern carrying the flags too. No recorder on the context → no-op
// (zero cost on every other caller: evaluator, MCP, Overview mappers).
//
// Only the query text and counters are recorded — never auth headers or
// tokens.

import (
	"context"
	"sync"
)

// Result kinds.
const (
	ResultRange     = "range"
	ResultInstant   = "instant"
	ResultHistogram = "histogram"
	// ResultCount — v0.10.1838 conditional series-total probe. Recorded so
	// the caller can tell it apart from the query it sized; never shown as
	// "the executed query".
	ResultCount = "count"
)

// ResultRecord — one VM read inside a request.
type ResultRecord struct {
	Kind  string
	Query string
	// StepSec — the step sent on the wire (0 for instant reads).
	StepSec int
	// Total — series VM returned BEFORE the MaxSeriesParsed backstop.
	Total int
	// Truncated — Total > promapi.MaxSeriesParsed; the decoded set is cut.
	Truncated bool
	// Partial — VM `isPartial:true` (some vmstorage nodes did not answer).
	Partial bool
}

// ResultRecorder collects ResultRecords for one request. Safe for concurrent
// use (a bundle fans slots out in goroutines, each with its own recorder, but
// the type does not rely on that).
type ResultRecorder struct {
	mu   sync.Mutex
	recs []ResultRecord
}

// Records — a copy, in execution order.
func (r *ResultRecorder) Records() []ResultRecord {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ResultRecord(nil), r.recs...)
}

type resultRecorderKey struct{}

// WithResultRecorder attaches r to ctx.
func WithResultRecorder(ctx context.Context, r *ResultRecorder) context.Context {
	return context.WithValue(ctx, resultRecorderKey{}, r)
}

func recordResult(ctx context.Context, rec ResultRecord) {
	r, _ := ctx.Value(resultRecorderKey{}).(*ResultRecorder)
	if r == nil {
		return
	}
	r.mu.Lock()
	r.recs = append(r.recs, rec)
	r.mu.Unlock()
}
