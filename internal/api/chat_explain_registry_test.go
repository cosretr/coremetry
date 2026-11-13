package api

// chat_explain_registry_test.go — v0.10.1405 (Tek CoSRE adım 2). Operatör:
// "Trace ve exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama
// sanki ayrı drawer hissi, CoSRE bütünselliğine uymuyor."
//
// Pinler:
//   - kayıttaki her tür ya kendi turuna (trace/exception) ya düğmenin
//     kurucusuna bağlı; yüzeyi aisurface'te kayıtlı, Eval ailesi HTTP
//     yüzeyininkiyle aynı;
//   - HER tür için ✨ HTTP düğmesi ile sohbet turu AYNI önbellek satırını
//     paylaşır: düğme üretir → sohbet turu cached:true, sıfır LLM çağrısı,
//     saklanan exchangeId (ve problem için ters yön);
//   - kurucu çıkarımı handler'ların davranışını değiştirmedi (bulunamadı
//     gövdeleri, runbook similarCount, log matched);
//   - niyet normalizasyonu: span / service-health / log kodek ister, tür ve
//     kimlik eşleşmeli, kanonik biçime yazılır; arşiv künyesi subject /
//     renderer / note'u süzer.
//
// Sentetik adlar: orders-api, payments, checkout, db.example.test.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// ── sahte okuma yüzeyi ────────────────────────────────────────────────────

const (
	xrProblemID  = "0000000000000p01"
	xrIncidentID = "00000000000000i1"
	xrAnomalyID  = "00000000000000a1"
)

var xrT0 = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

type xrStore struct{}

func (xrStore) problem() chstore.Problem {
	return chstore.Problem{ID: xrProblemID, RuleID: "r-latency", RuleName: "orders-api p99 latency", Service: "orders-api",
		Metric: "p99_latency", Value: 2400, Threshold: 800, Severity: "critical", Status: "open",
		Description: "p99 above threshold", StartedAt: xrT0.UnixNano()}
}
func (x xrStore) GetProblem(_ context.Context, id string) (*chstore.Problem, error) {
	if id != xrProblemID {
		return nil, nil
	}
	p := x.problem()
	return &p, nil
}
func (xrStore) EnrichProblemsWithDeploys(_ context.Context, ps []chstore.Problem, _ time.Duration) []chstore.Problem {
	out := append([]chstore.Problem(nil), ps...)
	for i := range out {
		out[i].RecentDeploy = &chstore.RecentDeploy{Version: "1.4.2", AgeSeconds: 240}
	}
	return out
}
func (xrStore) GetHypothesis(context.Context, string, string) (*chstore.RootCauseHypothesis, error) {
	return nil, nil
}

