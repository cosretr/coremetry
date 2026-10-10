package copilot

// deep_test.go — v0.10.1150 "Derin düşün": completion bütçesi ×1.5
// (maxMaxTokens ve pencere yarısı tavanlı, tabanın altına inmez), ctx işareti
// yokken bayt bayt aynı; derin profil tek (yeni işaret eskisini düşürür) ve
// kalıcı blob'dan geri gelir; wiki seçim prompt'unun Derin varyantı yalnız
// üst sınırı değiştirir. Adlar sentetik.

import (
	"context"
	"strings"
	"testing"
)

func TestDeepMaxTokens(t *testing.T) {
	cases := []struct {
		name         string
		base, window int
		want         int
	}{
		{"pencere bilinmiyor ×1.5", 4096, 0, 6144},
		{"geniş pencere ×1.5", 4096, 131072, 6144},
		{"dar pencere: yarısı tavan, tabanın altına inmez", 4096, 8192, 4096},
		{"orta pencere: yarısı tavan", 4096, 10000, 5000},
		{"mutlak tavan maxMaxTokens", 30000, 0, maxMaxTokens},
		{"sıfır taban aynen", 0, 0, 0},
	}
	for _, tc := range cases {
		if got := DeepMaxTokens(tc.base, tc.window); got != tc.want {
			t.Errorf("%s: DeepMaxTokens(%d,%d)=%d, want %d", tc.name, tc.base, tc.window, got, tc.want)
		}
	}
}

func TestDeepMaxTokensForOnlyWithFlag(t *testing.T) {
	ctx := context.Background()
	if DeepAnswerFrom(ctx) {
		t.Fatal("işaretsiz ctx Derin sayıldı")
	}
	if got := deepMaxTokensFor(ctx, 4096, "unknown-model"); got != 4096 {
		t.Fatalf("Derin kapalıyken bütçe değişti: %d", got)
	}
	if got := deepMaxTokensFor(WithDeepAnswer(ctx), 4096, "unknown-model"); got != 6144 {
		t.Fatalf("Derin açıkken bütçe %d, want 6144", got)
	}
}

func TestMaxTokensForDeep(t *testing.T) {
	s := New("anthropic", "", "")
	s.SetProfiles([]ModelProfile{
		{ID: "fast", Provider: ProviderOpenAI, BaseURL: "http://llm.example.test/v1", Model: "unknown-model", MaxTokens: 2000},
	}, "fast", nil)
	ctx := context.Background()
	if got := s.MaxTokensFor(ctx); got != 2000 {
		t.Fatalf("kapalı: %d", got)
	}
	if got := s.MaxTokensFor(WithDeepAnswer(ctx)); got != 3000 {
		t.Fatalf("Derin: %d, want 3000", got)
	}
	_, req, _, _, _ := s.callSnapshot(WithDeepAnswer(ctx))
	if req.MaxTokens != 3000 {
		t.Fatalf("Derin çağrı gövdesi max_tokens=%d, want 3000", req.MaxTokens)
	}
	_, req, _, _, _ = s.callSnapshot(ctx)
	if req.MaxTokens != 2000 {
		t.Fatalf("kapalı çağrı gövdesi max_tokens=%d, want 2000", req.MaxTokens)
	}
}

func TestDeepProfileExclusiveAndPersisted(t *testing.T) {
	store := newMemStore()
	s := New("anthropic", "", "")
	ctx := context.Background()
	if _, ok := s.DeepProfile(); ok {
		t.Fatal("işaretsiz kümede derin profil bulundu")
	}
	up := func(p ModelProfile) {
		t.Helper()
		if err := s.UpsertProfile(ctx, store, p); err != nil {
			t.Fatal(err)
		}
	}
	up(ModelProfile{ID: "fast", Provider: ProviderOpenAI, BaseURL: "http://llm.example.test/v1"})
	up(ModelProfile{ID: "big-a", Provider: ProviderOpenAI, BaseURL: "http://llm.example.test/v1", Deep: true})
	if p, ok := s.DeepProfile(); !ok || p.ID != "big-a" {
		t.Fatalf("derin profil = %+v ok=%v", p, ok)
	}
	up(ModelProfile{ID: "big-b", Provider: ProviderOpenAI, BaseURL: "http://llm.example.test/v1", Deep: true})
	n := 0
	for _, p := range s.Profiles() {
		if p.Deep {
			n++
		}
	}
	if p, _ := s.DeepProfile(); p.ID != "big-b" || n != 1 {
		t.Fatalf("derin işaret tek olmalı: aktif=%s işaretli=%d", p.ID, n)
	}
	// Kalıcı blob'dan geri gelir.
	s2 := New("anthropic", "", "")
	if err := s2.LoadPersisted(ctx, store); err != nil {
		t.Fatal(err)
	}
	if p, ok := s2.DeepProfile(); !ok || p.ID != "big-b" {
		t.Fatalf("yeniden yüklemede derin profil = %+v ok=%v", p, ok)
	}
}

func TestWikiSelectDeepPrompt(t *testing.T) {
	base := SystemPromptWikiSelect()
	if !strings.Contains(base, wikiSelectPickRule) {
		t.Fatalf("taban prompt seçim kuralını taşımıyor: %q", wikiSelectPickRule)
	}
	deep := SystemPromptWikiSelectDeep(8)
	if !strings.Contains(deep, "1–8 adayı seç") || strings.Contains(deep, wikiSelectPickRule) {
		t.Fatal("Derin varyant üst sınırı 8'e çevirmedi")
	}
	if strings.Replace(deep, "1–8 adayı seç", wikiSelectPickRule, 1) != base {
		t.Fatal("Derin varyant üst sınır dışında da değişti")
	}
	if got := ChatDeepLoopBudget(9, 7); !strings.Contains(got, "en çok 9 tool çağrısı ve 7 tur") {
		t.Fatalf("bütçe satırı: %q", got)
	}
}
