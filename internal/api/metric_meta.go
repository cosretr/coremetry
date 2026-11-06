package api

// metric_meta.go — v0.10.1835 (Metrics Explorer Faz 1 dilim 1.1, audit
// docs/metrics/metrics-explore-dashboards-audit.md §3.1 satır 11-12, §4
// "dürüstlük satırı": "8 / 12 seri · adım 1 dk (otomatik) · 142 ms ·
// VictoriaMetrics").
//
// The `meta` envelope every metric read now carries — ADDITIVE: no legacy
// field changes (testdata/metric_meta_legacy golden tests). Shape:
//
//	{ "source": "vm" | "clickhouse",
//	  "executed": "<MetricsQL / PromQL actually run>",   // omitted when not cheap
//	  "tookMs": 142,                                     // backend time at COMPUTE
//	  "seriesTotal": 12,                                 // omitted when unknown
//	  "seriesShown": 8,
//	  "truncated": true, "partial": false,
//	  "stepSec": 60,                                     // omitted when unknown
//	  "note": "…" }                                      // omitted when empty
//
// Where each field comes from, per backend:
//
//   - VictoriaMetrics: vmetrics.ResultRecorder on the request context records
//     the expression sent, the step, VM's pre-cap series count, the
//     promapi.MaxSeriesParsed (1000) backstop and `isPartial`. These were
//     visible only to MCP (vmetrics/investigate.go) until now.
//   - ClickHouse: `executed` only where it is the operator's own text
//     (PromQL) — the builder SQL is assembled deep inside chstore with
//     export-interval probes and rollup routing, and echoing a guess would be
//     worse than omitting it. `truncated` = the 50 000-row cap
//     (SeriesRowsCapped / HistogramSeries.RowCapped); `seriesTotal` is known
//     only when nothing was cut. `stepSec` is OBSERVED from the returned
//     points (observedStepSec): the CH step is chosen inside the store
//     (auto ladder + export-cadence clamp) and not returned.
//
// `tookMs` is measured inside the serveCached compute closure, so a cache hit
// repeats the ORIGINAL compute time; the response's X-Cache header is what
// says "önbellekten" (audit §3.1 satır 12). Cache keys are UNCHANGED by this
// release: meta is optional on the client, so a pre-deploy body without it
// renders exactly as before — no rolling-deploy hazard to bump against.

