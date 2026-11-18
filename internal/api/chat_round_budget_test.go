package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// chat_round_budget_test.go — v0.10.2340: proaktif tur bütçesi.

// ── SAF çekirdek ──────────────────────────────────────────────────────────

func budgetPair(id, tool, args, content string) []copilot.ChatMessage {
	return []copilot.ChatMessage{
		{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: id, Name: tool, Input: json.RawMessage(args)}}},
		{Role: "user", ToolResults: []copilot.ToolResult{{CallID: id, Name: tool, Content: content}}},
	}
}

func bigResult(tag string, n int) string {
	return `{"tool":"` + tag + `","rows":"` + strings.Repeat("x", n) + `"}`
}

// dört tur: R0 Metrik, R1 Sağlık, R2 Metrik (ailenin sonu), R3 Sağlık (son tur)
func budgetConv() []copilot.ChatMessage {
	conv := []copilot.ChatMessage{{Role: "user", Text: "checkout neden yavaş?"}}
	conv = append(conv, budgetPair("c0", "compare_periods", `{"service":"checkout","range_s":3600}`, bigResult("r0", 4000))...)
	conv = append(conv, budgetPair("c1", "get_service_health", `{"service":"orders"}`, bigResult("r1", 4000))...)
	conv = append(conv, budgetPair("c2", "compare_periods", `{"service":"payments"}`, bigResult("r2", 4000))...)
	conv = append(conv, budgetPair("c3", "get_service_health", `{"service":"payments"}`, bigResult("r3", 4000))...)
	return conv
}

// pairsIntact — her sonuç önündeki bir çağrıyla, her çağrı ardından gelen bir sonuçla eşleşir.
func pairsIntact(t *testing.T, conv []copilot.ChatMessage) {
	t.Helper()
	for i, m := range conv {
		for _, tc := range m.ToolCalls {
			if i+1 >= len(conv) {
				t.Fatalf("çağrı %s sonuçsuz", tc.ID)
			}
			found := false
			for _, tr := range conv[i+1].ToolResults {
				found = found || tr.CallID == tc.ID
			}
			if !found {
				t.Fatalf("çağrı %s öksüz", tc.ID)
			}
		}
		for _, tr := range m.ToolResults {
			if i == 0 || len(conv[i-1].ToolCalls) == 0 {
				t.Fatalf("sonuç %s öksüz", tr.CallID)
			}
		}
	}
}

func resultByID(conv []copilot.ChatMessage, id string) (copilot.ToolResult, bool) {
	for _, m := range conv {
		for _, tr := range m.ToolResults {
			if tr.CallID == id {
				return tr, true
			}
		}
	}
	return copilot.ToolResult{}, false
}

func isStub(s string) bool { return strings.HasPrefix(s, "[kısaltıldı: bağlam penceresine") }

// limitFor — tahmini tam istenen token'a oturtan pencere (bpt varsayılan).
func windowForLimit(limit, maxTok int) int {
	for w := maxTok + roundBudgetMarginMin; ; w++ {
		if roundBudgetLimit(w, maxTok) >= limit {
			return w
		}
	}
}

func TestRoundBudgetEstimateAndLimit(t *testing.T) {
	if got := roundEstimate(3000, 0, 0, 0); got != 1000 {
		t.Fatalf("ilk tur bayt/3: %d", got)
	}
	// gerçek sayı + eklenen bayt / kalibre oran
	if got := roundEstimate(13000, 2500, 10000, 4); got != 2500+750 {
		t.Fatalf("kalibre tahmin: %d", got)
	}
	// pay = max(pencere/20, 256); %80
	if got := roundBudgetLimit(32768, 4096); got != (32768-4096-1638)*80/100 {
		t.Fatalf("sınır: %d", got)
	}
	if roundBudgetLimit(4000, 4096) != 0 {
		t.Fatal("yer yokken sınır 0 olmalı")
	}
}

