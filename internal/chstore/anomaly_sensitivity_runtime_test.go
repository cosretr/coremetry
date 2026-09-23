package chstore

import "testing"

// v0.10.890 — runtime alt bloğu: varsayılan off/auto (davranış değişmez),
// bilinmeyen kip/kaynak varsayılana iner, sıfır struct (eski blob) varsayılan,
// kelepçeler; NormalizeAnomalySensitivity alanı TAŞIR (PUT'ta düşmez).
func TestAnomalyRuntimeNormalize(t *testing.T) {
	d := DefaultAnomalyRuntime()
	if d.HeapMode != HeapModeOff || d.HeapSource != HeapSourceAuto || d.HeapMinMAD != 2 || d.HeapFloorPct != 0.10 || d.HeapMinAbsDelta != 5 || d.HeapSilentBuckets != 3 || d.HeapMaxPods != 5000 {
		t.Fatalf("varsayılan: %+v", d)
	}
	if got := NormalizeAnomalyRuntime(AnomalyRuntimeConfig{}); got != d {
		t.Fatalf("sıfır struct varsayılana inmeli: %+v", got)
	}
	got := NormalizeAnomalyRuntime(AnomalyRuntimeConfig{HeapMode: " SHADOW ", HeapSource: "garbage", HeapMinMAD: -1, HeapFloorPct: 0, HeapMinAbsDelta: 500, HeapSilentBuckets: 0, HeapMaxPods: 7})
	if got.HeapMode != HeapModeShadow || got.HeapSource != HeapSourceAuto || got.HeapMinMAD != 2 || got.HeapFloorPct != 0 || got.HeapMinAbsDelta != 5 || got.HeapSilentBuckets != 3 || got.HeapMaxPods != 5000 {
		t.Fatalf("kelepçe: %+v", got)
	}
	if NormalizeAnomalyRuntime(AnomalyRuntimeConfig{HeapMode: "on", HeapMinMAD: 3}).HeapMode != HeapModeOn {
		t.Fatal("on korunmalı")
	}
	s := DefaultAnomalySensitivity()
	if s.HeapMode() != HeapModeOff || s.HeapProblemsActive() {
		t.Fatal("blob varsayılanı off")
	}
	c := NormalizeAnomalySensitivity(AnomalySensitivityConfig{Runtime: AnomalyRuntimeConfig{HeapMode: "shadow", HeapSource: "ch", HeapMinMAD: 1.5, HeapFloorPct: 0.2, HeapMinAbsDelta: 4, HeapSilentBuckets: 2, HeapMaxPods: 200}})
	if c.Runtime.HeapMode != HeapModeShadow || c.Runtime.HeapSource != HeapSourceCH || c.Runtime.HeapMinMAD != 1.5 || !c.HeapProblemsActive() {
		t.Fatalf("Normalize alanı taşımalı: %+v", c.Runtime)
	}
	if NormalizeAnomalySensitivity(AnomalySensitivityConfig{}).Runtime != d {
		t.Fatal("boş blob runtime varsayılanına somutlaşmalı")
	}
}
