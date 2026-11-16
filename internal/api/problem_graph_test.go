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
	if problemGraphKey("p-db") != "problem:graph:v1:p-db" {
		t.Fatal("cache key carries the id")
	}
}