func TestRoundBudgetCalibratesFromFirstRealCount(t *testing.T) {
	b := &roundBudget{window: 32768, sentBytes: 30000}
	b.observe(copilot.ChatTurn{InputTokens: 6000}, nil)
	if b.bpt != 5 || b.prevIn != 6000 || b.prevBytes != 30000 {
		t.Fatalf("kalibrasyon: %+v", b)
	}
	// ikinci gerçek sayı oranı değiştirmez (ilk sayı), taban kayar
	b.sentBytes = 40000
	b.observe(copilot.ChatTurn{InputTokens: 9000}, nil)
	if b.bpt != 5 || b.prevIn != 9000 || b.prevBytes != 40000 {
		t.Fatalf("ikinci gözlem: %+v", b)
	}
	if got := roundEstimate(45000, b.prevIn, b.prevBytes, b.bpt); got != 10000 {
		t.Fatalf("tahmin gerçek sayıdan: %d", got)
	}
	// uçuk oran kırpılır
	c := &roundBudget{window: 1, sentBytes: 100}
	c.observe(copilot.ChatTurn{InputTokens: 1000}, nil)
	if c.bpt != roundBudgetMinBPT {
		t.Fatalf("alt sınır: %v", c.bpt)
	}
	// taşma: sonraki çağrı tepkisel yeniden deneme
	c.observe(copilot.ChatTurn{}, fmt.Errorf("openai-compat 400: maximum context length is 8192 tokens"))
	if !c.lastOverflow {
		t.Fatal("taşma işaretlenmedi")
	}
}

func TestPlanRoundCompactionUnderLimitSameSlice(t *testing.T) {
	conv := budgetConv()
	out := planRoundCompaction(roundCompactIn{Conv: conv, FixedBytes: 1000, Window: 1 << 20, MaxTokens: 4096})
	if !out.Fits || out.Stubbed != 0 || &out.Conv[0] != &conv[0] || len(out.Conv) != len(conv) {
		t.Fatalf("sınırın altında dilim aynen dönmeli: %+v", out.Stubbed)
	}
}

func TestPlanRoundCompactionStubsOldestKeepsLatestPerFamily(t *testing.T) {
	conv := budgetConv()
	before, _ := json.Marshal(conv)
	full := 1000 + chatConvBytes(conv)
	est := roundEstimate(full, 0, 0, 0)
	// bir saplama yetecek kadar aşım
	w := windowForLimit(est-500, 4096)
	out := planRoundCompaction(roundCompactIn{Conv: conv, FixedBytes: 1000, Window: w, MaxTokens: 4096})
	if !out.Fits || out.Stubbed != 1 || out.DroppedPairs != 0 || out.Trimmed != 1 {
		t.Fatalf("tek saplama bekleniyordu: %+v", out)
	}
	pairsIntact(t, out.Conv)
	r0, _ := resultByID(out.Conv, "c0")
	if !isStub(r0.Content) || !strings.Contains(r0.Content, "compare_periods (service=checkout, range_s=3600)") || !strings.Contains(r0.Content, `ilk satırı: {"tool":"r0"`) {
		t.Fatalf("en eski sonuç saplama değil: %q", r0.Content)
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		if r, _ := resultByID(out.Conv, id); isStub(r.Content) {
			t.Fatalf("%s kısaltılmamalıydı", id)
		}
	}
	// daha çok aşım: R1 de; R2 (Metrik'in sonu) ve R3 (son tur) asla
	w2 := windowForLimit(est-1800, 4096)
	out2 := planRoundCompaction(roundCompactIn{Conv: conv, FixedBytes: 1000, Window: w2, MaxTokens: 4096})
	if !out2.Fits || out2.Stubbed != 2 {
		t.Fatalf("iki saplama bekleniyordu: %+v", out2)
	}
	for id, want := range map[string]bool{"c0": true, "c1": true, "c2": false, "c3": false} {
		if r, _ := resultByID(out2.Conv, id); isStub(r.Content) != want {
			t.Fatalf("%s saplama=%v bekleniyordu", id, want)
		}
	}
	if out2.Conv[0].Text != conv[0].Text {
		t.Fatal("kullanıcı mesajı değişti")
	}
	after, _ := json.Marshal(conv)
	if string(before) != string(after) {
		t.Fatal("girdi konuşması değişti (döngünün conv'u doğrulayıcıya tam gider)")
	}
}

