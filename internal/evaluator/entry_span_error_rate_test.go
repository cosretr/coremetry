package evaluator

import (
	"os"
	"strings"
	"testing"
)

// v0.10.783 — error_rate kuralı giriş span'lerinden (server/consumer):
// hem toplu yol (chstore.measureAllServicesPlan) hem tek-servis yolu
// (measure) aynı süzgeci taşımalı; ikisi ayrışırsa prefetch düşen tikte
// kural farklı sayıyı görür.
func TestErrorRateUsesEntrySpansOnBothPaths(t *testing.T) {
	src, err := os.ReadFile("evaluator.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "func (e *Evaluator) measure(")
	if i < 0 {
		t.Fatal("measure yok")
	}
	i = strings.Index(s[i:], `case "error_rate":`) + i
	branch := s[i:]
	if j := strings.Index(branch, `case "error_count":`); j > 0 {
		branch = branch[:j]
	}
	if strings.Count(branch, "chstore.EntrySpanKindsWhere") != 2 {
		t.Errorf("error_rate dalının iki yolu da (MV + ham) giriş-span süzgecini taşımalı:\n%s", branch)
	}
	if strings.Contains(branch, "service_summary_5m") {
		t.Error("service_summary_5m kind taşımaz — giriş-span oranı oradan okunamaz")
	}
}