import (
	"context"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// Wire values of meta.source. "clickhouse" rather than the internal "ch"
// marker: this one is read by people.
const (
	metaSourceVM = "vm"
	metaSourceCH = "clickhouse"
)

// metricMeta — see the file header for every field's provenance.
type metricMeta struct {
	Source      string `json:"source"`
	Executed    string `json:"executed,omitempty"`
	TookMs      int64  `json:"tookMs"`
	SeriesTotal *int   `json:"seriesTotal,omitempty"`
	SeriesShown int    `json:"seriesShown"`
	Truncated   bool   `json:"truncated"`
	Partial     bool   `json:"partial"`
	StepSec     int    `json:"stepSec,omitempty"`
	Note        string `json:"note,omitempty"`
	// Limit + Rank — v0.10.1838: the server-side top-N that produced this
	// body (metric_topn.go); omitted when the request did not opt in.
	Limit int    `json:"limit,omitempty"`
	Rank  string `json:"rank,omitempty"`
	// Cluster — v0.10.1839: the cluster scope APPLIED to this read
	// (metric_cluster_scope.go); omitted when none / not applicable.
	Cluster string `json:"cluster,omitempty"`
}

// metaSourceName — internal source marker → wire value.
func metaSourceName(srcName string) string {
	if srcName == metricSourceVM {
		return metaSourceVM
	}
	return metaSourceCH
}

// chMetaHints — what the ClickHouse path can say about itself.
type chMetaHints struct {
	Executed  string
	Truncated bool
	StepSec   int
}

// buildMetricMeta — SAF (metric_meta_test.go). recs are the VM reads the
// request made (nil on ClickHouse); shown is the series count in the body.
//
// VM: every non-count record contributes — executed lines joined in run
// order, flags OR'ed, totals summed (one record per builder/PromQL read in
// practice; the sum keeps a future multi-read honest instead of reporting
// only the first). The ResultCount probe never counts as "executed".
func buildMetricMeta(srcName string, recs []vmetrics.ResultRecord, took time.Duration, shown int, ch chMetaHints, note string) metricMeta {
	m := metricMeta{
		Source:      metaSourceName(srcName),
		TookMs:      took.Milliseconds(),
		SeriesShown: shown,
		Note:        note,
	}
	if srcName != metricSourceVM {
		m.Executed = ch.Executed
		m.Truncated = ch.Truncated
		m.StepSec = ch.StepSec
		if !ch.Truncated {
			total := shown
			m.SeriesTotal = &total
		}
		return m
	}
	var queries []string
	total, primary := 0, 0
	for _, r := range recs {
		if r.Kind == vmetrics.ResultCount {
			continue
		}
		primary++
		if r.Query != "" {
			queries = append(queries, r.Query)
		}
		total += r.Total
		m.Truncated = m.Truncated || r.Truncated
		m.Partial = m.Partial || r.Partial
		if m.StepSec == 0 {
			m.StepSec = r.StepSec
		}
	}
	m.Executed = strings.Join(queries, "\n")
	if primary > 0 {
		m.SeriesTotal = &total
	}
	return m
}

// observedStepSec — SAF: the smallest positive spacing between adjacent
// points of any series, in seconds; 0 when no series has two points. On a
// time-bucketed result that IS the bucket width as soon as one series has two
// neighbouring buckets; a sparse series can only make it larger, never
// smaller than the true step.
func observedStepSec(series []chstore.SpanMetricSeries) int {
	best := int64(0)
	for _, s := range series {
		for i := 1; i < len(s.Points); i++ {
			d := s.Points[i].Time - s.Points[i-1].Time
			if d > 0 && (best == 0 || d < best) {
				best = d
			}
		}
	}
	return int(best / int64(time.Second))
}

// metaRun — one compute's meta collection: the recorder rides ctx, the clock
// starts at begin.
type metaRun struct {
	src   string
	rec   *vmetrics.ResultRecorder
	start time.Time
}

// beginMetricMeta attaches a fresh recorder to ctx. Call INSIDE the
// serveCached closure (its ctx is the compute context; r.Context() would die
// with the SWR refresh — v0.8.319).
func beginMetricMeta(ctx context.Context, src metricSource) (context.Context, *metaRun) {
	m := &metaRun{src: src.Name(), rec: &vmetrics.ResultRecorder{}, start: time.Now()}
	return vmetrics.WithResultRecorder(ctx, m.rec), m
}

func (m *metaRun) finish(shown int, ch chMetaHints, note string) *metricMeta {
	out := buildMetricMeta(m.src, m.rec.Records(), time.Since(m.start), shown, ch, note)
	return &out
}

// histogramMetaShown — the heatmap's "series": one per le bucket.
func histogramMetaShown(h *chstore.HistogramSeries) int {
	if h == nil || len(h.Times) == 0 {
		return 0
	}
	return len(h.Bounds) + 1
}

// histogramResponse — the heatmap body + meta. Embedding a POINTER keeps the
// legacy fields flattened in their original order; meta is appended last.
type histogramResponse struct {
	*chstore.HistogramSeries
	Meta *metricMeta `json:"meta,omitempty"`
}

// promqlEnvelope — /api/metrics/promql?meta=1. Opt-in because the legacy
// body is a bare ARRAY, which cannot grow a member without breaking every
// caller that indexes it.
type promqlEnvelope struct {
	Series []chstore.SpanMetricSeries `json:"series"`
	Meta   *metricMeta                `json:"meta"`
}