func TestPlanRoundCompactionDropsWholePairsThenFallsBack(t *testing.T) {
	// beş tur: R0..R2 aynı aile (Metrik), R3 Sağlık, R4 Metrik (son tur)
	conv := []copilot.ChatMessage{{Role: "user", Text: "soru"}}
	conv = append(conv, budgetPair("c0", "compare_periods", `{"service":"a"}`, bigResult("r0", 4000))...)
	conv = append(conv, budgetPair("c1", "compare_periods", `{"service":"b"}`, bigResult("r1", 4000))...)
	conv = append(conv, budgetPair("c2", "get_service_health", `{"service":"c"}`, bigResult("r2", 4000))...)
	conv = append(conv, budgetPair("c3", "compare_periods", `{"service":"d"}`, bigResult("r3", 4000))...)
	full := chatConvBytes(conv)
	est := roundEstimate(full, 0, 0, 0)
	// saplamalar (R0, R1) yetmez → eski çiftler bütün çıkar
	stubSave := 2 * (len(bigResult("r0", 4000)) - 400)
	w := windowForLimit(est-(stubSave/3)-200, 4096)
	out := planRoundCompaction(roundCompactIn{Conv: conv, Window: w, MaxTokens: 4096})
	if !out.Fits || out.DroppedPairs == 0 {
		t.Fatalf("çift çıkarılması bekleniyordu: %+v", out)
	}
	pairsIntact(t, out.Conv)
	if _, ok := resultByID(out.Conv, "c0"); ok {
		t.Fatal("en eski çift çıkmadı")
	}
	for _, id := range []string{"c2", "c3"} {
		if r, ok := resultByID(out.Conv, id); !ok || isStub(r.Content) && id == "c3" {
			t.Fatalf("%s korunmalıydı", id)
		}
	}
	if out.Conv[0].Text != "soru" {
		t.Fatal("kullanıcı mesajı çıktı")
	}
	note := fmt.Sprintf(roundBudgetDropNoteFmt, out.DroppedPairs)
	found := false
	for _, m := range out.Conv {
		if len(m.ToolResults) > 0 {
			found = strings.HasPrefix(m.ToolResults[0].Content, note)
			break
		}
	}
	if !found {
		t.Fatal("çıkarılan turlar modele söylenmedi")
	}
	// hiçbir daraltma sığdıramıyor → girdi aynen (tepkisel yol)
	none := planRoundCompaction(roundCompactIn{Conv: conv, Window: windowForLimit(10, 4096), MaxTokens: 4096})
	if none.Fits || none.Stubbed != 0 || &none.Conv[0] != &conv[0] {
		t.Fatalf("sığmayınca dokunulmamalı: %+v", none)
	}
}

func TestPlanRoundCompactionTraceFollowUpStubsOtherFamiliesFirst(t *testing.T) {
	conv := []copilot.ChatMessage{{Role: "user", Text: "bu trace neden yavaş?"}}
	conv = append(conv, budgetPair("c0", "get_logs_for_trace", `{"trace_id":"t1"}`, bigResult("log0", 4000))...)
	conv = append(conv, budgetPair("c1", "compare_periods", `{"service":"checkout"}`, bigResult("m1", 4000))...)
	conv = append(conv, budgetPair("c2", "get_logs_for_trace", `{"trace_id":"t2"}`, bigResult("log2", 4000))...)
	conv = append(conv, budgetPair("c3", "compare_periods", `{"service":"payments"}`, bigResult("m3", 4000))...)
	conv = append(conv, budgetPair("c4", "get_service_health", `{"service":"payments"}`, bigResult("h4", 4000))...)
	est := roundEstimate(chatConvBytes(conv), 0, 0, 0)
	w := windowForLimit(est-500, 4096)
	gen := planRoundCompaction(roundCompactIn{Conv: conv, Window: w, MaxTokens: 4096})
	if r, _ := resultByID(gen.Conv, "c0"); gen.Stubbed != 1 || !isStub(r.Content) {
		t.Fatal("genel sohbette en eski (log) sonucu önce kısaltılmalı")
	}
	tr := planRoundCompaction(roundCompactIn{Conv: conv, Window: w, MaxTokens: 4096, TraceFollowUp: true})
	r0, _ := resultByID(tr.Conv, "c0")
	r1, _ := resultByID(tr.Conv, "c1")
	if tr.Stubbed != 1 || isStub(r0.Content) || !isStub(r1.Content) {
		t.Fatal("trace takibinde log/trace dışı aile önce kısaltılmalı")
	}
}

