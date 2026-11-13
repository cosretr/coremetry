package api

// ai_evalset_agent_test.go — v0.10.1316 (değerlendirme seti gerçek ajan
// döngüsünde). Ajan kipi vakası canlı copilotChat handler'ından geçer: gerçek
// copilot.Service + sahte OpenAI-uyumlu model (loopLLM) + sahte araç koşucusu
// (fakeTools / chatLoopHandlerSeam — katalog, rol süzgeci, Executor gerçek).
// Koşu uçları registerAIEvalsetRunRoutes ile, depo bellek içi (memEvalStore).
//
// Pinlenenler: uçtan uca koşu + iz + ai_calls etiketi; eksik / yasak araç;
// tavan ve atlanan çağrı sayımı; iptal edilen vaka sayılmaz; vaka tavanı;
// araç seti kataloğa iner; dış MCP + sohbet bağlamı araçları kataloğa girmez;
// işaret yokken üretim yolu aynen; SAF puanlama / iz / özet / kıyas / tavan
// tabloları; güvenlik pinleri (salt-okur katalog, yüzey etiketi, oy okumaz).

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcpclient"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// ── kurgu ────────────────────────────────────────────────────────────────

// newAgentEvalHarness — loopLLM'li sunucu + evalset dikişleri (newEvalHarness
// ile aynı temizlik). Niyet kademesi kapalı: düz soru serbest döngüye ulaşır.
func newAgentEvalHarness(t *testing.T, script func(n int, body map[string]any) map[string]any, cases []evalCase) (*evalHarness, *loopLLM, *fakeTools) {
	t.Helper()
	llm := newLoopLLM(t, script)
	s, _ := newLoopTestServer(t, llm, "u1")
	s.copilot.SetIntentClassify(copilot.IntentOff)
	s.buildVersion = "v0.10.1316"
	h := &evalHarness{s: s, store: newMemEvalStore(), rec: &chanRecorder{ch: make(chan copilot.CallRecord, 64)}}
	s.copilot.SetRecorder(h.rec)
	h.mux = http.NewServeMux()
	s.registerAIEvalsetRunRoutes(h.mux)
	prevStore, prevLoader, prevDeadline, prevWaits := evalRunStoreFor, evalsetCasesLoader, evalRunDeadline, evalFinalSaveWaits
	evalRunStoreFor = func(*Server) evalRunStore { return h.store }
	evalsetCasesLoader = func() ([]evalCase, error) { return cases, nil }
	evalFinalSaveWaits = []time.Duration{0, time.Millisecond}
	evalJobs.mu.Lock()
	evalJobs.running, evalJobs.last = nil, nil
	evalJobs.mu.Unlock()
	t.Cleanup(func() {
		evalJobs.mu.Lock()
		if j := evalJobs.running; j != nil && j.cancel != nil {
			j.cancel()
		}
		evalJobs.mu.Unlock()
		waitEvalIdle(t)
		evalJobs.mu.Lock()
		evalJobs.last = nil
		evalJobs.mu.Unlock()
		evalRunStoreFor, evalsetCasesLoader, evalRunDeadline, evalFinalSaveWaits = prevStore, prevLoader, prevDeadline, prevWaits
	})
	ft := &fakeTools{}
	ft.install(t)
	return h, llm, ft
}

func intp(n int) *int { return &n }

// agentCase — ajan vakası (ChatLoop); serbest döngüye ulaşan servissiz soru.
func agentCase(id string, e evalExpect) evalCase {
	return evalCase{ID: id, Surface: evalAgentLoopSurface, Mode: evalModeAgent, Why: "w", User: "Dünle kıyasla?", Expect: e}
}

// runAgentEval — koşuyu başlatır, bitmesini bekler, detayı döner.
func runAgentEval(t *testing.T, h *evalHarness, body string) (evalRunSummary, []evalCaseResult) {
	t.Helper()
	w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", body, auth.RoleAdmin)
	if w.Code != http.StatusAccepted {
		t.Fatalf("başlat %d: %s", w.Code, w.Body.String())
	}
	id := decodeEval[struct{ Run evalRunSummary }](t, w).Run.ID
	waitEvalIdle(t)
	w = h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+id, "", auth.RoleAdmin)
	det := decodeEval[struct {
		Run   evalRunSummary
		Cases []evalCaseResult
	}](t, w)
	return det.Run, det.Cases
}

// drainRecords — 300 ms içinde gelen bütün ai_calls kayıtları.
func drainRecords(rec *chanRecorder) []copilot.CallRecord {
	var out []copilot.CallRecord
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case r := <-rec.ch:
			out = append(out, r)
		case <-deadline:
			return out
		}
	}
}

