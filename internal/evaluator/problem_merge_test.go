package evaluator

// problem_merge_test.go — v0.10.2157 (Davis tarzı tek Problem D3): ikiz göçü
// ve bayat süpürme muafiyetinin SAF çekirdekleri. Sözleşme:
//   - kapanan eski aile satırı ÜYE OLAYINA dönüşür (dedektörün açık satırıyla);
//     ML ikizi için üye olayı yazılmaz (üyeliği kendi olaylarıdır);
//   - yazım sırası sahip → üye olayı → gencin kapanışı; sahip yazılamazsa o
//     servisin ikizi KAPANMAZ (yüksek öncelik taşınmadan gizlenmez);
//   - ikinci geçiş (anlık görüntü birleşmiş) hiçbir şey yazmaz (idempotent);
//   - canlı üyesi olan sahip bayat süpürmeyle kapanmaz; okuma hatası = muaf.
// Servis adları SENTETİK.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func mergeFixture(now time.Time) (pol chstore.OwnerPolicy, owner, slow, p99, mlTwin chstore.Problem) {
	pol = chstore.OwnerPolicy{Keep: chstore.DefaultInboxKeepSourcePriority(), Priority: chstore.DefaultProblemPriority(), NowNs: now.UnixNano()}
	owner = chstore.Problem{ID: "a1", RuleID: "anomaly:orders-api:error_rate", RuleName: "Anomaly · Error rate", Severity: "warning", Service: "orders-api",
		Metric: "error_rate", Comparator: ">", Value: 3, Threshold: 2, Status: "open", Description: "hata cümlesi", StartedAt: now.Add(-20 * time.Minute).UnixNano()}
	slow = chstore.Problem{ID: "svc-slowdown:orders-api", RuleID: "svc-slowdown:orders-api", RuleName: "Yaygın yavaşlama", Severity: "critical", Service: "orders-api",
		Metric: "p99_ms", Comparator: ">=", Value: 9000, Threshold: 300, Status: "acknowledged", Assignee: "payments", Description: "yavaşlama cümlesi",
		StartedAt: now.Add(-10 * time.Minute).UnixNano()}
	p99 = chstore.Problem{ID: "x9", RuleID: "anomaly:orders-api:p99_ms", RuleName: "Anomaly · p99", Severity: "critical", Service: "orders-api",
		Metric: "p99_ms", Comparator: ">", Value: 900, Threshold: 300, Status: "open", StartedAt: now.Add(-30 * time.Minute).UnixNano()}
	mlTwin = chstore.Problem{ID: "anomaly-auto:svc:orders-api", RuleID: "anomaly-auto:svc:orders-api", RuleName: "ML · servis anomalisi", Severity: "critical", Service: "orders-api",
		Metric: "error_rate", Comparator: ">=", Value: 0.012, Threshold: 0.01, Status: "open", StartedAt: now.Add(-5 * time.Minute).UnixNano()}
	return
}

