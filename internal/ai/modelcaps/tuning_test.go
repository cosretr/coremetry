package modelcaps

import (
	"encoding/json"
	"reflect"
	"testing"
)

// v0.10.2375 — Qwen3.8 Flash Next yetenek kaydı: ad eşleşmesi ("qwen3.8" ya da
// "qwen38" + "flash", büyük/küçük harf duyarsız), aile qwen3 kalır; eşleşmeyen
// her model için yeni alanların HEPSİ sıfır (bugünkü kayıt aynen).
func TestQwen38FlashNextCaps(t *testing.T) {
	want := Caps{
		Family: FamilyQwen3, Reasoning: true, ThinkingSwitch: SwitchChatTemplate,
		ContextWindow: 262144, MaxOutputTokens: 131072, DefaultMaxTokens: 32768,
		RecommendedSampling: true,
		SamplingThinking:    Sampling{Temperature: 1.0, TopP: 0.95, TopK: 20, PresencePenalty: 0},
		SamplingDirect:      Sampling{Temperature: 0.7, TopP: 0.8, TopK: 20, PresencePenalty: 1.5},
		EvidenceScaling:     true, // v0.10.2378
	}
	for _, m := range []string{
		"Qwen/Qwen3.8-Flash-Next-FP8", "qwen38-flash-next-fp8", "QWEN38-FLASH", "qwen3.8-flash", "  qwen38_flash_next  ",
	} {
		if got := For(m); got != want {
			t.Errorf("For(%q) = %+v, want %+v", m, got, want)
		}
	}
	plainQwen3 := Caps{Family: FamilyQwen3, Reasoning: true, ThinkingSwitch: SwitchChatTemplate}
	negatives := map[string]Caps{
		"qwen3-30b":         plainQwen3,
		"qwen3.8-plus":      plainQwen3, // flash yok
		"qwen38-max":        plainQwen3,
		"qwen3.5-flash":     plainQwen3, // 3.8 değil
		"qwen2.5":           {Family: FamilyUnknown},
		"qwen2.5-flash":     {Family: FamilyUnknown},
		"gemma-4-31b":       {Family: FamilyGemma},
		"gemma-flash":       {Family: FamilyGemma},
		"my-flash-finetune": {Family: FamilyUnknown},
		"":                  {Family: FamilyUnknown},
	}
	for m, w := range negatives {
		if got := For(m); got != w {
			t.Errorf("For(%q) = %+v, want %+v (yeni alanlar sıfır kalmalı)", m, got, w)
		}
	}
}

// Pencere ad tablosu: kayıtlı model 262144; diğerleri eski değerinde. Addaki
// açık "32k" eki kayıttan önce gelir.
func TestContextWindowQwen38FlashNext(t *testing.T) {
	cases := map[string]int{
		"Qwen/Qwen3.8-Flash-Next-FP8": 262144,
		"qwen38-flash-next-fp8":       262144,
		"qwen38-flash-32k":            32768,
		"qwen3-30b":                   32768,
		"qwen3.8-plus":                32768,
		"qwen2.5":                     32768,
		"gemma-4-31b":                 131072,
		"my-model":                    0,
	}
	for m, want := range cases {
		if got := ContextWindow(m); got != want {
			t.Errorf("ContextWindow(%q)=%d want %d", m, got, want)
		}
	}
}

