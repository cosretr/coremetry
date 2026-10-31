package chstore

import "testing"

// v0.10.1746 — native dedektör anahtarları: varsayılan HEPSİ kapalı;
// bilinmeyen ad düşer; eksik ad kapalı.
func TestNativeDetectorsDefaultsOff(t *testing.T) {
	d := DefaultNativeDetectors()
	for _, n := range NativeDetectorNames {
		if d.On(n) {
			t.Fatalf("%s varsayılan kapalı olmalı", n)
		}
	}
	c := NormalizeNativeDetectors(NativeDetectorsConfig{Detectors: map[string]bool{NativeTraceOp: true, "bogus": true}})
	if !c.On(NativeTraceOp) || c.On(NativeLogPattern) {
		t.Fatalf("normalize: %+v", c)
	}
	if _, ok := c.Detectors["bogus"]; ok {
		t.Fatal("bilinmeyen ad düşmeli")
	}
	if len(c.Detectors) != len(NativeDetectorNames) {
		t.Fatalf("tüm adlar dolu olmalı: %d", len(c.Detectors))
	}
}