// v0.10.1410 — komşular topoloji özetinden (ServiceNeighbors silindi).
func (xrStore) ReadServiceTopologyAggForFocus(context.Context, time.Time, time.Time, string, int, int) ([]chstore.ServiceTopologyEdge, error) {
	return []chstore.ServiceTopologyEdge{
		{ParentService: "checkout", ChildNode: "orders-api", NodeKind: "service", Protocol: "http", Calls: 40},
		{ParentService: "orders-api", ChildNode: "db:postgresql@db.example.test", NodeKind: "db", Protocol: "db", Calls: 90},
	}, nil
}
func (xrStore) GetTraces(context.Context, chstore.TraceFilter) ([]chstore.TraceRow, uint64, bool, error) {
	return []chstore.TraceRow{{RootName: "GET /orders", DurationMs: 2400, SpanCount: 12}}, 1, false, nil
}
func (xrStore) GetIncident(_ context.Context, id string) (*chstore.Incident, error) {
	if id != xrIncidentID {
		return nil, nil
	}
	return &chstore.Incident{ID: xrIncidentID, Title: "orders-api yavaşlıyor", Service: "orders-api", Severity: "critical", Status: "open", Summary: "p99 3x"}, nil
}
func (xrStore) IncidentProblems(context.Context, string) ([]string, error) {
	return []string{xrProblemID}, nil
}
func (x xrStore) ListProblems(context.Context, chstore.ProblemFilter) ([]chstore.Problem, error) {
	return []chstore.Problem{x.problem()}, nil
}
func (xrStore) GetAnomalyEvent(_ context.Context, id string, _ time.Duration) (*chstore.AnomalyEvent, error) {
	if id != xrAnomalyID {
		return nil, nil
	}
	return &chstore.AnomalyEvent{ID: xrAnomalyID, Kind: "log_pattern", Pattern: "connection reset", Service: "orders-api",
		PeakRatio: 6.5, CurrentRatio: 4.1, CurrentCount: 420, Sample: "connection reset by peer (db.example.test:5432)",
		StartedAt: xrT0.UnixNano(), LastSeen: xrT0.Add(20 * time.Minute).UnixNano()}, nil
}
func (xrStore) GetServiceDeploys(context.Context, string, time.Time, time.Time) ([]chstore.Deploy, error) {
	return nil, nil
}
func (xrStore) QuerySpanMetricMulti(context.Context, chstore.SpanMetricBatchFilter) (map[string][]chstore.SpanMetricSeries, int, error) {
	s := []chstore.SpanMetricSeries{{GroupKey: []string{"GET /orders"}, Points: []chstore.SpanMetricPoint{{Value: 1}, {Value: 3}}}}
	return map[string][]chstore.SpanMetricSeries{"rate": s, "error_rate": s, "p99": s}, 0, nil
}
func (xrStore) ListAnomalyFeed(context.Context, chstore.ListAnomalyEventsFilter) ([]chstore.AnomalyEvent, error) {
	return []chstore.AnomalyEvent{{ID: xrAnomalyID, Kind: "log_pattern", Pattern: "connection reset", Service: "orders-api",
		PeakRatio: 6.5, Status: "active", StartedAt: xrT0.UnixNano()}}, nil
}
func (xrStore) GetOperationSummaryCompared(context.Context, string, time.Duration, time.Time, time.Time, bool, string) ([]chstore.OperationSummary, error) {
	return []chstore.OperationSummary{{Name: "GET /orders", SpanCount: 400, P95Ms: 2400, ErrorRate: 4,
		HasPrior: true, PriorP95Ms: 600, PriorErrorRate: 0.5}}, nil
}
func (xrStore) GetServiceRollouts(context.Context, string, time.Time, time.Time) (*chstore.RolloutsResult, error) {
	return &chstore.RolloutsResult{Rollouts: []chstore.Rollout{{TimeUnixNs: xrT0.Add(-20 * time.Minute).UnixNano(), Kind: "deploy",
		VersionBefore: "1.4.1", VersionAfter: "1.4.2", PodsRemoved: 3}}}, nil
}
func (xrStore) FindSimilarResolvedProblems(context.Context, string, string, int) ([]chstore.Problem, error) {
	at := xrT0.Add(-48 * time.Hour)
	res := at.Add(12 * time.Minute).UnixNano()
	return []chstore.Problem{{Service: "orders-api", Metric: "p99_latency", Value: 1900, Severity: "critical", StartedAt: at.UnixNano(), ResolvedAt: &res}}, nil
}

// xrLogStore — tek satırlık sahte log arka ucu (bağlam yarıları boş).
type xrLogStore struct {
	logstore.Store
	rec *logstore.LogRecord
}

func (x *xrLogStore) Search(_ context.Context, f logstore.Filter) (*logstore.Page, error) {
	if f.Limit == logLineReadLimit {
		return &logstore.Page{Total: 1, Logs: []*logstore.LogRecord{x.rec}}, nil
	}
	return &logstore.Page{}, nil
}

func xrServer(t *testing.T, p *defProvider) *Server {
	t.Helper()
	explainStoreSeam = xrStore{}
	t.Cleanup(func() { explainStoreSeam = nil })
	return defServer(t, p)
}

