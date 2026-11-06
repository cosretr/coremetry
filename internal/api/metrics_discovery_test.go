package api

// v0.10.1840 — metric discovery endpoints (Metrics Explorer Faz 1 dilim 1.6).
// Kabul ölçütü (audit §7): "Tavanlar test edilir; CH'de supported:false".
// The fake VM answers the shapes listed in metrics_discovery.go's header —
// ASSUMED from the VM / Prometheus docs, not verified against a live VM.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestDiscoveryCeilings(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		def, max int
		want     int
	}{
		{"", 100, 200, 100}, {"0", 100, 200, 100}, {"-5", 100, 200, 100}, {"x", 100, 200, 100},
		{"150", 100, 200, 150}, {"201", 100, 200, 200}, {"999999", 100, 200, 200},
		{"", 10, 50, 10}, {"51", 10, 50, 50}, {"25", 20, 20, 20}, {"5", 20, 20, 5},
	} {
		if got := discClampInt(tc.raw, tc.def, tc.max); got != tc.want {
			t.Errorf("discClampInt(%q,%d,%d) = %d, want %d", tc.raw, tc.def, tc.max, got, tc.want)
		}
	}
	// windows: every unit the duration parser accepts, capped
	for _, tc := range []struct {
		raw  string
		max  time.Duration
		want time.Duration
	}{
		{"", 24 * time.Hour, 24 * time.Hour},
		{"30m", 24 * time.Hour, 30 * time.Minute},
		{"6h", 24 * time.Hour, 6 * time.Hour},
		{"48h", 24 * time.Hour, 24 * time.Hour},
		{"7d", 24 * time.Hour, 24 * time.Hour},
		{"90s", time.Hour, 90 * time.Second},
		{"2h", time.Hour, time.Hour},
		{"garbage", time.Hour, time.Hour},
	} {
		if got := clampWindow(tc.raw, tc.max); got != tc.want {
			t.Errorf("clampWindow(%q,%v) = %v, want %v", tc.raw, tc.max, got, tc.want)
		}
	}
	if discQuery("a") != "" || discQuery(" ab ") != "ab" || discQuery("ç") != "" {
		t.Error("q shorter than 2 runes must be ignored")
	}
	for sel, want := range map[string]bool{
		`http_server_request_duration_seconds_count{service_name="orders-api"}`: true,
		`up`:                     true,
		`{__name__="up"}`:        true,
		`{job="branch-teller"}`:  false,
		`  {service_name=~".*"}`: false,
		`sum(rate(x[5m]))`:       false,
	} {
		if got := selectorNamesMetric(sel); got != want {
			t.Errorf("selectorNamesMetric(%q) = %v", sel, got)
		}
	}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if d, err := clampTSDBDate("2026-11-05", now); err != nil || d != "2026-11-05" {
		t.Errorf("valid date: %q %v", d, err)
	}
	for _, bad := range []string{"2026-11-07", "05.11.2026", "yesterday"} {
		if _, err := clampTSDBDate(bad, now); err == nil {
			t.Errorf("date %q must be rejected", bad)
		}
	}
	for raw, want := range map[string]string{"": "30m", "10m": "10m", "2h": "120m", "48h": "1440m", "45s": "45s", "x": "30m"} {
		if got := clampLifetime(raw); got != want {
			t.Errorf("clampLifetime(%q) = %q, want %q", raw, got, want)
		}
	}
	if discKey("p", "a", "bc") == discKey("p", "ab", "c") {
		t.Error("discKey must length-prefix its parts")
	}
}

