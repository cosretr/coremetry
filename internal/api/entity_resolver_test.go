package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// v0.10.2193 (Entity S0) — /pods satırları resolver yolundan: servis her
// zaman resolver'ın (eşleşme yoksa boş), node yalnız Thanos boşsa.
func TestFillPodsFromPaths(t *testing.T) {
	rows := []thanos.PodRow{
		{Namespace: "digital", Pod: "dash-a"},
		{Namespace: "digital", Pod: "kept", Node: "ksm-node"},
		{Namespace: "digital", Pod: "none"},
	}
	ids := []string{"pod:c-1/digital/dash-a", "pod:c-1/digital/kept", "pod:c-1/digital/none"}
	paths := map[string]entity.Path{
		ids[0]: {Service: "svc:bsa-digital-dashboard-prod", Node: "node:c-1/w-3"},
		ids[1]: {Service: "svc:kept-svc", Node: "node:c-1/span-node"},
	}
	fillPodsFromPaths(rows, ids, paths)
	if rows[0].Service != "bsa-digital-dashboard-prod" || rows[0].Node != "w-3" {
		t.Errorf("row0 %+v", rows[0])
	}
	if rows[1].Service != "kept-svc" || rows[1].Node != "ksm-node" {
		t.Errorf("thanos node ezildi: %+v", rows[1])
	}
	if rows[2].Service != "" || rows[2].Node != "" {
		t.Errorf("bilinmeyen boş kalmalı: %+v", rows[2])
	}
}

// Ratchet (Entity S0): Clusters'ın sayfaya özel pod↔servis join'leri geri
// gelmez — bağlar entity.Resolver'dan geçer.
func TestNoAdHocPodServiceJoin(t *testing.T) {
	banned := []string{"PodServiceMap(", "pickPodService(", "podServiceMapSQL", "function workloadService("}
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
			if ext != ".go" && ext != ".ts" && ext != ".tsx" {
				return nil
			}
			if strings.HasSuffix(p, "entity_resolver_test.go") {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			for _, s := range banned {
				if strings.Contains(string(b), s) {
					t.Errorf("%s: %q — pod↔servis bağı entity.Resolver'dan geçmeli", p, s)
				}
			}
			return nil
		})
	}
}