// ── türlerin tablosu ──────────────────────────────────────────────────────

const xrLogBody = "ERROR orders-api: connection reset by peer (db.example.test:5432)"

var xrLogTs = xrT0.Add(5 * time.Minute).UnixNano()

type xrCase struct {
	kind    string
	ask     chatExplainAsk
	system  string
	surface string
	label   string
	http    func(s *Server) *httptest.ResponseRecorder
}

func xrPost(path string, body string, pv map[string]string, h func(*Server, http.ResponseWriter, *http.Request), s *Server) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for k, v := range pv {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	h(s, w, r)
	return w
}

func xrCases() []xrCase {
	from, to := xrT0.Add(-time.Hour).UnixNano(), xrT0.UnixNano()
	logSubj := mcptools.LogLineSubject(xrLogTs, "orders-api", "", "", xrLogBody)
	return []xrCase{
		{kind: "span", ask: chatExplainAsk{Kind: "span", ID: defTestTrace, Subject: "span:" + defTestTrace + ":" + defTestPaySpan},
			system: copilot.SystemPromptSpan(), surface: aisurface.ExplainSpanChat, label: "POST /charge",
			http: func(s *Server) *httptest.ResponseRecorder {
				return xrPost("/api/copilot/explain-span/"+defTestTrace+"?span="+defTestPaySpan, "", map[string]string{"traceId": defTestTrace}, (*Server).copilotExplainSpan, s)
			}},
		{kind: "problem", ask: chatExplainAsk{Kind: "problem", ID: xrProblemID},
			system: copilot.SystemPromptProblem(), surface: aisurface.ExplainProblemChat, label: "orders-api p99 latency",
			http: func(s *Server) *httptest.ResponseRecorder {
				return xrPost("/api/copilot/explain-problem/"+xrProblemID, "", map[string]string{"id": xrProblemID}, (*Server).copilotExplainProblem, s)
			}},
		{kind: "incident", ask: chatExplainAsk{Kind: "incident", ID: xrIncidentID},
			system: copilot.SystemPromptIncident(), surface: aisurface.ExplainIncidentChat, label: "orders-api yavaşlıyor",
			http: func(s *Server) *httptest.ResponseRecorder {
				return xrPost("/api/copilot/explain-incident/"+xrIncidentID, "", map[string]string{"id": xrIncidentID}, (*Server).copilotExplainIncident, s)
			}},
		{kind: "anomaly", ask: chatExplainAsk{Kind: "anomaly", ID: xrAnomalyID},
			system: copilot.SystemPromptAnomaly(), surface: aisurface.ExplainAnomalyChat, label: "connection reset",
			http: func(s *Server) *httptest.ResponseRecorder {
				return xrPost("/api/copilot/explain-anomaly/"+xrAnomalyID, "", map[string]string{"id": xrAnomalyID}, (*Server).copilotExplainAnomaly, s)
			}},
		{kind: "service-health", ask: chatExplainAsk{Kind: "service-health", ID: "orders-api",
			Subject: formatAiSubject(aiSubject{Kind: "service-health", ID: "orders-api", From: time.Unix(0, from), To: time.Unix(0, to)})},
			system: copilot.SystemPromptServiceHealth(), surface: aisurface.ExplainServiceHealthChat, label: "orders-api",
			http: func(s *Server) *httptest.ResponseRecorder {
				q := "?service=orders-api&from=" + strconv.FormatInt(from, 10) + "&to=" + strconv.FormatInt(to, 10)
				return xrPost("/api/copilot/explain-service"+q, "", nil, (*Server).copilotExplainServiceHealth, s)
			}},
		{kind: "runbook", ask: chatExplainAsk{Kind: "runbook", ID: xrProblemID},
			system: copilot.SystemPromptRunbook(), surface: aisurface.ExplainRunbookChat, label: runbookLabel,
			http: func(s *Server) *httptest.ResponseRecorder {
				return xrPost("/api/copilot/runbook/"+xrProblemID, "", map[string]string{"id": xrProblemID}, (*Server).copilotRunbook, s)
			}},
		{kind: "log", ask: chatExplainAsk{Kind: "log", Subject: logSubj},
			system: copilot.SystemPromptLogLine(), surface: aisurface.ExplainLogChat, label: "orders-api",
			http: func(s *Server) *httptest.ResponseRecorder {
				sp, _ := parseAiSubject(logSubj)
				body, _ := json.Marshal(map[string]string{"tsNs": sp.TsNs, "service": sp.ID, "pod": sp.Pod, "traceId": sp.TraceID, "hash": sp.Hash})
				return xrPost("/api/copilot/explain-log", string(body), nil, (*Server).copilotExplainLog, s)
			}},
	}
}

