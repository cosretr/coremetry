package api

// v0.10.1792 — Davis F1.4: inbox olay satırı önceliği tabansızlığı
// baseline_kind'den okur (eski satır: ratio 0). Tabansız → P3 "no baseline";
// tabanlı ama oran 0 (sıfıra düştü) → P3 "no signal" (merdiven değişmedi).

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestAnomalyPriorityBaselineKind(t *testing.T) {
	cases := []struct {
		ev          chstore.AnomalyEvent
		pri, reason string
	}{
		{chstore.AnomalyEvent{Status: "active", PeakRatio: 0, BaselineKind: "none"}, "P3", "no baseline"},
		{chstore.AnomalyEvent{Status: "active", PeakRatio: 0}, "P3", "no baseline"}, // eski satır
		{chstore.AnomalyEvent{Status: "active", PeakRatio: 0, BaselineKind: "seasonal"}, "P3", "no signal"},
		{chstore.AnomalyEvent{Status: "active", PeakRatio: 5, BaselineKind: "consecutive"}, "P2", "5.0x baseline"},
	}
	for _, c := range cases {
		p, r := anomalyPriority(c.ev)
		if p != c.pri || r != c.reason {
			t.Errorf("%+v → %s %q, want %s %q", c.ev, p, r, c.pri, c.reason)
		}
	}
}
