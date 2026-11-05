package chstore

import (
	"encoding/json"
	"testing"
)

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
	if c.PromotionAllowed("log_volume", sc, "Europe/Istanbul") {
		t.Fatal("kapalı anahtar terfi etmemeli")
	}
	c.Promotion["log_volume"] = MLPromotion{Enabled: true, MinHitRate: 0.8, MinSamples: 20}
	if c.PromotionAllowed("log_volume", sc, "Europe/Istanbul") {
		t.Fatal("ana anahtar/dedektör kapalıyken terfi olmamalı")
	}
	c.Enabled, c.Detectors["log_volume"] = true, true
	if !c.PromotionAllowed("log_volume", sc, "Europe/Istanbul") {
		t.Fatal("eşik geçildi, terfi açık olmalı")
	}
	if c.PromotionAllowed("log_volume", MLDetectorScore{Samples: 5, Hits: 5, HitRate: 1}, "Europe/Istanbul") {
		t.Fatal("asgari örnek altında terfi olmamalı")
	}
	if c.PromotionAllowed("log_volume", MLDetectorScore{Samples: 40, Hits: 20, HitRate: 0.5}, "Europe/Istanbul") {
		t.Fatal("isabet eşiği altında terfi olmamalı")
	}
	if c.PromotionAllowed("unknown", sc, "Europe/Istanbul") {
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

// Kalite çerçevesi (v0.10.1751): takvim dilimi olmadan terfi kapısı kapalı,
// her koşul ayrı gerekçe; eski blob (quality alanı yok) varsayılanla açılır.
func TestMLPromotionGateReasonsAndQualityDefaults(t *testing.T) {
	c := DefaultMLConfig()
	c.Enabled, c.Detectors["op_latency"] = true, true
	c.Promotion["op_latency"] = MLPromotion{Enabled: true, MinHitRate: 0.8, MinSamples: 20}
	sc := MLDetectorScore{Samples: 50, Hits: 45, HitRate: 0.9}
	if g := c.PromotionGate("op_latency", sc, ""); g.OK || len(g.Reasons) != 1 {
		t.Fatalf("takvimsiz kapı kapalı + tek gerekçe olmalı: %+v", g)
	}
	if g := DefaultMLConfig().PromotionGate("op_latency", MLDetectorScore{}, ""); len(g.Reasons) != 5 {
		t.Fatalf("varsayılan: 5 gerekçe (ana, dedektör, terfi, örnek, takvim), got %+v", g)
	}
	n := NormalizeMLConfig(MLConfig{})
	if n.Quality != DefaultMLQuality() {
		t.Fatalf("eksik quality = varsayılan, got %+v", n.Quality)
	}
	if NormalizeMLConfig(MLConfig{Quality: MLQuality{ImpactMinRel: 99, RegimeHours: 999}}).Quality.RegimeHours != 6 {
		t.Fatal("sınır dışı rejim saati varsayılana dönmeli")
	}
	if c.ConfigHash() == DefaultMLConfig().ConfigHash() {
		t.Fatal("farklı ayar farklı hash vermeli")
	}
}

// Ortak etiket seti: karar × olay penceresi; olayı düşmüş karar etiket değil,
// 'ml:' önekli olmayan karar yok sayılır.
func TestMLLabelsFromRows(t *testing.T) {
	ev := map[string]MLEvent{"a": {ID: "a", Detector: "log_volume", Signal: "log", Service: "pay", StartedAt: 100e9, LastSeen: 400e9}}
	got := MLLabelsFromRows(map[string]string{"ml:a": VerdictAnomaly, "ml:gone": VerdictNotAnomaly, "x": VerdictAnomaly}, ev)
	if len(got) != 1 || !got[0].Positive || got[0].From != 100 || got[0].To != 400 || got[0].Service != "pay" {
		t.Fatalf("got %+v", got)
	}
	if !MLExcluded([][2]int64{{100, 400}}, 300) || MLExcluded([][2]int64{{100, 400}}, 401) {
		t.Fatal("MLExcluded pencere sınırı")
	}
}

// Karne sinyal başına (v0.10.1751): dedektör satırları log/trace/metric toplamına.
func TestMLSignalScores(t *testing.T) {
	rows := MLScorecardFromRows(
		map[string]string{"a": "log_volume", "b": "error_rate", "c": "op_latency", "d": "metric_jvm"},
		map[string]string{"ml:a": VerdictAnomaly, "ml:b": VerdictNotAnomaly, "ml:c": VerdictAnomaly})
	got := MLSignalScores(rows)
	if len(got) != 3 || got[0].Signal != "log" || got[0].Events != 2 || got[0].Samples != 2 || got[0].HitRate != 0.5 ||
		got[1].Signal != "trace" || got[1].HitRate != 1 || got[2].Signal != "metric" || got[2].Samples != 0 {
		t.Fatalf("got %+v", got)
	}
}

// v0.10.1811 — metrik serisi Load bayrağı normalize + JSON turunda kaybolmaz
// (batch kuralı; düşerse operatörün işaretlediği yük serisi sessizce kalkar).
func TestMLMetricExprLoadRoundTrip(t *testing.T) {
	c := NormalizeMLConfig(MLConfig{MetricFamilies: map[string][]MLMetricExpr{
		"metric_kafka": {{ID: "msg_rate", Expr: "sum by ({svc}) (rate(x[{win}]))", Direction: "up", Load: true}},
	}})
	raw, _ := json.Marshal(c)
	var back MLConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	back = NormalizeMLConfig(back)
	if l := back.MetricFamilies["metric_kafka"]; len(l) != 1 || !l[0].Load {
		t.Fatalf("Load düştü: %+v", l)
	}
}

// v0.10.1814 — `ml.baseline.enabled` varsayılan kapalı, ana anahtara bağlı;
// ConfigHash'e girmez (olay kanıtındaki hash 1813 ile bayt bayt aynı).
func TestMLBaselineSettingOffAndOutOfHash(t *testing.T) {
	d := DefaultMLConfig()
	if d.BaselineOn() {
		t.Fatal("baseline varsayılan kapalı olmalı")
	}
	on := d
	on.Baseline = &MLBaselineSettings{Enabled: true}
	if on.BaselineOn() {
		t.Fatal("ana anahtar kapalıyken baseline koşmamalı")
	}
	on.Enabled = true
	if !on.BaselineOn() {
		t.Fatal("iki anahtar açıkken koşmalı")
	}
	d.Enabled = true
	if d.ConfigHash() != on.ConfigHash() {
		t.Fatal("baseline anahtarı ConfigHash'i değiştirmemeli")
	}
	if got := DefaultMLConfig().ConfigHash(); got != mlDefaultHash1813 {
		t.Fatalf("varsayılan ConfigHash 1813'ten farklı: %s", got)
	}
	raw, _ := json.Marshal(NormalizeMLConfig(on))
	var back MLConfig
	if err := json.Unmarshal(raw, &back); err != nil || !NormalizeMLConfig(back).BaselineOn() {
		t.Fatalf("baseline anahtarı gidiş-dönüşte kayboldu: %s", raw)
	}
}

// mlDefaultHash1813 — v0.10.1813 HEAD'de DefaultMLConfig().ConfigHash()
// (ölçüldü): 1814 baseline anahtarı varsayılan hash'i değiştirmemeli.
const mlDefaultHash1813 = "66df3eb38772bb64"
