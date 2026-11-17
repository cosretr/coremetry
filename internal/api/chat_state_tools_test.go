package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_state_tools_test.go — v0.10.2326 (karar 3): v2 + durum açıkken bağlam
// araçları katalogda yok ve çekirdek onları anmıyor; durum kapalıyken ve v1'de
// katalog + çekirdek aynen. Ölçüm: aynı tur, aynı ekran; fark = istem + katalog.

// firstLoopBody — rig'in ilk araçlı model çağrısının sistem metni ve araç JSON'u.
func firstLoopBody(t *testing.T, on bool) (system string, tools []byte, names []string) {
	t.Helper()
	setConversationState(t, on)
	rg := newStateRig(t, referansLoop, referansGuided)
	rg.s.copilot.SetIntentClassify(copilot.IntentOn) // iki kipte de aynı yol: "none" → döngü
	rg.ask("checkout'ta sabah bir şey mi oldu", nil)
	for _, c := range rg.llm.calls() {
		raw, ok := c["tools"].([]any)
		if !ok {
			continue
		}
		b, _ := json.Marshal(raw)
		for _, x := range raw {
			fn, _ := x.(map[string]any)["function"].(map[string]any)
			n, _ := fn["name"].(string)
			names = append(names, n)
		}
		return llmSystem(c), b, names
	}
	t.Fatal("araçlı model çağrısı yok")
	return "", nil, nil
}

func TestV2ContextToolsLeaveCatalogue(t *testing.T) {
	sysOff, toolsOff, namesOff := firstLoopBody(t, false)
	sysOn, toolsOn, namesOn := firstLoopBody(t, true)
	has := func(ns []string, n string) bool {
		for _, x := range ns {
			if x == n {
				return true
			}
		}
		return false
	}
	for _, n := range []string{"set_context", "get_context", "clear_context"} {
		if !has(namesOff, n) {
			t.Fatalf("durum kapalı v2 kataloğunda %s olmalı (bugünkü hâl)", n)
		}
		if has(namesOn, n) {
			t.Fatalf("durum açık v2 kataloğunda %s olmamalı", n)
		}
	}
	if len(namesOff)-len(namesOn) != 3 {
		t.Fatalf("yalnız üç ad düşmeli: %d → %d", len(namesOff), len(namesOn))
	}
	if !strings.Contains(sysOff, "set_context") || strings.Contains(sysOn, "set_context") || strings.Contains(sysOn, "get_context") {
		t.Fatal("çekirdek: kapalıda cümle var, açıkta yok olmalı")
	}
	// Ölçüm (3 B ≈ 1 token). İstem farkı analitik: tur içi önsözler (durum bloğu,
	// AKTİF SOHBET BAĞLAMI) iki kipte farklı dolduğundan uçtan uca istem farkı
	// karışıktır; çekirdekten ve önsöz başlığından çıkan bayt kesin.
	core := len(copilot.SystemPromptChatAgentLoopFor(false)) - len(copilot.SystemPromptChatAgentLoopV2For(false))
	pre := len(" (set_context ile güncellenir)")
	t.Logf("istem: çekirdek −%d B, önsöz başlığı −%d B (önsöz varken) → −%d B ≈ −%d token/çağrı", core, pre, core+pre, (core+pre)/3)
	t.Logf("katalog: kapalı %d B, açık %d B → −%d B ≈ −%d token/çağrı", len(toolsOff), len(toolsOn), len(toolsOff)-len(toolsOn), (len(toolsOff)-len(toolsOn))/3)
	if core <= 0 || len(toolsOff) <= len(toolsOn) {
		t.Fatal("ölçüm: düşüş yok")
	}
	_, _ = sysOff, sysOn
}

// TestV1CatalogueKeepsContextTools — v1 (durum açık olsa da) kataloğu ve
// çekirdeği bağlam araçlarını taşır.
func TestV1CatalogueKeepsContextTools(t *testing.T) {
	setConversationState(t, true)
	llm := newLoopLLM(t, func(_ int, body map[string]any) map[string]any {
		if _, ok := body["tools"]; !ok {
			return answerMsg(`{"intent":"none"}`)
		}
		return answerMsg("tamam")
	})
	s, _ := newLoopTestServer(t, llm, "u-v1")
	s.l1, s.stats = newL1Cache(64), newCacheStats()
	prev := guidedDispatchSeam
	guidedDispatchSeam = func(guidedRoute) bool { return true }
	t.Cleanup(func() { guidedDispatchSeam = prev })
	s.copilot.SetIntentClassify(copilot.IntentOn) // "none" → serbest döngü (v1)
	postLoopChat(t, s, context.Background(), "u-v1", map[string]any{"messages": []map[string]any{{"role": "user", "text": "bu konuda ne düşünüyorsun acaba"}}})
	for _, c := range llm.calls() {
		raw, ok := c["tools"]
		if !ok {
			continue
		}
		b, _ := json.Marshal(raw)
		for _, n := range []string{`"set_context"`, `"get_context"`, `"clear_context"`} {
			if !strings.Contains(string(b), n) {
				t.Fatalf("v1 kataloğunda %s yok", n)
			}
		}
		if !strings.Contains(llmSystem(c), "set_context ile değişeni yaz") {
			t.Fatal("v1 çekirdeği bağlam aracı cümlesini kaybetti")
		}
		return
	}
	t.Fatal("v1 döngüye girmedi")
}
