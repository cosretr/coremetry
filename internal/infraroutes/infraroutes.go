// Package infraroutes — platform-tesisat rotaları (sağlık / hazırlık yoklaması,
// Prometheus scrape'i, keepalive) için TEK parça listesi ve eşleyicileri.
//
// v0.10.2434 (operatör onaylı, RED metrik-önce): metrik (VM) RED'inin rota
// serileri bu rotaları VARSAYILAN olarak dışlar. Prod örneği: bir servisin
// "/<APP>/actuator/prometheus" rotası 3.4 req/s toplamın 1.7 req/s'ini
// taşıyordu — scrape trafiği throughput'u ikiye katlıyordu. Liste önceden
// yalnız topoloji kenar süzgecindeydi (api/topology.go looksLikeInfraEdge);
// buraya taşındı, kopya yok. Eşleşme büyük/küçük harf duyarsız ALT-DİZE:
// önekli yollar (/APP/actuator/health) da eşleşir. Yanlış pozitif riski için
// liste dar tutulur; frontend kopyası lib/infraRoutes.ts (Go pin testi).
package infraroutes

import (
	"regexp"
	"strings"
)

// Fragments — parça listesi (sıra = regex alternasyon sırası). PİNLİ.
var Fragments = []string{
	"/health", "/healthz", "/healthcheck", "/livez", "/readyz",
	"/metrics", "/-/metrics", "/prometheus", "/actuator/prometheus",
	"/actuator/health", "/actuator/info",
	"/ping", "/heartbeat", "/keepalive",
	"/.well-known/", "/-/ready", "/-/healthy",
}

// Match — SAF: rota/op adı bir tesisat parçası içeriyor mu (harf duyarsız).
func Match(s string) bool {
	lower := strings.ToLower(s)
	for _, f := range Fragments {
		if strings.Contains(lower, f) {
			return true
		}
	}
	return false
}

// RegexBody — SAF: harf duyarsız alternasyon gövdesi `(?i:a|b|…)` (RE2;
// VictoriaMetrics ve Go regexp aynı sözdizimi). Parçalar QuoteMeta ile kaçışlı.
func RegexBody() string {
	parts := make([]string, len(Fragments))
	for i, f := range Fragments {
		parts[i] = regexp.QuoteMeta(f)
	}
	return "(?i:" + strings.Join(parts, "|") + ")"
}

// RouteRegex — SAF: tam-çapalı PromQL eşleyicisinin regex'i (`.*(?:gövde).*`) —
// `route!~"<bu>"` tesisat rotalarını düşürür; etiketi OLMAYAN seri (boş değer)
// eşleşmez, yani "(rota etiketi yok)" serisi korunur.
func RouteRegex() string { return ".*(?:" + RegexBody() + ").*" }
