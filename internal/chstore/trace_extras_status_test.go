package chstore

// v0.10.1519 regression (operator-reported, prod): the /traces
// http.status_code column showed 0 for long (11–18 s) calls and Kafka
// producer spans. spans.http_status is UInt16 DEFAULT 0, so a span with
// no status attribute (non-HTTP span, or a request that never got a
// response) stores 0, and any() could pick that 0 over a sibling's 200.
// The extras projection reads only spans that carry a status; filters keep
// the raw column (`http.status_code = 0` unchanged).

import (
	"strings"
	"testing"
)

func TestTraceExtrasStatusReadsOnlySpansWithStatus(t *testing.T) {
	for _, key := range []string{"http.status_code", "http.response.status_code"} {
		sel, args := traceExtrasProjection([]string{key}, "")
		want := ", argMinIf(toString(http_status), " + traceExtrasNoSvcRank + ", http_status != 0) AS extra_0"
		if sel != want {
			t.Errorf("%s: got %q want %q", key, sel, want)
		}
		if len(args) != 0 {
			t.Errorf("%s: no binds expected, got %v", key, args)
		}
		if strings.Contains(sel, "any(toString(http_status))") {
			t.Errorf("%s: bare any() can surface the DEFAULT 0", key)
		}
	}
	// Other structured columns are untouched.
	if sel, _ := traceExtrasProjection([]string{"http.method"}, ""); sel != ", argMinIf(http_method, "+traceExtrasNoSvcRank+", http_method != '') AS extra_0" {
		t.Errorf("http.method projection changed: %q", sel)
	}
}

// Filter semantics stay on the raw column: `= 0` still selects spans
// without a status.
func TestHTTPStatusZeroFilterUnchanged(t *testing.T) {
	sql, args, err := FilterExpr{Key: "http.status_code", Op: "=", Values: []string{"0"}}.SQLWithPromoted(nil)
	if err != nil {
		t.Fatal(err)
	}
	if sql != "http_status = ?" || len(args) != 1 || args[0] != "0" {
		t.Fatalf("status=0 filter changed: %q %v", sql, args)
	}
}
