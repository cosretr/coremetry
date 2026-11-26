package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

// forceTraceExplainStyle — testte trace açıklaması biçimini sabitler.
func forceTraceExplainStyle(t *testing.T, style string) {
	t.Helper()
	prev := chstore.CurrentCosreConversation()
	c := prev
	c.TraceExplainStyle = style
	chstore.SetCosreConversation(c)
	t.Cleanup(func() { chstore.SetCosreConversation(prev) })
}

// ── sentetik trace'ler (yalnız uydurma adlar) ─────────────────────────

var nrT0 = time.Date(2026, 11, 26, 17, 37, 2, 0, time.UTC).UnixNano()

func nrSpan(id, parent, svc, name, kind string, startMs, durMs int64) chstore.SpanRow {
	return chstore.SpanRow{TraceID: "t1", SpanID: id, ParentSpanID: parent, ServiceName: svc, Name: name, Kind: kind,
		StartTime: nrT0 + startMs*1e6, EndTime: nrT0 + (startMs+durMs)*1e6, DurationMs: float64(durMs), StatusCode: "ok",
		Attributes: map[string]string{}, ResourceAttributes: map[string]string{"k8s.pod.name": svc + "-7d9f-abcde"}}
}

func nrFail(sp chstore.SpanRow, code uint16, msg string) chstore.SpanRow {
	sp.StatusCode, sp.HTTPStatus, sp.StatusMessage = "error", code, msg
	return sp
}

func nrException(sp chstore.SpanRow, typ, msg string) chstore.SpanRow {
	sp.StatusCode = "error"
	sp.Events = []any{map[string]any{"name": "exception", "attributes": map[string]any{"exception.type": typ, "exception.message": msg}}}
	return sp
}

// iş kuralı reddi: orders-api SERVER → CLIENT POST payments-api 400 (hızlı).
func nrBusinessRejection() []chstore.SpanRow {
	root := nrFail(nrSpan("r1", "", "orders-api", "POST /orders", "server", 0, 60), 400, "")
	call := nrFail(nrSpan("c1", "r1", "orders-api", "POST", "client", 10, 30), 400, "")
	call.Attributes["url.full"] = "http://payments-api.internal/charge"
	call.Attributes["request_id"] = "req-0001"
	return []chstore.SpanRow{root, call}
}

func nrDependency5xx() []chstore.SpanRow {
	root := nrFail(nrSpan("r1", "", "orders-api", "POST /orders", "server", 0, 90), 500, "")
	call := nrFail(nrSpan("c1", "r1", "orders-api", "POST", "client", 10, 60), 503, "")
	call.Attributes["url.full"] = "http://inventory-api.internal/reserve"
	return []chstore.SpanRow{root, call}
}

func nrTimeout() []chstore.SpanRow {
	root := nrFail(nrSpan("r1", "", "orders-api", "GET /orders/{id}", "server", 0, 5010), 504, "")
	call := nrException(nrSpan("c1", "r1", "orders-api", "GET", "client", 5, 5000), "java.net.SocketTimeoutException", "Read timed out")
	call.Attributes["url.full"] = "http://shipping-api.internal/quote"
	return []chstore.SpanRow{root, call}
}

func nrSlowExternal() []chstore.SpanRow {
	root := nrSpan("r1", "", "orders-api", "GET /orders", "server", 0, 2100)
	db := nrSpan("d1", "r1", "orders-api", "SELECT orders", "client", 5, 40)
	db.DBSystem = "postgresql"
	call := nrSpan("c1", "r1", "orders-api", "GET", "client", 60, 2000)
	call.Attributes["url.full"] = "https://rates.example.test/v1/fx"
	return []chstore.SpanRow{root, db, call}
}

func nrSuccess() []chstore.SpanRow {
	return []chstore.SpanRow{nrSpan("r1", "", "orders-api", "GET /health", "server", 0, 3), nrSpan("c1", "r1", "orders-api", "GET", "internal", 1, 1)}
}

func nrView(spans []chstore.SpanRow, logs []*logstore.LogRecord) *traceEvidenceView {
	ch := tracechain.Of(spans)
	return buildTraceEvidenceView(traceEvidenceSrc{Spans: spans, Analyzed: len(spans), Chain: ch, Crit: tracechain.CriticalPathOf(spans),
		Logs: logs, LogOutcome: traceLogOutcome{State: "rows", Rows: len(logs)}})
}

