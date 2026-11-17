package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	aiprov "github.com/cilcenk/coremetry/internal/ai/provider"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_round_retry_test.go — v0.10.2321: model turu geçici ağ geçidi
// hatasında alışveriş başına BİR kez yeniden denenir.

func noRetryPause(t *testing.T) {
	t.Helper()
	prev := chatRoundRetrySleep
	chatRoundRetrySleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	t.Cleanup(func() { chatRoundRetrySleep = prev })
}

func TestShouldRetryRound(t *testing.T) {
	live := context.Background()
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	e503 := &aiprov.HTTPError{Provider: "openai-compat", Status: 503, Body: "busy"}
	cases := []struct {
		name      string
		ctx       context.Context
		err       error
		forwarded bool
		requests  uint8
		used      bool
		want      bool
	}{
		{"503", live, e503, false, 1, false, true},
		{"502", live, &aiprov.HTTPError{Status: 502}, false, 1, false, true},
		{"504", live, &aiprov.HTTPError{Status: 504}, false, 1, false, true},
		{"akış başı 503", live, &aiprov.StreamFallbackError{Stage: aiprov.StageHead, Status: 503}, false, 1, false, true},
		{"bağlantı sıfırlandı", live, fmt.Errorf("openai-compat stream read: %w", syscall.ECONNRESET), false, 1, false, true},
		{"beklenmeyen EOF", live, fmt.Errorf("openai-compat stream read: %w", io.ErrUnexpectedEOF), false, 1, false, true},
		{"akış bağlantısı sıfırlandı", live, &aiprov.StreamFallbackError{Stage: aiprov.StageConnect, Err: syscall.ECONNRESET}, false, 1, false, true},
		{"akış bağlantı zaman aşımı değil", live, &aiprov.StreamFallbackError{Stage: aiprov.StageConnect, Err: context.DeadlineExceeded}, false, 1, false, false},
		{"akış 400 değil", live, &aiprov.StreamFallbackError{Stage: aiprov.StageHead, Status: 400}, false, 1, false, false},
		{"500 değil", live, &aiprov.HTTPError{Status: 500}, false, 1, false, false},
		{"400 değil", live, &aiprov.HTTPError{Status: 400}, false, 1, false, false},
		{"taşma değil", live, &aiprov.HTTPError{Status: 400, Body: "maximum context length exceeded"}, false, 1, false, false},
		{"429 değil", live, &aiprov.HTTPError{Status: 429}, false, 1, false, false},
		{"zaman aşımı değil", live, context.DeadlineExceeded, false, 1, false, false},
		{"iptal değil", live, context.Canceled, false, 1, false, false},
		{"istemci gitti", dead, e503, false, 1, false, false},
		{"delta iletildi", live, e503, true, 1, false, false},
		{"düşüş ikinci isteği yaptı", live, e503, false, 2, false, false},
		{"hak kullanıldı", live, e503, false, 1, true, false},
		{"hata yok", live, nil, false, 1, false, false},
	}
	for _, c := range cases {
		if got := shouldRetryRound(c.ctx, c.err, c.forwarded, c.requests, c.used); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}

// retryFakeCall — callWithRoundRetry'ye verilen sahte sağlayıcı çağrısı.
type retryFakeCall struct {
	n       int
	results []error
	deltas  []bool
}

func (f *retryFakeCall) call(_ context.Context, onDelta func(copilot.TextDelta)) (copilot.ChatTurn, error) {
	i := f.n
	f.n++
	if i < len(f.deltas) && f.deltas[i] {
		onDelta(copilot.TextDelta{Text: "kısmi"})
	}
	var err error
	if i < len(f.results) {
		err = f.results[i]
	}
	if err != nil {
		return copilot.ChatTurn{InputTokens: 50, ProviderRequests: 1}, err
	}
	return copilot.ChatTurn{Text: "tamam", InputTokens: 900, ProviderRequests: 1}, nil
}

func TestCallWithRoundRetry(t *testing.T) {
	noRetryPause(t)
	e503 := &aiprov.HTTPError{Provider: "openai-compat", Status: 503, Body: "busy"}
	emit := func(string, any) {}
	nop := func(copilot.TextDelta) {}

	t.Run("503 sonra başarı: bir yeniden deneme, başarısız denemenin token'ı sayılmaz", func(t *testing.T) {
		rr := &roundRetry{}
		f := &retryFakeCall{results: []error{e503, nil}}
		turn, err := callWithRoundRetry(withRoundRetry(context.Background(), rr), emit, nop, f.call)
		if err != nil || f.n != 2 || !rr.used || turn.InputTokens != 900 {
			t.Fatalf("n=%d used=%v turn=%+v err=%v", f.n, rr.used, turn, err)
		}
		if !rr.mark(copilot.LoopUsage{Rounds: 1}).GatewayRetry {
			t.Fatal("satır bayrağı yok")
		}
	})
	t.Run("503 iki kez: hata bugünkü gibi", func(t *testing.T) {
		f := &retryFakeCall{results: []error{e503, e503}}
		_, err := callWithRoundRetry(withRoundRetry(context.Background(), &roundRetry{}), emit, nop, f.call)
		if err == nil || f.n != 2 || !strings.Contains(err.Error(), "503") {
			t.Fatalf("n=%d err=%v", f.n, err)
		}
	})
	t.Run("400 yeniden denenmez", func(t *testing.T) {
		f := &retryFakeCall{results: []error{&aiprov.HTTPError{Status: 400, Body: "context length exceeded"}}}
		rr := &roundRetry{}
		_, _ = callWithRoundRetry(withRoundRetry(context.Background(), rr), emit, nop, f.call)
		if f.n != 1 || rr.used {
			t.Fatalf("n=%d used=%v", f.n, rr.used)
		}
	})
	t.Run("delta iletildikten sonra hata yeniden denenmez", func(t *testing.T) {
		f := &retryFakeCall{results: []error{fmt.Errorf("stream read: %w", io.ErrUnexpectedEOF)}, deltas: []bool{true}}
		_, err := callWithRoundRetry(withRoundRetry(context.Background(), &roundRetry{}), emit, nop, f.call)
		if f.n != 1 || err == nil {
			t.Fatalf("n=%d err=%v", f.n, err)
		}
	})
	t.Run("aynı alışverişte ikinci geçici hata yeniden denenmez", func(t *testing.T) {
		rr := &roundRetry{}
		ctx := withRoundRetry(context.Background(), rr)
		f := &retryFakeCall{results: []error{e503, nil, e503}}
		if _, err := callWithRoundRetry(ctx, emit, nop, f.call); err != nil {
			t.Fatal(err)
		}
		if _, err := callWithRoundRetry(ctx, emit, nop, f.call); err == nil || f.n != 3 {
			t.Fatalf("ikinci tur: n=%d err=%v", f.n, err)
		}
	})
	t.Run("iptal edilmiş ctx yeniden denenmez", func(t *testing.T) {
		ctx, cancel := context.WithCancel(withRoundRetry(context.Background(), &roundRetry{}))
		cancel()
		f := &retryFakeCall{results: []error{e503}}
		_, _ = callWithRoundRetry(ctx, emit, nop, f.call)
		if f.n != 1 {
			t.Fatalf("n=%d", f.n)
		}
	})
	t.Run("duraklamada iptal: ikinci istek yok, ilk hata döner", func(t *testing.T) {
		prev := chatRoundRetrySleep
		chatRoundRetrySleep = func(context.Context, time.Duration) error { return context.Canceled }
		defer func() { chatRoundRetrySleep = prev }()
		f := &retryFakeCall{results: []error{e503, nil}}
		_, err := callWithRoundRetry(withRoundRetry(context.Background(), &roundRetry{}), emit, nop, f.call)
		if f.n != 1 || err != e503 {
			t.Fatalf("n=%d err=%v", f.n, err)
		}
	})
	t.Run("hak yoksa (döngü dışı ctx) tek çağrı", func(t *testing.T) {
		f := &retryFakeCall{results: []error{e503}}
		_, _ = callWithRoundRetry(context.Background(), emit, nop, f.call)
		if f.n != 1 {
			t.Fatal(f.n)
		}
	})
}

// ── uçtan uca: gerçek copilot.Service + sahte ağ geçidi ────────────────

type gwLLM struct {
	mu     sync.Mutex
	stream []bool // istek başına stream:true mu
	srv    *httptest.Server
}

func (g *gwLLM) requests() []bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]bool(nil), g.stream...)
}

