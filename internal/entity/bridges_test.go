package entity

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// v0.10.2232 (S2) — kalıcı köprüler: serves (wl→svc) ve same_as.

func wlEnt(id string) Entity {
	r, _ := ParseID(id)
	return Entity{Type: TypeWorkload, ID: id, Namespace: r.Namespace, Name: r.Name}
}

func svcEnt(name string) Entity { return Entity{Type: TypeService, ID: ServiceID(name), Name: name} }

func relSet(rels []Relation) map[string]bool {
	out := map[string]bool{}
	for _, r := range rels {
		out[r.Type+" "+r.ParentID+" "+r.ChildID] = true
	}
	return out
}

func TestBridgeServesFromDominantPodService(t *testing.T) {
	dash := "wl:c-1/web/Deployment/shop-dashboard-prod" // tetik regresyonu (kurgusal ad)
	api := "wl:c-1/web/Deployment/api"
	ents := map[string]Entity{dash: wlEnt(dash), api: wlEnt(api),
		"svc:shop-dashboard-prod": svcEnt("shop-dashboard-prod"), "svc:api": svcEnt("api"), "svc:api-worker": svcEnt("api-worker")}
	snap := LiveSnapshot{Pods: []LivePod{
		{Namespace: "web", Name: "shop-dashboard-prod-5c7dbf9b8d-x2b9z", Workload: dash, Services: []LiveService{{Name: "shop-dashboard-prod", Spans: 9}}},
		{Namespace: "web", Name: "api-5c7dbf9b8d-b2c4d", Workload: api, Services: []LiveService{{Name: "api", Spans: 9}, {Name: "api-worker", Spans: 2}}},
		{Namespace: "web", Name: "api-5c7dbf9b8d-k9q2z", Workload: api, Services: []LiveService{{Name: "api-worker", Spans: 5}}},
		{Namespace: "web", Name: "lonely", Services: []LiveService{{Name: "api", Spans: 1}}}, // iş yüksüz
	}}
	got := relSet(BridgeRelations("c-1", ents, snap, OpenSet{}))
	want := map[string]bool{
		"serves " + dash + " svc:shop-dashboard-prod": true,
		"serves " + api + " svc:api":                  true,
		"serves " + api + " svc:api-worker":           true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("serves:\n got %v\nwant %v", got, want)
	}
}

func TestBridgeServesCarriedWithoutEvidence(t *testing.T) {
	quiet := "wl:c-1/web/Deployment/quiet"
	gone := "wl:c-1/web/Deployment/gone"
	ents := map[string]Entity{quiet: wlEnt(quiet), "svc:quiet": svcEnt("quiet")}
	open := OpenSet{Relations: map[string]Relation{
		relKey(RelServes, quiet, "svc:quiet"): {Type: RelServes, ParentID: quiet, ChildID: "svc:quiet", Source: SourceSpan},
		relKey(RelServes, gone, "svc:gone"):   {Type: RelServes, ParentID: gone, ChildID: "svc:gone", Source: SourceSpan},
	}}
	got := relSet(BridgeRelations("c-1", ents, LiveSnapshot{}, open))
	if !got["serves "+quiet+" svc:quiet"] || got["serves "+gone+" svc:gone"] {
		t.Fatalf("susan iş yükünün kenarı korunur, görülmeyeninki korunmaz: %v", got)
	}
}

func TestBridgeSameAs(t *testing.T) {
	dep := "wl:c-1/web/Deployment/checkout"
	derived := "wl:c-1/web/Derived/checkout"
	rs := "wl:c-1/web/ReplicaSet/checkout-7d9f8c6b5"
	other := "wl:c-1/web/Derived/billing"
	ents := map[string]Entity{dep: wlEnt(dep),
		"wl:c-1/web/Deployment/billing": wlEnt("wl:c-1/web/Deployment/billing"), "wl:c-1/web/StatefulSet/billing": wlEnt("wl:c-1/web/StatefulSet/billing")}
	open := OpenSet{Entities: map[string]Entity{derived: wlEnt(derived), rs: wlEnt(rs), other: wlEnt(other), dep: wlEnt(dep)}}
	got := relSet(BridgeRelations("c-1", ents, LiveSnapshot{}, open))
	want := map[string]bool{"same_as " + derived + " " + dep: true, "same_as " + rs + " " + dep: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("same_as (iki yetkili aday = kenar yok):\n got %v\nwant %v", got, want)
	}
	// Hedef görüldükçe kenar korunur (eski ömür kapandıktan sonra da).
	open2 := OpenSet{Relations: map[string]Relation{relKey(RelSameAs, derived, dep): {Type: RelSameAs, ParentID: derived, ChildID: dep}}}
	if got := relSet(BridgeRelations("c-1", ents, LiveSnapshot{}, open2)); !got["same_as "+derived+" "+dep] {
		t.Fatalf("same_as korunmalı: %v", got)
	}
}

