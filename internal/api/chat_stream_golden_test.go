package api

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
)

// chat_stream_golden_test.go — v0.10.1589 (CoSRE v2 Faz 1, R1): v1 sohbet
// akışının BAYT altını. Sonraki sürüm (R2) handler'ın en altındaki SSE
// yazıcısını bir fabrika parametresine (chatSink) çeviriyor; o refaktörün
// "v1 çıktısı bayt bayt aynı" iddiası ancak refaktörden ÖNCE kaydedilmiş bir
// tabana karşı kanıtlanabilir. Bu dosya o taban.
//
// İki senaryo:
//   - help: `/help` komutu (LLM'siz kademe) — çerçeve şekli + done.
//   - loop: sahte OpenAI-uyumlu model (newLoopLLM) — tur 0 tek araç çağrısı,
//     tur 1 son cevap. Sarım zincirinin TAMAMINI geçer (withStepIDs →
//     turnBlockCapture → withBlockSeq → xch.tap): step/step-result/answer/done.
//     Sahte model akış (SSE) değil tek JSON döndürdüğü için bu senaryoda
//     `delta` çerçevesi beklenmez; delta/reset kapsaması paylaşılan jsonl
//     fixture'ında (testdata/cosre, R3).
//
// Yenileme: go test ./internal/api -run TestChatStreamGolden -update

var updateGolden = flag.Bool("update", false, "rewrite golden files under testdata/")

// chatGoldenMasks — oynak alanlar. Her regex YANINDA gerekçesi; yalnız
// oynaklığı kanıtlanmış alan maskelenir (fazlası altını körleştirir).
var chatGoldenMasks = []struct {
	re   *regexp.Regexp
	repl string
}{
	// Araç süresi duvar saatinden ölçülür.
	{regexp.MustCompile(`"durationMs":\d+`), `"durationMs":0`},
	// callId = modele giden çağrının rastgele kimliği (newRandID). turnId
	// MASKELENMEZ: kök exchangeId'dir (eval işaretiyle "golden-xid").
	{regexp.MustCompile(`"callId":"[^"]*"`), `"callId":"X"`},
}

// normalizeChatStream — heartbeat yorum satırlarını atar, maskeleri uygular.
func normalizeChatStream(raw string) string {
	var keep []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, ": ping") {
			continue
		}
		keep = append(keep, line)
	}
	out := strings.Join(keep, "\n")
	for _, m := range chatGoldenMasks {
		out = m.re.ReplaceAllString(out, m.repl)
	}
	return out
}

// goldenLoopLLM — tur 0: tek araç çağrısı; tur 1: son cevap.
func goldenLoopLLM(t *testing.T) *loopLLM {
	return newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n > 0 {
			return answerMsg("**Bulgu** — orders-api p95 yükseldi.")
		}
		return toolCallMsg([2]string{"get_service_health", `{"service":"orders-api"}`})
	})
}

// chatGoldenRequest — gerçek handler'a giden istek; eval işareti exchangeId'yi
// sabitler ve serbest döngüye doğrudan iner (guided/RAG/niyet atlanır).
func chatGoldenRequest(path string, body map[string]any, loop bool) *http.Request {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-golden", Email: "u-golden@example.test", Role: "viewer"})
	ctx = withChatEvalMode(ctx, &chatEvalMode{Surface: "evalset-chat", ExchangeID: "golden-xid", SkipIntent: true, LoopProbe: loop})
	return r.WithContext(ctx)
}

func chatGoldenHelpBody() map[string]any {
	return map[string]any{"messages": []map[string]any{{"role": "user", "text": "/help"}},
		"context": map[string]any{"command": "help"}}
}

func chatGoldenLoopBody() map[string]any {
	return map[string]any{"messages": []map[string]any{{"role": "user", "text": "orders-api neden yavaş?"}}}
}

func compareChatGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "chat_stream", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("altın okunamadı (%v) — -update ile üret", err)
	}
	if string(want) != got {
		t.Fatalf("%s altınla uyuşmuyor\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func TestChatStreamGoldenV1Help(t *testing.T) {
	s, _ := newLoopTestServer(t, noLLM(t), "u-golden")
	w := httptest.NewRecorder()
	s.copilotChat(w, chatGoldenRequest("/api/copilot/chat", chatGoldenHelpBody(), false))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	compareChatGolden(t, "v1_help.sse", normalizeChatStream(w.Body.String()))
}

func TestChatStreamGoldenV1Loop(t *testing.T) {
	llm := goldenLoopLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-golden")
	ft := &fakeTools{}
	ft.install(t)
	w := httptest.NewRecorder()
	s.copilotChat(w, chatGoldenRequest("/api/copilot/chat", chatGoldenLoopBody(), true))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if n := len(llm.calls()); n != 2 {
		t.Fatalf("model %d kez çağrıldı, 2 bekleniyordu (araç turu + cevap)", n)
	}
	if got := ft.snapshot(); len(got) != 1 || got[0].name != "get_service_health" {
		t.Fatalf("araç çağrıları: %+v", got)
	}
	compareChatGolden(t, "v1_loop.sse", normalizeChatStream(w.Body.String()))
}
