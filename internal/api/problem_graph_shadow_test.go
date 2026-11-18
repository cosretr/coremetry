package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestBuildProblemGraphShadowResponse(t *testing.T) {
	if r := buildProblemGraphShadowResponse(chstore.ProblemGraphShadow{}, false); r.Available || len(r.Largest) != 0 {
		t.Fatalf("missing blob: %+v", r)
	}
	rep := chstore.ProblemGraphShadow{Links: map[string]int64{"M-CALL": 2}, Groups: []chstore.ProblemGraphGroup{
		{OwnerID: "p-1", Entities: 1, Members: []chstore.ProblemGraphMember{{ProblemID: "p-1", Name: "search-api", Owner: true}}},
		{OwnerID: "p-2", Entities: 3, Members: []chstore.ProblemGraphMember{
			{ProblemID: "p-2", Name: "cart-api", Owner: true, StartedAt: 5},
			{ProblemID: "p-3", Name: "orders-db", Link: "M-CALL"},
			{ProblemID: "p-4", Name: "checkout-api", Link: "M-CALL"},
		}},
	}}
	r := buildProblemGraphShadowResponse(rep, true)
	if !r.Available || len(r.Largest) != 1 || r.Largest[0].OwnerName != "cart-api" || r.Largest[0].Rules["M-CALL"] != 2 {
		t.Fatalf("largest = %+v", r.Largest)
	}
	if r.Report.Groups != nil || r.Report.Links["M-CALL"] != 2 {
		t.Fatalf("report must carry counters, not groups: %+v", r.Report)
	}
}

// v0.10.2341 — rapor kipi (S4/S5) ve geçiş ölçütü sayılarını taşır.
func TestProblemGraphShadowModesAndCriterion(t *testing.T) {
	rep := chstore.ProblemGraphShadow{Since: 1700000000,
		MergeReport:    &chstore.ProblemGraphMergeReport{Joined: 4, WouldHold: 3, HeldBecameP1: 1, Late: 1},
		FrequentReport: &chstore.FrequentReport{WouldHold: 2}}
	for _, on := range []bool{false, true} {
		restore := chstore.SetProblemGraphMergeEnforceForTest(on)
		r := buildProblemGraphShadowResponse(rep, true)
		restore()
		if r.Modes.S4 != on || r.Modes.S5 {
			t.Fatalf("kip: %+v (S4 %v)", r.Modes, on)
		}
		c := r.Criterion
		if c.WindowStart != 1700000000 || c.MergeJoined != 4 || c.MergeWouldHold != 3 || c.MergeHeldP1 != 1 || c.FrequentWouldHold != 2 || c.Late != 1 {
			t.Fatalf("ölçüt: %+v", c)
		}
	}
	if r := buildProblemGraphShadowResponse(chstore.ProblemGraphShadow{}, false); r.Modes.S4 {
		t.Fatalf("blob yokken de kip söylenir (kapalı): %+v", r.Modes)
	}
}

// v0.10.2342 (S5): rapor ve liste özeti kipi söyler; kapalıyken özet gövdesi aynı.
func TestProblemGraphS5Mode(t *testing.T) {
	rep := chstore.ProblemGraphShadow{At: 9, Frequent: map[string]chstore.FrequentVerdict{"p-1": {Frequent: true, Kind: chstore.FrequentKindHold}}}
	off, _ := json.Marshal(buildProblemGraphSummary(rep, true))
	if strings.Contains(string(off), "frequentEnforce") || buildProblemGraphShadowResponse(rep, true).Modes.S5 {
		t.Fatalf("kapalı: %s", off)
	}
	defer chstore.SetProblemGraphFrequentEnforceForTest(true)()
	if !buildProblemGraphSummary(rep, true).FrequentEnforce || !buildProblemGraphShadowResponse(rep, true).Modes.S5 {
		t.Fatal("açık: kip özet ve raporda")
	}
	if c := buildProblemGraphShadowResponse(chstore.ProblemGraphShadow{FrequentReport: &chstore.FrequentReport{WouldHold: 2, HeldBecameP1: 0}}, true).Criterion; c.FrequentWouldHold != 2 {
		t.Fatalf("S5 ölçüt sayısı: %+v", c)
	}
}