func TestRoundBudgetDeepReserveIsLarger(t *testing.T) {
	// 128k adlı model: Derin payı pencerenin yarısıyla sınırlanmaz (8k adlı modelde base = deep)
	cop := copilot.New(copilot.ProviderOpenAI, "k", "gemma4-31b")
	cop.Configure(copilot.ProviderOpenAI, "k", "gemma4-31b", "http://127.0.0.1:1", false, true)
	s := &Server{copilot: cop}
	base := s.roundBudgetMaxTokens(context.Background())
	deep := s.roundBudgetMaxTokens(copilot.WithDeepAnswer(context.Background()))
	if base <= 0 || deep <= base {
		t.Fatalf("Derin cevap payı büyük olmalı: base=%d deep=%d", base, deep)
	}
	if c := s.roundBudgetMaxTokens(copilot.WithMaxTokensCap(context.Background(), 700)); c != 700 {
		t.Fatalf("ctx tavanı: %d", c)
	}
	conv := budgetConv()
	est := roundEstimate(chatConvBytes(conv), 0, 0, 0)
	w := windowForLimit(est, base) // normal kipte tam sığar
	if out := planRoundCompaction(roundCompactIn{Conv: conv, Window: w, MaxTokens: base}); out.Stubbed != 0 {
		t.Fatal("normal kipte daraltma beklenmiyordu")
	}
	if out := planRoundCompaction(roundCompactIn{Conv: conv, Window: w, MaxTokens: deep}); out.Stubbed+out.DroppedPairs == 0 && out.Fits {
		t.Fatal("Derin kipin büyük cevap payı aynı pencerede yer ayırmalı")
	}
}

// ── uçtan uca: gerçek handler + sahte sağlayıcı ───────────────────────────

type budgetLLM struct {
	mu     sync.Mutex
	raw    [][]byte
	srv    *httptest.Server
	window int                    // >0 → GET /models bunu bildirir (Source "server")
	tokens func(raw []byte) int   // prompt_tokens; nil = 0 (bildirmez)
	reply  func(n int) (int, any) // n. model isteği → (status, mesaj)
}

func newBudgetLLM(t *testing.T, window int, reply func(n int) (int, any)) *budgetLLM {
	t.Helper()
	b := &budgetLLM{window: window, reply: reply}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if b.window > 0 && strings.HasSuffix(r.URL.Path, "/models") {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "gemma4", "max_model_len": b.window}}})
				return
			}
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/api/show") {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		b.mu.Lock()
		n := len(b.raw)
		b.raw = append(b.raw, raw)
		b.mu.Unlock()
		st, msg := b.reply(n)
		w.Header().Set("Content-Type", "application/json")
		if st != http.StatusOK {
			w.WriteHeader(st)
			_, _ = io.WriteString(w, `{"error":{"message":"This model's maximum context length is 8192 tokens. However, your messages resulted in 9100 tokens."}}`)
			return
		}
		pt := 0
		if b.tokens != nil {
			pt = b.tokens(raw)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": msg, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": pt, "completion_tokens": 9},
		})
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *budgetLLM) bodies() [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]byte(nil), b.raw...)
}

// bigTools — her araç sonucu büyük (v1 6000 rune / v2 2048 bayt tavanını doldurur).
func bigTools(t *testing.T) {
	t.Helper()
	chatLoopHandlerSeam = func(name string, _ mcp.ToolHandler) mcp.ToolHandler {
		return func(_ context.Context, raw json.RawMessage) (any, error) {
			var args map[string]any
			_ = json.Unmarshal(raw, &args)
			return map[string]any{"a_rows": strings.Repeat(name+" satır ", 900), "service": args["service"],
				"source": map[string]any{"source": "traces", "backend": "clickhouse", "state": "ok"}}, nil
		}
	}
	t.Cleanup(func() { chatLoopHandlerSeam = nil })
}

