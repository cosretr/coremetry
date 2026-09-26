package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// ai_evalset_runs_test.go — v0.10.940: Değerlendirme panelinin HTTP ve
// yaşam döngüsü sözleşmesi. Gerçek copilot.Service + OpenAI-uyumlu sahte uç
// (ai_call_matrix_test.go emsali) → çağrı gerçekten s.aiCall'dan geçer;
// depolama bellek içi sahte (canlı CH yok). Rotalar registerAIEvalsetRunRoutes
// ile kurulur: rol ve requireCopilot sarımları da sınanır.

// memEvalStore — FINAL'i taklit eder: kimlik başına en yüksek updated_at kazanır.
type memEvalStore struct {
	mu          sync.Mutex
	rows        map[string]chstore.EvalRun
	saves       []chstore.EvalRun // denenen TÜM yazımlar (düşenler dahil)
	panicOnSave int               // n. yazım (1 tabanlı) panikler; 0 = asla
	failCases   bool              // v0.10.940: cases taşıyan (son) yazımlar düşer
}

func newMemEvalStore() *memEvalStore { return &memEvalStore{rows: map[string]chstore.EvalRun{}} }

func (m *memEvalStore) setFailCases(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCases = v
}

func (m *memEvalStore) SaveEvalRun(_ context.Context, r chstore.EvalRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saves = append(m.saves, r)
	if m.panicOnSave > 0 && len(m.saves) == m.panicOnSave {
		panic("sahte depo paniği")
	}
	if m.failCases && r.Cases != "" {
		return errors.New("sahte depo: yazım düştü")
	}
	if old, ok := m.rows[r.ID]; ok && old.UpdatedAt.After(r.UpdatedAt) {
		return nil
	}
	m.rows[r.ID] = r
	return nil
}

func (m *memEvalStore) ListEvalRuns(_ context.Context, limit int) ([]chstore.EvalRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []chstore.EvalRun{}
	for _, r := range m.rows {
		r.Cases = "" // liste cases OKUMAZ
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *memEvalStore) GetEvalRun(_ context.Context, id string) (*chstore.EvalRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (m *memEvalStore) savesCopy() []chstore.EvalRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]chstore.EvalRun{}, m.saves...)
}

// evalStub — OpenAI-uyumlu sahte uç. JSON istenirse niyet JSON'u, değilse
// düz metin döner; block açıkken istek iptal ya da serbest bırakılana dek bekler.
type evalStub struct {
	srv     *httptest.Server
	hits    atomic.Int64
	block   chan struct{}
	mu      sync.Mutex
	schemas []string
}

func newEvalStub(t *testing.T, block bool) *evalStub {
	t.Helper()
	st := &evalStub{}
	if block {
		st.block = make(chan struct{})
	}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.hits.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if st.block != nil {
			select {
			case <-st.block:
			case <-r.Context().Done():
				return
			}
		}
		content := "checkout 2.14.0 sonrası bozuldu"
		if rf, ok := body["response_format"].(map[string]any); ok {
			if js, ok := rf["json_schema"].(map[string]any); ok {
				st.mu.Lock()
				st.schemas = append(st.schemas, js["name"].(string))
				st.mu.Unlock()
			}
			content = `{"intent":"service_health","service":"checkout","env":"","rangeS":3600}`
		}
		w.Header().Set("Content-Type", "application/json")
		out, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 2},
		})
		_, _ = w.Write(out)
	}))
	t.Cleanup(func() {
		if st.block != nil {
			select {
			case <-st.block:
			default:
				close(st.block)
			}
		}
		st.srv.Close()
	})
	return st
}

// evalTestCases — denetimli küçük küme: 2 geçer, 1 kalır, 1 atlanır.
func evalTestCases() []evalCase {
	zero := 0
	return []evalCase{
		{ID: "p-ok", Surface: "Problem", Why: "w", User: "Service: checkout", expectRaw: json.RawMessage(`{"mustContain":["2.14.0"]}`),
			Expect: evalExpect{MustContain: []string{"2.14.0"}, KnownEntities: []string{"checkout"}, MaxUnknownEntities: &zero}},
		{ID: "p-fail", Surface: "Problem", Why: "w", User: "Service: checkout", Expect: evalExpect{MustContain: []string{"payments"}}},
		{ID: "i-ok", Surface: "IntentClassify", Why: "w", User: "checkout nasıl?", Expect: evalExpect{Intent: "service_health", IntentService: "checkout", KnownEntities: []string{"checkout"}}},
		{ID: "s-skip", Surface: "Problem", Why: "w", Prompt: "kırpık", Provenance: &evalProvenance{Truncated: true}, Expect: evalExpect{MustContain: []string{"x"}}},
	}
}

