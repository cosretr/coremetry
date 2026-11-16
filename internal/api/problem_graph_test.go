package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestBuildProblemGraphResponse(t *testing.T) {
	rep := chstore.ProblemGraphShadow{At: 7, Groups: []chstore.ProblemGraphGroup{
		{OwnerID: "p-cart", Entities: 2, Members: []chstore.ProblemGraphMember{
			{ProblemID: "p-cart", Name: "cart-api", Owner: true},
			{ProblemID: "p-db", Name: "orders-db", Link: chstore.GraphRuleCall},
		}, NearMisses: []chstore.ProblemGraphNearMiss{{ProblemID: "p-search", Name: "search-api", Reason: chstore.GraphNoRel}}},
		{OwnerID: "p-search", Entities: 1, Members: []chstore.ProblemGraphMember{{ProblemID: "p-search", Name: "search-api", Owner: true}},
			NearMisses: []chstore.ProblemGraphNearMiss{{ProblemID: "p-cart", Name: "cart-api", Reason: chstore.GraphNoRel}}},
	}}
	r := buildProblemGraphResponse(rep, true, "p-db")
	if !r.Shadow || r.OwnerID != "p-cart" || len(r.Members) != 2 || len(r.NearMisses) != 1 || r.At != 7 {
		t.Fatalf("member view = %+v", r)
	}
	// Tek Problem: üye listesi boş, yalnız bağlanmayanlar.
	if r := buildProblemGraphResponse(rep, true, "p-search"); len(r.Members) != 0 || len(r.NearMisses) != 1 {
		t.Fatalf("singleton view = %+v", r)
	}
	// Kümesiz Problem ve blob yok: boş (sayfa hiçbir şey çizmez).
	for _, r := range []problemGraphResponse{buildProblemGraphResponse(rep, true, "p-other"), buildProblemGraphResponse(rep, false, "p-db")} {
		if len(r.Members) != 0 || len(r.NearMisses) != 0 || r.Members == nil || r.NearMisses == nil {
			t.Fatalf("empty view = %+v", r)
		}
	}
	if problemGraphKey("p-db") != "problem:graph:v3:p-db" {
		t.Fatal("cache key carries the id")
	}
}

func TestBuildProblemGraphSummary(t *testing.T) {
	rep := chstore.ProblemGraphShadow{At: 7, Groups: []chstore.ProblemGraphGroup{
		{OwnerID: "p-cart", Entities: 2, Members: []chstore.ProblemGraphMember{{ProblemID: "p-cart"}, {ProblemID: "p-db"}},
			Origin: &chstore.ProblemGraphOrigin{Name: "orders-db", Confidence: chstore.OriginStrong, Earliest: "orders-db"}},
		{OwnerID: "p-search", Entities: 1, Members: []chstore.ProblemGraphMember{{ProblemID: "p-search"}}},
	}}
	s := buildProblemGraphSummary(rep, true)
	if len(s.Rows) != 2 || s.Rows["p-db"].Origin != "orders-db" || s.Rows["p-cart"].Events != 2 || s.Rows["p-cart"].Entities != 2 {
		t.Fatalf("summary = %+v", s)
	}
	if _, single := s.Rows["p-search"]; single {
		t.Fatal("single-service Problem must carry no sub-line")
	}
	if e := buildProblemGraphSummary(rep, false); e.Rows == nil || len(e.Rows) != 0 {
		t.Fatalf("no blob = %+v", e)
	}
}

func TestProblemGraphFrequentViews(t *testing.T) {
	rep := chstore.ProblemGraphShadow{At: 9, Frequent: map[string]chstore.FrequentVerdict{
		"p-batch": {Signature: "svc:report-batch|traffic|down", Frequent: true, Kind: chstore.FrequentKindHold, Prior24h: 1, Days7d: 5},
	}}
	if r := buildProblemGraphResponse(rep, true, "p-batch"); r.Frequent == nil || r.Frequent.Days7d != 5 || len(r.Members) != 0 {
		t.Fatalf("detail = %+v", r)
	}
	s := buildProblemGraphSummary(rep, true)
	if row := s.Rows["p-batch"]; row.Frequent == nil || row.Frequent.Kind != chstore.FrequentKindHold || row.Entities != 0 {
		t.Fatalf("summary row = %+v", row)
	}
	if a := buildProblemGraphShadowResponse(rep, true); a.Report.Frequent != nil {
		t.Fatal("admin report must not carry per-Problem verdicts")
	}
}
