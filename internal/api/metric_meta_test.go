package api

// v0.10.1835 — meta envelope contract (Metrics Explorer Faz 1 dilim 1.1).
// Kabul ölçütü (audit §7): "VM 1000 kesmesinde truncated=true". The pure
// builder is table-tested; the handler tests drive a fake VM so the flag is
// proven to travel from the wire to the body, not just to be computed.

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/promapi"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestBuildMetricMeta(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		recs  []vmetrics.ResultRecord
		shown int
		ch    chMetaHints
		note  string
		want  metricMeta
	}{
		{"vm plain", metricSourceVM,
			[]vmetrics.ResultRecord{{Kind: vmetrics.ResultRange, Query: "max(x)", StepSec: 60, Total: 3}}, 3, chMetaHints{}, "",
			metricMeta{Source: "vm", Executed: "max(x)", SeriesTotal: intp(3), SeriesShown: 3, StepSec: 60}},
		{"vm over the 1000 backstop", metricSourceVM,
			[]vmetrics.ResultRecord{{Kind: vmetrics.ResultRange, Query: "up", StepSec: 30, Total: 1005, Truncated: true}}, 1000, chMetaHints{}, "",
			metricMeta{Source: "vm", Executed: "up", SeriesTotal: intp(1005), SeriesShown: 1000, Truncated: true, StepSec: 30}},
		{"vm partial + note", metricSourceVM,
			[]vmetrics.ResultRecord{{Kind: vmetrics.ResultRange, Query: "up", StepSec: 30, Partial: true}}, 0, chMetaHints{}, "boş",
			metricMeta{Source: "vm", Executed: "up", SeriesTotal: intp(0), Partial: true, StepSec: 30, Note: "boş"}},
		{"vm count probe is not 'executed'", metricSourceVM,
			[]vmetrics.ResultRecord{
				{Kind: vmetrics.ResultRange, Query: "topk_avg(8, x)", StepSec: 60, Total: 8},
				{Kind: vmetrics.ResultCount, Query: "count(x)", Total: 1},
			}, 8, chMetaHints{}, "",
			metricMeta{Source: "vm", Executed: "topk_avg(8, x)", SeriesTotal: intp(8), SeriesShown: 8, StepSec: 60}},
		{"vm instant (no step)", metricSourceVM,
			[]vmetrics.ResultRecord{{Kind: vmetrics.ResultInstant, Query: "up", Total: 2}}, 2, chMetaHints{}, "",
			metricMeta{Source: "vm", Executed: "up", SeriesTotal: intp(2), SeriesShown: 2}},
		{"vm with no recorded read (seriesTotal unknown)", metricSourceVM, nil, 0, chMetaHints{}, "",
			metricMeta{Source: "vm"}},
		{"ch complete", metricSourceCH, nil, 4, chMetaHints{StepSec: 60}, "",
			metricMeta{Source: "clickhouse", SeriesTotal: intp(4), SeriesShown: 4, StepSec: 60}},
		{"ch row cap hit → total unknown", metricSourceCH, nil, 120, chMetaHints{Truncated: true, StepSec: 60}, "",
			metricMeta{Source: "clickhouse", SeriesShown: 120, Truncated: true, StepSec: 60}},
		{"ch promql echoes the operator's text", metricSourceCH, nil, 1, chMetaHints{Executed: "sum(up)"}, "",
			metricMeta{Source: "clickhouse", Executed: "sum(up)", SeriesTotal: intp(1), SeriesShown: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildMetricMeta(tc.src, tc.recs, 1500*time.Microsecond, tc.shown, tc.ch, tc.note)
			tc.want.TookMs = 1
			gb, _ := json.Marshal(got)
			wb, _ := json.Marshal(tc.want)
			if string(gb) != string(wb) {
				t.Fatalf("\n got  %s\n want %s", gb, wb)
			}
		})
	}
}

