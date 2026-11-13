package ml

// svc_breadth_test.go — v0.10.2086 (spec-ml-service-level dilim 2):
// yaygın yavaşlama — op_latency olaylarından türetilen servis kararı.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func opLat(svc, op string, p95, base float64, measure string) chstore.MLEvent {
	return chstore.MLEvent{ID: "op:" + svc + op, Detector: detOpLatency, Signal: chstore.MLSignalTrace, Service: svc, Code: op,
		Observed: p95, Baseline: base, Direction: "up", Score: 6, Evidence: mergeEvidence("", map[string]any{"measure": measure})}
}

func TestBreadthOpsBySvc(t *testing.T) {
	isBatch := func(s string) bool { return strings.HasSuffix(s, "-batch") }
	evs := []chstore.MLEvent{
		opLat("orders-api", "GET /a", 1400, 200, "p95"),        // 7×
		opLat("orders-api", "GET /b", 2500, 1000, "p95"),       // 2,5×
		opLat("orders-api", "GET /c", 900, 100, "p95"),         // < 1000 ms → yok
		opLat("orders-api", "GET /d", 1800, 1000, "p95"),       // 1,8× → yok
		opLat("orders-api", "GET /e", 0.4, 0.05, "slow_ratio"), // p95 ölçüsü değil → yok
		opLat("orders-api", "GET /f", 3000, 1000, "p95"),       // 3×
		opLat("report-batch", "RUN", 9000, 1000, "p95"),        // batch → yok
	}
	chronic := opLat("orders-api", "GET /g", 5000, 1000, "p95")
	chronic.Stage = chstore.MLStageChronic
	evs = append(evs, chronic)
	got := breadthOpsBySvc(evs, isBatch)
	if len(got["report-batch"]) != 0 {
		t.Fatal("batch servis dışarıda olmalı")
	}
	ops := got["orders-api"]
	if len(ops) != 3 || ops[0].Op != "GET /a" || ops[1].Op != "GET /f" || ops[2].Op != "GET /b" {
		t.Fatalf("ops=%+v", ops)
	}
}

func TestBreadthVerdict(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	three := map[string][]BreadthOp{"orders-api": {{Op: "a"}, {Op: "b"}, {Op: "c"}}}
	two := map[string][]BreadthOp{"orders-api": {{Op: "a"}, {Op: "b"}}}
	// 1 tik → açmaz
	fire, st := breadthVerdict(three, nil, t0)
	if len(fire) != 0 || st["orders-api"].ticks != 1 {
		t.Fatalf("1 tik açmamalı: %v %v", fire, st)
	}
	// 2. ardışık tik (10 dk) → açar, başlangıç ilk tik
	fire, st = breadthVerdict(three, st, t0.Add(5*time.Minute))
	if r, ok := fire["orders-api"]; !ok || !r.since.Equal(t0) {
		t.Fatalf("2 tikte açmalı: %v", fire)
	}
	// 2 op → koşul düşer, durum sıfırlanır
	fire, st = breadthVerdict(two, st, t0.Add(10*time.Minute))
	if len(fire) != 0 || len(st) != 0 {
		t.Fatalf("2 op açmamalı ve ardışıklık sıfırlanmalı: %v %v", fire, st)
	}
	fire, _ = breadthVerdict(three, st, t0.Add(15*time.Minute))
	if len(fire) != 0 {
		t.Fatal("ara verdikten sonra yeniden 2 tik beklenir")
	}
}

func TestBreadthEventsAndSentence(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	cfg := chstore.DefaultMLConfig()
	evs := []chstore.MLEvent{
		opLat("orders-api", "GET /a", 1400, 200, "p95"),
		opLat("orders-api", "GET /b", 2500, 1000, "p95"),
		opLat("orders-api", "GET /f", 3000, 1000, "p95"),
		opLat("orders-api", "POST /x", 4000, 1000, "p95"),
	}
	_, st := breadthEvents(evs, nil, nil, t0, cfg)
	out, _ := breadthEvents(evs, nil, st, t0.Add(5*time.Minute), cfg)
	if len(out) != 1 {
		t.Fatalf("tek olay beklenir: %d", len(out))
	}
	e := out[0]
	if e.Detector != detSvcBreadth || e.Signal != chstore.MLSignalTrace || e.Code != "" || e.ID != chstore.MLEventID(detSvcBreadth, "orders-api", "") {
		t.Fatalf("olay kimliği: %+v", e)
	}
	// oran medyanı (2,5; 3; 4; 7 → 3,5) × taban medyanı (1000) → Value/Threshold oranı 3,5
	if e.Baseline != 1000 || e.Observed != 3500 || e.StartedAt != t0.UnixNano() || e.Score != 6 {
		t.Fatalf("değerler: base=%v obs=%v start=%v score=%v", e.Baseline, e.Observed, e.StartedAt, e.Score)
	}
	in := CardInputFromEvent(e, t0.Add(6*time.Minute), time.UTC)
	got := ExplainSummary(in)
	want := "orders-api: 4 işlem birlikte yavaşladı (p95) — GET /a normalde 200 ms, şimdi 1,4 sn; POST /x normalde 1 sn, şimdi 4 sn; GET /f normalde 1 sn, şimdi 3 sn; ve 1 işlem daha"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("cümle:\n got %s\nwant %s", got, want)
	}
	if eventPart(e) != "" || !chstore.MLDetectorDefaultOn(detSvcBreadth) || chstore.MLDetectorSignal(detSvcBreadth) != chstore.MLSignalTrace {
		t.Fatal("part boş, varsayılan açık, trace sinyali")
	}
}
