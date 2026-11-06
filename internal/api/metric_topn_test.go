package api

// v0.10.1838 — server-side top-N contract (Metrics Explorer Faz 1 dilim 1.4).
// Kabul ölçütü (audit §7): "Aynı sorgu iki kez aynı N seriyi döner; cache
// anahtarında limit, rank".

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestParseTopN(t *testing.T) {
	cases := []struct {
		limit, rank       string
		limitSet, rankSet bool
		want              topNSpec
		wantErr           bool
	}{
		{"", "", false, false, topNSpec{}, false}, // legacy: off
		{"", "", true, false, topNSpec{8, "avg"}, false},
		{"abc", "", true, false, topNSpec{8, "avg"}, false},
		{"0", "", true, false, topNSpec{8, "avg"}, false},
		{"-3", "", true, false, topNSpec{8, "avg"}, false},
		{"1", "", true, false, topNSpec{8, "avg"}, false},
		{"8", "", true, false, topNSpec{8, "avg"}, false},
		{"9", "", true, false, topNSpec{20, "avg"}, false},
		{"20", "", true, false, topNSpec{20, "avg"}, false},
		{"21", "", true, false, topNSpec{50, "avg"}, false},
		{"50", "", true, false, topNSpec{50, "avg"}, false},
		{"5000", "", true, false, topNSpec{50, "avg"}, false},
		{"", "max", false, true, topNSpec{8, "max"}, false},
		{"20", "LAST", true, true, topNSpec{20, "last"}, false},
		{"20", "avg", true, true, topNSpec{20, "avg"}, false},
		{"20", "median", true, true, topNSpec{}, true},
	}
	for _, tc := range cases {
		got, err := parseTopN(tc.limit, tc.rank, tc.limitSet, tc.rankSet)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("parseTopN(%q,%q,%v,%v) = %+v, %v", tc.limit, tc.rank, tc.limitSet, tc.rankSet, got, err)
		}
	}
	if (topNSpec{}).keyTag() != "" || (topNSpec{20, "max"}).keyTag() != ":topn=20:max" {
		t.Error("keyTag must be empty when off and carry limit+rank when on")
	}
}

func topnPts(vals ...float64) []chstore.SpanMetricPoint {
	out := make([]chstore.SpanMetricPoint, len(vals))
	for i, v := range vals {
		out[i] = chstore.SpanMetricPoint{Time: int64(i+1) * 60e9, Value: v}
	}
	return out
}

func TestCutTopNGoIsDeterministic(t *testing.T) {
	var in []chstore.SpanMetricSeries
	for i := 0; i < 30; i++ {
		v := float64(i % 5) // heavy ties
		in = append(in, chstore.SpanMetricSeries{GroupKey: []string{fmt.Sprintf("orders-api-%02d", i)}, Points: topnPts(v, v)})
	}
	in = append(in, chstore.SpanMetricSeries{GroupKey: []string{"empty"}})
	first, total := cutTopNGo(in, topNSpec{8, "avg"})
	if total != 31 || len(first) != 8 {
		t.Fatalf("total %d len %d", total, len(first))
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		sh := append([]chstore.SpanMetricSeries(nil), in...)
		r.Shuffle(len(sh), func(a, b int) { sh[a], sh[b] = sh[b], sh[a] })
		got, _ := cutTopNGo(sh, topNSpec{8, "avg"})
		for j := range got {
			if got[j].GroupKey[0] != first[j].GroupKey[0] {
				t.Fatalf("shuffle %d changed position %d: %v vs %v", i, j, got[j].GroupKey, first[j].GroupKey)
			}
		}
	}
	// value 4 wins; ties broken by key ascending
	if first[0].GroupKey[0] != "orders-api-04" || first[1].GroupKey[0] != "orders-api-09" {
		t.Fatalf("order %v %v", first[0].GroupKey, first[1].GroupKey)
	}
}

func TestRankModes(t *testing.T) {
	in := []chstore.SpanMetricSeries{
		{GroupKey: []string{"a"}, Points: topnPts(10, 0, 0)}, // max 10, avg 3.33, last 0
		{GroupKey: []string{"b"}, Points: topnPts(4, 4, 4)},  // max 4, avg 4, last 4
		{GroupKey: []string{"c"}, Points: topnPts(0, 1, 5)},  // max 5, avg 2, last 5
	}
	for rank, want := range map[string]string{"avg": "b", "max": "a", "last": "c"} {
		got, _ := cutTopNGo(in, topNSpec{1, rank})
		if got[0].GroupKey[0] != want {
			t.Errorf("rank=%s: top %v, want %s", rank, got[0].GroupKey, want)
		}
	}
}

