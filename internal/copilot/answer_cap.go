package copilot

// answer_cap.go — v0.10.2331 (docs/cosre/conversation-audit.md §6.1 B):
// cevap sınıfının çıktı tavanı. WithMaxTokensCap'in ikizi; tek fark: çözülen
// modelin düşünme fazı AÇIKSA (modelcaps.Reasoning ve profil thinking "off"
// değil) tavan UYGULANMAZ — düşünme tokenları aynı completion bütçesinden
// yenir, kısa bir uzunluk tavanı cevabı hiç yazdırmadan bitirebilirdi.
// Aile adı aranmaz: karar modelcaps yeteneğinden (Reasoning) gelir.
//
// İki tavan birlikte varsa (yineleme düşüşü 700 + sınıf tavanı) küçüğü
// kazanır: önce cappedMaxTokens, sonra bu.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
)

type answerCapKey struct{}

// WithAnswerCap — bu çağrının completion bütçesine cevap sınıfı tavanı.
// n ≤ 0 → ctx aynen.
func WithAnswerCap(ctx context.Context, n int) context.Context {
	if n <= 0 {
		return ctx
	}
	return context.WithValue(ctx, answerCapKey{}, n)
}

// AnswerCapOf — ctx'teki sınıf tavanı (yoksa 0).
func AnswerCapOf(ctx context.Context) int {
	c, _ := ctx.Value(answerCapKey{}).(int)
	return c
}

// answerCappedMaxTokens — SAF: sınıf tavanı varsa ve model düşünmüyorsa
// min(bütçe, tavan); bütçe 0 (sağlayıcı varsayılanı) ise tavanın kendisi.
func answerCappedMaxTokens(ctx context.Context, maxTok int, model, thinking string) int {
	c := AnswerCapOf(ctx)
	if c <= 0 || answerCapLifted(model, thinking) {
		return maxTok
	}
	if maxTok <= 0 || maxTok > c {
		return c
	}
	return maxTok
}

// answerCapLifted — SAF: düşünen model + düşünme kapalı değil → tavan yok.
func answerCapLifted(model, thinking string) bool {
	return modelcaps.For(model).Reasoning && modelcaps.Thinking(thinking) != modelcaps.ThinkingOff
}
