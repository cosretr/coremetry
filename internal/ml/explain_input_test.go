package ml

// explain_input_test.go — v0.10.1843 (audit §7.3 dilim 2.3): kart
// adaptörleri. Gerçek biçimli olay satırı (trace_red + quality kanıtı) +
// olay anı kaydı (thresholds.band, seri, dwell) + yakın kayıtlar + yayınlar
// → kart. Eski kayıt (bant/seri yok) ve yalnız-kayıt (olay satırı TTL'le
// düşmüş) yolları da kart üretir — boş kart yok.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func fixtureEvent() chstore.MLEvent {
	return chstore.MLEvent{
		ID: "ep-7c1", Detector: "op_latency", Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow, Service: "orders-api",
		Code: "/orders/{id}", StartedAt: at(14, 30).UnixNano(), LastSeen: at(14, 45).UnixNano(), WindowSec: 300,
		Baseline: 0.04, Observed: 0.12, Score: 5.6, Direction: "up",
		Evidence: `{"source":"vm","route":"/orders/{id}","dayClass":"weekday","mad":0.004,"dwell":2,"bucketSec":300,"baselineDays":21,` +
			`"measure":"slow_ratio","slowSec":1,"affected":36,"p95Ms":2300,"episodes7d":1,"confirmScore":1,"confirmSignals":["log"],` +
			`"configHash":"9f2c41","observed":0.12,"baseline":0.04,"pod":"","code":"/orders/{id}","direction":"up"}`,
	}
}

func fixtureSnapshot() chstore.MLSnapshot {
	var band [][4]float64
	var pts []chstore.MLPoint
	for i := 0; i < 12; i++ {
		t := at(13, 50).Add(time.Duration(i) * 5 * time.Minute).Unix()
		band = append(band, [4]float64{float64(t), 0.03, 0.04, 0.05})
		v := 0.04
		if i >= 8 {
			v = 0.12
		}
		pts = append(pts, chstore.MLPoint{T: t, V: v})
	}
	thr, _ := json.Marshal(map[string]any{"criticalZ": 4, "effectiveZ": 4.5, "dwellBuckets": 2, "baselineDays": 21, "impactMinRel": 0.2, "band": band})
	ser, _ := json.Marshal(pts)
	return chstore.MLSnapshot{EventID: "ep-7c1", Detector: "op_latency", Signal: chstore.MLSignalTrace, Service: "orders-api",
		Part: "/orders/{id}|slow", DetectedAt: at(14, 30).UnixNano(), LastSeen: at(14, 35).UnixNano(), BucketSec: 300,
		Series: string(ser), BaselineMed: 0.04, BaselineMAD: 0.004, Thresholds: string(thr), ConfigHash: "9f2c41", DayClass: "weekday",
		Dwell: fmt.Sprintf("[%d,%d]", at(14, 30).Unix(), at(14, 35).Unix()), Evidence: fixtureEvent().Evidence, Samples: "[]", Deploys: "[]"}
}

func fixtureSources() CardSources {
	ev, snap := fixtureEvent(), fixtureSnapshot()
	tpl, _ := json.Marshal([]chstore.MLPoint{{T: at(14, 30).Unix(), V: 90}, {T: at(14, 35).Unix(), V: 70}})
	return CardSources{
		Event: &ev, Snapshot: &snap, Now: at(14, 45), Loc: trLoc, Profile: ProfileAlwaysOn, DeploysOK: true, DeployLookback: 24 * time.Hour,
		Deploys: []chstore.Deploy{{Service: "orders-api", Version: "v2.42.0", TimeUnixNs: at(14, 20).UnixNano()}, {Service: "orders-api", Version: "v2.41.3", TimeUnixNs: at(9, 0).UnixNano()}},
		Related: []chstore.MLSnapshot{
			{EventID: "ep-tpl", Detector: "rare_template", Signal: chstore.MLSignalLog, Service: "orders-api", DetectedAt: at(14, 31).UnixNano(),
				LastSeen: at(14, 40).UnixNano(), Series: string(tpl), Evidence: `{"phrase":"Connection pool exhausted","template":"Connection pool exhausted: active=50"}`},
			{EventID: "ep-err", Detector: "error_rate", Signal: chstore.MLSignalLog, Service: "orders-api", DetectedAt: at(14, 32).UnixNano(), Evidence: `{}`},
			{EventID: "ep-old", Detector: "log_volume", Signal: chstore.MLSignalLog, Service: "orders-api", DetectedAt: at(12, 0).UnixNano(), Evidence: `{}`},
		},
		HrefOf: func(id string) string { return "/anomalies/ml/" + id },
	}
}

