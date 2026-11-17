package copilot

import (
	"context"
	"testing"
)

// v0.10.2331 — sınıf tavanı: düşünmeyen modelde min(bütçe, tavan); düşünen
// modelde düşünme kapalı değilse kalkar; tavan yoksa aynen.
func TestAnswerCappedMaxTokens(t *testing.T) {
	ctx := WithAnswerCap(context.Background(), 384)
	cases := []struct {
		model, thinking string
		budget, want    int
	}{
		{"gemma4", "", 4096, 384},
		{"gemma4", "", 0, 384},
		{"gemma4", "", 200, 200},
		{"qwen3-8b", "", 4096, 4096},
		{"qwen3-8b", "on", 4096, 4096},
		{"qwen3-8b", "off", 4096, 384},
	}
	for _, c := range cases {
		if got := answerCappedMaxTokens(ctx, c.budget, c.model, c.thinking); got != c.want {
			t.Errorf("%s/%q/%d → %d, beklenen %d", c.model, c.thinking, c.budget, got, c.want)
		}
	}
	if got := answerCappedMaxTokens(context.Background(), 4096, "gemma4", ""); got != 4096 {
		t.Fatalf("tavansız ctx değişti: %d", got)
	}
	if WithAnswerCap(context.Background(), 0) != context.Background() {
		t.Fatal("n ≤ 0 ctx değiştirdi")
	}
}
