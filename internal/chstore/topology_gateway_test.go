package chstore

import (
	"strings"
	"testing"
)

// v0.10.1151 — gatewayPathRegex SQL metnine gömülü: '?' clickhouse-go'da
// konumsal yer tutucu, '{' sunucu tarafı parametre tetikleyicisi. İkisi de
// INSERT'i düşürür (v0.10.1146 prod hatası).
func TestGatewayPathRegexIsBindSafe(t *testing.T) {
	if strings.ContainsAny(gatewayPathRegex, "?{}") {
		t.Fatalf("gatewayPathRegex SQL'e gömülü; '?', '{', '}' içeremez: %s", gatewayPathRegex)
	}
	if e := gatewayExtExpr("infra_host"); strings.ContainsAny(e, "?{}") {
		t.Fatalf("gatewayExtExpr bind-güvenli değil: %s", e)
	}
}
