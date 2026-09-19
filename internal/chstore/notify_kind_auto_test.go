package chstore

import "testing"

// v0.10.814 — anomali tür boşlukları (operatör: "anomali maillerini kapatamıyorum").
func TestProblemNotifyKindPromotedAndExternalHealth(t *testing.T) {
	cases := map[string]string{
		PromotedAnomalyRulePrefix + "ev-1":             NotifyKindAnomaly, // evaluator promoteStrongAnomalies
		RuleExtDownPrefix + "extsrc/OP1":               NotifyKindProblem, // deterministik kaynak sağlığı
		RuleExtCapPrefix + "extsrc/OP1:ext:fail_count": NotifyKindAnomaly, // istatistik motorunun taşma özeti
		"anomaly-autox:ev":                             NotifyKindProblem, // önek sözcük sınırı
		"anomaly:shop:p99_ms":                          NotifyKindAnomaly,
	}
	for rid, want := range cases {
		if got := ProblemNotifyKind(Problem{RuleID: rid}); got != want {
			t.Errorf("RuleID=%q → %q, istenen %q", rid, got, want)
		}
	}
}

func TestTeamContactsKindAllows(t *testing.T) {
	if !(TeamContacts{}).KindAllows(NotifyKindAnomaly) {
		t.Error("boş süzgeç her türü geçirir")
	}
	tc := TeamContacts{Kinds: []string{NotifyKindProblem, NotifyKindIncident}}
	if tc.KindAllows(NotifyKindAnomaly) || !tc.KindAllows(NotifyKindProblem) || !tc.KindAllows(NotifyKindIncident) {
		t.Error("dolu süzgeç yalnız listedekileri geçirir")
	}
}
