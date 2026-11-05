package ml

// v0.10.1811 — ML hattı batch kuralı (audit §13 D8). Önceki semptom:
// internal/ml batch kalıbını hiç uygulamıyordu; `-batch` servisinde gece
// işinin başlaması log_volume / throughput_drop adayı üretiyordu.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func sensWith(patterns ...string) chstore.AnomalySensitivityConfig {
	p := append([]string{}, patterns...)
	return chstore.AnomalySensitivityConfig{BatchServicePatterns: &p}
}

func TestBatchKeep(t *testing.T) {
	def := chstore.AnomalySensitivityConfig{} // nil liste = varsayılan ["-batch"]
	loadParts := map[string]bool{"metric_kafka|msg_rate": true}
	share := func(cur, base float64) string {
		return mergeEvidence(`{"dayClass":"normal"}`, errShareEvidence(cur*100, 100, base*100, 100))
	}
	cases := []struct {
		name string
		sens chstore.AnomalySensitivityConfig
		ev   chstore.MLEvent
		keep bool
	}{
		{"batch + log hacmi ↑ → kesilir", def, chstore.MLEvent{Detector: detLogVolume, Signal: chstore.MLSignalLog, Service: "orders-batch", Direction: "up"}, false},
		{"batch + log hacmi ↓ → kesilir (iki yön, klasik request_rate)", def, chstore.MLEvent{Detector: detLogVolume, Signal: chstore.MLSignalLog, Service: "orders-batch", Direction: "down"}, false},
		{"batch + servis sustu → kesilir", def, chstore.MLEvent{Detector: detServiceSilent, Signal: chstore.MLSignalLog, Service: "orders-batch"}, false},
		{"batch + trafik düşüşü → kesilir", def, chstore.MLEvent{Detector: detThroughputDrop, Signal: chstore.MLSignalTrace, Service: "orders-batch"}, false},
		{"batch + log kırılma _total → kesilir", def, chstore.MLEvent{Detector: detChangePoint, Signal: chstore.MLSignalLog, Service: "orders-batch"}, false},
		{"batch + Load metrik → kesilir", def, chstore.MLEvent{Detector: "metric_kafka", Signal: chstore.MLSignalMetric, Service: "orders-batch", Code: "metric_kafka|msg_rate"}, false},
		{"batch + yük-dışı metrik → kalır", def, chstore.MLEvent{Detector: "metric_k8s", Signal: chstore.MLSignalMetric, Service: "orders-batch", Code: "metric_k8s|restarts"}, true},
		{"batch + endpoint hata oranı → kalır", def, chstore.MLEvent{Detector: detOpErrorRate, Signal: chstore.MLSignalTrace, Service: "orders-batch"}, true},
		{"batch + endpoint gecikmesi → kalır", def, chstore.MLEvent{Detector: detOpLatency, Signal: chstore.MLSignalTrace, Service: "orders-batch"}, true},
		{"batch + aykırı pod → kalır", def, chstore.MLEvent{Detector: detPodOutlier, Signal: chstore.MLSignalLog, Service: "orders-batch"}, true},
		{"batch + hata sayısı, pay 4× → kalır", def, chstore.MLEvent{Detector: detErrorRate, Signal: chstore.MLSignalLog, Service: "orders-batch", Evidence: share(0.04, 0.01)}, true},
		{"batch + hata sayısı, pay sabit (yükle orantılı) → kesilir", def, chstore.MLEvent{Detector: detErrorRate, Signal: chstore.MLSignalLog, Service: "orders-batch", Evidence: share(0.01, 0.01)}, false},
		{"batch + hata sayısı, pay 2× → kesilir", def, chstore.MLEvent{Detector: detErrorRate, Signal: chstore.MLSignalLog, Service: "orders-batch", Evidence: share(0.02, 0.01)}, false},
		{"batch + hata sayısı, pay kanıtı yok → kalır", def, chstore.MLEvent{Detector: detErrorRate, Signal: chstore.MLSignalLog, Service: "orders-batch", Evidence: `{"dayClass":"normal"}`}, true},
		{"batch + hata sayısı, taban payı 0 → kalır", def, chstore.MLEvent{Detector: detErrorRate, Signal: chstore.MLSignalLog, Service: "orders-batch", Evidence: share(0.01, 0)}, true},
		{"batch + log kırılma error, pay kanıtı yok → kalır", def, chstore.MLEvent{Detector: detChangePoint, Signal: chstore.MLSignalLog, Service: "orders-batch", Level: "error"}, true},
		{"batch değil + hacim ↑ → kalır", def, chstore.MLEvent{Detector: detLogVolume, Signal: chstore.MLSignalLog, Service: "orders-api", Direction: "up"}, true},
		{"batch değil + hata sayısı pay sabit → kalır", def, chstore.MLEvent{Detector: detErrorRate, Signal: chstore.MLSignalLog, Service: "orders-api", Evidence: share(0.01, 0.01)}, true},
		{"ayar listesi: -nightly kalıbı → kesilir", sensWith("-nightly"), chstore.MLEvent{Detector: detLogVolume, Signal: chstore.MLSignalLog, Service: "payments-NIGHTLY-job"}, false},
		{"ayar listesi: -batch artık listede yok → kalır", sensWith("-nightly"), chstore.MLEvent{Detector: detLogVolume, Signal: chstore.MLSignalLog, Service: "orders-batch"}, true},
		{"ayar listesi boş = kural kapalı → kalır", sensWith(), chstore.MLEvent{Detector: detLogVolume, Signal: chstore.MLSignalLog, Service: "orders-batch"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := batchKeep(c.ev, c.sens.IsBatchService, loadParts); got != c.keep {
				t.Fatalf("batchKeep=%v, want %v", got, c.keep)
			}
		})
	}
}

