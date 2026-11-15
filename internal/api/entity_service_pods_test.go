package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// v0.10.2239 (Entity S4) — svc → pod seçicisi resolver komşularından: cluster
// başına tam adlar, boş küme = bilinmiyor (cluster geneli seçici yok).
func TestPodSelectorFrom(t *testing.T) {
	set := entity.ServicePodSet{Source: "live", Pods: []entity.ResolvedPod{
		{ID: "pod:c-1/shop/web-7d9f6c5b4-a1b2c", ClusterID: "c-1", Namespace: "shop", Name: "web-7d9f6c5b4-a1b2c"},
		{ID: "pod:c-1/shop/web-7d9f6c5b4-d3e4f", ClusterID: "c-1", Namespace: "shop", Name: "web-7d9f6c5b4-d3e4f"},
		{ID: "pod:c-2/shop/web-6c5b4a3d2-g5h6i", ClusterID: "c-2", Namespace: "shop", Name: "web-6c5b4a3d2-g5h6i"},
	}}
	ps := podSelectorFrom(set)
	if ps.Unknown() || ps.Pods != 3 || ps.Source != "live" {
		t.Fatalf("küme: %+v", ps)
	}
	if ps.ByCluster["c-1"] != `^(web-7d9f6c5b4-a1b2c|web-7d9f6c5b4-d3e4f)$` || ps.ByCluster["c-2"] != `^(web-6c5b4a3d2-g5h6i)$` {
		t.Fatalf("cluster başına tam ad seçicisi: %v", ps.ByCluster)
	}
	if !strings.Contains(ps.Union, "web-6c5b4a3d2-g5h6i") || strings.Contains(ps.Union, ".*") {
		t.Fatalf("birleşik seçici tam adlar, önek yok: %q", ps.Union)
	}
	empty := podSelectorFrom(entity.ServicePodSet{})
	if !empty.Unknown() || empty.Union != "" || len(empty.ByCluster) != 0 {
		t.Fatalf("boş küme → seçici yok: %+v", empty)
	}
	if out := renderThanosPodsTR(thanosPodsResult{Unknown: true}, "web-api"); !strings.Contains(out, "pod bilinmiyor") {
		t.Fatalf("bilinmiyor cümlesi: %q", out)
	}
	var nb mcptools.EntityNeighbours
	fillNeighbourPods(&nb, entity.ServicePodSet{})
	if nb.PodCount != 0 || nb.Note == "" {
		t.Fatalf("komşu yok notu: %+v", nb)
	}
	fillNeighbourPods(&nb, set)
	if nb.PodCount != 3 || len(nb.Pods) != 3 || nb.PodSource != "live" {
		t.Fatalf("komşu pod'lar: %+v", nb)
	}
}

// Ratchet: servis → pod ad öneki sezgisi (deployment | servis | ortam eksiz ad
// + "-.*") CoSRE / MCP / api'ye geri dönmez. internal/ml'in metrik Thanos
// yolu ayrı iş akışında (izinli tek çağıran).
func TestNoServicePodRegexOutsideML(t *testing.T) {
	banned := []string{"ServicePodRegex(", "thanosPodRegexFor(", "resPodOwnerService(", "thanosServicePodRegex("}
	_ = filepath.WalkDir("..", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "ml" || d.Name() == "thanos" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".go" || strings.HasSuffix(p, "entity_service_pods_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for _, s := range banned {
			if strings.Contains(string(b), s) {
				t.Errorf("%s: %q — servisin pod'ları entity.Resolver komşularından (servicePodSelector)", p, s)
			}
		}
		return nil
	})
}

// seedLivePods — test: etkin her cluster'ın canlı dizinine servisin pod'ları
// (s.cache bir *memCache olmalı).
func seedLivePods(t *testing.T, s *Server, ns, svc string, pods ...string) {
	t.Helper()
	now := time.Now()
	for _, cid := range s.entityClusterIDs() {
		ents := map[string]entity.Entity{}
		var spans []entity.SeenRow
		for _, p := range pods {
			id := entity.PodID(cid, ns, p)
			ents[id] = entity.Entity{Type: entity.TypePod, ClusterID: cid, ID: id, Namespace: ns, Name: p, Source: entity.SourceThanos}
			spans = append(spans, entity.SeenRow{Namespace: ns, Pod: p, Service: svc, Spans: 5, LastSeen: now})
		}
		b, err := entity.EncodeLive(entity.BuildLiveSnapshot(cid, now, ents, nil, spans))
		if err != nil {
			t.Fatal(err)
		}
		s.cache.(*memCache).m[entity.LiveKey(cid)] = b
	}
}