// topNVM — answers a topk_*(k, …) wrapper with k series in a RANDOM order
// (ties included) and count(…) with total.
func topNVM(t *testing.T, available, total int) *fakeVM {
	calls := 0
	return newFakeVM(t, func(path string, v url.Values) (int, string) {
		if path != "/api/v1/query_range" {
			return fakeVMDefault(path, v)
		}
		q := v.Get("query")
		if strings.HasPrefix(q, "count(") {
			return 200, fmt.Sprintf(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000060,"%d"],[1700000120,"%d"]]}]}}`, total-3, total)
		}
		k := available
		var kk int
		if _, err := fmt.Sscanf(q[strings.Index(q, "(")+1:], "%d,", &kk); err == nil && kk < k {
			k = kk
		}
		series := make([]vmSeries, k)
		for i := range series {
			val := float64(i / 3) // ties in threes
			series[i] = vmSeries{labels: map[string]string{"job": fmt.Sprintf("payments-mobile-%02d", i)}, values: []float64{val, val}}
		}
		calls++
		r := rand.New(rand.NewSource(int64(calls)))
		r.Shuffle(len(series), func(a, b int) { series[a], series[b] = series[b], series[a] })
		return 200, vmMatrix(series, false)
	})
}

func runTopNQuery(t *testing.T, s *Server, extra string) (metricMeta, []string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.queryMetric(rec, httptest.NewRequest("GET", "/api/metrics/query?refresh=1&name=process_cpu_seconds_total&agg=max&groupBy=job&step=60&"+goldenWindow()+extra, nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var b struct {
		Series []chstore.SpanMetricSeries `json:"series"`
		Meta   metricMeta                 `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(b.Series))
	for i, s := range b.Series {
		keys[i] = strings.Join(s.GroupKey, ",")
	}
	return b.Meta, keys
}

func TestTopNOnVMWrapsAndCountsOnlyWhenFull(t *testing.T) {
	fv := topNVM(t, 40, 37+3)
	s := fv.server()
	m, first := runTopNQuery(t, s, "&limit=8&rank=avg")
	if len(first) != 8 || m.SeriesShown != 8 || m.SeriesTotal == nil || *m.SeriesTotal != 40 || !m.Truncated ||
		m.Limit != 8 || m.Rank != "avg" || !strings.HasPrefix(m.Executed, "topk_avg(16, (") {
		t.Fatalf("meta %+v total %v", m, m.SeriesTotal)
	}
	if strings.Contains(m.Executed, "count(") {
		t.Fatal("the count probe must not be reported as the executed query")
	}
	// same query twice (VM shuffles its answer) → same N series, same order
	_, second := runTopNQuery(t, s, "&limit=8&rank=avg")
	if strings.Join(first, "|") != strings.Join(second, "|") {
		t.Fatalf("non-deterministic top-N:\n%v\n%v", first, second)
	}
	if n := countQueries(fv, "count("); n != 2 {
		t.Fatalf("count probes = %d, want one per full read", n)
	}
	for rank, fn := range map[string]string{"max": "topk_max(16,", "last": "topk_last(16,"} {
		m, _ := runTopNQuery(t, s, "&limit=8&rank="+rank)
		if !strings.HasPrefix(m.Executed, fn) {
			t.Errorf("rank=%s executed %q", rank, m.Executed)
		}
	}
}

func countQueries(fv *fakeVM, prefix string) int {
	n := 0
	for _, r := range fv.requestsTo("/api/v1/query_range") {
		if strings.HasPrefix(r.Values.Get("query"), prefix) {
			n++
		}
	}
	return n
}

func TestTopNNotFullNeedsNoCount(t *testing.T) {
	fv := topNVM(t, 5, 999)
	s := fv.server()
	m, keys := runTopNQuery(t, s, "&limit=8")
	if len(keys) != 5 || m.SeriesTotal == nil || *m.SeriesTotal != 5 || m.Truncated {
		t.Fatalf("meta %+v", m)
	}
	if n := countQueries(fv, "count("); n != 0 {
		t.Fatalf("count probe ran %d times on a result below the window", n)
	}
}