func xrLogServer(s *Server) {
	s.logs = &xrLogStore{rec: &logstore.LogRecord{Timestamp: xrLogTs, ServiceName: "orders-api", SeverityText: "ERROR", Body: xrLogBody}}
}

// ── kayıt ─────────────────────────────────────────────────────────────────

func TestExplainRegistryKindsRegistered(t *testing.T) {
	httpLabel := map[string]string{
		"span": "explain-span", "problem": "explain-problem", "incident": "explain-incident", "anomaly": "explain-anomaly",
		"service-health": "explain-service", "runbook": "runbook", "log": "explain-log",
		"trace": "explain-trace", "exception": "explain-exception",
		"charts": "explain-charts", "log-spike": "explain-log-spike", // v0.10.1407
		"metric_panel": "explain-metric-panel", // v0.10.1435 — HTTP ikizi yok; etiket aynı aileden
	}
	for _, kind := range explainTurnKinds {
		k, ok := explainKindFor(kind)
		if !ok || k.Kind != kind {
			t.Fatalf("%s kayıtta yok", kind)
		}
		if (k.Turn == nil) == (k.Build == nil) {
			t.Errorf("%s: tam olarak biri (Turn | Build) olmalı", kind)
		}
		if k.Surface != httpLabel[kind]+":chat" {
			t.Errorf("%s: yüzey %q, HTTP yüzeyi + :chat bekleniyor", kind, k.Surface)
		}
		if _, ok := aisurface.Lookup(k.Surface); !ok {
			t.Errorf("%s: yüzey %q aisurface'te kayıtlı değil", kind, k.Surface)
		}
		// Eval ailesi HTTP düğmesinin yüzeyiyle aynı (log: ikisi de Unknown).
		if got, want := evalSurfaceFromLabel(k.Surface), evalSurfaceFromLabel(httpLabel[kind]); got != want {
			t.Errorf("%s: evalset ailesi %q, HTTP %q", kind, got, want)
		}
	}
	// v0.10.1407 — charts / log-spike kayıtta; endpoint hâlâ değil (ayrı açıklama yüzeyi yok).
	for _, bad := range []string{"endpoint", ""} {
		if _, ok := explainKindFor(bad); ok {
			t.Errorf("%q kayıtta olmamalı", bad)
		}
	}
}

// ── düğme ↔ sohbet: tek önbellek satırı ───────────────────────────────────

