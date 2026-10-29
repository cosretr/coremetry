package api

// chat_clarify_synth_test.go — v0.10.1572 (operatör, 2026-10-29: "çipler çok
// kullanılmıyor, soru cevap şeklinde frontier model gibi olsun"), dilim 2:
// Oracle operasyon cevabının sonundaki düz metin trace sorusu ve olumlu
// cevaptaki TEK sentez cevabı (Oracle başlıkları + HATA ZİNCİRİ + stack
// dosya:satır).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

const (
	synthTID   = "4bf92f3577b34da6a3ce929d0e0e4736"
	synthFP    = "ora:00000000000000b2"
	synthChain = tracechain.Header + "\n1. orders-api · CLIENT · GET db.example.test /orders → HTTP 500 (120 ms)"
)

const synthStack = `java.sql.SQLException: ORA-00060: deadlock detected
	at com.acme.orders.OrderRepository.save(OrderRepository.java:88)
	at com.acme.orders.OrderService.place(OrderService.java:42)
	at org.springframework.web.servlet.FrameworkServlet.service(FrameworkServlet.java:897)`

func synthTraceInput() traceExplainInput {
	return traceExplainInput{User: synthChain + "\n\nTrace " + synthTID + " with 3 spans", ChainBlock: synthChain,
		RootService: "orders-api", ErrorText: "ORA-00060: deadlock detected", DBStatements: []string{"UPDATE orders SET state = :1"},
		Stack: synthStack, LogsBlock: "\n\nBu trace'in ilişkili LOGLARI: ERROR orders-api kayıt yazılamadı", Evidence: []string{"sp-1"}}
}

func withSynthTraceSeam(t *testing.T, err error) *[]string {
	t.Helper()
	var asked []string
	traceExplainInputSeam = func(_ context.Context, id string) (traceExplainInput, error) {
		asked = append(asked, id)
		if err != nil {
			return traceExplainInput{}, err
		}
		return synthTraceInput(), nil
	}
	t.Cleanup(func() { traceExplainInputSeam = nil })
	return &asked
}

