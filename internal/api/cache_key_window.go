package api

import (
	"fmt"
	"time"
)

// cache_key_window.go — v0.10.860 (scale-audit 2026-09-23, kontrol 4): üç
// anahtar ham UnixNano / ham sorgu dizesi taşıyordu; parametresiz çağrıda
// `to = time.Now()` (ya da SPA'nın to=Date.now()'u) her istekte yeni anahtar
// → TTL hiç işlemez (SOĞUK anahtar; zehirlenme değil — girdiler tam).
// Kardeşleri cacheBucket (30 s) / deployWindowBucket (5 dk) /
// cacheRawQueryGrid'e oturuyordu. Saf yardımcılar burada: api.go büyümez,
// kova davranışı tablo-testli (cache_key_window_test.go). Önek sürümü v2:
// eski şekilli anahtarlarla çakışmaz.

// traceShapesKey — GET /api/traces/shapes (shapes.go). 30 s kova.
func traceShapesKey(from, to time.Time, service string, limit int) string {
	return fmt.Sprintf("trace-shapes:v2:w=%s:svc=%s:lim=%d", cacheBucket(from, to), service, limit)
}

// serviceDeploysKey — GET /api/services/{name}/deploys (api.go). Kardeşi
// svc-deploys:v1 ile aynı 5 dk kova (deployWindowBucket).
func serviceDeploysKey(service string, from, to time.Time) string {
	return fmt.Sprintf("service-deploys:v2:svc=%s:w=%s", service, deployWindowBucket(from, to))
}

// attrValuesKey — GET /api/attributes/values (api.go). from/to sıfırsa
// (since kipi) 0 yazılır; doluysa attr-keys ile AYNI cacheRawQueryGrid'e
// oturur ("SPA to=Date.now() her poll MISS etmesin", v0.10.256).
func attrValuesKey(rawKey, since string, from, to time.Time, limit int, pattern string) string {
	var f, t int64
	if !from.IsZero() {
		f = from.Truncate(cacheRawQueryGrid).UnixNano()
	}
	if !to.IsZero() {
		t = to.Truncate(cacheRawQueryGrid).UnixNano()
	}
	return fmt.Sprintf("attr-values:v2:%s:since=%s:from=%d:to=%d:limit=%d:q=%s", rawKey, since, f, t, limit, pattern)
}
