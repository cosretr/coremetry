package runtimesig

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

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
