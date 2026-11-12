package ml

// v0.10.2058 — Davis tek-olay kolu, YALNIZ GÖLGE (docs/anomaly/davis-ml-gap.md
// dilim 4): tek sinyalli grupta ≥ 600 sn süren, kötüleşme yönünde ≥ 2 kat
// (taban 0: sayım ≥ 20) olay "tek sinyal: Problem açardı" alır; WouldOpen
// false kalır (canlı yol Problem açmaz). ≥ 2 sinyal kararı bayt bayt aynı.
// Servis adları SENTETİK.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestDecideGroupSingleSignalShadow(t *testing.T) {
	isBatch := func(s string) bool { return strings.HasSuffix(s, "-batch") }
	in := GroupGateInput{IsBatch: isBatch}
	now := gBase.Add(30 * time.Minute)
	down := func(e chstore.MLEvent) chstore.MLEvent { e.Direction = "down"; return e }
	cases := []struct {
		name    string
		ev      chstore.MLEvent
		want    bool
		wantWhy string
	}{
		{"güçlü + uzun (30 dk, 5 kat gecikme) → gölge açardı", gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 500), true, "normalin 5.0 katı"},
		{"güçlü ama 300 sn → hayır", gateEv("a", detOpLatency, "orders-api", 25, 30, 100, 500), false, ""},
		{"tam 600 sn → evet (sınır dahil)", gateEv("a", detOpLatency, "orders-api", 20, 30, 100, 500), true, "10 dk sürdü"},
		{"uzun ama 1,5 kat → hayır", gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 150), false, ""},
		{"tam 2 kat → evet", gateEv("a", detOpErrorRate, "orders-api", 0, 30, 0.1, 0.2), true, "normalin 2.0 katı"},
		{"iyileşme yönü (gecikme düştü) → hayır", down(gateEv("a", detOpLatency, "orders-api", 0, 30, 500, 100)), false, ""},
		{"trafik düşüşü (yük, kötüleşme sayılmaz) → hayır", down(gateEv("a", detThroughputDrop, "orders-api", 0, 30, 100, 10)), false, ""},
		{"taban 0, hata sayısı 20 → evet", gateEv("a", detTraceErrorCount, "orders-api", 0, 30, 0, 20), true, "normalde hiç yokken 20"},
		{"taban 0, hata sayısı 19 → hayır", gateEv("a", detTraceErrorCount, "orders-api", 0, 30, 0, 19), false, ""},
		{"taban 0, gecikme (sayı değil) → hayır", gateEv("a", detOpLatency, "orders-api", 0, 30, 0, 900), false, ""},
		{"batch servis, yalnız yük (log hacmi) → hayır", gateEv("a", detLogVolume, "billing-batch", 0, 30, 100, 900), false, ""},
		{"batch servis, gerçek hata oranı → evet", gateEv("a", detErrorRate, "billing-batch", 0, 30, 10, 40), true, "error_rate"},
		{"Kafka lag 3 kat → evet", func() chstore.MLEvent {
			e := gateEv("a", "metric_kafka", "orders-consumer", 0, 30, 1000, 3000)
			e.Code = "metric_kafka|consumer_lag"
			return e
		}(), true, "metric_kafka"},
		{"kapanmış olay → hayır", closed(gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 500)), false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gs := GroupMLEvents([]chstore.MLEvent{c.ev}, 10*time.Minute)
			if len(gs) != 1 {
				t.Fatalf("tek grup bekleniyordu: %d", len(gs))
			}
			d := DecideGroupPromotion(gs[0], []chstore.MLEvent{c.ev}, in, now)
			if d.WouldOpen || d.Severity != "" || d.Priority != "" || d.Live {
				t.Fatalf("tek sinyal canlı karar üretmemeli: %+v", d)
			}
			if d.SingleWouldOpen != c.want {
				t.Fatalf("SingleWouldOpen=%v, istenen %v (%s)", d.SingleWouldOpen, c.want, d.Reason)
			}
			if c.want && (!strings.Contains(d.Reason, "tek sinyal: Problem açardı (gölge)") || !strings.Contains(d.Reason, c.wantWhy)) {
				t.Fatalf("neden %q; istenen ~%q", d.Reason, c.wantWhy)
			}
		})
	}
}

// ≥ 2 sinyal kararı (canlı yolun tek girdisi) bayt bayt pinli: tek sinyal
// kolu eklenince değişmemeli; uzun + güçlü üyeler olsa da SingleWouldOpen yok.
func TestDecideGroupTwoSignalsLiveUnchanged(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	evs := []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 500), gateEv("l", detErrorRate, "orders-api", 5, 20, 10, 14)}
	gs := GroupMLEvents(evs, 10*time.Minute)
	if len(gs) != 1 {
		t.Fatalf("tek grup bekleniyordu: %d", len(gs))
	}
	d := DecideGroupPromotion(gs[0], evs, GroupGateInput{}, now)
	got, _ := json.Marshal(d)
	want := `{"group":"` + gs[0].ID + `","wouldOpen":true,"rule":"anomaly-auto:group:a","severity":"critical","priority":"P2",` +
		`"reason":"açardı: 2 farklı sinyal teyit etti (log, trace), en büyük sapma 5.0 kat","detectors":["error_rate","op_latency"],` +
		`"signals":["log","trace"],"at":` + jsonInt(now.Unix()) + `}`
	if string(got) != want {
		t.Fatalf("karar değişti:\n got %s\nwant %s", got, want)
	}
	// sayaç: tek sinyal kolu yalnız kendi sayacına
	_, c := decideGroups(evs, nil, 10*time.Minute, GroupGateInput{}, now)
	if c.WouldOpen != 1 || c.SingleWouldOpen != 0 {
		t.Fatalf("sayaçlar %+v", c)
	}
	_, c1 := decideGroups(evs[:1], nil, 10*time.Minute, GroupGateInput{}, now)
	if c1.WouldOpen != 0 || c1.SingleWouldOpen != 1 {
		t.Fatalf("tek sinyal sayaçları %+v", c1)
	}
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

// Canlı plan: "tek sinyal: açardı" kararı Problem AÇMAZ (bu sürümde gölge).
func TestSingleSignalShadowNeverOpensLive(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	evs := []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 500)}
	gs, c := decideGroups(evs, nil, 10*time.Minute, GroupGateInput{}, now)
	if c.SingleWouldOpen != 1 || len(gs) != 1 || !gs[0].D.SingleWouldOpen {
		t.Fatalf("ön koşul: tek sinyal gölge kararı bekleniyordu: %+v", c)
	}
	plan := planGroupProblems(gs, nil, true, func(decidedGroup) string { return "x" })
	if len(plan.Open) != 0 || len(plan.Refresh) != 0 {
		t.Fatalf("tek sinyal canlıda Problem açtı/tazeledi: %+v", plan)
	}
}
