package chstore

// anomaly_sensitivity_logtemplatenew_test.go — v0.10.1061: `logTemplateNew`
// anahtarı.
//
// Operatör onaylı (prod): "Bu log anomalileri de false pozitif geliyor."
// log_template_new dedektörü varsayılan KAPALI; *bool, nil = kapalı
// (opLatency v0.10.1056 birebir emsali). Eski blob KAPALI okunur, açıkça
// true tur atar, Normalize alanı düşürmez (PUT'ta kaybolsaydı her kayıt
// dedektörü sessizce kapatırdı). Komşu opLatency anahtarından bağımsızdır.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLogTemplateNewOn(t *testing.T) {
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
			if got := (AnomalySensitivityConfig{LogTemplateNew: tc.in}).LogTemplateNewOn(); got != tc.want {
				t.Fatalf("LogTemplateNewOn = %v, beklenen %v", got, tc.want)
			}
		})
	}
	if DefaultAnomalySensitivity().LogTemplateNewOn() {
		t.Fatal("varsayılan KAPALI olmalı (operatör kararı)")
	}
	var zero AnomalySensitivityConfig
	if zero.LogTemplateNewOn() {
		t.Fatal("sıfır değer KAPALI olmalı")
	}
	// Komşu anahtarlar birbirini sürüklemez.
	if (AnomalySensitivityConfig{OpLatency: &tr}).LogTemplateNewOn() {
		t.Fatal("opLatency=true logTemplateNew'i açtı")
	}
	// v0.10.1085 — opLatency'nin nil'i artık AÇIK; komşu sınaması açıkça
	// kapalı opLatency ile.
	if (AnomalySensitivityConfig{LogTemplateNew: &tr, OpLatency: &fa}).OpLatencyOn() {
		t.Fatal("logTemplateNew=true opLatency'yi açtı")
	}
	if !(AnomalySensitivityConfig{LogTemplateNew: &fa}).OpLatencyOn() {
		t.Fatal("logTemplateNew=false opLatency'yi kapattı")
	}
}

func TestLogTemplateNewNormalizeKeepsPointer(t *testing.T) {
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
			n := NormalizeAnomalySensitivity(AnomalySensitivityConfig{LogTemplateNew: tc.in})
			if n.LogTemplateNew == nil {
				t.Fatal("Normalize alanı düşürdü (nil) — PUT'ta kaybolur")
			}
			if *n.LogTemplateNew != tc.want || n.LogTemplateNewOn() != tc.want {
				t.Fatalf("Normalize = %v, beklenen %v", *n.LogTemplateNew, tc.want)
			}
		})
	}
}

func TestLogTemplateNewJSONRoundTrip(t *testing.T) {
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
		if err := json.Unmarshal([]byte(`{"dwellBuckets":3,"criticalZ":6,"serviceSilent":true,"opLatency":true}`), &old); err != nil {
			t.Fatal(err)
		}
		if old.LogTemplateNew != nil {
			t.Fatal("alan olmayan blob nil olmayan işaretçi üretti")
		}
		if NormalizeAnomalySensitivity(old).LogTemplateNewOn() {
			t.Fatal("eski blob açık okundu — varsayılan KAPALI olmalı")
		}
	})

	t.Run("açıkça true tur atar", func(t *testing.T) {
		tr := true
		got, raw := putGet(t, AnomalySensitivityConfig{LogTemplateNew: &tr})
		if !strings.Contains(raw, `"logTemplateNew":true`) {
			t.Fatalf("kaydedilen blob logTemplateNew:true taşımıyor: %s", raw)
		}
		if !got.LogTemplateNewOn() {
			t.Fatal("true kayıt-okuma turunda kayboldu")
		}
	})

	t.Run("kapalı blob dürüst yazılır", func(t *testing.T) {
		got, raw := putGet(t, AnomalySensitivityConfig{})
		if !strings.Contains(raw, `"logTemplateNew":false`) {
			t.Fatalf("kaydedilen blob kapalıyı açıkça yazmıyor: %s", raw)
		}
		if got.LogTemplateNewOn() {
			t.Fatal("kapalı kayıt açık okundu")
		}
	})
}
