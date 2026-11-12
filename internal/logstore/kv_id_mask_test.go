package logstore

// v0.10.2040 — Operator-reported: "Logları aç" sorgusu `anahtar=değer`
// dökümündeki rastgele kimliği (sabit sanılan) taşıdığı için yalnız birkaç
// satır buluyordu. Maske kimliği değişken sayınca sorgu satırın SABİT başını
// arar. Fikstür nötr.

import (
	"strings"
	"testing"
)

func TestPatternSearchQueryKeyValueDump(t *testing.T) {
	line := "AuthContext(challengeCode=482913, tabId=eISAG_fy28U, channel=MOBILE, status=null, imageType=SECURITY_IMAGE)"
	tpl := MaskVariableTokens(line, "<*>")
	q := PatternSearchQuery(tpl)
	if strings.Contains(q, "eISAG_fy28U") || strings.Contains(tpl, "eISAG_fy28U") {
		t.Fatalf("kimlik sorguda/şablonda: %q / %q", q, tpl)
	}
	if !strings.HasPrefix(q, `"AuthContext(challengeCode"`) {
		t.Fatalf("sorgu sabit başla başlamalı: %q", q)
	}
	if !strings.Contains(q, `imageType=SECURITY_IMAGE"`) {
		t.Fatalf("sabitler sorguda kalmalı: %q", q)
	}
}

func TestIsRandomIDValue(t *testing.T) {
	for v, want := range map[string]bool{
		"eISAG_fy28U": true, "JVNQbZ54lgk": true, "bZNnzw9iSFk": true, "z8CscvEYOzo": true,
		"SECURITY_IMAGE": false, "LoginStatusResponse": false, "OrderServiceImpl": false, "HTTPServer": false,
		"Log4j2Plugins": false, "inProgress": false, "ACCEPTED": false, "abc12": false, "utf8mb4": false,
		"release-2024": false, "1234567890": false, "sha256sum": false,
	} {
		if got := isRandomIDValue(v); got != want {
			t.Errorf("%s → %v, istenen %v", v, got, want)
		}
	}
}
