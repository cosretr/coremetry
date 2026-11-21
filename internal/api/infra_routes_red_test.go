package api

import (
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/infraroutes"
)

// TestInfraRouteFragmentsMatchFrontend — v0.10.2434: lib/infraRoutes.ts parça
// listesi Go infraroutes.Fragments ile BAYT BAYT (sıra dahil) eşli.
func TestInfraRouteFragmentsMatchFrontend(t *testing.T) {
	src := readRepoFile(t, "../../frontend/src/lib/infraRoutes.ts")
	m := regexp.MustCompile(`(?s)INFRA_ROUTE_FRAGMENTS: readonly string\[\] = \[(.*?)\];`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("INFRA_ROUTE_FRAGMENTS bulunamadı")
	}
	var fe []string
	for _, q := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(m[1], -1) {
		fe = append(fe, q[1])
	}
	if strings.Join(fe, "|") != strings.Join(infraroutes.Fragments, "|") {
		t.Errorf("FE %v ≠ Go %v", fe, infraroutes.Fragments)
	}
}

// TestInfraRouteFilterOnHTTPFamiliesOnly — varsayılan dışlama HTTP türü
// ailelerde (rota anahtarına !~), gRPC'de ve ?infra=1'de yok.
func TestInfraRouteFilterOnHTTPFamiliesOnly(t *testing.T) {
	for _, f := range redFamilies {
		got := infraRouteFilter(f, false)
		if f.Kind == redKindHTTP {
			if got == nil || got.Key != f.RouteKey || got.Op != "!~" || got.Values[0] != infraroutes.RouteRegex() {
				t.Errorf("%s: %+v", f.ID, got)
			}
		} else if got != nil {
			t.Errorf("%s: gRPC'de süzgeç olmamalı", f.ID)
		}
		if infraRouteFilter(f, true) != nil {
			t.Errorf("%s: infra=1 süzgeci kaldırmalı", f.ID)
		}
	}
	// Services listesi: HTTP sorguları rota süzgecini taşır, gRPC taşımaz.
	http, _ := redFamilyByID(redFamilyHTTP)
	c, p, a := servicesREDMetricQueries(http, "service_name", []string{"orders-api"}, "", 3600, false)
	for _, q := range []string{c, p, a} {
		if !strings.Contains(q, `http_route!~".*(?:(?i:/health|`) {
			t.Errorf("HTTP sorgusu tesisat süzgeci taşımalı: %s", q)
		}
	}
	if c2, _, _ := servicesREDMetricQueries(http, "service_name", []string{"orders-api"}, "", 3600, true); strings.Contains(c2, "http_route!~") {
		t.Errorf("infra=1: süzgeç yok: %s", c2)
	}
	g, _ := redFamilyByID(redFamilyGRPCLegacy)
	if c3, _, _ := servicesREDMetricQueries(g, "service_name", []string{"ledger-grpc"}, "", 3600, false); strings.Contains(c3, "!~") {
		t.Errorf("gRPC: süzgeç yok: %s", c3)
	}
}
