package api

// chat_deep_test.go — v0.10.1150 "Derin düşün" (chat_deep.go) sözleşmesi:
//   - türetme: bayrak yoksa sıfır değer; açık profil seçimi kazanır; derin
//     profilin rol allowlist'i çağıranı kapsamıyorsa derin model sessizce düşer;
//   - tavanlar: tur/çağrı ×1.5 (mutlak tavanlarla), wiki 8 sayfa / 15 aday,
//     bütçe ×1.5 model penceresi tavanıyla (RAG yarısı değişmez);
//   - Derin KAPALIYKEN her şey bayt bayt eski (ctx, prompt'lar, etiketler,
//     bütçe, seçim) ve istek gövdesi `deep` taşımıyorsa bayrak false.
// Adlar sentetik.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

func TestDeriveDeepMode(t *testing.T) {
	big := copilot.ModelProfile{ID: "big-model", Provider: copilot.ProviderOpenAI}
	adminOnly := copilot.ModelProfile{ID: "big-admin", Provider: copilot.ProviderOpenAI, Roles: []string{"admin"}}
	cases := []struct {
		name     string
		deep     bool
		explicit string
		p        copilot.ModelProfile
		has      bool
		role     string
		want     deepMode
	}{
		{"kapalı: sıfır değer", false, "", big, true, "viewer", deepMode{}},
		{"açık + derin profil", true, "", big, true, "viewer", deepMode{On: true, Profile: "big-model"}},
		{"açık profil seçimi kazanır", true, "fast", big, true, "viewer", deepMode{On: true}},
		{"derin profil yok: model aynen", true, "", copilot.ModelProfile{}, false, "editor", deepMode{On: true}},
		{"rol kapsamıyor: derin model sessizce düşer", true, "", adminOnly, true, "viewer", deepMode{On: true}},
		{"rol kapsıyor", true, "", adminOnly, true, "admin", deepMode{On: true, Profile: "big-admin"}},
	}
	for _, tc := range cases {
		if got := deriveDeepMode(tc.deep, tc.explicit, tc.p, tc.has, tc.role); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestDeepModeCaps(t *testing.T) {
	off, on := deepMode{}, deepMode{On: true}
	if off.toolRounds() != chatMaxToolRounds || off.toolCalls() != chatMaxToolCalls ||
		off.wikiMaxPages() != wikiMaxPages || off.wikiCandidates() != wikiSelectMaxCandidates ||
		off.wikiSearchLimit() != wikiTierSearchLimit {
		t.Fatal("kapalıyken tavanlar eski sabitler olmalı")
	}
	if on.toolRounds() != 7 || on.toolCalls() != 9 {
		t.Fatalf("Derin döngü tavanı %d tur / %d çağrı, want 7 / 9", on.toolRounds(), on.toolCalls())
	}
	if on.toolRounds() > deepToolRoundsCeiling || on.toolCalls() > deepToolCallsCeiling {
		t.Fatal("Derin döngü mutlak tavanı aştı")
	}
	if on.wikiMaxPages() != 8 || on.wikiCandidates() != 15 || on.wikiSearchLimit() != 15*wikiTierPerPage {
		t.Fatalf("Derin wiki: %d sayfa / %d aday / %d isabet", on.wikiMaxPages(), on.wikiCandidates(), on.wikiSearchLimit())
	}
}

func TestDeepWikiBudgetScaledAndCapped(t *testing.T) {
	on := deepMode{On: true}
	cases := []struct {
		name                       string
		manual, window, completion int
		want                       int
	}{
		{"geniş pencere ×1.5", 0, 131072, 4096, 36000},
		{"dar pencere: pencere tavanı", 0, 8192, 4096, 7788},
		{"orta pencere: ×1.5'i pencere tavanı keser", 0, 15596, 4096, 30000},
		{"elle ayar, pencere bilinmiyor: ×1.5", 20000, 0, 4096, 30000},
		{"elle ayar, pencere bilinmiyor: wiki tavanı", 40000, 0, 4096, wiki.MaxContextChars},
		{"bilinmeyen model, otomatik: varsayılan pencere tavanı", 0, 0, 4096, 7788},
	}
	for _, tc := range cases {
		b := wikiBudgetFor(tc.manual, tc.window, tc.completion)
		if got := on.wikiBudget(b, tc.manual, tc.window, tc.completion); got != tc.want {
			t.Errorf("%s: %d (taban %d), want %d", tc.name, got, b, tc.want)
		}
		if got := (deepMode{}).wikiBudget(b, tc.manual, tc.window, tc.completion); got != b {
			t.Errorf("%s: kapalıyken %d, want %d", tc.name, got, b)
		}
	}
}

func TestWikiBudgetDeepSurfaceOnly(t *testing.T) {
	withModelLimits(t, 131072, 4096)
	var s *Server
	plain := context.Background()
	deep := withDeepMode(plain, deepMode{On: true})
	if got := s.wikiBudget(plain, nil, "wiki-chat"); got != 24000 {
		t.Fatalf("kapalı wiki-chat: %d", got)
	}
	if got := s.wikiBudget(deep, nil, "wiki-chat"); got != 36000 {
		t.Fatalf("Derin wiki-chat: %d, want 36000", got)
	}
	if got := s.wikiBudget(deep, nil, "rag-chat"); got != 24000 {
		t.Fatalf("Derin rag-chat değişmemeli: %d", got)
	}
}

// TestDeepOffByteIdentical — Derin kapalıyken hiçbir şey değişmez.
func TestDeepOffByteIdentical(t *testing.T) {
	ctx := context.Background()
	off := deepMode{}
	if withDeepMode(ctx, off) != ctx {
		t.Fatal("kapalıyken ctx sarılmamalı")
	}
	if deepModeFrom(ctx) != off || copilot.DeepAnswerFrom(ctx) {
		t.Fatal("işaretsiz ctx Derin sayıldı")
	}
	if off.answerPrefix() != "" || off.loopPrefix() != "" {
		t.Fatal("kapalıyken prompt eki boş olmalı")
	}
	if off.wikiSelectPrompt() != copilot.SystemPromptWikiSelect() {
		t.Fatal("kapalıyken seçim prompt'u eskisi olmalı")
	}
	if chatBudgetExhaustedCapsLabelTR(4, chatMaxToolCalls, 3, 1, 2, chatMaxToolRounds) != chatBudgetExhaustedLabelTR(4, 3, 1, 2) {
		t.Fatal("varsayılan tavanlı bütçe etiketi değişti")
	}
	// Gövde `deep` taşımıyorsa bayrak false; taşıyorsa true.
	var req chatRequest
	if err := json.Unmarshal([]byte(`{"messages":[{"role":"user","text":"x"}],"context":{"service":"svc-orders"}}`), &req); err != nil || req.Context.Deep {
		t.Fatalf("deep'siz gövde: err=%v deep=%v", err, req.Context.Deep)
	}
	if err := json.Unmarshal([]byte(`{"messages":[{"role":"user","text":"x"}],"context":{"deep":true}}`), &req); err != nil || !req.Context.Deep {
		t.Fatalf("deep'li gövde: err=%v deep=%v", err, req.Context.Deep)
	}
	// Profil audit satırı: işaretsizken eski satır, işaretliyken " deep=true" eki.
	if profileDeepAudit(false) != "" || profileDeepAudit(true) != " deep=true" {
		t.Fatal("derin profil audit eki")
	}
	// Kapalıyken chatDeepMode profil okumaz (nil sunucu da güvenli).
	var s *Server
	if got := s.chatDeepMode(nil, false, ""); got != off {
		t.Fatalf("kapalı chatDeepMode: %+v", got)
	}
}

func TestDeepModeCtxCarriesProfileAndAnswerFlag(t *testing.T) {
	d := deepMode{On: true, Profile: "big-model"}
	ctx := withDeepMode(context.Background(), d)
	if deepModeFrom(ctx) != d || !copilot.DeepAnswerFrom(ctx) {
		t.Fatal("Derin karar ctx'e inmedi")
	}
	if !strings.Contains(d.loopPrefix(), copilot.ChatDeepAddendum()) ||
		!strings.Contains(d.loopPrefix(), fmt.Sprintf("en çok %d tool çağrısı ve %d tur", d.toolCalls(), d.toolRounds())) {
		t.Fatalf("döngü eki gerçek tavanı söylemiyor: %q", d.loopPrefix())
	}
	if !strings.Contains(d.stepLabel(), "derin düşün") || !strings.Contains(d.stepLabel(), "big-model") {
		t.Fatalf("adım etiketi: %q", d.stepLabel())
	}
}

// TestWikiSelectPagesDeepPool — Derin: 15 aday, en çok 8 seçim, Derin prompt;
// kapalı: 10 aday, 5 seçim, eski prompt.
func TestWikiSelectPagesDeepPool(t *testing.T) {
	pages := make([]wikiPage, 0, 12)
	for i := 0; i < 12; i++ {
		pages = append(pages, wikiPage{Hits: []wiki.Hit{{ChunkRef: wiki.ChunkRef{
			Title: fmt.Sprintf("Runbook %02d", i), Path: fmt.Sprintf("/Runbook-%02d", i), Text: "sentetik kesit"},
			Score: 1 - float64(i)/100}}})
	}
	var gotSystem, gotUser string
	prev := wikiSelectFn
	wikiSelectFn = func(_ *Server, _ context.Context, system, user string) (string, error) {
		gotSystem, gotUser = system, user
		return `{"pages":[1,2,3,4,5,6,7,8,9,10,11,12]}`, nil
	}
	t.Cleanup(func() { wikiSelectFn = prev })
	s := &Server{}
	noop := func(string, any) {}

	chosen, ok := s.wikiSelectPages(context.Background(), noop, "nasıl", "", pages)
	if !ok || len(chosen) != wikiSelectMaxPick || gotSystem != copilot.SystemPromptWikiSelect() || strings.Contains(gotUser, "[11]") {
		t.Fatalf("kapalı: ok=%v seçilen=%d (prompt eski=%v, 11. aday sunuldu=%v)", ok, len(chosen),
			gotSystem == copilot.SystemPromptWikiSelect(), strings.Contains(gotUser, "[11]"))
	}
	deep := withDeepMode(context.Background(), deepMode{On: true})
	chosen, ok = s.wikiSelectPages(deep, noop, "nasıl", "", pages)
	if !ok || len(chosen) != deepWikiMaxPages || !strings.Contains(gotSystem, "1–8 adayı seç") || !strings.Contains(gotUser, "[12]") {
		t.Fatalf("Derin: ok=%v seçilen=%d", ok, len(chosen))
	}
	if n := len(wikiScorePagesN(pages, deepWikiMaxPages)); n != deepWikiMaxPages {
		t.Fatalf("Derin skor tabanlı seçim %d sayfa, want 8", n)
	}
	if n := len(wikiScorePages(pages)); n != wikiMaxPages {
		t.Fatalf("kapalı skor tabanlı seçim %d sayfa, want 5", n)
	}
}
