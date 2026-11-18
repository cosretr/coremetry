package api

// chat_state_tools.go — v0.10.2326 (conversation-audit §7 karar 3): sunucu
// konuşma durumunu kendisi yazdığında (chat_state.go) set_context /
// get_context / clear_context v2 kataloğundan çıkar ve çekirdek istem onları
// anmaz. Kapı chatStateOn: v1 ve "durum kapalı" v2 kataloğu + istemi bayt
// bayt aynı. Trace takibinde yalnız bu üç ad düşer; log/trace yorum araçları
// (traceFollowUpToolsFor) değişmez.

import (
	"context"
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// chatContextToolNames — v2 durum açıkken sunulmayan araçlar.
var chatContextToolNames = map[string]bool{"set_context": true, "get_context": true, "clear_context": true}

// dropChatContextTools — SAF: sıra korunur.
func dropChatContextTools(tools []mcp.Tool) []mcp.Tool {
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if !chatContextToolNames[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// chatAgentLoopCore — döngü çekirdeği: v2 + durum açıkken bağlam aracı cümlesiz;
// v0.10.2337 — üslup açık + ölçülü sınıfta biçim kuralsız varyant.
func chatAgentLoopCore(ctx context.Context, simple bool) string {
	if answerStyleFrom(ctx).styled() { // v0.10.2337 — C: örnekle üslup varyantı (chat_style_prompt.go)
		return copilot.SystemPromptChatAgentLoopStyleFor(chatStateOn(ctx), simple)
	}
	if chatStateOn(ctx) {
		return copilot.SystemPromptChatAgentLoopV2For(simple)
	}
	return copilot.SystemPromptChatAgentLoopFor(simple)
}

// chatContextPreambleFor — AKTİF SOHBET BAĞLAMI önsözü; v2 + durum açıkken
// başlıktaki "(set_context ile güncellenir)" düşer.
func chatContextPreambleFor(ctx context.Context, c ChatContext) string {
	p := chatContextPreambleTR(c)
	if chatStateOn(ctx) {
		p = strings.Replace(p, " (set_context ile güncellenir)", "", 1)
	}
	return p
}
