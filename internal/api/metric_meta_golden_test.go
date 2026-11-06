package api

// v0.10.1835 — the `meta` envelope is ADDITIVE (audit §7 dilim 1.1 kabul
// ölçütü: "eski alanlar birebir; golden test").
//
// The golden files under testdata/metric_meta_legacy/ were captured from the
// handlers as they stood BEFORE the envelope existed (the v0.10.1835 move
// commit is verbatim, so the capture ran on the legacy code). Each test strips
// the `meta` member from today's body and asserts the remainder is
// BYTE-IDENTICAL to that capture — a renamed, reordered or re-typed legacy
// field fails here even if every FE consumer would still happen to parse it.
//
// Regenerate ONLY when a legacy field changes on purpose:
//   METRIC_META_GOLDEN_UPDATE=1 go test ./internal/api/ -run TestMetricMetaLegacyGolden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var goldenFrom = time.Unix(1_700_000_000, 0)
var goldenTo = goldenFrom.Add(time.Hour)

func goldenWindow() string {
	return fmt.Sprintf("from=%d&to=%d", goldenFrom.UnixNano(), goldenTo.UnixNano())
}

// goldenVM — answers the builder query with 3 series, the heatmap bucket
// query with two le buckets, and instant/raw queries with 2 series.
func goldenVM(t *testing.T) *fakeVM {
	return newFakeVM(t, func(path string, v url.Values) (int, string) {
		q := v.Get("query")
		switch path {
		case "/api/v1/query_range":
			if strings.Contains(q, "absent_metric") {
				return 200, vmMatrix(nil, false)
			}
			if strings.Contains(q, "_bucket") {
				return 200, vmMatrix([]vmSeries{
					{labels: map[string]string{"le": "0.1"}, values: []float64{1, 3}},
					{labels: map[string]string{"le": "+Inf"}, values: []float64{2, 5}},
				}, false)
			}
			return 200, vmMatrix(nSeries(3), false)
		case "/api/v1/query":
			return 200, vmVector(nSeries(2), false)
		}
		return fakeVMDefault(path, v)
	})
}

// stripMeta — the body with its top-level `"meta":<raw>` member removed,
// byte-for-byte otherwise. Fails if meta is absent (the envelope must be
// there for the comparison to mean anything).
func stripMeta(t *testing.T, body []byte) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, body)
	}
	raw, ok := m["meta"]
	if !ok {
		t.Fatalf("body carries no meta member: %s", body)
	}
	for _, cut := range [][]byte{
		append([]byte(`,"meta":`), raw...),
		append(append([]byte(`"meta":`), raw...), ','),
	} {
		if i := bytes.Index(body, cut); i >= 0 {
			return append(append([]byte(nil), body[:i]...), body[i+len(cut):]...)
		}
	}
	t.Fatalf("could not locate the meta member bytes in %s", body)
	return nil
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	got = bytes.TrimSpace(got)
	p := filepath.Join("testdata", "metric_meta_legacy", name)
	if os.Getenv("METRIC_META_GOLDEN_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("golden %s missing: %v", p, err)
	}
	if !bytes.Equal(got, bytes.TrimSpace(want)) {
		t.Fatalf("legacy fields of %s drifted:\n got  %s\n want %s", name, got, bytes.TrimSpace(want))
	}
}

// legacyBody — today's body minus meta, or the body itself while the
// golden is being captured from the pre-envelope handler.
func legacyBody(t *testing.T, body []byte) []byte {
	if os.Getenv("METRIC_META_GOLDEN_UPDATE") == "1" && !bytes.Contains(body, []byte(`"meta"`)) {
		return body
	}
	return stripMeta(t, body)
}

func TestMetricMetaLegacyGolden(t *testing.T) {
	fv := goldenVM(t)
	s := fv.server()

	t.Run("query", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.queryMetric(rec, httptest.NewRequest("GET", "/api/metrics/query?name=process_cpu_seconds_total&agg=max&groupBy=job&step=60&"+goldenWindow(), nil))
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		checkGolden(t, "query.json", legacyBody(t, rec.Body.Bytes()))
	})
	t.Run("query-empty-percentile-note", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.queryMetric(rec, httptest.NewRequest("GET", "/api/metrics/query?name=absent_metric&service=orders-api&agg=p95&step=60&"+goldenWindow(), nil))
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		checkGolden(t, "query_note.json", legacyBody(t, rec.Body.Bytes()))
	})
	t.Run("histogram", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.getMetricHistogram(rec, httptest.NewRequest("GET", "/api/metrics/histogram?name=http_server_request_duration_seconds&service=orders-api&step=60&"+goldenWindow(), nil))
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		checkGolden(t, "histogram.json", legacyBody(t, rec.Body.Bytes()))
	})
	// promql without ?meta=1 is the legacy BARE ARRAY, byte-identical — the
	// envelope is opt-in there because an array cannot grow a member.
	t.Run("promql-range-legacy", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.queryPromQL(rec, httptest.NewRequest("GET", "/api/metrics/promql?query=sum(rate(x[5m]))&step=60&"+goldenWindow(), nil))
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		checkGolden(t, "promql_range.json", rec.Body.Bytes())
	})
	t.Run("promql-instant-legacy", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.queryPromQL(rec, httptest.NewRequest("GET", "/api/metrics/promql?query=up&instant=1&"+goldenWindow(), nil))
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		checkGolden(t, "promql_instant.json", rec.Body.Bytes())
	})
	t.Run("bundle-metric-slot", func(t *testing.T) {
		body := fmt.Sprintf(`{"from":%d,"to":%d,"requests":[{"id":"p1","type":"metric","name":"process_cpu_seconds_total","agg":"max","groupBy":["job"],"step":60}]}`,
			goldenFrom.UnixNano(), goldenTo.UnixNano())
		rec := httptest.NewRecorder()
		s.dashboardsData(rec, httptest.NewRequest("POST", "/api/dashboards/data", strings.NewReader(body)))
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		var slots map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &slots); err != nil {
			t.Fatal(err)
		}
		checkGolden(t, "bundle_slot.json", legacyBody(t, slots["p1"]))
	})
}