// discVM — answers every discovery path; records requests.
func discVM(t *testing.T, missing map[string]bool) *fakeVM {
	return newFakeVM(t, func(path string, v url.Values) (int, string) {
		if missing[path] {
			return 404, "404 page not found"
		}
		switch {
		case path == "/api/v1/labels":
			return 200, `{"status":"success","data":["__name__","cluster_zone","job","k8s_cluster_name","service_name"]}`
		case strings.HasPrefix(path, "/api/v1/label/"):
			return 200, `{"status":"success","data":["branch-teller","orders-api","payments-mobile"]}`
		case path == "/api/v1/series":
			return 200, `{"status":"success","data":[{"__name__":"up","job":"orders-api"},{"__name__":"up","job":"payments-mobile"}]}`
		case path == "/api/v1/status/tsdb":
			return 200, `{"status":"success","data":{"totalSeries":1200,"totalLabelValuePairs":90,` +
				`"seriesCountByMetricName":[{"name":"http_server_request_duration_seconds_bucket","value":800},{"name":"up","value":12}],` +
				`"seriesCountByLabelName":[{"name":"le","value":800}],"seriesCountByFocusLabelValue":[],` +
				`"seriesCountByLabelValuePair":[{"name":"job=orders-api","value":300}],` +
				`"labelValueCountByLabelName":[{"name":"job","value":3},{"name":"service_name","value":7}]}}`
		case path == "/api/v1/status/top_queries":
			return 200, `{"topN":"5","maxLifetime":"30m","topByCount":[{"query":"up","timeRangeSeconds":3600,"count":9}],` +
				`"topByAvgDuration":[{"query":"sum(rate(x[5m]))","timeRangeSeconds":3600,"avgDurationSeconds":1.5,"count":2}],"topBySumDuration":[]}`
		}
		return fakeVMDefault(path, v)
	})
}

func resetProbe() {
	vmAPIProbe.Lock()
	vmAPIProbe.until = map[string]time.Time{}
	vmAPIProbe.Unlock()
}

