package ml

// svc_live_test.go — v0.10.2130 (spec docs/plans/spec-ml-service-level.md
// dilim 4, operatör kararı 2026-11-14 "Yenileri canlıya al"): servis düzeyi
// ML kararı gerçek Problem açar. Saf dikişler: svcPromote (Value/Threshold →
// computePriority ≥ 2× P1, altı P2), ownerDedupe (servis başına tek ML
// Problem'i; sahiplik ilk açanda, öncelik yalnız yükselir), kapanış, düz
// cümle. v0.10.2155: eski kural ikizi notu kalktı, yerine aileler arası
// sahiplik (TestOwnerDedupeAcrossFamilies). Servis adları SENTETİK.

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
	in := svcProblemInputs(kept, ds)
	if len(in) != 1 {
		t.Fatalf("girdi: %+v", in)
	}
	x := in[0]
	want := "orders-batch servisinde hata oranı %3,4, olağanı %1. orders-batch servisinde 4 operasyon birden yavaşladı (p95 olağanın 3 katı)."
	if x.Desc != want {
		t.Errorf("cümle:\n%q\n%q", x.Desc, want)
	}
	// en büyük oran: hata 3,4× > yavaşlama 3,0×; başlangıç en erken olay
	if x.Value != 0.034 || x.Threshold != 0.01 || x.Metric != "error_rate" || x.StartedAt != br.StartedAt || len(x.Events) != 2 {
		t.Errorf("girdi: %+v", x)
	}
}

