package chstore

// v0.10.1587 — Davis paritesi FAZ 0.5 / B: sayım-oran. countOnlyMinCount
// varsayılan 20; eski blob (alan yok) varsayılanı alır; MinCount'un altı
// MinCount'a kelepçelenir.

import (
	"encoding/json"
	"testing"
)

func TestAnomalyPromotionCountOnlyMinCountDefault(t *testing.T) {
	if d := DefaultAnomalyPromotion(); d.CountOnlyMinCount != 20 {
		t.Fatalf("varsayılan 20: %d", d.CountOnlyMinCount)
	}
	var legacy AnomalyPromotionConfig
	_ = json.Unmarshal([]byte(`{"enabled":true,"minPeakRatio":5,"minSustainedSec":300,"minCount":10,"criticalPeakRatio":20}`), &legacy)
	if n := NormalizeAnomalyPromotion(legacy); n.CountOnlyMinCount != 20 {
		t.Fatalf("eski blob varsayılanı almalı: %d", n.CountOnlyMinCount)
	}
	if n := NormalizeAnomalyPromotion(AnomalyPromotionConfig{MinCount: 50, CountOnlyMinCount: 20}); n.CountOnlyMinCount != 50 {
		t.Fatalf("MinCount'un altı kelepçelenmeli: %d", n.CountOnlyMinCount)
	}
	if n := NormalizeAnomalyPromotion(AnomalyPromotionConfig{CountOnlyMinCount: 100}); n.CountOnlyMinCount != 100 || n.MinCount != 10 {
		t.Fatalf("operatör değeri korunur: %+v", n)
	}
}

func TestIsCountOnlyAnomaly(t *testing.T) {
	for _, c := range []struct {
		ev   AnomalyEvent
		want bool
	}{
		{AnomalyEvent{Kind: "trace_op", PeakRatio: 0}, true},
		{AnomalyEvent{Kind: "log_pattern", PeakRatio: 0}, true},
		{AnomalyEvent{Kind: "trace_op", PeakRatio: 4.2}, false},
		// Oransız başka türler (yeni log şablonu) sayım kapısına GİRMEZ:
		// bugün hiç terfi etmiyorlar, bu sürüm onları açmamalı.
		{AnomalyEvent{Kind: "log_template_new", PeakRatio: 0}, false},
		{AnomalyEvent{Kind: "behavior_change", PeakRatio: 0}, false},
	} {
		if got := IsCountOnlyAnomaly(c.ev); got != c.want {
			t.Errorf("%+v: %v, want %v", c.ev, got, c.want)
		}
	}
}
