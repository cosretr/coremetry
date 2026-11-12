package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestApplyBackgroundDefaults locks in the zero-value → canonical
// fallback for the cadence knobs. Pre-v0.4.95 these were magic
// numbers in main.go (2*time.Minute / 30*time.Second / etc.);
// the audit's "config-ize hardcoded TTLs" hit moved them here.
// Test catches a future contributor who pulls a default out of
// the defaults struct without updating the applier.
func TestApplyBackgroundDefaults(t *testing.T) {
	t.Run("zero values fall back to defaults", func(t *testing.T) {
		b := BackgroundConfig{}
		applyBackgroundDefaults(&b)
		if b.AnomalyInterval != 2*time.Minute {
			t.Errorf("AnomalyInterval: got %v, want 2m", b.AnomalyInterval)
		}
		if b.AnomalyRecordInterval != 1*time.Minute {
			t.Errorf("AnomalyRecordInterval: got %v, want 1m", b.AnomalyRecordInterval)
		}
		if b.AnomalyRecordBackfill != 5*time.Minute {
			t.Errorf("AnomalyRecordBackfill: got %v, want 5m", b.AnomalyRecordBackfill)
		}
		if b.SMTPCacheTTL != 30*time.Second {
			t.Errorf("SMTPCacheTTL: got %v, want 30s", b.SMTPCacheTTL)
		}
		if b.StatusProbeTimeout != 5*time.Second {
			t.Errorf("StatusProbeTimeout: got %v, want 5s", b.StatusProbeTimeout)
		}
	})
	t.Run("explicit values pass through", func(t *testing.T) {
		b := BackgroundConfig{
			AnomalyInterval:    10 * time.Second,
			StatusProbeTimeout: 1 * time.Second,
		}
		applyBackgroundDefaults(&b)
		if b.AnomalyInterval != 10*time.Second {
			t.Errorf("explicit AnomalyInterval clobbered: got %v", b.AnomalyInterval)
		}
		if b.StatusProbeTimeout != 1*time.Second {
			t.Errorf("explicit StatusProbeTimeout clobbered: got %v", b.StatusProbeTimeout)
		}
		// Untouched fields still get defaults.
		if b.AnomalyRecordInterval != 1*time.Minute {
			t.Errorf("AnomalyRecordInterval should default when others set: got %v", b.AnomalyRecordInterval)
		}
	})
}

// TestResolveLogAnomalyEnabled guards the v0.8.227 COREMETRY_LOG_ANOMALY_ENABLED
// gate. Operator-reported: the worker-role log-pattern detector + Drain
// templater hammer ES with curated-pattern _msearch / significant_text /
// sample pulls; the operator wanted to switch that traffic off. The detector
// MUST default ON (current preserved) and flip OFF only on an explicit
// "false"/"0" — never silently disable on an unset or garbage value, which
// would drop log anomalies for installs that never set the var.
func TestResolveLogAnomalyEnabled(t *testing.T) {
	cases := []struct {
		env     string
		current bool
		want    bool
	}{
		{"", true, true},       // unset → keep default ON
		{"", false, false},     // unset → keep an already-off value
		{"false", true, false}, // explicit disable
		{"0", true, false},     // explicit disable (numeric)
		{"true", true, true},   // explicit enable
		{"1", false, true},     // explicit enable flips an off default back on
		{"yes", true, true},    // garbage → leave current untouched (don't disable)
		{"FALSE", true, true},  // case-sensitive: not a recognised disable token → keep ON
		{"off", false, false},  // garbage → leave current (off) untouched
	}
	for _, c := range cases {
		if got := resolveLogAnomalyEnabled(c.env, c.current); got != c.want {
			t.Errorf("resolveLogAnomalyEnabled(%q, %v) = %v, want %v", c.env, c.current, got, c.want)
		}
	}
}

// v0.10.2057 — Elastic ML job poller varsayılan AÇIK (operatör kararı);
// yaml `ml_enabled: false` ve COREMETRY_ES_ML_ENABLED=false kapatır.
func TestElasticMLPollerDefaultOn(t *testing.T) {
	if !defaults.Logs.Elasticsearch.MLEnabled {
		t.Fatal("varsayılan açık olmalı")
	}
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "c.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name, yaml, env string
		want            bool
	}{
		{"alan yok → açık", "logs:\n  backend: elasticsearch\n", "", true},
		{"yaml false → kapalı", "logs:\n  elasticsearch:\n    ml_enabled: false\n", "", false},
		{"env false → kapalı", "", "false", false},
		{"env 0 → kapalı", "", "0", false},
		{"yaml false + env true → açık", "logs:\n  elasticsearch:\n    ml_enabled: false\n", "true", true},
	}
	for _, c := range cases {
		t.Setenv("COREMETRY_ES_ML_ENABLED", c.env)
		cfg, err := Load(write(c.yaml))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if cfg.Logs.Elasticsearch.MLEnabled != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, cfg.Logs.Elasticsearch.MLEnabled, c.want)
		}
	}
}
