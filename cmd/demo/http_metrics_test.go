package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// v0.10.2160 — the http.server.duration histogram carries the status code.
//
// Before: the histogram aggregate was keyed by (service, method, route,
// status) but its datapoints emitted only http.method + http.route, so one
// route's 2xx and 5xx points left with identical attributes and Coremetry's
// metric mode (error rate = duration histogram _count split by status code)
// had no status to split on. The contract pinned here:
//   - every histogram datapoint carries http.status_code, as an INT, exactly
//     like the request counter;
//   - the counter and the histogram describe the SAME series set;
//   - no two datapoints of one metric share an attribute set;
//   - metric names and units are untouched (a collector translates names);
//   - the series count stays bounded by the distinct (method, route, status)
//     triples recorded — more requests never mean more series.

func newTestMetricsState() *metricsState {
	return &metricsState{
		httpRequests: map[httpKey]uint64{},
		httpDuration: map[httpKey]*histogramAgg{},
		bizCounters:  map[string]uint64{},
		dbQueries:    map[dbKey]uint64{},
		dbDuration:   map[dbKey]*histogramAgg{},
		cumRequests:  map[httpKey]uint64{},
		cumBiz:       map[string]uint64{},
		cumDBQ:       map[dbKey]uint64{},
	}
}

// findMetric returns the named metric of one service from a flush result.
func findMetric(t *testing.T, rms []*metricspb.ResourceMetrics, service, name string) *metricspb.Metric {
	t.Helper()
	for _, rm := range rms {
		isSvc := false
		for _, a := range rm.GetResource().GetAttributes() {
			if a.GetKey() == "service.name" && a.GetValue().GetStringValue() == service {
				isSvc = true
			}
		}
		if !isSvc {
			continue
		}
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				if m.GetName() == name {
					return m
				}
			}
		}
	}
	t.Fatalf("metric %q not found for service %q", name, service)
	return nil
}

// attrIdentity renders an attribute set as a stable string, type included, so
// an int status and a string status never compare equal.
func attrIdentity(attrs []*commonpb.KeyValue) string {
	parts := make([]string, 0, len(attrs))
	for _, a := range attrs {
		switch v := a.GetValue().GetValue().(type) {
		case *commonpb.AnyValue_IntValue:
			parts = append(parts, fmt.Sprintf("%s=int:%d", a.GetKey(), v.IntValue))
		case *commonpb.AnyValue_StringValue:
			parts = append(parts, fmt.Sprintf("%s=str:%s", a.GetKey(), v.StringValue))
		default:
			parts = append(parts, fmt.Sprintf("%s=other:%v", a.GetKey(), v))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func TestHTTPDurationHistogramCarriesStatusCode(t *testing.T) {
	const svc = "demo-test-http-service"
	m := newTestMetricsState()
	// One route, two outcomes; a second route with a single outcome.
	for i := 0; i < 7; i++ {
		m.RecordHTTP(svc, "POST", "/api/v1/things", 201, 12)
	}
	for i := 0; i < 3; i++ {
		m.RecordHTTP(svc, "POST", "/api/v1/things", 500, 340)
	}
	for i := 0; i < 5; i++ {
		m.RecordHTTP(svc, "GET", "/api/v1/things/{id}", 200, 4)
	}

	rms := m.flush(1, 2)

	dur := findMetric(t, rms, svc, "http.server.duration")
	if dur.GetUnit() != "ms" {
		t.Fatalf("http.server.duration unit = %q, want ms (names/units must not change)", dur.GetUnit())
	}
	req := findMetric(t, rms, svc, "http.server.requests")
	if req.GetUnit() != "1" {
		t.Fatalf("http.server.requests unit = %q, want 1", req.GetUnit())
	}

	histPoints := dur.GetHistogram().GetDataPoints()
	wantCount := map[string]uint64{
		"http.method=str:POST,http.route=str:/api/v1/things,http.status_code=int:201":     7,
		"http.method=str:POST,http.route=str:/api/v1/things,http.status_code=int:500":     3,
		"http.method=str:GET,http.route=str:/api/v1/things/{id},http.status_code=int:200": 5,
	}
	if len(histPoints) != len(wantCount) {
		t.Fatalf("histogram datapoints = %d, want %d (one per method+route+status)", len(histPoints), len(wantCount))
	}
	histIDs := map[string]bool{}
	for _, dp := range histPoints {
		id := attrIdentity(dp.GetAttributes())
		if histIDs[id] {
			t.Fatalf("two histogram datapoints share the attribute set %q", id)
		}
		histIDs[id] = true
		want, ok := wantCount[id]
		if !ok {
			t.Fatalf("unexpected histogram attribute set %q", id)
		}
		if dp.GetCount() != want {
			t.Fatalf("histogram %q count = %d, want %d", id, dp.GetCount(), want)
		}
	}

	// Same series set as the counter — same keys, same value types.
	reqPoints := req.GetSum().GetDataPoints()
	if len(reqPoints) != len(histPoints) {
		t.Fatalf("counter datapoints = %d, histogram = %d — must describe the same series", len(reqPoints), len(histPoints))
	}
	for _, dp := range reqPoints {
		if id := attrIdentity(dp.GetAttributes()); !histIDs[id] {
			t.Fatalf("counter series %q has no histogram twin", id)
		}
	}
}

// More traffic on the same routes must not add series: the status dimension
// is bounded by the literals the scenarios pass, not by request volume.
func TestHTTPDurationHistogramCardinalityBounded(t *testing.T) {
	const svc = "demo-test-http-service"
	m := newTestMetricsState()
	statuses := []int{200, 500}
	for i := 0; i < 5000; i++ {
		m.RecordHTTP(svc, "GET", "/api/v1/things", statuses[i%len(statuses)], float64(i%900))
	}
	rms := m.flush(1, 2)
	got := len(findMetric(t, rms, svc, "http.server.duration").GetHistogram().GetDataPoints())
	if got != len(statuses) {
		t.Fatalf("histogram datapoints after 5000 requests = %d, want %d", got, len(statuses))
	}
}
