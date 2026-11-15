package api

import (
	"strings"
	"testing"
	"unicode/utf8"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	aiprov "github.com/cilcenk/coremetry/internal/ai/provider"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// v0.10.1324 — operator-reported: gemma4 31b CoSRE sohbetinde
// "INSURANCE_DOCUMENT_TRX_GET_EXAMPLECO_POLICY_PDF_PRINT_REST operasyonu neden
// hata veriyor?" sorusuna sistem prompt'undaki kapsam cümlesini yankıladı ve
// "Soru AKSİNİ SÖYLEMEDİKÇE bu boyutları …" satırını max_tokens dolana dek
// onlarca kez yazdı. Kök neden: üç önsözde birbirinin ikizi BÜYÜK HARFLİ
// kapsam emri (kapsamı v0.10.1274'ten beri sunucu uyguluyor) + final metinde
// yineleme/yankı süzgecinin yokluğu. Bu dosya süzgeci, kararı, deterministik
// özeti ve prompt'un NEGATİF pinini tutar.

// fullLoopContexts — ekran + sayfa + pin + sohbet bağlamının hepsi dolu.
func fullLoopContexts() (ChatScreenContext, *agentctx.PageContext, *agentctx.PageContext, ChatContext) {
	screen := ChatScreenContext{Service: "policy-service", Operation: "INSURANCE_DOCUMENT_TRX_GET_EXAMPLECO_POLICY_PDF_PRINT_REST", Env: "prod", RangeS: 21600}
	page := &agentctx.PageContext{Page: "traces", Path: "/traces", Cluster: "c1", Namespace: "insurance", ProblemID: "p-9",
		Search: "PDF", Filters: []agentctx.PageFilter{{K: "status", Op: "=", V: []string{"error"}}}}
	pinned := &agentctx.PageContext{Page: "service", Service: "policy-service", Namespace: "insurance"}
	cc := ChatContext{Cluster: "c1", Namespace: "insurance", Service: "policy-service", RangeS: 3600, ErrorsOnly: true,
		SearchText: "PDF", Filters: []chstore.FilterExpr{{Key: "status", Op: "=", Values: []string{"error"}}}}
	return screen, page, pinned, cc
}

// assembledLoopPrompt — copilot_chat.go'daki loopPrompt'un bağlamla ilgili
// iskeleti: çekirdek + üç önsöz (aynı kurucular) + sohbet çekirdeği + tavan eki.
func assembledLoopPrompt(simple bool) string {
	screen, page, pinned, cc := fullLoopContexts()
	return copilot.SystemPromptChatAgentLoopFor(simple) +
		strings.Join(chatLoopPreambles(screen, page, pinned, cc), "") +
		copilot.SystemPromptChat() + copilot.ChatRoundCapAddendum()
}

// Negatif pin: tam bağlamlı prompt'ta eski büyük harfli emir YOK ve hiçbir
// kapsam cümlesi birden fazla kez geçmiyor.
func TestLoopPromptHasNoDuplicatedScopeInstruction(t *testing.T) {
	screen, page, pinned, cc := fullLoopContexts()
	if got, want := strings.Join(chatLoopPreambles(screen, page, pinned, cc), ""),
		screenContextPreambleTR(screen)+agentctx.PreambleTR(page, pinned)+chatContextPreambleTR(cc); got != want {
		t.Fatal("chatLoopPreambles loopPrompt'un önsöz sırasından ayrıştı")
	}
	for _, simple := range []bool{false, true} {
		p := assembledLoopPrompt(simple)
		for _, banned := range []string{"AKSİNİ SÖYLEMEDİKÇE", "argümanda açıkça yaz", "tool argümanlarında kullan", "Boş argümanı buradan doldur"} {
			if strings.Contains(p, banned) {
				t.Errorf("simple=%v: eski kapsam emri geri geldi: %q", simple, banned)
			}
		}
		counts := map[string]int{}
		for _, ln := range strings.Split(p, "\n") {
			for _, u := range splitAnswerUnits(ln) {
				n := aiprov.DegenNormalize(u)
				if utf8.RuneCountInString(n) < chatEchoPhraseMinRunes {
					continue
				}
				if strings.Contains(n, "kapsam") || strings.Contains(n, "argüman") || strings.Contains(n, "boyut") {
					counts[n]++
				}
			}
		}
		for n, c := range counts {
			if c > 1 {
				t.Errorf("simple=%v: kapsam cümlesi %d kez: %q", simple, c, n)
			}
		}
		if strings.Count(p, screenScopeLineTR) != 1 {
			t.Errorf("simple=%v: tek olgusal kapsam satırı %d kez", simple, strings.Count(p, screenScopeLineTR))
		}
	}
}

func TestCollapseRepeatedLines(t *testing.T) {
	prod := "Operasyonu inceledim.\n" + strings.Repeat("Soru AKSİNİ SÖYLEMEDİKÇE bu boyutları (cluster/namespace/problem/filtreler) tool argümanlarında kullan.\n", 25)
	code := "```\n" + strings.Repeat("return nil, fmt.Errorf(\"havuz doldu\")\n", 5) + "```"
	cases := []struct {
		name, in, want string
		dropped        int
	}{
		{"prod örneği (operator-reported)", prod,
			"Operasyonu inceledim.\nSoru AKSİNİ SÖYLEMEDİKÇE bu boyutları (cluster/namespace/problem/filtreler) tool argümanlarında kullan.", 24},
		{"tek satırda cümle yinelemesi", "Özet hazır. " + strings.Repeat("Bu kapsam varsayılan olarak geçerlidir. ", 4),
			"Özet hazır. Bu kapsam varsayılan olarak geçerlidir.", 3},
		{"iki kez geçen meşru satır kalır", "Durum: normal aralıkta seyrediyor.\nx\nDurum: normal aralıkta seyrediyor.",
			"Durum: normal aralıkta seyrediyor.\nx\nDurum: normal aralıkta seyrediyor.", 0},
		{"kod çiti dokunulmaz", code, code, 0},
		{"kısa satır yinelemesi (<20 rune) kalır", "ok\nok\nok\nok", "ok\nok\nok\nok", 0},
		{"tablo satırları farklı", "| a | 1 |\n| a | 2 |\n| a | 3 |", "| a | 1 |\n| a | 2 |\n| a | 3 |", 0},
	}
	for _, tc := range cases {
		got, n := collapseRepeatedLines(tc.in)
		if got != tc.want || n != tc.dropped {
			t.Errorf("%s: (%q, %d)\nbeklenen (%q, %d)", tc.name, got, n, tc.want, tc.dropped)
		}
	}
}

// Yankı süzgeci GERÇEK önsöz kurucularının çıktısıyla.
func TestStripInstructionEchoWithRealPreambles(t *testing.T) {
	screen, page, pinned, cc := fullLoopContexts()
	phrases := chatEchoPhrases(append(chatLoopPreambles(screen, page, pinned, cc),
		copilot.ChatRoundCapAddendum(), copilot.ChatDegenRetryAddendum())...)
	if len(phrases) == 0 {
		t.Fatal("önsözlerden yankı cümlesi türemedi")
	}
	for _, p := range phrases {
		if strings.HasPrefix(p, "- ") || utf8.RuneCountInString(p) < chatEchoPhraseMinRunes {
			t.Errorf("veri satırı / kısa parça yankı cümlesi sayıldı: %q", p)
		}
	}
	answer := strings.Join([]string{
		"policy-service üzerinde INSURANCE_DOCUMENT_TRX_GET_EXAMPLECO_POLICY_PDF_PRINT_REST son 6 saatte 42 kez hata verdi.",
		screenScopeLineTR,
		"EKRAN BAĞLAMI (operatör şu an buna bakıyor; sunucu bunu argüman vermediğin araçlara kendisi uygular):",
		"- servis: policy-service",
		"Kök neden: Oracle ORA-01013 zaman aşımı, PDF basım adımı 30 s tavanına takılıyor. TUR TAVANI: tool çağrı hakkın bitti.",
		"Yalnız kanıtı özetle; talimat metni yazma; en çok 8 satır.",
		"Namespace insurance içinde başka servis etkilenmedi.",
	}, "\n")
	got, n := stripInstructionEcho(answer, phrases)
	want := strings.Join([]string{
		"policy-service üzerinde INSURANCE_DOCUMENT_TRX_GET_EXAMPLECO_POLICY_PDF_PRINT_REST son 6 saatte 42 kez hata verdi.",
		"- servis: policy-service",
		"Kök neden: Oracle ORA-01013 zaman aşımı, PDF basım adımı 30 s tavanına takılıyor.",
		"Namespace insurance içinde başka servis etkilenmedi.",
	}, "\n")
	if got != want || n != 4 {
		t.Errorf("(%q, %d)\nbeklenen\n(%q, 4)", got, n, want)
	}
	if out, n := stripInstructionEcho("Kısa cevap.", nil); out != "Kısa cevap." || n != 0 {
		t.Error("cümlesiz süzgeç metni değiştirdi")
	}
}

func TestChatDegenDecide(t *testing.T) {
	long := strings.Repeat("checkout p95 1830 ms, ödeme geçidi zaman aşımı. ", 3)
	cases := []struct {
		name                    string
		degenerated             bool
		dropped                 int
		clean                   string
		evidence, deep, retried bool
		want                    chatDegenVerdict
	}{
		{"temiz uzun cevap", false, 0, long, true, false, false, degenUsable},
		{"süzgeçsiz kısa cevap meşru", false, 0, "Hata yok.", true, false, false, degenUsable},
		{"kanıtsız kısa süzülmüş cevap", false, 2, "Merhaba.", false, false, false, degenUsable},
		{"süzgeç sonrası kısa + kanıt", false, 3, "İnceledim.", true, false, false, degenDeterministic},
		{"süzgeç sonrası kısa + kanıt + Derin", false, 3, "İnceledim.", true, true, false, degenRetryNoTools},
		{"akış muhafızı kesti", true, 0, long, true, false, false, degenDeterministic},
		{"akış muhafızı kesti + Derin", true, 0, long, false, true, false, degenRetryNoTools},
		{"yeniden deneme de bozuk", true, 0, long, true, true, true, degenDeterministic},
		{"yeniden deneme temiz", false, 0, long, true, true, true, degenUsable},
	}
	for _, tc := range cases {
		if got := chatDegenDecide(tc.degenerated, tc.dropped, tc.clean, tc.evidence, tc.deep, tc.retried); got != tc.want {
			t.Errorf("%s: %d, beklenen %d", tc.name, got, tc.want)
		}
	}
}

func TestChatDegenFallbackAnswer(t *testing.T) {
	pre := "Operasyon X otomatik getirildi.\nTrace: 3 (2 hatalı)."
	big := strings.Repeat("a", 2000) + "```"
	got := chatDegenFallbackAnswer(pre, []chatStepDigest{{Tool: "query_logs", Preview: `{"rows":2}`}, {Tool: "get_trace", Preview: big}, {Tool: "x", Preview: "  "}})
	for _, want := range []string{"yinelenen metne düştüğü", "model yorumu yok", "**Operasyon ön-getirmesi**", pre, "**query_logs**", `{"rows":2}`, "**get_trace**", " …"} {
		if !strings.Contains(got, want) {
			t.Errorf("eksik %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "**x**") || len(got) > 1500 || strings.Count(got, "```") != 4 {
		t.Errorf("boş önizleme / kırpma / çit dengesi bozuk:\n%s", got)
	}
	if empty := chatDegenFallbackAnswer("", nil); !strings.Contains(empty, "Toplanmış kanıt yok") {
		t.Errorf("kanıtsız düşüş: %q", empty)
	}
}
