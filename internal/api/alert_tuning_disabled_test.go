package api

// alert_tuning_disabled_test.go — v0.10.1069: yerleşik kurallar varsayılan
// kapalı. "Noisy rules" raporu son 24 saate bakar; kapatılan yerleşikler
// (prod: HTTP P99 >3s 340×, >5s 215×) bu pencerede hâlâ en gürültülü
// görünür ve panel ZATEN KAPALI kural için "Disable" önerirdi. Kapalı kural
// rapordan düşer; kural tablosunda olmayan rule_id'ler (anomali vb.) kalır.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestDropDisabledNoisy(t *testing.T) {
	byID := map[string]chstore.AlertRule{
		"builtin-warn-http-p99-3s": {ID: "builtin-warn-http-p99-3s", Enabled: false, BuiltIn: true},
		"builtin-http-p99-5s":      {ID: "builtin-http-p99-5s", Enabled: false, BuiltIn: true},
		"user-a":                   {ID: "user-a", Enabled: true},
		"user-b":                   {ID: "user-b", Enabled: true},
	}
	rows := []chstore.NoisyRule{
		{RuleID: "builtin-warn-http-p99-3s", OpenCount: 340},
		{RuleID: "builtin-http-p99-5s", OpenCount: 215},
		{RuleID: "user-a", OpenCount: 40},
		{RuleID: "anomaly:svc-alpha:p99_ms", OpenCount: 20},
		{RuleID: "user-b", OpenCount: 5},
	}
	tests := []struct {
		name  string
		limit int
		want  []string
	}{
		{"kapalılar düşer, sıra korunur", 10, []string{"user-a", "anomaly:svc-alpha:p99_ms", "user-b"}},
		{"limit düşmeden SONRA uygulanır", 2, []string{"user-a", "anomaly:svc-alpha:p99_ms"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dropDisabledNoisy(rows, byID, tt.limit)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d rows %+v, want %v", len(got), got, tt.want)
			}
			for i := range got {
				if got[i].RuleID != tt.want[i] {
					t.Errorf("[%d] = %s, want %s", i, got[i].RuleID, tt.want[i])
				}
			}
		})
	}

	// Hepsi kapalı: boş liste (nil değil) — panel kendini gizler, hata değil.
	allOff := map[string]chstore.AlertRule{
		"builtin-warn-http-p99-3s": byID["builtin-warn-http-p99-3s"],
		"builtin-http-p99-5s":      byID["builtin-http-p99-5s"],
	}
	if got := dropDisabledNoisy(rows[:2], allOff, 10); got == nil || len(got) != 0 {
		t.Fatalf("hepsi kapalı → boş dilim beklenir, got %#v", got)
	}
}

func TestNoisyFetchLimit(t *testing.T) {
	rules := []chstore.AlertRule{{ID: "a", Enabled: false}, {ID: "b", Enabled: false}, {ID: "c", Enabled: true}}
	if got := noisyFetchLimit(10, rules); got != 12 {
		t.Errorf("10 + 2 kapalı = %d, want 12", got)
	}
	many := make([]chstore.AlertRule, 500)
	if got := noisyFetchLimit(30, many); got != 200 {
		t.Errorf("store tavanı 200 olmalı, got %d", got)
	}
}
