package evaluator

// ml_promoted_text_test.go — v0.10.1875 (öğrenme döngüsü Faz 2 parti B,
// audit §7.3 "bildirim konusu = tek satır özet"): ML olayının terfi kopyası
// (kind "ml:", Sample = ml.ExplainSummary) Problem'e özetle girer; manşet
// (bildirim konusu) bu cümle olur. Diğer anomali türleri bayt bayt aynı.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestPromotedRuleText(t *testing.T) {
	sum := "orders-api: hata logları normalin yaklaşık 9 katı, 13 dakikadır (14:31'den beri)."
	ml := chstore.AnomalyEvent{ID: "e9", Kind: "ml:error_rate", Pattern: "_error", Service: "orders-api", Sample: sum, PeakRatio: 9.4, CurrentCount: 640}
	name, desc := promotedRuleText(ml)
	if name != chstore.MLPromotedRuleNamePrefix+"_error" || desc != sum {
		t.Fatalf("ML terfisi özet taşımalı: %q / %q", name, desc)
	}
	p := chstore.Problem{RuleID: promoteAnomalyRuleID + ml.ID, RuleName: name, Description: desc, Service: ml.Service, Metric: "anomaly_ratio", Value: 9.4, Threshold: 2}
	if h, ok := chstore.MLPromotedHeadline(p); !ok || h != sum {
		t.Errorf("manşet özet olmalı: %q %v", h, ok)
	}

	old := chstore.AnomalyEvent{ID: "x", Kind: "log_pattern", Pattern: "timeout", Service: "orders-api", Sample: "ERROR timeout", PeakRatio: 4, CurrentCount: 12}
	name, desc = promotedRuleText(old)
	if name != "Anomaly · timeout" || !strings.HasPrefix(desc, "Auto-promoted from anomaly: log_pattern / timeout") {
		t.Errorf("ML dışı tür değişmez: %q / %q", name, desc)
	}
	empty := ml
	empty.Sample = " "
	if name, _ := promotedRuleText(empty); name != "Anomaly · _error" {
		t.Errorf("özetsiz ML kopyası eski yol: %q", name)
	}
	if _, ok := chstore.MLPromotedHeadline(chstore.Problem{RuleID: "anomaly:x:error_rate", RuleName: chstore.MLPromotedRuleNamePrefix + "x", Description: sum}); ok {
		t.Error("yalnız anomaly-auto: ailesi")
	}
}
