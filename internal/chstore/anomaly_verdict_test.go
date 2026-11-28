package chstore

// anomaly_verdict_test.go — v0.10.181 saf sözleşme: iki karar değeri; IN
// listesi yer tutucuyla (kullanıcı kimliği SQL'e dizgiyle girmez).

import "testing"

func TestValidVerdict(t *testing.T) {
	for v, want := range map[string]bool{"anomaly": true, "not_anomaly": true, "": false, "yes": false, "Anomaly": false} {
		if got := ValidVerdict(v); got != want {
			t.Errorf("ValidVerdict(%q)=%v want %v", v, got, want)
		}
	}
}

func TestVerdictInPlaceholders(t *testing.T) {
	for n, want := range map[int]string{0: "", 1: "?", 3: "?,?,?"} {
		if got := verdictInPlaceholders(n); got != want {
			t.Errorf("verdictInPlaceholders(%d)=%q want %q", n, got, want)
		}
	}
}

// v0.10.2256 — "Sorun değil" nedeni: kapalı küme, yalnız not_anomaly'de.
func TestNormalizeVerdictReason(t *testing.T) {
	cases := []struct {
		verdict, reason, want string
		ok                    bool
	}{
		{VerdictNotAnomaly, "no_impact", "no_impact", true},
		{VerdictNotAnomaly, " expected ", "expected", true},
		{VerdictNotAnomaly, "", "", true},
		{VerdictNotAnomaly, "free text", "", false},
		{VerdictAnomaly, "no_impact", "", true}, // "gerçek" oyunda neden düşer
	}
	for _, c := range cases {
		got, ok := NormalizeVerdictReason(c.verdict, c.reason)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeVerdictReason(%q,%q)=(%q,%v) want (%q,%v)", c.verdict, c.reason, got, ok, c.want, c.ok)
		}
	}
}

// v0.10.2256 — kolon yokken (eski replika) INSERT/SELECT reason'ı anmaz.
func TestVerdictColsReasonGated(t *testing.T) {
	without, with := verdictCols(false, false), verdictCols(true, false)
	for _, c := range without {
		if c == "reason" || c == "origin" {
			t.Fatal("kolon görülmeden reason / origin yazılmamalı")
		}
	}
	if len(with) != len(without)+1 || with[len(with)-1] != "reason" {
		t.Fatalf("reason sona eklenmeli: %v", with)
	}
	// v0.10.2484 — origin (kuyruk işareti) en sonda, reason'dan sonra.
	both := verdictCols(true, true)
	if len(both) != len(without)+2 || both[len(both)-2] != "reason" || both[len(both)-1] != "origin" {
		t.Fatalf("origin reason'dan sonra eklenmeli: %v", both)
	}
}
