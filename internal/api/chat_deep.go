package api

// chat_deep.go — v0.10.1150 "Derin düşün" (operatör): composer'daki Derin
// anahtarı açıkken (context.deep) alışveriş daha çok kaynak okur ve daha çok
// adım atar. Karar istek başına TEK kez türetilir (deepMode) ve ctx ile aşağı
// iner; kademeler kendi tavanlarını buradan okur.
//
// Derin açıkken (hepsi sınırlı, kapı/yetki aynen):
//   1. Model: "Derin düşün profili" işaretliyse o profil — kullanıcının AÇIK
//      profil seçimi kazanır; derin profilin rol allowlist'i çağıranı
//      kapsamıyorsa derin model sessizce düşer (mevcut model).
//   2. Wiki (kademe / takip / kurtarma): bütçe ×1.5 (model penceresi tavanı
//      aynen), en çok 8 sayfa (5 yerine), wiki_select aday havuzu 15 (10 yerine).
//   3. Serbest araç döngüsü: tur ve çağrı tavanı %50 büyür; mutlak tavanlar
//      (deepToolRoundsCeiling / deepToolCallsCeiling, alışveriş deadline'ı) kalır.
//   4. Cevap üslubu: kısalık talimatı gevşer (copilot.ChatDeepAddendum),
//      completion bütçesi +%50 (copilot.DeepMaxTokens tavanlarıyla).
//
// Derin KAPALIYKEN deepMode sıfır değerdir: ctx'e hiçbir şey eklenmez, her
// tavan eski sabittir ve her prompt bayt bayt eskisidir (chat_deep_test.go).

import (
	"context"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	deepScaleNum, deepScaleDen = 3, 2 // ×1.5
	deepWikiMaxPages           = 8
	deepWikiCandidates         = 15
	// Mutlak tavanlar — ölçek ne derse desin döngü bunları aşmaz.
	deepToolRoundsCeiling = 8
	deepToolCallsCeiling  = 12
)

// deepMode — istek başına Derin kararı. Sıfır değer = kapalı (eski davranış).
type deepMode struct {
	On bool
	// Profile — Derin için seçilen profil kimliği ("" = model değişmedi).
	Profile string
}

func deepScale(n int) int { return n * deepScaleNum / deepScaleDen }

// toolRounds / toolCalls — serbest döngünün tur ve çağrı tavanı.
func (d deepMode) toolRounds() int {
	if !d.On {
		return chatMaxToolRounds
	}
	return min(deepScale(chatMaxToolRounds), deepToolRoundsCeiling)
}

func (d deepMode) toolCalls() int {
	if !d.On {
		return chatMaxToolCalls
	}
	return min(deepScale(chatMaxToolCalls), deepToolCallsCeiling)
}

// wikiMaxPages — okunan en çok wiki sayfası (skor tabanlı ve model seçimi).
func (d deepMode) wikiMaxPages() int {
	if !d.On {
		return wikiMaxPages
	}
	return deepWikiMaxPages
}

// wikiCandidates — wiki_select aday havuzu / kademe aday kümesi.
func (d deepMode) wikiCandidates() int {
	if !d.On {
		return wikiSelectMaxCandidates
	}
	return deepWikiCandidates
}

// wikiSearchLimit — aday havuzuna yetecek isabet (sayfa başı wikiTierPerPage).
func (d deepMode) wikiSearchLimit() int {
	if !d.On {
		return wikiTierSearchLimit
	}
	return deepWikiCandidates * wikiTierPerPage
}

// wikiBudget — SAF: hesaplanan bütçe ×1.5, model penceresinin tavanıyla
// (pencere bilinmiyorsa: elle ayar varsa wiki.MaxContextChars, yoksa varsayılan
// pencere tavanı). Tabanın altına inmez.
func (d deepMode) wikiBudget(budget, manual, windowTokens, completionTokens int) int {
	if !d.On {
		return budget
	}
	var capChars int
	switch {
	case windowTokens > 0:
		capChars = wikiWindowCap(windowTokens, completionTokens)
	case manual > 0:
		capChars = wiki.MaxContextChars
	default:
		capChars = wikiWindowCap(wikiAssumedWindow, completionTokens)
	}
	return max(budget, min(deepScale(budget), capChars))
}

// answerPrefix — sistem mesajının önüne giden üslup eki (kapalıyken "").
func (d deepMode) answerPrefix() string {
	if !d.On {
		return ""
	}
	return copilot.ChatDeepAddendum()
}

// loopPrefix — serbest döngünün eki: üslup + gerçek araç bütçesi.
func (d deepMode) loopPrefix() string {
	if !d.On {
		return ""
	}
	return copilot.ChatDeepAddendum() + copilot.ChatDeepLoopBudget(d.toolCalls(), d.toolRounds())
}

// wikiSelectPrompt — sayfa seçiminin sistem mesajı (kapalıyken eskisi).
func (d deepMode) wikiSelectPrompt() string {
	if !d.On {
		return copilot.SystemPromptWikiSelect()
	}
	return copilot.SystemPromptWikiSelectDeep(d.wikiMaxPages())
}

// stepLabel — görünür adım çipi (operatör Derin'in uygulandığını görür).
func (d deepMode) stepLabel() string {
	s := "derin düşün · daha çok kaynak, daha çok adım"
	if d.Profile != "" {
		s += " · profil: " + d.Profile
	}
	return s
}

// deriveDeepMode — SAF: istek bayrağı + açık profil seçimi + işaretli derin
// profil + çağıranın rolü → karar.
func deriveDeepMode(deep bool, explicitProfile string, deepProfile copilot.ModelProfile, hasDeep bool, role string) deepMode {
	if !deep {
		return deepMode{}
	}
	d := deepMode{On: true}
	if strings.TrimSpace(explicitProfile) == "" && hasDeep && deepProfile.ID != "" &&
		copilot.ProfileRoleAllowed(deepProfile, role) {
		d.Profile = deepProfile.ID
	}
	return d
}

// chatDeepMode — isteğin Derin kararı (kapalıyken profil okuması bile yok).
func (s *Server) chatDeepMode(r *http.Request, deep bool, explicitProfile string) deepMode {
	if !deep {
		return deepMode{}
	}
	role := ""
	if c := auth.FromContext(r.Context()); c != nil {
		role = c.Role
	}
	var p copilot.ModelProfile
	ok := false
	if s != nil && s.copilot != nil {
		p, ok = s.copilot.DeepProfile()
	}
	return deriveDeepMode(deep, explicitProfile, p, ok, role)
}

type deepModeKey struct{}

// withDeepMode — Derin açıksa kararı ctx'e koyar, completion bütçesini
// büyütür ve (açık seçim yoksa) derin profili uygular. Kapalıyken ctx AYNEN döner.
func withDeepMode(ctx context.Context, d deepMode) context.Context {
	if !d.On {
		return ctx
	}
	ctx = context.WithValue(ctx, deepModeKey{}, d)
	ctx = copilot.WithDeepAnswer(ctx)
	if d.Profile != "" {
		ctx = copilot.WithProfile(ctx, d.Profile)
	}
	return ctx
}

// deepModeFrom — ctx'teki karar; yoksa sıfır değer (kapalı).
func deepModeFrom(ctx context.Context) deepMode {
	if ctx == nil {
		return deepMode{}
	}
	d, _ := ctx.Value(deepModeKey{}).(deepMode)
	return d
}
