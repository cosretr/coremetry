package chstore

import (
	"encoding/json"
	"strings"
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
		// v0.10.2059: Davis yetenekleri (mlDefaultOnDetectors) varsayılan açık.
		if c.Detectors[d] != MLDetectorDefaultOn(d) || DefaultMLConfig().Detectors[d] != MLDetectorDefaultOn(d) {
			t.Fatalf("%s: dedektör varsayılanı %v olmalı", d, MLDetectorDefaultOn(d))
		}
	}
	c2 := NormalizeMLConfig(MLConfig{BucketSec: 1, TickSec: 99999, Detectors: map[string]bool{"bogus": true}})
	if c2.BucketSec != 300 || c2.TickSec != 300 {
		t.Fatalf("sınır dışı değerler varsayılana dönmeli: %+v", c2)
	}
	if _, ok := c2.Detectors["bogus"]; ok {
		t.Fatalf("bilinmeyen dedektör anahtarı düşmeli")
	}
}

// v0.10.2067 — eski `promotion` alanı (ml/promote.go kopya yolu) kaldırıldı:
// kayıtlı blobda kalan alan hatasız okunur, yok sayılır; yeniden yazımda düşer.
func TestMLConfigTolerantOfRetiredPromotionField(t *testing.T) {
	raw := `{"enabled":true,"detectors":{"error_rate":true},"promotion":{"error_rate":{"enabled":true,"minHitRate":0.8,"minSamples":20}}}`
	var c MLConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("eski blob okunmalı: %v", err)
	}
	c = NormalizeMLConfig(c)
	if !c.Enabled || !c.Detectors["error_rate"] {
		t.Fatalf("diğer alanlar aynen: %+v", c)
	}
	out, _ := json.Marshal(c)
	if strings.Contains(string(out), `"promotion"`) {
		t.Fatalf("yeniden yazımda promotion alanı olmamalı: %s", out)
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

// Kalite çerçevesi (v0.10.1751): eski blob (quality alanı yok) varsayılanla
// açılır. (Terfi kapısı gerekçeleri v0.10.2067'de kopya yoluyla kalktı.)
func TestMLQualityDefaults(t *testing.T) {
	c := DefaultMLConfig()
	c.Enabled, c.Detectors["log_volume"] = true, true
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
// v0.10.2018: yeni dedektör (trace_error_count) Detectors/Promotion
// haritasına girdiği için varsayılan hash BEKLENEN şekilde değişti
// (66df3eb38772bb64 → 254767bc3f68c4a3); baseline anahtarının hash dışında
// kaldığı yukarıdaki d/on karşılaştırmasıyla ayrıca çivili.
// v0.10.2020: k8s cAdvisor ifadelerine container süzgeci → 60610b14fef39c95.
// v0.10.2059: op_latency varsayılan açık (Davis S1) → 7bbfaa479ac1811c.
// v0.10.2063: dependency_error_rate (Davis S2, varsayılan açık) → 9cc38f9f62ebea9e.
// v0.10.2064: metric_kafka varsayılan açık (Davis S3) → 338fe387817c0331.
// v0.10.2067: `promotion` alanı (ml/promote.go kopya yolu) blobdan kalktı →
// 3095f1a59e098e82 (tek seferlik; hash yalnız kanıt/backtest etiketi).
// v0.10.2069: new_template (eski log_template_new taşıması, varsayılan açık) →
// 226ec2c6bb225cdf.
// v0.10.2077: svc_error_rate (servis hata oranı, varsayılan açık) → ef4c8495b67d41e7.
const mlDefaultHash1813 = "ef4c8495b67d41e7"

// v0.10.2020 — k8s ailesi: cAdvisor ifadeleri pod toplam satırını
// (container="") ve duraklatma kabını (POD) süzer; KSM restart ifadesi
// değişmez; operatörün kaydettiği ifade korunur (yalnız varsayılan değişti).
func TestMLK8sCAdvisorFilter(t *testing.T) {
	by := map[string]string{}
	for _, e := range DefaultMLMetricFamilies()["metric_k8s"] {
		by[e.ID] = e.Expr
	}
	for _, id := range []string{"cpu", "memory"} {
		if !strings.Contains(by[id], `container!="",container!="POD"`) {
			t.Errorf("%s süzgeçsiz: %s", id, by[id])
		}
	}
	if by["restarts"] != "sum by ({svc}) (increase(kube_pod_container_status_restarts_total[{win}]))" {
		t.Errorf("KSM restart ifadesi değişmemeli: %s", by["restarts"])
	}
	saved := DefaultMLConfig()
	custom := "sum by ({svc}) (rate(container_cpu_usage_seconds_total[{win}]))"
	saved.MetricFamilies["metric_k8s"] = []MLMetricExpr{{ID: "cpu", Label: "CPU", Expr: custom, Direction: "up"}}
	raw, _ := json.Marshal(saved)
	var back MLConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if got := NormalizeMLConfig(back).MetricFamilies["metric_k8s"]; len(got) != 1 || got[0].Expr != custom {
		t.Fatalf("kayıtlı ifade korunmalı: %+v", got)
	}
}
