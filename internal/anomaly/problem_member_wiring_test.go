package anomaly

// problem_member_wiring_test.go — v0.10.2156 (Davis tarzı tek Problem D2):
// metrik anomalisi yazarının (applyOutcome) üyelik yolu. Sözleşme: açık
// sahip varken açılış kararı üye olayı yazar (satır / bildirim yok);
// üyelik sonraki tiklerde açık satır gibi davranır — histerezis ("none")
// üyeliği tazeler, çözülme üyeden AYRILIR; gerçek satır bugünkü yoldan;
// üyenin yükselttiği ölçü sahibin tazelemesinde ezilmez.

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestApplyOutcomeMembership(t *testing.T) {
	ctx := context.Background()
	d := &Detector{}
	cfg := chstore.DefaultAnomalySensitivity()
	st := newFakeMemberStore()
	snap := chstore.NewOpenProblems([]chstore.Problem{memMLOwner()})
	rule := "anomaly:orders-api:p99_ms"
	open := anomalyOutcome{Action: "open", Severity: "warning", Direction: "spiked", Current: 900, Median: 300, Z: 5, Dwell: 3, LatestHasData: true}

	// tik 1: açılış kararı → açık sahibe üye
	d.applyOutcome(ctx, "orders-api", "p99_ms", open, cfg, LoadMembers(ctx, st, snap, MemberDetMetricAnomaly, memNow))
	if len(st.problems) != 0 || len(st.events) != 1 {
		t.Fatalf("üyelik: Problem yazılmamalı, tek üye olayı: %+v %d", st.problems, len(st.events))
	}
	// tik 2: histerezis bandı ("none") → üyelik açık satır gibi görünür ve tazelenir
	t2 := memNow.Add(2 * time.Minute)
	m := LoadMembers(ctx, st, snap, MemberDetMetricAnomaly, t2)
	row := m.OpenRow(rule, "orders-api")
	if row == nil || row.RuleID != rule || row.Value != 900 || row.Threshold != 300 || row.Comparator != ">" {
		t.Fatalf("sanal satır: %+v", row)
	}
	if !reachesApply("none", row != nil) {
		t.Fatal("üyelik varken \"none\" uygulama fazına girmeli (touch)")
	}
	d.applyOutcome(ctx, "orders-api", "p99_ms", anomalyOutcome{Action: "none"}, cfg, m)
	for _, e := range st.events {
		if e.LastSeen != t2.UnixNano() || e.Stage != chstore.MLStageLive {
			t.Errorf("histerezis üyeliği tazelemeli: %+v", e)
		}
	}
	// tik 3: açılış kararı sürüyor → tazeleme (ölçü güncel), yeni olay / Problem yok
	t3 := t2.Add(2 * time.Minute)
	again := open
	again.Current = 1200
	d.applyOutcome(ctx, "orders-api", "p99_ms", again, cfg, LoadMembers(ctx, st, snap, MemberDetMetricAnomaly, t3))
	if len(st.problems) != 0 || len(st.events) != 1 {
		t.Fatalf("tazeleme: %+v %d", st.problems, len(st.events))
	}
	for _, e := range st.events {
		if e.Observed != 1200 || e.LastSeen != t3.UnixNano() {
			t.Errorf("tazelenmiş üye: %+v", e)
		}
	}
	// tik 4: çözülme → üyeden ayrılma (Problem yazılmaz)
	t4 := t3.Add(2 * time.Minute)
	d.applyOutcome(ctx, "orders-api", "p99_ms", anomalyOutcome{Action: "resolve", LatestHasData: true}, cfg, LoadMembers(ctx, st, snap, MemberDetMetricAnomaly, t4))
	if len(st.problems) != 0 {
		t.Fatalf("üyenin çözülmesi Problem yazmamalı: %+v", st.problems)
	}
	for _, e := range st.events {
		if e.Stage != chstore.MLStageClosed {
			t.Errorf("ayrılan üye: %+v", e)
		}
	}
	if LoadMembers(ctx, st, snap, MemberDetMetricAnomaly, t4.Add(time.Minute)).OpenRow(rule, "orders-api") != nil {
		t.Error("ayrılan üyenin açık satırı kalmamalı")
	}
}

