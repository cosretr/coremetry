package sourcestate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/mcp"
)

func TestClassify(t *testing.T) {
	opErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	cases := []struct {
		name string
		err  error
		want State
	}{
		{"nil", nil, OK},
		{"sentinel unauthorized", fmt.Errorf("es search: %w", ErrUnauthorized), Unauthorized},
		{"sentinel not configured", fmt.Errorf("vm: %w", ErrNotConfigured), NotConfigured},
		{"deadline", fmt.Errorf("q: %w", context.DeadlineExceeded), Timeout},
		{"cancelled is not a source state", context.Canceled, Error},
		{"es 401 text", errors.New("ES search: security_exception (status 401) — check API key"), Unauthorized},
		{"es 403 text", errors.New("ES _cat/indices: (status 403) missing monitor privilege"), Unauthorized},
		{"vm 401 text", errors.New("victoriametrics: HTTP 401: Unauthorized"), Unauthorized},
		{"vm 403 text", errors.New("victoriametrics: HTTP 403: forbidden"), Unauthorized},
		{"ch timeout", errors.New("code: 159, DB::Exception: Timeout exceeded"), Timeout},
		{"gateway timeout", errors.New("victoriametrics: HTTP 504: gateway timeout"), Timeout},
		{"dial", fmt.Errorf("es: %w", opErr), Unreachable},
		{"refused text", errors.New("dial tcp 10.0.0.1:9200: connect: connection refused"), Unreachable},
		{"vm 503", errors.New("victoriametrics: HTTP 503: service unavailable"), Unreachable},
		{"not configured text", errors.New("log backend yapılandırılmamış"), NotConfigured},
		{"other", errors.New("strange failure"), Error},
		// v0.10.944 — ES root_cause sorguyu yankılar; sorgudaki kelime yetki hatası değil.
		{"es 400 query echo", errors.New(`ES histogram 400: all shards failed (search_phase_execution_exception): Failed to parse query [level:error AND "Unauthorized] (query_shard_exception)`), Error},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("%s: Classify=%q want %q", c.name, got, c.want)
		}
	}
}

func TestResultPrimaryAndFlags(t *testing.T) {
	cases := []struct {
		name  string
		o     Outcome
		want  State
		flags []State
	}{
		{"ok", Outcome{Returned: 5, Limit: 50}, OK, nil},
		{"empty", Outcome{Returned: 0, Limit: 50}, Empty, nil},
		{"truncated", Outcome{Returned: 50, Limit: 50, Truncated: true}, Truncated, nil},
		{"partial beats truncated", Outcome{Returned: 50, Partial: true, Truncated: true}, Partial, []State{Partial, Truncated}},
		{"partial and empty", Outcome{Returned: 0, Partial: true}, Partial, []State{Partial, Empty}},
		{"delayed", Outcome{Returned: 3, Delayed: true}, Delayed, nil},
	}
	for _, c := range cases {
		st := Result("logs", "elasticsearch", c.o)
		if st.State != c.want {
			t.Errorf("%s: state=%q want %q", c.name, st.State, c.want)
		}
		if fmt.Sprint(st.Flags) != fmt.Sprint(c.flags) {
			t.Errorf("%s: flags=%v want %v", c.name, st.Flags, c.flags)
		}
	}
}

func TestWithNoteMarksPartial(t *testing.T) {
	st := Result("logs", "elasticsearch", Outcome{Returned: 4}).WithNote("env filtresi uygulanamadı", true)
	if st.State != Partial || len(st.Notes) != 1 {
		t.Fatalf("got %+v", st)
	}
	// Empty + uygulanamayan filtre: birincil partial, empty bayrakta kalır.
	st = Result("logs", "elasticsearch", Outcome{}).WithNote("pod alanı bulunamadı", true)
	if st.State != Partial || fmt.Sprint(st.Flags) != fmt.Sprint([]State{Empty, Partial}) {
		t.Fatalf("got %+v", st)
	}
}

func TestSummaryEmptyIsNotNoError(t *testing.T) {
	s := Result("logs", "elasticsearch", Outcome{}).SummaryTR()
	if !strings.Contains(s, "'hata yok'") || !strings.Contains(s, "DEĞİL") {
		t.Fatalf("empty summary must say it is not 'no error': %q", s)
	}
	u := FromError("metrics", "victoriametrics", errors.New("victoriametrics: HTTP 401")).SummaryTR()
	if !strings.Contains(u, "kanıt YOK") {
		t.Fatalf("unauthorized summary must say there is no evidence: %q", u)
	}
}

func TestUsable(t *testing.T) {
	for _, s := range []State{OK, Empty, Partial, Truncated, Delayed} {
		if !(Status{State: s}).Usable() {
			t.Errorf("%s should be usable", s)
		}
	}
	for _, s := range []State{Unreachable, Unauthorized, Timeout, NotConfigured, Error} {
		if (Status{State: s}).Usable() {
			t.Errorf("%s should not be usable", s)
		}
	}
}

func TestWithWindowUTC(t *testing.T) {
	loc := time.FixedZone("TRT", 3*3600)
	st := Status{}.WithWindow(time.Date(2026, 9, 26, 15, 0, 0, 0, loc), time.Time{})
	if st.FromISO != "2026-09-26T12:00:00Z" || st.ToISO != "" {
		t.Fatalf("got %+v", st)
	}
}

func TestDetailCappedRuneSafe(t *testing.T) {
	st := FromError("logs", "elasticsearch", errors.New(strings.Repeat("ğ", 500)))
	if n := len([]rune(st.Detail)); n != 201 || !strings.HasSuffix(st.Detail, "…") {
		t.Fatalf("detail runes=%d", n)
	}
}

func TestIsCancelled(t *testing.T) {
	if !IsCancelled(fmt.Errorf("x: %w", context.Canceled)) || IsCancelled(context.DeadlineExceeded) || IsCancelled(nil) {
		t.Fatal("IsCancelled wrong")
	}
}

// TestUnauthorizedSignalsMatchToolErr — mcp protokol paketi depolama
// paketlerini içe aktaramadığı için 401/403 listesi orada ayrı duruyor;
// iki liste ayrışırsa bir kaynak "unauthorized" derken tool hatası
// "internal" der. Eşitlik burada pinli.
func TestUnauthorizedSignalsMatchToolErr(t *testing.T) {
	if fmt.Sprint(unauthorizedSignals) != fmt.Sprint(mcp.ToolErrUnauthorizedSignals) {
		t.Fatalf("liste ayrıştı:\n sourcestate=%v\n mcp=%v", unauthorizedSignals, mcp.ToolErrUnauthorizedSignals)
	}
	if got := mcp.ClassifyToolError(fmt.Errorf("x: %w", ErrUnauthorized)).Error; got != mcp.ToolErrUnauthorized {
		t.Fatalf("sentinel metni mcp'de %q sınıfına düştü", got)
	}
}