func TestBatchFilterCounts(t *testing.T) {
	sens := chstore.AnomalySensitivityConfig{}
	evs := []chstore.MLEvent{
		{Detector: detLogVolume, Service: "orders-batch"},
		{Detector: detOpErrorRate, Service: "orders-batch"},
		{Detector: detLogVolume, Service: "orders-api"},
	}
	kept, cut := batchFilter(evs, sens.IsBatchService, nil)
	if len(cut) != 1 || cut[0].Service != "orders-batch" || len(kept) != 2 || kept[0].Detector != detOpErrorRate || kept[1].Service != "orders-api" {
		t.Fatalf("cut=%+v kept=%+v", cut, kept)
	}
	// isBatch nil = kural kapalı: hiçbiri kesilmez, giriş dilimi aynen
	if k, c := batchFilter(evs, nil, nil); len(c) != 0 || len(k) != 3 {
		t.Fatalf("nil yüklem: cut=%d len=%d", len(c), len(k))
	}
	if len(evs) != 3 || evs[0].Detector != detLogVolume {
		t.Fatal("girdi dilimi değiştirilmemeli")
	}
}

func TestMetricLoadParts(t *testing.T) {
	cfg := chstore.MLConfig{MetricFamilies: map[string][]chstore.MLMetricExpr{
		"metric_kafka": {{ID: "consumer_lag"}, {ID: "msg_rate", Load: true}},
	}}
	got := metricLoadParts(cfg)
	if len(got) != 1 || !got["metric_kafka|msg_rate"] {
		t.Fatalf("got %v", got)
	}
	// varsayılan aileler: hiçbiri yük serisi değil (CPU/heap/lag yükün sonucu)
	if d := metricLoadParts(chstore.MLConfig{MetricFamilies: chstore.DefaultMLMetricFamilies()}); len(d) != 0 {
		t.Fatalf("varsayılanda yük serisi olmamalı: %v", d)
	}
}

func TestErrShareEvidenceUnknown(t *testing.T) {
	if errShareEvidence(5, 0, 1, 10) != nil || errShareEvidence(5, 10, 1, 0) != nil {
		t.Fatal("toplam bilinmiyorsa kanıt yok")
	}
}

// Kablolama: batch süzgeci recorder'da kalite süzgecinden ve yazımdan ÖNCE
// (kesilen aday epizot/rejim izleyicisine de girmez).
func TestRecorderBatchFilterBeforeQuality(t *testing.T) {
	src, err := os.ReadFile("recorder.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	b, q := strings.Index(s, "batchFilter(evs, isBatch, loadParts)"), strings.Index(s, "qualityFilter(all")
	if b < 0 || q < 0 || b > q {
		t.Fatalf("batchFilter (%d) qualityFilter'dan (%d) önce çağrılmalı", b, q)
	}
}

// Kesilen aday günlük tavanı geri verir: yük adayları tavanı bitirmesin.
// Yük adayı üreten dört dedektör iade arayüzünü taşır.
func TestBatchCapRefund(t *testing.T) {
	cfg := chstore.MLConfig{DailyEventCap: 1}
	now := time.Unix(1_800_000_000, 0)
	lr := NewLogRED()
	if !lr.dayCap(detLogVolume, cfg, now) || lr.dayCap(detLogVolume, cfg, now) {
		t.Fatal("tavan 1: ilk alır, ikinci alamaz")
	}
	var rf capRefunder = lr
	rf.refundCap(detLogVolume)
	if !lr.dayCap(detLogVolume, cfg, now) {
		t.Fatal("iade sonrası tavan yeniden alınabilmeli")
	}
	m := map[string]int{}
	refundDaily(m, "x")
	if m["x"] != 0 {
		t.Fatal("sayaç sıfırın altına inmemeli")
	}
	for _, d := range []Detector{NewLogRED(), NewTraceRED(), NewLogChangePoint(), NewMetricML()} {
		if _, ok := d.(capRefunder); !ok {
			t.Fatalf("%s capRefunder değil", d.Name())
		}
	}
}