func TestApplyOutcomeWithoutOwnerOpensRow(t *testing.T) {
	ctx := context.Background()
	st := newFakeMemberStore()
	d := &Detector{}
	oc := anomalyOutcome{Action: "open", Severity: "critical", Direction: "spiked", Current: 9, Median: 2, Z: 6, Dwell: 3, LatestHasData: true}
	d.applyOutcome(ctx, "orders-api", "error_rate", oc, chstore.DefaultAnomalySensitivity(), LoadMembers(ctx, st, chstore.NewOpenProblems(nil), MemberDetMetricAnomaly, memNow))
	if len(st.problems) != 1 || len(st.events) != 0 {
		t.Fatalf("sahip yokken bugünkü gibi satır açılır: %+v", st)
	}
	p := st.problems[0]
	if p.RuleID != "anomaly:orders-api:error_rate" || p.Status != "open" || p.Value != 9 || p.Threshold != 2 || p.Comparator != ">" {
		t.Errorf("satır: %+v", p)
	}
}

// Sahip anomaly:<svc>:error_rate iken bir üye önceliği yükselttiyse satırın
// ölçüsü üyenindir; dedektörün tazelemesi yalnız açıklamayı günceller.
func TestApplyOutcomeKeepsMemberRaisedMeasure(t *testing.T) {
	ctx := context.Background()
	d := &Detector{}
	oc := anomalyOutcome{Action: "open", Severity: "warning", Direction: "spiked", Current: 4, Median: 2, Z: 5, Dwell: 3, LatestHasData: true}
	raised := memErrRateOwner()
	raised.Severity, raised.Metric, raised.Comparator, raised.Value, raised.Threshold = "critical", "p99_ms", ">=", 9000, 300
	mlRaised := memErrRateOwner()
	mlRaised.Severity, mlRaised.Comparator, mlRaised.Value, mlRaised.Threshold = "critical", ">=", 0.03, 0.01 // ML svc: metrik adı aynı, karşılaştırıcı ">="
	for name, owner := range map[string]chstore.Problem{"yaygın yavaşlama üyesi": raised, "ML servis üyesi": mlRaised} {
		st := newFakeMemberStore()
		d.applyOutcome(ctx, "orders-api", "error_rate", oc, chstore.DefaultAnomalySensitivity(),
			LoadMembers(ctx, st, chstore.NewOpenProblems([]chstore.Problem{owner}), MemberDetMetricAnomaly, memNow))
		if len(st.problems) != 1 {
			t.Fatalf("%s: tazeleme yazılmalı: %+v", name, st.problems)
		}
		got := st.problems[0]
		if got.Value != owner.Value || got.Threshold != owner.Threshold || got.Metric != owner.Metric || got.Comparator != ">=" || got.Severity != "critical" {
			t.Errorf("%s: üyenin ölçüsü ezilmemeli: %+v", name, got)
		}
		if got.Description == owner.Description {
			t.Errorf("%s: açıklama güncellenmeli", name)
		}
	}
	// kendi ölçüsü: bugünkü davranış (Value güncel)
	st := newFakeMemberStore()
	d.applyOutcome(ctx, "orders-api", "error_rate", oc, chstore.DefaultAnomalySensitivity(),
		LoadMembers(ctx, st, chstore.NewOpenProblems([]chstore.Problem{memErrRateOwner()}), MemberDetMetricAnomaly, memNow))
	if len(st.problems) != 1 || st.problems[0].Value != 4 || st.problems[0].Threshold != 2 || st.problems[0].Comparator != ">" {
		t.Fatalf("kendi ölçüsü her tazelemede güncellenir: %+v", st.problems)
	}
}

func TestOwnAnomalyMeasure(t *testing.T) {
	cases := []struct {
		metric, cmp string
		want        bool
	}{
		{"error_rate", ">", true}, {"error_rate", "<", true}, {"error_rate", "", true}, {"error_rate", " > ", true},
		{"error_rate", ">=", false}, {"p99_ms", ">=", false}, {"p99_ms", ">", false}, {"ml_group_signals", ">=", false},
	}
	for _, c := range cases {
		if got := ownAnomalyMeasure(chstore.Problem{Metric: c.metric, Comparator: c.cmp}, "error_rate"); got != c.want {
			t.Errorf("%s %q: %v", c.metric, c.cmp, got)
		}
	}
}
