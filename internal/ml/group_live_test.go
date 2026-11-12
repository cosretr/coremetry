package ml

// v0.10.2052 — anomali tekilleştirme D4 (operatör onayı 2026-11-12): ML
// grubu gerçek Problem açar. Plan SAF: açılış / tazeleme / kapanış, servis
// başına tek Problem, lider değişiminde kapanış izni yok. Servis adları
// SENTETİK.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func liveGroup(svc, first string, sigs ...string) decidedGroup {
	g := MLGroup{ID: mlGroupID(svc, first), Service: svc, Start: gBase.UnixNano(), Members: []string{first}}
	d := GroupDecision{Group: g.ID, RuleID: GroupRuleID(g), Signals: sigs}
	if len(sigs) >= groupMinSignals {
		d.WouldOpen, d.Severity, d.Priority = true, "critical", "P2"
	}
	return decidedGroup{G: g, D: d}
}

func descStub(g decidedGroup) string { return g.G.Service + " cümle" }

func TestPlanGroupProblems(t *testing.T) {
	two := liveGroup("orders-api", "ev1", "log", "trace")
	one := liveGroup("payments-api", "ev2", "trace")
	openP := newGroupProblem(two, "eski")
	openP.Status, openP.Assignee = "acknowledged", "ops@acme.example"
	drift := liveGroup("orders-api", "ev9", "log", "trace") // lider değişimi: aynı olay, başka ilk üye

	t.Run("iki sinyal, açık Problem yok → aç", func(t *testing.T) {
		p := planGroupProblems([]decidedGroup{two, one}, nil, true, descStub)
		if len(p.Open) != 1 || len(p.Refresh) != 0 || len(p.Resolve) != 0 {
			t.Fatalf("plan: %+v", p)
		}
		o := p.Open[0]
		if o.ID != "anomaly-auto:group:ev1:orders-api" || o.RuleID != "anomaly-auto:group:ev1" || o.Severity != "critical" || o.Status != "open" {
			t.Errorf("Problem: %+v", o)
		}
		fresh := o
		fresh.StartedAt = time.Now().UnixNano() // 4 sa staleCrit kuralı dışında
		if pr := chstore.EnrichProblemsWithPriority([]chstore.Problem{fresh})[0]; pr.Priority != "P2" {
			t.Errorf("öncelik %q (%s), istenen P2", pr.Priority, pr.PriorityReason)
		}
		if chstore.ProblemNotifyKind(o) != chstore.NotifyKindAnomaly {
			t.Errorf("bildirim türü %q", chstore.ProblemNotifyKind(o))
		}
		if p.LiveGroup[two.G.ID] != o.ID || p.LiveGroup[one.G.ID] != "" {
			t.Errorf("canlı işaret: %+v", p.LiveGroup)
		}
	})
	t.Run("tekrar eden tik → tazele, yeni açılış yok, operatör alanı taşınır", func(t *testing.T) {
		p := planGroupProblems([]decidedGroup{two}, []chstore.Problem{openP}, true, descStub)
		if len(p.Open) != 0 || len(p.Refresh) != 1 || len(p.Resolve) != 0 {
			t.Fatalf("plan: %+v", p)
		}
		r := p.Refresh[0]
		if r.ID != openP.ID || r.Status != "acknowledged" || r.Assignee != "ops@acme.example" || r.Description != "orders-api cümle" {
			t.Errorf("tazeleme: %+v", r)
		}
	})
	t.Run("grup kimliği kaydı (lider değişimi) → aynı servisin açık Problem'i tazelenir", func(t *testing.T) {
		p := planGroupProblems([]decidedGroup{drift}, []chstore.Problem{openP}, true, descStub)
		if len(p.Open) != 0 || len(p.Refresh) != 1 || p.Refresh[0].ID != openP.ID || p.LiveGroup[drift.G.ID] != openP.ID {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("aynı tikte aynı servise iki açan grup → tek Problem", func(t *testing.T) {
		p := planGroupProblems([]decidedGroup{two, drift}, nil, true, descStub)
		if len(p.Open) != 1 || len(p.Refresh) != 0 {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("grup bitti (karar yok) → kapanır", func(t *testing.T) {
		p := planGroupProblems(nil, []chstore.Problem{openP}, true, descStub)
		if len(p.Resolve) != 1 || !strings.Contains(p.Resolve[0].Description, "anomali grubu bitti") {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("bitmiş grup kararı → yeni Problem yok, açık olan kapanır", func(t *testing.T) {
		fin := two
		fin.D.Finished, fin.D.WouldOpen = true, false
		if p := planGroupProblems([]decidedGroup{fin}, nil, true, descStub); len(p.Open) != 0 {
			t.Fatalf("bitmiş grup açtı: %+v", p)
		}
		if p := planGroupProblems([]decidedGroup{fin}, []chstore.Problem{openP}, true, descStub); len(p.Resolve) != 1 {
			t.Fatalf("bitmiş grubun Problem'i kapanmalı: %+v", p)
		}
	})
	t.Run("yeni lider (kapanış izni yok) → kapatmaz", func(t *testing.T) {
		if p := planGroupProblems(nil, []chstore.Problem{openP}, false, descStub); len(p.Resolve) != 0 {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("grup sürerken tek sinyale düşse de Problem kalır (açıklama eski)", func(t *testing.T) {
		weak := liveGroup("orders-api", "ev1", "trace")
		p := planGroupProblems([]decidedGroup{weak}, []chstore.Problem{openP}, true, descStub)
		if len(p.Refresh) != 1 || p.Refresh[0].Description != "eski" || len(p.Resolve) != 0 {
			t.Fatalf("plan: %+v", p)
		}
	})
	t.Run("önek dışı açık Problem'e dokunulmaz", func(t *testing.T) {
		other := chstore.Problem{ID: "anomaly-auto:abcdef0123456789:orders-api", RuleID: "anomaly-auto:abcdef0123456789", Service: "orders-api", Status: "open"}
		if p := planGroupProblems(nil, []chstore.Problem{other}, true, descStub); len(p.Resolve)+len(p.Refresh)+len(p.Open) != 0 {
			t.Fatalf("plan: %+v", p)
		}
	})
}

// Cümle: önce servis + kaç sinyal, sonra sinyal başına en güçlü üyenin özeti
// (servis öneki atılır); batch kuralının saymadığı sinyal cümleye girmez.
func TestGroupProblemDescription(t *testing.T) {
	mem := []chstore.MLEvent{
		gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 150),
		gateEv("b", detDependencyLatency, "orders-api", 2, 30, 100, 400),
		gateEv("l", detErrorRate, "orders-api", 5, 20, 10, 30),
		gateEv("m", "metric_pool", "orders-api", 5, 20, 1, 9),
	}
	g := decidedGroup{G: MLGroup{Service: "orders-api"}, D: GroupDecision{Signals: []string{"log", "trace"}}, Members: mem}
	got := groupProblemDescription(g, func(e chstore.MLEvent) string { return e.Service + ": özet " + e.ID + "." })
	want := "orders-api: aynı anda 2 farklı sinyalde anomali var (log ve trace). Log: özet l. Trace: özet b."
	if got != want {
		t.Errorf("cümle:\n got %q\nwant %q", got, want)
	}
	if joinTR([]string{"a", "b", "c"}) != "a, b ve c" {
		t.Error("joinTR")
	}
}

// Yazım hatası olan Problem'in grubu "Problem açtı" işaretini almaz.
func TestMarkLiveGroups(t *testing.T) {
	gs := []decidedGroup{liveGroup("orders-api", "ev1", "log", "trace"), liveGroup("payments-api", "ev2", "log", "trace")}
	markLiveGroups(gs, map[string]string{gs[0].G.ID: "p1", gs[1].G.ID: "p2"}, map[string]bool{"p2": true})
	if !gs[0].D.Live || gs[0].D.ProblemID != "p1" || gs[1].D.Live {
		t.Fatalf("işaret: %+v / %+v", gs[0].D, gs[1].D)
	}
}

// Kural öneki chstore'un bildirim/süpürme sınıflandırıcılarıyla aynı kaynaktan.
func TestMLGroupRulePrefixPin(t *testing.T) {
	if MLGroupRulePrefix != "anomaly-auto:group:" || !chstore.IsMLGroupProblem(MLGroupRulePrefix+"x") {
		t.Fatal("önek kaydı")
	}
	if chstore.ProblemNotifyKind(chstore.Problem{RuleID: MLGroupRulePrefix + "x"}) != chstore.NotifyKindAnomaly {
		t.Fatal("grup Problem'i mevcut 'anomaly' bildirim türünde olmalı")
	}
	if _, ok := chstore.PromotedAnomalyEventID(MLGroupRulePrefix + "abcdef0123456789"); ok {
		t.Fatal("grup Problem'i anomaly_events terfi Problem'i sayılmamalı")
	}
	h, ok := chstore.MLPromotedHeadline(chstore.Problem{RuleID: MLGroupRulePrefix + "x", RuleName: groupProblemRuleName, Description: "orders-api: cümle"})
	if !ok || h != "orders-api: cümle" {
		t.Fatalf("manşet açıklamadan gelmeli: %q %v", h, ok)
	}
}
