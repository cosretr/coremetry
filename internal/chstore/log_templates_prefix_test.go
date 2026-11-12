package chstore

// v0.10.2040 — log_template_new ön ek ailesi: bilinen şablon okuması belirteç
// sayısı VEYA sabit baş ile (aynı biçimin farklı belirteç sayılı varyantları).

import (
	"strings"
	"testing"
)

func TestLogTemplatesWhereAnyPrefixes(t *testing.T) {
	w, args := logTemplatesWhere(ListLogTemplatesFilter{TokenCounts: []int{15}, AnyPrefixes: []string{"AuthContext(challengeCode="}})
	if !strings.Contains(w, "AND ((countSubstrings(template, ' ') + 1) IN (?) OR startsWith(template, ?))") {
		t.Fatalf("koşul: %s", w)
	}
	if len(args) != 2 || args[0] != 15 || args[1] != "AuthContext(challengeCode=" {
		t.Fatalf("argümanlar: %v", args)
	}
	w, args = logTemplatesWhere(ListLogTemplatesFilter{AnyPrefixes: []string{"a", "b"}})
	if !strings.Contains(w, "AND (startsWith(template, ?) OR startsWith(template, ?))") || len(args) != 2 {
		t.Fatalf("yalnız önek: %s %v", w, args)
	}
	many := make([]string, 30)
	for i := range many {
		many[i] = "p"
	}
	if _, args = logTemplatesWhere(ListLogTemplatesFilter{AnyPrefixes: many}); len(args) != 20 {
		t.Fatalf("önek tavanı 20, %d", len(args))
	}
	if w, _ = logTemplatesWhere(ListLogTemplatesFilter{TokenCounts: []int{3}}); !strings.Contains(w, "AND (countSubstrings(template, ' ') + 1) IN (?)") {
		t.Fatalf("tek koşul parantezsiz (eski biçim): %s", w)
	}
}