func TestObservedStepSec(t *testing.T) {
	pts := func(secs ...int64) []chstore.SpanMetricPoint {
		out := make([]chstore.SpanMetricPoint, len(secs))
		for i, s := range secs {
			out[i] = chstore.SpanMetricPoint{Time: s * int64(time.Second)}
		}
		return out
	}
	cases := []struct {
		name string
		in   []chstore.SpanMetricSeries
		want int
	}{
		{"empty", nil, 0},
		{"single point", []chstore.SpanMetricSeries{{Points: pts(60)}}, 0},
		{"regular", []chstore.SpanMetricSeries{{Points: pts(0, 60, 120)}}, 60},
		{"sparse series does not widen the answer", []chstore.SpanMetricSeries{{Points: pts(0, 300)}, {Points: pts(0, 30, 60)}}, 30},
		{"duplicate stamps ignored", []chstore.SpanMetricSeries{{Points: pts(60, 60, 120)}}, 60},
	}
	for _, tc := range cases {
		if got := observedStepSec(tc.in); got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}

type metaBody struct {
	Meta metricMeta `json:"meta"`
}

// The VM 1000-series cut reaches the HTTP body (it used to stop at MCP).
func TestMetricQueryMetaCarriesVMTruncationAndPartial(t *testing.T) {
	n := promapi.MaxSeriesParsed + 5
	fv := newFakeVM(t, func(path string, v url.Values) (int, string) {
		if path == "/api/v1/query_range" {
			return 200, vmMatrix(nSeries(n), true)
		}
		return fakeVMDefault(path, v)
	})
	s := fv.server()
	rec := httptest.NewRecorder()
	s.queryMetric(rec, httptest.NewRequest("GET", "/api/metrics/query?name=process_cpu_seconds_total&agg=max&groupBy=job&step=60&"+goldenWindow(), nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var b metaBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	m := b.Meta
	if m.Source != "vm" || !m.Truncated || !m.Partial || m.SeriesShown != promapi.MaxSeriesParsed ||
		m.SeriesTotal == nil || *m.SeriesTotal != n || m.StepSec != 60 || !strings.Contains(m.Executed, "process_cpu_seconds_total") {
		t.Fatalf("meta = %+v (total %v)", m, m.SeriesTotal)
	}
	// Exactly one VM read: the meta collection must not cost a round trip.
	if got := len(fv.requestsTo("/api/v1/query_range")); got != 1 {
		t.Fatalf("query_range requests = %d, want 1", got)
	}
}

// ?meta=1 wraps the SAME series the legacy array carries; the cache key
// differs only by the flag.
func TestPromQLMetaEnvelopeOptIn(t *testing.T) {
	fv := newFakeVM(t, func(path string, v url.Values) (int, string) {
		if path == "/api/v1/query_range" {
			return 200, vmMatrix(nSeries(3), false)
		}
		return fakeVMDefault(path, v)
	})
	s := fv.server()
	plain := httptest.NewRecorder()
	s.queryPromQL(plain, httptest.NewRequest("GET", "/api/metrics/promql?query=up&step=60&"+goldenWindow(), nil))
	env := httptest.NewRecorder()
	s.queryPromQL(env, httptest.NewRequest("GET", "/api/metrics/promql?query=up&step=60&meta=1&"+goldenWindow(), nil))
	var e struct {
		Series json.RawMessage `json:"series"`
		Meta   metricMeta      `json:"meta"`
	}
	if err := json.Unmarshal(env.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(plain.Body.String()) != string(e.Series) {
		t.Fatalf("envelope series differ from the legacy array:\n%s\n%s", plain.Body, e.Series)
	}
	if e.Meta.Executed != "up" || e.Meta.SeriesShown != 3 || e.Meta.Source != "vm" {
		t.Fatalf("meta = %+v", e.Meta)
	}

	s2, rc := faz2Server()
	for _, u := range []string{"/api/metrics/promql?query=up&from=1&to=2", "/api/metrics/promql?query=up&from=1&to=2&meta=1"} {
		s2.queryPromQL(httptest.NewRecorder(), httptest.NewRequest("GET", u, nil))
	}
	keys := rc.keys
	if len(keys) != 2 || keys[0] == keys[1] || !strings.HasSuffix(keys[1], ":meta=1") || strings.Contains(keys[0], "meta") {
		t.Fatalf("keys = %v", keys)
	}
}

// Histogram meta: shown = le buckets, step from the time axis.
func TestHistogramMeta(t *testing.T) {
	fv := goldenVM(t)
	s := fv.server()
	rec := httptest.NewRecorder()
	s.getMetricHistogram(rec, httptest.NewRequest("GET", "/api/metrics/histogram?name=http_server_request_duration_seconds&service=orders-api&step=60&"+goldenWindow(), nil))
	var b metaBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if b.Meta.SeriesShown != 2 || b.Meta.StepSec != 60 || !strings.Contains(b.Meta.Executed, "_bucket") {
		t.Fatalf("histogram meta = %+v", b.Meta)
	}
}
