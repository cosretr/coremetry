package evaluator

// ml_promoted_text_test.go — v0.10.2067: ML olayının "ml:" terfi kopyası
// (ml/promote.go, v0.10.1761/1875) kaldırıldı. ML Problem'ini yalnız grup
// yolu açar (ml/group_live.go, rule id anomaly-auto:group:*); genel terfi
// metni her anomali türünde aynı gövdeyi yazar. Eski "ml:" satırı kalırsa
// (TTL içinde) özel dal YOK — kopya yolunun geri gelmediğini pinler.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestPromotedRuleText(t *testing.T) {
	old := chstore.AnomalyEvent{ID: "x", Kind: "log_pattern", Pattern: "timeout", Service: "orders-api", Sample: "ERROR timeout", PeakRatio: 4, CurrentCount: 12}
	name, desc := promotedRuleText(old)
	if name != "Anomaly · timeout" || !strings.HasPrefix(desc, "Auto-promoted from anomaly: log_pattern / timeout") {
		t.Errorf("genel terfi metni: %q / %q", name, desc)
	}
	ml := chstore.AnomalyEvent{ID: "e9", Kind: "ml:error_rate", Pattern: "_error", Service: "orders-api", Sample: "özet", PeakRatio: 9.4, CurrentCount: 640}
	if name, _ := promotedRuleText(ml); strings.HasPrefix(name, chstore.MLPromotedRuleNamePrefix) {
		t.Errorf("ml: kopya dalı kaldırıldı, ad ML öneki taşımamalı: %q", name)
	}
	if _, ok := chstore.MLPromotedHeadline(chstore.Problem{RuleID: "anomaly:x:error_rate", RuleName: chstore.MLPromotedRuleNamePrefix + "x", Description: "özet"}); ok {
		t.Error("yalnız anomaly-auto: ailesi")
	}
}
