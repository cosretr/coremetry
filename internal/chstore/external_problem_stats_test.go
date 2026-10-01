package chstore

// v0.10.998 — Oracle canlıya geçiş önizlemesinin kural önekleri.

import "testing"

func TestExternalRulePrefixes(t *testing.T) {
	series, cluster := ExternalRulePrefixes("core-oracle")
	if series != "anomaly:ext:core-oracle/" || cluster != "anomaly-cluster:ext:core-oracle/" {
		t.Fatalf("önekler: %q %q", series, cluster)
	}
	// Üreticinin yazdığı kural kimlikleriyle eşleşir (anomaly/external.go:
	// "anomaly:" + ExternalSubject + ":" + metrik; küme clusterRulePrefix + anahtar).
	for rule, want := range map[string]bool{
		"anomaly:ext:core-oracle/OP1/ERR_020/WEB/generic:ext:error_count": true,
		"anomaly-cluster:ext:core-oracle/OP1":                             true,
		"anomaly:ext:core-oracle2/OP1/x:ext:error_count":                  false, // başka kaynak ("/" sınırı)
		"anomaly:ext-down:ext:core-oracle":                                false, // sağlık Problem'i açılış sayımına girmez
		"anomaly:ext-cap:ext:core-oracle:ext:error_count":                 false, // tavan özeti de
		"anomaly:checkout:error_rate":                                     false,
	} {
		got := len(rule) >= len(series) && rule[:len(series)] == series || len(rule) >= len(cluster) && rule[:len(cluster)] == cluster
		if got != want {
			t.Errorf("%s: eşleşme %v, istenen %v", rule, got, want)
		}
	}
	// Seri öneki tip sistemiyle aynı kökten: notify sınıflaması anomali kalır.
	if ProblemNotifyKind(Problem{RuleID: series + "x:ext:error_count"}) != NotifyKindAnomaly ||
		ProblemNotifyKind(Problem{RuleID: cluster + "OP1"}) != NotifyKindAnomaly {
		t.Error("dış seri / küme Problem'i bildirimde anomali türüdür")
	}
}