type evalHarness struct {
	s     *Server
	mux   *http.ServeMux
	store *memEvalStore
	big   *evalStub
	small *evalStub
	rec   *chanRecorder
}

// newEvalHarness — iki profil: "big" varsayılan, "small" chat-intent'e
// eşli (üretim yönlendirmesi). Paket düzeyi dikişler temizlikte geri döner.
func newEvalHarness(t *testing.T, block bool, cases []evalCase) *evalHarness {
	t.Helper()
	h := &evalHarness{store: newMemEvalStore(), big: newEvalStub(t, block), small: newEvalStub(t, block)}
	cop := copilot.New(copilot.ProviderOpenAI, "SECRET-BIG", "gemma-big")
	cop.Configure(copilot.ProviderOpenAI, "SECRET-BIG", "gemma-big", h.big.srv.URL, false, true)
	cop.SetProfiles([]copilot.ModelProfile{
		{ID: "big", Label: "Büyük", Provider: copilot.ProviderOpenAI, BaseURL: h.big.srv.URL, APIKey: "SECRET-BIG", Model: "gemma-big"},
		{ID: "small", Label: "Yerel qwen", Provider: copilot.ProviderOpenAI, BaseURL: h.small.srv.URL, APIKey: "SECRET-SMALL", Model: "qwen-small"},
	}, "big", map[string]string{"chat-intent": "small"})
	h.rec = &chanRecorder{ch: make(chan copilot.CallRecord, 64)}
	cop.SetRecorder(h.rec)
	h.s = &Server{copilot: cop, buildVersion: "v0.10.940", auditQ: make(chan chstore.AuditEntry, 16)}
	h.mux = http.NewServeMux()
	h.s.registerAIEvalsetRunRoutes(h.mux)

	prevStore, prevLoader, prevDeadline, prevWaits := evalRunStoreFor, evalsetCasesLoader, evalRunDeadline, evalFinalSaveWaits
	evalRunStoreFor = func(*Server) evalRunStore { return h.store }
	evalsetCasesLoader = func() ([]evalCase, error) { return cases, nil }
	// Son yazım denemeleri: sayı üretimdeki gibi 4, aralık milisaniye.
	evalFinalSaveWaits = []time.Duration{0, time.Millisecond, time.Millisecond, time.Millisecond}
	evalJobs.mu.Lock()
	evalJobs.running, evalJobs.last = nil, nil
	evalJobs.mu.Unlock()
	t.Cleanup(func() {
		// Koşan iş varsa bitir ki sonraki teste sızmasın.
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
	return h
}

func (h *evalHarness) do(t *testing.T, method, target, body, role string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = bytes.NewBufferString(body)
	}
	r := httptest.NewRequest(method, target, rd)
	r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: "u1", Email: "admin@example.com", Role: role}))
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	return w
}

func waitEvalIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		evalJobs.mu.Lock()
		idle := evalJobs.running == nil
		evalJobs.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("koşu 5 sn içinde bitmedi")
}

func decodeEval[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("gövde çözülemedi (%d): %v — %s", w.Code, err, w.Body.String())
	}
	return v
}

