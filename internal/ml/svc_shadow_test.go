package ml

// svc_shadow_test.go — v0.10.2089 (spec-ml-service-level dilim 3): servis
// düzeyi gölge kapısı + eski ↔ yeni karşılaştırma. Problem AÇILMAZ.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func svcEv(id, det, svc string, startMin int, base, obs float64) chstore.MLEvent {
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	return chstore.MLEvent{ID: id, Detector: det, Signal: chstore.MLSignalTrace, Service: svc, Stage: chstore.MLStageShadow,
		StartedAt: t0.Add(time.Duration(startMin) * time.Minute).UnixNano(), LastSeen: t0.Add(30 * time.Minute).UnixNano(),
		Baseline: base, Observed: obs, Direction: "up"}
}

func TestDecideSvcShadow(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	prio := chstore.DefaultProblemPriority()
	cases := []struct {
		name   string
		evs    []chstore.MLEvent
		groups []decidedGroup
		check  func(t *testing.T, d map[string]SvcShadow)
	}{
		{"hata oranı 2,5× → açardı P1", []chstore.MLEvent{svcEv("e1", detSvcErrorRate, "orders-api", 0, 0.01, 0.025)}, nil, func(t *testing.T, d map[string]SvcShadow) {
			x := d["e1"]
			if !x.WouldOpen || !x.Owner || x.Priority != "P1" || x.RuleID != "anomaly-auto:svc:orders-api" || x.Value != 0.025 || x.Threshold != 0.01 {
				t.Fatalf("%+v", x)
			}
		}},
		{"taban 0 hata oranı %1 → eşik %0,5, P1", []chstore.MLEvent{svcEv("e1", detSvcErrorRate, "orders-api", 0, 0, 0.01)}, nil, func(t *testing.T, d map[string]SvcShadow) {
			if x := d["e1"]; x.Threshold != 0.005 || x.Priority != "P1" {
				t.Fatalf("%+v", x)
			}
		}},
		{"yavaşlama oranı 1,5 (P2'ye düşen sınır) → P2", []chstore.MLEvent{svcEv("b1", detSvcBreadth, "orders-api", 0, 1000, 1500)}, nil, func(t *testing.T, d map[string]SvcShadow) {
			if x := d["b1"]; !x.WouldOpen || x.Priority != "P2" {
				t.Fatalf("%+v", x)
			}
		}},
		{"iki svc olayı aynı servis → tek sahip (en erken), öncelik en yükseği", []chstore.MLEvent{
			svcEv("b1", detSvcBreadth, "orders-api", 5, 1000, 1500), svcEv("e1", detSvcErrorRate, "orders-api", 0, 0.01, 0.05)}, nil,
			func(t *testing.T, d map[string]SvcShadow) {
				if !d["e1"].Owner || d["b1"].Owner || d["b1"].Priority != "P1" || !strings.Contains(d["b1"].Reason, "üye") {
					t.Fatalf("%+v", d)
				}
				if n, p1 := svcShadowCounts(d); n != 1 || p1 != 1 {
					t.Fatalf("sayaç %d %d", n, p1)
				}
			}},
		{"servisin grup Problem'i açık → katılırdı, sahip değil", []chstore.MLEvent{svcEv("e1", detSvcErrorRate, "orders-api", 0, 0.01, 0.05)},
			[]decidedGroup{{D: GroupDecision{WouldOpen: true, RuleID: "anomaly-auto:group:x"}, Members: []chstore.MLEvent{{Service: "orders-api"}}}},
			func(t *testing.T, d map[string]SvcShadow) {
				if x := d["e1"]; !x.WouldOpen || x.Owner || x.JoinsRule != "anomaly-auto:group:x" {
					t.Fatalf("%+v", x)
				}
			}},
		{"op_latency ve kapanmış svc olayı karar almaz", []chstore.MLEvent{svcEv("o1", detOpLatency, "orders-api", 0, 100, 500),
			func() chstore.MLEvent {
				e := svcEv("e1", detSvcErrorRate, "orders-api", 0, 0.01, 0.05)
				e.Stage = chstore.MLStageClosed
				return e
			}()}, nil,
			func(t *testing.T, d map[string]SvcShadow) {
				if len(d) != 0 {
					t.Fatalf("%+v", d)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.check(t, decideSvcShadow(c.evs, nil, c.groups, prio, now)) })
	}
}

func TestSvcShadowStampRoundTrip(t *testing.T) {
	evs := []chstore.MLEvent{svcEv("e1", detSvcErrorRate, "orders-api", 0, 0.01, 0.05)}
	d := decideSvcShadow(evs, nil, nil, chstore.DefaultProblemPriority(), time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC))
	stampSvcShadow(evs, d)
	got, ok := SvcShadowOf(evs[0].Evidence)
	if !ok || got.Priority != "P1" || !got.WouldOpen {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := SvcShadowOf(`{"x":1}`); ok {
		t.Fatal("kararsız kanıt ok=false")
	}
}

// svc_* olayları tek sinyal kolunu tetiklemez (canlı karşılıkları kendi kapısı).
func TestSvcEventsNotSingleSignal(t *testing.T) {
	for _, det := range []string{detSvcErrorRate, detSvcBreadth} {
		if w, _ := singleWorsening(chstore.MLEvent{Detector: det, Direction: "up"}); w {
			t.Fatalf("%s tek sinyal kolunda sayılmamalı", det)
		}
	}
}

func TestCompareSvcShadow(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	at := func(m int) int64 { return now.Add(time.Duration(m) * time.Minute).UnixNano() }
	olds := []SvcCompareOld{
		{Service: "orders-api", RuleID: "anomaly:orders-api:error_rate", Priority: "P1", Start: at(-120), End: at(-90)},
		{Service: "pay-api", RuleID: "svc-slowdown:pay-api", Priority: "P1", Start: at(-200), End: at(-180)},
		{Service: "cart-api", RuleID: "svc-slowdown:cart-api", Priority: "P1", Start: at(-60), End: 0},
	}
	news := []SvcCompareNew{
		{Service: "orders-api", Detector: detSvcErrorRate, Priority: "P1", Start: at(-115), End: at(-95)},
		{Service: "cart-api", Detector: detSvcBreadth, Priority: "P2", Start: at(-30), End: 0},
		{Service: "stock-api", Detector: detSvcErrorRate, Priority: "P2", Start: at(-10), End: 0},
	}
	rows := CompareSvcShadow(olds, news, now)
	by := map[string]SvcCompareRow{}
	for _, r := range rows {
		by[r.Service] = r
	}
	if by["orders-api"].Diff != "matched" || by["pay-api"].Diff != "missed" || by["cart-api"].Diff != "late" || by["stock-api"].Diff != "extra" {
		t.Fatalf("%+v", rows)
	}
	if len(rows) != 4 || rows[0].Service != "stock-api" {
		t.Fatalf("sıra en yeni önce: %+v", rows)
	}
	for _, r := range []string{"svc-slowdown:x", "anomaly:x:error_rate"} {
		if !SvcCompareOldFamily(r) {
			t.Fatal(r)
		}
	}
	if SvcCompareOldFamily("anomaly:x:p99") || SvcCompareOldFamily("anomaly-auto:group:x") {
		t.Fatal("aile dışı")
	}
}
