package provider

import "testing"

// v0.10.807 (dış skill denetimi L2) — sağlayıcı önek-önbellek sayacı:
// OpenAI uyumlu `usage.prompt_tokens_details.cached_tokens`, Anthropic
// `usage.cache_read_input_tokens`. Alan yoksa 0 (ölçülmedi), hata değil.

func TestParseOpenAIChatCachedTokens(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":1200,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":1024}}}`)
	r, err := ParseOpenAIChat(body)
	if err != nil {
		t.Fatal(err)
	}
	if r.InputTokens != 1200 || r.OutputTokens != 30 || r.CachedTokens != 1024 {
		t.Errorf("usage: %+v", r)
	}
	// Ayrıntı yok (Ollama / eski vLLM) → 0, çözümleme aynen.
	r, err = ParseOpenAIChat([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`))
	if err != nil || r.CachedTokens != 0 || r.InputTokens != 10 {
		t.Errorf("ayrıntısız usage: %+v err=%v", r, err)
	}
}

func TestParseAnthropicCachedTokens(t *testing.T) {
	body := []byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":900,"output_tokens":40,"cache_read_input_tokens":800}}`)
	r, err := ParseAnthropic(body)
	if err != nil {
		t.Fatal(err)
	}
	// InputTokens = TOPLAM giriş (openai prompt_tokens ile aynı anlam):
	// kuyruk 900 + önbellekten okunan 800.
	if r.InputTokens != 1700 || r.CachedTokens != 800 {
		t.Errorf("usage: %+v", r)
	}
}

func TestOpenAIStreamAccumCachedTokens(t *testing.T) {
	var a openAIStreamAccum
	a.feed(`data: {"choices":[{"delta":{"content":"a"}}]}`)
	a.feed(`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":64}}}`)
	if a.inTokens != 100 || a.cached != 64 {
		t.Errorf("acc: in=%d cached=%d", a.inTokens, a.cached)
	}
}

func TestAnthropicStreamAccumCachedTokens(t *testing.T) {
	var a anthropicStreamAccum
	a.feed(`data: {"type":"message_start","message":{"usage":{"input_tokens":50,"cache_read_input_tokens":40}}}`)
	if a.inTokens != 90 || a.cached != 40 {
		t.Errorf("acc: in=%d cached=%d", a.inTokens, a.cached)
	}
}
