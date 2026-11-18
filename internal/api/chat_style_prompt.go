package api

// chat_style_prompt.go — v0.10.2337 (docs/cosre/conversation-audit.md §6.1 C):
// ÖRNEKLE ÜSLUP. Bulgu sabit, cümle serbest: araçlar sayıyı ve kanıtı verir,
// model onları doğal Türkçeyle anlatır.
//
// Kapı B ile aynı: answerStyleOn (v2 VE cosre_conversation.style; değerlendirme
// koşusunun zorlaması dahil) VE turun sınıfı ölçülü (answerClassLines'ta satırı
// var). Rapor türleri (servis raporu, kaynak incelemesi, trace / span /
// request-id açıklaması, rota incelemesi, açıklama turu, Derin) sınıf `report`
// olduğundan bugünkü istemi alır. Kapalıyken her istem bayt bayt eski.
//
// Yerleşim (önek önbelleği, §6.0): kararlı metin önde (üslup varyantı +
// çekirdek), değişken bloklar sonda — durum bloğu, seçilen örnekler, sınıf
// satırı. Örnekler sınıf + hitapla styledialogs.Select'ten; ek model çağrısı yok.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/copilot/styledialogs"
)

// styledFor — üslup açık ve sınıf ölçülü mü (nil-güvenli).
func (t *answerStyleTurn) styledFor(c answerClass) bool {
	return t != nil && t.on && answerClassLines[c] != ""
}

// styled — turun not edilmiş sınıfıyla styledFor.
func (t *answerStyleTurn) styled() bool {
	c, _ := t.snapshot()
	return t.styledFor(c)
}

// address — konuşma durumunun hitabı ("" = durum kapalı / bilinmiyor → sen).
func (t *answerStyleTurn) address() string {
	if t == nil || t.stt == nil || t.stt.st == nil {
		return ""
	}
	return t.stt.st.ctx.Address
}

// styleExamples — sınıf + hitap + kademe → örnek bloğu.
func (t *answerStyleTurn) styleExamples(c answerClass, mode styledialogs.Mode) string {
	return copilot.StyleExamplesBlock(styledialogs.Texts(styledialogs.Select(string(c), t.address(), mode)))
}

// loopChatPromptFor — döngünün systemChat'i: üslup açık + ölçülü sınıfta
// biçim kuralsız varyant (+ üslup çekirdeği); aksi hâlde bugünkü metin.
func loopChatPromptFor(ctx context.Context) string {
	if answerStyleFrom(ctx).styled() {
		return copilot.SystemPromptChatStyle()
	}
	return copilot.SystemPromptChat()
}

// answerStyleGuidedPrompt — guided anlatımın sistem metni: üslup açık +
// ölçülü sınıfta madde talimatsız varyant + 1 örnek (sınıf satırı çağıranda
// en sona eklenir); rapor anlatıcıları ve kapalıyken guidedNarrationPrompt aynen.
func answerStyleGuidedPrompt(ctx context.Context, intent guidedIntent) string {
	base := guidedNarrationPrompt(intent)
	c := answerClassOfIntent(intent)
	t := answerStyleFrom(ctx)
	if !t.styledFor(c) || base != copilot.SystemPromptGuidedChat() {
		return base
	}
	return copilot.SystemPromptGuidedChatStyle() + t.styleExamples(c, styledialogs.ModeGuided)
}

// answerStyleGeneralPrompt — araçsız genel cevap: üslup açık + ölçülü sınıfta
// taban + çekirdek + 1 sohbet / bilgi örneği; aksi hâlde bugünkü metin.
func answerStyleGeneralPrompt(ctx context.Context, c answerClass) string {
	t := answerStyleFrom(ctx)
	if !t.styledFor(c) {
		return copilot.SystemPromptGeneralChat()
	}
	return copilot.SystemPromptGeneralChatStyle() + t.styleExamples(c, styledialogs.ModeGeneral)
}
