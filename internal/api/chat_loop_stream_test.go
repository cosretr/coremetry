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
	"testing"
	"time"
)

// v0.10.1273 — CoSRE adım 4: serbest döngüde token akışı + adım satırları.
//
// Uçtan uca (gerçek copilotChat + gerçek copilot.Service + SSE konuşan sahte
// model): araç turu metin YAYINLAMAZ (önsöz dahil), final tur canlı akar
// (düşünce bloğu soyulmuş), `answer` otoriter metni taşır (künye eklenmiş
// hâli; deltaların birleşimi onun önekidir), araç çipleri isteğe bağlı
// `phase` taşır (eski alanlar aynen), istemci kopunca üst akış kesilir.

type sseLoopLLM struct {
	mu    sync.Mutex
	n     int
	write func(n int, w http.ResponseWriter, r *http.Request)
	srv   *httptest.Server
}

func newSSELoopLLM(t *testing.T, write func(n int, w http.ResponseWriter, r *http.Request)) *sseLoopLLM {
	t.Helper()
	l := &sseLoopLLM{write: write}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || strings.HasSuffix(r.URL.Path, "/api/show") {
			http.NotFound(w, r) // v0.10.2340 — bağlam penceresi yoklaması model çağrısı değil
			return
		}
		_, _ = io.ReadAll(r.Body)
		l.mu.Lock()
		n := l.n
		l.n++
		l.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		l.write(n, w, r)
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *sseLoopLLM) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

