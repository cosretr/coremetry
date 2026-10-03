package api

// v0.10.1072 — regressed grupta yeniden açılış hacmi (operatör onaylı, prod
// 2026-10-02): 54.812 oluşumlu bir grup regressed olduğu için P2 görünüyordu.
// Kapı ömür boyu toplama DEĞİL, resolve anındaki anlık görüntünün
// (occurrences_at_resolve) üstündeki hacme bakar; anlık görüntüsüz grup P2.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestExceptionLadderRegressedVolumeSinceResolve(t *testing.T) {
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	cfg := chstore.DefaultExceptionTriage() // P1MinOccurrences 500
	// 30 günlük ömür: hız patlama eşiğinin çok altında (patlama dalı devre dışı).
	first := now.Add(-30 * 24 * time.Hour).UnixNano()
	last := now.Add(-2 * time.Hour).UnixNano()
	grp := func(state string, occ, snap uint64) chstore.ExceptionGroup {
		return chstore.ExceptionGroup{Fingerprint: "fp", State: state, Occurrences: occ,
			OccurrencesAtResolve: snap, FirstSeen: first, LastSeen: last}
	}
	cases := []struct {
		name       string
		g          chstore.ExceptionGroup
		wantPrio   string
		wantReason string
	}{
		{"regressed + 499 yeni → P2", grp("regressed", 10_499, 10_000), "P2", "regressed"},
		{"regressed + 500 yeni → P1", grp("regressed", 10_500, 10_000), "P1",
			"yeniden açıldıktan sonra ≥500 oluşum (500 yeni · 10,500 toplam)"},
		{"regressed, anlık görüntüsüz, 50k ömür → P2 (ömür toplamıyla P1 ASLA)",
			grp("regressed", 50_000, 0), "P2", "regressed"},
		{"regressed, tutarsız anlık görüntü (toplam < görüntü) → P2", grp("regressed", 400, 900), "P2", "regressed"},
		{"new grup ≥500 → P1 (hacim kapısı değişmedi)", grp("new", 500, 0), "P1", "500 total · stopped"},
		{"acknowledged grubun eski görüntüsü kapıya girmez", grp("acknowledged", 10_600, 10_000), "P1", "total · stopped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prio, reason := exceptionPriorityAt(c.g, cfg, now)
			if prio != c.wantPrio || !strings.Contains(reason, c.wantReason) {
				t.Fatalf("= (%s, %q), want (%s, …%q…)", prio, reason, c.wantPrio, c.wantReason)
			}
		})
	}
	// Eşik vidası okunuyor: P1MinOccurrences 1000'de aynı 500 yeni → P2.
	tight := cfg
	tight.P1MinOccurrences = 1000
	if prio, _ := exceptionPriorityAt(grp("regressed", 10_500, 10_000), tight, now); prio != "P2" {
		t.Errorf("eşik 1000'de 500 yeni → %s, want P2", prio)
	}
}
