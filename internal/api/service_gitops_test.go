package api

// v0.10.981 — GitOps sekmesi: MV referansları → tekil iş yükleri.

import (
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestServiceGitOpsWorkloads(t *testing.T) {
	refs := []chstore.WorkloadRevisionRef{
		{Cluster: "prod-a", Namespace: "pay", Workload: "checkout", Revision: "r1"},
		{Cluster: "prod-a", Namespace: "pay", Workload: "checkout", Revision: "r2"}, // aynı iş yükü, başka revizyon
		{Cluster: "prod-a-alias", Namespace: "pay", Workload: "checkout", Revision: "r2"},
		{Cluster: "prod-b", Namespace: "pay", Workload: "checkout", Revision: "r1"},
		{Cluster: "ghost", Namespace: "pay", Workload: "checkout", Revision: "r1"},
		{Cluster: "prod-a", Namespace: "pay", Workload: "", Revision: "r1"},
	}
	bySpan := map[string]string{"prod-a": "c-a", "prod-a-alias": "c-a", "prod-b": "c-b"}
	got, unm := serviceGitOpsWorkloads(refs, bySpan)
	want := []argocd.ServiceWorkload{
		{ClusterID: "c-a", Namespace: "pay", Workload: "checkout"},
		{ClusterID: "c-b", Namespace: "pay", Workload: "checkout"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("iş yükleri %+v", got)
	}
	if !reflect.DeepEqual(unm, []string{"ghost"}) {
		t.Fatalf("eşlenemeyen %v", unm)
	}

	// küme tanımsız (tek küme): değer olduğu gibi geçer, eşlenemeyen yok
	got, unm = serviceGitOpsWorkloads(refs[:1], nil)
	if len(got) != 1 || got[0].ClusterID != "prod-a" || len(unm) != 0 {
		t.Fatalf("tek küme: %+v %v", got, unm)
	}
}
