package vmetrics

// discovery.go — v0.10.1840 (Metrics Explorer Faz 1 dilim 1.6, audit §3.1
// satır 2-4 + 13, §3.2 maliyet/koruma tablosu).
//
// Four VictoriaMetrics surfaces the product never called before, each behind
// the ceilings audit §3.2 sets. The CEILINGS live in the API layer's pure
// clamp functions (metrics_discovery.go, tested); this file only speaks the
// wire and decodes. Response shapes are ASSUMED from the VM / Prometheus docs
// (listed in the Faz 1 report under "not verified"):
//
//	/api/v1/labels               {status, data:[string]}            (Prometheus)
//	/api/v1/label/<l>/values     {status, data:[string]}            (Prometheus)
//	/api/v1/series               {status, data:[{label:value}]}     (Prometheus; VM `limit`)
//	/api/v1/status/tsdb          {status, data:{totalSeries, totalLabelValuePairs,
//	                              seriesCountByMetricName, seriesCountByLabelName,
//	                              seriesCountByFocusLabelValue, seriesCountByLabelValuePair,
//	                              labelValueCountByLabelName}}     (VM; entries {name, value})
//	/api/v1/status/top_queries   {topN, maxLifetime, topByCount, topByAvgDuration,
//	                              topBySumDuration}                 (VM; NO status envelope)
//
// Every call is a GET with a bounded context set by the caller; the scope
// (extra_filters[]) rides requestCtx like every other read, except tsdb,
// whose documented scope arg is extra_label (the caller passes it).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/promapi"
)

// LabelNames — /api/v1/labels over [from,to], optional match[], VM `limit`.
func (s *Service) LabelNames(ctx context.Context, match string, from, to time.Time, limit int) ([]string, bool, error) {
	cfg, err := s.ready()
	if err != nil {
		return nil, false, err
	}
	p := url.Values{"start": {promTime(from)}, "end": {promTime(to)}, "limit": {strconv.Itoa(limit)}}
	if match != "" {
		p.Set("match[]", match)
	}
	res, err := promapi.QueryStringsMeta(ctx, s.requestCtx(ctx, "/api/v1/labels", p, cfg))
	if err != nil {
		return nil, false, err
	}
	return res.Values, res.IsPartial, nil
}

// LabelValues — /api/v1/label/<label>/values, optional match[], VM `limit`.
func (s *Service) LabelValues(ctx context.Context, label, match string, from, to time.Time, limit int) ([]string, bool, error) {
	cfg, err := s.ready()
	if err != nil {
		return nil, false, err
	}
	p := url.Values{"start": {promTime(from)}, "end": {promTime(to)}, "limit": {strconv.Itoa(limit)}}
	if match != "" {
		p.Set("match[]", match)
	}
	res, err := promapi.QueryStringsMeta(ctx, s.requestCtx(ctx, "/api/v1/label/"+url.PathEscape(label)+"/values", p, cfg))
	if err != nil {
		return nil, false, err
	}
	return res.Values, res.IsPartial, nil
}

// SeriesSample — /api/v1/series, match[] REQUIRED, VM `limit` (a sample:
// VM returns up to limit series, not a ranked set).
func (s *Service) SeriesSample(ctx context.Context, match string, from, to time.Time, limit int) ([]map[string]string, bool, error) {
	cfg, err := s.ready()
	if err != nil {
		return nil, false, err
	}
	p := url.Values{"match[]": {match}, "start": {promTime(from)}, "end": {promTime(to)}, "limit": {strconv.Itoa(limit)}}
	data, partial, err := promapi.QueryData(ctx, s.requestCtx(ctx, "/api/v1/series", p, cfg))
	if err != nil {
		return nil, false, err
	}
	var out []map[string]string
	if len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, false, fmt.Errorf("victoriametrics series decode: %w", err)
		}
	}
	if len(out) > limit {
		out = out[:limit] // defensive: a VM ignoring `limit` must not widen the sample
	}
	return out, partial, nil
}

// TSDBEntry — one {name, value} row of /api/v1/status/tsdb.
type TSDBEntry struct {
	Name  string `json:"name"`
	Value uint64 `json:"value"`
}

// TSDBStatus — the decoded /api/v1/status/tsdb data.
type TSDBStatus struct {
	TotalSeries                  uint64      `json:"totalSeries"`
	TotalLabelValuePairs         uint64      `json:"totalLabelValuePairs"`
	SeriesCountByMetricName      []TSDBEntry `json:"seriesCountByMetricName"`
	SeriesCountByLabelName       []TSDBEntry `json:"seriesCountByLabelName"`
	SeriesCountByFocusLabelValue []TSDBEntry `json:"seriesCountByFocusLabelValue"`
	SeriesCountByLabelValuePair  []TSDBEntry `json:"seriesCountByLabelValuePair"`
	LabelValueCountByLabelName   []TSDBEntry `json:"labelValueCountByLabelName"`
}