// TestEvalsetCatalogShapeHasNoKeyFields — katalog üretimin profilini
// ANAHTARSIZ anlatır; IntentClassify yüzey haritasını izler (small).
func TestEvalsetCatalogShapeHasNoKeyFields(t *testing.T) {
	h := newEvalHarness(t, false, evalTestCases())
	w := h.do(t, http.MethodGet, "/api/ai/evalset/catalog", "", auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("katalog %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, leak := range []string{"SECRET-BIG", "SECRET-SMALL", "apiKey", "hasKey"} {
		if strings.Contains(body, leak) {
			t.Fatalf("katalog %q sızdırıyor: %s", leak, body)
		}
	}
	cat := decodeEval[evalCatalog](t, w)
	if !cat.Ready || cat.AppVersion != "v0.10.940" || cat.PromptVersion != copilot.PromptVersion() || cat.Total != 4 || len(cat.Surfaces) != 2 {
		t.Fatalf("katalog başlığı: %+v", cat)
	}
	if p := cat.Surfaces[0]; p.Surface != "Problem" || p.Cases != 3 || p.ProfileID != "big" || p.Model != "gemma-big" || p.BaseURL != h.big.srv.URL {
		t.Fatalf("Problem (çok vakalı önce, varsayılan profil): %+v", p)
	}
	if i := cat.Surfaces[1]; i.Surface != "IntentClassify" || i.ProfileID != "small" || i.ProfileLabel != "Yerel qwen" || i.Model != "qwen-small" {
		t.Fatalf("IntentClassify chat-intent profilini izlemeli: %+v", i)
	}
	// v0.10.940 — defaultProfileId: profil KİMLİĞİ (anahtar değil), koşu
	// özetinin profileId'siyle AYNI çözücü.
	if cat.DefaultProfileID != "big" || !strings.Contains(body, `"defaultProfileId":"big"`) {
		t.Fatalf("defaultProfileId: %q — %s", cat.DefaultProfileID, body)
	}
	if w := h.do(t, http.MethodGet, "/api/ai/evalset/catalog", "", auth.RoleViewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer katalog: %d", w.Code)
	}

	// Varsayılan değişince katalog ve koşu başlangıcı BİRLİKTE izler.
	h.s.copilot.SetProfiles([]copilot.ModelProfile{
		{ID: "big", Label: "Büyük", Provider: copilot.ProviderOpenAI, BaseURL: h.big.srv.URL, APIKey: "SECRET-BIG", Model: "gemma-big"},
		{ID: "small", Label: "Yerel qwen", Provider: copilot.ProviderOpenAI, BaseURL: h.small.srv.URL, APIKey: "SECRET-SMALL", Model: "qwen-small"},
	}, "small", nil)
	w = h.do(t, http.MethodGet, "/api/ai/evalset/catalog", "", auth.RoleAdmin)
	body = w.Body.String()
	cat = decodeEval[evalCatalog](t, w)
	w = h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleAdmin)
	if w.Code != http.StatusAccepted {
		t.Fatalf("başlat %d: %s", w.Code, w.Body.String())
	}
	started := decodeEval[struct{ Run evalRunSummary }](t, w).Run
	waitEvalIdle(t)
	if cat.DefaultProfileID != "small" || started.ProfileID != cat.DefaultProfileID || strings.Contains(body, "SECRET") {
		t.Fatalf("katalog %q ↔ koşu %q aynı çözücü olmalı, anahtarsız: %s", cat.DefaultProfileID, started.ProfileID, body)
	}
}

