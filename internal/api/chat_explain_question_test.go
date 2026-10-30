package api

// chat_explain_question_test.go — v0.10.1645. Operator-reported: /ai
// "Açıklama turu" çekmecesinin SORU kutusu `Bu trace'i açıkla · eea6c7b0…`
// gösteriyordu — kaydedilen soru istemcinin KISA kimlikli görünür metniydi,
// hangi trace'in açıklandığı görülemiyor / kopyalanamıyordu. Kayıt (ai_calls
// etkileşim satırı + cosre_interactions.question) artık tam kimliği taşır.

import (
	"os"
	"strings"
	"testing"
)

func TestExplainRecordedQuestion(t *testing.T) {
	const tr = "0af7651916cd43dd8448eb211c80319c"
	const sp = "b7ad6b7169203331"
	cases := []struct {
		name string
		q    string
		ask  *chatExplainAsk
		want string
	}{
		{"trace", "Bu trace'i açıkla · 0af76519…", &chatExplainAsk{Kind: "trace", ID: tr},
			"Bu trace'i açıkla · " + tr},
		{"trace upper id normalised", "Bu trace'i açıkla · 0af76519…", &chatExplainAsk{Kind: "trace", ID: "0AF7651916CD43DD8448EB211C80319C"},
			"Bu trace'i açıkla · " + tr},
		{"trace code turn", "Bu trace'i kodla birlikte açıkla · 0af76519…", &chatExplainAsk{Kind: "trace", ID: tr, Code: true},
			"Bu trace'i kodla birlikte açıkla · " + tr},
		{"exception", "NullPointerException grubunu açıkla · a1b2c3d4…", &chatExplainAsk{Kind: "exception", ID: "a1b2c3d4e5f60718"},
			"NullPointerException grubunu açıkla · a1b2c3d4e5f60718"},
		{"oracle exception keeps prefix", "ORA-00001 grubunu açıkla · ora:a1b2c3d4…", &chatExplainAsk{Kind: "exception", ID: "ora:a1b2c3d4e5f60718"},
			"ORA-00001 grubunu açıkla · ora:a1b2c3d4e5f60718"},
		{"problem", "Bu problemi açıkla · p-123456…", &chatExplainAsk{Kind: "problem", ID: "p-1234567890"},
			"Bu problemi açıkla · p-1234567890"},
		{"incident", "Bu olayı açıkla · inc-0001…", &chatExplainAsk{Kind: "incident", ID: "inc-000123"},
			"Bu olayı açıkla · inc-000123"},
		{"anomaly", "Bu anomaliyi açıkla · an-00001…", &chatExplainAsk{Kind: "anomaly", ID: "an-0000123"},
			"Bu anomaliyi açıkla · an-0000123"},
		{"runbook", "Bu problem için runbook çıkar · p-123456…", &chatExplainAsk{Kind: "runbook", ID: "p-1234567890"},
			"Bu problem için runbook çıkar · p-1234567890"},
		{"span uses span id", "Bu span'i açıkla · b7ad6b71…", &chatExplainAsk{Kind: "span", Subject: "span:" + tr + ":" + sp},
			"Bu span'i açıkla · " + sp},
		// Kısa kimlik zaten tam: dokunulmaz.
		{"short id unchanged", "Bu problemi açıkla · p-1", &chatExplainAsk{Kind: "problem", ID: "p-1"}, "Bu problemi açıkla · p-1"},
		// Niyet yok / geçersiz / metin kimliği taşımıyor: bayt bayt aynı.
		{"no ask", "Bu trace'i açıkla · 0af76519…", nil, "Bu trace'i açıkla · 0af76519…"},
		{"invalid ask", "Bu trace'i açıkla · 0af76519…", &chatExplainAsk{Kind: "trace", ID: "nothex"}, "Bu trace'i açıkla · 0af76519…"},
		{"text lacks short id", "bu trace'i açıkla", &chatExplainAsk{Kind: "trace", ID: tr}, "bu trace'i açıkla"},
		{"other id prefix not rewritten", "Bu trace'i açıkla · deadbeef…", &chatExplainAsk{Kind: "trace", ID: tr}, "Bu trace'i açıkla · deadbeef…"},
		{"service-health has no short id", "checkout sağlığını açıkla", &chatExplainAsk{Kind: "service-health", Subject: "service-health:checkout:1:2"},
			"checkout sağlığını açıkla"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := explainRecordedQuestion(c.q, c.ask); got != c.want {
				t.Fatalf("explainRecordedQuestion(%q) = %q, want %q", c.q, got, c.want)
			}
		})
	}
}

func TestShortExplainIDMirrorsClient(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0af7651916cd43dd8448eb211c80319c", "0af76519…"},
		{"ora:a1b2c3d4e5f60718", "ora:a1b2c3d4…"},
		{"12345678", "12345678"},
		{"ora:1234", "ora:1234"},
		{"", ""},
	}
	for _, c := range cases {
		if got := shortExplainID(c.in); got != c.want {
			t.Errorf("shortExplainID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Kayıt yolunun bağı: copilotChat etkileşim satırını genişletilmiş soruyla kurar.
func TestCopilotChatRecordsExpandedQuestion(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "newChatExchange(exchangeID, explainRecordedQuestion(lastUserText(req.Messages), req.Context.ExplainAsk)") {
		t.Fatal("copilotChat must record explainRecordedQuestion(...) as the exchange question")
	}
}
