package ml

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2396 (operatör kararı 2026-11-20): etki kuralı consumer lag'inin tek
// sinyal Problem'i gözlenen ≥ 2 × tabansa P1 — mevcut computePriority
// bigBreach kuralıyla (Value = lag, Threshold = taban); altında P2 aynen.

func TestImpactLagBigBreach(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	ev := func(base, obs float64, evidence string) chstore.MLEvent {
		return lagGateEv("a", now, 4*time.Minute, base, obs, evidence)
	}
	imp := lagImpactEvidence(true, 1200, 4)
	thanos := ev(600, 165000, mergeEvidence("", map[string]any{"source": "thanos", "dwell": 2}))
	thanos.Code = lagSvcPart + "@prod-east"
	otherPart := ev(600, 165000, imp)
	otherPart.Code = "metric_kafka|fetch_throttle"
	for _, tc := range []struct {
		name  string
		e     chstore.MLEvent
		ok    bool
		v, th float64
	}{
		{"165 bin, taban 600 → P1 ölçüsü", ev(600, 165000, imp), true, 165000, 600},
		{"tam 2 kat → P1 ölçüsü", ev(600, 1200, imp), true, 1200, 600},
		{"1,5 kat → yok (P2 aynen)", ev(600, 900, imp), false, 0, 0},
		{"taban 0 → P1 ölçüsü, eşik 1 kayıt", ev(0, 5000, imp), true, 5000, 1},
		{"etki damgasız lag → yok", ev(600, 165000, `{"source":"vm","dwell":2}`), false, 0, 0},
		{"Thanos lag → yok", thanos, false, 0, 0},
		{"consumer_lag dışı kafka serisi → yok", otherPart, false, 0, 0},
	} {
		v, th, ok := impactLagBigBreach([]chstore.MLEvent{tc.e}, "a")
		if ok != tc.ok || v != tc.v || th != tc.th {
			t.Errorf("%s: %v %v %v", tc.name, v, th, ok)
		}
	}
	if _, _, ok := impactLagBigBreach([]chstore.MLEvent{ev(600, 165000, imp)}, "b"); ok {
		t.Error("tetikleyen üye başka olaysa ölçü yok")
	}
}

// Karar → açılan satır → computePriority: ≥ 2 × P1, altı P2; lider okuması sürer.
func TestGroupLagProblemPriority(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	cfg := chstore.DefaultProblemPriority()
	for _, tc := range []struct {
		name   string
		base   float64
		obs    float64
		prio   string
		metric string
	}{
		{"165 bin kayıt, taban 600", 600, 165000, "P1", groupLagMetric},
		{"taban 0", 0, 5000, "P1", groupLagMetric},
		{"1,5 kat", 600, 900, "P2", groupProblemMetric},
	} {
		e := lagGateEv("a", now, 4*time.Minute, tc.base, tc.obs, lagImpactEvidence(true, 1200, 4))
		gs := GroupMLEvents([]chstore.MLEvent{e}, 10*time.Minute)
		d := DecideGroupPromotion(gs[0], []chstore.MLEvent{e}, GroupGateInput{}, now)
		if d.Priority != tc.prio {
			t.Fatalf("%s: karar önceliği %s (%+v)", tc.name, d.Priority, d)
		}
		plan := planGroupProblems([]decidedGroup{{G: gs[0], D: d, Members: []chstore.MLEvent{e}}}, nil, true, func(decidedGroup) string { return "x" })
		if len(plan.Open) != 1 {
			t.Fatalf("%s: açılış %+v", tc.name, plan)
		}
		p := plan.Open[0]
		if got, why := chstore.PreviewProblemPriority(p, now.UnixNano(), cfg); got != tc.prio || p.Metric != tc.metric {
			t.Fatalf("%s: satır %s %s (%s) %+v", tc.name, got, p.Metric, why, p)
		}
		if id, ok := ImpactFactsLeadOf(p); !ok || id != "a" {
			t.Fatalf("%s: lider okunmalı (Neden satırları): %q %v", tc.name, id, ok)
		}
	}
}

// Tazeleme: öncelik yalnız yükselir — P2 satırı lag ≥ 2 × ile P1'e çıkar;
// P1 lag satırı sonraki düşük oranlı tikte inmez.
func TestGroupLagRefreshOnlyRaises(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	decide := func(base, obs float64) decidedGroup {
		e := lagGateEv("a", now, 4*time.Minute, base, obs, lagImpactEvidence(true, 1200, 4))
		gs := GroupMLEvents([]chstore.MLEvent{e}, 10*time.Minute)
		return decidedGroup{G: gs[0], D: DecideGroupPromotion(gs[0], []chstore.MLEvent{e}, GroupGateInput{}, now), Members: []chstore.MLEvent{e}}
	}
	desc := func(decidedGroup) string { return "x" }
	p2 := planGroupProblems([]decidedGroup{decide(600, 900)}, nil, true, desc).Open[0]
	up := planGroupProblems([]decidedGroup{decide(600, 165000)}, []chstore.Problem{p2}, true, desc)
	if len(up.Refresh) != 1 || up.Refresh[0].Metric != groupLagMetric || up.Refresh[0].Value != 165000 || up.Refresh[0].Threshold != 600 {
		t.Fatalf("P2 → P1 yükselmeli: %+v", up.Refresh)
	}
	p1 := up.Refresh[0]
	for name, g := range map[string]decidedGroup{"1,5 kat": decide(600, 900), "daha düşük 2+ kat": decide(600, 2000)} {
		down := planGroupProblems([]decidedGroup{g}, []chstore.Problem{p1}, true, desc)
		if len(down.Refresh) != 1 || down.Refresh[0].Value != 165000 || down.Refresh[0].Metric != groupLagMetric {
			t.Fatalf("%s: öncelik inmemeli: %+v", name, down.Refresh)
		}
	}
}