// TestEvalRunEndToEnd — koşu uçtan uca: 202, ardışık vakalar, üretim
// profiline yönlendirme, ai_calls etiketi/exchange kimliği, ilerleme
// yazımları cases'siz, son yazım cases'li, detay/liste, audit.
func TestEvalRunEndToEnd(t *testing.T) {
	h := newEvalHarness(t, false, evalTestCases())
	w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleAdmin) // gövdesiz = tümü
	if w.Code != http.StatusAccepted {
		t.Fatalf("başlat %d: %s", w.Code, w.Body.String())
	}
	started := decodeEval[struct{ Run evalRunSummary }](t, w).Run
	if started.Status != evalStatusRunning || started.Total != 4 || started.StartedBy != "admin@example.com" ||
		started.ProfileID != "big" || started.Model != "gemma-big" || started.Surfaces == nil || len(started.Surfaces) != 0 || started.FinishedAt != "" {
		t.Fatalf("202 özeti: %+v", started)
	}
	if !strings.HasPrefix(started.ID, "ev-") {
		t.Fatalf("kimlik biçimi: %q", started.ID)
	}
	waitEvalIdle(t)

	final, _ := h.store.GetEvalRun(context.Background(), started.ID)
	if final == nil || final.Status != evalStatusDone || final.Done != 4 || final.Pass != 2 || final.Fail != 1 || final.Skipped != 1 || final.FinishedAt.IsZero() {
		t.Fatalf("son satır: %+v", final)
	}
	saves := h.store.savesCopy()
	if len(saves) != 1+4+1 {
		t.Fatalf("yazım sayısı %d (başlangıç + 4 vaka + son)", len(saves))
	}
	for i, sv := range saves[:len(saves)-1] {
		if sv.Cases != "" || sv.Status != evalStatusRunning || int(sv.Done) != i {
			t.Fatalf("ilerleme yazımı %d: status=%s done=%d casesLen=%d", i, sv.Status, sv.Done, len(sv.Cases))
		}
		if sv.Summary == "" {
			t.Fatalf("ilerleme yazımı %d summary taşımalı", i)
		}
	}
	if last := saves[len(saves)-1]; last.Cases == "" || !last.UpdatedAt.After(saves[len(saves)-2].UpdatedAt) {
		t.Fatal("son yazım cases taşımalı ve en yüksek sürüm olmalı")
	}

	// Yönlendirme: Problem → big (2 çağrı; atlanan çağrılmaz), IntentClassify → small.
	if b, s := h.big.hits.Load(), h.small.hits.Load(); b != 2 || s != 1 {
		t.Fatalf("uç isabetleri big=%d small=%d", b, s)
	}
	h.small.mu.Lock()
	schemas := append([]string{}, h.small.schemas...)
	h.small.mu.Unlock()
	if len(schemas) != 1 || schemas[0] != "evalset-IntentClassify" {
		t.Fatalf("niyet çağrısı üretim şemasıyla (json_schema) gitmeli: %v", schemas)
	}
	seen := map[string]copilot.CallRecord{}
	for i := 0; i < 3; i++ {
		rec := h.rec.next(t)
		seen[rec.ExchangeID] = rec
	}
	ic, ok := seen[started.ID+"/i-ok"]
	if !ok || ic.Surface != "evalset-IntentClassify" || ic.ProfileID != "small" || ic.UserEmail != "admin@example.com" {
		t.Fatalf("ai_calls satırı (evalset öneki + üretim profili): %+v", seen)
	}
	if p := seen[started.ID+"/p-ok"]; p.Surface != "evalset-Problem" || p.ProfileID != "big" {
		t.Fatalf("Problem satırı: %+v", p)
	}

	w = h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+started.ID, "", auth.RoleAdmin)
	det := decodeEval[struct {
		Run   evalRunSummary
		Cases []evalCaseResult
	}](t, w)
	if w.Code != http.StatusOK || det.Run.Status != evalStatusDone || len(det.Cases) != 4 {
		t.Fatalf("detay %d: %+v", w.Code, det.Run)
	}
	byID := map[string]evalCaseResult{}
	for _, c := range det.Cases {
		byID[c.ID] = c
	}
	if c := byID["i-ok"]; !c.OK || c.ProfileID != "small" || c.Model != "qwen-small" || c.Input != "checkout nasıl?" || !strings.Contains(c.Answer, "service_health") {
		t.Fatalf("niyet vakası: %+v", c)
	}
	if c := byID["p-fail"]; c.OK || len(c.Fails) != 1 || c.Fails[0] != "missing: payments" {
		t.Fatalf("kalan vaka: %+v", c)
	}
	if c := byID["p-ok"]; string(c.Expect) != `{"mustContain":["2.14.0"]}` {
		t.Fatalf("expect harfiyen: %s", c.Expect)
	}
	if c := byID["s-skip"]; !c.Skipped || c.SkipReason == "" || c.OK {
		t.Fatalf("atlanan: %+v", c)
	}
	var prob *evalSurfaceSummary
	for i := range det.Run.BySurface {
		if det.Run.BySurface[i].Surface == "Problem" {
			prob = &det.Run.BySurface[i]
		}
	}
	if prob == nil || prob.Pass != 1 || prob.Fail != 1 || prob.Skipped != 1 || prob.UnknownEntities == nil {
		t.Fatalf("yüzey özeti: %+v", det.Run.BySurface)
	}

	w = h.do(t, http.MethodGet, "/api/ai/evalset/runs", "", auth.RoleAdmin)
	list := decodeEval[struct{ Runs []evalRunSummary }](t, w)
	if len(list.Runs) != 1 || list.Runs[0].ID != started.ID || list.Runs[0].Status != evalStatusDone {
		t.Fatalf("liste: %+v", list.Runs)
	}

	select {
	case e := <-h.s.auditQ:
		if e.Action != "ai.evalset.run" || e.TargetKind != "ai_evalset" || e.TargetID != started.ID || e.Details != "surfaces=all cases=4" {
			t.Fatalf("audit: %+v", e)
		}
	default:
		t.Fatal("ai.evalset.run audit'i yazılmadı")
	}
}

