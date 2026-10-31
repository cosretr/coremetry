package ml

// v0.10.1751 — kalite çerçevesi: etki eşiği, geri bildirim z artışı, eksik
// veri, rejim değişimi, teyit skoru ve backtest (ortak etiket seti).

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestImpactAndFeedback(t *testing.T) {
	q := chstore.DefaultMLQuality()
	cases := []struct {
		base, obs float64
		want      bool
	}{{100, 110, false}, {100, 130, true}, {0, 5, true}, {100, 0, true}}
	for _, c := range cases {
		if got := impactOK(c.base, c.obs, q); got != c.want {
			t.Errorf("impactOK(%v,%v)=%v want %v", c.base, c.obs, got, c.want)
		}
	}
	q.ImpactMinAbs = 50
	if impactOK(100, 140, q) {
		t.Error("mutlak eşik altında olay olmamalı")
	}
	q = chstore.DefaultMLQuality()
	fz := []struct {
		neg, pos int
		want     float64
	}{{0, 0, 4}, {2, 0, 4}, {3, 0, 4.5}, {5, 0, 5.5}, {5, 5, 4}, {40, 0, 8}}
	for _, c := range fz {
		if got := feedbackZ(4, c.neg, c.pos, q); got != c.want {
			t.Errorf("feedbackZ(neg=%d,pos=%d)=%v want %v", c.neg, c.pos, got, c.want)
		}
	}
	q.FeedbackOff = true
	if feedbackZ(4, 40, 0, q) != 4 {
		t.Error("kapalı geri bildirim eşiği değiştirmemeli")
	}
}

func TestDwellWindowMissing(t *testing.T) {
	byB := map[int64]float64{0: 1, 300: 2}
	if _, ok := dwellWindow(byB, 0, 600, 300, false); ok {
		t.Fatal("VM: eksik kova → değerlendirme yok")
	}
	cur, ok := dwellWindow(byB, 0, 600, 300, true)
	if !ok || len(cur) != 3 || cur[2] != 0 {
		t.Fatalf("log: eksik kova = 0, got %v %v", cur, ok)
	}
}

func TestQualityFilterRegimeAndConfirm(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	tr := newTracker()
	t0 := time.Unix(1_800_000_000, 0)
	mk := func(id, det, sig string) chstore.MLEvent {
		return chstore.MLEvent{ID: id, Detector: det, Signal: sig, Service: "pay", Baseline: 100, Observed: 200, Score: 6}
	}
	// teyit: aynı tikte log + trace olayı → birbirini teyit eder
	kept, sup := qualityFilter([]chstore.MLEvent{mk("a", "log_volume", "log"), mk("b", "op_latency", "trace")}, cfg, nil, tr, t0)
	if len(kept) != 2 || sup != (suppression{}) {
		t.Fatalf("kept=%d sup=%+v", len(kept), sup)
	}
	var ev map[string]any
	_ = json.Unmarshal([]byte(kept[0].Evidence), &ev)
	if ev["confirmScore"].(float64) != 1 || ev["confirmSignals"].(float64) != 1 || ev["configHash"] == "" {
		t.Fatalf("teyit kanıtı: %v", ev)
	}
	if kept[0].StartedAt != t0.UnixNano() {
		t.Fatal("StartedAt koşu başı olmalı")
	}
	// rejim: 6 saat + 1 tik kesintisiz → bastırılır
	var last []chstore.MLEvent
	for i := 1; i <= 6*12+1; i++ {
		last, sup = qualityFilter([]chstore.MLEvent{mk("a", "log_volume", "log")}, cfg, nil, tr, t0.Add(time.Duration(i)*5*time.Minute))
	}
	if len(last) != 0 || sup.Regime != 1 {
		t.Fatalf("rejim bastırması bekleniyordu: kept=%d sup=%+v", len(last), sup)
	}
	// kesinti sonrası koşu baştan
	if k, _ := qualityFilter([]chstore.MLEvent{mk("a", "log_volume", "log")}, cfg, nil, tr, t0.Add(20*time.Hour)); len(k) != 1 {
		t.Fatal("kesintiden sonra yeni koşu")
	}
	// etki + geri bildirim
	small := mk("c", "log_volume", "log")
	small.Observed = 105
	lb := NewLabels([]chstore.MLLabel{{Detector: "log_volume", Service: "pay"}, {Detector: "log_volume", Service: "pay"}, {Detector: "log_volume", Service: "pay"}, {Detector: "log_volume", Service: "pay"}})
	weak := mk("d", "log_volume", "log")
	weak.Score = 4.2
	_, sup = qualityFilter([]chstore.MLEvent{small, weak}, cfg, lb, newTracker(), t0)
	if sup.Impact != 1 || sup.Feedback != 1 {
		t.Fatalf("etki + geri bildirim bastırması: %+v", sup)
	}
}