// fourRounds — R0 Metrik, R1 Sağlık, R2 Metrik, R3 Sağlık, sonra cevap.
func fourRounds(n int) (int, any) {
	switch n {
	case 0:
		return 200, toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
	case 1:
		return 200, toolCallMsg([2]string{"get_service_health", `{"service":"orders"}`})
	case 2:
		return 200, toolCallMsg([2]string{"compare_periods", `{"service":"payments"}`})
	case 3:
		return 200, toolCallMsg([2]string{"get_service_health", `{"service":"payments"}`})
	}
	return 200, answerMsg("**Bulgu** — payments p95 yükseldi.")
}

type budgetRun struct {
	sse    string
	bodies [][]byte
	rec    copilot.CallRecord
}

func runBudgetLoop(t *testing.T, v2 bool, window int, reply func(int) (int, any), tokens func([]byte) int) budgetRun {
	t.Helper()
	noRetryPause(t)
	llm := newBudgetLLM(t, window, reply)
	llm.tokens = tokens
	s, _ := newLoopTestServer(t, &loopLLM{srv: llm.srv}, "u-golden")
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 64)}
	s.copilot.SetRecorder(rec)
	bigTools(t)
	var body string
	if v2 {
		setCosreV2(t, s, true)
		body = serveV2(s, chatGoldenRequest("/api/cosre/v2/chat", chatGoldenLoopBody(), true)).Body.String()
	} else {
		w := httptest.NewRecorder()
		s.copilotChat(w, chatGoldenRequest("/api/copilot/chat", chatGoldenLoopBody(), true))
		body = w.Body.String()
	}
	return budgetRun{sse: body, bodies: llm.bodies(), rec: loopTimingRecord(t, rec)}
}