// newGWLLM — reply(n, stream, w): n. model isteğine cevap.
func newGWLLM(t *testing.T, reply func(n int, stream bool, w http.ResponseWriter)) *gwLLM {
	t.Helper()
	g := &gwLLM{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || strings.HasSuffix(r.URL.Path, "/api/show") {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		st, _ := body["stream"].(bool)
		g.mu.Lock()
		n := len(g.stream)
		g.stream = append(g.stream, st)
		g.mu.Unlock()
		reply(n, st, w)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func gw503(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = io.WriteString(w, `{"error":"upstream unavailable"}`)
}

func gwJSON(w http.ResponseWriter, msg map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": msg, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 700, "completion_tokens": 9},
	})
}

func runGW(t *testing.T, g *gwLLM) ([]chatFrame, copilot.CallRecord) {
	t.Helper()
	noRetryPause(t)
	s, _ := newLoopTestServer(t, &loopLLM{srv: g.srv}, "u-a")
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 32)}
	s.copilot.SetRecorder(rec)
	(&fakeTools{}).install(t)
	frames := postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Neden yavaş?", anchoredToMs(), "conv-1"))
	return frames, loopTimingRecord(t, rec)
}

func retrySteps(fr []chatFrame) int {
	n := 0
	for _, l := range stepLabels(fr) {
		if l == chatRoundRetryLabel {
			n++
		}
	}
	return n
}