func TestTopNLegacyRequestUnchanged(t *testing.T) {
	fv := topNVM(t, 40, 40)
	s := fv.server()
	m, keys := runTopNQuery(t, s, "")
	if len(keys) != 40 || m.Limit != 0 || strings.Contains(m.Executed, "topk") {
		t.Fatalf("no limit param must keep today's result set: %d series, meta %+v", len(keys), m)
	}
}

func TestTopNKeysCarryLimitAndRank(t *testing.T) {
	s, rc := faz2Server()
	base := fmt.Sprintf("/api/metrics/query?name=m&from=%d&to=%d", faz2From.UnixNano(), faz2To.UnixNano())
	for _, u := range []string{base, base + "&limit=8", base + "&limit=20", base + "&limit=8&rank=max"} {
		s.queryMetric(httptest.NewRecorder(), httptest.NewRequest("GET", u, nil))
	}
	k := rc.keys
	if len(k) != 4 || strings.Contains(k[0], "topn") || k[1] == k[2] || k[1] == k[3] || !strings.HasSuffix(k[3], ":topn=8:max") {
		t.Fatalf("keys %v", k)
	}
	rec := httptest.NewRecorder()
	s.queryMetric(rec, httptest.NewRequest("GET", base+"&rank=p99", nil))
	if rec.Code != 400 {
		t.Fatalf("bad rank → %d", rec.Code)
	}
}

func TestTopNPromQLAndBundle(t *testing.T) {
	fv := topNVM(t, 40, 40)
	s := fv.server()
	rec := httptest.NewRecorder()
	s.queryPromQL(rec, httptest.NewRequest("GET", "/api/metrics/promql?meta=1&query=up&limit=20&rank=last&step=60&"+goldenWindow(), nil))
	var e struct {
		Series []chstore.SpanMetricSeries `json:"series"`
		Meta   metricMeta                 `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if len(e.Series) != 20 || e.Meta.Executed != "topk_last(40, (up))" || e.Meta.SeriesTotal == nil || *e.Meta.SeriesTotal != 40 {
		t.Fatalf("promql top-N: %d series, meta %+v", len(e.Series), e.Meta)
	}

	body := fmt.Sprintf(`{"from":%d,"to":%d,"requests":[{"id":"p1","type":"metric","name":"process_cpu_seconds_total","agg":"max","groupBy":["job"],"step":60,"limit":8,"rank":"max"}]}`,
		goldenFrom.UnixNano(), goldenTo.UnixNano())
	rec = httptest.NewRecorder()
	s.dashboardsData(rec, httptest.NewRequest("POST", "/api/dashboards/data", strings.NewReader(body)))
	var slots map[string]bundleSlot
	if err := json.Unmarshal(rec.Body.Bytes(), &slots); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	p := slots["p1"]
	if len(p.Series) != 8 || p.Meta == nil || p.Meta.Limit != 8 || p.Meta.Rank != "max" {
		t.Fatalf("bundle slot: %d series meta %+v err %q", len(p.Series), p.Meta, p.Error)
	}
	// dashPanelKey: unchanged without limit, forked with it
	q := bundleReq{ID: "p1", Type: "metric", Name: "m"}
	k0 := dashPanelKey("t", q, goldenFrom, goldenTo)
	q.Limit, q.Rank = 8, "max"
	if k0 == dashPanelKey("t", q, goldenFrom, goldenTo) {
		t.Fatal("bundle key must carry limit/rank")
	}
}

// The handler and the bundle slot share ONE builder read, and that read still
// reaches the note-carrying seam call (the v0.9.566 drift class).
func TestReadMetricIsTheSharedBuilderRead(t *testing.T) {
	raw, err := os.ReadFile("metric_topn.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoComments(string(raw))
	i := strings.Index(src, "func readMetric(")
	if i < 0 {
		t.Fatal("readMetric missing")
	}
	body := src[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "queryMetricNoted(ctx, src, f)") || !strings.Contains(body, "tn.QueryMetricTopN(ctx, f, spec.Limit, spec.Rank)") {
		t.Fatalf("readMetric no longer routes through queryMetricNoted / QueryMetricTopN:\n%s", body)
	}
	h, err := readMetricHandlerSources()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoComments(string(h)), "rd, qerr := readMetric(ctx, src, chstore.MetricQueryFilter{") {
		t.Fatal("queryMetric does not use readMetric")
	}
}
