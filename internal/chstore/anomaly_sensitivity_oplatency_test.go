package chstore

// anomaly_sensitivity_oplatency_test.go — v0.10.1056: `opLatency` anahtarı.
//
// Operatör (prod): "Trace op latency false pozitif geliyor, gerek yok
// gelmelerine bence." trace_op_latency dedektörü varsayılan KAPALI; *bool,
// nil = kapalı (ServiceSilent v0.10.543, self_health.diskEta v0.10.1031
// emsali). Eski blob KAPALI okunur, açıkça true tur atar, Normalize alanı
// düşürmez (PUT'ta kaybolsaydı her kayıt dedektörü sessizce kapatırdı).

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpLatencyOn(t *testing.T) {
	tr, fa := true, false
	for _, tc := range []struct {
		name string
		in   *bool
		want bool
	}{
		{"nil (alan yok) → kapalı", nil, false},
		{"açıkça false → kapalı", &fa, false},
		{"açıkça true → açık", &tr, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (AnomalySensitivityConfig{OpLatency: tc.in}).OpLatencyOn(); got != tc.want {
				t.Fatalf("OpLatencyOn = %v, beklenen %v", got, tc.want)
			}
		})
	}
	if DefaultAnomalySensitivity().OpLatencyOn() {
		t.Fatal("varsayılan KAPALI olmalı (operatör kararı)")
	}
	var zero AnomalySensitivityConfig
	if zero.OpLatencyOn() {
		t.Fatal("sıfır değer KAPALI olmalı")
	}
}

func TestOpLatencyNormalizeKeepsPointer(t *testing.T) {
	tr, fa := true, false
	for _, tc := range []struct {
		name string
		in   *bool
		want bool
	}{
		{"nil → somut false", nil, false},
		{"false → false", &fa, false},
		{"true → true", &tr, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := NormalizeAnomalySensitivity(AnomalySensitivityConfig{OpLatency: tc.in})
			if n.OpLatency == nil {
				t.Fatal("Normalize alanı düşürdü (nil) — PUT'ta kaybolur")
			}
			if *n.OpLatency != tc.want || n.OpLatencyOn() != tc.want {
				t.Fatalf("Normalize = %v, beklenen %v", *n.OpLatency, tc.want)
			}
		})
	}
}

func TestOpLatencyJSONRoundTrip(t *testing.T) {
	// PUT = Normalize + Marshal (SaveAnomalySensitivity); GET = Unmarshal +
	// Normalize (ReadAnomalySensitivity).
	putGet := func(t *testing.T, in AnomalySensitivityConfig) (AnomalySensitivityConfig, string) {
		t.Helper()
		raw, err := json.Marshal(NormalizeAnomalySensitivity(in))
		if err != nil {
			t.Fatal(err)
		}
		var back AnomalySensitivityConfig
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		return NormalizeAnomalySensitivity(back), string(raw)
	}

	t.Run("eski blob (alan yok) → KAPALI", func(t *testing.T) {
		var old AnomalySensitivityConfig
		if err := json.Unmarshal([]byte(`{"dwellBuckets":3,"criticalZ":6,"serviceSilent":true}`), &old); err != nil {
			t.Fatal(err)
		}
		if old.OpLatency != nil {
			t.Fatal("alan olmayan blob nil olmayan işaretçi üretti")
		}
		if NormalizeAnomalySensitivity(old).OpLatencyOn() {
			t.Fatal("eski blob açık okundu — varsayılan KAPALI olmalı")
		}
	})

	t.Run("açıkça true tur atar", func(t *testing.T) {
		tr := true
		got, raw := putGet(t, AnomalySensitivityConfig{OpLatency: &tr})
		if !strings.Contains(raw, `"opLatency":true`) {
			t.Fatalf("kaydedilen blob opLatency:true taşımıyor: %s", raw)
		}
		if !got.OpLatencyOn() {
			t.Fatal("true kayıt-okuma turunda kayboldu")
		}
	})

	t.Run("kapalı blob dürüst yazılır", func(t *testing.T) {
		got, raw := putGet(t, AnomalySensitivityConfig{})
		if !strings.Contains(raw, `"opLatency":false`) {
			t.Fatalf("kaydedilen blob kapalıyı açıkça yazmıyor: %s", raw)
		}
		if got.OpLatencyOn() {
			t.Fatal("kapalı kayıt açık okundu")
		}
	})
}
