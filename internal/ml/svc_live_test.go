package ml

// svc_live_test.go — v0.10.2130 (spec docs/plans/spec-ml-service-level.md
// dilim 4, operatör kararı 2026-11-14 "Yenileri canlıya al"): servis düzeyi
// ML kararı gerçek Problem açar. Saf dikişler: svcPromote (Value/Threshold →
// computePriority ≥ 2× P1, altı P2), ownerDedupe (servis başına tek ML
// Problem'i; sahiplik ilk açanda, öncelik yalnız yükselir), kapanış, eski
// kural ikizi notu, düz cümle. Servis adları SENTETİK.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func svcIn(svc string, v, th float64, evs ...string) svcProblemIn {
	return svcProblemIn{Service: svc, Value: v, Threshold: th, Metric: "error_rate", StartedAt: gBase.UnixNano(),
		Desc: svc + " servisinde hata oranı", Events: evs}
}

func freshPriority(p chstore.Problem) string {
	p.StartedAt = time.Now().UnixNano() // 4 sa staleCrit kuralı dışında
	return chstore.EnrichProblemsWithPriority([]chstore.Problem{p})[0].Priority
}

func TestSvcPromote(t *testing.T) {
	cases := []struct {
		name  string
		v, th float64
		want  string
	}{
		{"2,5× → P1", 0.025, 0.01, "P1"},
		{"tam 2× → P1", 0.02, 0.01, "P1"},
		{"1,5× → P2", 1500, 1000, "P2"},
	}
	for _, c := range cases {
		p := planMLProblems(nil, []svcProblemIn{svcIn("orders-api", c.v, c.th, "e1")}, nil, true, descStub)
		if len(p.Open) != 1 {
			t.Fatalf("%s: plan %+v", c.name, p)
		}
		o := p.Open[0]
		if o.ID != "anomaly-auto:svc:orders-api" || o.RuleID != o.ID || o.Severity != "critical" || o.Value != c.v || o.Threshold != c.th ||
			!strings.HasPrefix(o.RuleName, chstore.MLPromotedRuleNamePrefix) {
			t.Errorf("%s: satır %+v", c.name, o)
		}
		if got := freshPriority(o); got != c.want {
			t.Errorf("%s: öncelik %s, istenen %s", c.name, got, c.want)
		}
		if chstore.ProblemNotifyKind(o) != chstore.NotifyKindAnomaly || !p.SvcOpen[o.ID] || p.LiveSvc["e1"] != o.ID {
			t.Errorf("%s: bildirim türü / sayaç / canlı işaret: %+v", c.name, p)
		}
		if h, ok := chstore.MLPromotedHeadline(o); !ok || h != o.Description {
			t.Errorf("%s: manşet düz cümle olmalı: %q", c.name, h)
		}
	}
}

