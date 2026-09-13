package chstore

import (
	"testing"
	"time"
)

// v0.10.717 — rollout analizi cluster başına: iki cluster'ın kademeli
// çıkışı iki satır (her biri kendi cluster damgası ve pod sayısıyla),
// zaman sırasıyla birleşik; sürüm sabitliği/kimlik izleme birleşimi.
func TestAnalyzeRolloutsByCluster(t *testing.T) {
	t0 := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	b := func(min int, ver string, pods ...string) rolloutBucket {
		return rolloutBucket{t: t0.Add(time.Duration(min) * time.Minute), pods: pods, version: ver}
	}
	byCl := map[string][]rolloutBucket{
		// Dört kova: varlık yumuşatması [i-1,i] (v0.8.405) kesimin bir kova
		// gecikmeli görünmesine yol açar; tek geçiş kovası rollout saymaz.
		"prod-eu": {b(0, "v1", "e1", "e2"), b(5, "v1", "e1", "e2"), b(10, "v2", "e3", "e4"), b(15, "v2", "e3", "e4")},
		"prod-us": {b(0, "v1", "u1"), b(5, "v1", "u1"), b(30, "v2", "u2"), b(35, "v2", "u2")},
	}
	res := analyzeRolloutsByCluster("svc", []string{"prod-eu", "prod-us"}, byCl)
	if len(res.Rollouts) != 2 {
		t.Fatalf("iki cluster iki rollout: %+v", res.Rollouts)
	}
	if res.Rollouts[0].Cluster != "prod-eu" || res.Rollouts[1].Cluster != "prod-us" {
		t.Fatalf("zaman sırası + cluster damgası: %+v", res.Rollouts)
	}
	if res.Rollouts[0].PodsRemoved != 2 || res.Rollouts[1].PodsRemoved != 1 {
		t.Fatalf("pod sayıları cluster'a özgü: %+v", res.Rollouts)
	}
	if res.VersionConstant || !res.InstancesTracked {
		t.Fatalf("birleşim bayrakları: %+v", res)
	}
	empty := analyzeRolloutsByCluster("svc", nil, nil)
	if empty == nil || len(empty.Rollouts) != 0 {
		t.Fatal("boş girdi → boş sonuç")
	}
}
