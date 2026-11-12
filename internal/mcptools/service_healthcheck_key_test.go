package mcptools

// service_healthcheck_key_test.go — v0.10.2048 (anahtar api'den taşındı;
// ilk sürüm v0.10.2039). Önbellek anahtarı TÜM girdileri hash'ler (v0.5.187
// sınıfı): servis, env, pencere ve cluster kümesinin her biri ayrı anahtar
// üretir; cluster / span değeri sırası anahtarı değiştirmez; aynı sayıda
// farklı cluster çakışmaz; pencere normalize edilir (boş = 15m).

import (
	"context"
	"encoding/json"
	"testing"
)

// TestSweepToolChecksAll — v0.10.2048: service_health_sweep checks:"all"
// sağlık kontrolünü koşar (yeni araç yok); okuyucusuz Deps'te her kontrol
// Ölçülemedi, hiçbiri İyi değil.
func TestSweepToolChecksAll(t *testing.T) {
	tool := serviceHealthSweepTool(Deps{})
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"service":"orders-api","checks":"all"}`))
	if err != nil {
		t.Fatal(err)
	}
	res, ok := out.(HealthcheckResult)
	if !ok {
		t.Fatalf("sonuç HealthcheckResult değil: %T", out)
	}
	if res.Service != "orders-api" || res.Window != "15m" || len(res.Rows) == 0 || res.Verdict != HCUnknown {
		t.Fatalf("sonuç: %+v", res)
	}
	for _, r := range res.Rows {
		if r.State == HCOK {
			t.Fatalf("okuyucusuz koşuda İyi satır olamaz: %+v", r)
		}
	}
}

func TestHealthcheckCacheKey(t *testing.T) {
	a := ClusterRef{ID: "a", Name: "cluster-a", SpanValues: []string{"x", "y"}}
	b := ClusterRef{ID: "b", Name: "cluster-b"}
	c := ClusterRef{ID: "c", Name: "cluster-c"}
	base := HealthcheckCacheKey("orders-api", "", "15m", []ClusterRef{a, b})
	distinct := map[string]string{
		"servis":                  HealthcheckCacheKey("billing-api", "", "15m", []ClusterRef{a, b}),
		"env":                     HealthcheckCacheKey("orders-api", "prod", "15m", []ClusterRef{a, b}),
		"pencere":                 HealthcheckCacheKey("orders-api", "", "1h", []ClusterRef{a, b}),
		"aynı sayıda farklı küme": HealthcheckCacheKey("orders-api", "", "15m", []ClusterRef{a, c}),
		"küme yok":                HealthcheckCacheKey("orders-api", "", "15m", nil),
		"span değeri":             HealthcheckCacheKey("orders-api", "", "15m", []ClusterRef{{ID: "a", Name: "cluster-a", SpanValues: []string{"x"}}, b}),
	}
	for name, k := range distinct {
		if k == base {
			t.Fatalf("%s farklı anahtar üretmeli: %s", name, k)
		}
	}
	if got := HealthcheckCacheKey("orders-api", "", "15m", []ClusterRef{b, a}); got != base {
		t.Fatalf("cluster sırası anahtarı değiştirmemeli")
	}
	rev := ClusterRef{ID: "a", Name: "cluster-a", SpanValues: []string{"y", "x"}}
	if got := HealthcheckCacheKey("orders-api", "", "15m", []ClusterRef{rev, b}); got != base {
		t.Fatalf("span değeri sırası anahtarı değiştirmemeli")
	}
	if HealthcheckCacheKey("orders-api", "", "", []ClusterRef{a, b}) != base {
		t.Fatalf("boş pencere 15m ile aynı anahtar olmalı (normalize)")
	}
}
