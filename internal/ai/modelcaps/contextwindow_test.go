package modelcaps

import "testing"

// v0.10.1136 — wiki bağlam bütçesinin model penceresi (bilinmiyorsa 0).
// Tutucu: yalnız adı açıkça uzun bağlam söyleyen modeller büyük pencere alır.
func TestContextWindow(t *testing.T) {
	cases := map[string]int{
		"":                        0,
		"my-private-model":        0,
		"gemma3:27b":              131072,
		"google/gemma-3-27b-it":   131072,
		"gemma-4-31b-it":          131072,
		"gemma3:4b":               8192, // küçük varyant
		"gemma3":                  8192, // belirsiz
		"google/gemma-2-27b-it":   8192,
		"gemma:7b":                8192,
		"Qwen/Qwen3-32B":          32768,
		"qwen2.5-coder:14b":       32768,
		"qwen:7b":                 8192,
		"llama3.1:70b":            131072,
		"meta-llama/Llama-3-8B":   8192,
		"mistral:7b":              8192,
		"mistral-small":           8192,
		"mistral-nemo-instruct":   32768,
		"mistral-7b-instruct-32k": 32768,
		"some-model-128k":         131072,
		"deepseek-r1:32b":         32768,
		"claude-sonnet-5":         200000,
		"gpt-4o-mini":             128000,
	}
	for m, want := range cases {
		if got := ContextWindow(m); got != want {
			t.Errorf("ContextWindow(%q)=%d want %d", m, got, want)
		}
	}
}
