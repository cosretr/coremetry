package chstore

import "testing"

// v0.10.782 — exception türü: sınıflandırma, OPT-IN süzgeç, öncelik koruma.
func TestNotifyKindException(t *testing.T) {
	if ProblemNotifyKind(Problem{RuleID: "exception-group:new"}) != NotifyKindException {
		t.Error("exception-group: → exception")
	}
	if ProblemNotifyKind(Problem{RuleID: "exception:shared-dependency"}) != NotifyKindAnomaly {
		t.Error("exception: (paylaşılan/ölümcül kuralları) anomaly kalır")
	}
	if !IsNotifyKind("exception") || len(NotifyKindsAll) != 4 {
		t.Error("NotifyKindsAll dört tür")
	}
	empty := ChannelMatchRules{}
	if !empty.allowsKind(NotifyKindProblem) || !empty.allowsKind(NotifyKindIncident) || empty.allowsKind(NotifyKindException) {
		t.Error("boş süzgeç: problem/anomali/incident evet, exception HAYIR (opt-in)")
	}
	explicit := ChannelMatchRules{Kinds: []string{NotifyKindException}}
	if !explicit.allowsKind(NotifyKindException) || explicit.allowsKind(NotifyKindProblem) {
		t.Error("açık seçim: yalnız seçilen")
	}
	p := Problem{RuleID: "exception-group:new", Severity: "warning", Priority: "P2", PriorityReason: "merdiven"}
	if pr, why := computePriority(p, 0, ProblemPriorityConfig{}); pr != "P2" || why != "merdiven" {
		t.Errorf("önceden hesaplanmış öncelik korunmalı: %s (%s)", pr, why)
	}
	if pr, _ := computePriority(Problem{RuleID: "exception-group:new", Severity: "warning"}, 0, ProblemPriorityConfig{}); pr == "" {
		t.Error("öncelik boşsa merdiven düşer, boş dönmez")
	}
}
