package copilot

// prompt_quote_verbatim_test.go — v0.10.2461 (operatör-bildirimli, 2026-11-22):
// trace açıklaması span'in İngilizce exception mesajını yazdı, kullanıcının
// gördüğü Türkçe log metni değil. Alıntı kuralı (tek yazım) kanıtı alıntılayan
// her anlatım isteminde; iki model aynı metni alır (model dalı yok).

import (
	"strings"
	"testing"
)

func TestQuoteVerbatimRuleInPrompts(t *testing.T) {
	for _, must := range []string{"BİREBİR alıntıla, çevirme", "log satırındaki metni (kullanıcının gördüğü) tercih et", "Yanıtın dili Türkçe."} {
		if !strings.Contains(quoteVerbatimRule, must) {
			t.Fatalf("quoteVerbatimRule %q taşımalı", must)
		}
	}
	prompts := map[string]string{
		"trace":           SystemPromptTrace(),
		"traceFollowUp":   TraceFollowUpAddendum(),
		"traceFollowUp.s": TraceFollowUpAddendumFor(true),
		"chat":            SystemPromptChat(),
		"chatRoundCap":    SystemPromptChatRoundCap(),
		"agentLoop":       SystemPromptChatAgentLoopFor(false),
		"agentLoop.s":     SystemPromptChatAgentLoopFor(true),
		"agentLoopV2":     SystemPromptChatAgentLoopV2For(false),
		"guided":          SystemPromptGuidedChat(),
	}
	for name, p := range prompts {
		if !strings.Contains(p, quoteVerbatimRule) {
			t.Errorf("%s alıntı kuralını taşımalı", name)
		}
	}
	// Trace istemi: exception adımı log metnini önce ister.
	if !strings.Contains(SystemPromptTrace(), `(kanıtta "log:" metni de varsa önce onu)`) {
		t.Error("trace istemi 4. adımda log metnini önce istemeli")
	}
}
