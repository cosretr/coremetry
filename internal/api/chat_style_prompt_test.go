package api

// chat_style_prompt_test.go — v0.10.2337 (conversation-audit §6.1 C): örnekle
// üslup istemlerinin kapısı, yerleşimi ve bayt bütçesi.

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/copilot/styledialogs"
)

// styledClasses — örnek alan (ölçülü) sınıflar.
func styledClasses() []answerClass {
	var out []answerClass
	for c, line := range answerClassLines {
		if line != "" {
			out = append(out, c)
		}
	}
	return out
}

// TestStyleClassesMatchDialogSet — ölçülü her sınıf örnek setinde tanımlı,
// setin her sınıfı ölçülü (styledialogs api'yi içe aktaramaz; eşitlik burada).
func TestStyleClassesMatchDialogSet(t *testing.T) {
	set := map[string]bool{}
	for _, c := range styledialogs.Classes {
		set[c] = true
	}
	got := styledClasses()
	if len(got) != len(set) {
		t.Fatalf("ölçülü sınıf %d, set sınıfı %d", len(got), len(set))
	}
	for _, c := range got {
		if !set[string(c)] {
			t.Errorf("sınıf %s örnek setinde yok", c)
		}
	}
}

// TestStyleLoopBudget — üslup açık v2 döngü istemi (çekirdek + systemChat
// varyantı + üslup çekirdeği + örnekler; durum bloğu ve sınıf satırı hariç)
// bugünkü v2 döngü çekirdeğinden büyük değil: her sınıf × hitap × durum ×
// araç seti için.
func TestStyleLoopBudget(t *testing.T) {
	for _, stateOn := range []bool{false, true} {
		for _, simple := range []bool{false, true} {
			before := copilot.SystemPromptChatAgentLoopFor(simple)
			if stateOn {
				before = copilot.SystemPromptChatAgentLoopV2For(simple)
			}
			before += copilot.SystemPromptChat()
			worst := 0
			for _, c := range styledClasses() {
				for _, a := range []string{"", addressSen, addressSiz} {
					ex := copilot.StyleExamplesBlock(styledialogs.Texts(styledialogs.Select(string(c), a, styledialogs.ModeLoop)))
					after := copilot.SystemPromptChatAgentLoopStyleFor(stateOn, simple) + copilot.SystemPromptChatStyle() + ex
					if len(after) > len(before) {
						t.Errorf("durum=%v simple=%v %s/%q: %d B > bugünkü %d B", stateOn, simple, c, a, len(after), len(before))
					}
					worst = max(worst, len(after))
				}
			}
			t.Logf("döngü durum=%v simple=%v: önce %d B (≈%d token), sonra en çok %d B (≈%d token)", stateOn, simple, len(before), len(before)/3, worst, worst/3)
		}
	}
	g0, g1 := len(copilot.SystemPromptGuidedChat()), 0
	n0, n1 := len(copilot.SystemPromptGeneralChat()), 0
	for _, c := range styledClasses() {
		for _, a := range []string{addressSen, addressSiz} {
			ex := copilot.StyleExamplesBlock(styledialogs.Texts(styledialogs.Select(string(c), a, styledialogs.ModeGuided)))
			g1 = max(g1, len(copilot.SystemPromptGuidedChatStyle()+ex))
			ex = copilot.StyleExamplesBlock(styledialogs.Texts(styledialogs.Select(string(c), a, styledialogs.ModeGeneral)))
			n1 = max(n1, len(copilot.SystemPromptGeneralChatStyle()+ex))
		}
	}
	t.Logf("guided: önce %d B (≈%d token), sonra en çok %d B (≈%d token)", g0, g0/3, g1, g1/3)
	t.Logf("genel: önce %d B (≈%d token), sonra en çok %d B (≈%d token)", n0, n0/3, n1, n1/3)
}

func styleTurnCtx(on bool, address string) context.Context {
	t := &answerStyleTurn{on: on}
	if address != "" {
		t.stt = &chatStateTurn{st: &chatContextState{ctx: ChatContext{Address: address}}}
	}
	return context.WithValue(withCosreV2Loop(context.Background()), answerStyleKey{}, t)
}

// baseLoopCore — bugünkü (üslupsuz) döngü çekirdeği.
func baseLoopCore(ctx context.Context, simple bool) string {
	if chatStateOn(ctx) {
		return copilot.SystemPromptChatAgentLoopV2For(simple)
	}
	return copilot.SystemPromptChatAgentLoopFor(simple)
}

