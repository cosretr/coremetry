package api

// v0.10.1841 — /api/metrics/promql error position (Metrics Explorer Faz 1
// dilim 1.7). Kabul ölçütü (audit §7): "CH parse hatasında pos sayı". The VM
// error texts below are ASSUMED shapes (the VM error format is undocumented;
// Faz 1 report lists the curl that settles it).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestPromQLErrorPos(t *testing.T) {
	const q = "sum(rate(up[5m])"
	cases := []struct {
		name, msg, query string
		want             int
		ok               bool
	}{
		{"ch parser byte offset", `promql: expected ")", got "" (at position 16)`, q, 16, true},
		{"ch lexer", `unexpected character "$" at position 4`, "up + $x", 4, true},
		{"byte offset after a multibyte rune", `promql: x (at position 14)`, `up{şube="ç"} +`, 12, true},
		{"vm line/col", `cannot parse query: line 1, col 10: unexpected token`, q, 9, true},
		{"vm line/col second line", `line 2, column 3: bad`, "up\n+ x(", 5, true},
		{"vm unparsed tail", `cannot parse "sum(up) foo": unparsed data left: "foo"`, "sum(up) foo", 8, true},
		{"vm end of stream", `cannot parse "sum(rate(up[5m])": unexpected end of stream; want ")"`, q, 16, true},
		{"tail not a suffix → no pos", `unparsed data left: "bar"`, "sum(up) foo", 0, false},
		{"line outside the query → falls through", `line 9, col 1`, q, 0, false},
		{"offset beyond the query → no pos", `at position 99`, q, 0, false},
		{"vm HTTP error body (escaped JSON) is unwrapped", `upstream metrics backend: victoriametrics: HTTP 422: {"status":"error","errorType":"422","error":"cannot parse \"sum(up) foo\": unparsed data left: \"foo\""}`, "sum(up) foo", 8, true},
		{"no position at all", `victoriametrics: HTTP 422: {"status":"error","error":"unknown function foo"}`, "foo(up)", 0, false},
	}
	for _, tc := range cases {
		got, ok := promqlErrorPos(tc.msg, tc.query)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%s: got (%d,%v), want (%d,%v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if l, c := runeLineCol("up\n+ x(", 5); l != 2 || c != 3 {
		t.Errorf("runeLineCol = %d,%d", l, c)
	}
	if d := promqlDetail(`promql: expected ")", got "" (at position 16)`); d != `expected ")", got ""` {
		t.Errorf("detail %q", d)
	}
}

func TestPromQLErrorStatus(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{fmt.Errorf("%w: promql: bad (at position 1)", errBadRequest), 400},
		{fmt.Errorf("%w: victoriametrics: HTTP 422: cannot parse", errUpstream), 502}, // today's status kept
		{errors.New("promql: unexpected token (at position 3)"), 400},                 // untagged 500 syntax → 400
		{errors.New("clickhouse: connection reset"), 500},
	}
	for _, tc := range cases {
		if got := promqlErrorStatus(tc.err); got != tc.want {
			t.Errorf("%v → %d, want %d", tc.err, got, tc.want)
		}
	}
}

type promqlErrResp struct {
	Error  string `json:"error"`
	Pos    *int   `json:"pos"`
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	Detail string `json:"detail"`
}

func TestPromQLCHSyntaxErrorCarriesPos(t *testing.T) {
	s := &Server{store: &chstore.Store{}, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	rec := httptest.NewRecorder()
	s.queryPromQL(rec, httptest.NewRequest("GET", "/api/metrics/promql?query="+url.QueryEscape("sum(rate(up[5m])"), nil))
	var b promqlErrResp
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if rec.Code != http.StatusBadRequest || b.Pos == nil || *b.Pos != 16 || b.Line != 1 || b.Col != 17 || b.Error == "" || b.Detail == "" {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestPromQLVMErrorCarriesPosAndKeepsStatus(t *testing.T) {
	fv := newFakeVM(t, func(path string, v url.Values) (int, string) {
		return 422, `{"status":"error","errorType":"422","error":"cannot parse \"sum(up) foo\": unparsed data left: \"foo\""}`
	})
	s := fv.server()
	rec := httptest.NewRecorder()
	s.queryPromQL(rec, httptest.NewRequest("GET", "/api/metrics/promql?query="+url.QueryEscape("sum(up) foo")+"&"+goldenWindow(), nil))
	var b promqlErrResp
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if rec.Code != http.StatusBadGateway || b.Pos == nil || *b.Pos != 8 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	// no recognisable position → body has no pos, still the legacy error text
	fv2 := newFakeVM(t, func(path string, v url.Values) (int, string) {
		return 422, `{"status":"error","errorType":"422","error":"unknown func foo"}`
	})
	rec = httptest.NewRecorder()
	b = promqlErrResp{}
	fv2.server().queryPromQL(rec, httptest.NewRequest("GET", "/api/metrics/promql?query=foo(up)&"+goldenWindow(), nil))
	if rec.Code != http.StatusBadGateway || json.Unmarshal(rec.Body.Bytes(), &b) != nil || b.Pos != nil || b.Error == "" {
		t.Fatalf("no-pos case: %d %s", rec.Code, rec.Body)
	}
}
