package copilot

import (
	"context"
	"testing"
)

// v0.10.940 — SurfaceProfileID, evalset koşucusunun "üretim bu yüzeyi hangi
// profile gönderirdi" sorusu. Çözücünün KENDİSİNİ (resolveProfileLocked)
// çağırdığı için sıra aynıdır: harita > grup kardeşi > varsayılan. Tablo,
// ayrı bir kopya mantık yazılırsa kayacak üç kolu pinler.
func TestSurfaceProfileIDFollowsResolver(t *testing.T) {
	s := New("openai", "kd", "default-model")
	s.SetProfiles([]ModelProfile{
		{ID: "big", Label: "Büyük", Provider: ProviderOpenAI, BaseURL: "http://big/v1", APIKey: "kb", Model: "gemma4-31b"},
		{ID: "small", Label: "Yerel qwen", Provider: ProviderOpenAI, BaseURL: "http://small/v1", APIKey: "ks", Model: "qwen3-8b"},
		{ID: "bg", Provider: ProviderOpenAI, BaseURL: "http://bg/v1", APIKey: "kg", Model: "bg-model"},
	}, "big", map[string]string{"chat-intent": "small", "explain-slo": "bg"})

	cases := []struct {
		surface string
		want    string
		why     string
	}{
		{"chat-intent", "small", "harita girdisi"},
		{"explain-slo", "bg", "harita grupsuz yüzeyi de taşır"},
		{"chat-offtopic", "small", "haritada yok → grup kardeşi (chat-intent)"},
		{"chat-intent-none", "small", "grup kardeşi"},
		{"problem-auto-explain", "big", "background grubu kayıtsız → varsayılan"},
		{"explain-problem", "big", "grupsuz, haritasız → varsayılan"},
		{"evalset-IntentClassify", "big", "evalset etiketi haritada yok → varsayılan (koşucunun WithProfile sebebi)"},
		{"", "big", "boş yüzey → varsayılan"},
	}
	for _, c := range cases {
		if got := s.SurfaceProfileID(c.surface); got != c.want {
			t.Errorf("%q → %q, want %q (%s)", c.surface, got, c.want, c.why)
		}
		// Çözücüyle birebir: aynı yüzeyle ctx kurup profileID sormak aynı kimliği vermeli.
		if got, via := s.SurfaceProfileID(c.surface), s.profileID(WithMeta(context.Background(), CallMeta{Surface: c.surface})); got != via {
			t.Errorf("%q: SurfaceProfileID %q ≠ çözücü %q", c.surface, got, via)
		}
	}

	var nilSvc *Service
	if got := nilSvc.SurfaceProfileID("chat-intent"); got != "" {
		t.Errorf("nil servis: %q", got)
	}
}

// ProfileView — anahtarsız görünüm: alanlar profilden, boş model sağlayıcı
// varsayılanına düşer, bilinmeyen kimlik ok=false, küme boşken "default"
// geçici aynayı anlatır. İmza anahtar TAŞIYAMAZ (dönüş değerleri sabit);
// test yine de dönen hiçbir alanın anahtar değerini içermediğini doğrular.
func TestProfileViewIsKeyFree(t *testing.T) {
	s := New("openai", "kd", "default-model")
	s.SetProfiles([]ModelProfile{
		{ID: "yerel", Label: "Yerel qwen", Provider: ProviderOpenAI, BaseURL: "http://ollama:11434/v1", APIKey: "SECRET-1", Model: "qwen3.5-2b"},
		{ID: "claude", Provider: ProviderAnthropic, APIKey: "SECRET-2"},
	}, "yerel", nil)

	label, prov, model, base, ok := s.ProfileView("yerel")
	if !ok || label != "Yerel qwen" || prov != ProviderOpenAI || model != "qwen3.5-2b" || base != "http://ollama:11434/v1" {
		t.Fatalf("yerel: %q %q %q %q %v", label, prov, model, base, ok)
	}
	_, prov, model, _, ok = s.ProfileView("claude")
	if want := s.DefaultModels()[ProviderAnthropic]; !ok || prov != ProviderAnthropic || model != want {
		t.Fatalf("boş model sağlayıcı varsayılanına düşmeli: %q %q (want %q) %v", prov, model, want, ok)
	}
	for _, id := range []string{"yerel", "claude"} {
		l, p, m, b, _ := s.ProfileView(id)
		for _, f := range []string{l, p, m, b} {
			if f == "SECRET-1" || f == "SECRET-2" {
				t.Fatalf("%s: anahtar görünüme sızdı", id)
			}
		}
	}
	if _, _, _, _, ok := s.ProfileView("yok"); ok {
		t.Fatal("bilinmeyen kimlik ok=false olmalı")
	}

	// Profilsiz servis (eski düz alanlar): çözücü geçici aynayı "default"
	// kimliğiyle döner; görünüm de onu anlatabilmeli.
	legacy := &Service{provider: ProviderOpenAI, model: "gemma4", baseURL: "http://vllm/v1", apiKey: "SECRET-3"}
	if id := legacy.SurfaceProfileID("chat-intent"); id != DefaultProfileID {
		t.Fatalf("profilsiz servis kimliği %q", id)
	}
	if _, p, m, b, ok := legacy.ProfileView(DefaultProfileID); !ok || p != ProviderOpenAI || m != "gemma4" || b != "http://vllm/v1" {
		t.Fatalf("ayna görünümü: %q %q %q %v", p, m, b, ok)
	}
}
