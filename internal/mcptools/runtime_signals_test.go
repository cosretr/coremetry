package mcptools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

func rtReader(rep runtimesig.Report) RuntimeSignalsFunc {
	return func(context.Context, string) (runtimesig.Report, error) { return rep, nil }
}

func TestSweepRuntimeHopFollowsRuntime(t *testing.T) {
	req := SweepRequest{Service: "svc-a", To: time.Now()}
	alerts := chstore.DefaultRuntimeAlerts()

	// .NET: sinyaller bulgu olur, JVM okunmaz, çıktıda JVM yazmaz.
	dot := runtimesig.Report{Service: "svc-a", Mode: "shadow", Pods: 3,
		Profile: runtimesig.Profile{Runtime: runtimesig.RuntimeDotnet, Signals: []runtimesig.Resolved{{Signal: runtimesig.SigPoolQueue, Metric: "process_runtime_dotnet_thread_pool_queue_length"}}},
		Signals: []runtimesig.Signal{
			{Check: runtimesig.CheckPoolStarvation, Level: runtimesig.LevelCritical, Pod: "pod-1", Summary: "Thread pool açlığı: pod-1 …"},
			{Check: runtimesig.CheckGCPressure, Level: runtimesig.LevelInfo, Pod: "pod-2", Summary: "GC baskısı …"},
		}}
	o := sweepRunRuntime(context.Background(), sweepReaders{runtimeSignals: rtReader(dot)}, req, alerts)
	if o.section.Name != sweepHopRuntime || o.section.State != hopOK || o.runtime != "dotnet" || len(o.cands) != 1 || o.cands[0].Kind != sweepKindRuntime || o.cands[0].Name != "pod-1" {
		t.Fatalf(".NET outcome = %+v", o)
	}
	b, _ := json.Marshal(o.section)
	if strings.Contains(string(b), "JVM") || strings.Contains(o.section.Summary, "JVM") {
		t.Fatalf(".NET section mentions JVM: %s", b)
	}

	// Python: halka atlanır, JVM okunmaz (r.runtime nil olsa da "okuyucu yok" denmez).
	py := runtimesig.Report{Profile: runtimesig.Profile{Runtime: runtimesig.RuntimePython}}
	o = sweepRunRuntime(context.Background(), sweepReaders{runtimeSignals: rtReader(py)}, req, alerts)
	if o.section.State != hopSkipped || o.runtime != "python" || len(o.cands) != 0 {
		t.Fatalf("python outcome = %+v", o)
	}

	// JVM: eski halka (okuyucu yok → skipped, JVM adıyla).
	jvm := runtimesig.Report{Profile: runtimesig.Profile{Runtime: runtimesig.RuntimeJVM}}
	o = sweepRunRuntime(context.Background(), sweepReaders{runtimeSignals: rtReader(jvm)}, req, alerts)
	if o.section.Name != sweepHopJVM || o.runtime != "jvm" {
		t.Fatalf("jvm outcome = %+v", o)
	}
}
