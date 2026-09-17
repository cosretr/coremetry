package chstore

// trace_root_route_test.go — v0.10.756: liste satırı kök span'ın http_route'unu
// taşır (root_route) — ham yol anyIf, MV yolu entry_route_state (v0.8.52'den
// beri MV'de; ŞEMA DEĞİŞMEDİ). İki SELECT ve iki tarayıcı aynı sırada
// (root_svc'den hemen sonra); biri kayarsa kolonlar sessizce kayar.

import (
	"os"
	"strings"
	"testing"
)

func TestRawListSelectCarriesRootRoute(t *testing.T) {
	q := buildGetTracesListSQL("WHERE 1", "", "trace_start", "DESC")
	i, j, k := strings.Index(q, "AS root_svc"), strings.Index(q, "AS root_route"), strings.Index(q, "AS trace_start")
	if i < 0 || j < 0 || k < 0 || !(i < j && j < k) {
		t.Fatalf("root_route root_svc'den sonra, trace_start'tan önce olmalı (svc=%d route=%d start=%d)", i, j, k)
	}
	if !strings.Contains(q, "anyIf(http_route, (parent_id = '' OR parent_id = '0000000000000000') AND name != '')") {
		t.Fatalf("ham root_route kök yüklemiyle anyIf olmalı:\n%s", q)
	}
}

func TestRootRouteScannersAligned(t *testing.T) {
	for _, path := range []string{"trace_raw_probe.go", "trace_slice.go"} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "&t.RootName, &t.ServiceName, &t.RootRoute, &ts") {
			t.Errorf("%s: tarayıcı RootRoute'u ServiceName'den hemen sonra okumalı", path)
		}
	}
	repo, _ := os.ReadFile("repo.go")
	if !strings.Contains(string(repo), "argMaxIfMerge(entry_route_state)                            AS root_route,") {
		t.Error("MV 2. aşama SELECT'i entry_route_state'i root_route olarak vermeli")
	}
}
