package chstore

// v0.10.1766 (A3) — kendi anahtarlı eski dedektörler yerleşik ekranda; "hepsini
// kapat" yerleşik + op_latency'yi kapatır, blobun kalanı kalır (v0.10.2085: log_template_new silindi).

import "testing"

func TestAllNativeOffAndOwnSwitches(t *testing.T) {
	sens := DefaultAnomalySensitivity()
	tr := true
	sens.OpLatency = &tr
	nat, out := AllNativeOff(sens)
	for _, n := range NativeDetectorNames {
		if nat.On(n) {
			t.Fatalf("%s açık kaldı", n)
		}
	}
	if out.OpLatencyOn() {
		t.Fatal("kendi anahtarlar kapanmalı")
	}
	if out.DwellBuckets != sens.DwellBuckets {
		t.Fatal("blobun kalanı korunmalı")
	}
	own := OwnSwitchesFrom(ApplyOwnSwitches(out, NativeOwnSwitches{OpLatency: true}))
	if !own.OpLatency {
		t.Fatalf("own=%+v", own)
	}
}
