package chstore

// problem_member_test.go — v0.10.2156 (Davis tarzı tek Problem D2): üye
// olayının SAF sözleşmesi. Üyelik ml_events satırıdır (şema değişmez):
// kimlik deterministik (yeniden katılma ikinci satır üretmez), kanıt sahibin
// kimliğini + dedektörün açacağı satırı taşır, ML'in canlı damgası aynı
// okuyucudan okunur, üye dedektörleri anomali yüzeylerinden gizlidir.
// Servis adları SENTETİK.

import (
	"strings"
	"testing"
	"time"
)

func TestMemberEventRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	owner := Problem{ID: "anomaly-auto:svc:orders-api", RuleID: "anomaly-auto:svc:orders-api", Service: "orders-api", Status: "open", StartedAt: now.Add(-10 * time.Minute).UnixNano()}
	row := Problem{ID: "svc-slowdown:orders-api", RuleID: "svc-slowdown:orders-api", RuleName: "Yaygın yavaşlama", Severity: "critical", Service: "orders-api",
		Metric: "p99_ms", Comparator: ">=", Value: 9000, Threshold: 300, Status: "open", Description: "cümle", StartedAt: now.Add(-5 * time.Minute).UnixNano(),
		Priority: "P1", OwnerTeam: "payments", Clusters: []string{"cluster-a"}}
	e := NewMemberEvent(MLDetMemberSvcSlowdown, row, owner, row.StartedAt, now.UnixNano(), false)
	if e.Detector != "svc_slowdown" || e.Stage != MLStageLive || e.Signal != MLSignalTrace || e.Service != "orders-api" ||
		e.StartedAt != row.StartedAt || e.LastSeen != now.UnixNano() || e.Observed != 9000 || e.Baseline != 300 || e.Score != 30 || e.Direction != "up" || e.WindowSec != MemberEventWindowSec {
		t.Fatalf("olay: %+v", e)
	}
	ev, ok := MemberEvidenceOf(e)
	if !ok || ev.ProblemID != owner.ID || ev.RuleID != row.RuleID || ev.Value != 9000 || ev.Threshold != 300 || ev.Row == nil {
		t.Fatalf("kanıt: %v %+v", ok, ev)
	}
	got := ev.Row.Problem()
	if got.ID != row.ID || got.RuleName != row.RuleName || got.Severity != "critical" || got.Metric != "p99_ms" || got.Comparator != ">=" ||
		got.Description != "cümle" || got.StartedAt != row.StartedAt || got.Status != "open" {
		t.Errorf("sanal satır saklanan alanları taşımalı: %+v", got)
	}
	if got.Priority != "" || got.OwnerTeam != "" || got.Clusters != nil {
		t.Errorf("okuma-anı zenginleştirmeleri kanıta girmemeli: %+v", got)
	}
	if MLEventProblemID(e) != owner.ID {
		t.Errorf("Problem bağı: %q", MLEventProblemID(e))
	}
	if !MLEventOpen(e, now.UnixNano()) || MLEventOpen(e, now.Add(21*time.Minute).UnixNano()) {
		t.Error("üye 20 dk tazelenmezse kendiliğinden biter (emniyet kapanışı)")
	}
	closed := NewMemberEvent(MLDetMemberSvcSlowdown, row, owner, row.StartedAt, now.UnixNano(), true)
	if closed.ID != e.ID || closed.Stage != MLStageClosed || MLEventOpen(closed, now.UnixNano()) {
		t.Errorf("ayrılma aynı satıra stage=closed yazar: %+v", closed)
	}
	t.Run("kimlik deterministik; sahibin yeni bölümü ayrı satır", func(t *testing.T) {
		again := NewMemberEvent(MLDetMemberSvcSlowdown, row, owner, now.UnixNano(), now.Add(time.Minute).UnixNano(), false)
		if again.ID != e.ID {
			t.Error("aynı üyelik aynı kimliğe yazmalı")
		}
		next := owner
		next.StartedAt = now.Add(time.Hour).UnixNano()
		other := owner
		other.ID = "anomaly-auto:group:ev1:orders-api"
		for _, o := range []Problem{next, other} {
			if NewMemberEvent(MLDetMemberSvcSlowdown, row, o, 0, now.UnixNano(), false).ID == e.ID {
				t.Errorf("farklı sahip / bölüm aynı kimliği almamalı: %+v", o)
			}
		}
		if NewMemberEvent(MLDetMemberMetricAnomaly, row, owner, 0, now.UnixNano(), false).ID == e.ID {
			t.Error("farklı dedektör aynı kimliği almamalı")
		}
	})
	t.Run("düşüş yönü comparator'dan; başlangıç yoksa şimdi", func(t *testing.T) {
		drop := row
		drop.RuleID, drop.Comparator, drop.Value, drop.Threshold = "anomaly:orders-api:request_rate", "<", 2, 40
		d := NewMemberEvent(MLDetMemberMetricAnomaly, drop, owner, 0, now.UnixNano(), false)
		if d.Direction != "down" || d.StartedAt != now.UnixNano() || d.Signal != MLSignalTrace {
			t.Errorf("olay: %+v", d)
		}
	})
}