func discGet(t *testing.T, h http.HandlerFunc, u string, role string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest("GET", u, nil)
	if role != "" {
		r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: "u-" + role, Role: role}))
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func TestDiscoveryOnClickHouseIsUnsupported(t *testing.T) {
	s := &Server{store: &chstore.Store{}, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	for name, h := range map[string]http.HandlerFunc{
		"labels": s.discoverLabels, "label-values": s.discoverLabelValues, "cardinality": s.discoverCardinality,
		"series-sample": s.discoverSeriesSample, "top-queries": s.discoverTopQueries,
	} {
		code, m := discGet(t, h, "/api/metrics/discover/"+name+"?label=job&match=up", "")
		if code != 200 || m["supported"] != false || m["reason"] != discReasonCH {
			t.Errorf("%s on CH: %d %v", name, code, m)
		}
	}
}

func TestDiscoveryEnforcesCeilingsOnTheWire(t *testing.T) {
	resetProbe()
	fv := discVM(t, nil)
	s := fv.server()
	discGet(t, s.discoverLabels, "/api/metrics/discover/labels?limit=100000&since=720h&q=clu", "")
	discGet(t, s.discoverLabelValues, "/api/metrics/discover/label-values?label=job&limit=5000&since=30d&q=or", "")
	discGet(t, s.discoverCardinality, "/api/metrics/discover/cardinality?topN=5000&focusLabel=service_name", "")
	discGet(t, s.discoverSeriesSample, "/api/metrics/discover/series-sample?match=up&limit=5000&since=48h", "")
	discGet(t, s.discoverTopQueries, "/api/metrics/discover/top-queries?topN=500&maxLifetime=720h", auth.RoleAdmin)

	span := func(v url.Values) time.Duration {
		var a, b float64
		fmt.Sscan(v.Get("start"), &a)
		fmt.Sscan(v.Get("end"), &b)
		return time.Duration((b - a) * float64(time.Second))
	}
	seen := map[string]bool{}
	for _, r := range fv.requests() {
		if strings.HasPrefix(r.Path, "/api/v1/label/") {
			seen["/api/v1/label/"] = true
		} else {
			seen[r.Path] = true
		}
	}
	for _, p := range []string{"/api/v1/labels", "/api/v1/label/", "/api/v1/status/tsdb", "/api/v1/series", "/api/v1/status/top_queries"} {
		if !seen[p] {
			t.Errorf("%s never reached the fake VM — the ceiling checks below would pass vacuously", p)
		}
	}
	for _, r := range fv.requests() {
		switch {
		case r.Path == "/api/v1/labels":
			if span(r.Values) > 24*time.Hour+time.Second || r.Values.Get("limit") != "1000" {
				t.Errorf("labels window %v limit %s", span(r.Values), r.Values.Get("limit"))
			}
		case strings.HasPrefix(r.Path, "/api/v1/label/"):
			if span(r.Values) > 24*time.Hour+time.Second || r.Values.Get("limit") != "201" || !strings.Contains(r.Values.Get("match[]"), "job=~`(?i).*or.*`") {
				t.Errorf("label values window %v limit %s match %q", span(r.Values), r.Values.Get("limit"), r.Values.Get("match[]"))
			}
		case r.Path == "/api/v1/status/tsdb":
			if r.Values.Get("topN") != "50" {
				t.Errorf("tsdb topN %s", r.Values.Get("topN"))
			}
		case r.Path == "/api/v1/series":
			if span(r.Values) > time.Hour+time.Second || r.Values.Get("limit") != "20" || r.Values.Get("match[]") != "up" {
				t.Errorf("series window %v limit %s", span(r.Values), r.Values.Get("limit"))
			}
		case r.Path == "/api/v1/status/top_queries":
			if r.Values.Get("topN") != "20" || r.Values.Get("maxLifetime") != "1440m" {
				t.Errorf("top_queries topN %s life %s", r.Values.Get("topN"), r.Values.Get("maxLifetime"))
			}
		}
	}
}

func TestDiscoveryBodies(t *testing.T) {
	resetProbe()
	fv := discVM(t, nil)
	s := fv.server()
	code, m := discGet(t, s.discoverLabels, "/api/metrics/discover/labels?q=clu", "")
	labels, _ := m["labels"].([]any)
	if code != 200 || m["supported"] != true || len(labels) != 2 {
		t.Fatalf("labels: %d %v", code, m)
	}
	code, m = discGet(t, s.discoverCardinality, "/api/metrics/discover/cardinality?topN=1", "")
	st, _ := m["stats"].(map[string]any)
	byName, _ := st["seriesCountByMetricName"].([]any)
	if code != 200 || m["supported"] != true || len(byName) != 1 || st["totalSeries"].(float64) != 1200 {
		t.Fatalf("cardinality: %d %v", code, m)
	}
	// labels now carry value counts (tsdb cached by the cardinality call? no —
	// different topN; labels reuses the topN=50 entry once it exists)
	discGet(t, s.discoverCardinality, "/api/metrics/discover/cardinality?topN=50", "")
	_, m = discGet(t, s.discoverLabels, "/api/metrics/discover/labels?q=job&refresh=1", "")
	labels, _ = m["labels"].([]any)
	if len(labels) != 1 || labels[0].(map[string]any)["valueCount"].(float64) != 3 {
		t.Fatalf("labels with counts: %v", m)
	}
	code, m = discGet(t, s.discoverSeriesSample, "/api/metrics/discover/series-sample?match={job=%22orders-api%22}", "")
	if code != 400 {
		t.Fatalf("series-sample without a metric name must 400, got %d %v", code, m)
	}
	code, m = discGet(t, s.discoverSeriesSample, "/api/metrics/discover/series-sample?match=up", "")
	if series, _ := m["series"].([]any); code != 200 || len(series) != 2 {
		t.Fatalf("series: %d %v", code, m)
	}
	code, m = discGet(t, s.discoverTopQueries, "/api/metrics/discover/top-queries", auth.RoleEditor)
	qs, _ := m["queries"].(map[string]any)
	if code != 200 || len(qs["topByCount"].([]any)) != 1 || len(qs["topBySumDuration"].([]any)) != 0 {
		t.Fatalf("top-queries: %d %v", code, m)
	}
}

func TestTopQueriesRoleGate(t *testing.T) {
	resetProbe()
	fv := discVM(t, nil)
	s := fv.server()
	h := auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.discoverTopQueries))
	for role, want := range map[string]int{auth.RoleViewer: http.StatusForbidden, auth.RoleEditor: 200, auth.RoleAdmin: 200} {
		code, _ := discGet(t, h.ServeHTTP, "/api/metrics/discover/top-queries", role)
		if code != want {
			t.Errorf("role %s → %d, want %d", role, code, want)
		}
	}
	// everyone reads cardinality (S10)
	code, _ := discGet(t, s.discoverCardinality, "/api/metrics/discover/cardinality", auth.RoleViewer)
	if code != 200 {
		t.Errorf("viewer cardinality → %d", code)
	}
}