// TestEvalRunConflictAndCancel — koşu sürerken ikinci başlatma 409 (gövdede
// koşan koşu); iptal 200 → cancelled; ikinci iptal 409; bitmiş koşu kıyaslanır.
func TestEvalRunConflictAndCancel(t *testing.T) {
	h := newEvalHarness(t, true, evalTestCases())
	w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", `{"surfaces":["Problem"]}`, auth.RoleAdmin)
	if w.Code != http.StatusAccepted {
		t.Fatalf("başlat %d: %s", w.Code, w.Body.String())
	}
	run := decodeEval[struct{ Run evalRunSummary }](t, w).Run
	if run.Total != 3 || strings.Join(run.Surfaces, ",") != "Problem" {
		t.Fatalf("seçim: %+v", run)
	}

	w = h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleAdmin)
	if w.Code != http.StatusConflict {
		t.Fatalf("ikinci başlatma %d", w.Code)
	}
	conflict := decodeEval[struct {
		Error string
		Run   evalRunSummary
	}](t, w)
	if conflict.Error != "bir değerlendirme koşusu zaten sürüyor" || conflict.Run.ID != run.ID {
		t.Fatalf("409 gövdesi: %+v", conflict)
	}

	// Detay koşu sürerken bellekten (cases o ana dek gelenler).
	w = h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+run.ID, "", auth.RoleAdmin)
	if w.Code != http.StatusOK || decodeEval[struct{ Run evalRunSummary }](t, w).Run.Status != evalStatusRunning {
		t.Fatalf("süren detay %d: %s", w.Code, w.Body.String())
	}

	if w := h.do(t, http.MethodPost, "/api/ai/evalset/runs/ev-baska/cancel", "", auth.RoleAdmin); w.Code != http.StatusConflict ||
		!strings.Contains(w.Body.String(), "koşu bu sunucuda sürmüyor ya da bitti") {
		t.Fatalf("yabancı iptal %d: %s", w.Code, w.Body.String())
	}
	w = h.do(t, http.MethodPost, "/api/ai/evalset/runs/"+run.ID+"/cancel", "", auth.RoleAdmin)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("iptal %d: %s", w.Code, w.Body.String())
	}
	waitEvalIdle(t)
	final, _ := h.store.GetEvalRun(context.Background(), run.ID)
	if final == nil || final.Status != evalStatusCancelled || final.Done != 0 || final.FinishedAt.IsZero() {
		t.Fatalf("iptal sonrası (yarıda kesilen vaka sayılmaz): %+v", final)
	}
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/runs/"+run.ID+"/cancel", "", auth.RoleAdmin); w.Code != http.StatusConflict {
		t.Fatalf("bitmiş koşuyu iptal %d", w.Code)
	}
	var actions []string
	for len(h.s.auditQ) > 0 {
		actions = append(actions, (<-h.s.auditQ).Action)
	}
	if strings.Join(actions, ",") != "ai.evalset.run,ai.evalset.cancel" {
		t.Fatalf("audit sırası: %v", actions)
	}
}

