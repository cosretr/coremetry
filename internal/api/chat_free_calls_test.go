package api

// v0.10.2480 (A/B/D) — operatör-bildirimi (prod v0.10.2478): kayıt tohumlanmış
// turda model kök neden + korelasyon kanıtını yeniden çekti, 6 çağrılık tavan
// en ilgili aracı (messaging_flow) "atlandı" yaptı.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
	"github.com/cilcenk/coremetry/internal/sre"
)

func tcOf(name, args string) copilot.ToolCall {
	return copilot.ToolCall{ID: name + args, Name: name, Input: json.RawMessage(args)}
}

func TestSplitByChargedBudget(t *testing.T) {
	calls := []copilot.ToolCall{tcOf("a", `{}`), tcOf("free", `{}`), tcOf("b", `{}`), tcOf("c", `{}`), tcOf("free", `{"x":1}`)}
	free := func(tc copilot.ToolCall) bool { return tc.Name == "free" }
	run, over, freeAt := splitByChargedBudget(calls, 2, free)
	names := func(cs []copilot.ToolCall) string {
		var s []string
		for _, c := range cs {
			s = append(s, c.Name)
		}
		return strings.Join(s, ",")
	}
	// Ücretsiz çağrı tavan dolsa da koşar; tavan yalnız harcanana.
	if names(run) != "a,free,b,free" || names(over) != "c" || countTrue(freeAt) != 2 || len(freeAt) != len(run) {
		t.Fatalf("run=%s over=%s free=%v", names(run), names(over), freeAt)
	}
	if run, over, _ := splitByChargedBudget(calls, -1, nil); len(run) != 0 || len(over) != len(calls) {
		t.Fatal("negatif hak")
	}
}

func TestChargeFix(t *testing.T) {
	cases := []struct {
		left             int
		wasFree, nowFree bool
		want             int
	}{
		{3, false, true, 4}, // ücretli sanıldı, kayıttan / önbellekten çıktı → iade
		{3, true, false, 2}, // ücretsiz sanıldı, yürüdü → düş
		{0, true, false, 0}, // 0'ın altına inmez
		{3, true, true, 3},
		{3, false, false, 3},
	}
	for _, c := range cases {
		if got := chargeFix(c.left, c.wasFree, c.nowFree); got != c.want {
			t.Errorf("%+v: %d", c, got)
		}
	}
}

func TestRecordFreshAndServe(t *testing.T) {
	now := time.Date(2026, 11, 26, 21, 7, 0, 0, time.UTC)
	start := now.Add(-8*time.Hour - 30*time.Minute).UnixNano()
	finished := now.Add(-8 * time.Hour).UnixNano()
	r := &chatProblemRecord{ProblemID: "p-internal", Ref: "P-1234", FinishedAt: finished, ProblemAt: start}

	if _, ok := r.serve("get_problem_root_cause", []byte(`{"problem_id":"p-internal"}`), now); !ok {
		t.Fatal("kök neden aynı Problem için kayıttan karşılanmalı (≤24 sa, başlangıçtan sonra)")
	}
	if c, ok := r.serve("get_correlation_evidence", []byte(`{"problem_id":"p-1234"}`), now); !ok || !strings.Contains(c, `"served_from":"saved_investigation"`) {
		t.Fatalf("görünen kimlik (harf duyarsız) karşılanmalı: %q", c)
	}
	if _, ok := r.serve("get_problem", []byte(`{"problem_id":"p-internal"}`), now); ok {
		t.Fatal("get_problem canlı durum: 8 sa eski kayıttan karşılanmamalı (≤15 dk)")
	}
	if _, ok := r.serve("get_problem", []byte(`{"problem_id":"p-internal"}`), time.Unix(0, finished).Add(10*time.Minute)); !ok {
		t.Fatal("get_problem 10 dk içinde kayıttan")
	}
	if _, ok := r.serve("get_problem_root_cause", []byte(`{"problem_id":"başka"}`), now); ok {
		t.Fatal("başka Problem karşılanmamalı")
	}
	if _, ok := r.serve("messaging_flow", []byte(`{"problem_id":"p-internal"}`), now); ok {
		t.Fatal("kayıt aracı olmayan karşılanmamalı")
	}
	if _, ok := r.serve("get_problem_root_cause", []byte(`{"problem_id":"p-internal"}`), now.Add(17*time.Hour)); ok {
		t.Fatal("24 sa'ten eski kayıt bayat")
	}
	old := &chatProblemRecord{ProblemID: "p", FinishedAt: start - 1, ProblemAt: start}
	if _, ok := old.serve("get_problem_root_cause", []byte(`{"problem_id":"p"}`), now); ok {
		t.Fatal("Problem başlangıcından önce biten kayıt başlangıcı kapsamaz")
	}
	var nilRec *chatProblemRecord
	if _, ok := nilRec.serve("get_problem", []byte(`{"problem_id":"p"}`), now); ok {
		t.Fatal("nil kayıt")
	}
}