func TestPickSynthesisTrace(t *testing.T) {
	rows := []opTraceRow{{TraceID: "ok-1"}, {TraceID: "err-1", Error: true}, {TraceID: "err-2", Error: true}}
	for _, c := range []struct {
		name     string
		screen   opScreenTrace
		services []string
		traces   []opTraceRow
		want     string
	}{
		{"ekran trace'i, servis operasyonun", opScreenTrace{TraceID: "scr", Service: "orders-api"}, []string{"orders-api"}, rows, "scr"},
		{"ekran trace'i, servis bilinmiyor", opScreenTrace{TraceID: "scr"}, []string{"orders-api"}, rows, "scr"},
		{"ekran trace'i, operasyon servissiz", opScreenTrace{TraceID: "scr", Service: "x"}, nil, rows, "scr"},
		{"ekran başka servisin → ilk hatalı", opScreenTrace{TraceID: "scr", Service: "billing-api"}, []string{"orders-api"}, rows, "err-1"},
		{"ekran yok → ilk hatalı", opScreenTrace{}, []string{"orders-api"}, rows, "err-1"},
		{"hatalı yok", opScreenTrace{}, nil, []opTraceRow{{TraceID: "ok-1"}}, ""},
		{"boş", opScreenTrace{}, nil, nil, ""},
	} {
		if got := pickSynthesisTrace(c.screen, c.services, c.traces); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := opScreenTraceOf("", "", "trace:"+synthTID, "orders-api"); got.TraceID != synthTID || got.Service != "orders-api" {
		t.Errorf("özne trace'i: %+v", got)
	}
	if got := opScreenTraceOf("a", "b", "trace:c", ""); got.TraceID != "a" {
		t.Errorf("trace alanı önce: %+v", got)
	}
}

func TestSynthReplyClass(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"evet", synthReplyYes},
		{"Evet.", synthReplyYes},
		{"evet lütfen", synthReplyYes},
		{"evet, incele", synthReplyYes},
		{"olur bak", synthReplyYes},
		{"incele", synthReplyYes},
		{"inceleyelim", synthReplyYes},
		{"tamam stack'i de incele", synthReplyYes},
		{"hadi bakalım", synthReplyYes},
		{"ok", synthReplyYes},
		{"devam et", synthReplyYes},
		{"hayır", synthReplyNo},
		{"hayir gerek yok", synthReplyNo},
		{"gerek yok", synthReplyNo},
		{"yok", synthReplyNo},
		{"kalsın teşekkürler", synthReplyNo},
		{"istemiyorum", synthReplyNo},
		// Cevap değil.
		{"orders-api sağlığı nasıl?", clarifyReplyDropped},
		{"evet ama önce orders-api p99 değerini göster", clarifyReplyDropped},
		{"neden", clarifyReplyDropped},
		{"teşekkürler", clarifyReplyDropped},
		{"", clarifyReplyDropped},
	} {
		if got := synthReplyClass(c.in); got != c.want {
			t.Errorf("synthReplyClass(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Paylaşılan isAffirmative genişlemedi (guided uç onayı).
	if isAffirmative(normalizeGuidedMsg("tamam stack'i de incele")) {
		t.Error("isAffirmative genişlemiş — sentez listesi ayrı kalmalı")
	}
}

func TestSynthTraceBlockBounded(t *testing.T) {
	tin := synthTraceInput()
	b := synthTraceBlockTR(synthTID, tin, synthTraceBlockMax)
	for _, want := range []string{"TRACE KANITI (" + synthTID + ", kök servis orders-api):", tracechain.Header,
		"OrderRepository.java:88", "OrderService.java:42", "UPDATE orders", "Loglar:"} {
		if !strings.Contains(b, want) {
			t.Errorf("blokta %q olmalı:\n%s", want, b)
		}
	}
	if strings.Contains(b, "FrameworkServlet") {
		t.Error("kütüphane çerçevesi uygulama çerçevesi sayıldı")
	}
	// Bütçe: loglar önce düşer, zincir + çerçeveler kalır; tavan aşılmaz.
	tin.LogsBlock = strings.Repeat("log ", 600)
	small := synthTraceBlockTR(synthTID, tin, 700)
	if strings.Contains(small, "Loglar:") || !strings.Contains(small, "OrderService.java:42") || len([]rune(small)) > 700 {
		t.Fatalf("bütçe aşımında loglar düşmeli (%d rune):\n%s", len([]rune(small)), small)
	}
	if tiny := synthTraceBlockTR(synthTID, tin, 120); len([]rune(tiny)) > 120 {
		t.Fatalf("tavan aşıldı: %d", len([]rune(tiny)))
	}
	tin = traceExplainInput{}
	if b := synthTraceBlockTR(synthTID, tin, synthTraceBlockMax); !strings.Contains(b, "stack yok") || !strings.Contains(b, "HATA ZİNCİRİ: hesaplanamadı") {
		t.Fatalf("boş girdi dürüst olmalı:\n%s", b)
	}
}

// synthCtx — kullanıcılı sohbet bağlamı (bekleyen soru taşınabilir).
func synthCtx(st *chatContextState) context.Context {
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-synth"})
	return ctxWithChatContext(copilot.WithMeta(ctx, copilot.CallMeta{ExchangeID: "x-1"}), st)
}

func TestOpExplainAppendsTraceAsk(t *testing.T) {
	prov := &opExplainProvider{deltas: []string{"**Hata ne demek** ORD-200."}}
	s := newOpExplainServer(t, prov)
	s.cache = newMemCache()
	p, _ := opExplainFixture(t, nil)
	p.Services = []string{"orders-api"}
	p.Traces = []opTraceRow{{TraceID: "ok-1"}, {TraceID: synthTID, Service: "orders-api", Error: true}}
	st := &chatContextState{user: "u-synth"}
	var evs []opExplainEvent
	emit := withStepIDs(func(k string, v any) { evs = append(evs, opExplainEvent{k, v}) })
	if h, ok := s.opExceptionExplainInChat(synthCtx(st), emit, p, time.UTC); !h || !ok {
		t.Fatalf("handled=%v ok=%v", h, ok)
	}
	text := evs[len(evs)-1].data.(map[string]any)["text"].(string)
	if !strings.HasSuffix(text, "Exceptions açıklamasıyla aynı kanıt\n\n"+traceAskTextTR(synthTID)) {
		t.Fatalf("cevap sentez sorusuyla bitmeli: %q", text)
	}
	if traceAskTextTR(synthTID) != "Bağlamdaki 4bf92f35 trace'iyle birlikte hata zincirini ve stack'i de inceleyeyim mi?" {
		t.Fatalf("soru metni: %q", traceAskTextTR(synthTID))
	}
	pd := st.ctx.Pending
	if pd == nil || pd.Kind != pendingKindTraceSynth || pd.TraceID != synthTID || pd.Fingerprint != synthFP || pd.Operation != "ORDERS_GET_X" || !st.dirty {
		t.Fatalf("bekleyen soru: %+v", pd)
	}

	// Hatalı trace yok → cevap aynen, soru yok.
	p.Traces = []opTraceRow{{TraceID: "ok-1"}}
	st2 := &chatContextState{user: "u-synth"}
	evs = nil
	s.opExceptionExplainInChat(synthCtx(st2), emit, p, time.UTC)
	if text := evs[len(evs)-1].data.(map[string]any)["text"].(string); strings.Contains(text, "inceleyeyim mi") || st2.ctx.Pending != nil {
		t.Fatalf("trace yokken soru eklenmemeli: %q", text)
	}
	// Kullanıcısız bağlam → soru eklenmez (cevabı hatırlanamaz).
	p.Traces = []opTraceRow{{TraceID: synthTID, Error: true}}
	evs = nil
	s.opExceptionExplainInChat(synthCtx(&chatContextState{}), emit, p, time.UTC)
	if text := evs[len(evs)-1].data.(map[string]any)["text"].(string); strings.Contains(text, "inceleyeyim mi") {
		t.Fatalf("kullanıcısız turda soru eklenmemeli: %q", text)
	}
}

func TestOpExplainNonOracleNoTraceAsk(t *testing.T) {
	prov := &opExplainProvider{deltas: []string{"**Bulgu** IllegalStateException."}}
	s := newOpExplainServer(t, prov)
	s.cache = newMemCache()
	p, _ := opExplainFixture(t, nil)
	p.Exceptions.Groups = []exGroupLine{{Fingerprint: "00000000000000c3", Type: "java.lang.IllegalStateException", Priority: "P1", State: "new"}}
	p.Traces = []opTraceRow{{TraceID: synthTID, Error: true}}
	st := &chatContextState{user: "u-synth"}
	var evs []opExplainEvent
	s.opExceptionExplainInChat(synthCtx(st), withStepIDs(func(k string, v any) { evs = append(evs, opExplainEvent{k, v}) }), p, time.UTC)
	if text := evs[len(evs)-1].data.(map[string]any)["text"].(string); strings.Contains(text, "inceleyeyim mi") || st.ctx.Pending != nil {
		t.Fatalf("span exception grubunda sentez sorusu yok (yalnız Oracle): %q", text)
	}
}

func TestTraceSynthesisAnswer(t *testing.T) {
	prov := &opExplainProvider{deltas: []string{"**Hata ne demek** ORA-00060 kilitlenme. ", "**Çağıran taraf (Coremetry trace)** orders-api."}}
	s := newOpExplainServer(t, prov)
	mc := newMemCache()
	s.cache = mc
	_, askedFP := opExplainFixture(t, nil)
	askedTrace := withSynthTraceSeam(t, nil)
	p := &chatPendingAsk{Kind: pendingKindTraceSynth, TraceID: synthTID, Fingerprint: synthFP, Operation: "ORDERS_GET_X", AskedAt: time.Now().UnixNano()}
	var evs []opExplainEvent
	emit := withStepIDs(func(k string, v any) { evs = append(evs, opExplainEvent{k, v}) })
	h, ok := s.chatTraceSynthesisAnswer(synthCtx(&chatContextState{user: "u-synth"}), emit, p, time.UTC)
	if !h || !ok {
		t.Fatalf("handled=%v ok=%v", h, ok)
	}
	if len(*askedFP) != 1 || (*askedFP)[0] != synthFP || len(*askedTrace) != 1 || (*askedTrace)[0] != synthTID {
		t.Fatalf("kanıt: grup %v trace %v", *askedFP, *askedTrace)
	}
	wantSys := copilot.SystemPromptOracleException() + copilot.OracleTraceSynthesisAddendum()
	if prov.system != wantSys {
		t.Fatal("sistem prompt'u Oracle + sentez eki olmalı")
	}
	if !strings.HasPrefix(prov.user, "EXCEPTION KANITI "+synthFP) || !strings.Contains(prov.user, "TRACE KANITI ("+synthTID) ||
		!strings.Contains(prov.user, tracechain.Header) || !strings.Contains(prov.user, "OrderService.java:42") {
		t.Fatalf("kullanıcı mesajı grup girdisi + trace bloğu olmalı:\n%s", prov.user)
	}
	ans := evs[len(evs)-1].data.(map[string]any)
	text := ans["text"].(string)
	if !strings.Contains(text, "**Çağıran taraf (Coremetry trace)**") ||
		!strings.HasSuffix(text, "Kaynak: Exceptions grubu ORD-200 · ORDERS_GET_X · trace 4bf92f35 hata zinciri + stack") {
		t.Fatalf("cevap: %q", text)
	}
	lb, _ := json.Marshal(ans["links"])
	if !strings.Contains(string(lb), "/problems?exc=ora%3A00000000000000b2") || !strings.Contains(string(lb), "/trace?id="+synthTID) {
		t.Fatalf("linkler: %s", lb)
	}
	if ev, _ := ans["evidenceSpanIds"].([]string); len(ev) != 1 || ev[0] != "sp-1" {
		t.Fatalf("kanıt span'leri: %v", ans["evidenceSpanIds"])
	}
	if _, hit, _ := mc.Get(context.Background(), explainCacheKey(wantSys, prov.user, "synth:"+synthTID)); !hit {
		t.Fatal("sentez kendi anahtarına (synth:<trace>) yazılmalı")
	}
	if _, hit, _ := mc.Get(context.Background(), explainCacheKey(copilot.SystemPromptOracleException(), "EXCEPTION KANITI "+synthFP+": çağıran span orders-api SELECT … FROM orders (db.example.test)", "")); hit {
		t.Fatal("sentez ✨ düğmesinin anahtarını ezmemeli")
	}
}

func TestTraceSynthesisFallbacks(t *testing.T) {
	// Grup girdisi kurulamaz → trace'in klasik açıklaması (dürüst geri düşüş).
	prov := &opExplainProvider{deltas: []string{"**Bulgu** trace açıklaması."}}
	s := newOpExplainServer(t, prov)
	s.cache = newMemCache()
	opExplainFixture(t, errors.New("grup yok"))
	withSynthTraceSeam(t, nil)
	p := &chatPendingAsk{Kind: pendingKindTraceSynth, TraceID: synthTID, Fingerprint: synthFP, AskedAt: time.Now().UnixNano()}
	var evs []opExplainEvent
	emit := withStepIDs(func(k string, v any) { evs = append(evs, opExplainEvent{k, v}) })
	if h, ok := s.chatTraceSynthesisAnswer(synthCtx(&chatContextState{}), emit, p, time.UTC); !h || !ok {
		t.Fatalf("geri düşüş: handled=%v ok=%v", h, ok)
	}
	if prov.system != copilot.SystemPromptTrace() {
		t.Fatal("geri düşüş trace'in klasik prompt'u olmalı")
	}
	// Trace bulunamaz → LLM'siz dürüst cevap.
	prov2 := &opExplainProvider{}
	s2 := newOpExplainServer(t, prov2)
	opExplainFixture(t, nil)
	withSynthTraceSeam(t, errExplainTraceNotFound)
	evs = nil
	if h, ok := s2.chatTraceSynthesisAnswer(synthCtx(&chatContextState{}), emit, p, time.UTC); !h || ok {
		t.Fatalf("bulunamadı: handled=%v ok=%v", h, ok)
	}
	if text := evs[len(evs)-1].data.(map[string]any)["text"].(string); !strings.Contains(text, "bulunamadı") || prov2.calls != 0 {
		t.Fatalf("bulunamadı cevabı LLM'siz olmalı: %q (%d çağrı)", text, prov2.calls)
	}
}

// TestTraceSynthReplyFlow — GERÇEK copilotChat: bekleyen trace sorusuna
// "evet, incele" → operasyon kademesi, tek sentez cevabı; "hayır" → LLM'siz
// kapanış (clarify kademesi); ilgisiz soru → düşer.
func TestTraceSynthReplyFlow(t *testing.T) {
	seed := func(t *testing.T, mc *ctxMemCache) {
		b, _ := json.Marshal(ChatContext{Pending: &chatPendingAsk{Kind: pendingKindTraceSynth, TraceID: synthTID,
			Fingerprint: synthFP, Operation: "ORDERS_GET_X", AskedAt: time.Now().UnixNano()}})
		mc.mu.Lock()
		mc.m[chatContextKey(clarifyUID, clarifyConv)] = b
		mc.mu.Unlock()
	}
	post := func(t *testing.T, s *Server, reply string) []chatFrame {
		return postLoopChat(t, s, context.Background(), clarifyUID, map[string]any{"messages": []map[string]any{
			{"role": "user", "text": "ORDERS_GET_X operasyonu neden hata veriyor?"},
			{"role": "assistant", "text": "**Hata ne demek** …\n\n" + traceAskTextTR(synthTID)},
			{"role": "user", "text": reply}}, "context": map[string]any{"conversation": clarifyConv}})
	}
	t.Run("evet", func(t *testing.T) {
		rows := captureCosreRows(t)
		opExplainFixture(t, nil)
		withSynthTraceSeam(t, nil)
		llm := newLoopLLM(t, func(int, map[string]any) map[string]any {
			return answerMsg("**Çağıran taraf (Coremetry trace)** orders-api → db.example.test HTTP 500.")
		})
		s, mc := newLoopTestServer(t, llm, clarifyUID)
		seed(t, mc)
		fr := post(t, s, "evet, incele")
		r := lastRow(t, rows())
		if r.Tier != aisurface.TierOperation || r.Surface != "cosre-turn:operation/trace_synth" {
			t.Fatalf("kademe/yüzey: %q %q cevap=%q", r.Tier, r.Surface, lastAnswerText(fr))
		}
		if !strings.Contains(lastAnswerText(fr), "trace 4bf92f35 hata zinciri + stack") {
			t.Fatalf("sentez cevabı: %q", lastAnswerText(fr))
		}
		if m := replyTool(r); m["kind"] != pendingKindTraceSynth || m["reply"] != synthReplyYes {
			t.Fatalf("clarify_reply: %v", m)
		}
		calls := llm.calls()
		if len(calls) != 1 || llmSystem(calls[0]) != copilot.SystemPromptOracleException()+copilot.OracleTraceSynthesisAddendum() {
			t.Fatalf("tek sentez çağrısı (Oracle + ek) bekleniyordu: %d çağrı", len(calls))
		}
		if b := mc.m[chatContextKey(clarifyUID, clarifyConv)]; strings.Contains(string(b), "trace_synth") {
			t.Fatalf("bekleyen soru silinmeli: %s", b)
		}
	})
	t.Run("hayır", func(t *testing.T) {
		rows := captureCosreRows(t)
		llm := noLLM(t)
		s, mc := newLoopTestServer(t, llm, clarifyUID)
		seed(t, mc)
		fr := post(t, s, "gerek yok")
		r := lastRow(t, rows())
		if r.Tier != aisurface.TierClarify || r.Surface != "cosre-turn:clarify/trace_declined" || lastAnswerText(fr) != synthDeclinedTR {
			t.Fatalf("ret: %q %q %q", r.Tier, r.Surface, lastAnswerText(fr))
		}
		if len(llm.calls()) != 0 {
			t.Fatal("ret LLM'siz olmalı")
		}
	})
	t.Run("ilgisiz", func(t *testing.T) {
		rows := captureCosreRows(t)
		prevSeam := guidedDispatchSeam
		guidedDispatchSeam = func(guidedRoute) bool { return true }
		t.Cleanup(func() { guidedDispatchSeam = prevSeam })
		s, mc := newLoopTestServer(t, noLLM(t), clarifyUID)
		seed(t, mc)
		post(t, s, "checkout sağlığı nasıl?")
		r := lastRow(t, rows())
		if r.Tier != aisurface.TierGuided || replyTool(r)["reply"] != clarifyReplyDropped {
			t.Fatalf("ilgisiz cevap düşmeli: %q %v", r.Tier, replyTool(r))
		}
	})
}