// TestStyleNarrationPromptsGate — kapalıyken (ve v1'de) guided / genel / döngü
// istemleri bugünkü; açıkken varyant + örnekler; rapor türleri bugünkü.
func TestStyleNarrationPromptsGate(t *testing.T) {
	for _, ctx := range []context.Context{context.Background(), styleTurnCtx(false, "")} {
		if answerStyleGuidedPrompt(ctx, guidedServiceHealth) != copilot.SystemPromptGuidedChat() ||
			answerStyleGeneralPrompt(ctx, classChat) != copilot.SystemPromptGeneralChat() ||
			loopChatPromptFor(ctx) != copilot.SystemPromptChat() ||
			chatAgentLoopCore(ctx, false) != baseLoopCore(ctx, false) {
			t.Fatal("kapalıyken istem değişti")
		}
		if p := answerClassLoopPlan(ctx, stepPlan{}); p.SystemAddendum != "" {
			t.Fatal("kapalıyken döngü eki değişti")
		}
	}
	on := styleTurnCtx(true, "")
	g := answerStyleGuidedPrompt(on, guidedServiceHealth)
	ex := copilot.StyleExamplesBlock(styledialogs.Texts(styledialogs.Select(string(classStatus), "", styledialogs.ModeGuided)))
	if g != copilot.SystemPromptGuidedChatStyle()+ex {
		t.Fatal("guided üslup varyantı + 1 örnek bekleniyordu")
	}
	for _, in := range []guidedIntent{guidedServiceReport, guidedResourceInvestigation, guidedTraceByID, guidedSpanByID, guidedRequestID} {
		if answerStyleGuidedPrompt(on, in) != guidedNarrationPrompt(in) {
			t.Errorf("rapor türü %s bugünkü istemi almalı", in)
		}
	}
	gen := answerStyleGeneralPrompt(on, classKnowledge)
	if !strings.HasPrefix(gen, copilot.SystemPromptGeneralChatStyle()) || !strings.Contains(gen, "consumer lag") {
		t.Fatalf("genel cevap: çekirdek + bilgi örneği bekleniyordu: …%q", gen[max(0, len(gen)-120):])
	}
	// Döngü: sınıfa göre varyant; rapor ve sınıfsız tur bugünkü.
	answerStyleFrom(on).note(classErrorHunt, 768)
	if loopChatPromptFor(on) != copilot.SystemPromptChatStyle() || chatAgentLoopCore(on, true) != copilot.SystemPromptChatAgentLoopStyleFor(chatStateOn(on), true) {
		t.Fatal("açık + ölçülü sınıfta döngü varyantı bekleniyordu")
	}
	p := answerClassLoopPlan(on, stepPlan{})
	want := copilot.StyleExamplesBlock(styledialogs.Texts(styledialogs.Select(string(classErrorHunt), "", styledialogs.ModeLoop))) + answerClassLines[classErrorHunt]
	if p.SystemAddendum != want {
		t.Fatalf("döngü eki: örnekler + sınıf satırı (EN SONDA) bekleniyordu: %q", p.SystemAddendum)
	}
	answerStyleFrom(on).note(classReport, 0)
	if loopChatPromptFor(on) != copilot.SystemPromptChat() || chatAgentLoopCore(on, false) != baseLoopCore(on, false) {
		t.Fatal("rapor sınıfı bugünkü döngü istemini almalı")
	}
}

// TestStyleExamplesFollowAddress — durumun hitabı "siz" ise döngü örneklerinde
// siz diyaloğu var; sen ise hiç yok.
func TestStyleExamplesFollowAddress(t *testing.T) {
	siz := answerStyleFrom(styleTurnCtx(true, addressSiz)).styleExamples(classErrorHunt, styledialogs.ModeLoop)
	sen := answerStyleFrom(styleTurnCtx(true, addressSen)).styleExamples(classErrorHunt, styledialogs.ModeLoop)
	if !strings.Contains(siz, "Ekibiniz") || strings.Contains(sen, "Ekibiniz") {
		t.Fatalf("hitap örneğe yansımadı:\nsiz=%q\nsen=%q", siz, sen)
	}
}

// TestStyleEvalOverrideSelectsVariants — değerlendirme koşusunun üslup
// zorlaması (canlı ayar kapalıyken) varyantları da seçer.
func TestStyleEvalOverrideSelectsVariants(t *testing.T) {
	setConversationStyle(t, false)
	yes := true
	ctx := withChatEvalMode(withCosreV2Loop(context.Background()), &chatEvalMode{Conversation: &evalConvTurn{Style: &yes}})
	ctx = withAnswerStyle(ctx, nil)
	if answerStyleGuidedPrompt(ctx, guidedServiceHealth) == copilot.SystemPromptGuidedChat() {
		t.Fatal("koşu zorlaması guided varyantını seçmedi")
	}
	answerStyleFrom(ctx).note(classStatus, 384)
	if loopChatPromptFor(ctx) != copilot.SystemPromptChatStyle() {
		t.Fatal("koşu zorlaması döngü varyantını seçmedi")
	}
	if loopChatPromptFor(withAnswerStyle(withCosreV2Loop(context.Background()), nil)) != copilot.SystemPromptChat() {
		t.Fatal("zorlama üretim ctx'ine sızdı")
	}
}