func TestExplainRegistryChatSharesButtonCache(t *testing.T) {
	for _, c := range xrCases() {
		t.Run(c.kind, func(t *testing.T) {
			p := newDefProvider(t, "**Kök neden:** orders-api ", "db.example.test bağlantısı.")
			s := xrServer(t, p)
			if c.kind == "log" {
				xrLogServer(s)
			}
			// 1) ✨ düğmesi (buffered) üretir.
			w := c.http(s)
			if w.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
			var body map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			xid, _ := body["exchangeId"].(string)
			sys, user := p.lastMessages(t)
			if sys != c.system || xid == "" || p.requests() != 1 {
				t.Fatalf("düğme: sistem eşleşmedi ya da xid boş (calls=%d)", p.requests())
			}
			if _, hit, _ := s.cache.Get(context.Background(), explainCacheKey(c.system, user, "")); !hit {
				t.Fatal("düğme kendi anahtarına yazmadı")
			}
			// 2) Sohbet turu: isabet, sıfır LLM, saklanan exchangeId.
			var evs []xtEvent
			ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-chat"})
			handled, ok := s.chatExplainTurn(ctx, xtRecorder(&evs), &c.ask, time.UTC)
			if !handled || !ok {
				t.Fatalf("handled=%v ok=%v %s", handled, ok, xtKinds(evs))
			}
			if got := xtKinds(evs); got != "step,step-result,delta,answer" {
				t.Fatalf("olay sırası %s", got)
			}
			ans := xtAnswer(t, evs)
			if p.requests() != 1 || ans["cached"] != true || ans["exchangeId"] != xid {
				t.Fatalf("isabet değil: calls=%d cached=%v xid=%v (düğme %s)", p.requests(), ans["cached"], ans["exchangeId"], xid)
			}
			if ans["text"] != "**Kök neden:** orders-api db.example.test bağlantısı." {
				t.Fatalf("metin %q", ans["text"])
			}
			meta, _ := ans["explain"].(chatExplainMeta)
			if meta.Kind != c.kind || meta.Surface != c.surface || meta.Renderer != explainRendererBody || !meta.Cached || meta.CachedAtMs == 0 || meta.Label != c.label {
				t.Fatalf("künye %+v", meta)
			}
			if c.ask.Subject != "" && meta.Subject != c.ask.Subject {
				t.Fatalf("künye kodeği %q, niyet %q", meta.Subject, c.ask.Subject)
			}
			if c.kind == "runbook" && meta.Note != runbookSimilarNote(1) {
				t.Fatalf("runbook notu %q", meta.Note)
			}
			if _, has := ans["open"]; has {
				t.Fatal("açıklama turu çekmece açmamalı (open)")
			}
		})
	}
}

// Ters yön: önce sohbet üretir (akan), sonra düğme isabet eder.
func TestExplainRegistryButtonHitsChatCache(t *testing.T) {
	p := newDefProvider(t, "özet ", "cevap")
	s := xrServer(t, p)
	var evs []xtEvent
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-chat-1"})
	if h, ok := s.chatExplainTurn(ctx, xtRecorder(&evs), &chatExplainAsk{Kind: "problem", ID: xrProblemID, Src: "button"}, time.UTC); !h || !ok {
		t.Fatalf("sohbet turu: %s", xtKinds(evs))
	}
	if got := xtKinds(evs); got != "step,step-result,delta,delta,answer" || p.requests() != 1 {
		t.Fatalf("olay sırası %s calls=%d", got, p.requests())
	}
	w := xrPost("/api/copilot/explain-problem/"+xrProblemID, "", map[string]string{"id": xrProblemID}, (*Server).copilotExplainProblem, s)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["cached"] != true || body["exchangeId"] != "x-chat-1" || p.requests() != 1 || body["explanation"] != "özet cevap" {
		t.Fatalf("düğme sohbetin satırına isabet etmeli: %v calls=%d", body, p.requests())
	}
}

