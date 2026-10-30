package api

import (
	"os"
	"strings"
	"testing"
)

// v0.10.453 — sohbet trace açıklaması = ✨ Explain trace (operatör-bildirimli).

func TestIsTraceExplainAsk(t *testing.T) {
	const id = "07544915dcf643aead8a61070780e6f7"
	for msg, want := range map[string]bool{
		id:                               true,
		"bu trace'i açıkla":              true,
		"Bu trace'i açıkla (" + id + ")": true,
		"explain this trace " + id:       true,
		"trace " + id + " özetle":        true,
		"bu trace nasıl":                 true,
		"bu trace neden yavaş":           false,
		"hangi span hatalı " + id:        false,
		"bu trace'te kaç db çağrısı var": false,
		id + " hata var mı":              false,
	} {
		if got := isTraceExplainAsk(msg); got != want {
			t.Errorf("%q → %v, want %v", msg, got, want)
		}
	}
}

// Kaynak pinleri: trace_by_id Explain çekirdeğinden geçer; sistem/kanıt
// birebir (sarmalayıcı yok); önbellek anahtarı düğmeyle AYNI formül;
// yüzey explain-trace:chat; "Kaynak:" satırı yok.
func TestChatTraceExplainMatchesExplainButton(t *testing.T) {
	g, _ := os.ReadFile("copilot_guided.go")
	u, _ := os.ReadFile("trace_explain_unified.go")
	// v0.10.948 — handler api.go'dan trace_explain_handler.go'ya taşındı.
	a, _ := os.ReadFile("trace_explain_handler.go")
	if !strings.Contains(string(g), "if route.Intent == guidedTraceByID {\n\t\tif handled, ok := s.guidedTraceExplain(") {
		t.Fatal("runGuidedRoute trace_by_id'yi guidedTraceExplain'e vermeli (anlatım sarmalayıcısından ÖNCE)")
	}
	us := string(u)
	for _, want := range []string{
		// v0.10.1400 (Tek CoSRE) — açıklama isteği çekmece AÇMAZ; sohbet içinde
		// düğmenin klasik anahtarıyla akar (emitTraceExplainAnswer).
		`return s.emitTraceExplainAnswer(ctx, emit, route.TraceID, in, route, links)`,
		`s.streamTraceExplainCore(ctx, emit, aisurface.ExplainTraceChat, system, user, "", false)`, // v0.10.1406 — + fresh; v0.10.1630 — dış bağımlılık satırlı ikiz
		`"evidenceSpanIds": in.Evidence`,
	} {
		if !strings.Contains(us, want) {
			t.Errorf("birleşik yol %q içermeli", want)
		}
	}
	// v0.10.1400 — v0.10.460'ın `open` sıçrayışı ve aisrc=chat çekmecesi yok.
	for _, no := range []string{"guidedNarrationUser(", "withAddressee(", `"\n\nKaynak: "`, `"open":`, `aisrc=chat`} {
		if strings.Contains(us, no) {
			t.Errorf("birleşik yol %q kullanmamalı (Explain ile birebir)", no)
		}
	}
	if !strings.Contains(string(a), `explainCacheKey(copilot.SystemPromptTrace(), in.User, "")`) {
		t.Fatal("klasik trace explain önbellek anahtarı formülü değişti (explainTraceClassicPrepared)")
	}
	if !strings.Contains(us, `"chat": true`) && !strings.Contains(mustRead(t, "ai_observability.go"), `"chat": true`) {
		t.Fatal("aisrc=chat yüzey soneki whitelist'te olmalı (explain-trace:chat)")
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