// TSDBRequest — the bounded tsdb query (the API layer clamps every field).
type TSDBRequest struct {
	TopN       int
	Date       string // YYYY-MM-DD ("" = VM default: today)
	FocusLabel string
	Match      string
	// ExtraLabel — "label=value" scope (tsdb documents extra_label, not
	// extra_filters[]).
	ExtraLabel string
}

// TSDBStats — /api/v1/status/tsdb. Arrays are cut to TopN defensively.
func (s *Service) TSDBStats(ctx context.Context, req TSDBRequest) (TSDBStatus, error) {
	cfg, err := s.ready()
	if err != nil {
		return TSDBStatus{}, err
	}
	p := url.Values{"topN": {strconv.Itoa(req.TopN)}}
	if req.Date != "" {
		p.Set("date", req.Date)
	}
	if req.FocusLabel != "" {
		p.Set("focusLabel", req.FocusLabel)
	}
	if req.Match != "" {
		p.Set("match[]", req.Match)
	}
	if req.ExtraLabel != "" {
		p.Set("extra_label", req.ExtraLabel)
	}
	data, _, err := promapi.QueryData(ctx, s.request("/api/v1/status/tsdb", p, cfg))
	if err != nil {
		return TSDBStatus{}, err
	}
	var st TSDBStatus
	if len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &st); err != nil {
			return TSDBStatus{}, fmt.Errorf("victoriametrics tsdb decode: %w", err)
		}
	}
	cut := func(e []TSDBEntry) []TSDBEntry {
		if e == nil {
			return []TSDBEntry{}
		}
		if len(e) > req.TopN {
			return e[:req.TopN]
		}
		return e
	}
	st.SeriesCountByMetricName = cut(st.SeriesCountByMetricName)
	st.SeriesCountByLabelName = cut(st.SeriesCountByLabelName)
	st.SeriesCountByFocusLabelValue = cut(st.SeriesCountByFocusLabelValue)
	st.SeriesCountByLabelValuePair = cut(st.SeriesCountByLabelValuePair)
	st.LabelValueCountByLabelName = cut(st.LabelValueCountByLabelName)
	return st, nil
}

// TopQuery — one row of /api/v1/status/top_queries. Unknown fields are
// dropped; the query text is VM's verbatim.
type TopQuery struct {
	Query              string  `json:"query"`
	TimeRangeSeconds   float64 `json:"timeRangeSeconds"`
	Count              uint64  `json:"count,omitempty"`
	AvgDurationSeconds float64 `json:"avgDurationSeconds,omitempty"`
	SumDurationSeconds float64 `json:"sumDurationSeconds,omitempty"`
}

// TopQueries — the decoded /api/v1/status/top_queries body.
type TopQueries struct {
	TopByCount       []TopQuery `json:"topByCount"`
	TopByAvgDuration []TopQuery `json:"topByAvgDuration"`
	TopBySumDuration []TopQuery `json:"topBySumDuration"`
}

// FetchTopQueries — /api/v1/status/top_queries?topN=&maxLifetime=. The body
// has NO status envelope (VM-specific endpoint), so it is decoded directly;
// a body that does carry {"status":"error"} is surfaced as an error.
func (s *Service) FetchTopQueries(ctx context.Context, topN int, maxLifetime string) (TopQueries, error) {
	cfg, err := s.ready()
	if err != nil {
		return TopQueries{}, err
	}
	p := url.Values{"topN": {strconv.Itoa(topN)}}
	if maxLifetime != "" {
		p.Set("maxLifetime", maxLifetime)
	}
	body, err := promapi.Do(ctx, s.request("/api/v1/status/top_queries", p, cfg))
	if err != nil {
		return TopQueries{}, err
	}
	var probe struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(body, &probe) == nil && probe.Status == "error" {
		return TopQueries{}, fmt.Errorf("victoriametrics: %s", probe.Error)
	}
	var tq TopQueries
	if err := json.Unmarshal(body, &tq); err != nil {
		return TopQueries{}, fmt.Errorf("victoriametrics top_queries decode: %w", err)
	}
	cut := func(e []TopQuery) []TopQuery {
		if e == nil {
			return []TopQuery{}
		}
		if len(e) > topN {
			return e[:topN]
		}
		return e
	}
	tq.TopByCount, tq.TopByAvgDuration, tq.TopBySumDuration = cut(tq.TopByCount), cut(tq.TopByAvgDuration), cut(tq.TopBySumDuration)
	return tq, nil
}
