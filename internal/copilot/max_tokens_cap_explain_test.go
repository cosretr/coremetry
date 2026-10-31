package copilot

// max_tokens_cap_explain_test.go — v0.10.1699: WithMaxTokensCap Explain
// (callSnapshot) yolunda da tavan. Önce yalnız sohbet gövdesi uyguluyordu;
// briefing başlığı (~300 tok) profilin tam bütçesiyle gidiyordu.

import (
	"context"
	"testing"
)

func TestCallSnapshotHonorsMaxTokensCap(t *testing.T) {
	s := New("anthropic", "", "")
	s.SetProfiles([]ModelProfile{
		{ID: "fast", Provider: ProviderOpenAI, BaseURL: "http://llm.example.test/v1", Model: "unknown-model", MaxTokens: 2000},
	}, "fast", nil)
	ctx := context.Background()
	for _, c := range []struct {
		ctx  context.Context
		want int
	}{
		{ctx, 2000},
		{WithMaxTokensCap(ctx, 300), 300},
		{WithMaxTokensCap(ctx, 5000), 2000}, // tavan bütçeyi BÜYÜTMEZ
	} {
		if _, req, _, _, _ := s.callSnapshot(c.ctx); req.MaxTokens != c.want {
			t.Errorf("max_tokens=%d, want %d", req.MaxTokens, c.want)
		}
	}
}
