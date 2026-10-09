package modelcaps

import "strings"

// ContextWindow — v0.10.1136 (wiki çok-kaynaklı okuma): model adından BİLİNEN
// bağlam penceresi (girdi+çıktı jetonu); bilinmiyorsa 0.
//
// TUTUCU tablo: yerel sunucu (vLLM max_model_len, Ollama num_ctx) ailenin
// yayımlanmış penceresinden küçük açabilir. Bu yüzden yalnız adı AÇIKÇA uzun
// bağlam söyleyen modeller büyük pencere alır (gemma-3 27b/31b, llama 3.1+,
// "128k"/"32k" son ekleri…); küçük/belirsiz varyantlar 8192. Çağıran
// (api/chat_wiki_multi.go wikiBudget) bunu pencereden completion ve ek yük
// düşerek bütçe tavanına çevirir; operatör Ayarlar'dan elle değer verir
// (pencere biliniyorsa yine pencereyle kapaklı).
func ContextWindow(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(m, s) {
				return true
			}
		}
		return false
	}
	switch {
	case m == "":
		return 0
	case has("128k", "131k"):
		return 131072
	case has("32k"):
		return 32768
	case has("gemma"):
		// Gemma 3+ büyük varyantları 128k; gemma-2/gemma-1 ve küçük/belirsiz 8k.
		if !has("gemma-2", "gemma2", "gemma-1") && has("27b", "31b") {
			return 131072
		}
		return 8192
	case has("qwen3", "qwen2.5", "qwen-2.5"):
		return 32768
	case has("qwen"):
		return 8192
	case has("llama3.1", "llama-3.1", "llama3.2", "llama-3.2", "llama3.3", "llama-3.3", "llama4", "llama-4"):
		return 131072
	case has("llama"):
		return 8192
	case has("mixtral", "mistral-nemo", "mistral-large", "mistral-small-3", "mistral-small-24"):
		return 32768
	case has("mistral"):
		return 8192
	case has("deepseek"):
		return 32768
	case has("claude"):
		return 200000
	case strings.HasPrefix(m, "gpt-4o") || strings.HasPrefix(m, "gpt-4.1") || strings.HasPrefix(m, "gpt-5") ||
		strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4"):
		return 128000
	}
	return 0
}
