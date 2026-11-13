package chstore

// v0.10.1766 (A3) — "hepsini kapat" yerleşik dedektörlerin hepsini kapatır.
// v0.10.2087: kendi-anahtar görünümü (op_latency, log_template_new) dedektörlerle
// birlikte silindi; anomaly_sensitivity'ye dokunulmaz.

import "testing"

func TestAllNativeOff(t *testing.T) {
	nat := AllNativeOff()
	for _, n := range NativeDetectorNames {
		if nat.On(n) {
			t.Fatalf("%s açık kaldı", n)
		}
	}
}