// TestOwnerDedupeAcrossFamilies — v0.10.2155 (Davis tarzı tek Problem D1):
// sahiplik ML ailesinin dışına taşındı. Eski aile sahipse ML kararı üyedir
// (yeni Problem / bildirim yok, olaya sahibin kimliği damgalanır); sahibin
// satırı yalnız öncelik yükseliyorsa yazılır; ML ikizi "merged into" ile
// kapanır; alarm / yalnız-üye satır sahip sayılmaz; susturulmuş ML kuralı
// üye olmaz.
func TestOwnerDedupeAcrossFamilies(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	pol := chstore.OwnerPolicy{Keep: chstore.DefaultInboxKeepSourcePriority(), Priority: chstore.DefaultProblemPriority(), NowNs: now.UnixNano()}
	started := now.Add(-10 * time.Minute).UnixNano()
	slow := chstore.Problem{ID: "svc-slowdown:orders-api", RuleID: "svc-slowdown:orders-api", RuleName: "Yaygın yavaşlama", Severity: "critical",
		Service: "orders-api", Metric: "p99_ms", Comparator: ">=", Value: 9000, Threshold: 300, Status: "acknowledged", Assignee: "ops@acme.example",
		Description: "yavaşlama cümlesi", StartedAt: started}
	errRate := chstore.Problem{ID: "a1", RuleID: "anomaly:orders-api:error_rate", RuleName: "Anomaly · Error rate", Severity: "warning",
		Service: "orders-api", Metric: "error_rate", Comparator: ">", Value: 3, Threshold: 2, Status: "open", Description: "hata cümlesi", StartedAt: started}
	in := svcIn("orders-api", 0.03, 0.01, "e1")
	in.StartedAt = now.Add(-2 * time.Minute).UnixNano()
	grp := liveGroup("orders-api", "ev1", "log", "trace")
	grp.G.Start = in.StartedAt
	// Pin (v0.10.2156): ML satırları ">=" karşılaştırıcı taşır. Metrik anomalisi
	// dedektörü, sahibi olduğu satırın ölçüsünün bir üyeden geldiğini buna
	// bakarak anlar (anomaly.ownAnomalyMeasure: kendi ölçüsü ">" / "<");
	// "error_rate" adı iki ailede aynı olduğu için tek ayırt edici budur.
	if newSvcProblem(in).Comparator != ">=" || newGroupProblem(grp, "x").Comparator != ">=" {
		t.Fatal("ML Problem satırı \">=\" karşılaştırıcı taşımalı (anomaly.ownAnomalyMeasure buna dayanır)")
	}

	t.Run("svc-slowdown sahip + ML svc gelir → üye; P1 sahibi yeniden yazılmaz", func(t *testing.T) {
		p := planMLProblemsWith(nil, []svcProblemIn{in}, []chstore.Problem{slow}, true, descStub, pol)
		if len(p.Open)+len(p.Refresh)+len(p.Resolve) != 0 || p.LiveSvc["e1"] != slow.ID {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("svc-slowdown sahip + ML grubu gelir → üye; açmayacak zayıf grup üye olmaz", func(t *testing.T) {
		weak := liveGroup("orders-api", "ev2", "trace")
		p := planMLProblemsWith([]decidedGroup{grp, weak}, nil, []chstore.Problem{slow}, true, descStub, pol)
		if len(p.Open)+len(p.Refresh)+len(p.Resolve) != 0 || p.LiveGroup[grp.G.ID] != slow.ID {
			t.Fatalf("plan: %+v", p)
		}
		if _, ok := p.LiveGroup[weak.G.ID]; ok {
			t.Error("tek başına Problem açmayacak grup eski sahibe üye olmamalı")
		}
	})
	t.Run("uyarı error_rate sahip + ML svc (3×, critical) gelir → sahip P1'e yükselir, kimliği / cümlesi kalır", func(t *testing.T) {
		p := planMLProblemsWith(nil, []svcProblemIn{in}, []chstore.Problem{errRate}, true, descStub, pol)
		if len(p.Open) != 0 || len(p.Refresh) != 1 || len(p.Raised) != 1 || p.LiveSvc["e1"] != errRate.ID {
			t.Fatalf("plan: %+v", p)
		}
		r := p.Refresh[0]
		if r.ID != "a1" || r.RuleID != errRate.RuleID || r.Description != "hata cümlesi" || r.Severity != "critical" || r.Value != 0.03 || r.Threshold != 0.01 || r.Metric != "error_rate" || r.Comparator != ">=" {
			t.Errorf("yükseltilmiş sahip: %+v", r)
		}
		if !chstore.EscalatedToP1(errRate, r, pol) {
			t.Error("P1 yükselişi bekleniyor")
		}
		// ikinci tik: sahip zaten yükseltilmiş → yazım yok
		if p2 := planMLProblemsWith(nil, []svcProblemIn{in}, []chstore.Problem{r}, true, descStub, pol); len(p2.Refresh) != 0 || len(p2.Raised) != 0 {
			t.Errorf("yükseltme bir kez: %+v", p2)
		}
	})
	t.Run("ML ikizi (genç) + eski sahip → ikiz 'merged into' ile kapanır, atanan boşsa taşınır", func(t *testing.T) {
		twin := newSvcProblem(in)
		twin.Assignee = "payments"
		owner := slow
		owner.Assignee = ""
		p := planMLProblemsWith(nil, []svcProblemIn{in}, []chstore.Problem{owner, twin}, true, descStub, pol)
		if len(p.Open) != 0 || len(p.Resolve) != 1 || len(p.Merged) != 1 || p.LiveSvc["e1"] != slow.ID {
			t.Fatalf("plan: %+v", p)
		}
		if c := p.Resolve[0]; c.ID != twin.ID || !strings.HasSuffix(c.Description, chstore.MergedIntoNote(slow.ID)) || strings.Contains(c.Description, svcResolveReason) {
			t.Errorf("ikiz kapanışı: %+v", c)
		}
		if len(p.Refresh) != 1 || p.Refresh[0].ID != slow.ID || p.Refresh[0].Assignee != "payments" || p.Refresh[0].Status != "acknowledged" {
			t.Errorf("sahip atananı devralmalı: %+v", p.Refresh)
		}
		if q := planMLProblemsWith(nil, []svcProblemIn{in}, []chstore.Problem{owner, twin}, false, descStub, pol); len(q.Resolve) != 0 {
			t.Errorf("lider sürekliliği yokken ikiz kapanmaz: %+v", q)
		}
	})
	t.Run("ML sahip (eski) + genç svc-slowdown satırı → ML sahip kalır, kendi yolu", func(t *testing.T) {
		mlOwner := newSvcProblem(in)
		mlOwner.StartedAt = now.Add(-30 * time.Minute).UnixNano()
		p := planMLProblemsWith(nil, []svcProblemIn{in}, []chstore.Problem{mlOwner, slow}, true, descStub, pol)
		if len(p.Open) != 0 || len(p.Resolve) != 0 || len(p.Refresh) != 1 || p.Refresh[0].ID != mlOwner.ID || p.LiveSvc["e1"] != mlOwner.ID {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("alarm kuralı / yalnız-üye p99 satırı sahip değil → ML kendi Problem'ini açar", func(t *testing.T) {
		alarm := chstore.Problem{ID: "r1", RuleID: "rule-7f3a", Severity: "critical", Service: "orders-api", Status: "open", StartedAt: started}
		p99 := chstore.Problem{ID: "a2", RuleID: "anomaly:orders-api:p99_ms", Severity: "warning", Service: "orders-api", Status: "open", StartedAt: started}
		p := planMLProblemsWith(nil, []svcProblemIn{in}, []chstore.Problem{alarm, p99}, true, descStub, pol)
		if len(p.Open) != 1 || !chstore.IsMLSvcProblem(p.Open[0].RuleID) || len(p.Resolve) != 0 {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("ML kuralı 'problem değil' imzalı → üye olmaz, kendi satırını açar", func(t *testing.T) {
		q := pol
		q.Noise = map[string]struct{}{chstore.RuleProblemVerdictSignature(MLSvcRulePrefix+"orders-api", "orders-api"): {}}
		p := planMLProblemsWith(nil, []svcProblemIn{in}, []chstore.Problem{slow}, true, descStub, q)
		if len(p.Open) != 1 || p.LiveSvc["e1"] != p.Open[0].ID {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("eski sahip 'problem değil' imzalı → sahip sayılmaz", func(t *testing.T) {
		q := pol
		q.Noise = map[string]struct{}{chstore.RuleProblemVerdictSignature(slow.RuleID, slow.Service): {}}
		p := planMLProblemsWith(nil, []svcProblemIn{in}, []chstore.Problem{slow}, true, descStub, q)
		if len(p.Open) != 1 || len(p.Refresh) != 0 {
			t.Fatalf("plan: %+v", p)
		}
	})
}

// TestSplitHeldResolves — v0.10.2157 (Davis tarzı tek Problem D3): ML
// sinyali biten Problem, canlı üyesi (eski dedektörün üye olayı) kaldıkça
// kapanmaz; yabancı sahibe katılıp kapanan ikiz asla tutulmaz.
func TestSplitHeldResolves(t *testing.T) {
	a := newSvcProblem(svcIn("orders-api", 0.03, 0.01, "e0"))
	b := newSvcProblem(svcIn("payments-api", 0.03, 0.01, "e1"))
	twin := newSvcProblem(svcIn("search-api", 0.03, 0.01, "e2"))
	asked := map[string]bool{}
	live := func(p chstore.Problem) bool {
		asked[p.ID] = true
		return p.Service != "payments-api" // orders + search'ün canlı üyesi var
	}
	closeNow, held := splitHeldResolves([]chstore.Problem{a, b, twin}, map[string]bool{twin.ID: true}, live)
	if len(held) != 1 || held[0].ID != a.ID {
		t.Fatalf("canlı üyesi olan ML Problem'i tutulmalı: %+v", held)
	}
	if len(closeNow) != 2 || closeNow[0].ID != b.ID || closeNow[1].ID != twin.ID {
		t.Fatalf("üyesiz Problem ve birleşen ikiz kapanmalı (sıra korunur): %+v", closeNow)
	}
	if asked[twin.ID] {
		t.Error("birleşen ikiz için üye okuması yapılmamalı")
	}
	// yaşam döngüsü: plan kapanış adayı üretir, üye bitince aday kapanır
	p := planMLProblems(nil, nil, []chstore.Problem{a}, true, descStub)
	if c, h := splitHeldResolves(p.Resolve, nil, func(chstore.Problem) bool { return true }); len(c) != 0 || len(h) != 1 {
		t.Fatalf("üye canlıyken: kapat=%d tut=%d", len(c), len(h))
	}
	if c, h := splitHeldResolves(p.Resolve, nil, func(chstore.Problem) bool { return false }); len(c) != 1 || len(h) != 0 || !strings.HasSuffix(c[0].Description, svcResolveReason) {
		t.Fatalf("üye bitince bugünkü gerekçeyle kapanır: %+v", c)
	}
}
