package chstore

import "testing"

// v0.10.1027 — db problem öznesinin BİÇİMİ kuraldan türer; "problem değil"
// imzasının sunucu tanımı tek yerde.

func TestDBProblemSubjectForm(t *testing.T) {
	for _, c := range []struct {
		ruleID, want string
	}{
		{RuleDBCapacityPrefix + "oracle-tablespace", DBSubjectFormInstance},
		{RuleDBCapacityPrefix + "postgres-connections", DBSubjectFormInstance},
		{"db-slow-stmt", DBSubjectFormDBName},
		{"3f2a9c1e-target-rule", DBSubjectFormDBName}, // hedefli kural (kullanıcı kuralı kimliği)
		{"", DBSubjectFormDBName},
		{"db-capacity", DBSubjectFormDBName}, // öneksiz ad iki nokta taşımaz
	} {
		if got := DBProblemSubjectForm(c.ruleID); got != c.want {
			t.Errorf("%q: %q, beklenen %q", c.ruleID, got, c.want)
		}
	}
	if RuleDBCapacityPrefix != "db-capacity:" {
		t.Errorf("önek değişti: %q — FE DB_CAPACITY_RULE_PREFIX ve kayıtlı rule_id'ler buna bağlı", RuleDBCapacityPrefix)
	}
}

func TestRuleProblemVerdictSignature(t *testing.T) {
	if got := RuleProblemVerdictSignature("db-slow-stmt", "db:oracle@CORE"); got != "p:db-slow-stmt|db:oracle@CORE" {
		t.Errorf("imza %q", got)
	}
	if got := RuleProblemVerdictSignature("rule-1", ""); got != "p:rule-1|" {
		t.Errorf("öznesiz kural %q", got)
	}
	if got := RuleProblemVerdictSignature("", "db:oracle@CORE"); got != "" {
		t.Errorf("kural kimliği yoksa imza yok, gelen %q", got)
	}
	if !ValidProblemSignature(RuleProblemVerdictSignature(RuleDBCapacityPrefix+"oracle-sessions", "db:oracle@core-db")) {
		t.Error("üretilen imza yazma kapısından geçmeli")
	}
}

func TestNoiseVerdictSignatures(t *testing.T) {
	set := NoiseVerdictSignatures([]ProblemVerdict{
		{Signature: "p:r|s", Verdict: ProblemVerdictNoise},
		{Signature: "e:fp", Verdict: ProblemVerdictReal},
		{Signature: "e:fp2", Verdict: ProblemVerdictNoise},
	})
	if _, ok := set["p:r|s"]; !ok || len(set) != 2 {
		t.Fatalf("yalnız noise imzaları: %v", set)
	}
	if _, ok := set["e:fp"]; ok {
		t.Error("\"gerçek\" karar kümeye girmemeli")
	}
	if got := NoiseVerdictSignatures(nil); got == nil || len(got) != 0 {
		t.Errorf("boş liste boş küme: %v", got)
	}
}