func TestMLEventProblemID(t *testing.T) {
	cases := []struct {
		name, evidence, want string
	}{
		{"üye olayı", `{"problem_id":"P1","rule_id":"svc-slowdown:orders-api"}`, "P1"},
		{"ML servis düzeyi canlı damga", `{"svcShadow":{"wouldOpen":true,"rule":"anomaly-auto:svc:orders-api","live":true,"problemId":"P2"}}`, "P2"},
		{"ML grup canlı damga", `{"groupShadow":{"group":"g1","live":true,"problemId":"P3"}}`, "P3"},
		{"gölge karar (canlı değil) bağ sayılmaz", `{"groupShadow":{"group":"g1","wouldOpen":true,"problemId":"P3"}}`, ""},
		{"damgasız", `{"slowOps":[]}`, ""},
		{"boş", ``, ""},
		{"bozuk", `{`, ""},
	}
	for _, c := range cases {
		if got := MLEventProblemID(MLEvent{Evidence: c.evidence}); got != c.want {
			t.Errorf("%s: %q, istenen %q", c.name, got, c.want)
		}
	}
}

func TestLiveMemberEvents(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC).UnixNano()
	ev := func(id, det, svc, stage, evidence string, agoMin int) MLEvent {
		return MLEvent{ID: id, Detector: det, Service: svc, Stage: stage, WindowSec: 300, StartedAt: now - int64(30*time.Minute),
			LastSeen: now - int64(agoMin)*int64(time.Minute), Evidence: evidence}
	}
	member := `{"problem_id":"OWN","rule_id":"svc-slowdown:orders-api"}`
	mlStamp := `{"svcShadow":{"rule":"anomaly-auto:svc:orders-api","live":true,"problemId":"OWN"}}`
	evs := []MLEvent{
		ev("a", MLDetMemberSvcSlowdown, "orders-api", MLStageLive, member, 1),
		ev("b", MLDetMemberMetricAnomaly, "orders-api", MLStageClosed, member, 1),                             // ayrıldı
		ev("c", MLDetMemberPromotion, "orders-api", MLStageLive, member, 25),                                  // bayat
		ev("d", MLDetMemberSvcSlowdown, "orders-api", MLStageLive, `{"problem_id":"OTHER","rule_id":"x"}`, 1), // başka Problem
		ev("e", MLDetMemberSvcSlowdown, "payments-api", MLStageLive, member, 1),                               // başka servis
		ev("f", "svc_error_rate", "orders-api", MLStageShadow, mlStamp, 1),                                    // ML olayı
	}
	ids := func(xs []MLEvent) string {
		var s []string
		for _, x := range xs {
			s = append(s, x.ID)
		}
		return strings.Join(s, ",")
	}
	legacyOwner := Problem{ID: "OWN", RuleID: "svc-slowdown:orders-api", Service: "orders-api"}
	if got := ids(LiveMemberEvents(evs, legacyOwner, now)); got != "a,f" {
		t.Errorf("eski sahip: canlı üye olayı + ML'in canlı damgalı olayı sayılır: %q", got)
	}
	mlOwner := Problem{ID: "OWN", RuleID: "anomaly-auto:svc:orders-api", Service: "orders-api"}
	if got := ids(LiveMemberEvents(evs, mlOwner, now)); got != "a" {
		t.Errorf("ML sahip: ML'in kendi olayları üye sayılmaz: %q", got)
	}
}

