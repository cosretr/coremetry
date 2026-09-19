package evaluator

import (
	"os"
	"strings"
	"testing"
)

// v0.10.800 — SLO burn-rate problemleri yaş merdiveninden muaf VE tazeleme
// dalı şiddeti politikaya sabitler. İki kelepçe birlikte: yalnız süpürme
// muafiyeti olsaydı eski critical satırlar bandına dönmezdi; yalnız tazeleme
// olsaydı süpürme her tikte yeniden yükseltip bildirim üretirdi.
func TestSLOBurnRefreshPinsSeverityToPolicy(t *testing.T) {
	src, err := os.ReadFile("slo_burn.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "case breached && hasOpen:")
	if i < 0 {
		t.Fatal("tazeleme dalı yok")
	}
	tail := s[i:min(i+400, len(s))]
	if !strings.Contains(tail, "open.Severity = pol.Severity") {
		t.Error("tazeleme dalı şiddeti politikadan yazmalı (open.Severity = pol.Severity)")
	}
	if !escalationExempt("slo:x:warning") {
		t.Error("slo: öneki escalationExempt'te muaf olmalı")
	}
}
