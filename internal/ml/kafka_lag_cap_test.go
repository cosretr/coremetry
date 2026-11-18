package ml

// v0.10.2358 — Operator-reported: consumer lag olayı "Bitti · 3 sa 2 dk
// sürdü" dedi, lag hâlâ tekdüze artıyordu. Kök neden: günlük tavan
// (DailyEventCap) her TETİKLENEN tikte harcanıyordu; tavan dolunca açık
// olayın adayı da düşüyor, koşu 3 tik sonra kapanıyordu. Artık açık koşuyu
// uzatan aday tavan harcamaz. Fikstür nötr; saat dilimi / gün saatinden
// bağımsız (yerel öğlen, 5 sa — gece yarısı geçilmez).

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestKafkaLagEventStaysOpenWhileRising(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.DailyEventCap = 10
	tick := time.Duration(cfg.TickSec) * time.Second
	t0 := time.Date(2026, 6, 10, 12, 0, 0, 0, time.Local)
	const svc = "orders-consumer"
	part := kafkaTopicPart("payments-events")
	cand := func(at time.Time, lag float64) chstore.MLEvent {
		return chstore.MLEvent{ID: chstore.MLEventID("metric_kafka", svc, part), Detector: "metric_kafka", Signal: chstore.MLSignalMetric,
			Service: svc, Code: part, StartedAt: at.UnixNano(), LastSeen: at.Add(tick).UnixNano(), Baseline: 0, Observed: lag, Score: 50, Direction: "up"}
	}
	// run — n tik boyunca lag tekdüze artar (190k → …); döner: ilk kapanış tiki (-1 = yok).
	run := func(withOpen bool, n int) int {
		d, tr := NewMetricML(), newTracker()
		var deps Deps // withOpen=false: openRun nil (düzeltme öncesi davranış)
		for i := 0; i < n; i++ {
			now := t0.Add(time.Duration(i) * tick)
			if withOpen {
				deps.openRun = func(id string) bool { return tr.openRun(id, now, tick) }
			}
			var evs []chstore.MLEvent
			if admitMetric(deps, "metric_kafka", svc, part, func(fam string) bool { return d.dayCap(fam, cfg, now) }) {
				evs = append(evs, cand(now, 190_000+float64(i)*2_000))
			}
			qualityFilter(evs, cfg, tr, now)
			if len(tr.closeStale(now)) > 0 {
				return i
			}
		}
		return -1
	}
	// Eski davranış (açık koşu bilgisi yok): tavan tikle harcanır → 10 tik
	// sonra aday düşer, 3 tik sonra "bitti" — koşul sürerken.
	if got := run(false, 60); got != cfg.DailyEventCap+episodeCloseMisses-1 {
		t.Fatalf("eski yol tavan+3 tikte kapanmalıydı (hata kanıtı), kapanış tiki %d", got)
	}
	// Düzeltme: 60 tik (5 sa) boyunca lag artar, olay açık kalır.
	if got := run(true, 60); got != -1 {
		t.Fatalf("lag artarken olay kapandı (tik %d)", got)
	}
}

// Tavan YENİ olayları saymaya devam eder: açık olaylar uzarken tavan dolu
// ise yeni topic olayı açılmaz.
func TestDailyCapStillLimitsNewEvents(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.DailyEventCap = 10
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.Local)
	d := NewMetricML()
	open := map[string]bool{}
	deps := Deps{openRun: func(id string) bool { return open[id] }}
	take := func(fam string) bool { return d.dayCap(fam, cfg, now) }
	for i := 0; i < cfg.DailyEventCap; i++ {
		p := kafkaTopicPart("payments-events-" + string(rune('a'+i)))
		if !admitMetric(deps, "metric_kafka", "orders-consumer", p, take) {
			t.Fatalf("yeni olay %d tavan altında açılmalı", i)
		}
		open[chstore.MLEventID("metric_kafka", "orders-consumer", p)] = true
	}
	if admitMetric(deps, "metric_kafka", "orders-consumer", kafkaTopicPart("payments-events-z"), take) {
		t.Fatal("tavan dolu: yeni olay açılmamalı")
	}
	if !admitMetric(deps, "metric_kafka", "orders-consumer", kafkaTopicPart("payments-events-a"), take) {
		t.Fatal("tavan dolu iken açık olay uzamalı")
	}
}
