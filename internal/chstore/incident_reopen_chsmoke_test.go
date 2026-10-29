//go:build chsmoke

// v0.10.1580 — yeniden açılan sabit-kimlikli problem ÇÖZÜLMÜŞ incident'a
// yeniden bağlanıyordu.
//
// Kod denetimi bulgusu: incident_problems ReplacingMergeTree ORDER BY
// problem_id; AttachProblemToIncidentWith kural 1 mevcut bağı durum
// bakmadan döndürüyordu. Sabit kimlikli aileler (anomaly-auto:<fp>:<svc>,
// anomaly-cluster:<src> …) yeniden açıldığında problem resolved bir
// incident'a "bağlı" kalıyor, hiçbir açık incident'ta görünmüyordu
// (resolved incident'ı yeniden açan bir yol da yok).
//
//	CH_SMOKE_ADDR=localhost:19000 go test -tags=chsmoke ./internal/chstore/ -run TestAttachReopenedProblemSkipsResolvedIncident
package chstore

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAttachReopenedProblemSkipsResolvedIncident(t *testing.T) {
	st := smokeStore(t)
	ctx := context.Background()
	run := time.Now().UnixNano()

	cases := []struct {
		name string
		// incident ilk bağlanmadan sonra çözülsün mü
		resolveFirst bool
		// aynı serviste ayrıca taze açık bir incident var mı
		siblingOpen bool
		wantSameInc bool // yeniden bağlanma ilk incident'ı mı döndürür
		wantSibling bool // kardeş açık incident'a mı katılır
	}{
		{name: "açık incident'a bağ korunur", wantSameInc: true},
		{name: "çözülmüş incident'a bağ yeniden kurulur", resolveFirst: true},
		{name: "çözülmüş bağ, aynı serviste açık incident'a katılır", resolveFirst: true, siblingOpen: true, wantSibling: true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := fmt.Sprintf("reopen-svc-%d-%d", run, i)
			p := Problem{
				ID: fmt.Sprintf("anomaly-auto:fp%d:%s", i, svc), RuleID: "anomaly-auto:fp",
				RuleName: "error rate", Severity: "critical", Service: svc,
				Status: "open", StartedAt: time.Now().Add(-2 * time.Minute).UnixNano(),
			}
			first, err := st.AttachProblemToIncidentWith(ctx, p, nil)
			if err != nil || first == nil {
				t.Fatalf("ilk bağlama: %v", err)
			}
			if tc.resolveFirst {
				first.Status = "resolved"
				now := time.Now().UnixNano()
				first.ResolvedAt = &now
				if err := st.UpsertIncident(ctx, first); err != nil {
					t.Fatalf("resolve: %v", err)
				}
			}
			var sibling *Incident
			if tc.siblingOpen {
				sibling = &Incident{Title: "kardeş", Severity: "warning", Status: "open",
					Service: svc, StartedAt: time.Now().UnixNano()}
				if err := st.UpsertIncident(ctx, sibling); err != nil {
					t.Fatalf("kardeş: %v", err)
				}
			}
			// Problem yeniden açılır (aynı kimlik).
			p.StartedAt = time.Now().UnixNano()
			got, err := st.AttachProblemToIncidentWith(ctx, p, nil)
			if err != nil || got == nil {
				t.Fatalf("yeniden bağlama: %v", err)
			}
			if tc.wantSameInc {
				if got.ID != first.ID {
					t.Fatalf("açık incident'a bağ korunmadı: %s ≠ %s", got.ID, first.ID)
				}
				return
			}
			if got.ID == first.ID || got.Status == "resolved" {
				t.Fatalf("çözülmüş incident'a yeniden bağlandı: id=%s status=%s", got.ID, got.Status)
			}
			if tc.wantSibling && got.ID != sibling.ID {
				t.Fatalf("aynı servisteki açık incident'a katılmadı: %s ≠ %s", got.ID, sibling.ID)
			}
			// Bağ yeniden yazıldı: üçüncü çağrı yeni incident'ı döndürür.
			again, err := st.AttachProblemToIncidentWith(ctx, p, nil)
			if err != nil || again == nil || again.ID != got.ID {
				t.Fatalf("bağ yeniden yazılmadı: %+v %v", again, err)
			}
			// Eski incident yeniden AÇILMADI.
			old, err := st.GetIncident(ctx, first.ID)
			if err != nil || old == nil || old.Status != "resolved" {
				t.Fatalf("çözülmüş incident değişti: %+v %v", old, err)
			}
		})
	}
}
