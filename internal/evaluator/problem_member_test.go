package evaluator

// problem_member_test.go — v0.10.2156 (Davis tarzı tek Problem D2): yaygın
// yavaşlama yazarının (applySvcSlow) üyelik yolu. Sözleşme: açık sahip
// varken açılış üye olayı yazar (satır / bildirim / açılış sayacı yok);
// üyelik kuralın açık satırı gibi akışa girer (svcSlowMemo.step onu tazeler,
// temiz kovada kapatır) ve yazımlar üye olayına gider; sahip yokken bugünkü
// satır açılır. Servis adları SENTETİK.

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeMemberStore struct {
	problems []chstore.Problem
	events   map[string]chstore.MLEvent
	verdicts []chstore.ProblemVerdict
}

func (f *fakeMemberStore) UpsertProblem(_ context.Context, p chstore.Problem) error {
	f.problems = append(f.problems, p)
	return nil
}

func (f *fakeMemberStore) UpsertMLEvent(_ context.Context, e chstore.MLEvent) error {
	if f.events == nil {
		f.events = map[string]chstore.MLEvent{}
	}
	f.events[e.ID] = e
	return nil
}

func (f *fakeMemberStore) ListMLEvents(_ context.Context, flt chstore.MLEventsFilter) ([]chstore.MLEvent, error) {
	var out []chstore.MLEvent
	for _, e := range f.events {
		if (flt.Detector == "" || e.Detector == flt.Detector) && (flt.Service == "" || e.Service == flt.Service) &&
			(flt.Members || !chstore.IsMLMemberDetector(e.Detector)) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeMemberStore) ListProblemVerdicts(context.Context) ([]chstore.ProblemVerdict, error) {
	return f.verdicts, nil
}

func TestApplySvcSlowMembership(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	mlOwner := chstore.Problem{ID: "anomaly-auto:svc:orders-api", RuleID: "anomaly-auto:svc:orders-api", Severity: "critical", Service: "orders-api",
		Metric: "error_rate", Comparator: ">=", Value: 0.03, Threshold: 0.01, Status: "open", StartedAt: now.Add(-10 * time.Minute).UnixNano()}
	v := svcSlowVerdict{Service: "orders-api", ID: chstore.SvcSlowdownRuleID("orders-api"), Bucket: now.Add(-5 * time.Minute).Truncate(5 * time.Minute),
		Fire: true, OpFire: true, Ops: []svcSlowOp{{Operation: "GET /orders", CurP99Ms: 9000, BaseMs: 300, CurCalls: 100}}, Value: 9000, Threshold: 300}
	row := svcSlowProblem(v, time.UTC)

	t.Run("açık sahip var → üye; satır ve açılış sayacı yok", func(t *testing.T) {
		st := &fakeMemberStore{}
		e := &Evaluator{}
		snap := chstore.NewOpenProblems([]chstore.Problem{mlOwner})
		e.applySvcSlow(ctx, svcSlowActions{Open: []chstore.Problem{row}}, anomaly.LoadMembers(ctx, st, snap, anomaly.MemberDetSvcSlowdown, now))
		if len(st.problems) != 0 || len(st.events) != 1 || e.opened.Load() != 0 {
			t.Fatalf("üyelik: %+v %d açılış=%d", st.problems, len(st.events), e.opened.Load())
		}
		// sonraki tik: üyelik kuralın açık satırıdır (svcSlowOpenRows + Rows)
		t1 := now.Add(time.Minute)
		mem := anomaly.LoadMembers(ctx, st, snap, anomaly.MemberDetSvcSlowdown, t1)
		open := append(svcSlowOpenRows(snap.All()), mem.Rows()...)
		if len(open) != 1 || svcSlowServiceOf(open[0]) != "orders-api" || open[0].ID != row.ID {
			t.Fatalf("kuralın gördüğü açık satırlar: %+v", open)
		}
		// değişmeden tazele (aynı kova) → üye olayı tazelenir, Problem yazılmaz
		e.applySvcSlow(ctx, keepAll(open), mem)
		if len(st.problems) != 0 {
			t.Fatalf("üyeliğin tazelenmesi Problem yazmamalı: %+v", st.problems)
		}
		for _, ev := range st.events {
			if ev.LastSeen != t1.UnixNano() || ev.Stage != chstore.MLStageLive {
				t.Errorf("tazelenmiş üye: %+v", ev)
			}
		}
		// kural kapandı / temiz kovalar → kapanış = üyeden ayrılma; çözülme sayacı artmaz
		t2 := t1.Add(time.Minute)
		mem = anomaly.LoadMembers(ctx, st, snap, anomaly.MemberDetSvcSlowdown, t2)
		open = append(svcSlowOpenRows(snap.All()), mem.Rows()...)
		e.applySvcSlow(ctx, svcSlowActions{Resolve: svcSlowDisabledResolutions(open, t2.UnixNano())}, mem)
		if len(st.problems) != 0 || e.resolved.Load() != 0 {
			t.Fatalf("üyenin kapanışı Problem yazmamalı / sayılmamalı: %+v %d", st.problems, e.resolved.Load())
		}
		for _, ev := range st.events {
			if ev.Stage != chstore.MLStageClosed {
				t.Errorf("ayrılan üye: %+v", ev)
			}
		}
	})
	t.Run("sahip yok → bugünkü satır açılır ve sayılır", func(t *testing.T) {
		st := &fakeMemberStore{}
		e := &Evaluator{}
		e.applySvcSlow(ctx, svcSlowActions{Open: []chstore.Problem{row}}, anomaly.LoadMembers(ctx, st, chstore.NewOpenProblems(nil), anomaly.MemberDetSvcSlowdown, now))
		if len(st.problems) != 1 || st.problems[0].ID != row.ID || len(st.events) != 0 || e.opened.Load() != 1 {
			t.Fatalf("açılış: %+v %d", st.problems, e.opened.Load())
		}
	})
	t.Run("kendi gerçek satırı → tazeleme / kapanış bugünkü yoldan", func(t *testing.T) {
		st := &fakeMemberStore{}
		e := &Evaluator{}
		snap := chstore.NewOpenProblems([]chstore.Problem{row})
		mem := anomaly.LoadMembers(ctx, st, snap, anomaly.MemberDetSvcSlowdown, now)
		closed := row
		chstore.MarkResolved(&closed, now.UnixNano())
		e.applySvcSlow(ctx, svcSlowActions{Keep: []chstore.Problem{row}}, mem)
		e.applySvcSlow(ctx, svcSlowActions{Resolve: []chstore.Problem{closed}}, mem)
		if len(st.problems) != 2 || st.problems[1].Status != "resolved" || len(st.events) != 0 || e.resolved.Load() != 1 {
			t.Fatalf("gerçek satır: %+v %d", st.problems, e.resolved.Load())
		}
	})
}
