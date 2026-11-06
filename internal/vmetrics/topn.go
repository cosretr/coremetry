package vmetrics

// topn.go — v0.10.1838 (Metrics Explorer Faz 1 dilim 1.4, audit §3.1 satır
// 11 "sunucu tarafı deterministik kesme", §8 S5 varsayılan 8 / 8·20·50).
//
// Server-side top-N on VictoriaMetrics compiles to the MetricsQL topk_* family
// (docs.victoriametrics.com/victoriametrics/metricsql/ — aggregate functions,
// verified against the docs 2026-11-06, NOT against a live VM):
//
//	rank=avg  → topk_avg(k, q)   biggest average over the range
//	rank=max  → topk_max(k, q)   biggest maximum
//	rank=last → topk_last(k, q)  biggest last value
//
// Two-argument form only. The optional third argument
// ("other_label=other_value", a sum-of-the-rest series) is NOT used: the
// dashboard's "others" fold is a client concern with its own tail contract,
// and a synthetic series in the middle of the result would be counted as a
// real one by every consumer (audit §9 komut 10 stays open).
//
// DETERMINISM. "Same query twice → same N series" is ours to guarantee, and
// VM cannot: its topk_* implementation sorts by value only, so equal values
// (an all-zero error counter fleet is the common case) come back in storage
// order. We therefore OVERFETCH — ask VM for topkFetch(n) = min(2n, 100)
// series — and do the final cut in Go with RankSeries (rank value desc, then
// group key asc). A tie at position n is then resolved by our tie-break as
// long as the tie group fits inside the overfetched window; the remaining
// non-determinism (a tie spanning past 2n) is VM's and is documented, not
// hidden.
//
// TOTAL. When VM returns fewer than topkFetch(n) series, that IS every series
// the query has (topk returns all when fewer exist) — no extra query. Only
// when the overfetch window is full do we run ONE bounded count(q) range query
// and take its maximum point: the peak number of concurrently present series
// in the window. A series that appeared and vanished between steps is not
// double-counted, so on churny label sets this is a LOWER bound; meta reports
// it as seriesTotal and the FE wording "N seriden M'i" stays true ("at least").

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/promapi"
)

// Rank names (wire values of ?rank=).
const (
	RankAvg  = "avg"
	RankMax  = "max"
	RankLast = "last"
)

// topkFetchMax — the overfetch ceiling; 2 × the hard limit 50.
const topkFetchMax = 100

// TopkFetch — SAF: how many series VM is asked for when the operator wants n.
func TopkFetch(n int) int {
	k := 2 * n
	if k > topkFetchMax {
		k = topkFetchMax
	}
	if k < n {
		k = n
	}
	return k
}

// TopkExpr — SAF: the MetricsQL wrapper. The inner expression is
// parenthesised so a binary operator at its top level binds inside the
// wrapper's argument, not around it.
func TopkExpr(rank string, k int, expr string) string {
	fn := "topk_avg"
	switch rank {
	case RankMax:
		fn = "topk_max"
	case RankLast:
		fn = "topk_last"
	}
	return fmt.Sprintf("%s(%d, (%s))", fn, k, expr)
}

// rankValue — SAF: the per-series ranking value. No points → -Inf (ranks
// last, never first).
func rankValue(pts []chstore.SpanMetricPoint, rank string) float64 {
	if len(pts) == 0 {
		return math.Inf(-1)
	}
	switch rank {
	case RankMax:
		m := math.Inf(-1)
		for _, p := range pts {
			if p.Value > m {
				m = p.Value
			}
		}
		return m
	case RankLast:
		last := pts[0]
		for _, p := range pts[1:] {
			if p.Time >= last.Time {
				last = p
			}
		}
		return last.Value
	}
	sum := 0.0
	for _, p := range pts {
		sum += p.Value
	}
	return sum / float64(len(pts))
}

// RankSeries — SAF: a sorted COPY, rank value descending, ties by group key
// ascending (joined with U+001F, which no label value carries). Input order
// never influences the output — the property "same query twice → same N"
// rests on.
func RankSeries(in []chstore.SpanMetricSeries, rank string) []chstore.SpanMetricSeries {
	type ranked struct {
		s   chstore.SpanMetricSeries
		v   float64
		key string
	}
	rs := make([]ranked, len(in))
	for i, s := range in {
		rs[i] = ranked{s: s, v: rankValue(s.Points, rank), key: strings.Join(s.GroupKey, "\x1f")}
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].v != rs[j].v {
			return rs[i].v > rs[j].v
		}
		return rs[i].key < rs[j].key
	})
	out := make([]chstore.SpanMetricSeries, len(rs))
	for i, r := range rs {
		out[i] = r.s
	}
	return out
}