// Kurucu çıkarımı handler davranışını değiştirmedi: bulunamadı gövdeleri,
// ek alanlar; sohbette bulunamayan özne LLM'siz dürüst cevap.
func TestExplainRegistryHandlersUnchanged(t *testing.T) {
	p := newDefProvider(t, "x")
	s := xrServer(t, p)
	for _, c := range []struct {
		name string
		w    *httptest.ResponseRecorder
		code int
		body string
	}{
		{"problem", xrPost("/x", "", map[string]string{"id": "nope"}, (*Server).copilotExplainProblem, s), 404, "problem not found\n"},
		{"runbook", xrPost("/x", "", map[string]string{"id": "nope"}, (*Server).copilotRunbook, s), 404, "problem not found\n"},
		{"incident", xrPost("/x", "", map[string]string{"id": "nope"}, (*Server).copilotExplainIncident, s), 404, "incident not found\n"},
		{"anomaly", xrPost("/x", "", map[string]string{"id": "nope"}, (*Server).copilotExplainAnomaly, s), 404, "anomaly not found\n"},
		{"service yok", xrPost("/x", "", nil, (*Server).copilotExplainServiceHealth, s), 400, `{"error":"service required"}` + "\n"},
		{"span parametresiz", xrPost("/x", "", map[string]string{"traceId": defTestTrace}, (*Server).copilotExplainSpan, s), 400, "span query param required\n"},
		{"span trace'te yok", xrPost("/x?span=ffffffffffffffff", "", map[string]string{"traceId": defTestTrace}, (*Server).copilotExplainSpan, s), 404, "span not found in trace\n"},
		{"log arka ucu yok", xrPost("/x", `{"tsNs":"1700000000000000000","service":"orders-api"}`, nil, (*Server).copilotExplainLog, s), 503, ""},
	} {
		if c.w.Code != c.code || (c.body != "" && c.w.Body.String() != c.body) {
			t.Errorf("%s: %d %q, want %d %q", c.name, c.w.Code, c.w.Body.String(), c.code, c.body)
		}
	}
	if p.requests() != 0 {
		t.Fatalf("hata yolları LLM çağırdı: %d", p.requests())
	}
	// Ek alanlar: runbook similarCount, log matched.
	w := xrPost("/x", "", map[string]string{"id": xrProblemID}, (*Server).copilotRunbook, s)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["similarCount"] != float64(1) {
		t.Fatalf("runbook similarCount %v", body["similarCount"])
	}
	// Sohbette bulunamayan problem → dürüst cevap, LLM yok.
	calls := p.requests()
	var evs []xtEvent
	h, ok := s.chatExplainTurn(context.Background(), xtRecorder(&evs), &chatExplainAsk{Kind: "incident", ID: "00000000000000ff"}, time.UTC)
	if !h || ok || xtKinds(evs) != "step,step-result,answer" || p.requests() != calls {
		t.Fatalf("bulunamadı: h=%v ok=%v %s", h, ok, xtKinds(evs))
	}
	if txt, _ := xtAnswer(t, evs)["text"].(string); !strings.Contains(txt, "Olay `00000000000000ff` bulunamadı") {
		t.Fatalf("metin %q", txt)
	}
}

// ── niyet normalizasyonu + arşiv ──────────────────────────────────────────

