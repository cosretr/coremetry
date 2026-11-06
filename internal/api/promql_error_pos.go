package api

// promql_error_pos.go — v0.10.1841 (Metrics Explorer Faz 1 dilim 1.7, audit
// §3.1 satır 16 "kod kipi hata konumu"). Log Search's v0.10.1818 body is the
// model (logs_query_error.go):
//
//	{"error": "<the text clients already show>", "pos": <0-based rune index>,
//	 "line": 1, "col": 18, "detail": "<engine reason>"}
//
// `error` is BYTE-IDENTICAL to today's message, so every existing client
// (api.ts request → "HTTP 400: {…}", MQE's error line) keeps working; pos /
// line / col / detail are additive and written only when trustworthy.
//
// Where the position comes from:
//
//   - ClickHouse (internal/promql): the parser and lexer stamp "at position
//     N" — a BYTE offset into the query (parse.go errf, lex.go). Converted to
//     a rune index so a Turkish label value before the error does not shift
//     the caret.
//   - VictoriaMetrics (MetricsQL, verbatim pass-through): the error text is
//     VM's, and its format is NOT documented (Faz 1 report, not verified).
//     Patterns recognised, in order — anything else → no pos:
//       1. "line L, col C" / "line L, column C"     → offset of (L, C), 1-based
//       2. "at position N" / "pos N" / "position N" → byte offset N
//       3. `unparsed data left: "<tail>"`           → where <tail> starts, when
//                                                     it is a suffix of the query
//                                                     (audit: "artık kuyruk")
//       4. "unexpected end of stream"               → end of the query
//     A VM HTTP >= 300 body is JSON inside promapi's error text; its inner
//     `error` field is unwrapped first (engineErrorText).
//
// STATUS: CH syntax errors are 400 (ValidatePromQL → errBadRequest); an
// untagged (500) error whose text is a syntax error moves to 400.
// v0.10.1860 (Faz 2 dilim 2.0, operatör onaylı) — a query VictoriaMetrics
// REJECTS is the operator's to fix, not an outage: VM HTTP 400 / 422 → 400
// with the positioned body (vmQueryRejected). VM 5xx, transport errors and
// the 4xx that are NOT about the query text (401/403 credentials, 404 wrong
// base path, 408/429 load) stay 502 — they point at the VM connection.
// Error responses are never cached (serveCached).

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/promapi"
)

// promqlQueryErr — an error from /api/metrics/promql carrying the query it
// belongs to, so the position can be computed against the operator's text.
type promqlQueryErr struct {
	err   error
	query string
}

func (e *promqlQueryErr) Error() string { return e.err.Error() }
func (e *promqlQueryErr) Unwrap() error { return e.err }

func withPromQLQuery(err error, query string) error {
	if err == nil {
		return nil
	}
	return &promqlQueryErr{err: err, query: query}
}

type promqlErrorBody struct {
	Error  string `json:"error"`
	Pos    *int   `json:"pos,omitempty"`
	Line   int    `json:"line,omitempty"`
	Col    int    `json:"col,omitempty"`
	Detail string `json:"detail,omitempty"`
}

var (
	reLineCol   = regexp.MustCompile(`(?i)\bline (\d+),\s*col(?:umn)? (\d+)`)
	reBytePos   = regexp.MustCompile(`(?i)\b(?:at position|position|pos)[ =:]?(\d+)\b`)
	reUnparsed  = regexp.MustCompile(`unparsed data left: "((?:[^"\\]|\\.)*)"`)
	reEndStream = regexp.MustCompile(`(?i)unexpected end of (?:stream|input)`)
	reSyntaxish = regexp.MustCompile(`(?i)\b(?:parse|syntax|unexpected|unparsed|cannot parse)\b`)
)

// byteToRune — SAF: a byte offset into s → rune index, clamped to [0, len].
func byteToRune(s string, b int) int {
	if b < 0 {
		b = 0
	}
	if b > len(s) {
		b = len(s)
	}
	for b > 0 && b < len(s) && !utf8.RuneStart(s[b]) {
		b--
	}
	return utf8.RuneCountInString(s[:b])
}

// lineColToRune — SAF: 1-based (line, col) → rune index; ok=false when the
// pair lies outside the query.
func lineColToRune(s string, line, col int) (int, bool) {
	if line < 1 || col < 1 {
		return 0, false
	}
	lines := strings.Split(s, "\n")
	if line > len(lines) {
		return 0, false
	}
	idx := 0
	for i := 0; i < line-1; i++ {
		idx += utf8.RuneCountInString(lines[i]) + 1
	}
	if col-1 > utf8.RuneCountInString(lines[line-1]) {
		return 0, false
	}
	return idx + col - 1, true
}