func drainAudit(s *Server) []chstore.AuditEntry {
	var out []chstore.AuditEntry
	for {
		select {
		case e := <-s.auditQ:
			out = append(out, e)
		default:
			return out
		}
	}
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// agentServerFn — HTTP koşusu olmadan tek vaka (runEvalsetCase + sunucu ajanı).
func agentServerFn(s *Server) evalAgentFn {
	return s.evalsetServerAgent(&evalJob{row: chstore.EvalRun{ID: "ev-t"}, userID: "u1", userEmail: "u1@example.test", userRole: auth.RoleAdmin})
}

func fullCtx() context.Context {
	return mcptools.WithToolset(context.Background(), modelcaps.ToolsetFull)
}

// ── uçtan uca ────────────────────────────────────────────────────────────

// TestEvalAgentEndToEnd — koşu: araç turu + cevap turu; vaka geçer, iz
// (kademe, tur, çağrı, adım) saklanır; ai_calls'ta TEK satır evalset-ChatLoop
// (exchange koşu/vaka); cosre_interactions satırı yok; araç başına audit yok,
// koşu audit'i ajan sayısını taşır; model gerçek kataloğu görür (sohbet
// bağlamı araçları hariç).
func TestEvalAgentEndToEnd(t *testing.T) {
	rows := captureCosreRows(t)
	c := agentCase("a-ok", evalExpect{ExpectedTools: []string{"compare_periods"}, MaxToolCalls: intp(6), ExpectTier: "loop",
		MustContain: []string{"1830"}, KnownEntities: []string{"checkout"}, MaxUnknownEntities: intp(0)})
	h, llm, ft := newAgentEvalHarness(t, func(n int, _ map[string]any) map[string]any {
		if n == 0 {
			return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
		}
		return answerMsg("checkout p95 1830 ms; dünden yüksek.")
	}, []evalCase{c})
	drainAudit(h.s)
	run, cases := runAgentEval(t, h, "")
	if run.Status != evalStatusDone || run.Pass != 1 || len(cases) != 1 {
		t.Fatalf("koşu: %+v cases=%+v", run, cases)
	}
	got := cases[0]
	if !got.OK || got.Mode != evalModeAgent || got.Agent == nil || got.Toolset != modelcaps.ToolsetFull {
		t.Fatalf("vaka: %+v", got)
	}
	tr := got.Agent
	if tr.Tier != "loop" || tr.Rounds != 2 || tr.Calls != 1 || tr.Executed != 1 || tr.Skipped != 0 || tr.Capped ||
		len(tr.Steps) != 1 || tr.Steps[0].Tool != "compare_periods" || !tr.Steps[0].OK || tr.InputTokens != 22 || tr.OutputTokens != 14 {
		t.Fatalf("iz: %+v", tr)
	}
	if len(ft.snapshot()) != 1 {
		t.Fatalf("araç koşucusu: %+v", ft.snapshot())
	}
	calls := llm.calls()
	if len(calls) != 2 {
		t.Fatalf("model %d kez çağrıldı", len(calls))
	}
	names := llmToolNames(calls[0])
	if !hasName(names, "compare_periods") || !hasName(names, "get_service_health") {
		t.Fatalf("model gerçek kataloğu görmedi: %v", names)
	}
	for n := range mcptools.ConversationStateTools {
		if hasName(names, n) {
			t.Errorf("sohbet bağlamı aracı %s ajan kataloğunda", n)
		}
	}
	recs := drainRecords(h.rec)
	if len(recs) != 1 || recs[0].Surface != "evalset-ChatLoop" || recs[0].ExchangeID != run.ID+"/a-ok" || recs[0].UserEmail != "admin@example.com" {
		t.Fatalf("ai_calls: %+v", recs)
	}
	if n := len(rows()); n != 0 {
		t.Fatalf("cosre_interactions satırı yazıldı: %d", n)
	}
	for _, e := range drainAudit(h.s) {
		if e.Action == "mcp.tool.call" {
			t.Errorf("araç başına audit yazıldı (D4): %+v", e)
		}
		if e.Action == "ai.evalset.run" && !strings.Contains(e.Details, "agent=1") {
			t.Errorf("koşu audit'i ajan sayısını taşımalı: %q", e.Details)
		}
	}
	var sum *evalSurfaceSummary
	for i := range run.BySurface {
		if run.BySurface[i].Surface == evalAgentLoopSurface {
			sum = &run.BySurface[i]
		}
	}
	if sum == nil || sum.AgentCases != 1 || sum.AvgToolCalls == nil || *sum.AvgToolCalls != 1 || sum.AvgTokens == nil || *sum.AvgTokens != 36 {
		t.Fatalf("yüzey özeti: %+v", run.BySurface)
	}
}

// TestEvalAgentForbiddenAndMissingTools — yasak araç denendi, beklenen araç
// koşmadı → iki ihlal; cevap metni yine puanlanır.
func TestEvalAgentForbiddenAndMissingTools(t *testing.T) {
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n == 0 {
			return toolCallMsg([2]string{"get_logs_for_trace", `{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}`})
		}
		return answerMsg("payment gateway timeout logda görülüyor.")
	})
	s, _ := newLoopTestServer(t, llm, "u1")
	s.copilot.SetIntentClassify(copilot.IntentOff)
	(&fakeTools{}).install(t)
	c := agentCase("a-bad", evalExpect{ExpectedTools: []string{"compare_periods"}, ForbiddenTools: []string{"get_logs_for_trace"}, MustContain: []string{"timeout"}})
	out := runEvalsetCase(fullCtx(), nil, agentServerFn(s), c)
	want := []string{"missing tool: compare_periods", "forbidden tool: get_logs_for_trace"}
	if !reflect.DeepEqual(out.Fails, want) {
		t.Fatalf("ihlaller: %v, want %v", out.Fails, want)
	}
	if out.Mode != evalModeAgent || out.Agent == nil || out.Agent.Executed != 1 {
		t.Fatalf("iz: %+v", out.Agent)
	}
}

