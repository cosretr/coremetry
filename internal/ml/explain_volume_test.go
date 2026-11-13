package ml

// explain_volume_test.go — v0.10.2118: hacim sıçraması anomalisinin düz
// cümlesi (ML detay sayfası özeti + "Ne kadar"). Operatör kararı
// (2026-11-13): Problem değil anomali; detay "X servisi önceki 24 saatte N,
// son 24 saatte M span yazdı; K kat" der.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestVolumeSpikeExplainSentence(t *testing.T) {
	ev := chstore.MLEvent{ID: "v", Detector: chstore.MLDetSvcVolumeSpike, Signal: chstore.MLSignalTrace, Service: "orders-api",
		StartedAt: at(9, 0).UnixNano(), LastSeen: at(14, 0).UnixNano(), WindowSec: 3600,
		Baseline: 210_000, Observed: 1_200_000, Score: 5.71, Direction: "up"}
	in := SummaryInput(ev, chstore.MLConfig{}, at(14, 10), trLoc)
	want := "orders-api servisi önceki 24 saatte 210.000, son 24 saatte 1.200.000 span yazdı; 5,7 kat"
	if got := ExplainSummary(in); !strings.HasPrefix(got, want+", ") {
		t.Errorf("özet: %q", got)
	}
	c := BuildExplainCard(in)
	if c.HowMuch != want+"." {
		t.Errorf("ne kadar: %q", c.HowMuch)
	}
	if c.What != "orders-api servisinin yazdığı span sayısı bir günde sıçradı." {
		t.Errorf("ne oldu: %q", c.What)
	}
	if c.Header.Kind != chstore.MLSignalTrace {
		t.Errorf("tür: %q", c.Header.Kind)
	}
}
