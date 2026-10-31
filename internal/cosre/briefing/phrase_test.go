package briefing

import (
	"strings"
	"testing"
)

func TestNeedsLLMAndAcceptHeadline(t *testing.T) {
	mk := func(n int) Briefing {
		var fs []Finding
		for i := 0; i < n; i++ {
			fs = append(fs, Finding{Item: ItemRED, Service: "orders-api", Label: string(rune('a' + i)), Text: "x"})
		}
		return Assemble(AssembleInput{Team: "acme", WindowS: 10800, Services: []string{"orders-api"}, Findings: fs})
	}
	if NeedsLLM(mk(2), true, true) || !NeedsLLM(mk(3), true, true) || NeedsLLM(mk(5), false, true) || NeedsLLM(mk(5), true, false) {
		t.Fatal("LLM kapısı: ≤2 bulgu şablon, ayar/model kapalıysa deterministik")
	}
	for in, ok := range map[string]bool{
		"orders-api'de hata artışı ve açık bir P1 var.":                           false, // rakam (P1) → red
		"orders-api'de hata artışı var, payments-core'da yeni exception görüldü.": true,
		"":                       false,
		"- madde":                false,
		"**kalın** başlık":       false,
		strings.Repeat("a", 300): false,
	} {
		if _, got := AcceptHeadline(in); got != ok {
			t.Errorf("%q → %v, want %v", in, got, ok)
		}
	}
}

func TestCompactInputBounded(t *testing.T) {
	var fs []Finding
	for _, it := range ItemOrder {
		for i := 0; i < 3; i++ {
			fs = append(fs, Finding{Item: it, Service: strings.Repeat("s", 80), Label: strings.Repeat("l", 200), Kind: "k", Now: 1.23456, Text: strings.Repeat("t", 500), PageURL: "/x"})
		}
	}
	raw := CompactInput(Assemble(AssembleInput{Team: "acme", WindowS: 10800, Services: []string{"a"}, Findings: fs}))
	if len(raw) > LLMMaxInputBytes || strings.Contains(string(raw), "ttttt") || strings.Contains(string(raw), "/x") || !strings.Contains(string(raw), `"now":1.23`) {
		t.Fatalf("%d B", len(raw))
	}
}
