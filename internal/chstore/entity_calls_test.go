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

// v0.10.2260 (S6 parça 1) — host kanıt okuması: MV, servis öneki, yalnız
// namespace'siz satır, zaman sınırlı, LIMIT + max_execution_time; ham spans yok.
func TestEntityHostProofSQLShape(t *testing.T) {
	for _, want := range []string{
		"FROM entity_seen_5m", "service_name IN (?)", "k8s_namespace = ''", "k8s_pod IN (?)",
		"time_bucket >= toStartOfFiveMinute(?)", "time_bucket <= now()", "LIMIT 1000", "max_execution_time = 10",
	} {
		if !strings.Contains(entityHostProofSQL, want) {
			t.Errorf("SQL %q içermiyor", want)
		}
	}
	if strings.Contains(entityHostProofSQL, "FROM spans") || strings.Contains(entityHostProofSQL, "entity_seen_1m") {
		t.Error("ham spans / entity_seen_1m okunmamalı")
	}
	names, svcs := hostProofArgs([]entity.HostKey{{Name: "node-a-01", Service: "checkout-api"}, {Name: "node-a-01", Service: "orders-api"}, {Name: "", Service: "x"}})
	if len(names) != 1 || len(svcs) != 2 {
		t.Fatalf("argümanlar: %v %v", names, svcs)
	}
}

// v0.10.2262 (S6 parça 3) — açık iş yükü okuması: rollout MV'si, anahtar
// öneki GROUP BY, zaman sınırlı, LIMIT + max_execution_time; ham spans yok.
func TestEntityExplicitWorkloadsSQLShape(t *testing.T) {
	for _, want := range []string{
		"FROM workload_revision_activity_1m", "bucket >= toStartOfMinute(?)", "bucket <= now()",
		"GROUP BY cluster, k8s_namespace, workload, revision, service_name", "anyLast(workload_kind)", "LIMIT ?", "max_execution_time = 10",
	} {
		if !strings.Contains(entityExplicitWorkloadsSQL, want) {
			t.Errorf("SQL %q içermiyor", want)
		}
	}
	if strings.Contains(entityExplicitWorkloadsSQL, "FROM spans") {
		t.Error("ham spans okunmamalı")
	}
}
