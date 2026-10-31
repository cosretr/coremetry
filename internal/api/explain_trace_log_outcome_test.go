package api

// v0.10.1742 (operatör onayı) — trace açıklamasının log okuması Kanıt
// satırına üç hâlle girer; okunamama "log yok" diye yutulmaz.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

func TestClassifyTraceLogReadAndRender(t *testing.T) {
	three := &logstore.Page{Logs: []*logstore.LogRecord{{}, {}, {}}}
	cases := []struct {
		name    string
		storeOK bool
		page    *logstore.Page
		err     error
		want    traceLogOutcome
		wantTR  string
	}{
		{"satır okundu", true, three, nil, traceLogOutcome{State: "rows", Rows: 3}, "3 log satırı"},
		{"0 satır", true, &logstore.Page{}, nil, traceLogOutcome{State: "none"}, "log yok"},
		{"nil sayfa", true, nil, nil, traceLogOutcome{State: "none"}, "log yok"},
		{"zaman aşımı", true, nil, fmt.Errorf("es: %w", context.DeadlineExceeded), traceLogOutcome{State: "failed", Reason: "timeout"}, "log okunamadı (zaman aşımı)"},
		{"hata", true, nil, errors.New("es: 500 internal"), traceLogOutcome{State: "failed", Reason: "error"}, "log okunamadı (hata)"},
		{"yapılandırılmamış", true, nil, fmt.Errorf("x: %w", sourcestate.ErrNotConfigured), traceLogOutcome{State: "failed", Reason: "nostore"}, "log okunamadı (log deposu yok)"},
		{"depo yok", false, nil, nil, traceLogOutcome{State: "failed", Reason: "nostore"}, "log okunamadı (log deposu yok)"},
	}
	for _, c := range cases {
		got := classifyTraceLogRead(c.storeOK, c.page, c.err)
		if got != c.want {
			t.Errorf("%s: classify = %+v, want %+v", c.name, got, c.want)
		}
		if tr := traceLogEvidenceTR(got); tr != c.wantTR {
			t.Errorf("%s: render = %q, want %q", c.name, tr, c.wantTR)
		}
	}
	if traceLogEvidenceTR(traceLogOutcome{}) != "" {
		t.Error("boş hâl metin üretmemeli")
	}
}

func TestSanitizeTraceLogOutcome(t *testing.T) {
	cases := []struct {
		in   *traceLogOutcome
		want *traceLogOutcome
	}{
		{nil, nil},
		{&traceLogOutcome{}, nil},
		{&traceLogOutcome{State: "rows", Rows: 0}, nil},
		{&traceLogOutcome{State: "rows", Rows: 12, Reason: "x"}, &traceLogOutcome{State: "rows", Rows: 12}},
		{&traceLogOutcome{State: "none", Rows: 5}, &traceLogOutcome{State: "none"}},
		{&traceLogOutcome{State: "failed", Reason: "timeout"}, &traceLogOutcome{State: "failed", Reason: "timeout"}},
		{&traceLogOutcome{State: "failed", Reason: "<script>"}, &traceLogOutcome{State: "failed", Reason: "error"}},
		{&traceLogOutcome{State: "bogus"}, nil},
	}
	for i, c := range cases {
		got := sanitizeTraceLogOutcome(c.in)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("#%d: sanitize(%+v) = %+v, want %+v", i, c.in, got, c.want)
		}
	}
}

// Sonuç girdide taşınır ve iki yüzeye (sohbet künyesi + ✨ extra) gider;
// prompt'a girmez.
func TestTraceLogOutcomeWired(t *testing.T) {
	read := func(f string) string {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	in := read("explain_trace_input.go")
	if !strings.Contains(in, "logOutcome = classifyTraceLogRead(true, page, lerr)") || !strings.Contains(in, "Logs:            logOutcome") {
		t.Fatal("explain_trace_input log sonucunu taşımıyor")
	}
	if !strings.Contains(read("chat_explain_turn.go"), "Logs: sanitizeTraceLogOutcome(&in.Logs)") {
		t.Fatal("sohbet künyesi log sonucunu taşımıyor")
	}
	if !strings.Contains(read("explain_trace_oracle.go"), `extra["logs"] = lo`) {
		t.Fatal("✨ extra log sonucunu taşımıyor")
	}
	if strings.Contains(read("explain_trace_log_outcome.go"), "User") {
		t.Fatal("log sonucu prompt'a (User) girmemeli")
	}
}
