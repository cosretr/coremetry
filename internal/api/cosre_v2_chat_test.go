package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// cosre_v2_chat_test.go — v0.10.1593 (CoSRE v2 Faz 1, R5): POST
// /api/cosre/v2/chat. Gövde v1 ile ORTAK (copilotChatWith); yalnız yazıcı
// uistream.Emitter. Altınlar R1'in senaryolarıyla aynı girdiyi ve aynı
// normaliseri kullanır: gerçek sarım zinciri + çevirici uçtan uca.

func setCosreV2(t *testing.T, s *Server, on bool) {
	t.Helper()
	s.cosreV2Memo().set(on)
	t.Cleanup(func() { cosreV2Memos.Delete(s) })
}

func serveV2(s *Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.buildMux().ServeHTTP(w, r)
	return w
}

func TestCosreV2ChatFlagOffIs404(t *testing.T) {
	s, _ := newLoopTestServer(t, noLLM(t), "u-golden")
	setCosreV2(t, s, false)
	w := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenHelpBody(), false))
	if w.Code != http.StatusNotFound {
		t.Fatalf("bayrak kapalı → %d, 404 bekleniyordu", w.Code)
	}
	if w.Header().Get(uistream.HeaderName) != "" {
		t.Fatal("404'te akış başlığı var")
	}
}

func TestCosreV2ChatFlagOnCopilotOffIs503(t *testing.T) {
	s := &Server{copilot: copilot.New(copilot.ProviderOpenAI, "", "")}
	setCosreV2(t, s, true)
	w := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenHelpBody(), false))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("copilot kapalı → %d, 503 bekleniyordu", w.Code)
	}
}

func TestCosreV2ChatRouteResolves(t *testing.T) {
	mux := (&Server{}).buildMux()
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, "/api/cosre/v2/chat", nil))
	if pattern != "POST /api/cosre/v2/chat" {
		t.Fatalf("desen %q", pattern)
	}
}

func TestCosreV2ChatOversizeBodyRejectedBeforeStream(t *testing.T) {
	s, _ := newLoopTestServer(t, noLLM(t), "u-golden")
	setCosreV2(t, s, true)
	big := `{"messages":[{"role":"user","text":"` + strings.Repeat("a", 257<<10) + `"}]}`
	r := chatGoldenRequest("/api/cosre/v2/chat", nil, false)
	r.Body = httptestBody(big)
	w := serveV2(s, r)
	if w.Code == http.StatusOK {
		t.Fatal("257 KiB gövde 200 aldı")
	}
	if w.Header().Get(uistream.HeaderName) != "" {
		t.Fatal("reddedilen istekte akış başlığı yazıldı")
	}
}

func TestCosreV2ChatGoldenHelp(t *testing.T) {
	s, _ := newLoopTestServer(t, noLLM(t), "u-golden")
	setCosreV2(t, s, true)
	w := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenHelpBody(), false))
	assertV2Headers(t, w)
	compareChatGolden(t, "v2_help.sse", normalizeChatStream(w.Body.String()))
}

func TestCosreV2ChatGoldenLoop(t *testing.T) {
	llm := goldenLoopLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-golden")
	setCosreV2(t, s, true)
	ft := &fakeTools{}
	ft.install(t)
	w := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenLoopBody(), true))
	assertV2Headers(t, w)
	if n := len(llm.calls()); n != 2 {
		t.Fatalf("model %d kez çağrıldı, 2 bekleniyordu", n)
	}
	compareChatGolden(t, "v2_loop.sse", normalizeChatStream(w.Body.String()))
}

func assertV2Headers(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(uistream.HeaderName); got != uistream.ProtocolVersion {
		t.Fatalf("%s = %q", uistream.HeaderName, got)
	}
	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type %q", got)
	}
	if !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") {
		t.Fatal("akış [DONE] ile bitmiyor")
	}
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }

func httptestBody(s string) readCloser { return readCloser{bytes.NewReader([]byte(s))} }

// v0.10.1602 — operatör kararı 2026-10-29 §2: bayrak YALNIZ istek başında
// okunur (requireCosreV2). Açık bir v2 akışı bayrak kapatılsa da sonuna dek
// sürer; kapatma yalnız YENİ istekleri 404'e çevirir (podlar arası 30 s
// yayılım kabul). Pin: araç çağrısının ortasında bayrak kapanır → akış yine
// son cevabı ve [DONE]'u yazar, model iki kez çağrılır; sonraki istek 404.
func TestCosreV2ChatFlagOffMidStreamDoesNotCutStream(t *testing.T) {
	llm := goldenLoopLLM(t)
	s, _ := newLoopTestServer(t, llm, "u-golden")
	setCosreV2(t, s, true)
	flipped := false
	ft := &fakeTools{onCall: func(string) {
		s.cosreV2Memo().set(false) // admin PUT'un yerel etkisi
		flipped = !s.cosreV2Enabled(context.Background())
	}}
	ft.install(t)
	w := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenLoopBody(), true))
	if !flipped {
		t.Fatal("bayrak akış ortasında kapanmadı (test kurgusu)")
	}
	assertV2Headers(t, w)
	if n := len(llm.calls()); n != 2 {
		t.Fatalf("model %d kez çağrıldı — akış bayrakla kesildi", n)
	}
	if !strings.Contains(w.Body.String(), "orders-api p95 yükseldi") {
		t.Fatal("son cevap akışta yok — bayrak kapanınca akış kesildi")
	}
	if w2 := serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenHelpBody(), false)); w2.Code != http.StatusNotFound {
		t.Fatalf("kapandıktan sonraki yeni istek %d, 404 bekleniyordu", w2.Code)
	}
}
