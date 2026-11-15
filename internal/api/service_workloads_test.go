package api

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// v0.10.2232 (Entity S2) — servis → iş yükü satırları + ad hoc iş yükü
// join'i ratchet'i (S0 TestNoAdHocPodServiceJoin emsali).

func TestServiceWorkloadsFrom(t *testing.T) {
	name := func(cid string) string { return map[string]string{"c-1": "east"}[cid] }
	got := serviceWorkloadsFrom([]string{
		"wl:c-1/shop/Deployment/web", "svc:web", "wl:c-2/shop/StatefulSet/db", "wl:c-1/shop/Deployment/web", "bozuk",
	}, name)
	want := []serviceWorkload{
		{ID: "wl:c-1/shop/Deployment/web", ClusterID: "c-1", Cluster: "east", Namespace: "shop", Kind: "Deployment", Name: "web"},
		{ID: "wl:c-2/shop/StatefulSet/db", ClusterID: "c-2", Namespace: "shop", Kind: "StatefulSet", Name: "db"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestNoAdHocWorkloadJoin — iş yükü kimliği ve iş yükü → servis bağı
// entity modelinden geçer: pod adı sezgiselinin aynaları, Rollouts'un kendi
// eşlemesi ve ad aramalı wl: yedeği geri gelmez.
func TestNoAdHocWorkloadJoin(t *testing.T) {
	banned := []string{
		"workloadEntityFallback", "RolloutWorkloadServicesBatch", "rolloutWorkloadServicesBatchSQL",
		"stripPodSuffixes(", "DerivedWorkload(", "podWorkloadName(", "dominantWorkload(", "workloadMatchesService(",
	}
	for _, root := range []string{"..", "../../frontend/src"} {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(p)
			if (ext != ".go" && ext != ".ts" && ext != ".tsx") || strings.HasSuffix(p, "service_workloads_test.go") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			for _, s := range banned {
				if strings.Contains(string(b), s) {
					t.Errorf("%s: %q — iş yükü kimliği/servisi entity modelinden (wlkind, serves, same_as) geçmeli", p, s)
				}
			}
			return nil
		})
	}
}
