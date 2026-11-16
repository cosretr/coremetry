package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_loop_usage_test.go — v0.10.2297: serbest döngünün tek ai_calls
// satırına tur sayısı + en büyük tek prompt.

func TestLoopUsageAdd(t *testing.T) {
	var u loopUsage
	if got := u.usage(); got.Rounds != 0 || got.PeakInputTokens != 0 {
		t.Fatalf("sıfır değer: %+v", got)
	}
	for _, in := range []uint32{27000, 34100, 31000} {
		u.add(in)
	}
	if got := u.usage(); got.Rounds != 3 || got.PeakInputTokens != 34100 {
		t.Fatalf("3 tur: %+v", got)
	}
	u.add(0) // hatalı tur da bir gidiş-dönüş; peak değişmez
	if got := u.usage(); got.Rounds != 4 || got.PeakInputTokens != 34100 {
		t.Fatalf("hatalı tur: %+v", got)
	}
	u.rounds = ^uint16(0)
	u.add(1)
	if u.rounds != ^uint16(0) {
		t.Fatal("tur sayacı taşmamalı")
	}
}

// newUsageLLM — loopLLM'in tur başına farklı prompt_tokens döndüren ikizi.
func newUsageLLM(t *testing.T, script func(n int) map[string]any, promptTokens func(n int) int) *loopLLM {
	t.Helper()
	l := &loopLLM{script: func(n int, _ map[string]any) map[string]any { return script(n) }}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || strings.HasSuffix(r.URL.Path, "/api/show") {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		l.mu.Lock()
		n := len(l.bodies)
		l.bodies = append(l.bodies, body)
		l.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": script(n), "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": promptTokens(n), "completion_tokens": 7},
		})
	}))
	t.Cleanup(l.srv.Close)
	return l
}

// loopRecord — döngünün ai_calls kaydı (kök exchange'li satır).
func loopRecord(t *testing.T, rec *chanRecorder) copilot.CallRecord {
	t.Helper()
	var out []copilot.CallRecord
	for done := false; !done; {
		select {
		case r := <-rec.ch:
			out = append(out, r)
		case <-time.After(300 * time.Millisecond):
			done = true
		}
	}
	for _, r := range out {
		if r.InputTokens > 0 && r.Provider != "" {
			return r
		}
	}
	t.Fatalf("döngü satırı yazılmadı: %+v", out)
	return copilot.CallRecord{}
}

// 3 tur (2 araç turu + cevap) → satır rounds=3, peak = turların en büyüğü,
// input_tokens = toplam (değişmedi).
func TestLoopRecordsRoundsAndPeak(t *testing.T) {
	tokens := []int{27000, 34100, 31000}
	llm := newUsageLLM(t, func(n int) map[string]any {
		switch n {
		case 0:
			return toolCallMsg([2]string{"get_trace", `{"trace_id":"` + tfTrace + `"}`})
		case 1:
			return toolCallMsg([2]string{"get_logs_for_trace", `{"trace_id":"` + tfTrace + `"}`})
		}
		return answerMsg("**Bulgu** — tamam.")
	}, func(n int) int {
		if n < len(tokens) {
			return tokens[n]
		}
		return 1
	})
	s, _ := newLoopTestServer(t, llm, "u-a")
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 32)}
	s.copilot.SetRecorder(rec)
	ft := &fakeTools{}
	ft.install(t)
	postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Neden yavaş?", anchoredToMs(), "conv-1"))
	if n := len(llm.calls()); n != 3 {
		t.Fatalf("model %d kez çağrıldı, 3 bekleniyordu", n)
	}
	r := loopRecord(t, rec)
	if r.ModelRounds != 3 || r.PeakInputTokens != 34100 || r.InputTokens != 92100 {
		t.Fatalf("satır: rounds=%d peak=%d in=%d", r.ModelRounds, r.PeakInputTokens, r.InputTokens)
	}
}

// Model hiç durmazsa (tur tavanı + tavan turu): satırın tur sayısı modele
// giden istek sayısıyla AYNI (tavan turu dahil).
func TestLoopRecordsRoundsIncludingCapTurn(t *testing.T) {
	llm := newUsageLLM(t, func(n int) map[string]any {
		return toolCallMsg([2]string{"trace_stats", fmt.Sprintf(`{"service":"svc-a","limit":%d}`, n+1)})
	}, func(n int) int { return 1000 + n })
	s, _ := newLoopTestServer(t, llm, "u-a")
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 64)}
	s.copilot.SetRecorder(rec)
	ft := &fakeTools{}
	ft.install(t)
	postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Neden yavaş?", anchoredToMs(), "conv-1"))
	calls := len(llm.calls())
	if calls < 2 {
		t.Fatalf("döngü dönmedi: %d", calls)
	}
	r := loopRecord(t, rec)
	t.Logf("modele %d istek, satır rounds=%d", calls, r.ModelRounds)
	if int(r.ModelRounds) != calls || r.PeakInputTokens != uint32(1000+calls-1) {
		t.Fatalf("satır rounds=%d peak=%d; modele %d istek gitti", r.ModelRounds, r.PeakInputTokens, calls)
	}
}

// cosre_interactions.model_calls: döngünün tek notu N gidiş-dönüş sayılır;
// tursuz not (diğer kademeler) 1 kalır.
func TestChatExchangeModelCallsCountsLoopRounds(t *testing.T) {
	x := newChatExchange("ex1", "soru", false, time.Now())
	x.noteModelCall(copilot.CallNote{Model: "m", InputTokens: 10})
	x.noteModelCall(copilot.CallNote{Model: "m", InputTokens: 92100, Rounds: 3})
	if x.modelCalls != 4 {
		t.Fatalf("modelCalls = %d, beklenen 4 (1 + 3 tur)", x.modelCalls)
	}
	x.modelCalls = 65534
	x.noteModelCall(copilot.CallNote{Rounds: 5})
	if x.modelCalls != 65535 {
		t.Fatalf("taşma: %d", x.modelCalls)
	}
}
