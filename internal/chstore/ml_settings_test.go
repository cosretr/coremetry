package chstore

import "testing"

// v0.10.1743 — log ML dilim 0: terfi varsayılan KAPALI; eşik + örnek kapısı;
// karne paydasına kararsız satır girmez (S4); karar anahtarı önekli.
func TestMLConfigDefaultsOff(t *testing.T) {
	c := NormalizeMLConfig(MLConfig{})
	if c.BucketSec != 300 || c.TickSec != 300 || c.TimeoutMs != 10000 || c.MaxServices != 500 {
		t.Fatalf("defaults: %+v", c)
	}
	if c.Enabled {
		t.Fatal("ana anahtar varsayılan kapalı")
	}
	for _, d := range MLDetectorNames {
		if c.Detectors[d] {
			t.Fatalf("%s: dedektör varsayılan kapalı", d)
		}
		p, ok := c.Promotion[d]
		if !ok || p.Enabled {
			t.Fatalf("%s: terfi varsayılan kapalı olmalı: %+v ok=%v", d, p, ok)
		}
	}
	c2 := NormalizeMLConfig(MLConfig{BucketSec: 1, TickSec: 99999, Promotion: map[string]MLPromotion{"bogus": {Enabled: true}}})
	if c2.BucketSec != 300 || c2.TickSec != 300 {
		t.Fatalf("sınır dışı değerler varsayılana dönmeli: %+v", c2)
	}
	if _, ok := c2.Promotion["bogus"]; ok {
		t.Fatalf("bilinmeyen dedektör anahtarı düşmeli")
	}
}

func TestMLPromotionAllowed(t *testing.T) {
	c := DefaultMLConfig()
	sc := MLDetectorScore{Detector: "log_volume", Samples: 50, Hits: 45, HitRate: 0.9}
	if c.PromotionAllowed("log_volume", sc) {
		t.Fatal("kapalı anahtar terfi etmemeli")
	}
	c.Promotion["log_volume"] = MLPromotion{Enabled: true, MinHitRate: 0.8, MinSamples: 20}
	if c.PromotionAllowed("log_volume", sc) {
		t.Fatal("ana anahtar/dedektör kapalıyken terfi olmamalı")
	}
	c.Enabled, c.Detectors["log_volume"] = true, true
	if !c.PromotionAllowed("log_volume", sc) {
		t.Fatal("eşik geçildi, terfi açık olmalı")
	}
	if c.PromotionAllowed("log_volume", MLDetectorScore{Samples: 5, Hits: 5, HitRate: 1}) {
		t.Fatal("asgari örnek altında terfi olmamalı")
	}
	if c.PromotionAllowed("log_volume", MLDetectorScore{Samples: 40, Hits: 20, HitRate: 0.5}) {
		t.Fatal("isabet eşiği altında terfi olmamalı")
	}
	if c.PromotionAllowed("unknown", sc) {
		t.Fatal("bilinmeyen dedektör terfi etmemeli")
	}
}

func TestMLScorecardRateAndIDs(t *testing.T) {
	if r := MLScorecardRate(3, 0); r != 0 {
		t.Fatalf("samples=0 → 0, got %v", r)
	}
	if r := MLScorecardRate(3, 4); r != 0.75 {
		t.Fatalf("3/4 = %v", r)
	}
	id := MLVerdictID("abc")
	if id != "ml:abc" || !IsMLVerdictID(id) || IsMLVerdictID("abc") {
		t.Fatalf("verdict id öneki: %q", id)
	}
	if MLEventID("log_volume", "orders-api", "error") == MLEventID("error_rate", "orders-api", "error") {
		t.Fatal("dedektör id'ye girmeli")
	}
	if MLDetectorSignal("metric_jvm") != MLSignalMetric || len(DefaultMLConfig().MetricFamilies) != 5 {
		t.Fatal("metrik aileleri")
	}
	if MLDetectorSignal("op_latency") != MLSignalTrace || MLDetectorSignal("log_volume") != MLSignalLog || MLDetectorSignal("x") != MLSignalLog {
		t.Fatal("dedektör → sinyal eşlemesi")
	}
}

func TestMLScorecardFromRows(t *testing.T) {
	ev := map[string]string{"a": "log_volume", "b": "log_volume", "c": "error_rate"}
	vd := map[string]string{"ml:a": "anomaly", "ml:b": "not_anomaly", "ml:zzz": "anomaly"}
	out := MLScorecardFromRows(ev, vd)
	if len(out) != 2 || out[0].Detector != "error_rate" || out[1].Detector != "log_volume" {
		t.Fatalf("sıra/dedektör: %+v", out)
	}
	if out[1].Events != 2 || out[1].Samples != 2 || out[1].Hits != 1 || out[1].HitRate != 0.5 {
		t.Fatalf("log_volume: %+v", out[1])
	}
	if out[0].Events != 1 || out[0].Samples != 0 || out[0].HitRate != 0 {
		t.Fatalf("error_rate kararsız: %+v", out[0])
	}
}
