package api

import (
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
