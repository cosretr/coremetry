package chstore

import (
	"strings"
	"testing"
)

// v0.10.2209 — Endpoints başlık hunisi: metot + durum sınıfı ham yola zorlar
// ve sidecar'ın kolonlarıyla (http_method, http_status) süzer.
func TestEndpointsRawFilters_MethodStatus(t *testing.T) {
	s := &Store{}
	q := EndpointsQuery{Method: "POST", StatusClass: "5xx"}
	if !q.forcesRaw() {
		t.Fatal("metot / durum MV'de yok → ham yol")
	}
	sql, args := s.endpointsRawFilters(q, "http_route")
	if !strings.Contains(sql, " AND http_method = ?") || !strings.Contains(sql, " AND intDiv(http_status, 100) = ?") {
		t.Fatalf("conjunct eksik: %q", sql)
	}
	if len(args) != 2 || args[0] != "POST" || args[1] != 5 {
		t.Fatalf("args: %#v", args)
	}
	if statusClassDigit("6xx") != 0 || statusClassDigit("2xx") != 2 || statusClassDigit("") != 0 {
		t.Fatal("statusClassDigit")
	}
	if (EndpointsQuery{}).forcesRaw() {
		t.Fatal("süzgeçsiz sorgu MV'de kalır")
	}
}
