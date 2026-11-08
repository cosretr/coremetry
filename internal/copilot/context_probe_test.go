package copilot

import "testing"

// v0.10.1991 — bağlam penceresi sunucudan: vLLM /models ve Ollama /api/show ayrıştırması.
func TestParseModelsWindow(t *testing.T) {
	b := []byte(`{"data":[{"id":"other","max_model_len":8192},{"id":"qwen3.8","max_model_len":131072}]}`)
	got, ok := parseModelsWindow(b, "qwen3.8")
	if !ok || got.Tokens != 131072 || got.Source != "server" {
		t.Fatalf("vLLM: %+v %v", got, ok)
	}
	if _, ok := parseModelsWindow([]byte(`{"data":[{"id":"qwen3.8"}]}`), "qwen3.8"); ok {
		t.Fatal("alan yoksa okunmadı sayılmalı (Ollama'ya düşer)")
	}
}

func TestParseOllamaShow(t *testing.T) {
	set, ok := parseOllamaShow([]byte(`{"parameters":"temperature 0.2\nnum_ctx 32768","model_info":{"gemma3.context_length":131072}}`))
	if !ok || set.Tokens != 32768 || set.ModelMax != 131072 {
		t.Fatalf("num_ctx: %+v", set)
	}
	def, ok := parseOllamaShow([]byte(`{"parameters":"temperature 0.2","model_info":{"qwen3.context_length":40960}}`))
	if !ok || def.Tokens != ollamaDefaultNumCtx || def.ModelMax != 40960 || def.Detail == "" {
		t.Fatalf("varsayılan: %+v", def)
	}
	if _, ok := parseOllamaShow([]byte(`not json`)); ok {
		t.Fatal("bozuk yanıt")
	}
}