func TestOwnerDedupe(t *testing.T) {
	grp := liveGroup("orders-api", "ev1", "log", "trace")
	openGroup := newGroupProblem(grp, "grup cümlesi")
	openGroup.Status, openGroup.Assignee = "acknowledged", "ops@acme.example"
	openSvc := newSvcProblem(svcIn("orders-api", 0.03, 0.01, "e0"))
	openSvc.Status, openSvc.Assignee = "acknowledged", "ops@acme.example"

	t.Run("grup Problem'i açık → svc üye; yeni açılış yok, oran yükselir", func(t *testing.T) {
		p := planMLProblems(nil, []svcProblemIn{svcIn("orders-api", 0.025, 0.01, "e1")}, []chstore.Problem{openGroup}, true, descStub)
		if len(p.Open) != 0 || len(p.Refresh) != 1 || len(p.Resolve) != 0 {
			t.Fatalf("plan: %+v", p)
		}
		r := p.Refresh[0]
		if r.ID != openGroup.ID || r.Value != 0.025 || r.Threshold != 0.01 || r.Metric != "error_rate" || r.Description != "grup cümlesi" ||
			r.Status != "acknowledged" || r.Assignee != "ops@acme.example" || p.LiveSvc["e1"] != openGroup.ID {
			t.Errorf("tazeleme: %+v", r)
		}
		if freshPriority(r) != "P1" {
			t.Errorf("öncelik P1'e yükselmeli")
		}
	})
	t.Run("svc Problem'i açık → grup üye; sayım satırı oranı düşürmez", func(t *testing.T) {
		p := planMLProblems([]decidedGroup{grp}, nil, []chstore.Problem{openSvc}, true, descStub)
		if len(p.Open) != 0 || len(p.Refresh) != 1 || len(p.Resolve) != 0 {
			t.Fatalf("plan: %+v", p)
		}
		r := p.Refresh[0]
		if r.ID != openSvc.ID || r.Value != 0.03 || r.Threshold != 0.01 || r.Status != "acknowledged" || p.LiveGroup[grp.G.ID] != openSvc.ID {
			t.Errorf("tazeleme: %+v live=%v", r, p.LiveGroup)
		}
	})
	t.Run("aynı tik: grup + svc → TEK Problem (grup sahip), oran svc'den", func(t *testing.T) {
		p := planMLProblems([]decidedGroup{grp}, []svcProblemIn{svcIn("orders-api", 0.025, 0.01, "e1")}, nil, true, descStub)
		if len(p.Open) != 1 || len(p.Refresh) != 0 {
			t.Fatalf("plan: %+v", p)
		}
		o := p.Open[0]
		if !chstore.IsMLGroupProblem(o.RuleID) || o.Value != 0.025 || p.SvcOpen[o.ID] || p.LiveSvc["e1"] != o.ID {
			t.Errorf("açılış: %+v", o)
		}
	})
	t.Run("öncelik yalnız yükselir: düşük oran Value/Threshold'u indirmez, cümle güncel", func(t *testing.T) {
		in := svcIn("orders-api", 0.012, 0.01, "e1")
		in.Desc = "yeni cümle"
		p := planMLProblems(nil, []svcProblemIn{in}, []chstore.Problem{openSvc}, true, descStub)
		if len(p.Refresh) != 1 || p.Refresh[0].Value != 0.03 || p.Refresh[0].Threshold != 0.01 || p.Refresh[0].Description != "yeni cümle" {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("başka servis → ayrı Problem", func(t *testing.T) {
		p := planMLProblems(nil, []svcProblemIn{svcIn("payments-api", 0.03, 0.01, "e2")}, []chstore.Problem{openSvc}, false, descStub)
		if len(p.Open) != 1 || p.Open[0].Service != "payments-api" {
			t.Fatalf("plan: %+v", p)
		}
	})
}

func TestSvcProblemLifecycle(t *testing.T) {
	openSvc := newSvcProblem(svcIn("orders-api", 0.03, 0.01, "e0"))
	t.Run("açık olay kalmadı → 3 tik kuralıyla kapanır", func(t *testing.T) {
		p := planMLProblems(nil, nil, []chstore.Problem{openSvc}, true, descStub)
		if len(p.Resolve) != 1 || !strings.HasSuffix(p.Resolve[0].Description, svcResolveReason) {
			t.Fatalf("plan: %+v", p)
		}
		if p := planMLProblems(nil, nil, []chstore.Problem{openSvc}, false, descStub); len(p.Resolve) != 0 {
			t.Errorf("lider sürekliliği yokken kapanmaz: %+v", p)
		}
	})
	t.Run("svc olayı bitti ama servisin grubu sürüyor → açık kalır", func(t *testing.T) {
		g := liveGroup("orders-api", "ev1", "trace") // tek sinyal, açmaz ama bitmedi
		p := planMLProblems([]decidedGroup{g}, nil, []chstore.Problem{openSvc}, true, descStub)
		if len(p.Resolve) != 0 || len(p.Refresh) != 1 {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("kapanmış satır sahip sayılmaz → yeni açılış", func(t *testing.T) {
		closed := openSvc
		closed.Status = "resolved"
		p := planMLProblems(nil, []svcProblemIn{svcIn("orders-api", 0.03, 0.01, "e1")}, []chstore.Problem{closed}, true, descStub)
		if len(p.Open) != 1 || len(p.Resolve) != 0 {
			t.Fatalf("plan: %+v", p)
		}
	})
}

func TestSvcProblemInputs(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	er := svcEv("e1", detSvcErrorRate, "orders-batch", 5, 0.01, 0.034)
	br := svcEv("b1", detSvcBreadth, "orders-batch", 0, 400, 1200)
	br.Evidence = `{"slowOps":[{"op":"GET /a"}],"slowOpsTotal":4}`
	vol := svcEv("v1", chstore.MLDetSvcVolumeSpike, "orders-batch", 0, 10, 100)
	kept := []chstore.MLEvent{br, er, vol}
	// -batch serviste hata oranı AÇAR (batchKeep yük sinyali saymaz; hata gerçek).
	ds := decideSvcShadow(kept, nil, nil, chstore.DefaultProblemPriority(), now)
	if _, ok := ds["v1"]; ok {
		t.Fatal("hacim sıçraması servis düzeyi karar almaz (Problem açmaz)")
	}
	in := svcProblemInputs(kept, ds, func(string) string { return " NOT" })
	if len(in) != 1 {
		t.Fatalf("girdi: %+v", in)
	}
	x := in[0]
	want := "orders-batch servisinde hata oranı %3,4, olağanı %1. orders-batch servisinde 4 operasyon birden yavaşladı (p95 olağanın 3 katı). NOT"
	if x.Desc != want {
		t.Errorf("cümle:\n%q\n%q", x.Desc, want)
	}
	// en büyük oran: hata 3,4× > yavaşlama 3,0×; başlangıç en erken olay
	if x.Value != 0.034 || x.Threshold != 0.01 || x.Metric != "error_rate" || x.StartedAt != br.StartedAt || len(x.Events) != 2 {
		t.Errorf("girdi: %+v", x)
	}
}

func TestLegacyTwinNote(t *testing.T) {
	open := []chstore.Problem{
		{RuleID: "svc-slowdown:orders-api", Service: "orders-api", Status: "open"},
		{RuleID: "anomaly:orders-api:error_rate", Service: "orders-api", Status: "acknowledged"},
		{RuleID: "anomaly:orders-api:p99_ms", Service: "orders-api", Status: "open"},
		{RuleID: "svc-slowdown:payments-api", Service: "payments-api", Status: "resolved"},
	}
	if n := legacyTwinNote("orders-api", open); !strings.Contains(n, "anomaly:orders-api:error_rate, svc-slowdown:orders-api") || !strings.Contains(n, "çift kaydı") {
		t.Errorf("not: %q", n)
	}
	if n := legacyTwinNote("payments-api", open); n != "" {
		t.Errorf("kapanmış eski kural not üretmez: %q", n)
	}
}