// TestEvalAgentCapAndSkippedCounted — tek turda 7 çağrı: 6 hak, biri tekrar
// (yürümez), biri tavanı aşar (Executor'a hiç gitmez); tavan turu koşar.
// Rubriğin araç boyutu geçersiz.
func TestEvalAgentCapAndSkippedCounted(t *testing.T) {
	var calls [][2]string
	for i := 1; i <= 5; i++ {
		calls = append(calls, [2]string{"compare_periods", fmt.Sprintf(`{"service":"svc%d"}`, i)})
	}
	calls = append(calls, [2]string{"compare_periods", `{"service":"svc5"}`}, [2]string{"compare_periods", `{"service":"svc6"}`})
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n == 0 {
			return toolCallMsg(calls...)
		}
		return answerMsg("eldeki kıyasla özet.")
	})
	s, _ := newLoopTestServer(t, llm, "u1")
	s.copilot.SetIntentClassify(copilot.IntentOff)
	(&fakeTools{}).install(t)
	out := runEvalsetCase(fullCtx(), nil, agentServerFn(s), agentCase("a-cap", evalExpect{AnyOfTools: []string{"compare_periods"}, MaxToolCalls: intp(4)}))
	tr := out.Agent
	if tr == nil || tr.Calls != 7 || tr.Executed != 5 || tr.Skipped != 2 || !tr.Capped || tr.Rounds != 2 {
		t.Fatalf("iz: %+v", tr)
	}
	if !reflect.DeepEqual(out.Fails, []string{"tool calls 5 > 4"}) {
		t.Fatalf("ihlaller: %v", out.Fails)
	}
	valid := false
	for _, d := range out.Rubric.Dims {
		if d.Name == "tool_calls_valid" {
			valid = true
			if d.Score != 0 {
				t.Errorf("tekrar + tavan aşımı varken araç boyutu %v", d.Score)
			}
		}
	}
	if !valid {
		t.Fatalf("ajan kipinde tool_calls_valid boyutu yok: %+v", out.Rubric.Dims)
	}
}