func TestPlanMergeWrites(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	pol, owner, slow, p99, mlTwin := mergeFixture(now)
	snap := chstore.NewOpenProblems([]chstore.Problem{owner, slow, p99, mlTwin})
	ws := planMergeWrites(chstore.PlanServiceMerges(snap.All(), pol), snap.ByID, now.UnixNano())
	if len(ws) != 1 {
		t.Fatalf("tek servis birleşmeli: %+v", ws)
	}
	w := ws[0]
	if w.OwnerID != "a1" || w.Owner == nil || w.Owner.Severity != "critical" || w.Owner.Value != 9000 || w.Owner.Assignee != "payments" ||
		w.Owner.RuleID != owner.RuleID || w.Owner.Description != "hata cümlesi" || w.Owner.StartedAt != owner.StartedAt {
		t.Fatalf("sahip: yaygın yavaşlamanın önceliği + atananı taşınır, kimliği / cümlesi / başlangıcı kalır: %+v", w.Owner)
	}
	if len(w.Closed) != 3 {
		t.Fatalf("üç satır kapanmalı (yavaşlama, p99, ML ikizi): %+v", w.Closed)
	}
	for _, c := range w.Closed {
		if c.Status != "resolved" || c.ResolvedAt == nil || c.Description == "" || c.ID == "a1" {
			t.Errorf("kapanan satır: %+v", c)
		}
	}
	// üye olayı yalnız eski aileler için; dedektörün AÇIK satırını taşır
	if len(w.Events) != 2 {
		t.Fatalf("iki üye olayı (yavaşlama + p99); ML ikizi için yok: %+v", w.Events)
	}
	byDet := map[string]chstore.MLEvent{}
	for _, e := range w.Events {
		byDet[e.Detector] = e
	}
	se, ok := byDet[chstore.MLDetMemberSvcSlowdown]
	ev, ok2 := chstore.MemberEvidenceOf(se)
	if !ok || !ok2 || ev.ProblemID != "a1" || ev.RuleID != slow.RuleID || se.StartedAt != slow.StartedAt || se.LastSeen != now.UnixNano() ||
		se.Stage != chstore.MLStageLive || ev.Row == nil || ev.Row.Problem().Status != "open" || ev.Row.Description != "yavaşlama cümlesi" {
		t.Errorf("yavaşlama üye olayı: %+v %+v", se, ev)
	}
	if pe, ok := byDet[chstore.MLDetMemberMetricAnomaly]; !ok || pe.StartedAt != p99.StartedAt || pe.Observed != 900 {
		t.Errorf("p99 üye olayı: %+v", pe)
	}
	t.Run("idempotent: birleşmiş görüntüde yazım yok", func(t *testing.T) {
		after := chstore.NewOpenProblems([]chstore.Problem{*w.Owner})
		if again := planMergeWrites(chstore.PlanServiceMerges(after.All(), pol), after.ByID, now.UnixNano()); len(again) != 0 {
			t.Fatalf("ikinci geçiş: %+v", again)
		}
	})
	t.Run("sahip değişmiyorsa yazılmaz", func(t *testing.T) {
		strong := slow
		strong.StartedAt, strong.Assignee = now.Add(-40*time.Minute).UnixNano(), "ops@acme.example"
		s2 := chstore.NewOpenProblems([]chstore.Problem{strong, mlTwin})
		ws := planMergeWrites(chstore.PlanServiceMerges(s2.All(), pol), s2.ByID, now.UnixNano())
		if len(ws) != 1 || ws[0].Owner != nil || len(ws[0].Closed) != 1 || len(ws[0].Events) != 0 {
			t.Fatalf("P1 sahip + ML ikizi: yalnız ikiz kapanır: %+v", ws)
		}
	})
}

func TestApplyMergeWrites(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	pol, owner, slow, _, _ := mergeFixture(now)
	snap := chstore.NewOpenProblems([]chstore.Problem{owner, slow})
	writes := planMergeWrites(chstore.PlanServiceMerges(snap.All(), pol), snap.ByID, now.UnixNano())
	e := &Evaluator{}
	t.Run("sıra: sahip → üye olayı → kapanış", func(t *testing.T) {
		st := &fakeMemberStore{}
		merged, services := e.applyMergeWrites(ctx, st, writes)
		if merged != 1 || services != 1 || len(st.problems) != 2 || len(st.events) != 1 {
			t.Fatalf("yazımlar: %d %d %+v %d", merged, services, st.problems, len(st.events))
		}
		if st.problems[0].ID != "a1" || st.problems[0].Status != "open" || st.problems[0].Severity != "critical" {
			t.Errorf("ilk yazım yükseltilmiş sahip olmalı: %+v", st.problems[0])
		}
		if st.problems[1].ID != slow.ID || st.problems[1].Status != "resolved" {
			t.Errorf("ikinci yazım kapanan ikiz olmalı: %+v", st.problems[1])
		}
	})
	t.Run("sahip yazılamadı → ikiz KAPANMAZ (öncelik taşınmadan gizlenmez)", func(t *testing.T) {
		st := &fakeMemberStore{failID: "a1"}
		merged, services := e.applyMergeWrites(ctx, st, writes)
		if merged != 0 || services != 0 || len(st.problems) != 0 || len(st.events) != 0 {
			t.Fatalf("sahip hatasında servis dokunulmaz: %d %d %+v %d", merged, services, st.problems, len(st.events))
		}
	})
}

