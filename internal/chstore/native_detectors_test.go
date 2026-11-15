package chstore

import (
	"encoding/json"
	"strings"
	"testing"
)

// v0.10.1746 — native dedektör anahtarları: varsayılan HEPSİ kapalı;
// bilinmeyen ad düşer; eksik ad kapalı.
func TestNativeDetectorsDefaultsOff(t *testing.T) {
	d := DefaultNativeDetectors()
	for _, n := range NativeDetectorNames {
		if d.On(n) {
			t.Fatalf("%s varsayılan kapalı olmalı", n)
		}
	}
	c := NormalizeNativeDetectors(NativeDetectorsConfig{Detectors: map[string]bool{NativeDBHealth: true, "bogus": true}})
	if !c.On(NativeDBHealth) || c.On(NativeMetricAnomaly) {
		t.Fatalf("normalize: %+v", c)
	}
	if _, ok := c.Detectors["bogus"]; ok {
		t.Fatal("bilinmeyen ad düşmeli")
	}
	if len(c.Detectors) != len(NativeDetectorNames) {
		t.Fatalf("tüm adlar dolu olmalı: %d", len(c.Detectors))
	}
}

// v0.10.2270 — silinen eski dedektörlerin (log_pattern, trace_op, behavior)
// anahtarlarını taşıyan kayıtlı blob temiz okunur: adlar düşer, kalan
// anahtarlar korunur, yeniden yazımda silinen adlar yoktur.
func TestNativeDetectorsDropsDeletedKeys(t *testing.T) {
	var in NativeDetectorsConfig
	raw := `{"detectors":{"log_pattern":true,"trace_op":true,"behavior":true,"metric_anomaly":true}}`
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("eski blob okunmalı: %v", err)
	}
	c := NormalizeNativeDetectors(in)
	if !c.On(NativeMetricAnomaly) {
		t.Fatal("kalan anahtar korunmalı")
	}
	for _, gone := range []string{"log_pattern", "trace_op", "behavior"} {
		if _, ok := c.Detectors[gone]; ok || c.On(gone) {
			t.Fatalf("%s düşmeli: %+v", gone, c.Detectors)
		}
	}
	out, _ := json.Marshal(c)
	if strings.Contains(string(out), "log_pattern") || strings.Contains(string(out), "trace_op") || strings.Contains(string(out), `"behavior"`) {
		t.Fatalf("yeniden yazımda silinen adlar: %s", out)
	}
}