func TestProblemEventsOf(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC).UnixNano()
	min := int64(time.Minute)
	resolved := t0 + 60*min
	p := Problem{ID: "svc-slowdown:orders-api", Service: "orders-api", StartedAt: t0, ResolvedAt: &resolved}
	mk := func(id string, startMin, lastMin int64) MLEvent {
		return MLEvent{ID: id, Detector: MLDetMemberMetricAnomaly, Service: "orders-api", StartedAt: t0 + startMin*min, LastSeen: t0 + lastMin*min,
			Evidence: `{"problem_id":"svc-slowdown:orders-api","rule_id":"anomaly:orders-api:p99_ms"}`}
	}
	evs := []MLEvent{
		mk("late", 20, 30), mk("early", -10, 5), mk("b-tie", 7, 9), mk("a-tie", 7, 9),
		mk("old-episode", -300, -200), // aynı kimlikli önceki bölümün üyesi
		mk("next-episode", 90, 95),    // kapanıştan sonra başlamış
		{ID: "other", Service: "orders-api", StartedAt: t0, LastSeen: t0, Evidence: `{"problem_id":"X"}`},
	}
	var got []string
	for _, e := range ProblemEventsOf(evs, p, 30*time.Minute) {
		got = append(got, e.ID)
	}
	if strings.Join(got, ",") != "early,a-tie,b-tie,late" {
		t.Errorf("zaman sıralı, bölüm dışı olaylar elenmiş olmalı: %v", got)
	}
}

func TestMLEventsWhereHidesMemberDetectors(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	where, _ := mlEventsWhere(MLEventsFilter{Since: now, Service: "orders-api"})
	if !strings.Contains(where, "detector NOT IN ('svc_slowdown','metric_anomaly','anomaly_promotion')") {
		t.Errorf("üye dedektörleri varsayılan okumada gizli olmalı: %s", where)
	}
	where, args := mlEventsWhere(MLEventsFilter{Since: now, Detector: MLDetMemberSvcSlowdown, Members: true})
	if strings.Contains(where, "NOT IN") || !strings.Contains(where, "detector = ?") || len(args) != 2 {
		t.Errorf("Members süzgeci üye olaylarını okur: %s %v", where, args)
	}
	for _, q := range []string{mlGroupRowsSQL, mlScorecardEventsSQL} {
		if !strings.Contains(q, "detector NOT IN ("+mlMemberDetectorsSQL()+")") {
			t.Errorf("gruplama / karne okuması üye dedektörlerini dışlamalı:\n%s", q)
		}
	}
	for _, d := range MLMemberDetectors {
		for _, n := range MLDetectorNames {
			if n == d {
				t.Errorf("%s ML ayar / karne listesinde olmamalı", d)
			}
		}
	}
}

func TestMemberDetectorForRule(t *testing.T) {
	cases := map[string]string{
		"svc-slowdown:orders-api":                   MLDetMemberSvcSlowdown,
		"anomaly:orders-api:error_rate":             MLDetMemberMetricAnomaly,
		"anomaly:orders-api:p99_ms":                 MLDetMemberMetricAnomaly,
		"anomaly-auto:1f2e3d":                       MLDetMemberPromotion,
		"anomaly-auto:subject:orders-api:p99_ms:up": MLDetMemberPromotion,
		"anomaly-auto:svc:orders-api":               "", // ML'in üyeliği kendi olaylarıdır
		"anomaly-auto:group:ev1":                    "",
		"rule-7f3a":                                 "",
	}
	for rule, want := range cases {
		if got := MemberDetectorForRule(rule); got != want {
			t.Errorf("%s: %q, istenen %q", rule, got, want)
		}
	}
}

// v0.10.2243 (anomali kartı Dilim C) — liste süzgeci pini: servis listesi
// platform satırlarını (telemetry_volume sınıfı + eski svc_volume_spike)
// SQL'de dışlar, platform yalnız onları okur; varsayılan (View "") süzmez —
// diğer okuyucular (Problem, bildirim, karne) değişmez.
func TestMLEventsWhereViews(t *testing.T) {
	now := time.Now()
	plat := "(class = 'telemetry_volume' OR detector IN ('svc_volume_spike', 'svc_telemetry_volume'))"
	cases := []struct {
		f    MLEventsFilter
		want string
		not  string
	}{
		{MLEventsFilter{Since: now, View: MLViewService, recCols: true}, "NOT " + plat, ""},
		{MLEventsFilter{Since: now, View: MLViewPlatform, recCols: true}, plat, "NOT ("},
		{MLEventsFilter{Since: now, View: MLViewPlatform}, "detector IN ('svc_volume_spike', 'svc_telemetry_volume')", "class"},
		{MLEventsFilter{Since: now, View: MLViewHeld, recCols: true}, `position(record, '"passed":false') > 0`, ""},
		{MLEventsFilter{Since: now, recCols: true}, "", "class"},
	}
	for i, c := range cases {
		where, _ := mlEventsWhere(c.f)
		if c.want != "" && !strings.Contains(where, c.want) {
			t.Errorf("%d: %q içinde %q yok", i, where, c.want)
		}
		if c.not != "" && strings.Contains(where, c.not) {
			t.Errorf("%d: %q içinde %q olmamalı", i, where, c.not)
		}
	}
}
