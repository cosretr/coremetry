package entity

import (
	"context"
	"testing"
	"time"
)

// v0.10.2239 (Entity S4) — svc → pod komşuluğu: canlı dizin (span'li pod +
// serves kenarlı iş yükünün span'siz pod'u), kardeş önek karışmaz, ıska →
// MV servis öneki, depo boş → küme boş.
func TestServicePodsLiveServesAndSiblings(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	idx := NewLiveIndex(liveFixture(t, now))
	idx.now = func() time.Time { return now }
	src := &fakeSrc{}
	wl := WorkloadID("c-1", "shop", "Deployment", "web")
	r := NewResolver(src, liveClusters{}).WithLive(idx).
		WithRelations(fakeRels{edges: []Relation{{Type: RelServes, ParentID: wl, ChildID: ServiceID("web-api")}}})
	r.now = func() time.Time { return now }
	ctx := context.Background()

	set, err := r.ServicePods(ctx, "web-api", []string{"c-1"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Pods) != 2 || set.Source != "live" || src.calls != 0 {
		t.Fatalf("span'li pod + serves kenarlı iş yükünün span'siz pod'u, MV okunmaz: %+v calls=%d", set, src.calls)
	}
	if got := set.ByCluster()["c-1"]; len(got) != 2 || got[0] != "web-6f7c9d8b4-q7m3z" {
		t.Fatalf("cluster başına sıralı adlar: %v", got)
	}
	// Eski önek kuralı "web-.*" iki pod'u da "web" servisine verirdi.
	if set, _ := r.ServicePods(ctx, "web", []string{"c-1"}, time.Time{}); len(set.Pods) != 0 || set.Source != "" {
		t.Fatalf("kardeş önek karışmaz (ad eşitliği yok): %+v", set)
	}
	if wp, _ := r.WorkloadPodsNamed(ctx, "web", []string{"c-1"}, time.Time{}); len(wp.Pods) != 2 {
		t.Fatalf("iş yükü adı → pod'lar dizinden: %+v", wp)
	}
	if wp, _ := r.WorkloadPodsNamed(ctx, "we", []string{"c-1"}, time.Time{}); len(wp.Pods) != 0 {
		t.Fatalf("iş yükü adında önek eşleşmesi yok: %+v", wp)
	}
	if pn, _ := r.PodsNamed(ctx, "web-6f7c9d8b4-x2k9p", []string{"c-1"}, time.Time{}); len(pn) != 1 || pn[0].Services[0] != "web-api" {
		t.Fatalf("pod adı → sahibi servis dizinden: %+v", pn)
	}
}

func TestServicePodsFallbackAndEmpty(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	src := &fakeSrc{seen: []SeenEdge{
		{Namespace: "shop", Pod: "web-6f7c9d8b4-x2k9p", Service: "web-api", Spans: 9, Cluster: "edge-1", LastSeen: now},
		{Namespace: "shop", Pod: "other-5d4c3b2a1-k8j7h", Service: "other", Spans: 9, Cluster: "edge-1", LastSeen: now},
	}}
	idx := NewLiveIndex(newMemKV())
	idx.now = func() time.Time { return now }
	r := NewResolver(src, liveClusters{}).WithLive(idx)
	r.now = func() time.Time { return now }
	ctx := context.Background()
	set, _ := r.ServicePods(ctx, "web-api", []string{"c-1"}, time.Time{})
	if len(set.Pods) != 1 || set.Source != "mv" || set.Pods[0].Name != "web-6f7c9d8b4-x2k9p" || src.calls != 1 {
		t.Fatalf("dizin ıskası → tek MV servis okuması: %+v calls=%d", set, src.calls)
	}
	if set, _ := r.ServicePods(ctx, "missing", []string{"c-1"}, time.Time{}); len(set.Pods) != 0 {
		t.Fatalf("depoda olmayan servis → boş küme (çağıran 'pod bilinmiyor' der): %+v", set)
	}
	if set, _ := r.ServicePods(ctx, "web-api", nil, time.Time{}); len(set.Pods) != 0 {
		t.Fatalf("cluster yok → boş: %+v", set)
	}
}