func TestSplitLiveOwners(t *testing.T) {
	stale := []chstore.Problem{
		{ID: "o1", RuleID: "anomaly-auto:svc:orders-api", Service: "orders-api"},   // canlı üyesi var
		{ID: "o2", RuleID: "svc-slowdown:payments-api", Service: "payments-api"},   // üyesi yok
		{ID: "o3", RuleID: "anomaly:search-api:error_rate", Service: "search-api"}, // okuma hatası
		{ID: "m1", RuleID: "anomaly:orders-api:p99_ms", Service: "orders-api"},     // yalnız-üye aile: sorulmaz
		{ID: "r1", RuleID: "rule-7f3a", Service: "orders-api"},                     // alarm: sorulmaz
	}
	asked := map[string]bool{}
	sweep, exempt := splitLiveOwners(stale, func(p chstore.Problem) (bool, error) {
		asked[p.ID] = true
		switch p.ID {
		case "o1":
			return true, nil
		case "o3":
			return false, errors.New("ch down")
		}
		return false, nil
	})
	ids := func(ps []chstore.Problem) string {
		s := ""
		for _, p := range ps {
			s += p.ID + ","
		}
		return s
	}
	if ids(sweep) != "o2,m1,r1," || ids(exempt) != "o1,o3," {
		t.Fatalf("süpürülecek %q, muaf %q", ids(sweep), ids(exempt))
	}
	if asked["m1"] || asked["r1"] {
		t.Error("sahip olamayan aileler için üye okuması yapılmamalı")
	}
}

func TestApplySvcSlowHoldsOwnerWithLiveMembers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	owner := chstore.Problem{ID: "svc-slowdown:orders-api", RuleID: "svc-slowdown:orders-api", RuleName: svcSlowRuleName, Severity: "critical", Service: "orders-api",
		Metric: "p99_ms", Comparator: ">=", Value: 9000, Threshold: 300, Status: "acknowledged", Description: "cümle", StartedAt: now.Add(-20 * time.Minute).UnixNano()}
	snap := chstore.NewOpenProblems([]chstore.Problem{owner})
	closed := owner
	chstore.MarkResolved(&closed, now.UnixNano())
	mlEvent := func(stage string) chstore.MLEvent {
		return chstore.MLEvent{ID: "ml1", Detector: "svc_error_rate", Service: "orders-api", Stage: stage, WindowSec: 300,
			StartedAt: now.Add(-5 * time.Minute).UnixNano(), LastSeen: now.Add(-time.Minute).UnixNano(),
			Evidence: `{"svcShadow":{"rule":"anomaly-auto:svc:orders-api","live":true,"problemId":"svc-slowdown:orders-api"}}`}
	}
	t.Run("ML üyesi canlı → kapanmaz, satır aynen tazelenir", func(t *testing.T) {
		st := &fakeMemberStore{events: map[string]chstore.MLEvent{"ml1": mlEvent(chstore.MLStageShadow)}}
		e := &Evaluator{}
		e.applySvcSlow(ctx, svcSlowActions{Resolve: []chstore.Problem{closed}}, anomaly.LoadMembers(ctx, st, snap, anomaly.MemberDetSvcSlowdown, now))
		if len(st.problems) != 1 || st.problems[0].Status != "acknowledged" || st.problems[0].ResolvedAt != nil || e.resolved.Load() != 0 {
			t.Fatalf("canlı üyesi olan sahip açık kalmalı (ack korunur): %+v %d", st.problems, e.resolved.Load())
		}
	})
	t.Run("üye bitti → bugünkü gibi kapanır", func(t *testing.T) {
		st := &fakeMemberStore{events: map[string]chstore.MLEvent{"ml1": mlEvent(chstore.MLStageClosed)}}
		e := &Evaluator{}
		e.applySvcSlow(ctx, svcSlowActions{Resolve: []chstore.Problem{closed}}, anomaly.LoadMembers(ctx, st, snap, anomaly.MemberDetSvcSlowdown, now))
		if len(st.problems) != 1 || st.problems[0].Status != "resolved" || e.resolved.Load() != 1 {
			t.Fatalf("üyesiz sahip kapanmalı: %+v %d", st.problems, e.resolved.Load())
		}
	})
}
