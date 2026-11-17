package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_loop_timing_test.go — v0.10.2318: serbest döngünün tek ai_calls
// satırına cevap turu TTFT'si, toplam model / araç süresi, araç çağrısı
// sayısı ve tek turdaki en çok çağrı.

func TestLoopUsageAddCalls(t *testing.T) {
	var u loopUsage
	u.addCalls(0) // araçsız (cevap) turu sayılmaz
	for _, n := range []int{2, 1, 3, 1} {
		u.addCalls(n)
	}
	got := u.usage()
	if got.ToolCalls != 7 || got.MaxRoundToolCalls != 3 {
		t.Fatalf("toplam/en çok: %+v", got)
	}
	u.answer(900)
	u.answer(2100) // sonraki cevap turu (bozuk-cevap yeniden denemesi) ezer
	if u.usage().AnswerTTFTMs != 2100 {
		t.Fatalf("ttft: %+v", u.usage())
	}
	u.toolCalls = ^uint16(0) - 1
	u.addCalls(5)
	if u.toolCalls != ^uint16(0) {
		t.Fatal("araç sayacı taşmamalı")
	}
	u.addCalls(1 << 20)
	if u.maxRoundCalls != ^uint16(0) {
		t.Fatal("tur başı en çok tavanlı olmalı")
	}
}

func TestChatSpanLoopTimes(t *testing.T) {
	_, cs := (&Server{}).beginChatSpan(context.Background(), "ex1")
	_, end := cs.turn(context.Background(), 0, false)
	time.Sleep(15 * time.Millisecond)
	end(1, 1, 0, nil)
	_, endTool := cs.tool(context.Background(), "get_trace", false)
	time.Sleep(10 * time.Millisecond)
	endTool(10, false)
	lu := cs.withLoopTimes(copilot.LoopUsage{Rounds: 1})
	if lu.ModelMs < 15 || lu.ToolMs < 10 || lu.Rounds != 1 {
		t.Fatalf("süreler: %+v", lu)
	}
	if durMsU32(-time.Second) != 0 || durMsU32(time.Duration(1<<62)) != ^uint32(0) {
		t.Fatal("durMsU32 sınırları")
	}
}

// sseUsage — akan cevabın son usage parçası (openai include_usage biçimi).
func sseUsage(w http.ResponseWriter, prompt int) {
	b, _ := json.Marshal(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": prompt, "completion_tokens": 5}})
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func sseToolCalls(w http.ResponseWriter, calls ...[2]string) {
	for i, c := range calls {
		sseChunk(w, map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": fmt.Sprintf("c%d", i), "type": "function",
			"function": map[string]any{"name": c[0], "arguments": c[1]},
		}}})
	}
}

// 3 tur: 2 çağrı + 1 çağrı + akan cevap. TTFT YALNIZ cevap turundan (ilk
// turun önsözü çok daha geç başlar — o alınsaydı ≥150 ms okunurdu); araç
// süresi yürüyen üç çağrının toplamı; model süresi turların toplamı.
func TestLoopRecordsTimingBreakdown(t *testing.T) {
	llm := newSSELoopLLM(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		switch n {
		case 0:
			time.Sleep(150 * time.Millisecond)
			sseChunk(w, map[string]any{"content": "Önce metriklere bakıyorum."})
			sseToolCalls(w, [2]string{"compare_periods", `{"service":"checkout"}`}, [2]string{"get_logs_for_trace", `{"trace_id":"` + tfTrace + `"}`})
		case 1:
			sseToolCalls(w, [2]string{"get_trace", `{"trace_id":"` + tfTrace + `"}`})
		default:
			time.Sleep(40 * time.Millisecond)
			sseChunk(w, map[string]any{"content": "**Bulgu** — ödeme adımı yavaş; kanıt iki araçta aynı yönde. "})
			sseChunk(w, map[string]any{"content": "Loglarda ödeme geçidi zaman aşımı aynı pencerede görünüyor."})
		}
		sseUsage(w, 1000+n)
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	s := sseLoopServer(t, llm)
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 32)}
	s.copilot.SetRecorder(rec)
	ft := &fakeTools{onCall: func(string) { time.Sleep(20 * time.Millisecond) }}
	ft.install(t)
	postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Neden yavaş?", anchoredToMs(), "conv-1"))
	if llm.count() != 3 {
		t.Fatalf("model %d kez çağrıldı, 3 bekleniyordu", llm.count())
	}
	if n := len(ft.snapshot()); n != 3 {
		t.Fatalf("%d araç yürüdü, 3 bekleniyordu", n)
	}
	r := loopTimingRecord(t, rec)
	if r.LoopToolCalls != 3 || r.LoopMaxRoundToolCalls != 2 || r.ModelRounds != 3 {
		t.Fatalf("sayılar: calls=%d max=%d rounds=%d", r.LoopToolCalls, r.LoopMaxRoundToolCalls, r.ModelRounds)
	}
	if r.TTFTMs < 40 || r.TTFTMs >= 150 {
		t.Fatalf("TTFT %d ms — cevap turundan (≥40, <150) alınmalıydı", r.TTFTMs)
	}
	if r.LoopToolMs < 60 {
		t.Fatalf("araç süresi %d ms, ≥60 bekleniyordu", r.LoopToolMs)
	}
	if r.LoopModelMs < 190 || r.LoopModelMs > r.DurationMs {
		t.Fatalf("model süresi %d ms (satır %d ms)", r.LoopModelMs, r.DurationMs)
	}
}

