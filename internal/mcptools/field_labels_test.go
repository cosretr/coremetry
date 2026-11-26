package mcptools

import "testing"

// v0.10.2480 (G) — oran / kimlik alanlı çıktılar insan etiketi taşır (eklemeli).
func TestWithFieldLabels(t *testing.T) {
	out := withFieldLabels(map[string]any{"x": 1}, "peakRatio", "fingerprint", "bilinmeyen")
	l, ok := out["labels"].(map[string]string)
	if !ok || len(l) != 2 || l["peakRatio"] == "" || l["fingerprint"] == "" || out["x"] != 1 {
		t.Fatalf("%v", out)
	}
	if _, has := withFieldLabels(map[string]any{}, "bilinmeyen")["labels"]; has {
		t.Fatal("etiketsiz alan için labels yazılmamalı")
	}
}