// TestEvalRunStartGuards — 400 bilinmeyen yüzey / bozuk JSON, 403 viewer,
// 503 AI hazır değil, çapraz-pod: taze başka-pod "running" 409, bayatı engel değil.
func TestEvalRunStartGuards(t *testing.T) {
	h := newEvalHarness(t, false, evalTestCases())
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", `{"surfaces":["Trace"]}`, auth.RoleAdmin); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "bilinmeyen yüzey") {
		t.Fatalf("bilinmeyen yüzey %d: %s", w.Code, w.Body.String())
	}
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", `{"surfaces":`, auth.RoleAdmin); w.Code != http.StatusBadRequest {
		t.Fatalf("bozuk JSON %d", w.Code)
	}
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleEditor); w.Code != http.StatusForbidden {
		t.Fatalf("editor %d", w.Code)
	}

	now := time.Now().UTC()
	h.store.rows["ev-otherpod"] = chstore.EvalRun{ID: "ev-otherpod", Status: evalStatusRunning, StartedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-30 * time.Second)}
	w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleAdmin)
	if w.Code != http.StatusConflict || decodeEval[struct{ Run evalRunSummary }](t, w).Run.ID != "ev-otherpod" {
		t.Fatalf("başka pod'un taze koşusu 409 vermeli: %d %s", w.Code, w.Body.String())
	}
	h.store.rows["ev-otherpod"] = chstore.EvalRun{ID: "ev-otherpod", Status: evalStatusRunning, StartedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-40 * time.Minute)}
	w = h.do(t, http.MethodGet, "/api/ai/evalset/runs", "", auth.RoleAdmin)
	if l := decodeEval[struct{ Runs []evalRunSummary }](t, w); len(l.Runs) != 1 || l.Runs[0].Status != evalStatusAbandoned {
		t.Fatalf("bayat sahipsiz koşu abandoned görünmeli: %+v", l.Runs)
	}
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleAdmin); w.Code != http.StatusAccepted {
		t.Fatalf("terk edilmiş koşu yeni koşuyu engellememeli: %d %s", w.Code, w.Body.String())
	}
	waitEvalIdle(t)

	h.s.copilot = nil
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleAdmin); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("AI hazır değilken %d", w.Code)
	}
	if w := h.do(t, http.MethodGet, "/api/ai/evalset/catalog", "", auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("katalog AI kapalıyken de cevap verir: %d %s", w.Code, w.Body.String())
	} else if cat := decodeEval[evalCatalog](t, w); cat.Ready || cat.DefaultProfileID != "" {
		t.Fatalf("AI kapalıyken ready=false, defaultProfileId boş: %+v", cat)
	}
}

