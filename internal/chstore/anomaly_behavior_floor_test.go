package chstore

// anomaly_behavior_floor_test.go — v0.10.1070 (operatör onaylı, prod: "Bu da
// mesela false pozitif"): davranış motorunun üç yeni vidası — MinP99Ms,
// MinErrorRatePct, SpikyBandFactor. Kelepçe duruşu bölümün geri kalanıyla
// aynı: 0 meşru DEĞİL (eski blob alanı taşımaz ve 0 okunur; 0'ı "kapalı"
// saymak tabanı sessizce kaldırırdı), aralık dışı / NaN → varsayılan.

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

// TestBehaviorFloorDefaults — operatör onaylı varsayılanlar PİNLİ. 200 ms
// op-latency'nin opLatencyMinP99Ms'iyle aynı sayı; %1 ani-sapma error_rate
// AbsFloor'uyla aynı.
func TestBehaviorFloorDefaults(t *testing.T) {
	d := DefaultAnomalyBehavior()
	if d.MinP99Ms != 200 {
		t.Errorf("MinP99Ms = %v, want 200", d.MinP99Ms)
	}
	if d.MinErrorRatePct != 1.0 {
		t.Errorf("MinErrorRatePct = %v, want 1.0", d.MinErrorRatePct)
	}
	if d.SpikyBandFactor != 1.5 {
		t.Errorf("SpikyBandFactor = %v, want 1.5", d.SpikyBandFactor)
	}
	if got := DefaultAnomalySensitivity().Behavior; got.MinP99Ms != 200 || got.MinErrorRatePct != 1.0 || got.SpikyBandFactor != 1.5 {
		t.Errorf("üst varsayılan blob yeni vidaları taşımıyor: %+v", got)
	}
}

func TestNormalizeBehaviorFloorKnobs(t *testing.T) {
	d := DefaultAnomalyBehavior()
	cases := []struct {
		name                string
		p99, errPct, band   float64
		wantP99, wantErr, w float64
	}{
		{"boş (eski blob) → varsayılan", 0, 0, 0, d.MinP99Ms, d.MinErrorRatePct, d.SpikyBandFactor},
		{"geçerli korunur", 500, 2.5, 2, 500, 2.5, 2},
		{"alt sınırlar meşru", 1, 0.01, 1, 1, 0.01, 1},
		{"üst sınırlar meşru", 60000, 100, 10, 60000, 100, 10},
		{"negatif → varsayılan", -1, -0.5, -2, d.MinP99Ms, d.MinErrorRatePct, d.SpikyBandFactor},
		{"üst sınır aşımı → varsayılan", 60001, 101, 10.5, d.MinP99Ms, d.MinErrorRatePct, d.SpikyBandFactor},
		{"bant 1'in altı (p90'ın ALTINDA ateşlemek) → varsayılan", 200, 1, 0.9, 200, 1, d.SpikyBandFactor},
		{"NaN / Inf → varsayılan", math.NaN(), math.Inf(1), math.NaN(), d.MinP99Ms, d.MinErrorRatePct, d.SpikyBandFactor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeAnomalyBehavior(AnomalyBehaviorConfig{MinP99Ms: c.p99, MinErrorRatePct: c.errPct, SpikyBandFactor: c.band})
			if got.MinP99Ms != c.wantP99 || got.MinErrorRatePct != c.wantErr || got.SpikyBandFactor != c.w {
				t.Errorf("got %v/%v/%v, want %v/%v/%v", got.MinP99Ms, got.MinErrorRatePct, got.SpikyBandFactor, c.wantP99, c.wantErr, c.w)
			}
		})
	}
}

// TestBehaviorFloorLegacyBlobAndRoundTrip — bu sürümden ESKİ blob (alanlar
// yok) varsayılanları devralır; kaydet → oku aynı değeri verir; JSON
// etiketleri frontend sözleşmesi.
func TestBehaviorFloorLegacyBlobAndRoundTrip(t *testing.T) {
	var legacy AnomalySensitivityConfig
	if err := json.Unmarshal([]byte(`{"dwellBuckets":3,"criticalZ":6,"behavior":{"seasonalZ":4,"regimeRatio":1.5,"dwellSeasonal":3,"dwellRegime":6,"maxCandidatesPerTick":50}}`), &legacy); err != nil {
		t.Fatal(err)
	}
	n := NormalizeAnomalySensitivity(legacy).Behavior
	if n.MinP99Ms != 200 || n.MinErrorRatePct != 1.0 || n.SpikyBandFactor != 1.5 {
		t.Fatalf("eski blob varsayılanları devralmadı: %+v", n)
	}

	in := NormalizeAnomalySensitivity(AnomalySensitivityConfig{
		Behavior: AnomalyBehaviorConfig{MinP99Ms: 350, MinErrorRatePct: 2, SpikyBandFactor: 2.5},
	})
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{`"minP99Ms":350`, `"minErrorRatePct":2`, `"spikyBandFactor":2.5`} {
		if !strings.Contains(string(raw), f) {
			t.Errorf("JSON'da %s yok — frontend sözleşmesi kırık: %s", f, raw)
		}
	}
	var back AnomalySensitivityConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	got := NormalizeAnomalySensitivity(back).Behavior
	if got.MinP99Ms != 350 || got.MinErrorRatePct != 2 || got.SpikyBandFactor != 2.5 {
		t.Errorf("round-trip değiştirdi: %+v", got)
	}
}

// TestBehaviorFloorKeepsLastGood — okuma hatası operatörün kaydettiği tabanı
// varsayılanla EZMEZ (v0.10.1039 son-iyi-değer kablosu yeni alanları da taşır).
func TestBehaviorFloorKeepsLastGood(t *testing.T) {
	s := &Store{}
	good := func() (AnomalySensitivityConfig, error) {
		c := DefaultAnomalySensitivity()
		c.Behavior.MinP99Ms, c.Behavior.MinErrorRatePct, c.Behavior.SpikyBandFactor = 500, 3, 2
		return NormalizeAnomalySensitivity(c), nil
	}
	fail := func() (AnomalySensitivityConfig, error) { return AnomalySensitivityConfig{}, errors.New("ch: timeout") }
	s.loadAnomalySensitivityWith(good)
	for i := 0; i < 3; i++ {
		b := s.loadAnomalySensitivityWith(fail).Behavior
		if b.MinP99Ms != 500 || b.MinErrorRatePct != 3 || b.SpikyBandFactor != 2 {
			t.Fatalf("okuma hatası son iyi değeri ezdi: %+v", b)
		}
	}
	if b := s.AnomalySensitivityForDetectors().Behavior; b.MinP99Ms != 500 {
		t.Fatalf("dedektör okuması son iyi değeri görmüyor: %+v", b)
	}
	// Hiç yüklenmemiş + hata → varsayılanlar (nil → defaults).
	s2 := &Store{}
	if b := s2.loadAnomalySensitivityWith(fail).Behavior; b.MinP99Ms != 200 || b.SpikyBandFactor != 1.5 {
		t.Fatalf("ilk yüklemede hata varsayılanı yayınlamadı: %+v", b)
	}
}
