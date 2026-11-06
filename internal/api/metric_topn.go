package api

// metric_topn.go — v0.10.1838 (Metrics Explorer Faz 1 dilim 1.4; audit §3.1
// satır 11, §8 S5: varsayılan 8, seçenek 8/20/50, tavan 50). Behaviour
// change approved by the operator (audit §11 "Tamamdır").
//
//	?limit=8|20|50&rank=avg|max|last   on /api/metrics/query, /promql
//	{"limit":…, "rank":…}              on a bundle metric request
//
// OPT-IN: a request with neither param is answered exactly as before (same
// key, same body). The default 8 applies only once the caller opts in — a
// `rank` without `limit`, or a `limit` that is empty / not a number. That is
// the audit's default for the Faz 2 query row; no caller in the tree sends
// either param today, so no existing surface changes in this release.
//
// Limits snap UP to the next rung (12 → 20) and clamp at 50: the rung set is
// the cache-key cardinality bound, and an arbitrary `limit=37` would fork the
// key per keystroke of a future custom field.
//
// VictoriaMetrics: topk_* MetricsQL wrapper + Go-side deterministic cut
// (vmetrics/topn.go explains the overfetch and the conditional count()).
// ClickHouse: the existing builder SQL is unchanged and the rank + cut run in
// Go over its (already 50 000-row bounded) result — the SAME RankSeries, so
// the two backends order identically. Pushing an ORDER BY … LIMIT into
// buildMetricQuerySQL would need a per-series aggregate over the bucketed
// sub-select AND the rollup tiers (tryMetricRollupRoute, queryMetricRollup)
// to grow it too; ranking ≤ 50 000 points in Go costs microseconds and keeps
// one ranking definition. Capabilities says {topN: supported, mode}.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

const (
	topNDefault = 8
	topNMax     = 50
)

// topNRungs — the allowed limits, ascending.
var topNRungs = []int{8, 20, 50}

// topNSpec — Limit 0 = top-N off (legacy behaviour).
type topNSpec struct {
	Limit int
	Rank  string
}

func (t topNSpec) on() bool { return t.Limit > 0 }

// keyTag — appended to a cache key ONLY when on, so every existing key is
// byte-identical (no cold wave on deploy).
func (t topNSpec) keyTag() string {
	if !t.on() {
		return ""
	}
	return fmt.Sprintf(":topn=%d:%s", t.Limit, t.Rank)
}

// parseTopN — SAF (metric_topn_test.go). Bad rank → errBadRequest (400).
func parseTopN(limitRaw, rankRaw string, limitSet, rankSet bool) (topNSpec, error) {
	if !limitSet && !rankSet {
		return topNSpec{}, nil
	}
	rank := strings.ToLower(strings.TrimSpace(rankRaw))
	switch rank {
	case "":
		rank = vmetrics.RankAvg
	case vmetrics.RankAvg, vmetrics.RankMax, vmetrics.RankLast:
	default:
		return topNSpec{}, fmt.Errorf("%w: rank=%q geçersiz — avg, max ya da last", errBadRequest, clampParamValue(rankRaw, 16))
	}
	n, err := strconv.Atoi(strings.TrimSpace(limitRaw))
	if err != nil || n <= 0 {
		n = topNDefault
	}
	limit := topNMax
	for _, r := range topNRungs {
		if n <= r {
			limit = r
			break
		}
	}
	return topNSpec{Limit: limit, Rank: rank}, nil
}

// parseTopNQuery — the URL form (presence matters: `?limit=` alone opts in).
func parseTopNQuery(q interface {
	Get(string) string
	Has(string) bool
}) (topNSpec, error) {
	return parseTopN(q.Get("limit"), q.Get("rank"), q.Has("limit"), q.Has("rank"))
}

// metricTopNSource — OPTIONAL seam capability (metricNoteSource pattern): a
// backend that can rank server-side. Only VictoriaMetrics implements it.
type metricTopNSource interface {
	QueryMetricTopN(ctx context.Context, f chstore.MetricQueryFilter, n int, rank string) (vmetrics.TopNResult, error)
	QueryPromQLRangeTopN(ctx context.Context, query string, from, to time.Time, stepSeconds, maxDataPoints, n int, rank string) (vmetrics.TopNResult, error)
}

// cutTopNGo — SAF: the ClickHouse half — rank the full result, cut to n.
func cutTopNGo(series []chstore.SpanMetricSeries, spec topNSpec) ([]chstore.SpanMetricSeries, int) {
	ranked := vmetrics.RankSeries(series, spec.Rank)
	total := len(ranked)
	if len(ranked) > spec.Limit {
		ranked = ranked[:spec.Limit]
	}
	return ranked, total
}

// metricRead — one builder read, top-N or not.
type metricRead struct {
	Series []chstore.SpanMetricSeries
	Note   string
	// Total — top-N only: the series count before the cut (0 when off).
	Total int
	// Capped — the CH 50 000-row cap was hit (computed on the FULL result,
	// before any cut: a cut result is always far below the cap).
	Capped bool
}

// readMetric — THE builder read for /api/metrics/query and the bundle's
// metric slot, so the two cannot drift (v0.9.566 class).
func readMetric(ctx context.Context, src metricSource, f chstore.MetricQueryFilter, spec topNSpec) (metricRead, error) {
	if spec.on() {
		if tn, ok := src.(metricTopNSource); ok {
			res, err := tn.QueryMetricTopN(ctx, f, spec.Limit, spec.Rank)
			if err != nil {
				return metricRead{}, err
			}
			return metricRead{Series: res.Series, Note: res.Note, Total: res.Total}, nil
		}
	}
	series, note, err := queryMetricNoted(ctx, src, f)
	if err != nil {
		return metricRead{}, err
	}
	out := metricRead{Series: series, Note: note, Capped: chstore.SeriesRowsCapped(series)}
	if spec.on() {
		out.Series, out.Total = cutTopNGo(series, spec)
	}
	return out, nil
}

// applyTopNMeta — top-N overrides the record-derived total: VM's records count
// the OVERFETCHED window, not the query's series. CH with the row cap hit has
// no honest total.
func applyTopNMeta(m *metricMeta, spec topNSpec, total int, capped bool) {
	if !spec.on() {
		return
	}
	m.Limit, m.Rank = spec.Limit, spec.Rank
	if capped {
		m.SeriesTotal = nil
		m.Truncated = true
		return
	}
	t := total
	m.SeriesTotal = &t
	m.Truncated = m.Truncated || total > m.SeriesShown
}