// TestEvalRunFinalSaveFailureKeepsResults — v0.10.940: son yazım (cases'i
// taşıyan TEK yazım) tüm denemelerde düşerse koşu kaybolmaz. Eskiden iş
// defterden atılıyordu: CH'de bayat "running"+boş cases → sahip pod dahil
// 15 dk 409, sonra "abandoned" ve vaka sonuçları kayıp. Şimdi: yeniden
// denenir, düşerse bellekte kalır — aynı süreçte yeni koşu 409 ALMAZ,
// detay/liste/kıyas son durumu verir; sonraki koşunun son yazımı tutunca
// eski satır da yazılır ve defter boşalır.
func TestEvalRunFinalSaveFailureKeepsResults(t *testing.T) {
	h := newEvalHarness(t, false, evalTestCases())
	h.store.setFailCases(true)
	w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleAdmin)
	if w.Code != http.StatusAccepted {
		t.Fatalf("başlat %d: %s", w.Code, w.Body.String())
	}
	id := decodeEval[struct{ Run evalRunSummary }](t, w).Run.ID
	waitEvalIdle(t)

	// Ön koşul: CH'de bayat "running", cases boş; son yazım 4 kez denendi.
	if stored, _ := h.store.GetEvalRun(context.Background(), id); stored == nil || stored.Status != evalStatusRunning || stored.Cases != "" {
		t.Fatalf("CH'de bayat running kalmalı: %+v", stored)
	}
	tries := 0
	for _, sv := range h.store.savesCopy() {
		if sv.Cases != "" {
			tries++
		}
	}
	if tries != len(evalFinalSaveWaits) {
		t.Fatalf("son yazım %d kez denenmeli, %d", len(evalFinalSaveWaits), tries)
	}

	// Detay: bellekten, son durum + vakalar.
	w = h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+id, "", auth.RoleAdmin)
	det := decodeEval[struct {
		Run   evalRunSummary
		Cases []evalCaseResult
	}](t, w)
	if w.Code != http.StatusOK || det.Run.Status != evalStatusDone || det.Run.Done != 4 || det.Run.FinishedAt == "" || len(det.Cases) != 4 {
		t.Fatalf("detay son durumu ve vakaları vermeli %d: %+v cases=%d", w.Code, det.Run, len(det.Cases))
	}

	// Liste: bayat satırın yerine son durum; kıyas: CH satırı bitmemiş ama
	// bellek kopyası bitmiş → 200.
	older := time.Now().UTC().Add(-time.Hour)
	h.store.mu.Lock()
	h.store.rows["ev-older"] = chstore.EvalRun{ID: "ev-older", Status: evalStatusDone, Model: "gemma-big", StartedAt: older, UpdatedAt: older, Cases: "[]"}
	h.store.mu.Unlock()
	w = h.do(t, http.MethodGet, "/api/ai/evalset/runs", "", auth.RoleAdmin)
	if l := decodeEval[struct{ Runs []evalRunSummary }](t, w); len(l.Runs) != 2 || l.Runs[0].ID != id || l.Runs[0].Status != evalStatusDone || l.Runs[1].ID != "ev-older" {
		t.Fatalf("liste son durumu göstermeli: %+v", l.Runs)
	}
	w = h.do(t, http.MethodGet, "/api/ai/evalset/compare?base=ev-older&head="+id, "", auth.RoleAdmin)
	if cmp := decodeEval[evalCompare](t, w); w.Code != http.StatusOK || cmp.Head.ID != id || cmp.Head.Total != 4 {
		t.Fatalf("kıyas bellek kopyasıyla %d: %s", w.Code, w.Body.String())
	}

	// Aynı süreçte yeni koşu: en yeni CH satırı taze "running" ama bizim
	// bitmiş koşumuz → 409 değil. Bu sefer depo sağlıklı.
	h.store.setFailCases(false)
	w = h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleAdmin)
	if w.Code != http.StatusAccepted {
		t.Fatalf("kendi düşmüş son yazımımız yeni koşuyu engellememeli: %d %s", w.Code, w.Body.String())
	}
	id2 := decodeEval[struct{ Run evalRunSummary }](t, w).Run.ID
	waitEvalIdle(t)

	// Sonraki başarılı son yazım eski satırı da yazar; defter boşalır.
	healed, _ := h.store.GetEvalRun(context.Background(), id)
	if healed == nil || healed.Status != evalStatusDone || len(evalCasesFromRow(*healed)) != 4 {
		t.Fatalf("düşmüş satır sonraki koşuda yazılmalı: %+v", healed)
	}
	if second, _ := h.store.GetEvalRun(context.Background(), id2); second == nil || second.Status != evalStatusDone || second.Cases == "" {
		t.Fatalf("ikinci koşu: %+v", second)
	}
	evalJobs.mu.Lock()
	last := evalJobs.last
	evalJobs.mu.Unlock()
	if last != nil {
		t.Fatalf("yazılan iş defterde kalmamalı: %s", last.row.ID)
	}
}

// TestEvalRunFailurePaths — panik → failed (süreç ölmez), 60 dk tavanı →
// failed (tavan dikişle milisaniyeye indirilir).
func TestEvalRunFailurePaths(t *testing.T) {
	t.Run("panik", func(t *testing.T) {
		h := newEvalHarness(t, false, evalTestCases())
		h.store.panicOnSave = 2 // ilk ilerleme yazımı (koşu goroutine'inde)
		w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleAdmin)
		if w.Code != http.StatusAccepted {
			t.Fatalf("başlat %d", w.Code)
		}
		id := decodeEval[struct{ Run evalRunSummary }](t, w).Run.ID
		waitEvalIdle(t)
		final, _ := h.store.GetEvalRun(context.Background(), id)
		if final == nil || final.Status != evalStatusFailed || !strings.Contains(final.Error, "panik") || final.Cases == "" {
			t.Fatalf("panik → failed: %+v", final)
		}
	})
	t.Run("tavan", func(t *testing.T) {
		h := newEvalHarness(t, true, evalTestCases())
		evalRunDeadline = 50 * time.Millisecond
		w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", "", auth.RoleAdmin)
		if w.Code != http.StatusAccepted {
			t.Fatalf("başlat %d", w.Code)
		}
		id := decodeEval[struct{ Run evalRunSummary }](t, w).Run.ID
		waitEvalIdle(t)
		final, _ := h.store.GetEvalRun(context.Background(), id)
		if final == nil || final.Status != evalStatusFailed || !strings.Contains(final.Error, "tavan") {
			t.Fatalf("tavan → failed: %+v", final)
		}
	})
}

