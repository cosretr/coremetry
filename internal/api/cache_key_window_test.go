package api

import (
	"testing"
	"time"
)

// v0.10.860 (scale-audit) — üç soğuk anahtar kovalandı: aynı kova içindeki iki
// `to` aynı anahtarı, kova dışı farklı anahtarı üretir; içerik girdileri
// (servis/limit/q/since) anahtarda kalır (v0.5.187 sınıfı: hiçbir girdi düşmez).
func TestWindowedCacheKeysBucketTime(t *testing.T) {
	base := time.Date(2026, 9, 23, 10, 0, 1, 0, time.UTC)
	from := base.Add(-15 * time.Minute)

	// trace-shapes: 30 s kova
	if traceShapesKey(from, base, "svc", 30) != traceShapesKey(from, base.Add(5*time.Second), "svc", 30) {
		t.Fatal("shapes: aynı 30 s kovası farklı anahtar üretti")
	}
	if traceShapesKey(from, base, "svc", 30) == traceShapesKey(from, base.Add(31*time.Second), "svc", 30) {
		t.Fatal("shapes: kova dışı aynı anahtar")
	}
	if traceShapesKey(from, base, "a", 30) == traceShapesKey(from, base, "b", 30) || traceShapesKey(from, base, "a", 30) == traceShapesKey(from, base, "a", 31) {
		t.Fatal("shapes: servis/limit anahtardan düştü")
	}

	// service-deploys: 5 dk kova
	if serviceDeploysKey("s", from, base) != serviceDeploysKey("s", from, base.Add(3*time.Minute)) {
		t.Fatal("deploys: aynı 5 dk kovası farklı anahtar üretti")
	}
	if serviceDeploysKey("s", from, base) == serviceDeploysKey("s", from, base.Add(5*time.Minute)) {
		t.Fatal("deploys: kova dışı aynı anahtar")
	}

	// attr-values: since kipi (sıfır from/to) ve pencere kipi (grid)
	k0 := attrValuesKey("http.route", "1h", time.Time{}, time.Time{}, 200, "")
	if k0 != attrValuesKey("http.route", "1h", time.Time{}, time.Time{}, 200, "") || k0 == attrValuesKey("http.route", "6h", time.Time{}, time.Time{}, 200, "") {
		t.Fatalf("attr-values since kipi: %s", k0)
	}
	if attrValuesKey("k", "", from, base, 200, "q") != attrValuesKey("k", "", from, base.Add(cacheRawQueryGrid/2), 200, "q") {
		t.Fatal("attr-values: aynı grid farklı anahtar üretti")
	}
	if attrValuesKey("k", "", from, base, 200, "q") == attrValuesKey("k", "", from, base.Add(cacheRawQueryGrid+time.Second), 200, "q") {
		t.Fatal("attr-values: grid dışı aynı anahtar")
	}
	if attrValuesKey("k", "", from, base, 200, "a") == attrValuesKey("k", "", from, base, 200, "b") || attrValuesKey("k", "", from, base, 200, "a") == attrValuesKey("k", "", from, base, 100, "a") {
		t.Fatal("attr-values: q/limit anahtardan düştü")
	}
}