func TestSamplingFor(t *testing.T) {
	q := For("qwen38-flash-next")
	cases := []struct {
		name string
		caps Caps
		th   Thinking
		want Sampling
		ok   bool
	}{
		{"düşünme varsayılan (açık)", q, ThinkingDefault, Sampling{1.0, 0.95, 20, 0}, true},
		{"düşünme açık", q, ThinkingOn, Sampling{1.0, 0.95, 20, 0}, true},
		{"düşünme kapalı", q, ThinkingOff, Sampling{0.7, 0.8, 20, 1.5}, true},
		{"düz qwen3: öneri yok", For("qwen3-30b"), ThinkingDefault, Sampling{}, false},
		{"gemma: öneri yok", For("gemma-4-31b"), ThinkingOff, Sampling{}, false},
		{"bilinmeyen: öneri yok", For("my-model"), ThinkingOn, Sampling{}, false},
	}
	for _, c := range cases {
		got, ok := SamplingFor(c.caps, c.th)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: (%+v, %v), want (%+v, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// WithSampling girdiyi DEĞİŞTİRMEZ, düşünme anahtarıyla aynı gövdede birleşir;
// JSON'da tam olarak üç yeni anahtar çıkar.
func TestWithSamplingMergesWithoutMutating(t *testing.T) {
	base, _ := ExtraBody(For("qwen38-flash"), ThinkingOff)
	out := WithSampling(base, Sampling{Temperature: 0.7, TopP: 0.8, TopK: 20, PresencePenalty: 1.5})
	if len(base) != 1 {
		t.Fatalf("girdi değişti: %v", base)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"chat_template_kwargs":{"enable_thinking":false},"presence_penalty":1.5,"top_k":20,"top_p":0.8}`
	if string(raw) != want {
		t.Fatalf("gövde %s, want %s", raw, want)
	}
	// temperature bu yoldan İNMEZ (çekirdek alan).
	if _, has := out["temperature"]; has {
		t.Fatal("temperature ek gövdeye girmemeli")
	}
	// nil girdi + var olan anahtar korunur.
	if got := WithSampling(nil, Sampling{TopP: 0.95, TopK: 20}); !reflect.DeepEqual(got, map[string]any{"top_p": 0.95, "top_k": 20, "presence_penalty": 0.0}) {
		t.Fatalf("nil girdi: %v", got)
	}
	if got := WithSampling(map[string]any{"top_k": 5}, Sampling{TopK: 20}); got["top_k"] != 5 {
		t.Fatalf("var olan anahtar ezildi: %v", got)
	}
}

// Girdi gerçek gövdenin şeklidir: json.Marshal çıktısı (anahtarlar sıralı, "<"
// \u003c olarak kaçışlı) — sökülmeyen alanların baytları aynen kalır.
func TestStripSampling(t *testing.T) {
	in := `{"chat_template_kwargs":{"enable_thinking":true},"max_tokens":32768,"messages":[{"content":"a \u003c b","role":"user"}],"model":"qwen38-flash","presence_penalty":0,"temperature":1,"top_k":20,"top_p":0.95}`
	const want = `{"chat_template_kwargs":{"enable_thinking":true},"max_tokens":32768,"messages":[{"content":"a \u003c b","role":"user"}],"model":"qwen38-flash","temperature":1}`
	got, model, ok := StripSampling([]byte(in))
	if !ok || model != "qwen38-flash" || string(got) != want {
		t.Fatalf("(%s, %q, %v), want (%s, qwen38-flash, true)", got, model, ok, want)
	}
	// Tek alan da sökülür.
	if got, _, ok := StripSampling([]byte(`{"model":"m","top_k":20}`)); !ok || string(got) != `{"model":"m"}` {
		t.Fatalf("tek alan: %s %v", got, ok)
	}
	// Alan yoksa ya da gövde nesne değilse ok=false (yeniden deneme yok).
	for _, b := range []string{`{"model":"gemma-4-31b","temperature":0.2}`, `[1,2]`, `not json`, ``} {
		if _, _, ok := StripSampling([]byte(b)); ok {
			t.Errorf("%q: ok=true, sökülecek alan yok", b)
		}
	}
}

func TestMaxTokensCeiling(t *testing.T) {
	q := For("qwen38-flash-next")
	cases := []struct {
		name          string
		caps          Caps
		window, want  int
		generalCeilng int
	}{
		{"bilinmeyen model, pencere bilinmiyor", Caps{}, 0, 32768, 32768},
		{"bilinmeyen model, büyük pencere", Caps{}, 131072, 32768, 32768},
		{"bilinmeyen model, küçük pencere", Caps{}, 8192, 4096, 32768},
		{"gemma kaydı tavan taşımaz", For("gemma-4-31b"), 131072, 32768, 32768},
		{"qwen3.8: çıktı tavanı", q, 262144, 131072, 32768},
		{"qwen3.8: pencere bilinmiyor", q, 0, 131072, 32768},
		{"qwen3.8: sunucu 128k açmış → yarısı", q, 131072, 65536, 32768},
		{"qwen3.8: sunucu 32k açmış → yarısı", q, 32768, 16384, 32768},
	}
	for _, c := range cases {
		if got := MaxTokensCeiling(c.caps, c.window, c.generalCeilng); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

// v0.10.2378 — kanıt bütçesi ölçeği: yetenek + pencere / 32768, [1, 8];
// yeteneksiz model pencere ne olursa olsun 1.
func TestEvidenceScale(t *testing.T) {
	q := For("qwen38-flash-next-fp8")
	if !q.EvidenceScaling {
		t.Fatal("kayıtlı model kanıt ölçeği yeteneğini taşımalı")
	}
	cases := []struct {
		name   string
		caps   Caps
		window int
		want   int
	}{
		{"yetenek + yerel pencere 262144", q, 262144, 8},
		{"yetenek + geçit 131072", q, 131072, 4},
		{"yetenek + geçit 65536", q, 65536, 2},
		{"yetenek + geçit 100000 (aşağı yuvarlanır)", q, 100000, 3},
		{"yetenek + taban pencere", q, 32768, 1},
		{"yetenek + geçit 16384 → 1'in altına inmez", q, 16384, 1},
		{"yetenek + pencere bilinmiyor", q, 0, 1},
		{"yetenek + geçit yerel pencereden büyük sayı bildirdi → tavan 8", q, 1 << 20, EvidenceScaleMax},
		{"gemma 131072", For("gemma-4-31b-it"), 131072, 1},
		{"gemma 262144", For("gemma-4-31b-it"), 262144, 1},
		{"düz qwen3 262144", For("qwen3-30b"), 262144, 1},
		{"qwen3.8 flash'sız 262144", For("qwen3.8-plus"), 262144, 1},
		{"claude 200000", For("claude-sonnet-5"), 200000, 1},
		{"bilinmeyen 1M", For("my-model"), 1 << 20, 1},
		{"boş ad", For(""), 262144, 1},
	}
	for _, c := range cases {
		if got := EvidenceScale(c.caps, c.window); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	if EvidenceBaselineWindow*EvidenceScaleMax != q.ContextWindow {
		t.Errorf("tavan gerekçesi bozuldu: %d × %d ≠ yerel pencere %d", EvidenceBaselineWindow, EvidenceScaleMax, q.ContextWindow)
	}
	// Yetenek yalnız kayıtlı satırda: başka hiçbir ad desenine sızmamalı.
	for _, m := range []string{"qwen3-30b", "qwq-32b", "deepseek-r1", "gemma-4-31b", "llama-4", "mistral-large", "gpt-5", "o3", "claude-fable-5", "x"} {
		if For(m).EvidenceScaling {
			t.Errorf("%q kanıt ölçeği yeteneği taşımamalı", m)
		}
	}
}
