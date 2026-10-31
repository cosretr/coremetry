package ml

// v0.10.1754 — T1b: hangi trace dedektörü hangi bağlamı alır; BubbleUp
// özeti boş sonuçta boş liste (null değil — kanıt JSON'u UI'da dizi okunur).

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestNeedsTraceContext(t *testing.T) {
	cases := []struct {
		det     string
		ex, bub bool
	}{{detOpErrorRate, true, true}, {detOpLatency, false, true}, {detThroughputDrop, false, false}, {detLogVolume, false, false}}
	for _, c := range cases {
		ex, bub := needsTraceContext(c.det)
		if ex != c.ex || bub != c.bub {
			t.Errorf("%s: got %v,%v", c.det, ex, bub)
		}
	}
	if got := bubbleSummary(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil sonuç boş liste olmalı, got %#v", got)
	}
	bu := &chstore.BubbleUpResult{SelectionTotal: 100, BaselineTotal: 100, Attributes: []chstore.BubbleUpAttribute{
		{Key: "k8s.pod.name", Values: []chstore.BubbleUpValue{{Value: "pay-7", SelectionCount: 90, BaselineCount: 10, SelectionPct: 0.9, BaselinePct: 0.1, Score: 0.8}}}}}
	if got := bubbleSummary(bu); len(got) == 0 || got[0]["value"] != "pay-7" {
		t.Fatalf("got %#v", got)
	}
}