func TestLoopCallFree(t *testing.T) {
	now := time.Now()
	r := &chatProblemRecord{ProblemID: "p1", FinishedAt: now.Add(-time.Hour).UnixNano(), ProblemAt: now.Add(-2 * time.Hour).UnixNano()}
	seen := map[string]bool{}
	if !loopCallFree(r, seen, tcOf("get_problem_root_cause", `{"problem_id":"p1"}`), now) {
		t.Fatal("kayıttan karşılanan ücretsiz")
	}
	a := tcOf("list_anomalies", `{"service":"svc-a","range_s":900}`)
	b := tcOf("list_anomalies", `{"range_s":900,"service":"svc-a"}`) // aynı çağrı, anahtar sırası farklı
	if loopCallFree(r, seen, a, now) {
		t.Fatal("ilk çağrı ücretli")
	}
	if !loopCallFree(r, seen, b, now) {
		t.Fatal("aynı araç + argüman ikinci kez ücretsiz (aynı tur önbelleği)")
	}
}

func TestServedStepResultAndStopFree(t *testing.T) {
	m := servedStepResult(4, "get_problem_root_cause", `{"x":1}`, servedFromRecord)
	if m["served"] != servedFromRecord || m["ok"] != true || m["i"] != 4 {
		t.Fatalf("%v", m)
	}
	if _, has := m["durationMs"]; has {
		t.Fatal("ücretsiz çağrı ölçüm değil")
	}
	if ev := chatStopEventFree(chatStopEvent(stopCallCap, 6, 3, time.Second), 0); ev["free"] != nil {
		t.Fatal("free=0 alan yazmamalı (eski şekil)")
	}
	ev := chatStopEventFree(chatStopEvent(stopAnswered, 6, 3, time.Second), 2)
	if ev["calls"] != 6 || ev["free"] != 2 {
		t.Fatalf("%v", ev)
	}
	// loop_stop künyesi harcanan ve ücretsizi ayrı taşır.
	x := &chatExchange{}
	x.noteStopEvent(ev)
	x.noteComplexity(loopComplexity{Tier: complexityComplex, Fallback: true})
	d := cosrelearn.LoopStopDetail(x.loopShape)
	if !strings.Contains(d, "calls=6") || !strings.Contains(d, "free=2") || !strings.Contains(d, "tier=complex cue_fallback=1") {
		t.Fatalf("künye: %s", d)
	}
}

func TestRecordCoverageLine(t *testing.T) {
	inv := sre.Investigation{Findings: []sre.Finding{{Name: "x"}}, Hypotheses: []sre.Hypothesis{{Label: "h"}},
		Facts: &sre.Facts{Impact: &sre.ImpactFacts{Rows: []sre.ImpactFact{{Sentence: "s"}}}}}
	got := recordCoverageTR(inv)
	for _, want := range []string{"KAYIT KAPSAMI: bulgular, kök neden hipotezleri, etki olayları.", "get_problem_root_cause", "yalnız kayıtta OLMAYAN için çağır"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q yok: %s", want, got)
		}
	}
	if recordCoverageTR(sre.Investigation{}) != "" {
		t.Fatal("boş kayıt kapsam yazmamalı")
	}
}

func TestSeededPairs(t *testing.T) {
	conv := []copilot.ChatMessage{{Role: "user", Text: "q"}, {Role: "assistant"}, {Role: "user"}}
	if got := seededPairs(conv, 1); len(got) != 2 {
		t.Fatalf("%d", len(got))
	}
	if seededPairs(conv, 3) != nil || seededPairs(conv, -1) != nil {
		t.Fatal("tohum yokken nil")
	}
}