// TestEvalAgentCancelMidCaseNotCounted — araç koşarken koşu iptal edilir:
// yarıda kalan ajan vakası sayılmaz (done 0), koşu cancelled.
func TestEvalAgentCancelMidCaseNotCounted(t *testing.T) {
	h, llm, ft := newAgentEvalHarness(t, func(n int, _ map[string]any) map[string]any {
		if n == 0 {
			return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`}, [2]string{"compare_periods", `{"service":"payments"}`})
		}
		return answerMsg("gelmemeli")
	}, []evalCase{agentCase("a-cancel", evalExpect{AnyOfTools: []string{"compare_periods"}})})
	ft.onCall = func(string) {
		if sum := evalJobs.current(); sum != nil {
			if cancel, _, ok := evalJobs.cancelIfRunning(sum.ID); ok {
				cancel()
			}
		}
	}
	run, cases := runAgentEval(t, h, "")
	if run.Status != evalStatusCancelled || run.Done != 0 || len(cases) != 0 {
		t.Fatalf("iptal edilen ajan vakası sayıldı: %+v cases=%+v", run, cases)
	}
	if n := len(llm.calls()); n != 1 {
		t.Fatalf("iptalden sonra model yeniden çağrıldı: %d", n)
	}
	if n := len(ft.snapshot()); n != 1 {
		t.Fatalf("iptalden sonra kalan çağrı yürüdü: %d", n)
	}
}

// TestEvalAgentCaseDeadline — vaka tavanı dolar (model yanıt vermez): koşu
// sürüyor, vaka SAYILIR ve kalır (cevap yok + hata metni).
func TestEvalAgentCaseDeadline(t *testing.T) {
	prev := evalAgentCaseCap
	evalAgentCaseCap = 80 * time.Millisecond
	t.Cleanup(func() { evalAgentCaseCap = prev })
	h, _, _ := newAgentEvalHarness(t, func(n int, _ map[string]any) map[string]any {
		time.Sleep(400 * time.Millisecond)
		return answerMsg("geç")
	}, []evalCase{agentCase("a-slow", evalExpect{AnyOfTools: []string{"compare_periods"}})})
	run, cases := runAgentEval(t, h, "")
	if run.Status != evalStatusDone || run.Done != 1 || run.Fail != 1 || len(cases) != 1 {
		t.Fatalf("koşu: %+v", run)
	}
	c := cases[0]
	if c.OK || c.Error == "" || len(c.Fails) == 0 || !strings.HasPrefix(c.Fails[0], "no answer (error") {
		t.Fatalf("tavanı dolan vaka: %+v", c)
	}
}

// TestEvalAgentToolsetOverrideReachesCatalogue — koşunun açık araç seti
// vakaya ve modele giden kataloğa iner. v0.10.2106 (kalite döngüsü D1):
// koşucu v2 yolunda → model V2ToolList(araç seti) kataloğunu görür (v1-yalnız
// search_traces / query_metric iki araç setinde de YOK, v2-yalnız
// list_my_services VAR). v1'de full ↔ simple farkı bu iki addı; v2
// kataloğunda iki araç setinin AD kümesi aynı (ölçüldü 2026-11-13).
func TestEvalAgentToolsetOverrideReachesCatalogue(t *testing.T) {
	for _, ts := range []string{modelcaps.ToolsetSimple, modelcaps.ToolsetFull} {
		t.Run(ts, func(t *testing.T) {
			h, llm, _ := newAgentEvalHarness(t, func(int, map[string]any) map[string]any { return answerMsg("tamam") },
				[]evalCase{agentCase("a-ts", evalExpect{AnyOfTools: []string{"compare_periods"}})})
			_, cases := runAgentEval(t, h, `{"toolset":"`+ts+`"}`)
			if len(cases) != 1 || cases[0].Toolset != ts {
				t.Fatalf("vaka araç seti: %+v", cases)
			}
			names := llmToolNames(llm.calls()[0])
			if hasName(names, "search_traces") || hasName(names, "query_metric") || !hasName(names, "list_my_services") {
				t.Fatalf("%s kataloğu v2 değil: %v", ts, names)
			}
		})
	}
}

// TestEvalAgentOmitsExternalAndContextTools — dış MCP yapılandırılmışken bile
// ajan kataloğunda dış araç ve sohbet bağlamı aracı yok.
func TestEvalAgentOmitsExternalAndContextTools(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("tamam") })
	s, _ := newLoopTestServer(t, llm, "u1")
	s.copilot.SetIntentClassify(copilot.IntentOff)
	srv, extCalls := newFakeTrackerMCP(t)
	mc := mcpclient.NewService()
	mc.Configure(mcpclient.Settings{Servers: []mcpclient.ServerConfig{{Name: "tracker", Transport: "http", URL: srv.URL, Enabled: true}}})
	t.Cleanup(mc.Close)
	s.mcpClient = mc
	live := false // kurgu: dış katalog CANLI — yokluk bağlantı hatası değil
	for _, pt := range mc.Registry().Tools(context.Background()) {
		live = live || pt.Name == "tracker__create_issue"
	}
	if !live || !mc.Configured() {
		t.Fatal("sahte dış MCP kataloğu tracker__create_issue ilan etmiyor — kurgu bozuk")
	}
	(&fakeTools{}).install(t)
	runEvalsetCase(fullCtx(), nil, agentServerFn(s), agentCase("a-ext", evalExpect{AnyOfTools: []string{"compare_periods"}}))
	calls := llm.calls()
	if len(calls) == 0 {
		t.Fatal("model çağrılmadı")
	}
	names := llmToolNames(calls[0])
	if !hasName(names, "compare_periods") {
		t.Fatalf("yerli katalog yok: %v", names)
	}
	for _, n := range names {
		if strings.Contains(n, "__") || mcptools.ConversationStateTools[n] {
			t.Errorf("ajan kataloğunda %s", n)
		}
	}
	if extCalls() != 0 {
		t.Errorf("dış araç çağrıldı: %d", extCalls())
	}
}

// TestChatEvalModeNilIsByteIdentical — işaret yokken (ya da nil işaretle)
// üretim yolu aynen: aynı katalog, aynı sistem mesajı; etkileşim satırı,
// cosre_interactions ve araç audit'i yazılır.
func TestChatEvalModeNilIsByteIdentical(t *testing.T) {
	if chatEvalModeFrom(context.Background()) != nil {
		t.Fatal("işaretsiz ctx'te işaret var")
	}
	base := context.Background()
	if withChatEvalMode(base, nil) != base {
		t.Fatal("nil işaret ctx'i değiştirmemeli")
	}
	run := func(ctx context.Context) (sys string, names []string, recs []copilot.CallRecord, cosre int, toolAudit int) {
		llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
			if n == 0 {
				return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
			}
			return answerMsg("checkout p95 1830 ms.")
		})
		s, _ := newLoopTestServer(t, llm, "u1")
		s.copilot.SetIntentClassify(copilot.IntentOff)
		rec := &chanRecorder{ch: make(chan copilot.CallRecord, 16)}
		s.copilot.SetRecorder(rec)
		rows := captureCosreRows(t)
		(&fakeTools{}).install(t)
		postLoopChat(t, s, ctx, "u1", map[string]any{"messages": []map[string]any{{"role": "user", "text": "Dünle kıyasla?"}}})
		calls := llm.calls()
		for _, e := range drainAudit(s) {
			if e.Action == "mcp.tool.call" {
				toolAudit++
			}
		}
		return llmSystem(calls[0]), llmToolNames(calls[0]), drainRecords(rec), len(rows()), toolAudit
	}
	sysA, namesA, recsA, cosreA, auditA := run(context.Background())
	sysB, namesB, recsB, cosreB, auditB := run(withChatEvalMode(context.Background(), nil))
	if sysA != sysB || !reflect.DeepEqual(namesA, namesB) {
		t.Fatal("nil işaretle katalog / sistem mesajı değişti")
	}
	for _, n := range []string{"set_context", "get_context", "clear_context"} {
		if !hasName(namesA, n) {
			t.Errorf("üretim kataloğunda %s yok", n)
		}
	}
	// v0.10.1387 — kayıtlar fire-and-forget RecordCall goroutine'lerinden
	// gelir; varış sırası ([chat cosre-turn] / [cosre-turn chat]) garanti
	// değil. Test SIRAYI değil yüzey KÜMESİNİ karşılaştırır — release
	// kapısındaki ara sıra kırılmanın (v0.10.1385/1386 yayınlanamadı) nedeni.
	surfaces := func(rs []copilot.CallRecord) []string {
		var out []string
		for _, r := range rs {
			out = append(out, strings.SplitN(r.Surface, ":", 2)[0])
		}
		sort.Strings(out)
		return out
	}
	if !reflect.DeepEqual(surfaces(recsA), surfaces(recsB)) || len(recsA) != 2 || cosreA != 1 || cosreB != 1 || auditA != 1 || auditB != 1 {
		t.Fatalf("üretim yan etkileri: %v / %v cosre=%d/%d audit=%d/%d", surfaces(recsA), surfaces(recsB), cosreA, cosreB, auditA, auditB)
	}
	for _, r := range recsA {
		if strings.HasPrefix(r.Surface, evalsetSurfacePrefix) {
			t.Errorf("üretim satırı evalset önekli: %q", r.Surface)
		}
	}
}

// TestEvalAgentSurfaceTag — döngü öncesi kademe (niyet sınıflandırıcısı)
// dahil turun HER ai_calls satırı evalset- önekli (üretim kaynak koşulu
// startsWith(surface,'evalset-') ile dışlar), exchange kimliği koşu/vaka
// kökünde; cosre_interactions satırı yok.
func TestEvalAgentSurfaceTag(t *testing.T) {
	llm := newLoopLLM(t, func(n int, body map[string]any) map[string]any {
		if _, ok := body["response_format"]; ok {
			return answerMsg(`{"intent":"none","service":"","env":"","rangeS":0}`)
		}
		if msgs, _ := body["messages"].([]any); len(msgs) <= 2 {
			return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
		}
		return answerMsg("checkout p95 1830 ms.")
	})
	s, _ := newLoopTestServer(t, llm, "u1")
	s.copilot.SetIntentClassify(copilot.IntentOn)
	rec := &chanRecorder{ch: make(chan copilot.CallRecord, 16)}
	s.copilot.SetRecorder(rec)
	rows := captureCosreRows(t)
	(&fakeTools{}).install(t)
	// v0.10.1321 — ajan vakasında niyet kademesi yalnız expectTier "intent"
	// sabitlenince açık (evalAgentSkipIntent); etiket pini sınıflandırıcı
	// satırını görmek için kademeyi sabitler (none → IntentOn → döngü).
	out := runEvalsetCase(fullCtx(), nil, agentServerFn(s), agentCase("a-tag", evalExpect{AnyOfTools: []string{"compare_periods"}, ExpectTier: "intent"}))
	if out.Agent == nil || out.Agent.Tier != "loop" {
		t.Fatalf("niyet → döngü kurgusu: %+v fails=%v", out.Agent, out.Fails)
	}
	recs := drainRecords(rec)
	if len(recs) < 2 {
		t.Fatalf("ai_calls: %+v", recs)
	}
	sawIntent, sawLoop := false, false
	for _, r := range recs {
		if !strings.HasPrefix(r.Surface, chstore.AICallEvalsetSurfacePrefix) {
			t.Errorf("üretim etiketiyle satır: %q", r.Surface)
		}
		if !strings.HasPrefix(r.ExchangeID, "ev-t/a-tag") {
			t.Errorf("exchange kimliği koşu/vaka kökünde değil: %q", r.ExchangeID)
		}
		sawIntent = sawIntent || r.Surface == "evalset-chat-intent"
		sawLoop = sawLoop || (r.Surface == "evalset-ChatLoop" && r.ExchangeID == "ev-t/a-tag")
	}
	if !sawIntent || !sawLoop {
		t.Fatalf("niyet + döngü satırları: %+v", recs)
	}
	if n := len(rows()); n != 0 {
		t.Fatalf("cosre_interactions: %d", n)
	}
}

// TestRunEvalsetCaseAgentSkippedOnCLI — agent nil (CLI) → atlanır, model
// çağrılmaz.
func TestRunEvalsetCaseAgentSkippedOnCLI(t *testing.T) {
	f := &fakeCall{answer: "x"}
	o := runEvalsetCase(context.Background(), f.fn, nil, agentCase("a-cli", evalExpect{AnyOfTools: []string{"compare_periods"}}))
	if !o.Skipped || o.SkipReason != "agent kipi yalnız sunucu koşusunda" || o.Mode != evalModeAgent || f.n != 0 {
		t.Fatalf("CLI ajan vakası: %+v (çağrı %d)", o, f.n)
	}
	if res := evalCaseResultFrom(agentCase("a-cli", evalExpect{}), o, "", ""); !res.Skipped || res.Mode != evalModeAgent || res.Agent != nil {
		t.Fatalf("panel satırı: %+v", res)
	}
}

// ── SAF tablolar ─────────────────────────────────────────────────────────

func TestScoreEvalAgentCase(t *testing.T) {
	okTool := func(name, out string) chstore.CosreTool { return chstore.CosreTool{Name: name, OK: true, Output: out} }
	skipped := chstore.CosreTool{Name: "compare_periods", Error: "skipped"}
	failed := chstore.CosreTool{Name: "get_service_health", Error: "timeout"}
	intent := chstore.CosreTool{Name: "intent_classify", Origin: "intent", OK: true}
	guided := chstore.CosreTool{Name: "list_problems", Origin: "guided", OK: true, Output: `{"problems":[]}`}
	for _, tc := range []struct {
		name    string
		e       evalExpect
		o       evalAgentOutcome
		text    string
		want    []string
		unknown int
	}{
		{"geçer", evalExpect{ExpectedTools: []string{"compare_periods"}, MustContain: []string{"p95"}},
			evalAgentOutcome{Tier: "loop", Status: "ok", Answer: "p95 arttı", Tools: []chstore.CosreTool{okTool("compare_periods", "")}}, "", nil, 0},
		{"cevap yok", evalExpect{MustContain: []string{"p95"}},
			evalAgentOutcome{Tier: "loop", Status: "error", ErrMsg: "tavan"}, "", []string{"no answer (error: tavan)"}, 0},
		{"atlanan ve hatalı çağrı başarı sayılmaz", evalExpect{ExpectedTools: []string{"compare_periods", "get_service_health"}},
			evalAgentOutcome{Status: "ok", Answer: "x", Tools: []chstore.CosreTool{skipped, failed}}, "", []string{"missing tool: compare_periods", "missing tool: get_service_health"}, 0},
		{"anyOf", evalExpect{AnyOfTools: []string{"query_logs", "search_logs"}},
			evalAgentOutcome{Status: "ok", Answer: "x", Tools: []chstore.CosreTool{okTool("compare_periods", "")}}, "", []string{"missing any of tools: [query_logs search_logs]"}, 0},
		{"yasak: denemek yeter (atlanan da)", evalExpect{ForbiddenTools: []string{"compare_periods"}},
			evalAgentOutcome{Status: "ok", Answer: "x", Tools: []chstore.CosreTool{skipped}}, "", []string{"forbidden tool: compare_periods"}, 0},
		{"çağrı tavanı yürütüleni sayar, niyet çipi araç değil", evalExpect{MaxToolCalls: intp(1)},
			evalAgentOutcome{Status: "ok", Answer: "x", Tools: []chstore.CosreTool{intent, okTool("a", ""), skipped, failed}}, "", []string{"tool calls 2 > 1"}, 0},
		{"guided ön-yüklemesi araç yolunu karşılar", evalExpect{ExpectedTools: []string{"list_problems"}},
			evalAgentOutcome{Tier: "guided", Status: "ok", Answer: "açık problem yok", Tools: []chstore.CosreTool{guided}}, "", nil, 0},
		{"kademe", evalExpect{ExpectTier: "loop"},
			evalAgentOutcome{Tier: "intent", Status: "ok", Answer: "x"}, "", []string{`tier "intent", want "loop"`}, 0},
		{"metin kapıları tek çağrılı yolla aynı", evalExpect{MustContain: []string{"P95"}, MustNotContain: []string{"kubectl"}, KeyFacts: []string{"1830"}},
			evalAgentOutcome{Status: "ok", Answer: "p95 yüksek; kubectl ile bak"}, "", []string{"forbidden: kubectl", "missing fact: 1830"}, 0},
		{"uydurma: araç metnindeki ad bilinir", evalExpect{MaxUnknownEntities: intp(0)},
			evalAgentOutcome{Status: "ok", Answer: "fraud-scorer yavaş"}, `{"service":"fraud-scorer"}`, nil, 0},
		{"uydurma: hiçbir yerde olmayan ad", evalExpect{MaxUnknownEntities: intp(0)},
			evalAgentOutcome{Status: "ok", Answer: "ledger-writer yavaş"}, "", []string{"unknown entities 1 > 0: [ledger-writer]"}, 1}, // v0.10.1476 — ad mesajda
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := agentCase("x", tc.e)
			fails, unknown := scoreEvalAgentCase(c, tc.o, evalAgentStats{}, tc.text)
			if !reflect.DeepEqual(fails, tc.want) || unknown != tc.unknown {
				t.Fatalf("fails=%v unknown=%d, want %v / %d", fails, unknown, tc.want, tc.unknown)
			}
		})
	}
}

func TestEvalAgentToolCallsValid(t *testing.T) {
	if evalAgentToolCallsValid(evalAgentStats{}) != nil {
		t.Error("döngü koşmadıysa n/a")
	}
	for _, tc := range []struct {
		st   evalAgentStats
		want bool
	}{
		{evalAgentStats{Rounds: 2, Calls: 2, ExecCalls: 2}, true},
		{evalAgentStats{Rounds: 2, Calls: 2, ExecCalls: 2, Unknown: 1}, false},
		{evalAgentStats{Rounds: 2, Calls: 2, ExecCalls: 2, Repeated: 1}, false},
		{evalAgentStats{Rounds: 2, Calls: 3, ExecCalls: 2}, false},
	} {
		if got := evalAgentToolCallsValid(tc.st); got == nil || *got != tc.want {
			t.Errorf("%+v → %v", tc.st, got)
		}
	}
}

// TestEvalAgentTrailBounded — 20 adım, uzun Türkçe önizleme/argüman: iz
// ≤ 8 KiB JSON, ≤ 12 adım, rune-güvenli; sayaçlar kırpmadan bağımsız.
func TestEvalAgentTrailBounded(t *testing.T) {
	var tools []chstore.CosreTool
	long := strings.Repeat("ğüşıöç", 400)
	for i := 0; i < 20; i++ {
		tools = append(tools, chstore.CosreTool{Name: fmt.Sprintf("t%d", i), OK: true, Args: long, Output: long, Bytes: 99})
	}
	tools = append(tools, chstore.CosreTool{Name: "skipped", Error: "skipped"})
	tr := evalAgentTrailFrom(agentCase("x", evalExpect{MaxTokens: 10, MaxLatencyMs: 5}),
		evalAgentOutcome{Tier: "loop", Tools: tools, InputTokens: 9, OutputTokens: 9}, evalAgentStats{Rounds: 5, Capped: true}, 10)
	b, _ := json.Marshal(tr)
	if len(b) > evalAgentTrailMax {
		t.Fatalf("iz %d bayt > %d", len(b), evalAgentTrailMax)
	}
	if !strings.Contains(string(b), "ğ") || strings.ContainsRune(string(b), '�') {
		t.Fatal("kırpma rune sınırında değil")
	}
	if len(tr.Steps) > evalAgentStepsMax || !tr.StepsTruncated || tr.Calls != 21 || tr.Executed != 20 || tr.Skipped != 1 {
		t.Fatalf("iz: steps=%d trunc=%v calls=%d exec=%d skip=%d", len(tr.Steps), tr.StepsTruncated, tr.Calls, tr.Executed, tr.Skipped)
	}
	for _, s := range tr.Steps {
		if len(s.Args) > evalAgentArgsMax || len(s.Preview) > evalAgentPreviewMax {
			t.Fatalf("adım tavanı: %d / %d", len(s.Args), len(s.Preview))
		}
	}
	if !reflect.DeepEqual(tr.Overruns, []string{"token 18 > 10", "süre 10 ms > 5 ms"}) {
		t.Fatalf("metrik aşımları: %v", tr.Overruns)
	}
	// Küçük iz olduğu gibi kalır.
	small := evalBoundTrail(evalAgentTrail{Steps: []evalAgentStep{{I: 1, Tool: "a", Preview: "kısa"}}}, evalAgentTrailMax)
	if small.Steps[0].Preview != "kısa" || small.StepsTruncated {
		t.Fatalf("küçük iz değişti: %+v", small)
	}
}

// TestEvalSurfaceSummariesAgentNulls — ajan vakası olmayan yüzeyde
// ortalamalar null; ajan yüzeyinde atlanan vaka ortalamaya girmez; vaka
// sayısı plandan.
func TestEvalSurfaceSummariesAgentNulls(t *testing.T) {
	plan := []evalCase{
		{ID: "p", Surface: "Problem"},
		agentCase("a1", evalExpect{}), agentCase("a2", evalExpect{}), agentCase("a3", evalExpect{}),
	}
	results := []evalCaseResult{
		{ID: "p", Surface: "Problem", OK: true},
		{ID: "a1", Surface: evalAgentLoopSurface, OK: true, Mode: evalModeAgent, Agent: &evalAgentTrail{Calls: 2, InputTokens: 100, OutputTokens: 20}},
		{ID: "a2", Surface: evalAgentLoopSurface, Mode: evalModeAgent, Agent: &evalAgentTrail{Calls: 4, InputTokens: 300, OutputTokens: 60}},
		{ID: "a3", Surface: evalAgentLoopSurface, Skipped: true, Mode: evalModeAgent},
	}
	out := evalSurfaceSummaries(plan, results)
	by := map[string]evalSurfaceSummary{}
	for _, s := range out {
		by[s.Surface] = s
	}
	if p := by["Problem"]; p.AgentCases != 0 || p.AvgToolCalls != nil || p.AvgTokens != nil {
		t.Fatalf("ajansız yüzey: %+v", p)
	}
	a := by[evalAgentLoopSurface]
	if a.AgentCases != 3 || a.AvgToolCalls == nil || *a.AvgToolCalls != 3 || a.AvgTokens == nil || *a.AvgTokens != 240 {
		t.Fatalf("ajan yüzeyi: %+v", a)
	}
	b, _ := json.Marshal(by["Problem"])
	if !strings.Contains(string(b), `"avgToolCalls":null`) || !strings.Contains(string(b), `"avgTokens":null`) {
		t.Fatalf("null sözleşmesi: %s", b)
	}
	// Henüz biten ajan vakası yokken de null (ölçülmedi ≠ 0).
	if s := evalSurfaceSummaries(plan, nil); s[0].AvgToolCalls != nil {
		t.Fatalf("sonuçsuz özet: %+v", s)
	}
}

// TestEvalCompareAgentMeans — kıyas tarafları ajan ortalamalarını taşır; not
// araç çağrısı ortalamasının değişimini söyler; ajansız tarafta null ve not yok.
func TestEvalCompareAgentMeans(t *testing.T) {
	mk := func(id string, calls int) chstore.EvalRun {
		cases := []evalCaseResult{{ID: "a1", Surface: evalAgentLoopSurface, OK: true, Mode: evalModeAgent,
			Agent: &evalAgentTrail{Calls: calls, InputTokens: 100, OutputTokens: 0}}}
		b, _ := json.Marshal(cases)
		return chstore.EvalRun{ID: id, Status: evalStatusDone, Model: "m", PromptVersion: "p1", Total: 1, Pass: 1, Cases: string(b), StartedAt: time.Unix(1, 0)}
	}
	cmp := evalCompareRuns(mk("b", 2), mk("h", 5))
	if cmp.Base.ToolCallsMean == nil || *cmp.Base.ToolCallsMean != 2 || cmp.Head.ToolCallsMean == nil || *cmp.Head.ToolCallsMean != 5 ||
		cmp.Head.TokensMean == nil || *cmp.Head.TokensMean != 100 {
		t.Fatalf("taraflar: %+v / %+v", cmp.Base, cmp.Head)
	}
	if !strings.Contains(cmp.Note, "Ajan vakalarında araç çağrısı ortalaması 2.0 → 5.0.") {
		t.Fatalf("not: %q", cmp.Note)
	}
	same := evalCompareRuns(mk("b", 3), mk("h", 3))
	if strings.Contains(same.Note, "Ajan") {
		t.Fatalf("aynı ortalamada not: %q", same.Note)
	}
	plain := chstore.EvalRun{ID: "x", Status: evalStatusDone, Model: "m", Cases: `[]`}
	if c := evalCompareRuns(plain, mk("h", 5)); c.Base.ToolCallsMean != nil || strings.Contains(c.Note, "Ajan") {
		t.Fatalf("ajansız taban: %+v %q", c.Base, c.Note)
	}
	if got := evalCompareAgentNote("Önceki not.", ptrF(1), ptrF(2)); got != "Önceki not. Ajan vakalarında araç çağrısı ortalaması 1.0 → 2.0." {
		t.Fatalf("not birleşimi: %q", got)
	}
}

func ptrF(f float64) *float64 { return &f }

func TestEvalRunDeadlineFor(t *testing.T) {
	prev := evalRunDeadline
	t.Cleanup(func() { evalRunDeadline = prev })
	evalRunDeadline = 60 * time.Minute
	plain := []evalCase{{ID: "p", Surface: "Problem"}}
	agents := func(n int) []evalCase {
		out := append([]evalCase{}, plain...)
		for i := 0; i < n; i++ {
			out = append(out, agentCase(fmt.Sprintf("a%d", i), evalExpect{}))
		}
		return out
	}
	for _, tc := range []struct {
		plan []evalCase
		want time.Duration
	}{
		{plain, 60 * time.Minute},
		{agents(1), 70 * time.Minute},
		{agents(6), 120 * time.Minute},
		{agents(30), 180 * time.Minute},
	} {
		if got := evalRunDeadlineFor(tc.plan); got != tc.want {
			t.Errorf("%d vaka: %v, want %v", len(tc.plan), got, tc.want)
		}
	}
	evalRunDeadline = 5 * time.Millisecond // test dikişi: taban küçükken de ajan payı eklenir, tavan tabanı kesmez
	if got := evalRunDeadlineFor(plain); got != 5*time.Millisecond {
		t.Errorf("ajansız test dikişi: %v", got)
	}
	evalRunDeadline = 200 * time.Minute
	if got := evalRunDeadlineFor(agents(2)); got != 200*time.Minute {
		t.Errorf("taban tavandan büyükse taban: %v", got)
	}
	if evalAgentCaseMax(0) != min(chatDeadlineMin, evalAgentCaseCap) || evalAgentCaseMax(time.Hour) != evalAgentCaseCap {
		t.Errorf("vaka tavanı: %v / %v", evalAgentCaseMax(0), evalAgentCaseMax(time.Hour))
	}
}

func TestEvalAgentToolsDropsContextTools(t *testing.T) {
	in := []mcp.Tool{{Name: "get_service_health"}, {Name: "set_context"}, {Name: "get_context"}, {Name: "clear_context"}, {Name: "compare_periods"}}
	got := evalAgentTools(in)
	if len(got) != 2 || got[0].Name != "get_service_health" || got[1].Name != "compare_periods" {
		t.Fatalf("ajan kataloğu: %+v", got)
	}
}

// ── güvenlik pinleri ─────────────────────────────────────────────────────

// evalAgentMutatingCall — internal/sre/guardrails_test.go deseni (import
// edilmez, kopya): yazan/uygulayan çağrı adı önekleri.
var evalAgentMutatingCall = regexp.MustCompile(`^(Upsert|Insert|Delete|Mark|Update|Set|Append|Resolve|Ack|Execute|Put|Save)`)

// evalAgentMutatingAllow — "dosya:çağrı" → gerekçe. Bugün boş: koşunun tek
// kalıcı yazımı ai_evalset_runs.go'daki SaveEvalRun (taranan dosyalarda değil).
var evalAgentMutatingAllow = map[string]string{}

// TestEvalAgentCatalogReadOnly — ajan kataloğu handler'ın eval kipinde
// kurduğu gibi (rol süzgeci admin, sohbet bağlamı araçları düşer): her araç
// viewer tabanında, bağlam aracı yok; ajan dosyaları hiçbir yazan çağrı
// yapmaz.
func TestEvalAgentCatalogReadOnly(t *testing.T) {
	for _, ts := range []string{modelcaps.ToolsetFull, modelcaps.ToolsetSimple} {
		tools := evalAgentTools(toolsForRole(mcptools.ChatToolListFor(mcptools.Deps{}, ts), auth.RoleAdmin))
		if len(tools) < 30 {
			t.Fatalf("%s: yalnız %d araç", ts, len(tools))
		}
		for _, tl := range tools {
			if tl.MinRole != "" {
				t.Errorf("%s/%s: MinRole %q — ajan kataloğu salt-okur viewer tabanı", ts, tl.Name, tl.MinRole)
			}
			if mcptools.ConversationStateTools[tl.Name] {
				t.Errorf("%s: sohbet bağlamı aracı %s ajan kataloğunda", ts, tl.Name)
			}
		}
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, f := range []string{"ai_evalset_agent.go", "chat_eval_mode.go"} {
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if name == "" || !evalAgentMutatingCall.MatchString(name) {
				return true
			}
			key := f + ":" + types.ExprString(call.Fun)
			if _, ok := evalAgentMutatingAllow[key]; ok {
				seen[key] = true
				return true
			}
			t.Errorf("%s: %s(…) — ajan değerlendirmesi hiçbir şey yazmaz", fset.Position(call.Pos()), name)
			return true
		})
	}
	for k := range evalAgentMutatingAllow {
		if !seen[k] {
			t.Errorf("izin listesi bayat: %s", k)
		}
	}
}

// TestEvalNeverReadsVotes — oylar değerlendirme girdisi değildir (operatör
// 2026-10-17 "oylara çok güvenmeyelim"): ai_evalset_*.go hiçbir oy / kalite
// okuyucusu anmaz (cosrelearn TestVotesNeverReachRuntimeDecisions iğneleri + 👎 listesi)
// ve vaka / beklenti / sonuç / iz şekillerinde oy alanı yoktur.
func TestEvalNeverReadsVotes(t *testing.T) {
	needles := []string{"AIFeedbackVerdictsSince", "ListCosreAnswerQuality", "ListCosreToolQuality",
		"CosreAnswerQuality(", "CosreToolQuality(", "ListSREReviewDownvotes", "NegativeFeedback", "Downvote"}
	// ai_evalset.go (👎 dışa aktarımı — insan onaylı fikstür ADAYI, inceleme
	// yüzeyi) bilinçli olarak glob dışında: koşucu, puanlayıcı ve ajan
	// ai_evalset_*.go'da.
	files, _ := filepath.Glob("ai_evalset_*.go")
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, n := range needles {
			if strings.Contains(string(b), n) {
				t.Errorf("%s: %q — değerlendirme oy / kalite puanı okumaz", f, n)
			}
		}
	}
	if scanned < 3 {
		t.Fatalf("yalnız %d dosya tarandı", scanned)
	}
	banned := []string{"feedback", "vote", "thumb"}
	seen := map[reflect.Type]bool{}
	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			low := strings.ToLower(f.Name + " " + f.Tag.Get("json"))
			for _, b := range banned {
				if strings.Contains(low, b) {
					t.Errorf("%s.%s — değerlendirme girdisi oy taşımaz", path, f.Name)
				}
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	for _, v := range []any{evalCase{}, evalExpect{}, evalAgentOutcome{}, evalAgentStats{}, evalAgentTrail{}, evalCaseResult{}} {
		walk(reflect.TypeOf(v), reflect.TypeOf(v).Name())
	}
}

// _ — derleme pini: OnTool kancası Executor sonucunu aynen alır.
var _ = func(c *evalAgentCollector) func(string, json.RawMessage, agenttools.Outcome) { return c.onTool }