// runeLineCol — SAF: rune index → 1-based (line, col).
func runeLineCol(s string, pos int) (int, int) {
	line, col := 1, 1
	for i, r := range []rune(s) {
		if i >= pos {
			break
		}
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return line, col
}

// engineErrorText — SAF: the engine's own message. A VM rejection with HTTP
// >= 300 arrives as promapi.HTTPError, whose text embeds VM's JSON body
// ("…: HTTP 422: {"status":"error",…,"error":"…"}") with its quotes escaped;
// the inner `error` is what the patterns below must read. Anything that is
// not such a body is returned unchanged.
func engineErrorText(msg string) string {
	i := strings.Index(msg, "{")
	if i < 0 {
		return msg
	}
	var b struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(msg[i:]), &b) == nil && b.Error != "" {
		return b.Error
	}
	return msg
}

// promqlErrorPos — SAF (promql_error_pos_test.go): the error text + query →
// rune position. ok=false = no trustworthy position.
func promqlErrorPos(msg, query string) (int, bool) {
	msg = engineErrorText(msg)
	if m := reLineCol.FindStringSubmatch(msg); m != nil {
		l, _ := strconv.Atoi(m[1])
		c, _ := strconv.Atoi(m[2])
		if p, ok := lineColToRune(query, l, c); ok {
			return p, true
		}
	}
	if m := reBytePos.FindStringSubmatch(msg); m != nil {
		if b, err := strconv.Atoi(m[1]); err == nil && b <= len(query) {
			return byteToRune(query, b), true
		}
	}
	if m := reUnparsed.FindStringSubmatch(msg); m != nil {
		tail := m[1]
		if unq, err := strconv.Unquote(`"` + tail + `"`); err == nil {
			tail = unq
		}
		if tail != "" && strings.HasSuffix(query, tail) {
			return utf8.RuneCountInString(query) - utf8.RuneCountInString(tail), true
		}
	}
	if reEndStream.MatchString(msg) {
		return utf8.RuneCountInString(query), true
	}
	return 0, false
}

// promqlDetail — SAF: the engine reason without our wrappers: the CH
// " (at position N)" suffix and the "promql: " prefix are dropped; a VM body
// keeps VM's text.
func promqlDetail(msg string) string {
	d := strings.TrimSpace(engineErrorText(msg))
	if i := strings.LastIndex(d, " (at position "); i > 0 && strings.HasSuffix(d, ")") {
		d = d[:i]
	}
	return strings.TrimPrefix(d, "promql: ")
}

// promqlErrorStatus — SAF: today's status, except an untagged (500) syntax
// error, which is the operator's to fix → 400.
func promqlErrorStatus(err error) int {
	switch {
	case errors.Is(err, errBadRequest):
		return http.StatusBadRequest
	case vmQueryRejected(err):
		return http.StatusBadRequest
	case errors.Is(err, errUpstream):
		return http.StatusBadGateway
	case reSyntaxish.MatchString(err.Error()):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// vmQueryRejected — SAF: true when the error chain holds a VM (promapi) HTTP
// answer that rejects the QUERY TEXT: 400 Bad Request or 422 Unprocessable
// Entity (VM's "cannot parse" code). Every other status — 401/403 auth, 404
// path, 408/429 load, 5xx — is the connection's problem and stays 502.
func vmQueryRejected(err error) bool {
	var he *promapi.HTTPError
	if !errors.As(err, &he) {
		return false
	}
	return he.Status == http.StatusBadRequest || he.Status == http.StatusUnprocessableEntity
}

// promqlErrorBodyFor — SAF: the body for err against query.
func promqlErrorBodyFor(err error, query string) promqlErrorBody {
	b := promqlErrorBody{Error: err.Error()}
	if p, ok := promqlErrorPos(err.Error(), query); ok {
		b.Pos = &p
		b.Line, b.Col = runeLineCol(query, p)
		b.Detail = promqlDetail(err.Error())
	}
	return b
}

// writePromQLQueryErr — the serveCached error hook (cache.go) and the
// pre-cache syntax gate: writes the positioned body when err carries a
// promql query; false otherwise (caller falls through to writeErr).
func writePromQLQueryErr(w http.ResponseWriter, err error) bool {
	var pe *promqlQueryErr
	if !errors.As(err, &pe) || errors.Is(err, context.Canceled) {
		return false // 499 / plain errors stay with writeErr
	}
	status := promqlErrorStatus(pe.err)
	if status == http.StatusInternalServerError {
		return false // a real server fault: writeErr logs it
	}
	if status == http.StatusBadGateway {
		log.Printf("[api] upstream error: %v", pe.err) // writeErr's log line, kept
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(promqlErrorBodyFor(pe.err, pe.query))
	return true
}
