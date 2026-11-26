package api

// chat_problem_record_test.go — v0.10.2477: Problem kapsamlı sohbetin ilk
// turu kayıttan açılır. Sözleşme: yalnız ilk tur; döngüde sentetik
// saved_investigation çağrısı + sonucu (blok aynen); guided kanıtına ek,
// kök-neden rotasının kendi kayıt bloğu varsa çift eklenmez; kayıt yoksa
// görünür çip ve blok yok (nil alıcı her yerde no-op).

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/sre"
)

func TestFirstChatTurn(t *testing.T) {
	cases := []struct {
		name string
		msgs []copilot.ChatMessage
		want bool
	}{
		{"tek soru", []copilot.ChatMessage{{Role: "user", Text: "neden?"}}, true},
		{"ikinci tur", []copilot.ChatMessage{{Role: "user", Text: "a"}, {Role: "assistant", Text: "b"}, {Role: "user", Text: "c"}}, false},
		{"boş", nil, false},
		{"iki soru", []copilot.ChatMessage{{Role: "user", Text: "a"}, {Role: "user", Text: "b"}}, false},
	}
	for _, c := range cases {
		if got := firstChatTurn(c.msgs); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}

func TestChatProblemRecordSeedAndGuided(t *testing.T) {
	var nilRec *chatProblemRecord
	msgs := []copilot.ChatMessage{{Role: "user", Text: "bu problem neden oldu?"}}
	if got := nilRec.seedScaled(msgs, 1); len(got) != 1 || nilRec.seededTools() != nil || nilRec.guidedSuffix("x") != "" {
		t.Fatal("kayıt yokken her şey aynen")
	}
	rec := &chatProblemRecord{ProblemID: "p1", Block: "KAYITLI PROBLEM ARAŞTIRMASI — problem P-1\n```text\nBULGULAR:\n```\n"}
	conv := rec.seedScaled(msgs, 1)
	if len(conv) != 3 || conv[1].ToolCalls[0].Name != problemRecordTool || conv[2].ToolResults[0].Content != rec.Block {
		t.Fatalf("döngü tohumu: %+v", conv)
	}
	if len(msgs) != 1 {
		t.Fatal("çağıranın dilimi değişmemeli")
	}
	if s := rec.guidedSuffix("SERVİS: checkout"); !strings.HasSuffix(s, rec.Block) {
		t.Fatalf("guided eki blok olmalı: %q", s)
	}
	if rec.guidedSuffix("KAYITLI ARAŞTIRMA (14:05) — AI SRE ajanı") != "" {
		t.Fatal("kök-neden rotası kendi kayıt bloğunu taşıyorsa çift eklenmez")
	}
	ctx := withChatProblemRecord(context.Background(), rec)
	if chatProblemRecordFrom(ctx) != rec || chatProblemRecordFrom(context.Background()) != nil {
		t.Fatal("ctx taşıyıcısı")
	}
}

func TestProblemRecordStepTR(t *testing.T) {
	if problemRecordStepTR(true, sre.StateDone) != "kayıtlı araştırmadan başlandı" {
		t.Fatal("bulundu çipi")
	}
	if !strings.Contains(problemRecordStepTR(false, sre.StateRunning), "sürüyor") {
		t.Fatal("koşan araştırma çipi")
	}
	if !strings.Contains(problemRecordStepTR(false, ""), "kayıtlı araştırma yok") {
		t.Fatal("kayıt yok çipi")
	}
}