// Sentetik log serisi: 8 gün düz 100/kova, son gün 12:00–12:30 600 → log_volume
// isabeti; gerçek etiket örtüşür → TP, recall 1. Hijyen: etiketli pencere
// tabana girmez.
func TestRunBacktestLogSpike(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	day0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var pts []chstore.MLPoint
	spikeFrom := day0.Add(7*24*time.Hour + 12*time.Hour).Unix()
	for t := day0.Unix(); t < day0.Add(8*24*time.Hour).Unix(); t += 300 {
		v := 100.0
		if t >= spikeFrom && t < spikeFrom+1800 {
			v = 600
		}
		pts = append(pts, chstore.MLPoint{T: t, V: v})
	}
	labels := []chstore.MLLabel{{Service: "pay", Signal: "trace", Detector: "op_latency", From: spikeFrom, To: spikeFrom + 900, Positive: true}}
	from, to := day0.Add(7*24*time.Hour), day0.Add(8*24*time.Hour)
	res := RunBacktest(chstore.MLSignalLog, "pay", from, to, map[string][]chstore.MLPoint{partTotal: pts}, cfg, chstore.AnomalyCalendarConfig{}, time.UTC, labels)
	if res.Series != 1 || len(res.Detectors) != 1 {
		t.Fatalf("res=%+v", res)
	}
	d := res.Detectors[0]
	if d.Detector != detLogVolume || d.Fired != 1 || d.TP != 1 || d.Recall != 1 || d.Precision != 1 {
		t.Fatalf("detector=%+v hits=%+v", d, res.Hits)
	}
	if h := res.Hits[0]; h.From != spikeFrom+300 || h.Label != chstore.VerdictAnomaly {
		t.Fatalf("hit=%+v (dwell 2: ilk tetik ikinci kova)", h)
	}
	// "değil" etiketi → FP, precision 0
	labels[0].Positive = false
	res = RunBacktest(chstore.MLSignalLog, "pay", from, to, map[string][]chstore.MLPoint{partTotal: pts}, cfg, chstore.AnomalyCalendarConfig{}, time.UTC, labels)
	if d := res.Detectors[0]; d.FP != 1 || d.Precision != 0 || d.Positives != 0 {
		t.Fatalf("FP bekleniyordu: %+v", d)
	}
	// what-if: etki eşiği ×10 → 5 katlık sıçrama olay değil
	cfg.Quality.ImpactMinRel = 10
	res = RunBacktest(chstore.MLSignalLog, "pay", from, to, map[string][]chstore.MLPoint{partTotal: pts}, cfg, chstore.AnomalyCalendarConfig{}, time.UTC, nil)
	if len(res.Hits) != 0 {
		t.Fatalf("etki eşiği 10'da isabet olmamalı: %+v", res.Hits)
	}
}

func TestRulesFor(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cases := []struct {
		sig, part, det string
		ok             bool
	}{
		{"log", "_total", detLogVolume, true}, {"log", "_error", detErrorRate, true}, {"log", "warn", "", false},
		{"trace", "/pay|count", detThroughputDrop, true}, {"trace", "/pay|p95", detOpLatency, true},
		{"trace", "/pay|err_ratio", detOpErrorRate, true}, {"trace", "/pay|err", "", false},
		{"metric", "metric_jvm|heap_used", "metric_jvm", true}, {"metric", "metric_jvm|nope", "", false},
	}
	for _, c := range cases {
		r, ok := rulesFor(c.sig, c.part, cfg)
		if ok != c.ok || r.detector != c.det {
			t.Errorf("rulesFor(%s,%s)=%q,%v", c.sig, c.part, r.detector, ok)
		}
	}
}
