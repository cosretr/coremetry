package anomaly

// notify_kind_pin_test.go — v0.10.747: bu paketin ÜRETTİĞİ RuleID'ler
// chstore.ProblemNotifyKind'da "anomaly" çıkmalı. Sınıflandırıcı öneki
// kendi metninde tekrar yazar (chstore anomaly'yi import edemez); sabit
// burada değişirse kanal süzgeci sessizce kural tarafına kayardı — pin
// sabitin SAHİBİNDE.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestAnomalyRuleIDsClassifyAsAnomaly(t *testing.T) {
	for _, rid := range []string{
		"anomaly:" + "shop" + ":" + "p99_ms",                  // anomaly.go metrik anomalisi
		"anomaly:" + "shop" + ":service_silent",               // anomaly.go service_silent
		clusterRulePrefix + "shop-payment",                    // clustering.go / external.go
		exceptionStormRuleID,                                  // exception_storm.go
		"anomaly:" + "extsrc/OP1/E1" + ":" + "ext:fail_count", // external.go
	} {
		if got := chstore.ProblemNotifyKind(chstore.Problem{RuleID: rid}); got != chstore.NotifyKindAnomaly {
			t.Errorf("RuleID=%q → %q, anomaly bekleniyordu", rid, got)
		}
	}
}

// v0.10.814 — dış kaynak sağlığı deterministik: "anomaly:ext-down:" ANOMALİ
// DEĞİL problem (Anomali tikini kaldıran operatör kaynak-düştü alarmını
// kaybetmemeli); "ext-cap" istatistik motorunun taşma özeti → anomali kalır.
func TestExternalHealthRuleIDsClassify(t *testing.T) {
	if got := chstore.ProblemNotifyKind(chstore.Problem{RuleID: chstore.RuleExtDownPrefix + "extsrc/OP1"}); got != chstore.NotifyKindProblem {
		t.Errorf("ext-down → %q, problem bekleniyordu", got)
	}
	if got := chstore.ProblemNotifyKind(chstore.Problem{RuleID: chstore.RuleExtCapPrefix + "extsrc/OP1:ext:fail_count"}); got != chstore.NotifyKindAnomaly {
		t.Errorf("ext-cap → %q, anomaly bekleniyordu", got)
	}
}
