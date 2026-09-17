package evaluator

// notify_kind_pin_test.go — v0.10.747: evaluator'ın ürettiği RuleID'lerin
// kanal türü. Exception patlamaları (fatal altyapı / paylaşılan bağımlılık)
// otomatik tespittir → "anomaly" (operatör kararı 2026-09-17); kural,
// SLO, DB, runtime, watcher → "problem". Pin sabitin SAHİBİNDE.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEvaluatorRuleIDsClassify(t *testing.T) {
	cases := []struct {
		ruleID string
		want   string
	}{
		{fatalExcRuleID, chstore.NotifyKindAnomaly},    // fatal_exception.go
		{sharedBurstRuleID, chstore.NotifyKindAnomaly}, // shared_exception.go
		{dbSlowStmtRuleID, chstore.NotifyKindProblem},  // db_slow_statement.go
		{"runtime:jvm-gc", chstore.NotifyKindProblem},  // runtime_vm.go
		{"slo:slo-1:critical", chstore.NotifyKindProblem},
		{"builtin-error-rate", chstore.NotifyKindProblem},
		{"r-1a2b3c", chstore.NotifyKindProblem}, // operatör kuralı (r.ID)
	}
	for _, c := range cases {
		if got := chstore.ProblemNotifyKind(chstore.Problem{RuleID: c.ruleID}); got != c.want {
			t.Errorf("RuleID=%q → %q, istenen %q", c.ruleID, got, c.want)
		}
	}
}