func TestNormalizeExplainAskRegistryKinds(t *testing.T) {
	span := "span:" + defTestTrace + ":" + defTestPaySpan
	sh := "service-health:orders-api:1000:2000"
	for _, c := range []struct {
		name string
		in   chatExplainAsk
		want chatExplainAsk
		ok   bool
	}{
		{"problem", chatExplainAsk{Kind: "problem", ID: "p-1", Src: "button"}, chatExplainAsk{Kind: "problem", ID: "p-1", Src: "button"}, true},
		{"problem eşleşen kodek", chatExplainAsk{Kind: "problem", ID: "p-1", Subject: "problem:p-1"}, chatExplainAsk{Kind: "problem", ID: "p-1"}, true},
		{"problem uyuşmayan kodek", chatExplainAsk{Kind: "problem", ID: "p-1", Subject: "problem:p-2"}, chatExplainAsk{}, false},
		{"runbook kodeği başka tür", chatExplainAsk{Kind: "runbook", ID: "p-1", Subject: "problem:p-1"}, chatExplainAsk{}, false},
		{"incident enjeksiyon", chatExplainAsk{Kind: "incident", ID: "x&ai=trace"}, chatExplainAsk{}, false},
		{"span kodeksiz", chatExplainAsk{Kind: "span", ID: defTestTrace}, chatExplainAsk{}, false},
		{"span", chatExplainAsk{Kind: "span", ID: defTestTrace, Subject: span}, chatExplainAsk{Kind: "span", ID: defTestTrace, Subject: span}, true},
		{"span kimlik kodekten", chatExplainAsk{Kind: "span", Subject: span}, chatExplainAsk{Kind: "span", ID: defTestTrace, Subject: span}, true},
		{"span kimlik uyuşmaz", chatExplainAsk{Kind: "span", ID: "abc", Subject: span}, chatExplainAsk{}, false},
		{"service-health", chatExplainAsk{Kind: "Service-Health", ID: "orders-api", Subject: sh}, chatExplainAsk{Kind: "service-health", ID: "orders-api", Subject: sh}, true},
		{"service-health ters pencere", chatExplainAsk{Kind: "service-health", Subject: "service-health:orders-api:2000:1000"}, chatExplainAsk{}, false},
		{"service-health tür uyuşmaz", chatExplainAsk{Kind: "service-health", Subject: "charts:orders-api:1000:2000:all"}, chatExplainAsk{}, false},
		{"kodek çok uzun", chatExplainAsk{Kind: "service-health", Subject: "service-health:" + strings.Repeat("a", chatExplainSubjectMax) + ":1:2"}, chatExplainAsk{}, false},
		{"servis adında kontrol karakteri", chatExplainAsk{Kind: "service-health", Subject: "service-health:a%0Ab:1000:2000"}, chatExplainAsk{}, false},
		{"charts (v0.10.1407)", chatExplainAsk{Kind: "charts", ID: "orders-api", Subject: "charts:orders-api:1000:2000:all"}, chatExplainAsk{Kind: "charts", ID: "orders-api", Subject: "charts:orders-api:1000:2000:all"}, true},
		{"endpoint kayıt dışı", chatExplainAsk{Kind: "endpoint", Subject: "endpoint:orders-api:%2Fcart:1000:2000"}, chatExplainAsk{}, false},
	} {
		got, ok := normalizeExplainAsk(&c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestSanitizeChatExplainRegistryKinds(t *testing.T) {
	sh := "service-health:orders-api:1000:2000"
	out := sanitizeChatMessages([]aiChatMessage{
		{Role: "assistant", Text: "runbook", Explain: &chatExplainMeta{Kind: "runbook", ID: xrProblemID, Label: "RUNBOOK",
			Renderer: "body", Note: strings.Repeat("n", 500)}},
		{Role: "assistant", Text: "sağlık", Explain: &chatExplainMeta{Kind: "service-health", ID: "orders-api", Subject: sh, Renderer: "<script>"}},
		{Role: "assistant", Text: "kodeksiz span", Explain: &chatExplainMeta{Kind: "span", ID: defTestTrace}},
		{Role: "assistant", Text: "uzun kodek", Explain: &chatExplainMeta{Kind: "service-health", ID: "orders-api",
			Subject: "service-health:" + strings.Repeat("a", chatExplainSubjectMax) + ":1:2"}},
	})
	if m := out[0].Explain; m == nil || m.Renderer != "body" || len([]rune(m.Note)) != chatExplainNoteMax || m.Subject != "" {
		t.Fatalf("runbook künyesi %+v", out[0].Explain)
	}
	if m := out[1].Explain; m == nil || m.Subject != sh || m.Renderer != "" {
		t.Fatalf("service-health künyesi %+v (renderer beyaz listesi)", out[1].Explain)
	}
	if out[2].Explain != nil || out[3].Explain != nil {
		t.Fatalf("kodeksiz / uzun kodekli künye düşmeli: %+v %+v", out[2].Explain, out[3].Explain)
	}
	raw, _ := json.Marshal(out[1])
	if !strings.Contains(string(raw), `"subject":"service-health:orders-api:1000:2000"`) {
		t.Fatalf("tel şekli %s", raw)
	}
}
