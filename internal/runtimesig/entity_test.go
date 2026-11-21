package runtimesig

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

// Follow-up 2026-11-21: prod VM serileri host_name (= pod adı) taşır — pod
// boyutu önce host_name, sonra k8s_pod_name.
func TestPodGroupByHostFirst(t *testing.T) {
	if PodGroupBy[0] != "resource.host.name" || PodGroupBy[1] != "resource.k8s.pod.name" {
		t.Fatalf("PodGroupBy = %v", PodGroupBy)
	}
	if got := podOf([]string{"svc-a-7f9c-1", "", ""}, 0); got != "svc-a-7f9c-1" {
		t.Fatalf("host_name pod = %q", got)
	}
	if got := podOf([]string{"", "svc-a-7f9c-2", ""}, 0); got != "svc-a-7f9c-2" {
		t.Fatalf("k8s_pod_name pod = %q", got)
	}
}

func TestCapPodIndex(t *testing.T) {
	in := map[string]PodEntityRef{"p-c": {Pod: "p-c"}, "p-a": {Pod: "p-a"}, "p-b": {Pod: "p-b"}}
	out := CapPodIndex(in, 2)
	if len(out) != 2 || out["p-a"].Pod != "p-a" || out["p-b"].Pod != "p-b" {
		t.Fatalf("cap = %v", out)
	}
	if CapPodIndex(nil, 2) != nil {
		t.Fatal("empty → nil")
	}
}

func TestPodEntityIndex(t *testing.T) {
	aggs := []chstore.EntitySeenAgg{
		{Cluster: "prod-clusterA", Namespace: "ns-a", Pod: "svc-a-7f9c-1"},
		{Cluster: "prod-clusterA", Namespace: "ns-a", Pod: "svc-a-7f9c-2"},
		{Cluster: "prod-clusterB", Namespace: "ns-a", Pod: "svc-a-7f9c-2"}, // aynı ad iki cluster → belirsiz
		{Cluster: "unmapped", Namespace: "ns-a", Pod: "svc-a-x"},
	}
	idx := PodEntityIndex(aggs, map[string]string{"prod-clusterA": "cl-a", "prod-clusterB": "cl-b"})
	r, ok := idx["svc-a-7f9c-1"]
	if !ok || r.ID != entity.PodID("cl-a", "ns-a", "svc-a-7f9c-1") || r.ClusterID != "cl-a" {
		t.Fatalf("pod-1 = %+v", r)
	}
	if _, ok := idx["svc-a-7f9c-2"]; ok {
		t.Fatal("ambiguous pod name must not resolve")
	}
	if _, ok := idx["svc-a-x"]; ok {
		t.Fatal("unmapped cluster must not resolve")
	}
}
