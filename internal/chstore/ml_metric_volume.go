package chstore

// ml_metric_volume.go — metrik ailesi hacim kapısı (operatör hatası
// 2026-11-21): seyrek çağrılan HTTP client'ın p95'i bir avuç isteğin p95'i =
// tek yavaş çağrı; oran serisi (5xx) aynı zayıflıkta. Quantile / oran serisi
// kovada en az MLMetricMinVolume çağrı görmezse kova yazılmaz ve
// yargılanmaz (internal/ml/metric_volume_gate.go).

import (
	"regexp"
	"strings"
)

// MLMetricMinVolume — kova penceresinde en az çağrı. Problem standardının G1
// trafik kapısıyla AYNI sayı (StdMinCalls5m = 30, 5 dk kova): 30 çağrının
// p95'i en kötü ~2 çağrıdır, tek yavaş çağrı p95'i tek başına sürükleyemez;
// 5xx oranında tek hata ≤ %3.3. Kod sabiti, ayar değil (K2).
const MLMetricMinVolume = StdMinCalls5m

const httpClientVolumeExpr = "sum by ({svc}) (increase(http_client_request_duration_seconds_count[{win}]))"

// bucketMetricRe — histogram_quantile içindeki `<ad>_bucket{seçici}[{win}]`.
var bucketMetricRe = regexp.MustCompile(`([A-Za-z_:][A-Za-z0-9_:]*)_bucket(\{[^}]*\})?\[\{win\}\]`)

// leSelectorRe — seçicide `le` eşleşmesi: _count'ta `le` yok, türetme yapılmaz.
var leSelectorRe = regexp.MustCompile(`[{,]\s*le\s*[=!]`)

// MLMetricVolumeExpr — SAF: serinin hacim ifadesi ("" = kapı yok).
// Sıra: açık VolumeExpr → ifadesi varsayılanla birebir aynı seri (operatörün
// kaydettiği eski blob'da alan yok) → histogram_quantile ifadesinden türetme
// (tek bir `_bucket` metriği, seçicisi taşınır). Oran ifadesi genel olarak
// türetilmez (pay/payda yapısı güvenle okunamaz) — kapısız kalır.
func MLMetricVolumeExpr(family string, e MLMetricExpr) string {
	if v := strings.TrimSpace(e.VolumeExpr); v != "" {
		return v
	}
	for _, d := range DefaultMLMetricFamilies()[family] {
		if d.ID == e.ID && d.Expr == e.Expr {
			return d.VolumeExpr
		}
	}
	return deriveQuantileVolumeExpr(e.Expr)
}

// deriveQuantileVolumeExpr — SAF: histogram_quantile(... X_bucket{sel}[{win}] ...)
// → sum by ({svc}) (increase(X_count{sel}[{win}])). Birden çok farklı
// bucket metriği, seçicide `le`, ya da {svc} yoksa "".
func deriveQuantileVolumeExpr(expr string) string {
	if !strings.Contains(expr, "histogram_quantile(") || !strings.Contains(expr, "{svc}") {
		return ""
	}
	ms := bucketMetricRe.FindAllStringSubmatch(expr, -1)
	if len(ms) == 0 {
		return ""
	}
	name, sel := ms[0][1], ms[0][2]
	for _, m := range ms[1:] {
		if m[1] != name || m[2] != sel {
			return ""
		}
	}
	if leSelectorRe.MatchString(sel) {
		return ""
	}
	return "sum by ({svc}) (increase(" + name + "_count" + sel + "[{win}]))"
}