func TestDiscoveryMissingAPIIsProbedOnce(t *testing.T) {
	resetProbe()
	fv := discVM(t, map[string]bool{"/api/v1/status/top_queries": true})
	s := fv.server()
	for i := 0; i < 3; i++ {
		code, m := discGet(t, s.discoverTopQueries, fmt.Sprintf("/api/metrics/discover/top-queries?topN=%d", i+1), auth.RoleAdmin)
		if code != 200 || m["supported"] != false || !strings.Contains(m["reason"].(string), "top_queries") {
			t.Fatalf("call %d: %d %v", i, code, m)
		}
	}
	if n := len(fv.requestsTo("/api/v1/status/top_queries")); n != 1 {
		t.Fatalf("missing API probed %d times, want 1", n)
	}
	// a 5xx is NOT remembered — it is the operator's 502
	resetProbe()
	fv2 := newFakeVM(t, func(path string, v url.Values) (int, string) { return 503, "overloaded" })
	s2 := fv2.server()
	code, _ := discGet(t, s2.discoverTopQueries, "/api/metrics/discover/top-queries", auth.RoleAdmin)
	if code != 502 || vmAPIKnownMissing(fv2.srv.URL, "/api/v1/status/top_queries", time.Now()) {
		t.Fatalf("5xx → %d, remembered=%v", code, vmAPIKnownMissing(fv2.srv.URL, "/api/v1/status/top_queries", time.Now()))
	}
	_ = vmetrics.TSDBStatus{}
}

func TestDiscoveryClusterScope(t *testing.T) {
	resetProbe()
	fv := discVM(t, nil)
	s := fv.server()
	discGet(t, s.discoverLabels, "/api/metrics/discover/labels?cluster=acme-prod-1", "")
	discGet(t, s.discoverCardinality, "/api/metrics/discover/cardinality?cluster=acme-prod-1", "")
	for _, r := range fv.requests() {
		switch r.Path {
		case "/api/v1/labels":
			if r.Values.Get("extra_filters[]") != `{k8s_cluster_name="acme-prod-1"}` {
				t.Errorf("labels extra_filters %q", r.Values.Get("extra_filters[]"))
			}
		case "/api/v1/status/tsdb":
			if r.Values.Get("extra_label") != "k8s_cluster_name=acme-prod-1" || r.Values.Get("extra_filters[]") != "" {
				t.Errorf("tsdb scope extra_label=%q extra_filters=%q", r.Values.Get("extra_label"), r.Values.Get("extra_filters[]"))
			}
		}
	}
}

func TestCapabilitiesAdvertiseDiscovery(t *testing.T) {
	vm, ch := capabilitiesFor(metricSourceVM, false), capabilitiesFor(metricSourceCH, false)
	for _, f := range []string{"discoverLabels", "discoverLabelValues", "cardinality", "seriesSample", "topQueries"} {
		if !vm.Features[f].Supported || ch.Features[f].Supported || ch.Features[f].Reason != discReasonCH {
			t.Errorf("%s: vm=%+v ch=%+v", f, vm.Features[f], ch.Features[f])
		}
	}
	if vm.Limits.CardinalityTopNMax != 50 || vm.Limits.SeriesSampleMax != 20 || vm.Limits.DiscoverLabelsMax != 200 || vm.Limits.TopQueriesMax != 20 {
		t.Errorf("limits %+v", vm.Limits)
	}
}
