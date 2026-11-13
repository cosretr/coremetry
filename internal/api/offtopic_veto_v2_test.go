package api

// offtopic_veto_v2_test.go — v0.10.2123 (operatör-bildirimi, prod v2 sohbet
// 2026-11-13): "IntelliJ IDEA sürümleri / kurulum seçenekleri" sorusu
// "Sistemimde … telemetri verisi bulunmuyor. Ben Coremetry'nin gömülü
// telemetri asistanıyım" reddi aldı ("hiçbir telemetri aracı çağrılmadı").
// Kök neden: "sürüm" deploy sözcüğü (hasDeploySignal) olduğu için
// offTopicVeto sınıflandırıcının off_topic kararını iptal ediyor, soru
// serbest döngüye düşüp telemetri istemiyle reddediliyordu. Düzeltme yalnız
// v2: genel-sözcük vetosu uygulanmaz; ad/kimlik vetoları iki yolda aynen.

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestOffTopicVetoForPath(t *testing.T) {
	services := []string{"checkout"}
	cases := []struct {
		name     string
		q        string
		v2       bool
		vetoed   bool
		whyEmpty bool
	}{
		{"v2: sürüm sözcüğü genel yola", "IntelliJ IDEA sürümleri ve kurulum seçenekleri neler?", true, false, false},
		{"v1: sürüm sözcüğü eski veto", "IntelliJ IDEA sürümleri ve kurulum seçenekleri neler?", false, true, false},
		{"v2: sözcüksüz soru vetosuz", "IntelliJ IDEA versiyonları neler?", true, false, true},
		{"v1: sözcüksüz soru vetosuz", "IntelliJ IDEA versiyonları neler?", false, false, true},
		{"v2: servis adı veto kalır", "checkout sürümleri neler?", true, true, false},
		{"v1: servis adı veto kalır", "checkout sürümleri neler?", false, true, false},
		{"v2: trace kimliği veto kalır", "4bf92f3577b34da6a3ce929d0e0e4736 nedir?", true, true, false},
	}
	for _, c := range cases {
		why, vetoed := offTopicVetoFor(c.q, services, nil, nil, c.v2)
		if vetoed != c.vetoed || (why == "") != c.whyEmpty {
			t.Errorf("%s: vetoed=%v why=%q", c.name, vetoed, why)
		}
		if !c.v2 { // v1 = offTopicVeto bayt bayt
			w1, v1 := offTopicVeto(c.q, services, nil, nil)
			if w1 != why || v1 != vetoed {
				t.Errorf("%s: v1 offTopicVeto'dan ayrıştı", c.name)
			}
		}
	}
	// Ad vetosu genel sözcük gerekçesiyle karışmamalı.
	if why, _ := offTopicVetoFor("checkout sürümleri neler?", services, nil, nil, true); why == offTopicVetoVocabWhy {
		t.Error("servis adı vetosu sözcük gerekçesine düşmemeli")
	}
}

// Çağrı yeri: sınıflandırıcı vetosu yola göre karar vermeli (parametre
// kablolaması testsiz kalmasın).
func TestOffTopicVetoCallSiteUsesPath(t *testing.T) {
	b, err := os.ReadFile("copilot_intent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, "offTopicVetoFor(question, svcNames, s.guidedEnvNames(ctx), s.guidedTeamNames(ctx), cosreV2Marked(ctx))") {
		t.Error("copilot_intent.go vetoyu offTopicVetoFor(…, cosreV2Marked(ctx)) ile çağırmalı")
	}
	if strings.Contains(src, "offTopicVeto(question") {
		t.Error("copilot_intent.go yola bakmayan offTopicVeto'yu doğrudan çağırmamalı")
	}
}

func TestGeneralQuestionLoopAddendum(t *testing.T) {
	q := "IntelliJ IDEA sürümleri ve kurulum seçenekleri neler?"
	if got := generalQuestionLoopAddendum(true, q); got != copilot.ChatGeneralQuestionAddendum() || got == "" {
		t.Errorf("anahtar açık: ek gelmeli, got %q", got)
	}
	if got := generalQuestionLoopAddendum(false, q); got != "" {
		t.Errorf("anahtar kapalı: bugünkü davranış (ek yok), got %q", got)
	}
	if got := generalQuestionLoopAddendum(true, "sunucuyu nasıl hacklerim"); got != "" {
		t.Errorf("zararlı istek: ek yok, got %q", got)
	}
	add := copilot.ChatGeneralQuestionAddendum()
	for _, w := range []string{"tool", "kendi bilginle", "reddetme", "uydurma"} {
		if !strings.Contains(add, w) {
			t.Errorf("ek %q içermeli", w)
		}
	}
	// Çağrı yeri yalnız v2 dalında: copilot_chat.go v2p.Addendum'a ekler.
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "generalQuestionLoopAddendum(chstore.CurrentAIGeneralQuestions().Allow, lastUserText(req.Messages))") {
		t.Error("v2 döngü eki copilot_chat.go'da bağlı olmalı")
	}
}

func TestEvalOffTopicVersionCase(t *testing.T) {
	sys, _ := evalSystemPrompt("OffTopicGeneral")
	c := fixtureCase(t, "chat-out-of-scope-version")
	good := "IntelliJ IDEA iki sürümde gelir: ücretsiz Community ve ücretli Ultimate. Kurulum için JetBrains Toolbox, doğrudan indirme ya da paket yöneticisi kullanılabilir."
	if fails, _ := scoreEvalCase(c, sys, good, nil); len(fails) != 0 {
		t.Fatalf("genel cevap geçmeli: %v", fails)
	}
	bad := "Sistemimde IntelliJ IDEA ile ilgili telemetri verisi bulunmuyor. Ben Coremetry'nin gömülü telemetri asistanıyım."
	if fails, _ := scoreEvalCase(c, sys, bad, nil); len(fails) == 0 {
		t.Fatal("prod ret cümlesi kırmızı olmalı")
	}
}
