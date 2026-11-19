package ml

import (
	"math"
	"testing"
)

// v0.10.2372 — etki kuralı çekirdeği (I1). Saat / dilim bağımsız: yalnız dizi.

func mins(load float64, vals ...float64) []impactSample {
	out := make([]impactSample, len(vals))
	for i, x := range vals {
		out[i] = impactSample{Value: x, Load: load, OK: !math.IsNaN(x)}
	}
	return out
}

func TestImpactThreshold(t *testing.T) {
	fr := impactRules[impactFailureRate]
	for _, c := range []struct{ base, want float64 }{
		{0, 0.01},      // normalde hiç yok → %1
		{0.004, 0.014}, // +1 puan baskın
		{0.05, 0.075},  // +%50 baskın
		{-1, 0.01},     // negatif taban 0 sayılır
		{math.NaN(), 0.01},
	} {
		if got := fr.threshold(c.base); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("hata oranı eşiği taban=%v: %v, beklenen %v", c.base, got, c.want)
		}
	}
	if got := impactRules[impactResponseMedian].threshold(120); got != 220 {
		t.Errorf("medyan yanıt 120 ms: %v, beklenen 220 (+100 ms baskın)", got)
	}
	if got := impactRules[impactResponseMedian].threshold(400); got != 600 {
		t.Errorf("medyan yanıt 400 ms: %v, beklenen 600 (+%%50 baskın)", got)
	}
	if got := impactRules[impactConsumerLag].threshold(0); got != 1000 {
		t.Errorf("lag taban 0: %v, beklenen 1000 (etki tabanı)", got)
	}
	if got := impactRules[impactConsumerLag].threshold(4000); got != 8000 {
		t.Errorf("lag taban 4000: %v, beklenen 8000", got)
	}
}

func TestImpactDecide(t *testing.T) {
	nan := math.NaN()
	for _, c := range []struct {
		name    string
		kind    impactKind
		samples []impactSample
		base    float64
		fire    bool
		clear   bool
		viol    int
		onset   int
	}{
		{"5'te 3 ihlal, son örnek ihlal → olay", impactFailureRate, mins(100, 0.002, 0.03, 0.001, 0.04, 0.05), 0.002, true, false, 3, 1},
		{"5'te 2 ihlal → olay değil", impactFailureRate, mins(100, 0.002, 0.002, 0.001, 0.04, 0.05), 0.002, false, false, 2, 3},
		{"3 ihlal ama son örnek düzelmiş → olay değil", impactFailureRate, mins(100, 0.03, 0.04, 0.05, 0.002, 0.001), 0.002, false, false, 3, 0},
		{"yük kapısı: dakikada 10 istek altı ihlal sayılmaz", impactFailureRate, mins(4, 0.5, 0.5, 0.5, 0.5, 0.5), 0, false, false, 0, -1},
		{"yüksek taban: %5 → %7 yetmez (+%50 şartı)", impactFailureRate, mins(100, 0.07, 0.07, 0.07, 0.07, 0.07), 0.05, false, true, 0, -1},
		{"yüksek taban: %5 → %8 olay", impactFailureRate, mins(100, 0.08, 0.08, 0.08, 0.08, 0.08), 0.05, true, false, 5, 0},
		{"pencere temiz → kapanabilir", impactFailureRate, mins(100, 0.001, 0.002, 0.001, 0, 0.001), 0.002, false, true, 0, -1},
		{"eksik dakika temiz saymaz", impactFailureRate, mins(100, 0.001, nan, 0.001, 0, 0.001), 0.002, false, false, 0, -1},
		{"eksik dakika ihlal de saymaz", impactFailureRate, mins(100, nan, nan, 0.05, 0.05, nan), 0, false, false, 2, 2},
		{"yalnız son 5 örnek okunur", impactFailureRate, mins(100, 0.9, 0.9, 0.9, 0.001, 0.001, 0.001, 0.001, 0.001), 0, false, true, 0, -1},
		{"lag: 2 kat ve taban üstü", impactConsumerLag, mins(0, 500, 9000, 9500, 12000, 15000), 4000, true, false, 4, 1},
		{"lag: etki tabanının altı olay değil", impactConsumerLag, mins(0, 900, 900, 900, 900, 900), 0, false, true, 0, -1},
		{"en yavaş %10: 2 kat ve +1 sn", impactResponseSlow, mins(50, 800, 2600, 2700, 3000, 2900), 1200, true, false, 4, 1},
		{"ölçüm yok", impactFailureRate, mins(100, nan, nan), 0, false, false, 0, -1},
		{"bilinmeyen ölçü ateşlemez", impactKind("x"), mins(100, 1, 1, 1, 1, 1), 0, false, false, 0, -1},
	} {
		v := impactDecide(c.kind, c.samples, c.base)
		if v.Fire != c.fire || v.Clear != c.clear || v.Violations != c.viol || v.Onset != c.onset {
			t.Errorf("%s: fire=%v clear=%v ihlal=%d onset=%d (%s); beklenen fire=%v clear=%v ihlal=%d onset=%d",
				c.name, v.Fire, v.Clear, v.Violations, v.Onset, v.Reason, c.fire, c.clear, c.viol, c.onset)
		}
		if v.Fire && v.Reason != "" {
			t.Errorf("%s: olayda neden boş olmalı, %q", c.name, v.Reason)
		}
		if !v.Fire && v.Reason == "" {
			t.Errorf("%s: olay yokken neden yazılmalı", c.name)
		}
	}
}

func TestImpactDecidePeakAndObserved(t *testing.T) {
	v := impactDecide(impactFailureRate, mins(100, 0.02, 0.09, 0.04, 0.03, 0.05), 0)
	if !v.Fire || v.Peak != 0.09 || v.Observed != 0.05 || v.Threshold != 0.01 || v.Measured != 5 {
		t.Fatalf("tepe / son değer / eşik: %+v", v)
	}
}
