package chstore

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/entity"
)

// v0.10.2240 (Entity S5) — calls okumasının SQL şekli: MV, zaman sınırlı,
// servis düzeyi GROUP BY, RUNS_ON dışlı, LIMIT + max_execution_time; ham
// spans yok, operasyon tablosu yok.
func TestEntityCallEdgesSQLShape(t *testing.T) {
	for _, want := range []string{
		"FROM topology_edges_5m", "time_bucket >= toStartOfFiveMinute(?)", "time_bucket <= now()",
		"GROUP BY parent_service, child_node", TopoCallEdgeFilterSQL, "LIMIT ?", "max_execution_time = 10",
	} {
		if !strings.Contains(entityCallEdgesSQL, want) {
			t.Errorf("SQL %q içermiyor", want)
		}
	}
	for _, bad := range []string{"FROM spans", "topology_op_edges", "FINAL"} {
		if strings.Contains(entityCallEdgesSQL, bad) {
			t.Errorf("SQL %q içermemeli", bad)
		}
	}
}

func TestFoldExtCallEdges(t *testing.T) {
	in := []entity.CallEdge{
		{Parent: "checkout-api", Child: "orders-api", Calls: 3},
		{Parent: "checkout-api", Child: "ext:orders-api.shop.svc.cluster.local:8080", Calls: 2},
		{Parent: "checkout-api", Child: "ext:payments.example", Calls: 1},
	}
	out := foldExtCallEdges(in)
	if len(out) != 2 || out[0].Child != "orders-api" || out[0].Calls != 5 || out[1].Child != "ext:payments.example" {
		t.Fatalf("katlama: %+v", out)
	}
}
