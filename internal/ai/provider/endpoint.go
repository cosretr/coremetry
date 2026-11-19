package provider

import (
	"net/url"
	"strings"
)

// endpoint.go — v0.10.2375: openai-uyumlu sohbet ucunun URL kuralı TEK yerde.
// Üç gövde (buffered/stream/tools) URL'yi buradan kurar; copilot'un örnekleme
// alanı düşüşü (copilot/sampling_fallback.go) aynı kuralı TERSİNDEN okur —
// "bu istek hangi (base, model) ucuna gitti" anahtarı, isteği kuran ifadeyle
// ayrışamaz.

const chatCompletionsPath = "/chat/completions"

// chatCompletionsURL — base (/v1 önekini içerir) + /chat/completions.
func chatCompletionsURL(base string) string {
	return strings.TrimRight(base, "/") + chatCompletionsPath
}

// OpenAIChatEndpointKey — bir /chat/completions isteğinin URL'sinden uç
// tabanı (şema + host + yol öneki); ok=false: bu bir sohbet isteği değil.
func OpenAIChatEndpointKey(u *url.URL) (string, bool) {
	if u == nil || !strings.HasSuffix(u.Path, chatCompletionsPath) {
		return "", false
	}
	return u.Scheme + "://" + u.Host + strings.TrimSuffix(u.Path, chatCompletionsPath), true
}

// OpenAIChatEndpointKeyForBase — profilin base URL'sinden AYNI anahtar: boş
// base sağlayıcının varsayılanıdır; URL chatCompletionsURL ile kurulup aynı
// ayrıştırıcıdan geçer. Ayrıştırılamayan base ham hâliyle anahtar olur (o
// istek zaten kurulamaz).
func OpenAIChatEndpointKeyForBase(base string) string {
	if base == "" {
		base = defaultBaseURL
	}
	raw := chatCompletionsURL(base)
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	key, _ := OpenAIChatEndpointKey(u)
	return key
}