// countSeriesRange — ONE bounded count(expr) range query; the max point.
// Recorded as ResultCount so meta never shows it as "the executed query".
func (s *Service) countSeriesRange(ctx context.Context, cfg Settings, expr string, from, to time.Time, step int) (int, error) {
	q := "count(" + expr + ")"
	params := url.Values{
		"query": {q},
		"start": {promTime(from)},
		"end":   {promTime(to)},
		"step":  {strconv.Itoa(step) + "s"},
	}
	res, err := promapi.QuerySeriesMeta(ctx, s.request("/api/v1/query_range", params, cfg))
	if err != nil {
		return 0, err
	}
	peak := 0
	for _, sr := range res.Series {
		for _, raw := range sr.Values {
			if _, v, ok := promapi.Sample(raw); ok && int(v) > peak {
				peak = int(v)
			}
		}
	}
	recordResult(ctx, ResultRecord{Kind: ResultCount, Query: q, StepSec: step, Total: peak, Partial: res.IsPartial})
	return peak, nil
}

// TopNResult — a cut result plus what it was cut from.
type TopNResult struct {
	Series []chstore.SpanMetricSeries
	// Total — every series the query has (exact when VM returned fewer than
	// the overfetch window, else the count() peak — a lower bound).
	Total int
	Note  string
}

// cutTopN — rank, then cut to n; total never below what VM returned.
func cutTopN(series []chstore.SpanMetricSeries, rank string, n, total int) ([]chstore.SpanMetricSeries, int) {
	ranked := RankSeries(series, rank)
	if total < len(ranked) {
		total = len(ranked)
	}
	if len(ranked) > n {
		ranked = ranked[:n]
	}
	return ranked, total
}

// QueryMetricTopN — the builder query, top n by rank, server-side.
func (s *Service) QueryMetricTopN(ctx context.Context, f chstore.MetricQueryFilter, n int, rank string) (TopNResult, error) {
	cfg, err := s.ready()
	if err != nil {
		return TopNResult{}, err
	}
	f = normalizeQueryWindow(f)
	q, err := buildPromQL(f, promOptions(cfg))
	if err != nil {
		return TopNResult{}, err
	}
	k := TopkFetch(n)
	d, err := s.runRangeQueryDetailed(ctx, cfg, TopkExpr(rank, k, q), f)
	if err != nil {
		return TopNResult{}, err
	}
	total := len(d.Series)
	if len(d.Series) >= k {
		if c, cerr := s.countSeriesRange(ctx, cfg, q, f.From, f.To, d.Step); cerr == nil {
			total = c
		}
	}
	out, total := cutTopN(d.Series, rank, n, total)
	return TopNResult{Series: out, Total: total, Note: emptyResultNote(f, len(out))}, nil
}

// QueryPromQLRangeTopN — the operator's MetricsQL, top n by rank.
func (s *Service) QueryPromQLRangeTopN(ctx context.Context, query string, from, to time.Time, stepSeconds, maxDataPoints, n int, rank string) (TopNResult, error) {
	cfg, err := s.ready()
	if err != nil {
		return TopNResult{}, err
	}
	q := strings.TrimSpace(query)
	k := TopkFetch(n)
	series, err := s.QueryPromQLRange(ctx, TopkExpr(rank, k, q), from, to, stepSeconds, maxDataPoints)
	if err != nil {
		return TopNResult{}, err
	}
	total := len(series)
	if len(series) >= k {
		if to.IsZero() {
			to = time.Now()
		}
		if from.IsZero() {
			from = to.Add(-time.Hour)
		}
		if c, cerr := s.countSeriesRange(ctx, cfg, "("+q+")", from, to, promStep(from, to, stepSeconds, maxDataPoints)); cerr == nil {
			total = c
		}
	}
	out, total := cutTopN(series, rank, n, total)
	return TopNResult{Series: out, Total: total}, nil
}
