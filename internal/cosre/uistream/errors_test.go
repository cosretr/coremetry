package uistream

import "testing"

// errors_test.go — v0.10.1591 (CoSRE v2 Faz 1, R3): ClassifyError. Her sınıf
// için KAYNAKTAN KOPYALANMIŞ bir gerçek metin (yorumda dosyası); metin
// değişirse sınıf "unknown"a düşer ve bu test kırmızıya döner.
func TestClassifyError(t *testing.T) {
	cases := []struct{ raw, want string }{
		// internal/api/chat_deadline.go chatDeadlineMessageTR(3*time.Minute)
		{"Bu alışveriş 3 dakika tavanına dayandı ve durduruldu. Soru muhtemelen çok geniş: tek bir servise ya da daha dar bir zaman aralığına sorun.", ClassDeadline},
		// internal/api/chat_overflow.go chatOverflowMessageTR(false)
		{"Bu konuşma modelin bağlam penceresine sığmadı. En olası sebep birikmiş araç sonuçları ve uzun sohbet geçmişi. Yeni bir sohbet açıp soruyu daraltın.", ClassOverflow},
		// internal/api/chat_loop_steps.go chatCancelledMessageTR
		{"İstek iptal edildi — kalan araç çağrıları yürütülmedi.", ClassCancelled},
		// internal/ai/provider/salvage.go EmptyAnswerError("length")
		{"model returned no answer — token budget exhausted by reasoning; raise max_tokens or disable thinking (e.g. Qwen3 /no_think)", ClassMaxTokens},
		// internal/ai/provider/salvage.go EmptyAnswerError("")
		{"openai-compat: model returned empty content — no answer in content/reasoning. Check the model name + endpoint; a reasoning model may need /no_think. See the [copilot] pod log for the raw response", ClassMaxTokens},
		// internal/ai/provider/errors.go HTTPError{Provider:"openai-compat",Status:429}
		{"openai-compat 429: rate limit exceeded", ClassQuota},
		{"gemini: RESOURCE_EXHAUSTED quota", ClassQuota},
		// internal/ai/provider/errors.go HTTPError{Status:503}
		{"openai-compat 503: no slot available", ClassProviderDown},
		{"github copilot 502: bad gateway", ClassProviderDown},
		// internal/ai/provider/openai.go transport error
		{`openai-compat call: Post "http://llm.internal.test/v1/chat/completions": dial tcp: connection refused`, ClassProviderDown},
		// blocks.Emitter.Close / Translator.Finish
		{"stream closed before completion", ClassClosed},
		{"bambaşka bir hata", ClassUnknown},
		{"", ClassUnknown},
	}
	for _, c := range cases {
		if got := ClassifyError(c.raw); got != c.want {
			t.Errorf("ClassifyError(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}
