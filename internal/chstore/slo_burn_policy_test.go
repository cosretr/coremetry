package chstore

import (
	"os"
	"strings"
	"testing"
	"time"
)

// v0.10.794 — burn pencereleri tek kaynak: değerler SRE Workbook, tüketiciler
// (evaluator alarmı, explain-slo ucu) yerel literal taşımaz.
func TestBurnPoliciesAreWorkbookValues(t *testing.T) {
	if len(BurnPolicies) != 2 {
		t.Fatalf("iki bant bekleniyor, %d var", len(BurnPolicies))
	}
	c, w := BurnPolicies[0], BurnPolicies[1]
	if c.Severity != "critical" || c.FastWindow != time.Hour || c.FastRate != 14.4 || c.SlowWindow != 6*time.Hour || c.SlowRate != 6.0 {
		t.Errorf("critical bandı: %+v", c)
	}
	if w.Severity != "warning" || w.FastWindow != 6*time.Hour || w.FastRate != 6.0 || w.SlowWindow != 24*time.Hour || w.SlowRate != 3.0 {
		t.Errorf("warning bandı: %+v", w)
	}
	for _, p := range BurnPolicies {
		if p.FastWindow >= p.SlowWindow || p.FastRate <= p.SlowRate {
			t.Errorf("%s: hızlı pencere kısa ve eşiği yüksek olmalı: %+v", p.Severity, p)
		}
	}
	if BurnExplainPolicy().Severity != "critical" {
		t.Error("açıklama ucu critical çiftini ölçmeli — problemdeki burn_rate_60m ile aynı")
	}
}

func TestBurnPolicyConsumersUseTheSingleSource(t *testing.T) {
	for _, f := range []string{"../evaluator/slo_burn.go", "../api/copilot_explain_slo.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		s := string(b)
		if !strings.Contains(s, "chstore.BurnPolicies") && !strings.Contains(s, "chstore.BurnExplainPolicy()") {
			t.Errorf("%s: pencereleri chstore.BurnPolicies / BurnExplainPolicy'den okumalı", f)
		}
		for _, lit := range []string{"5 * time.Minute", "5*time.Minute", "fastRate: 14.4", "var burnPolicies"} {
			if strings.Contains(s, lit) {
				t.Errorf("%s: yerel pencere/eşik literali %q — tek kaynak dışına çıkıldı", f, lit)
			}
		}
	}
}
