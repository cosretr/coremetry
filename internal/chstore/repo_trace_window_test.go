package chstore

// repo_trace_window_test.go — v0.10.690: GetTrace'in kademeli pencere probe'u
// TraceWindow'a ayrıldı; iki çağıran (GetTrace, /api/logs/search trace dalı)
// aynı sınırlı yolu kullanır. Kaynak pini: GetTrace probe'u kendi içinde
// yeniden yazmaz; TraceWindow kademeleri + max_execution_time taşır.

import (
	"os"
	"strings"
	"testing"
)

func TestGetTraceUsesTraceWindow(t *testing.T) {
	src, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	g := strings.Index(s, "func (s *Store) GetTrace(")
	end := strings.Index(s[g:], "\n}\n")
	body := s[g : g+end]
	if !strings.Contains(body, "s.TraceWindow(ctx, traceID)") {
		t.Error("GetTrace pencereyi TraceWindow'dan almalı")
	}
	if strings.Contains(body, "range traceWindowSteps") {
		t.Error("GetTrace probe döngüsünü kendi içinde tekrarlamamalı")
	}
	w := strings.Index(s, "func (s *Store) TraceWindow(")
	wend := strings.Index(s[w:], "\n}\n")
	wb := s[w : w+wend]
	for _, want := range []string{"range traceWindowSteps", "max_execution_time = 3", "traceTimeBound(winStart, winEndNanos)"} {
		if !strings.Contains(wb, want) {
			t.Errorf("TraceWindow %q taşımalı", want)
		}
	}
}

// v0.10.2201 (operator-reported) — probe hatası kademeyi durdurmaz; yalnız
// iptal edilmiş ctx durdurur.
func TestTraceWindowProbeErrorContinuesCascade(t *testing.T) {
	b, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	w := strings.Index(s, "func (s *Store) TraceWindow(")
	wb := s[w : w+strings.Index(s[w:], "\n}\n")]
	if !strings.Contains(wb, "if ctx.Err() != nil {\n\t\t\t\tbreak\n\t\t\t}\n\t\t\tcontinue") {
		t.Error("TraceWindow probe hatasında sonraki pencereye geçmeli (iptalde durur)")
	}
}
