package copilot

// deep.go — v0.10.1150 "Derin düşün": sohbet composer'ının Derin anahtarı
// açıkken alışverişin completion bütçesi %50 büyür (daha uzun cevap). Kapalıyken
// ctx'te işaret yoktur ve her çağrı gövdesi bayt bayt eskisidir.
//
// Tavanlar: maxMaxTokens (tuning aralığının üstü; modelin belgelenmiş çıktı
// tavanı daha büyükse o — v0.10.2375) ve model penceresi biliniyorsa pencerenin
// yarısı — girdiye yer kalsın. Hiçbir zaman tabanın altına inmez.

import (
	"context"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
)

// ChatDeepLoopBudget — Derin kipte serbest döngünün araç bütçesi satırı
// (chatDeepLoopBudget, prompts.go; sayılar kodun gerçek tavanı).
func ChatDeepLoopBudget(calls, rounds int) string {
	return fmt.Sprintf(chatDeepLoopBudget, calls, rounds)
}

// wikiSelectPickRule — systemWikiSelect'in seçim aralığı; Derin varyant
// yalnız üst sınırı değiştirir (geri kalanı bayt bayt aynı prompt).
const wikiSelectPickRule = "1–5 adayı seç"

// SystemPromptWikiSelectDeep — Derin kipte sayfa seçimi: en çok maxPick aday.
func SystemPromptWikiSelectDeep(maxPick int) string {
	return strings.Replace(systemWikiSelect, wikiSelectPickRule, fmt.Sprintf("1–%d adayı seç", maxPick), 1)
}

// DeepAnswerFactor — Derin kipte completion bütçesinin çarpanı.
const DeepAnswerFactor = 1.5

type deepAnswerKey struct{}

// WithDeepAnswer — bu alışverişin çağrıları Derin kipte (daha büyük completion bütçesi).
func WithDeepAnswer(ctx context.Context) context.Context {
	return context.WithValue(ctx, deepAnswerKey{}, true)
}

// WithoutDeepAnswer — v0.10.1458: bu çağrı alışverişin Derin kipini YOK sayar
// (konu dışı genel cevap kısa kalır; bütçe profilin tabanı). Derin değilse ctx aynen.
func WithoutDeepAnswer(ctx context.Context) context.Context {
	if !DeepAnswerFrom(ctx) {
		return ctx
	}
	return context.WithValue(ctx, deepAnswerKey{}, false)
}

// DeepAnswerFrom — ctx Derin kipte mi.
func DeepAnswerFrom(ctx context.Context) bool {
	v, _ := ctx.Value(deepAnswerKey{}).(bool)
	return v
}

// DeepMaxTokens — SAF: Derin kipte completion bütçesi = taban × 1.5, en çok
// maxMaxTokens ve (pencere biliniyorsa) pencerenin yarısı; tabanın altına inmez.
func DeepMaxTokens(base, windowTokens int) int {
	return deepMaxTokensWithin(base, modelcaps.MaxTokensCeiling(modelcaps.Caps{}, windowTokens, maxMaxTokens))
}

// deepMaxTokensWithin — SAF: taban × 1.5, en çok ceiling; tabanın altına inmez.
func deepMaxTokensWithin(base, ceiling int) int {
	if base <= 0 {
		return base
	}
	scaled := int(float64(base) * DeepAnswerFactor)
	return max(base, min(scaled, ceiling))
}

// deepMaxTokensFor — ctx Derin değilse base aynen döner. v0.10.2375: tavan
// model-farkında (çıktı tavanı bilinen modelde 32768 yerine o) ve pencere
// çağıranın YÜRÜRLÜKTEKİ penceresi (cachedContextWindow: sunucudan okunmuşsa
// o, yoksa ad tablosu) — eskiden yalnız ad tablosuydu.
func deepMaxTokensFor(ctx context.Context, base int, model string, window int) int {
	if ctx == nil || !DeepAnswerFrom(ctx) {
		return base
	}
	return deepMaxTokensWithin(base, modelcaps.MaxTokensCeiling(modelcaps.For(model), window, maxMaxTokens))
}
