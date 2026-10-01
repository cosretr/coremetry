package notify

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1027 — kural problemi imzasının sunucu tanımı chstore'da TEK:
// bildirim hunisi ve /api/databases/problems ("problem değil" dışlaması) aynı
// fonksiyonu çağırır. Biri kendi kopyasını yazarsa öğretilen karar bir
// yüzeyde tutar, ötekinde tutmaz.
func TestVerdictSignatureSharedWithChstore(t *testing.T) {
	for _, p := range []chstore.Problem{
		{ID: "x1", RuleID: chstore.RuleDBCapacityPrefix + "oracle-sessions", Service: "db:oracle@core-db", Kind: chstore.ProblemKindDB},
		{ID: "x2", RuleID: "db-slow-stmt", Service: "db:postgresql@orders", Kind: chstore.ProblemKindDB},
		{ID: "x3", RuleID: "rule-1", Service: "payments"},
		{ID: "x4", Service: "payments"},
	} {
		if got, want := verdictSignature(p), chstore.RuleProblemVerdictSignature(p.RuleID, p.Service); got != want {
			t.Errorf("%s: notify %q, chstore %q", p.ID, got, want)
		}
	}
	src, err := os.ReadFile("verdict_silence.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		"chstore.RuleProblemVerdictSignature(p.RuleID, p.Service)",
		"chstore.NoiseVerdictSignatures(list)",
	} {
		if !strings.Contains(string(src), w) {
			t.Errorf("verdict_silence.go %q çağırmalı (kopya tanım yok)", w)
		}
	}
	if strings.Contains(string(src), `"p:" + p.RuleID`) {
		t.Error("verdict_silence.go kural imzasını elle kuruyor — chstore tanımını kullan")
	}
}