// reqToolContents — openai gövdesindeki role:tool mesajları (tool_call_id → içerik) ve çift bütünlüğü.
func reqToolContents(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	var body struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    any    `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	open := map[string]bool{}
	for _, m := range body.Messages {
		for _, tc := range m.ToolCalls {
			open[tc.ID] = true
		}
		if m.Role == "tool" {
			if !open[m.ToolCallID] {
				t.Fatalf("öksüz araç sonucu %s", m.ToolCallID)
			}
			delete(open, m.ToolCallID)
			s, _ := m.Content.(string)
			out[m.ToolCallID+"#"+fmt.Sprint(len(out))] = s
		}
	}
	if len(open) != 0 {
		t.Fatalf("sonuçsuz çağrı: %v", open)
	}
	return out
}

func stubCount(contents map[string]string) int {
	n := 0
	for _, c := range contents {
		if isStub(c) {
			n++
		}
	}
	return n
}

// Sınırın altında (pencere bilinen ve geniş) sağlayıcıya giden bayt, pencere
// bilinmezkiyle BİREBİR aynı — v1 ve v2.
func TestRoundBudgetUnderThresholdByteIdentical(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("v2=%v", v2), func(t *testing.T) {
			tok := func(raw []byte) int { return len(raw) / 4 }
			unknown := runBudgetLoop(t, v2, 0, fourRounds, tok)
			wide := runBudgetLoop(t, v2, 1<<20, fourRounds, tok)
			if len(unknown.bodies) != 5 || len(wide.bodies) != len(unknown.bodies) {
				t.Fatalf("istek sayısı: %d / %d", len(unknown.bodies), len(wide.bodies))
			}
			for i := range unknown.bodies {
				if string(unknown.bodies[i]) != string(wide.bodies[i]) {
					t.Fatalf("istek %d farklı", i)
				}
			}
			if strings.Contains(wide.sse, "bağlam daraltıldı") || wide.rec.LoopCompactedResults != 0 {
				t.Fatal("sınırın altında daraltma izi")
			}
		})
	}
}

// loopMaxTok — sahte profilin cevap payı (newLoopTestServer ile aynı yapılandırma).
func loopMaxTok() int {
	cop := copilot.New(copilot.ProviderOpenAI, "k", "gemma4")
	cop.Configure(copilot.ProviderOpenAI, "k", "gemma4", "http://127.0.0.1:1", false, true)
	return (&Server{copilot: cop}).roundBudgetMaxTokens(context.Background())
}

// findCompactingWindow — son istekte kısaltma görülen ilk pencere: tahmin bayt/3
// (sağlayıcı token bildirmez); sınır son isteğin boyundan aşağı taranır.
func findCompactingWindow(t *testing.T, v2 bool, reply func(int) (int, any)) budgetRun {
	t.Helper()
	ref := runBudgetLoop(t, v2, 0, reply, nil)
	top := len(ref.bodies[len(ref.bodies)-1]) / 3
	mt := loopMaxTok()
	for lim := top; lim > top/2; lim -= 150 {
		r := runBudgetLoop(t, v2, windowForLimit(lim, mt), reply, nil)
		if stubCount(reqToolContents(t, r.bodies[len(r.bodies)-1])) > 0 {
			return r
		}
	}
	t.Fatal("daraltan pencere bulunamadı")
	return budgetRun{}
}

func TestRoundBudgetOverThresholdStubsOldest(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("v2=%v", v2), func(t *testing.T) {
			r := findCompactingWindow(t, v2, fourRounds)
			last := reqToolContents(t, r.bodies[len(r.bodies)-1])
			var stubbed, kept []string
			for k, c := range last {
				if isStub(c) {
					stubbed = append(stubbed, k)
				} else {
					kept = append(kept, k)
				}
			}
			if len(stubbed) == 0 || len(kept) < 2 {
				t.Fatalf("kısaltılan %v, tam kalan %v", stubbed, kept)
			}
			// son iki sonuç (R2 Metrik sonu, R3 son tur) tam
			for _, c := range last {
				if isStub(c) && !strings.Contains(c, "(service=checkout)") && !strings.Contains(c, "(service=orders)") {
					t.Fatalf("yeni sonuç kısaltıldı: %q", c)
				}
			}
			if !strings.Contains(r.sse, "bağlam daraltıldı: ") || !strings.Contains(r.sse, "eski araç sonucu kısaltıldı") {
				t.Fatal("adım satırı yok")
			}
			if r.rec.LoopCompactedResults == 0 || int(r.rec.LoopCompactedResults) != len(stubbed) {
				t.Fatalf("kayıt: %d, istekte %d", r.rec.LoopCompactedResults, len(stubbed))
			}
			if strings.Contains(r.sse, "bağlam taştı") {
				t.Fatal("tepkisel yol devreye girmemeli")
			}
		})
	}
}

// Ad tahmini (sunucu pencereyi bildirmiyor) → proaktif adım yok, dar pencere bile olsa.
func TestRoundBudgetUnknownWindowNoAction(t *testing.T) {
	prev := roundBudgetWindowSeam
	roundBudgetWindowSeam = func(context.Context) copilot.ContextWindowInfo {
		return copilot.ContextWindowInfo{Tokens: 6000, Source: "name"}
	}
	t.Cleanup(func() { roundBudgetWindowSeam = prev })
	r := runBudgetLoop(t, false, 0, fourRounds, nil)
	for _, b := range r.bodies {
		if stubCount(reqToolContents(t, b)) != 0 {
			t.Fatal("ad tahmininde kısaltma yapıldı")
		}
	}
	if strings.Contains(r.sse, "bağlam daraltıldı") || r.rec.LoopCompactedResults != 0 {
		t.Fatal("ad tahmininde iz")
	}
}

// Hiçbir daraltma sığdıramıyor → istek bugünkü gibi gider; taşmada tepkisel yol
// TAM bir kez (yeniden denemede proaktif adım yok).
func TestRoundBudgetCannotFitReactiveOnce(t *testing.T) {
	reply := func(n int) (int, any) {
		switch n {
		case 0, 1, 2:
			return fourRounds(n)
		case 3, 4: // 4. tur: akış + buffered düşüşü ikisi de taşar
			return http.StatusBadRequest, nil
		}
		return 200, answerMsg("**Bulgu** — payments p95 yükseldi.")
	}
	r := runBudgetLoop(t, false, windowForLimit(2000, loopMaxTok()), reply, nil) // katalog bile sığmaz
	if n := strings.Count(r.sse, "bağlam taştı — geçmiş küçültülüp yeniden deneniyor"); n != 1 {
		t.Fatalf("tepkisel adım %d kez", n)
	}
	if strings.Contains(r.sse, "bağlam daraltıldı") {
		t.Fatal("sığdıramayan proaktif adım iz bıraktı")
	}
	for _, b := range r.bodies {
		if stubCount(reqToolContents(t, b)) != 0 {
			t.Fatal("sığmayan istekte kısaltma")
		}
	}
	// sahte uç akışı desteklemiyor (kararı önbellekte): tur başına bir istek
	if len(r.bodies) != 5 {
		t.Fatalf("%d istek, 5 bekleniyordu (3 tur + taşan tur + yeniden deneme)", len(r.bodies))
	}
}

// ── doğrulayıcı ve durum defteri: tam çıktıyı okur ────────────────────────

func TestRoundBudgetVerifierAndLedgerUnaffected(t *testing.T) {
	setConversationState(t, true)
	loop := func(_ string, n int) map[string]any {
		_, m := fourRounds(n)
		mm, _ := m.(map[string]any)
		if n == 4 {
			return answerMsg(verifyFlowAnswer)
		}
		return mm
	}
	run := func(window int) (string, ChatContext, string, int) {
		prev := roundBudgetWindowSeam
		roundBudgetWindowSeam = func(context.Context) copilot.ContextWindowInfo {
			if window == 0 {
				return copilot.ContextWindowInfo{}
			}
			return copilot.ContextWindowInfo{Tokens: window, Source: "server"}
		}
		defer func() { roundBudgetWindowSeam = prev }()
		setVerification(t, "shadow")
		vc := captureVerify(t)
		rg := newStateRig(t, loop, nil)
		bigTools(t)
		body := rg.ask("checkout'ta sabah bir şey mi oldu", nil)
		stubs := 0
		for _, c := range rg.llm.calls() {
			raw, _ := json.Marshal(c)
			stubs += strings.Count(string(raw), "[kısaltıldı: bağlam penceresine")
		}
		verify := ""
		if len(vc.rows) == 1 {
			verify = vc.rows[0].Verify
		}
		return verify, rg.state(), body, stubs
	}
	baseVerify, baseState, _, baseStubs := run(0)
	if baseStubs != 0 || baseVerify == "" {
		t.Fatalf("taban: verify=%q stubs=%d", baseVerify, baseStubs)
	}
	// daraltan pencere: sahte sağlayıcı 11 token bildirir → tahmin 11 + eklenen bayt / 6
	found := false
	mt := loopMaxTok()
	for lim := 25; lim < 3000 && !found; lim += 25 {
		v, st, body, stubs := run(windowForLimit(lim, mt))
		if stubs == 0 || strings.Contains(body, "bağlam taştı") {
			continue
		}
		found = true
		if v != baseVerify {
			t.Fatalf("doğrulayıcı daraltmadan etkilendi: %q ≠ %q", v, baseVerify)
		}
		if !reflect.DeepEqual(st.Ledger, baseState.Ledger) {
			t.Fatalf("defter daraltmadan etkilendi:\n%+v\n%+v", st.Ledger, baseState.Ledger)
		}
	}
	if !found {
		t.Fatal("daraltan pencere bulunamadı — test bir şey ölçmüyor")
	}
}

// ── maliyet ───────────────────────────────────────────────────────────────

// Sınırın altındaki tahmin: katalog önbellekte, konuşma serileştirilmez.
func BenchmarkRoundBudgetEstimate(b *testing.B) {
	conv := []copilot.ChatMessage{{Role: "user", Text: "checkout neden yavaş?"}}
	for i := 0; i < 8; i++ {
		conv = append(conv, budgetPair(fmt.Sprintf("c%d", i), "compare_periods", `{"service":"checkout"}`, bigResult("r", 6000))...)
	}
	specs := make([]copilot.ToolSpec, 60)
	for i := range specs {
		specs[i] = copilot.ToolSpec{Name: fmt.Sprintf("tool_%d", i), Description: strings.Repeat("d", 400),
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"service": map[string]any{"type": "string"}}}}
	}
	bud := &roundBudget{window: 1 << 20, resolved: true}
	s := &Server{}
	ctx := withRoundBudget(context.Background(), bud)
	sys := strings.Repeat("s", 8000)
	emit := func(string, any) {}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.fitRoundBudget(ctx, emit, sys, conv, specs)
	}
}