// K2 + S2 — kayıt span değerini sahiplenince eski span-only ömürler kapanır,
// cluster:<değer> → cluster:<cid> same_as açılır; değer başına süreçte bir okuma.
func TestSyncerMergesSpanOnlyCluster(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{clusters: []ClusterRef{{ID: "c-a", Name: "a"}}, sets: map[string]SampleSets{"c-a": podSet("db-0"), "c-b": podSet("db-0")}}
	st := newMemStore()
	sy := NewSyncer(src, st, func() Resolved { return Resolved{} })
	sy.now = func() time.Time { return t0 }
	rows := []SeenRow{{ClusterValue: "edge-b", Namespace: "ns", Pod: "web-6f7c9b8d-x2b9z", Service: "web", Spans: 3, LastSeen: t0}}
	sy.seen = fakeSeen{rows: rows}
	sy.Tick(context.Background())
	if len(st.open["edge-b"]) == 0 {
		t.Fatal("ön koşul: span-only ömürler açık")
	}
	src.clusters = append(src.clusters, ClusterRef{ID: "c-b", Name: "b", SpanClusterValue: "edge-b", SpanClusterValues: []string{"edge-b"}})
	sy.now = func() time.Time { return t0.Add(time.Minute) }
	sy.Tick(context.Background())
	if n := len(st.open["edge-b"]); n != 0 {
		t.Fatalf("span-only ömürler kapanmalı, açık %d: %v", n, st.open["edge-b"])
	}
	if _, ok := st.open["c-b"][relKey(RelSameAs, ClusterID("edge-b"), ClusterID("c-b"))]; !ok {
		t.Fatalf("same_as açılmalı: %v", st.open["c-b"])
	}
	sy.now = func() time.Time { return t0.Add(2 * time.Minute) }
	sy.Tick(context.Background())
	if _, ok := st.open["c-b"][relKey(RelSameAs, ClusterID("edge-b"), ClusterID("c-b"))]; !ok {
		t.Fatal("same_as sonraki tick'lerde korunmalı")
	}
}

type fakeRels struct{ edges []Relation }

func (f fakeRels) RelationsFor(_ context.Context, rel string, ids []string, byChild bool, _ time.Time, _ int) ([]Relation, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []Relation
	for _, e := range f.edges {
		end := e.ParentID
		if byChild {
			end = e.ChildID
		}
		if e.Type == rel && want[end] {
			out = append(out, e)
		}
	}
	return out, nil
}

func TestResolverCanonicalAndWorkloadServices(t *testing.T) {
	dep := "wl:c-1/web/Deployment/checkout"
	rels := fakeRels{edges: []Relation{
		{Type: RelSameAs, ParentID: "wl:c-1/web/Derived/checkout", ChildID: dep},
		{Type: RelSameAs, ParentID: "cluster:edge-b", ChildID: "cluster:c-b"},
		{Type: RelServes, ParentID: dep, ChildID: "svc:checkout-api"},
		{Type: RelServes, ParentID: dep, ChildID: "svc:checkout-web"},
	}}
	r := NewResolver(nil, nil).WithRelations(rels)
	ctx := context.Background()
	for in, want := range map[string]string{
		"wl:c-1/web/Derived/checkout":              dep,
		"wl:c-1/web/ReplicaSet/checkout-7d9f8c6b5": dep,
		"cluster:edge-b":                           "cluster:c-b",
		"wl:c-1/web/Deployment/unknown":            "wl:c-1/web/Deployment/unknown",
	} {
		if got := r.Canonical(ctx, in, time.Time{}); got != want {
			t.Errorf("Canonical(%s) = %s, want %s", in, got, want)
		}
	}
	m, err := r.WorkloadServices(ctx, []string{"wl:c-1/web/Derived/checkout", "wl:c-1/web/Deployment/none"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := m["wl:c-1/web/Derived/checkout"]; !reflect.DeepEqual(got, []string{"checkout-api", "checkout-web"}) {
		t.Fatalf("serves (same_as üstünden): %v", got)
	}
	if _, ok := m["wl:c-1/web/Deployment/none"]; ok {
		t.Fatal("kenarsız + kaynaksız iş yükü servis almaz")
	}
	svcs, _ := r.ServiceWorkloads(ctx, "checkout-api", time.Time{}, 10)
	if !reflect.DeepEqual(svcs, []string{dep}) {
		t.Fatalf("ServiceWorkloads: %v", svcs)
	}
}