// ExampleCardJSON — rapor için gerçek biçimli kart (go test -run TestAssembleCard -v).
func TestAssembleCardFromRealShapes(t *testing.T) {
	in, ok := AssembleCardInput(fixtureSources())
	if !ok {
		t.Fatal("girdi kurulmadı")
	}
	c := BuildExplainCard(in)
	eq(t, "summary", c.Summary, "orders-api: yavaş istek oranı normalin yaklaşık 3 katı, 15 dakikadır (14:30'dan beri).")
	eq(t, "howMuch", c.HowMuch, "İsteklerin %12'si 1 saniyeden uzun sürüyor; bu saatte normalde %3–5. Yaklaşık 3 kat.")
	eq(t, "impact", c.Impact, "Son 5 dakikada yaklaşık 36 istek yavaş yanıt aldı.")
	eq(t, "why", c.WhyItMatters, "Bu servis günün her saati kullanılıyor.")
	eqList(t, "whatChanged", texts(c.WhatChanged), []string{
		"14:20'de yeni sürüm yayına alındı (v2.41.3 → v2.42.0).",
		"14:31'de daha önce görülmemiş bir hata mesajı çıkmaya başladı: \"Connection pool exhausted\".",
		"14:32'de aynı serviste hata logları arttı.",
	})
	if l := c.WhatChanged[1].Link; l == nil || l.Href != "/anomalies/ml/ep-tpl" || l.Label != "Log anomalisi" {
		t.Errorf("template link %+v", l)
	}
	eq(t, "technical.score", c.Technical.Score, "5,6 (etkin eşik 4,5)")
	eq(t, "technical.thresholds", c.Technical.Thresholds, "Süreklilik 2 kova (10 dk) · etki ≥ %20 göreli")
	eq(t, "technical.expected", c.Technical.Expected, "Son 21 günün aynı saat dilimi (±1 sa) · takvim sınıfı: iş günü · aralık: aynı kova diliminin q25–q75'i")
	eq(t, "technical.revision", c.Technical.Revision, "ayar özeti 9f2c41 · öğrenilen düzeltme yok")
	if testing.Verbose() {
		b, _ := json.MarshalIndent(c, "", "  ")
		t.Log(string(b))
	}
}

// Eski kayıt: bant/seri/eşik yok — kart yine dolu, sayı uydurulmaz.
func TestAssembleCardOldSnapshotDegrades(t *testing.T) {
	src := fixtureSources()
	snap := fixtureSnapshot()
	snap.Series, snap.Thresholds, snap.Dwell = "[]", `{"criticalZ":4}`, "[]"
	src.Snapshot, src.Related, src.Deploys, src.DeploysOK, src.Profile = &snap, nil, nil, false, ""
	in, _ := AssembleCardInput(src)
	c := BuildExplainCard(in)
	eq(t, "howMuch", c.HowMuch, "İsteklerin %12'si 1 saniyeden uzun sürüyor; bu saatte normalde yaklaşık %4. Yaklaşık 3 kat.")
	if len(c.WhatChanged) != 0 {
		t.Errorf("yayın verisi yokken madde uyduruldu: %v", texts(c.WhatChanged))
	}
	if c.Summary == "" || c.What == "" || c.Since == "" || c.WhyItMatters == "" {
		t.Errorf("boş kart alanı: %+v", c)
	}
}

// Olay satırı TTL'le düştü: yalnız kayıttan (son tetik kovası = gözlenen).
func TestAssembleCardSnapshotOnly(t *testing.T) {
	src := fixtureSources()
	snap := fixtureSnapshot()
	snap.Complete = true
	src.Event = nil
	src.Snapshot = &snap
	src.Now = at(18, 0)
	in, ok := AssembleCardInput(src)
	if !ok {
		t.Fatal("kayıttan kurulmadı")
	}
	c := BuildExplainCard(in)
	eq(t, "summary", c.Summary, "orders-api: yavaş istek oranı normalin yaklaşık 3 katı; 14:30'da başladı, 14:35'te bitti (5 dakika sürdü).")
	if c.Header.Open {
		t.Error("tamam kayıt açık sayıldı")
	}
	if _, ok := AssembleCardInput(CardSources{}); ok {
		t.Error("kaynaksız girdi ok döndü")
	}
}

func TestMetricLabelFor(t *testing.T) {
	cfg := chstore.MLConfig{MetricFamilies: chstore.DefaultMLMetricFamilies()}
	l, u := MetricLabelFor(cfg, "metric_pool", "metric_pool|pool_wait")
	if l != "Bağlantı bekleme" || u != "s/s" {
		t.Errorf("%q %q", l, u)
	}
	if l, _ := MetricLabelFor(cfg, "metric_pool", "metric_pool|x"); l != "x" {
		t.Errorf("bilinmeyen id: %q", l)
	}
	ev := chstore.MLEvent{ID: "m", Detector: "metric_pool", Signal: chstore.MLSignalMetric, Service: "payments-mobile", Code: "metric_pool|pool_wait",
		StartedAt: at(14, 28).UnixNano(), LastSeen: at(14, 40).UnixNano(), WindowSec: 300, Observed: 0.9, Baseline: 0.3, Direction: "up"}
	got := ExplainSummary(SummaryInput(ev, cfg, at(14, 44), trLoc))
	if !strings.HasPrefix(got, "payments-mobile: Bağlantı bekleme 0,9 sn/sn seviyesine çıktı (5 dakikalık ortalama); normalde yaklaşık 0,3 sn/sn.") { // v0.10.2032 → 2042 ölçü adı
		t.Errorf("metrik özeti: %q", got)
	}
}
