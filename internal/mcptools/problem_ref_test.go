package mcptools

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Görünen kimlik olmayan değer store'a dokunmadan aynen geçer (sıfır Deps).
func TestResolveProblemRefPassesThroughInternalIDs(t *testing.T) {
	for _, ref := range []string{"a1b2c3", "  a1b2c3 ", ""} {
		got, err := resolveProblemRef(context.Background(), Deps{}, ref)
		if err != nil || got != strings.TrimSpace(ref) {
			t.Errorf("resolveProblemRef(%q) = (%q, %v)", ref, got, err)
		}
	}
}

// Kaynak pini: problem_id alan her kardeş tool görünen kimliği çözer —
// yoksa "P-xxxxx" "henüz sentezlenmedi" diye yanlış cevap alır.
func TestProblemIDToolsResolveDisplayID(t *testing.T) {
	for file, want := range map[string]int{"tools.go": 1, "problem_tools.go": 2, "knowledge_tools.go": 1} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(b), "resolveProblemRef(ctx, d,"); n != want {
			t.Errorf("%s: resolveProblemRef çağrısı %d, want %d", file, n, want)
		}
	}
}
