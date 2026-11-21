package sre

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/runtimesig"
)

func depCheck(hs []Hypothesis, name string) (HypothesisCheck, bool) {
	for _, h := range hs {
		if h.Kind != HypDependency {
			continue
		}
		for _, c := range h.Checks {
			if c.Name == name {
				return c, true
			}
		}
	}
	return HypothesisCheck{}, false
}

// Follow-up 2026-11-21: giden çağrı tam sayımı (VM client histogramı).
func TestDownstreamExactCheck(t *testing.T) {
	// Okunmadı → kartta hiç yok (VM'siz kurulumun skoru aynen).
	if _, ok := depCheck(Score(Evidence{Service: "svc-a", Facts: Facts{Deps: &DepsFacts{TopologyOK: true}}}), "downstream_exact"); ok {
		t.Fatal("exact unread must not be listed")
	}
	ev := Evidence{Service: "svc-a", Facts: Facts{Deps: &DepsFacts{TopologyOK: true, ExactOK: true, Exact: []runtimesig.TargetRED{
		{Address: "svc-b.ns.svc", Service: "svc-b", RPS: 4, ErrorPct: 12, DominantError: "503", DominantErrorPct: 90, P95Ms: 800},
	}}}}
	c, ok := depCheck(Score(ev), "downstream_exact")
	if !ok || c.Outcome != 1 || !strings.Contains(c.Reason, "svc-b") || !strings.Contains(c.Reason, "503") {
		t.Fatalf("error case = %+v", c)
	}
	// Gecikme baskınlığı: servis p99 +50 %, hedef p95 p99'un yarısından büyük.
	ev.Facts.Deps.Exact = []runtimesig.TargetRED{{Address: "10.0.0.9:8080", RPS: 4, P95Ms: 700}}
	ev.Facts.Deps.Subject = &SubjectChange{P99DeltaPct: 50, CurP99Ms: 1000}
	if c, _ := depCheck(Score(ev), "downstream_exact"); c.Outcome != 1 || !strings.Contains(c.Reason, "p95 700") {
		t.Fatalf("latency case = %+v", c)
	}
	// Temiz → -1.
	ev.Facts.Deps.Subject = nil
	if c, _ := depCheck(Score(ev), "downstream_exact"); c.Outcome != -1 {
		t.Fatalf("clean case = %+v", c)
	}
}
