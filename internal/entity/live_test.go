package entity

import (
	"context"
	"errors"
	"testing"
	"time"
)

// v0.10.2225 (R1b) — canlı dizin: blob kur → resolver okur; ıska / Redis
// hatası / bayat blob → MV yolu; geçmiş an dizine hiç sorulmaz.
func liveFixture(t *testing.T, gen time.Time) *memKV {
	t.Helper()
	cid := "c-1"
	wl := WorkloadID(cid, "shop", "Deployment", "web")
	ents := map[string]Entity{
		PodID(cid, "shop", "web-6f7c9d8b4-x2k9p"): {Type: TypePod, ClusterID: cid, ID: PodID(cid, "shop", "web-6f7c9d8b4-x2k9p"), Namespace: "shop", Name: "web-6f7c9d8b4-x2k9p", UID: "u1", ParentID: wl, Source: SourceThanos},
		PodID(cid, "shop", "web-6f7c9d8b4-q7m3z"): {Type: TypePod, ClusterID: cid, ID: PodID(cid, "shop", "web-6f7c9d8b4-q7m3z"), Namespace: "shop", Name: "web-6f7c9d8b4-q7m3z", ParentID: wl, Source: SourceThanos},
		wl: {Type: TypeWorkload, ClusterID: cid, ID: wl, Namespace: "shop", Name: "web"},
	}
	rels := map[string]Relation{}
	add := func(typ, p, c string) {
		rels[relKey(typ, p, c)] = Relation{Type: typ, ClusterID: cid, ParentID: p, ChildID: c}
	}
	add(RelRunsOn, PodID(cid, "shop", "web-6f7c9d8b4-x2k9p"), NodeID(cid, "w-1"))
	add(RelRuns, PodID(cid, "shop", "web-6f7c9d8b4-x2k9p"), ServiceID("web-api"))
	add(RelRuns, PodID(cid, "shop", "web-6f7c9d8b4-x2k9p"), ServiceID("sidecar"))
	spans := []SeenRow{
		{ClusterValue: "edge-1", Namespace: "shop", Pod: "web-6f7c9d8b4-x2k9p", Service: "web-api", Spans: 90, FirstSeen: gen.Add(-50 * time.Minute), LastSeen: gen},
		{ClusterValue: "edge-1", Namespace: "shop", Pod: "web-6f7c9d8b4-x2k9p", Service: "sidecar", Spans: 3, LastSeen: gen},
	}
	b, err := EncodeLive(BuildLiveSnapshot(cid, gen, ents, rels, spans))
	if err != nil {
		t.Fatal(err)
	}
	kv := newMemKV()
	kv.m[LiveKey(cid)] = b
	return kv
}

type liveClusters struct{}

func (liveClusters) Values(cid string) []string {
	if cid == "c-1" {
		return []string{"edge-1"}
	}
	return nil
}
func (liveClusters) ID(v string) (string, bool) { return "c-1", v == "edge-1" }

func TestLiveSnapshotResolverRead(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	kv := liveFixture(t, now)
	src := &fakeSrc{}
	idx := NewLiveIndex(kv)
	idx.now = func() time.Time { return now }
	r := NewResolver(src, liveClusters{}).WithLive(idx)
	r.now = func() time.Time { return now }
	ctx := context.Background()

	pa := PodID("c-1", "shop", "web-6f7c9d8b4-x2k9p")
	paths, err := r.Paths(ctx, []string{pa, WorkloadID("c-1", "shop", "Deployment", "web")}, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := paths[pa]
	if p.Service != ServiceID("web-api") || p.Workload != WorkloadID("c-1", "shop", "Deployment", "web") || p.NodeName() != "w-1" || p.ClusterValue != "edge-1" || p.Cluster != ClusterID("c-1") {
		t.Fatalf("pod → wl → svc → cluster yolu dizinden: %+v", p)
	}
	if paths[WorkloadID("c-1", "shop", "Deployment", "web")].Service != ServiceID("web-api") {
		t.Fatalf("iş yükü → servis dizindeki pod'lardan: %+v", paths)
	}
	pods, _ := r.Pods(ctx, "c-1", PodQuery{Workload: WorkloadID("c-1", "shop", "ReplicaSet", "web")}, time.Time{})
	if len(pods) != 2 || !pods[1].Live || len(pods[1].Services) == 0 || pods[1].Services[0] != "web-api" || !pods[1].FirstSeen.Equal(now.Add(-50*time.Minute)) {
		t.Fatalf("servis/iş yükü → pod komşuları dizinden (tür bağımsız): %+v", pods)
	}
	if src.calls != 0 {
		t.Fatalf("dizin isabetinde MV okunmamalı: %d", src.calls)
	}
	if kv.get != 1 {
		t.Fatalf("çözülmüş kopya LiveLocalTTL boyunca paylaşılır: %d Get", kv.get)
	}
}

func TestLiveFallbackToMV(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	mv := &fakeSrc{seen: []SeenEdge{{Namespace: "shop", Pod: "web-6f7c9d8b4-x2k9p", Service: "from-mv", Spans: 5, Cluster: "edge-1", LastSeen: now}}}
	ctx := context.Background()
	check := func(name string, kv *memKV, at time.Time) {
		t.Helper()
		mv.calls = 0
		idx := NewLiveIndex(kv)
		idx.now = func() time.Time { return now }
		r := NewResolver(mv, liveClusters{}).WithLive(idx)
		r.now = func() time.Time { return now }
		got, _ := r.PodsByID(ctx, []string{PodID("c-1", "shop", "web-6f7c9d8b4-x2k9p")}, at)
		p := got[PodID("c-1", "shop", "web-6f7c9d8b4-x2k9p")]
		if p.Live || len(p.Services) != 1 || p.Services[0] != "from-mv" || mv.calls != 1 {
			t.Fatalf("%s: MV yoluna düşmeli: %+v calls=%d", name, p, mv.calls)
		}
		if p.Workload != WorkloadID("c-1", "shop", "Deployment", "web") {
			t.Fatalf("%s: iş yükü pod adından türetilir: %q", name, p.Workload)
		}
	}
	check("ıska", newMemKV(), now)
	down := liveFixture(t, now)
	down.err = errors.New("redis: connection refused")
	check("Redis kapalı", down, now)
	check("bayat blob", liveFixture(t, now.Add(-LiveTTL-time.Second)), now)
	check("geçmiş an", liveFixture(t, now), now.Add(-time.Hour))
}

func TestLiveEncodeRoundTrip(t *testing.T) {
	gen := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	kv := liveFixture(t, gen)
	s, err := DecodeLive(kv.m[LiveKey("c-1")])
	if err != nil || s.ClusterID != "c-1" || s.Gen != gen.UnixMilli() || len(s.Pods) != 2 {
		t.Fatalf("gidiş-dönüş: %+v %v", s, err)
	}
	if _, err := DecodeLive([]byte("not gzip")); err == nil {
		t.Fatal("bozuk blob hata vermeli")
	}
}