// TestEvalRunDetailAndCompareHTTP — 404 bilinmeyen koşu; kıyas 400
// (eksik/eşit), 404 (yok / bitmemiş), 200 (iki bitmiş koşu).
func TestEvalRunDetailAndCompareHTTP(t *testing.T) {
	h := newEvalHarness(t, false, evalTestCases())
	if w := h.do(t, http.MethodGet, "/api/ai/evalset/runs/ev-yok", "", auth.RoleAdmin); w.Code != http.StatusNotFound {
		t.Fatalf("bilinmeyen koşu %d", w.Code)
	}
	cases := func(ok bool) string {
		b, _ := json.Marshal([]evalCaseResult{{ID: "c1", Surface: "Problem", OK: ok, RubricTotal: map[bool]float64{true: 1, false: 0.4}[ok]}})
		return string(b)
	}
	t0 := time.Now().UTC().Add(-time.Hour)
	h.store.rows["ev-a"] = chstore.EvalRun{ID: "ev-a", Status: evalStatusDone, Model: "qwen", PromptVersion: "p1", StartedAt: t0, UpdatedAt: t0, Pass: 1, Total: 1, Cases: cases(true)}
	h.store.rows["ev-b"] = chstore.EvalRun{ID: "ev-b", Status: evalStatusCancelled, Model: "qwen", PromptVersion: "p2", StartedAt: t0.Add(time.Minute), UpdatedAt: t0.Add(time.Minute), Fail: 1, Total: 1, Cases: cases(false)}
	h.store.rows["ev-run"] = chstore.EvalRun{ID: "ev-run", Status: evalStatusRunning, StartedAt: t0, UpdatedAt: time.Now().UTC()}

	for _, c := range []struct {
		q    string
		want int
	}{
		{"", http.StatusBadRequest},
		{"base=ev-a", http.StatusBadRequest},
		{"base=ev-a&head=ev-a", http.StatusBadRequest},
		{"base=ev-a&head=ev-yok", http.StatusNotFound},
		{"base=ev-run&head=ev-a", http.StatusNotFound},
		{"base=ev-a&head=ev-b", http.StatusOK},
	} {
		if w := h.do(t, http.MethodGet, "/api/ai/evalset/compare?"+c.q, "", auth.RoleAdmin); w.Code != c.want {
			t.Errorf("compare?%s → %d, want %d (%s)", c.q, w.Code, c.want, w.Body.String())
		}
	}
	w := h.do(t, http.MethodGet, "/api/ai/evalset/compare?base=ev-a&head=ev-b", "", auth.RoleAdmin)
	cmp := decodeEval[evalCompare](t, w)
	if !cmp.Comparable || len(cmp.NewlyFailing) != 1 || cmp.NewlyFailing[0].ID != "c1" || cmp.Base.ID != "ev-a" || cmp.Head.ID != "ev-b" {
		t.Fatalf("kıyas: %+v", cmp)
	}
	w = h.do(t, http.MethodGet, "/api/ai/evalset/runs/ev-a", "", auth.RoleAdmin)
	if det := decodeEval[struct{ Cases []evalCaseResult }](t, w); w.Code != http.StatusOK || len(det.Cases) != 1 {
		t.Fatalf("CH'den detay %d: %s", w.Code, w.Body.String())
	}
	// Liste en yeni önce, cases taşımaz.
	w = h.do(t, http.MethodGet, "/api/ai/evalset/runs", "", auth.RoleAdmin)
	if l := decodeEval[struct{ Runs []evalRunSummary }](t, w); len(l.Runs) != 3 || l.Runs[0].ID != "ev-b" {
		t.Fatalf("liste sırası: %+v", l.Runs)
	}
}