// Buffered (akmayan) uç: TTFT bilinmiyor = 0; diğer sayılar yine yazılır.
func TestLoopTimingBufferedTTFTUnknown(t *testing.T) {
	llm := newUsageLLM(t, func(n int) map[string]any {
		if n == 0 {
			return toolCallMsg([2]string{"get_trace", `{"trace_id":"` + tfTrace + `"}`})
		}
		return answerMsg("**Bulgu** — tamam.")
	}, func(int) int { return 500 })
	s, _ := newLoopTestServer(t, llm, "u-a")
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 32)}
	s.copilot.SetRecorder(rec)
	(&fakeTools{}).install(t)
	postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Neden yavaş?", anchoredToMs(), "conv-1"))
	r := loopTimingRecord(t, rec)
	if r.TTFTMs != 0 {
		t.Fatalf("buffered yolda TTFT %d, 0 (bilinmiyor) bekleniyordu", r.TTFTMs)
	}
	if r.LoopToolCalls != 1 || r.LoopMaxRoundToolCalls != 1 {
		t.Fatalf("sayılar: %+v", r)
	}
}

func loopTimingRecord(t *testing.T, rec *chanRecorder) copilot.CallRecord {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case r := <-rec.ch:
			if r.ModelRounds > 0 {
				return r
			}
		case <-deadline:
			t.Fatal("döngü satırı yazılmadı")
			return copilot.CallRecord{}
		}
	}
}

// API: alanlar /api/ai/calls/{id} ve exchanges alt satırında; 0 iken yok.
func TestLoopTimingAPIFields(t *testing.T) {
	full := chstore.AICall{TTFTMs: 2100, LoopModelMs: 9400, LoopToolMs: 1800, LoopToolCalls: 4, LoopMaxRoundToolCalls: 2}
	sub := chstore.AIExchangeCall{TTFTMs: 2100, LoopModelMs: 9400, LoopToolMs: 1800, LoopToolCalls: 4, LoopMaxRoundToolCalls: 2}
	keys := []string{`"ttftMs":2100`, `"loopModelMs":9400`, `"loopToolMs":1800`, `"loopToolCalls":4`, `"loopMaxRoundToolCalls":2`}
	for _, v := range []any{full, sub} {
		b, _ := json.Marshal(v)
		for _, k := range keys {
			if !strings.Contains(string(b), k) {
				t.Errorf("%T JSON %s içermiyor: %s", v, k, b)
			}
		}
	}
	for _, v := range []any{chstore.AICall{}, chstore.AIExchangeCall{}} {
		b, _ := json.Marshal(v)
		for _, k := range []string{"ttftMs", "loopModelMs", "loopToolMs", "loopToolCalls", "loopMaxRoundToolCalls"} {
			if strings.Contains(string(b), k) {
				t.Errorf("%T: bilinmeyen (0) alan %s yazılmamalı: %s", v, k, b)
			}
		}
	}
}
