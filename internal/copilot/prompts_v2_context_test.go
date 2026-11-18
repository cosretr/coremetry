package copilot

import (
	"strings"
	"testing"
)

// v0.10.2326 — v2 çekirdeği = v1 çekirdeği eksi bağlam aracı cümlesi; v1 aynen.
func TestAgentLoopV2DropsOnlyContextToolRule(t *testing.T) {
	for _, simple := range []bool{false, true} {
		v1, v2 := SystemPromptChatAgentLoopFor(simple), SystemPromptChatAgentLoopV2For(simple)
		if !strings.Contains(v1, "set_context") || !strings.Contains(v1, "get_context") {
			t.Fatal("v1 çekirdeği bağlam aracı cümlesini kaybetti")
		}
		if strings.Contains(v2, "set_context") || strings.Contains(v2, "get_context") || strings.Contains(v2, "clear_context") {
			t.Fatal("v2 çekirdeği bağlam aracını anıyor")
		}
		// v0.10.2356 — S2: + adım 4'ün find_traces cümlesi (chatAgentLoopV2Search).
		if chatAgentLoopV2Search(strings.Replace(v1, ToolsetText(chatAgentLoopContextToolRule, simple), "", 1)) != v2 {
			t.Fatal("v2 çekirdeği v1'den yalnız o cümleyle (ve find_traces cümlesiyle) ayrışmalı")
		}
	}
	if SystemPromptChatAgentLoopFor(false) != systemChatAgentLoop {
		t.Fatal("v1 çekirdek baytı değişti")
	}
}
