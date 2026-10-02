package api

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// inbox_anomaly_episode_test.go — v0.10.1045.
//
// Operatör: "Eski yüksek oran taşınmasın: kapanıp yeniden tetiklenen
// anomali, eski en yüksek oranıyla (ör. '66×', P1) görünüyor. Yeni
// tetiklenme sıfırdan başlasın."
//
// /inbox anomali önceliği PeakRatio'dan gelir (anomalyPriority: ≥5 → P1,
// ≥2 → P2). anomaly_events tepeyi aynı parmak izi için 30 gün taşıdığından
// iki gün önceki 66× yük sıçraması, bugün 3.2× ile yeniden tetiklenen
// olayı P1 "66.0x baseline" gösteriyordu. Test GERÇEK birleşimden
// (chstore.MergeAnomalyCarry) geçer: elle kurulmuş bir satır, birleşim
// koşulsuz taşımaya dönse de yeşil kalırdı.
//
// Mutasyon kontrolü: MergeAnomalyCarry koşulsuz taşımaya döndürülürse
// "yeniden tetiklenme" satırı P1 / "66.0x baseline" döner ve test kırılır.
func TestInboxAnomalyPriorityRefire(t *testing.T) {
	day0 := time.Unix(1_700_000_000, 0)
	stored := chstore.AnomalyEvent{
		ID: "fp", StartedAt: day0.UnixNano(),
		LastSeen: day0.Add(40 * time.Minute).UnixNano(), PeakRatio: 66,
	}

	cases := []struct {
		name       string
		after      time.Duration // saklı last_seen'den sonra gelen last_seen
		ratio      float64
		wantPrio   string
		wantReason string
		wantStart  int64 // satırın StartedAt'i
	}{
		{
			name:  "iki gün sonra yeniden tetiklenme — yeni bölümün tepesi: P2",
			after: 48 * time.Hour, ratio: 3.2,
			wantPrio: "P2", wantReason: "3.2x baseline",
			wantStart: day0.Add(40*time.Minute + 48*time.Hour).UnixNano(),
		},
		{
			// Aynı bölüm (satır hâlâ aktif) — bugünkü davranış: tepe taşınır.
			name:  "süren bölüm — tepe taşınır: P1",
			after: 2 * time.Minute, ratio: 3.2,
			wantPrio: "P1", wantReason: "66.0x baseline",
			wantStart: day0.UnixNano(),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			last := time.Unix(0, stored.LastSeen).Add(c.after)
			in := chstore.AnomalyEvent{
				ID: "fp", Kind: "trace_op", Pattern: "GET /orders", Service: "orders-api",
				StartedAt: last.UnixNano(), LastSeen: last.UnixNano(),
				CurrentRatio: c.ratio, CurrentCount: 40,
			}
			ev := chstore.MergeAnomalyCarry(in, stored, true)
			ev.Status = "active" // okuma anında türetilir; ikisi de taze

			prio, reason := anomalyPriority(ev)
			if prio != c.wantPrio || reason != c.wantReason {
				t.Errorf("anomalyPriority = %s %q, want %s %q (PeakRatio=%v)",
					prio, reason, c.wantPrio, c.wantReason, ev.PeakRatio)
			}
			item := anomalyToInbox(ev)
			if item.Priority != c.wantPrio {
				t.Errorf("InboxItem.Priority = %s, want %s", item.Priority, c.wantPrio)
			}
			if item.StartedAt != c.wantStart {
				t.Errorf("InboxItem.StartedAt = %v, want %v",
					time.Unix(0, item.StartedAt), time.Unix(0, c.wantStart))
			}
			if item.Anomaly == nil || item.Anomaly.PeakRatio != ev.PeakRatio {
				t.Errorf("InboxItem.Anomaly.PeakRatio satırın tepesini taşımıyor: %+v", item.Anomaly)
			}
		})
	}
}
