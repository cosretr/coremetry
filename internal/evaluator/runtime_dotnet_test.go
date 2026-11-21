package evaluator

import (
	"sort"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

func TestDotnetPlan(t *testing.T) {
	sigs := []runtimesig.Signal{
		{Check: runtimesig.CheckPoolStarvation, Level: runtimesig.LevelCritical, Service: "svc-a"},
		{Check: runtimesig.CheckGCPressure, Level: runtimesig.LevelInfo, Service: "svc-a"},        // açık değil → yok
		{Check: runtimesig.CheckGCPressure, Level: runtimesig.LevelInfo, Service: "svc-b"},        // açık → histerezis
		{Check: runtimesig.CheckLockContention, Level: runtimesig.LevelWarning, Service: "svc-c"}, // tavan dışı
	}
	open := map[string]bool{
		dotnetProblemID(runtimesig.CheckGCPressure, "svc-b"): true,
		dotnetProblemID(runtimesig.CheckMemoryLeak, "svc-d"): true, // sinyal geçti → kapanır
	}
	acts, closes := dotnetPlan(sigs, open, 1)
	if len(acts) != 2 || acts[0].sig.Service != "svc-a" || acts[0].hasOpen || acts[1].sig.Service != "svc-b" || !acts[1].hasOpen || acts[1].sig.Level != runtimesig.LevelWarning {
		t.Fatalf("acts = %+v", acts)
	}
	sort.Strings(closes)
	if len(closes) != 1 || closes[0] != "runtime:dotnet-memory-leak:svc-d" {
		t.Fatalf("closes = %v", closes)
	}
}

// Yeni kural ailesi problem standardında runtime ailesine düşer.
func TestDotnetProblemStandardFamily(t *testing.T) {
	p := chstore.Problem{ID: "runtime:dotnet-gc-pressure:svc-a", RuleID: "runtime:dotnet-gc-pressure", Metric: "runtime.dotnet_gc_pressure", Service: "svc-a"}
	if got := chstore.ProblemMergeClass(p.RuleID); got != chstore.MergeSeparate {
		t.Fatalf("merge class = %v, want separate", got)
	}
	if chstore.ProblemNotifyKind(p) != chstore.NotifyKindProblem {
		t.Fatal("notify kind must be problem")
	}
}
