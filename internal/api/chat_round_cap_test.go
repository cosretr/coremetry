package api

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// v0.10.806 (dış skill denetimi L2) — tavan turu döngü prompt'unun devamı:
// önek bayt-aynı (sağlayıcı önek önbelleği isabet eder) ve ekran/sayfa/
// sohbet bağlamları tavan turunda da görünür. Eskiden
// withAddressee(hitap, SystemPromptChatRoundCap()) bambaşka bir prompt'tu.
func TestChatRoundCapExtendsLoopPrompt(t *testing.T) {
	chat, cap, add := copilot.SystemPromptChat(), copilot.SystemPromptChatRoundCap(), copilot.ChatRoundCapAddendum()
	if cap != chat+add {
		t.Fatal("SystemPromptChatRoundCap = SystemPromptChat + ChatRoundCapAddendum olmalı (dil kapısı + eski sözleşme)")
	}
	if !strings.Contains(add, "TUR TAVANI") || strings.Contains(add, "Sen Coremetry") {
		t.Error("ek yalnız tavan yönergesi olmalı, çekirdek metni içermemeli")
	}
	src, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "capPrompt := loopPrompt + copilot.ChatRoundCapAddendum()") {
		t.Error("tavan prompt'u döngü prompt'u + ek olarak kurulmuyor")
	}
	if strings.Contains(s, "withAddressee(addressee, copilot.SystemPromptChatRoundCap())") {
		t.Error("eski tavan prompt'u (bağlamsız, farklı önek) geri gelmiş")
	}
}
