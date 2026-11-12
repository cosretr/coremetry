package api

// Log Search Faz 6 dilim 6.4 — satır + runbook kanıt paketinin saf parçaları.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestLogRunbookQuery(t *testing.T) {
	ex := &logstore.ExceptionSummary{Type: "java.lang.OutOfMemoryError", Message: "Java heap space", Cause: "java.io.IOException", Class: "resource"}
	q := logRunbookQuery("boom body", "Out of memory", ex)
	if !strings.HasPrefix(q, "OutOfMemoryError IOException resource Java heap space Out of memory") || !strings.HasSuffix(q, "boom body") {
		t.Fatalf("sıra: %q", q)
	}
	if q := logRunbookQuery("only body", "", nil); q != "only body" {
		t.Fatalf("exception yok: %q", q)
	}
	if q := logRunbookQuery("b", "", &logstore.ExceptionSummary{Type: "System.TimeoutException"}); !strings.HasPrefix(q, "TimeoutException") {
		t.Fatalf(".NET kısa ad: %q", q)
	}
}

func TestRenderLogRunbookTR(t *testing.T) {
	line := &logstore.LogRecord{Timestamp: 1762941971204000000, ServiceName: "orders-api", Severity: 17, Body: "java.lang.OutOfMemoryError: Java heap space"}
	cases := []struct {
		name string
		in   logRunbookInput
		want []string
	}{
		{"eşleşme", logRunbookInput{Line: line, Pod: "orders-api-1", Exact: true, Pattern: "Out of memory",
			Exception: &logstore.ExceptionSummary{Type: "java.lang.OutOfMemoryError", Class: "resource", Critical: true, Frames: 30, AppFrames: 2, Top: []string{"A.b:1"}},
			Matches:   []mcptools.RunbookMatch{{ID: "rb1", Title: "JVM heap", Score: 0.75}}, BestSteps: "JVM heap\n1) heap"},
			[]string{"servis=orders-api · pod=orders-api-1 · seviye=ERROR", "DESEN: Out of memory", "EXCEPTION: tür=java.lang.OutOfMemoryError",
				"KRİTİK", "ilk uygulama çerçeveleri: A.b:1", `1. "JVM heap" — skor 0.75`, "EN İYİ EŞLEŞMENİN ADIMLARI"}},
		{"runbook yok", logRunbookInput{Line: line, Exact: false}, []string{"NOT: tam eşleşme bulunamadı", "EXCEPTION: yok", "RUNBOOK: eşleşen runbook yok"}},
		{"runbook okunamadı", logRunbookInput{Line: line, Exact: true, RunbookErr: true}, []string{"RUNBOOK: okunamadı"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := renderLogRunbookTR(c.in)
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("eksik %q\n%s", w, out)
				}
			}
		})
	}
	big := &logstore.LogRecord{Timestamp: 1, ServiceName: "s", Body: strings.Repeat("x", 20000)}
	if out := renderLogRunbookTR(logRunbookInput{Line: big, Exact: true, BestSteps: strings.Repeat("y", 5000)}); len(out) > logRunbookInputMax+8 {
		t.Errorf("paket tavanı aşıldı: %d", len(out))
	}
}

func TestRunbookStepsText(t *testing.T) {
	steps := []chstore.RunbookStep{{Title: "Heap'e bak", Instructions: "JVM sekmesi"}, {Title: "Dump al"}}
	got := runbookStepsText("JVM heap", steps, 1000)
	if got != "JVM heap\n1) Heap'e bak — JVM sekmesi\n2) Dump al\n" {
		t.Fatalf("got %q", got)
	}
	if r := []rune(runbookStepsText("T", steps, 5)); len(r) != 6 || string(r[5]) != "…" {
		t.Fatalf("kesim: %q", string(r))
	}
}