// Akış başı 503 → düşüş YOK, duraklama, tek buffered istek → cevap; satır işaretli.
func TestLoopGatewayRetryThenSuccess(t *testing.T) {
	g := newGWLLM(t, func(n int, _ bool, w http.ResponseWriter) {
		if n == 0 {
			gw503(w)
			return
		}
		gwJSON(w, answerMsg("**Bulgu** — ödeme adımı yavaş."))
	})
	frames, r := runGW(t, g)
	reqs := g.requests()
	if len(reqs) != 2 || !reqs[0] || reqs[1] {
		t.Fatalf("istekler (stream?) %v, beklenen [true false]", reqs)
	}
	if len(framesOf(frames, "answer")) != 1 || len(framesOf(frames, "error")) != 0 || retrySteps(frames) != 1 {
		t.Fatalf("cevap/hata/adım: %v", stepLabels(frames))
	}
	if !r.LoopGatewayRetry || r.Status != "ok" || r.ModelRounds != 1 || r.InputTokens != 700 {
		t.Fatalf("satır: retry=%v status=%s rounds=%d in=%d", r.LoopGatewayRetry, r.Status, r.ModelRounds, r.InputTokens)
	}
}

// 503 iki kez → hata bugünkü gibi; tur başına iki istek.
func TestLoopGatewayRetryTwiceSurfacesError(t *testing.T) {
	g := newGWLLM(t, func(_ int, _ bool, w http.ResponseWriter) { gw503(w) })
	frames, r := runGW(t, g)
	if n := len(g.requests()); n != 2 {
		t.Fatalf("%d istek, 2 bekleniyordu", n)
	}
	if len(framesOf(frames, "error")) != 1 || len(framesOf(frames, "answer")) != 0 {
		t.Fatal("hata yüzeye çıkmadı")
	}
	if !r.LoopGatewayRetry || r.Status != "error" || !strings.Contains(r.ErrorMsg, "503") {
		t.Fatalf("satır: %+v", r)
	}
}

// 400 → yeniden deneme yok; bugünkü akış→buffered düşüşü aynen (2 istek).
func TestLoopGateway400NoRetry(t *testing.T) {
	g := newGWLLM(t, func(_ int, _ bool, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad request"}`)
	})
	frames, r := runGW(t, g)
	if reqs := g.requests(); len(reqs) != 2 || !reqs[0] || reqs[1] {
		t.Fatalf("istekler %v — düşüş bugünkü gibi olmalı", reqs)
	}
	if retrySteps(frames) != 0 || r.LoopGatewayRetry {
		t.Fatal("400 yeniden denendi")
	}
}

// Akış delta iletti, sonra bağlantı koptu → yeniden deneme yok.
func TestLoopGatewayNoRetryAfterDelta(t *testing.T) {
	g := newGWLLM(t, func(_ int, _ bool, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseChunk(w, map[string]any{"content": strings.Repeat("Ödeme adımı yavaş görünüyor. ", 20)})
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		c, _, err := hj.Hijack()
		if err == nil {
			_ = c.Close()
		}
	})
	frames, r := runGW(t, g)
	if n := len(g.requests()); n != 1 {
		t.Fatalf("%d istek, 1 bekleniyordu", n)
	}
	if len(framesOf(frames, "delta")) == 0 {
		t.Fatal("delta iletilmedi (test kurgusu)")
	}
	if retrySteps(frames) != 0 || r.LoopGatewayRetry {
		t.Fatal("delta iletildikten sonra yeniden denendi")
	}
}

// İlk tur 503 → yeniden deneme (araç çağrısı); ikinci tur yine geçici hata →
// hak bitti: bugünkü düşüş (akış + buffered) ve hata, yeni yeniden deneme yok.
func TestLoopGatewaySecondTransientNoRetry(t *testing.T) {
	g := newGWLLM(t, func(n int, _ bool, w http.ResponseWriter) {
		if n == 1 {
			gwJSON(w, toolCallMsg([2]string{"get_trace", `{"trace_id":"` + tfTrace + `"}`}))
			return
		}
		gw503(w)
	})
	frames, r := runGW(t, g)
	reqs := g.requests()
	if len(reqs) != 4 || !reqs[0] || reqs[1] || !reqs[2] || reqs[3] {
		t.Fatalf("istekler %v, beklenen [true false true false]", reqs)
	}
	if retrySteps(frames) != 1 || len(framesOf(frames, "error")) != 1 {
		t.Fatalf("adımlar: %v", stepLabels(frames))
	}
	if !r.LoopGatewayRetry || r.ModelRounds != 2 {
		t.Fatalf("satır: retry=%v rounds=%d", r.LoopGatewayRetry, r.ModelRounds)
	}
}

// API: /api/ai/calls/{id} satırı bayrağı taşır; yokken alan yazılmaz.
func TestLoopGatewayRetryAPIField(t *testing.T) {
	b, _ := json.Marshal(chstore.AICall{LoopGatewayRetry: true})
	if !strings.Contains(string(b), `"loopGatewayRetry":true`) {
		t.Fatalf("alan yok: %s", b)
	}
	if b, _ := json.Marshal(chstore.AICall{}); strings.Contains(string(b), "loopGatewayRetry") {
		t.Fatalf("false yazılmamalı: %s", b)
	}
}
