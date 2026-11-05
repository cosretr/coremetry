package ml

// v0.10.1814 — backtest'te batch kuralı (audit §13 D8'in replay yarısı):
// canlı tik (recorder batchFilter) ve backtest aynı karar işlevini
// (batchKeep) çağırır; backtest'in olay kurucusu canlı olay kurucularıyla
// (log_red.go event, metric_ml.go) batch kuralının okuduğu alanlarda aynı.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func isBatchName(s string) bool { return strings.HasSuffix(s, "-batch") }

// spikeSeries — 8 gün düz `base`/kova, son gün 12:00–12:30 `spike`.
func spikeSeries(base, spike float64) ([]chstore.MLPoint, time.Time, time.Time) {
	day0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	spikeFrom := day0.Add(7*24*time.Hour + 12*time.Hour).Unix()
	var pts []chstore.MLPoint
	for t := day0.Unix(); t < day0.Add(8*24*time.Hour).Unix(); t += 300 {
		v := base
		if t >= spikeFrom && t < spikeFrom+1800 {
			v = spike
		}
		pts = append(pts, chstore.MLPoint{T: t, V: v})
	}
	return pts, day0.Add(7 * 24 * time.Hour), day0.Add(8 * 24 * time.Hour)
}

func TestRunBacktestBatchRule(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	tot, from, to := spikeSeries(100, 600)
	run := func(svc string, pts map[string][]chstore.MLPoint) map[string]int {
		res := RunBacktest(chstore.MLSignalLog, svc, from, to, pts, cfg, chstore.AnomalyCalendarConfig{}, time.UTC, nil, isBatchName)
		out := map[string]int{}
		for _, d := range res.Detectors {
			out[d.Detector] = d.Fired
		}
		return out
	}
	// yük sıçraması: batch serviste kesilir, diğerinde açılır
	if got := run("orders-api", map[string][]chstore.MLPoint{partTotal: tot}); got[detLogVolume] != 1 {
		t.Fatalf("batch olmayan serviste hacim isabeti beklenir: %v", got)
	}
	if got := run("orders-batch", map[string][]chstore.MLPoint{partTotal: tot}); got[detLogVolume] != 0 {
		t.Fatalf("batch serviste hacim isabeti olmamalı: %v", got)
	}
	// hata SAYISI hacimle orantılı arttı (pay sabit): batch'te kesilir
	errProp, _, _ := spikeSeries(10, 60)
	if got := run("orders-batch", map[string][]chstore.MLPoint{partTotal: tot, partError: errProp}); got[detErrorRate] != 0 {
		t.Fatalf("pay artmadan hata sayısı batch'te kesilmeli: %v", got)
	}
	// hacim düz, hata 6× → pay ≥ 3×: batch'te de kalır
	flat, _, _ := spikeSeries(100, 100)
	if got := run("orders-batch", map[string][]chstore.MLPoint{partTotal: flat, partError: errProp}); got[detErrorRate] != 1 {
		t.Fatalf("pay 6× arttı, batch'te de isabet beklenir: %v", got)
	}
}

// Canlı olay kurucusunun batch alanları ↔ backtest olay kurucusu: aynı
// girdide batchKeep aynı kararı verir.
func TestBacktestBatchEventMatchesLive(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.MetricFamilies["metric_k8s"] = append(cfg.MetricFamilies["metric_k8s"], chstore.MLMetricExpr{ID: "rps", Expr: "x", Direction: "up", Load: true})
	load := metricLoadParts(cfg)
	share := errShareEvidence(60, 100, 10, 100) // pay 6×
	shareFlat := errShareEvidence(60, 600, 10, 100)
	live := (&LogRED{}).event
	cases := []struct {
		name   string
		live   chstore.MLEvent
		signal string
		part   string
		share  map[string]any
	}{
		{"log hacim", live(detLogVolume, "orders-batch", partTotal, Judgement{}, time.Unix(0, 0), cfg, "weekday", 0, nil), "log", partTotal, nil},
		{"log hata pay 6×", withEvidence(live(detErrorRate, "orders-batch", partError, Judgement{}, time.Unix(0, 0), cfg, "weekday", 0, nil), share), "log", partError, share},
		{"log hata pay sabit", withEvidence(live(detErrorRate, "orders-batch", partError, Judgement{}, time.Unix(0, 0), cfg, "weekday", 0, nil), shareFlat), "log", partError, shareFlat},
		{"metrik yük serisi", chstore.MLEvent{Detector: "metric_k8s", Signal: "metric", Service: "orders-batch", Code: metricPart("metric_k8s", "rps")}, "metric", metricPart("metric_k8s", "rps"), nil},
		{"metrik cpu", chstore.MLEvent{Detector: "metric_k8s", Signal: "metric", Service: "orders-batch", Code: metricPart("metric_k8s", "cpu")}, "metric", metricPart("metric_k8s", "cpu"), nil},
		{"trace throughput", chstore.MLEvent{Detector: detThroughputDrop, Signal: "trace", Service: "orders-batch"}, "trace", "/a|count", nil},
		{"trace gecikme", chstore.MLEvent{Detector: detOpLatency, Signal: "trace", Service: "orders-batch"}, "trace", "/a|slow", nil},
	}
	for _, c := range cases {
		bt := backtestBatchEvent(c.signal, "orders-batch", c.part, c.live.Detector, c.share)
		if batchKeep(c.live, isBatchName, load) != batchKeep(bt, isBatchName, load) {
			t.Errorf("%s: canlı %v ≠ backtest %v", c.name, batchKeep(c.live, isBatchName, load), batchKeep(bt, isBatchName, load))
		}
	}
}

func withEvidence(ev chstore.MLEvent, sh map[string]any) chstore.MLEvent {
	ev.Evidence = mergeEvidence(ev.Evidence, sh)
	return ev
}
