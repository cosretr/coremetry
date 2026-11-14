package anomaly

// problem_member_hold_test.go — v0.10.2157 (Davis tarzı tek Problem D3):
// kapanış = üyeden ayrılma. Sahibin KENDİ sinyali bittiğinde satır, canlı
// üyesi kaldıkça açık kalır; son üye de bitince sahibin dedektörü bugünkü
// kuralıyla kapatır. Okuma hatası = bilinmiyor → kapatma (kör kapanış yok).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func holdMLEvent(owner, stage string, lastSeen time.Time) chstore.MLEvent {
	return chstore.MLEvent{ID: "ml1", Detector: "svc_error_rate", Service: "orders-api", Stage: stage, WindowSec: 300,
		StartedAt: lastSeen.Add(-5 * time.Minute).UnixNano(), LastSeen: lastSeen.UnixNano(),
		Evidence: `{"svcShadow":{"rule":"anomaly-auto:svc:orders-api","live":true,"problemId":"` + owner + `"}}`}
}

func TestHasLiveMembers(t *testing.T) {
	ctx := context.Background()
	owner := memErrRateOwner()
	t.Run("ML'in canlı damgalı olayı → üye var", func(t *testing.T) {
		st := newFakeMemberStore()
		st.events["ml1"] = holdMLEvent(owner.ID, chstore.MLStageShadow, memNow.Add(-time.Minute))
		if live, err := HasLiveMembers(ctx, st, owner, memNow); err != nil || !live {
			t.Fatalf("canlı üye: %v %v", live, err)
		}
	})
	t.Run("eski dedektörün üye olayı → üye var; ayrılınca yok", func(t *testing.T) {
		st := newFakeMemberStore()
		snap := chstore.NewOpenProblems([]chstore.Problem{owner})
		m := LoadMembers(ctx, st, snap, MemberDetSvcSlowdown, memNow)
		if _, err := m.Open(ctx, memSlowRow()); err != nil {
			t.Fatal(err)
		}
		if live, _ := HasLiveMembers(ctx, st, owner, memNow); !live {
			t.Fatal("üye olayı canlı üye sayılmalı")
		}
		row := *m.Row("svc-slowdown:orders-api", "orders-api")
		chstore.MarkResolved(&row, memNow.UnixNano())
		if err := m.Write(ctx, row); err != nil {
			t.Fatal(err)
		}
		if live, _ := HasLiveMembers(ctx, st, owner, memNow); live {
			t.Fatal("ayrılan üye canlı sayılmamalı")
		}
	})
	t.Run("kapanmış / bayat / başka Problem'in olayı → üye yok", func(t *testing.T) {
		for name, e := range map[string]chstore.MLEvent{
			"kapanmış": holdMLEvent(owner.ID, chstore.MLStageClosed, memNow.Add(-time.Minute)),
			"bayat":    holdMLEvent(owner.ID, chstore.MLStageShadow, memNow.Add(-25*time.Minute)),
			"başka":    holdMLEvent("OTHER", chstore.MLStageShadow, memNow.Add(-time.Minute)),
		} {
			st := newFakeMemberStore()
			st.events["ml1"] = e
			if live, err := HasLiveMembers(ctx, st, owner, memNow); err != nil || live {
				t.Errorf("%s: %v %v", name, live, err)
			}
		}
	})
	t.Run("okuma hatası hata döner (çağıran kapatmaz)", func(t *testing.T) {
		st := newFakeMemberStore()
		st.evErr = errors.New("ch down")
		if _, err := HasLiveMembers(ctx, st, owner, memNow); err == nil {
			t.Fatal("hata bekleniyor")
		}
	})
}

func TestMembersHoldResolve(t *testing.T) {
	ctx := context.Background()
	owner := memErrRateOwner()
	snap := chstore.NewOpenProblems([]chstore.Problem{owner})
	live := newFakeMemberStore()
	live.events["ml1"] = holdMLEvent(owner.ID, chstore.MLStageShadow, memNow.Add(-time.Minute))
	if !LoadMembers(ctx, live, snap, MemberDetMetricAnomaly, memNow).HoldResolve(ctx, owner) {
		t.Fatal("canlı üyesi olan sahip kapanmamalı")
	}
	if LoadMembers(ctx, newFakeMemberStore(), snap, MemberDetMetricAnomaly, memNow).HoldResolve(ctx, owner) {
		t.Fatal("üyesiz sahip bugünkü gibi kapanır")
	}
	// yalnız-üye aile sahip olamaz → okuma yapılmadan "tutma yok"
	p99 := owner
	p99.ID, p99.RuleID = "x9", "anomaly:orders-api:p99_ms"
	down := newFakeMemberStore()
	down.evErr = errors.New("ch down")
	m := LoadMembers(ctx, down, chstore.NewOpenProblems([]chstore.Problem{p99}), MemberDetMetricAnomaly, memNow)
	if m.HoldResolve(ctx, p99) {
		t.Fatal("yalnız-üye ailenin satırı tutulmaz")
	}
	// okuma hatası: sahip olabilen satır kapatılmaz (bilinmiyor ≠ temiz)
	if !LoadMembers(ctx, down, snap, MemberDetMetricAnomaly, memNow).HoldResolve(ctx, owner) {
		t.Fatal("okuma hatasında sahip kapatılmamalı")
	}
}

// Sahip anomaly:<svc>:error_rate; kendi sinyali düzeldi ("resolve") ama ML
// üyesi sürüyor → satır kapanmaz, aynen tazelenir. Üye bitince kapanır.
func TestApplyOutcomeHoldsOwnerWithLiveMembers(t *testing.T) {
	ctx := context.Background()
	d := &Detector{}
	owner := memErrRateOwner()
	owner.Status = "acknowledged"
	snap := chstore.NewOpenProblems([]chstore.Problem{owner})
	resolve := anomalyOutcome{Action: "resolve", LatestHasData: true}
	st := newFakeMemberStore()
	st.events["ml1"] = holdMLEvent(owner.ID, chstore.MLStageShadow, memNow.Add(-time.Minute))
	d.applyOutcome(ctx, "orders-api", "error_rate", resolve, chstore.DefaultAnomalySensitivity(), LoadMembers(ctx, st, snap, MemberDetMetricAnomaly, memNow))
	if len(st.problems) != 1 || st.problems[0].Status != "acknowledged" || st.problems[0].ResolvedAt != nil || st.problems[0].Description != owner.Description {
		t.Fatalf("canlı üyesi olan sahip açık kalmalı (durum / açıklama aynen): %+v", st.problems)
	}
	st = newFakeMemberStore()
	st.events["ml1"] = holdMLEvent(owner.ID, chstore.MLStageClosed, memNow.Add(-time.Minute))
	d.applyOutcome(ctx, "orders-api", "error_rate", resolve, chstore.DefaultAnomalySensitivity(), LoadMembers(ctx, st, snap, MemberDetMetricAnomaly, memNow))
	if len(st.problems) != 1 || st.problems[0].Status != "resolved" || st.problems[0].ResolvedAt == nil {
		t.Fatalf("üyesi kalmayan sahip kapanmalı: %+v", st.problems)
	}
}
