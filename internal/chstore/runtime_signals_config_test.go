package chstore

import "testing"

func TestNormalizeRuntimeSignals(t *testing.T) {
	d := DefaultRuntimeSignals()
	if got := NormalizeRuntimeSignals(RuntimeSignalConfig{}); got != d {
		t.Fatalf("zero → %+v, want defaults %+v", got, d)
	}
	if d.Mode != RuntimeSignalsShadow {
		t.Fatalf("default mode %q, want shadow", d.Mode)
	}
	got := NormalizeRuntimeSignals(RuntimeSignalConfig{Mode: " ON ", PoolQueueWarn: 50, PoolQueueCrit: 20, LeakMinR2: 5})
	if got.Mode != RuntimeSignalsOn || got.PoolQueueWarn != 50 || got.PoolQueueCrit != 500 || got.LeakMinR2 != d.LeakMinR2 {
		t.Fatalf("got %+v", got)
	}
	if NormalizeRuntimeSignals(RuntimeSignalConfig{Mode: "bogus"}).Mode != RuntimeSignalsShadow {
		t.Fatal("unknown mode must fall back to shadow")
	}
	// Eski blob (heap alanları dolu, signals yok) → sinyaller varsayılan.
	r := NormalizeAnomalyRuntime(AnomalyRuntimeConfig{HeapMode: HeapModeShadow, HeapMinMAD: 2})
	if r.Signals != d {
		t.Fatalf("legacy blob signals = %+v", r.Signals)
	}
	if DefaultAnomalySensitivity().RuntimeSignals() != d {
		t.Fatal("DefaultAnomalySensitivity must carry runtime signal defaults")
	}
}
