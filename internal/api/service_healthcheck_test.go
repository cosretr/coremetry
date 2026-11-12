package api

// service_healthcheck_test.go — v0.10.2039 (servis sağlık kontrolü). Önbellek
// anahtarı TÜM girdileri hash'ler (v0.5.187 sınıfı): servis, env, pencere ve
// cluster kümesinin her biri ayrı anahtar üretir; cluster sırası anahtarı
// değiştirmez; aynı sayıda farklı cluster çakışmaz.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestHealthcheckKey(t *testing.T) {
	a := mcptools.ClusterRef{ID: "a", Name: "cluster-a", SpanValues: []string{"x", "y"}}
	b := mcptools.ClusterRef{ID: "b", Name: "cluster-b"}
	c := mcptools.ClusterRef{ID: "c", Name: "cluster-c"}
	base := healthcheckKey("orders-api", "", "15m", []mcptools.ClusterRef{a, b})
	distinct := map[string]string{
		"servis":                  healthcheckKey("billing-api", "", "15m", []mcptools.ClusterRef{a, b}),
		"env":                     healthcheckKey("orders-api", "prod", "15m", []mcptools.ClusterRef{a, b}),
		"pencere":                 healthcheckKey("orders-api", "", "1h", []mcptools.ClusterRef{a, b}),
		"aynı sayıda farklı küme": healthcheckKey("orders-api", "", "15m", []mcptools.ClusterRef{a, c}),
		"küme yok":                healthcheckKey("orders-api", "", "15m", nil),
		"span değeri":             healthcheckKey("orders-api", "", "15m", []mcptools.ClusterRef{{ID: "a", Name: "cluster-a", SpanValues: []string{"x"}}, b}),
	}
	for name, k := range distinct {
		if k == base {
			t.Fatalf("%s farklı anahtar üretmeli: %s", name, k)
		}
	}
	if got := healthcheckKey("orders-api", "", "15m", []mcptools.ClusterRef{b, a}); got != base {
		t.Fatalf("cluster sırası anahtarı değiştirmemeli: %s vs %s", got, base)
	}
	rev := mcptools.ClusterRef{ID: "a", Name: "cluster-a", SpanValues: []string{"y", "x"}}
	if got := healthcheckKey("orders-api", "", "15m", []mcptools.ClusterRef{rev, b}); got != base {
		t.Fatalf("span değeri sırası anahtarı değiştirmemeli")
	}
}