func sseChunk(w http.ResponseWriter, delta map[string]any) {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
	fmt.Fprintf(w, "data: %s\n\n", b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func sseLoopServer(t *testing.T, l *sseLoopLLM) *Server {
	t.Helper()
	// newLoopTestServer'ın kurulumu; uç SSE konuşan sahte model.
	s, _ := newLoopTestServer(t, &loopLLM{srv: l.srv}, "u-a")
	return s
}

func TestChatLoopStreamsFinalTurnOnly(t *testing.T) {
	final := "**Bulgu** — checkout ödeme adımı yavaş: p95 1830 ms, ödeme geçidi zaman aşımı logları aynı pencerede. " +
		// v0.10.1324 — farklı cümleler: aynı cümlenin 3 kopyası artık yineleme sayılır (chat_answer_degen.go).
		"Kanıt: compare_periods aynı yönü gösteriyor. Kanıt: get_logs_for_trace aynı yönü gösteriyor. Kanıt: iki sinyal aynı pencerede örtüşüyor. "
	llm := newSSELoopLLM(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n == 0 {
			sseChunk(w, map[string]any{"content": "Önce metriklere, sonra loglara bakıyorum."})
			for i, c := range [][2]string{{"compare_periods", `{"service":"checkout"}`}, {"get_logs_for_trace", `{"trace_id":"` + tfTrace + `"}`}} {
				sseChunk(w, map[string]any{"tool_calls": []any{map[string]any{
					"index": i, "id": fmt.Sprintf("c%d", i), "type": "function",
					"function": map[string]any{"name": c[0], "arguments": c[1]},
				}}})
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		sseChunk(w, map[string]any{"content": "<thi"})
		sseChunk(w, map[string]any{"content": "nk>gizli plan</think>"})
		r := []rune(final)
		for i := 0; i < len(r); i += 11 {
			sseChunk(w, map[string]any{"content": string(r[i:min(i+11, len(r))])})
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	s := sseLoopServer(t, llm)
	(&fakeTools{}).install(t)
	frames := postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Loglarda bu hatanın karşılığı var mı?", anchoredToMs(), "conv-1"))

	if llm.count() != 2 {
		t.Fatalf("model %d kez çağrıldı, 2 bekleniyordu", llm.count())
	}
	lastResult, firstDelta := -1, -1
	var phases []string
	var streamed strings.Builder
	nDelta := 0
	for i, f := range frames {
		switch f.event {
		case "step-result":
			lastResult = i
		case "step":
			if tool, _ := f.data["tool"].(string); tool != "" {
				p, _ := f.data["phase"].(string)
				phases = append(phases, p)
				if _, ok := f.data["i"]; !ok {
					t.Errorf("araç çipi i taşımıyor: %v", f.data)
				}
			}
		case "delta":
			if firstDelta < 0 {
				firstDelta = i
			}
			nDelta++
			if r, _ := f.data["reset"].(bool); r {
				streamed.Reset()
			}
			txt, _ := f.data["text"].(string)
			streamed.WriteString(txt)
		}
	}
	if strings.Join(phases, ",") != "Metrik,Log" {
		t.Errorf("evreler %v, beklenen Metrik,Log", phases)
	}
	if firstDelta < lastResult {
		t.Errorf("araç turu metin yayınladı (ilk delta %d < son step-result %d)", firstDelta, lastResult)
	}
	if nDelta < 2 {
		t.Errorf("final tur canlı akmadı (%d delta)", nDelta)
	}
	got := streamed.String()
	if strings.Contains(got, "bakıyorum") || strings.Contains(got, "gizli plan") || strings.Contains(got, "think") {
		t.Errorf("gizlenmesi gereken metin aktı: %q", got)
	}
	ans := framesOf(frames, "answer")
	if len(ans) != 1 {
		t.Fatalf("answer sayısı %d", len(ans))
	}
	text, _ := ans[0]["text"].(string)
	if !strings.HasPrefix(text, strings.TrimSpace(final)) || !strings.HasPrefix(text, got) || got == "" {
		t.Errorf("answer otoriter değil / delta öneki tutmuyor:\nanswer=%q\nakan=%q", text, got)
	}
	if text == got {
		t.Error("answer künyesiz — son işlem (kaynak künyesi) uygulanmadı")
	}
	if d := framesOf(frames, "done"); len(d) != 1 || d[0]["ok"] != true {
		t.Errorf("done: %v", d)
	}
}

func TestChatLoopStreamAbortCancelsUpstream(t *testing.T) {
	upstreamCancelled := make(chan struct{})
	llm := newSSELoopLLM(t, func(n int, w http.ResponseWriter, r *http.Request) {
		sseChunk(w, map[string]any{"content": streamFillerText(30)}) // v0.10.1452 — yinelemesiz (kural 3)
		<-r.Context().Done()
		close(upstreamCancelled)
	})
	s := sseLoopServer(t, llm)
	(&fakeTools{}).install(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []chatFrame, 1)
	go func() {
		done <- postLoopChat(t, s, ctx, "u-a", traceDrawerBody("Loglarda bu hatanın karşılığı var mı?", anchoredToMs(), "conv-1"))
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case <-upstreamCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("istemci koptu ama üst akış isteği kesilmedi")
	}
	select {
	case frames := <-done:
		if len(framesOf(frames, "answer")) != 0 {
			t.Error("iptal edilen akış answer yayınladı")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler iptalden sonra dönmedi")
	}
	if llm.count() != 1 {
		t.Errorf("iptalden sonra model yeniden çağrıldı (%d)", llm.count())
	}
}

func TestStepPhaseTR(t *testing.T) {
	for tool, want := range map[string]string{
		"query_metrics": "Metrik", "query_logs": "Log", "query_traces": "Trace",
		"search_logs": "Log", "get_trace": "Trace", "list_problems": "Problem",
		"uydurma_arac": "", "": "",
	} {
		if got := stepPhaseTR(tool); got != want {
			t.Errorf("stepPhaseTR(%q)=%q, beklenen %q", tool, got, want)
		}
	}
	// isteğe bağlı alan: bilinmeyen araçta YOK, eski alanlar aynen.
	var got []recEv
	emit := withStepIDs(recEmit(&got))
	emitStepChip(emit, "query_logs", `{}`)
	emitStepChip(emit, "dis_arac", `{}`)
	if got[0].payload["phase"] != "Log" || got[0].payload["tool"] != "query_logs" || got[0].payload["args"] != "{}" {
		t.Errorf("çip: %v", got[0].payload)
	}
	if _, has := got[1].payload["phase"]; has {
		t.Errorf("bilinmeyen araç evre taşımamalı: %v", got[1].payload)
	}
}

// streamFillerText — v0.10.1452: uzun ama YİNELEMESİZ akış metni; aynı öbeği
// tekrarlayan dolgu artık yineleme muhafızının kural 3'ünü tetikler.
func streamFillerText(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "akan cevap %d ", i)
	}
	return b.String()
}
