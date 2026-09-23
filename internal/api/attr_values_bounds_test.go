package api

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// v0.10.869 (scale-audit 09-23) — attr-values'ın q'lu dalı: ILIKE iç taramanın
// WHERE'inde ve tarama süzgeçten SONRA attrValuesSampleRows ile örneklenir
// (uzun kuyruk ulaşılır, okuma sınırlı). Her iki dalda da iç LIMIT var.
func TestAttributeValuesBothBranchesSampleBounded(t *testing.T) {
	b, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *Server) getAttributeValues(")
	j := strings.Index(src[i+1:], "\nfunc ")
	body := src[i : i+1+j]
	if n := strings.Count(body, "LIMIT %d"); n != 2 {
		t.Fatalf("iç örnek LIMIT iki dalda da olmalı, %d bulundu", n)
	}
	if !regexp.MustCompile(`(?s)ILIKE \?\s+LIMIT %d\s+\)`).MatchString(body) {
		t.Fatal("q'lu dalda ILIKE iç taramada ve LIMIT'ten ÖNCE olmalı (önce süz, sonra örnekle)")
	}
	if strings.Contains(body, "WHERE v != '' AND v ILIKE ?") {
		t.Fatal("ILIKE dış sorguya geri döndü — iç tarama yine sınırsız")
	}
}
