package sre

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/runtimesig"
)

func rtCheckNames(h Hypothesis) string {
	var b []string
	for _, c := range h.Checks {
		b = append(b, c.Name)
	}
	return strings.Join(b, ",")
}

// .NET serviste "neden yavaş" → thread pool açlığı kanıtıyla doygunluk; JVM
// kontrolü kartta hiç yok. JVM serviste tersi.
func TestSaturationRuntimeChecksFollowRuntime(t *testing.T) {
	ev := baseEv(symptomLatency, stepSweep)
	ev.Facts.Pods = &PodFacts{PodsState: "ok", Runtime: "dotnet", RuntimeState: "ok", RuntimeSignals: []runtimesig.Signal{
		{Check: runtimesig.CheckExceptionSpike, Level: runtimesig.LevelWarning, Summary: "Exception sıçraması: pod-1 …"},
		{Check: runtimesig.CheckPoolStarvation, Level: runtimesig.LevelWarning, Summary: "Thread pool açlığı: pod-1 kuyruk ortalaması 150 iş öğesi"},
	}}
	h := hypOf(t, Score(ev), HypSaturation)
	if names := rtCheckNames(h); strings.Contains(names, "jvm_post_gc") || !strings.Contains(names, "runtime_signals") {
		t.Fatalf(".NET checks = %s", names)
	}
	for _, c := range h.Checks {
		if c.Name == "runtime_signals" && (c.Outcome != 1 || !strings.HasPrefix(c.Reason, "Thread pool açlığı") || !strings.Contains(c.Reason, "exception")) {
			t.Fatalf("runtime check = %+v", c)
		}
	}
	// Sinyal yok → çürütür.
	ev.Facts.Pods.RuntimeSignals = nil
	for _, c := range hypOf(t, Score(ev), HypSaturation).Checks {
		if c.Name == "runtime_signals" && c.Outcome != -1 {
			t.Fatalf("healthy = %+v", c)
		}
	}
	// JVM ve bilinmeyen: eski kontrol listesi aynen.
	for _, rt := range []string{"jvm", ""} {
		ev.Facts.Pods = &PodFacts{PodsState: "ok", Runtime: rt}
		if names := rtCheckNames(hypOf(t, Score(ev), HypSaturation)); names != "pod_limits,jvm_post_gc,node_pressure,latency_symptom" {
			t.Fatalf("%q checks = %s", rt, names)
		}
	}
}
