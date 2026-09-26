package api

import (
	"os"
	"strings"
	"testing"
)

// TestGuidedInheritsScreenTrace — v0.10.944: çekmece (Explain bağlamı)
// açıkken ekrandaki trace "bu trace" sorusuna trace_by_id olarak
// devredilmez; global sohbette devredilir.
func TestGuidedInheritsScreenTrace(t *testing.T) {
	cases := []struct {
		trace, explain string
		want           bool
	}{
		{"4bf92f3577b34da6a3ce929d0e0e4736", "", true},
		{"4bf92f3577b34da6a3ce929d0e0e4736", "KONU: Explain trace · 4bf92f35\n...", false},
		{"4bf92f3577b34da6a3ce929d0e0e4736", "   ", true},
		{"", "", false},
	}
	for _, c := range cases {
		if got := guidedInheritsScreenTrace(c.trace, c.explain); got != c.want {
			t.Errorf("guidedInheritsScreenTrace(%q,%q)=%v want %v", c.trace, c.explain, got, c.want)
		}
	}
	// Her iki devralma dalı da kapıdan geçmeli (biri unutulursa çekmece
	// sorusu yine koparılır).
	b, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Count(src, "inheritTrace && ") < 2 || strings.Contains(src, "if ctxTrace != \"\" && hasDemonstrativeTrace(norm)") {
		t.Fatal("ekrandaki trace devralması guidedInheritsScreenTrace kapısından geçmiyor")
	}
}
