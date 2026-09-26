package copilot

import (
	"context"
	"testing"
)

// ModelFor — "sen hangi modelsin" cevabı sohbetin GERÇEKTEN kullandığı
// profilin modelini söyler: yüzey haritası ve WithProfile varsayılanı ezer;
// profilde model boşsa sağlayıcı varsayılanı (YAPILANDIRILMAMIŞ değil).
func TestModelForFollowsResolvedProfile(t *testing.T) {
	s := New("openai", "kd", "default-model")
	s.SetProfiles([]ModelProfile{
		{ID: "big", Provider: ProviderOpenAI, BaseURL: "http://big/v1", APIKey: "kb", Model: "gemma4-31b"},
		{ID: "small", Provider: ProviderOpenAI, BaseURL: "http://small/v1", APIKey: "ks", Model: "qwen3-8b"},
		{ID: "claude", Provider: ProviderAnthropic, APIKey: "ka"},
	}, "big", map[string]string{"chat-guided": "small"})

	if got := s.ModelFor(context.Background()); got != "gemma4-31b" {
		t.Errorf("varsayılan profil: %q", got)
	}
	guided := WithMeta(context.Background(), CallMeta{Surface: "chat-guided"})
	if got := s.ModelFor(guided); got != "qwen3-8b" {
		t.Errorf("yüzey haritası (chat-guided → small) izlenmedi: %q", got)
	}
	if got, want := s.ModelFor(WithProfile(context.Background(), "claude")), s.DefaultModels()[ProviderAnthropic]; got != want {
		t.Errorf("boş model alanı sağlayıcı varsayılanına düşmeli: %q, want %q", got, want)
	}
	var nilSvc *Service
	if got := nilSvc.ModelFor(context.Background()); got != "" {
		t.Errorf("nil servis: %q", got)
	}
}