func TestTraceEvidenceViewSyntheticTraces(t *testing.T) {
	logs := []*logstore.LogRecord{
		{Timestamp: nrT0 + 41e6, Severity: 17, SeverityText: "ERROR", ServiceName: "orders-api", SpanID: "c1", Body: "charge rejected: amount must be positive (code VAL-001)\n\tat x.y",
			ResourceAttributes: map[string]string{"k8s.pod.name": "orders-api-7d9f-abcde"}},
		{Timestamp: nrT0 + 42e6, Severity: 17, SeverityText: "ERROR", ServiceName: "orders-api", SpanID: "c1", Body: "charge rejected: amount must be positive (code VAL-001)"},
		{Timestamp: nrT0 + 1e6, Severity: 9, SeverityText: "INFO", ServiceName: "orders-api", Body: "request received"},
	}
	t.Run("business rejection", func(t *testing.T) {
		v := nrView(nrBusinessRejection(), logs)
		if len(v.Chain) != 2 || v.Chain[0].Status != 400 || !strings.Contains(v.Chain[0].Call, "payments-api.internal") || v.Chain[0].SpanID != "c1" {
			t.Fatalf("zincir %+v", v.Chain)
		}
		if v.Origin == nil || v.Origin.Service != "orders-api" || v.Origin.Pod != "orders-api-7d9f-abcde" {
			t.Fatalf("köken %+v", v.Origin)
		}
		if len(v.Logs) != 1 || v.Logs[0].Count != 2 || v.Logs[0].TsNs != nrT0+41e6 || !strings.Contains(v.Logs[0].Message, "VAL-001") {
			t.Fatalf("loglar: ERROR deseni tek satır ×2, INFO düşer: %+v", v.Logs)
		}
		if !strings.Contains(v.Fallback, "HTTP 400") || !strings.Contains(v.Fallback, "POST /orders") {
			t.Fatalf("fallback %q", v.Fallback)
		}
		if len(v.Fields) == 0 || v.Fields[0].V != "req-0001" {
			t.Fatalf("iş alanları %+v", v.Fields)
		}
	})
	t.Run("dependency 5xx", func(t *testing.T) {
		v := nrView(nrDependency5xx(), nil)
		if v.Chain[0].Status != 503 || !v.Chain[0].Failed || !strings.Contains(v.Fallback, "HTTP 503") {
			t.Fatalf("%+v / %q", v.Chain, v.Fallback)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		v := nrView(nrTimeout(), nil)
		if !strings.Contains(v.Chain[0].Error, "SocketTimeoutException: Read timed out") {
			t.Fatalf("exception aynen: %+v", v.Chain[0])
		}
		if v.Critical == nil || v.Critical.SpanID != "c1" {
			t.Fatalf("kritik yol %+v", v.Critical)
		}
	})
	t.Run("slow external wait", func(t *testing.T) {
		v := nrView(nrSlowExternal(), nil)
		if len(v.Chain) != 0 || v.Critical == nil || v.Critical.Mode != "external" || v.Critical.SpanID != "c1" || v.Critical.SharePct < 90 || v.Critical.DurMs != 2000 {
			t.Fatalf("dış bekleme %+v", v.Critical)
		}
		if !strings.HasPrefix(v.Fallback, "Hata yok. Sürenin en büyük payı:") || !strings.Contains(v.Fallback, "rates.example.test") {
			t.Fatalf("fallback %q", v.Fallback)
		}
	})
	t.Run("success", func(t *testing.T) {
		v := nrView(nrSuccess(), nil)
		if len(v.Chain) != 0 || v.Origin != nil || v.Fallback == "" {
			t.Fatalf("%+v", v)
		}
		b, _ := json.Marshal(v)
		if len(b) > 1024 {
			t.Fatalf("başarılı trace görünümü küçük kalmalı: %d", len(b))
		}
	})
}

func TestTraceEvidenceViewBounds(t *testing.T) {
	spans := []chstore.SpanRow{nrFail(nrSpan("r", "", "orders-api", "POST /orders", "server", 0, 100), 500, "")}
	parent := "r"
	for i := 0; i < 20; i++ { // derin zincir
		id := fmt.Sprintf("s%02d", i)
		spans = append(spans, nrFail(nrSpan(id, parent, "svc-"+id, "call "+id, "client", int64(i), 50), 500, ""))
		parent = id
	}
	var logs []*logstore.LogRecord
	for i := 0; i < 50; i++ {
		logs = append(logs, &logstore.LogRecord{Timestamp: nrT0, Severity: 17, ServiceName: "svc", Body: fmt.Sprintf("err %d %s", i, strings.Repeat("x", 500))})
	}
	v := nrView(spans, logs)
	if len(v.Chain) != traceEvChainMax || v.ChainSkip == 0 || len(v.Logs) != traceEvLogsMax || len([]rune(v.Logs[0].Message)) > traceEvTextRunes+1 {
		t.Fatalf("tavanlar: zincir %d (atlanan %d), log %d", len(v.Chain), v.ChainSkip, len(v.Logs))
	}
	if b, _ := json.Marshal(v); len(b) > 8<<10 {
		t.Fatalf("görünüm %d bayt (≤8 KiB)", len(b))
	}
}

// TestTraceNarrativeInputBudget — anlatım biçimi bölümlü biçimle AYNI tam
// kanıtı alır (girdi kırpılmaz); bütçe yalnız tavanı aşan trace'te devreye
// girer: span'ler hatalar + en yavaşlar önce (pickExplainSpans).
func TestTraceNarrativeInputBudget(t *testing.T) {
	in := traceExplainInput{User: "Trace t1 with 2 spans:\n```json\n[]\n```"}
	if _, u := traceExplainPromptFor(true, in); u != in.User {
		t.Fatal("anlatım girdisi tam kanıt olmalı")
	}
	hits := 0
	for _, sp := range [][]chstore.SpanRow{nrBusinessRejection(), nrDependency5xx(), nrTimeout(), nrSlowExternal(), nrSuccess()} {
		if len(pickExplainSpans(sp, traceExplainSpanCap)) < len(sp) {
			hits++
		}
	}
	if hits != 0 {
		t.Fatalf("küçük sentetik trace'ler bütçeye takılmamalı: %d", hits)
	}
	big := nrBusinessRejection()
	for i := 0; i < 150; i++ {
		big = append(big, nrSpan(fmt.Sprintf("x%03d", i), "r1", "orders-api", fmt.Sprintf("step %d", i), "internal", 1, 1))
	}
	picked := pickExplainSpans(big, traceExplainSpanCap)
	keep := map[string]bool{}
	for _, sp := range picked {
		keep[sp.SpanID] = true
	}
	if len(picked) != traceExplainSpanCap || !keep["c1"] || !keep["r1"] {
		t.Fatalf("152 span → %d; hata kaynağı ve kök korunmalı", len(picked))
	}
}

func TestTraceNarrativeCtxThinkingAndCap(t *testing.T) {
	ctx := traceNarrativeCtx(context.Background())
	if copilot.ThinkSurfaceOf(ctx) != copilot.ThinkTraceNarrative || copilot.AnswerCapOf(ctx) != traceNarrativeMaxTokens {
		t.Fatalf("yüzey %s tavan %d", copilot.ThinkSurfaceOf(ctx), copilot.AnswerCapOf(ctx))
	}
	if copilot.DefaultThinkingModes()[string(copilot.ThinkTraceNarrative)] != copilot.ThinkModeOff ||
		copilot.DefaultThinkingModes()[string(copilot.ThinkTrace)] != copilot.ThinkModeOn {
		t.Fatal("anlatım düşünmesi varsayılan kapalı, bölümlü açık")
	}
}

// TestChatExplainTurnTraceNarrative — varsayılan biçim: kanıt bloğu modelden
// ÖNCE, anlatım istemi + kısa girdi, künye çizim kipi, çıktı tavanı.
func TestChatExplainTurnTraceNarrative(t *testing.T) {
	forceTraceExplainStyle(t, chstore.TraceExplainNarrative)
	p := newDefProvider(t, "Bağımlılık hatası\nKARAR: checkout GET /cart isteği payments POST /charge çağrısının ", "\"card declined\" hatasıyla bitti.\nSonraki adım: payments loglarına bakın.")
	s := defServer(t, p)
	var evs []xtEvent
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-n-1"})
	if h, ok := s.chatExplainTurn(ctx, xtRecorder(&evs), &chatExplainAsk{Kind: "trace", ID: defTestTrace}, time.UTC); !h || !ok {
		t.Fatalf("%s", xtKinds(evs))
	}
	if got := xtKinds(evs); got != "step,step-result,"+blocks.TypeTraceEvidence+",delta,delta,answer" {
		t.Fatalf("olay sırası %s (kanıt bloğu deltalardan önce)", got)
	}
	view, _ := evs[2].data.(*traceEvidenceView)
	if view == nil || len(view.Chain) == 0 || view.Fallback == "" {
		t.Fatalf("kanıt %+v", evs[2].data)
	}
	sys, user := p.lastMessages(t)
	if sys != copilot.SystemPromptTraceNarrative() || !strings.Contains(user, "```json\n[{") || !strings.HasPrefix(user, tracechain.Header) { // tam kanıt (bölümlüyle aynı girdi)
		t.Fatalf("istem/girdi: %.80q / %.200q", sys, user)
	}
	p.mu.Lock()
	body := p.bodies[len(p.bodies)-1]
	p.mu.Unlock()
	if mt, _ := body["max_tokens"].(float64); int(mt) != traceNarrativeMaxTokens {
		if mc, _ := body["max_completion_tokens"].(float64); int(mc) != traceNarrativeMaxTokens {
			t.Fatalf("çıktı tavanı: %v / %v", body["max_tokens"], body["max_completion_tokens"])
		}
	}
	ans := xtAnswer(t, evs)
	meta, _ := ans["explain"].(chatExplainMeta)
	if meta.Renderer != explainRendererTraceNarrative || meta.Surface != aisurface.ExplainTraceChat {
		t.Fatalf("künye %+v", meta)
	}
	if !strings.Contains(ans["text"].(string), "KARAR:") {
		t.Fatalf("metin %v", ans["text"])
	}
	if _, hit, _ := s.cache.Get(context.Background(), explainCacheKey(sys, user, "")); !hit {
		t.Fatal("anlatım anahtarına yazılmadı")
	}
	// bölümlü anahtarla çakışmaz: biçim değişince eski cevap gelmez.
	forceTraceExplainStyle(t, chstore.TraceExplainSections)
	evs = nil
	if h, ok := s.chatExplainTurn(ctx, xtRecorder(&evs), &chatExplainAsk{Kind: "trace", ID: defTestTrace}, time.UTC); !h || !ok {
		t.Fatal("bölümlü tur")
	}
	if sys2, _ := p.lastMessages(t); sys2 != copilot.SystemPromptTrace() || strings.Contains(xtKinds(evs), blocks.TypeTraceEvidence) {
		t.Fatalf("bölümlü biçim bayt bayt eski yol: %s", xtKinds(evs))
	}
}

// TestTraceEvidenceLogRow — her zaman görünen LOG satırı: seçim (hata
// kaynağının logu önce; ikinci satır yalnız başka servisin WARN/ERROR'u),
// korelasyon kimliği, üç ayrı boş durum (none / failed / nostore).
func TestTraceEvidenceLogRow(t *testing.T) {
	src := func(logs []*logstore.LogRecord, o traceLogOutcome) *traceEvidenceView {
		sp := nrBusinessRejection()
		return buildTraceEvidenceView(traceEvidenceSrc{Spans: sp, Analyzed: 2, Chain: tracechain.Of(sp), Crit: tracechain.CriticalPathOf(sp), Logs: logs, LogOutcome: o})
	}
	rows := traceLogOutcome{State: "rows", Rows: 7}
	origin := &logstore.LogRecord{Timestamp: nrT0 + 50e6, Severity: 13, SeverityText: "WARN", ServiceName: "orders-api", SpanID: "c1",
		Body: "charge rejected: amount must be positive", Attributes: map[string]string{"requestId": "req-0001"}}
	otherErr := &logstore.LogRecord{Timestamp: nrT0 + 20e6, Severity: 17, SeverityText: "ERROR", ServiceName: "payments-api", Body: "validation failed: amount"}
	sameSvc := &logstore.LogRecord{Timestamp: nrT0 + 10e6, Severity: 17, SeverityText: "ERROR", ServiceName: "orders-api", Body: "unrelated error"}

	v := src([]*logstore.LogRecord{otherErr, origin}, rows)
	if v.Logs[0].Message != origin.Body || v.Logs[0].CorrID != "requestId=req-0001" || v.LogsVisible != 2 || v.LogRows != 7 {
		t.Fatalf("kaynak logu önce (WARN da olsa), ikinci satır başka servis ERROR: %+v visible=%d", v.Logs, v.LogsVisible)
	}
	if v = src([]*logstore.LogRecord{origin, sameSvc}, rows); v.LogsVisible != 1 || len(v.Logs) != 2 {
		t.Fatalf("aynı servisin ikinci satırı görünmez (Kanıt'ta kalır): visible=%d", v.LogsVisible)
	}
	for _, c := range []struct {
		o             traceLogOutcome
		state, reason string
	}{
		{traceLogOutcome{State: "none"}, "none", ""},
		{traceLogOutcome{State: "failed", Reason: "timeout"}, "failed", "timeout"},
		{traceLogOutcome{State: "failed", Reason: "nostore"}, "failed", "nostore"},
	} {
		v := src(nil, c.o)
		if v.LogsVisible != 0 || len(v.Logs) != 0 || v.LogState != c.state || v.LogReason != c.reason {
			t.Fatalf("boş durum %+v → %+v", c.o, v)
		}
	}
}
