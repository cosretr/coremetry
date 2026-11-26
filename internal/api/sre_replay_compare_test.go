package api

// GET /api/sre/replay/compare: viewer okur, geçersiz kimlik 400, olmayan
// rapor 404, iki rapor → sayaçlar + sürüm damgaları.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/sre"
)

func TestSREReplayCompare(t *testing.T) {
	s, mux, rf := newReplayTestEnv(t)
	s.registerSREReplayCompareRoutes(mux)
	put := func(id, build, grade string) {
		sc, _ := sre.GradeScore(grade)
		rep := sre.ReplayReport{ReportID: id, State: sre.ReplayDone, Build: build, CreatedAt: 1, Items: []sre.ReplayItem{
			{ProblemID: "p9", Service: "pay", Runs: []sre.ReplayRun{{Rep: 1, State: sre.StateDone, Grade: grade, Score: &sc}}}}}
		raw, _ := json.Marshal(rep)
		if err := rf.PutSREReplayReport(context.Background(), id, rep.State, string(raw), 1); err != nil {
			t.Fatal(err)
		}
	}
	put("ra", "v0.10.1", sre.GradeMiss)
	put("rb", "v0.10.2", sre.GradeBullseye)
	w := sreDo(mux, "GET", "/api/sre/replay/compare?base=ra&cand=rb", "", auth.RoleViewer)
	if w.Code != 200 {
		t.Fatalf("viewer = %d %s", w.Code, w.Body)
	}
	var c sre.ReplayCompare
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.Improved != 1 || c.Base.Build != "v0.10.1" || c.Cand.Build != "v0.10.2" || c.ScoreDelta == nil || *c.ScoreDelta != 65 {
		t.Fatalf("karşılaştırma: %s", w.Body)
	}
	if w := sreDo(mux, "GET", "/api/sre/replay/compare?base=ra&cand=bad-id", "", auth.RoleViewer); w.Code != 400 {
		t.Errorf("geçersiz kimlik = %d", w.Code)
	}
	if w := sreDo(mux, "GET", "/api/sre/replay/compare?base=ra&cand=rnope", "", auth.RoleViewer); w.Code != 404 {
		t.Errorf("olmayan rapor = %d", w.Code)
	}
}
