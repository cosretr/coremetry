package api

// v0.10.953 — PromQL konsolu HTTP sözleşmesi (spec C5), SAHTE Thanos'a
// karşı (httptest; canlı çağrı YOK). Kapsam: rol kapısı (viewer 403, editor
// ok), parametre 400'leri, aralık korkuluğu (7g ±1 s), adım yükseltme meta'sı,
// 413 (istek + yanıt gövdesi), 429 + Retry-After, 504, 502 (uç nokta URL'si
// gövdede YOK), bad_data konumu, sonuç başına tek audit satırı (JSON
// details; metadata/geçmiş için YOK), meta.effectiveQuery, metadata
// önbellek isabeti upstream'e gitmez ve bütçeden düşmez.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// ── Sahte Thanos + test ortamı ──────────────────────────────────────────────

type promqlFakeReq struct {
	Method, Path string
	Form         url.Values
}

type promqlFakeThanos struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []promqlFakeReq
}

type promqlRespond func(w http.ResponseWriter, r *http.Request, req promqlFakeReq)

func newPromQLFakeThanos(t *testing.T, respond promqlRespond) *promqlFakeThanos {
	t.Helper()
	f := &promqlFakeThanos{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		req := promqlFakeReq{Method: r.Method, Path: r.URL.Path, Form: r.Form}
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		f.mu.Unlock()
		respond(w, r, req)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *promqlFakeThanos) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func (f *promqlFakeThanos) last(t *testing.T) promqlFakeReq {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		t.Fatal("sahte Thanos hiç istek almadı")
	}
	return f.reqs[len(f.reqs)-1]
}

// promqlDefaultThanos — her uca geçerli bir Prometheus cevabı.
func promqlDefaultThanos(w http.ResponseWriter, _ *http.Request, req promqlFakeReq) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case req.Path == "/api/v1/query":
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[`+
			`{"metric":{"__name__":"up","job":"a"},"value":[1790000000,"1"]},`+
			`{"metric":{"__name__":"up","job":"b"},"value":[1790000000,"NaN"]}]}}`)
	case req.Path == "/api/v1/query_range":
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[`+
			`{"metric":{"job":"a"},"values":[[1790000000,"1"],[1790000015,"2"]]}]},"warnings":["upstream note"]}`)
	case req.Path == "/api/v1/labels":
		fmt.Fprint(w, `{"status":"success","data":["__name__","job","namespace"]}`)
	case strings.HasPrefix(req.Path, "/api/v1/label/"):
		fmt.Fprint(w, `{"status":"success","data":["a","b","c"]}`)
	case req.Path == "/api/v1/series":
		fmt.Fprint(w, `{"status":"success","data":[{"__name__":"up","job":"a"},{"__name__":"up","job":"b"}]}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// promqlFakeHistoryStore — saved_views'ın bellek içi ikizi.
type promqlFakeHistoryStore struct {
	mu      sync.Mutex
	rows    map[string]chstore.SavedView
	upserts int
	getErr  error
}

func newPromQLFakeHistoryStore() *promqlFakeHistoryStore {
	return &promqlFakeHistoryStore{rows: map[string]chstore.SavedView{}}
}

func (f *promqlFakeHistoryStore) GetSavedView(_ context.Context, id string) (*chstore.SavedView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	v, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (f *promqlFakeHistoryStore) UpsertSavedView(_ context.Context, v chstore.SavedView) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts++
	f.rows[v.ID] = v
	return nil
}

func (f *promqlFakeHistoryStore) upsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upserts
}

type promqlTestEnv struct {
	s    *Server
	mux  *http.ServeMux
	fake *promqlFakeThanos
	hist *promqlFakeHistoryStore
	clk  *promqlFakeClock
}

const (
	promqlClusterA = "cluster-a" // cluster başına URL modeli (enjeksiyon yok)
	promqlClusterB = "cluster-b" // paylaşımlı querier: ThanosLabelName=cluster
)

// newPromQLTestEnv — sahte Thanos, iki cluster, noop önbellek, taze limiter
// (sahte saat), bellek içi geçmiş deposu, varsayılan ayarlar. Paket
// global'leri test sonunda geri konur.
func newPromQLTestEnv(t *testing.T, respond promqlRespond) *promqlTestEnv {
	t.Helper()
	if respond == nil {
		respond = promqlDefaultThanos
	}
	f := newPromQLFakeThanos(t, respond)
	svc := thanos.New()
	svc.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{
		{Name: promqlClusterA, URL: f.URL, Enabled: true},
		{Name: promqlClusterB, URL: f.URL, ThanosLabelName: "cluster", Enabled: true},
		{Name: "cluster-off", URL: f.URL, Enabled: false},
	}})
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats(),
		auditQ: make(chan chstore.AuditEntry, 256), thanos: svc}

	clk := &promqlFakeClock{t: time.Date(2026, 9, 26, 12, 0, 10, 0, time.UTC)}
	prevLim := promqlLimits
	promqlLimits = newPromQLLimiter(clk.now)
	hist := newPromQLFakeHistoryStore()
	prevStore := promqlHistoryStoreOf
	promqlHistoryStoreOf = func(*Server) promqlHistoryStore { return hist }
	t.Cleanup(func() {
		promqlLimits = prevLim
		promqlHistoryStoreOf = prevStore
	})
	withPromQLConsoleSettings(t, defaultPromQLConsoleSettings())

	mux := http.NewServeMux()
	s.registerPromQLRoutes(mux)
	return &promqlTestEnv{s: s, mux: mux, fake: f, hist: hist, clk: clk}
}

func promqlAs(r *http.Request, uid, role string) *http.Request {
	return r.WithContext(auth.ContextWithClaims(r.Context(),
		&auth.Claims{UserID: uid, Email: uid + "@example.test", Role: role}))
}

// do — GET: form sorgu dizesine; POST: x-www-form-urlencoded gövde.
func (e *promqlTestEnv) do(t *testing.T, method, path string, form url.Values, uid, role string) *httptest.ResponseRecorder {
	t.Helper()
	return e.doCtx(t, context.Background(), method, path, form, uid, role)
}

func (e *promqlTestEnv) doCtx(t *testing.T, ctx context.Context, method, path string, form url.Values, uid, role string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if method == http.MethodPost {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		target := path
		if len(form) > 0 {
			target += "?" + form.Encode()
		}
		req = httptest.NewRequest(method, target, nil)
	}
	req = req.WithContext(ctx)
	if uid != "" {
		req = promqlAs(req, uid, role)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func (e *promqlTestEnv) audits() []chstore.AuditEntry {
	var out []chstore.AuditEntry
	for {
		select {
		case a := <-e.s.auditQ:
			out = append(out, a)
		default:
			return out
		}
	}
}

// oneAudit — tam bir promql.query satırı; details JSON olarak çözülür.
func (e *promqlTestEnv) oneAudit(t *testing.T) (chstore.AuditEntry, map[string]any) {
	t.Helper()
	rows := e.audits()
	if len(rows) != 1 {
		t.Fatalf("tam 1 audit satırı beklenirdi, %d: %+v", len(rows), rows)
	}
	a := rows[0]
	if a.Action != "promql.query" || a.TargetKind != "thanos_cluster" {
		t.Fatalf("audit action/kind: %+v", a)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(a.Details), &d); err != nil {
		t.Fatalf("audit details JSON değil: %v — %s", err, a.Details)
	}
	for _, k := range []string{"cluster", "mode", "query", "start", "end", "step", "durationMs", "series", "truncated", "status", "errorType"} {
		if _, ok := d[k]; !ok {
			t.Errorf("audit details %q alanını taşımıyor: %s", k, a.Details)
		}
	}
	return a, d
}

func (e *promqlTestEnv) historyEntries(t *testing.T, uid string) []promqlHistoryEntry {
	t.Helper()
	e.hist.mu.Lock()
	v, ok := e.hist.rows[promqlHistoryID(uid)]
	e.hist.mu.Unlock()
	if !ok || v.Name == "" {
		return nil
	}
	var b promqlHistoryBlob
	if err := json.Unmarshal([]byte(v.QueryString), &b); err != nil {
		t.Fatalf("geçmiş gövdesi bozuk: %v", err)
	}
	return b.Entries
}

func decodePromQLBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("gövde JSON değil (%d): %v — %s", w.Code, err, w.Body.String())
	}
	return m
}

func promqlMeta(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	m, ok := body["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta yok: %v", body)
	}
	return m
}

// assertNoEndpointLeak — hiçbir hata gövdesi yapılandırılmış URL'yi, host'u
// ya da ham Go taşıma hatasını taşımaz (karar 11).
func assertNoEndpointLeak(t *testing.T, body string, endpoint string) {
	t.Helper()
	u, _ := url.Parse(endpoint)
	for _, leak := range []string{endpoint, u.Host, "127.0.0.1", "dial tcp", "Post \"", "Get \""} {
		if leak != "" && strings.Contains(body, leak) {
			t.Fatalf("gövde %q sızdırıyor: %s", leak, body)
		}
	}
}

func clusterIDOf(name string) string { return thanos.ClusterConfig{Name: name}.EffectiveID() }

const promqlT0 = int64(1790000000) // 2026-09-21T14:13:20Z

func unixStr(sec int64) string { return fmt.Sprint(sec) }

// ── Kayıt ve rol kapısı (karar 1, 9) ────────────────────────────────────────

func TestPromQLRoutesRegisteredViaRegistry(t *testing.T) {
	mux := (&Server{}).buildMux()
	for _, c := range []struct{ method, path, pattern string }{
		{"GET", "/api/promql/query", "GET /api/promql/query"},
		{"POST", "/api/promql/query", "POST /api/promql/query"},
		{"GET", "/api/promql/query_range", "GET /api/promql/query_range"},
		{"POST", "/api/promql/query_range", "POST /api/promql/query_range"},
		{"GET", "/api/promql/labels", "GET /api/promql/labels"},
		{"GET", "/api/promql/label/k8s.pod.name/values", "GET /api/promql/label/{name}/values"},
		{"GET", "/api/promql/series", "GET /api/promql/series"},
		{"GET", "/api/promql/history", "GET /api/promql/history"},
		{"DELETE", "/api/promql/history", "DELETE /api/promql/history"},
		{"GET", "/api/settings/promql-console", "GET /api/settings/promql-console"},
		{"PUT", "/api/settings/promql-console", "PUT /api/settings/promql-console"},
	} {
		_, pattern := mux.Handler(httptest.NewRequest(c.method, c.path, nil))
		if pattern != c.pattern {
			t.Errorf("%s %s → kalıp %q, beklenen %q (kayıtsız rota SPA'ya 200 düşer)", c.method, c.path, pattern, c.pattern)
		}
	}
	src, err := os.ReadFile("promql_console_routes.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoComments(string(src)), `registerRoutesExtra("promql", (*Server).registerPromQLRoutes)`) {
		t.Error("promql_console_routes.go init() kaydı yok")
	}
	if b, _ := os.ReadFile("api.go"); strings.Contains(string(b), "/api/promql") || strings.Contains(string(b), "registerPromQLRoutes") {
		t.Error("api.go konsol rotası içeriyor — kayıt defterden olmalı, api.go büyümez")
	}
}

// Sızıntı kuralı yapısal: handler dosyaları writeErr çağırmaz (varsayılan
// dalı ham err.Error() yankılar — yapılandırılmış URL'yi taşır).
func TestPromQLHandlersNeverUseWriteErr(t *testing.T) {
	for _, f := range []string{"promql_console_handlers.go", "promql_console_history.go", "promql_console_routes.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		code := stripGoComments(string(b))
		for _, bad := range []string{"writeErr(", "serveCached(", "err.Error()"} {
			if strings.Contains(code, bad) {
				t.Errorf("%s %q içeriyor — hata istemciye ham yankılanabilir / sorgu önbelleğe girer", f, bad)
			}
		}
	}
}

func TestPromQLRoutesRoleGate(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	q := url.Values{"query": {"up"}, "cluster": {promqlClusterA}}
	rq := url.Values{"query": {"up"}, "cluster": {promqlClusterA},
		"start": {unixStr(promqlT0 - 3600)}, "end": {unixStr(promqlT0)}}
	meta := url.Values{"cluster": {promqlClusterA}, "match[]": {"up"}}
	routes := []struct {
		method, path string
		form         url.Values
	}{
		{"GET", "/api/promql/query", q},
		{"POST", "/api/promql/query", q},
		{"GET", "/api/promql/query_range", rq},
		{"POST", "/api/promql/query_range", rq},
		{"GET", "/api/promql/labels", meta},
		{"GET", "/api/promql/label/job/values", meta},
		{"GET", "/api/promql/series", meta},
		{"GET", "/api/promql/history", nil},
		{"DELETE", "/api/promql/history", nil},
	}
	for _, rt := range routes {
		if w := e.do(t, rt.method, rt.path, rt.form, "viewer-1", auth.RoleViewer); w.Code != http.StatusForbidden {
			t.Errorf("viewer %s %s → %d, beklenen 403", rt.method, rt.path, w.Code)
		}
		if w := e.do(t, rt.method, rt.path, rt.form, "", ""); w.Code != http.StatusUnauthorized {
			t.Errorf("kimliksiz %s %s → %d, beklenen 401", rt.method, rt.path, w.Code)
		}
	}
	if n := e.fake.count(); n != 0 {
		t.Fatalf("reddedilen istekler Thanos'a %d çağrı yaptı", n)
	}
	if rows := e.audits(); len(rows) != 0 {
		t.Fatalf("rol reddi handler'a ulaşmamalı (audit %d)", len(rows))
	}
	for _, role := range []string{auth.RoleEditor, auth.RoleAdmin} {
		for _, rt := range routes {
			if w := e.do(t, rt.method, rt.path, rt.form, role+"-1", role); w.Code != http.StatusOK {
				t.Errorf("%s %s %s → %d: %s", role, rt.method, rt.path, w.Code, w.Body.String())
			}
		}
	}

	// Ayarlar: GET her rol, PUT yalnız admin (viewer VE editor 403).
	if w := e.do(t, "GET", "/api/settings/promql-console", nil, "viewer-1", auth.RoleViewer); w.Code != http.StatusOK {
		t.Errorf("viewer ayar GET → %d, beklenen 200", w.Code)
	}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		req := promqlAs(httptest.NewRequest("PUT", "/api/settings/promql-console", strings.NewReader(`{"timeoutS":60}`)), role+"-1", role)
		w := httptest.NewRecorder()
		e.mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s ayar PUT → %d, beklenen 403", role, w.Code)
		}
	}
	// Admin PUT kapıdan geçer (store yok → 503; doğrulama 400 değil).
	req := promqlAs(httptest.NewRequest("PUT", "/api/settings/promql-console", strings.NewReader(`{"timeoutS":60}`)), "admin-1", auth.RoleAdmin)
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("admin ayar PUT (store yok) → %d, beklenen 503", w.Code)
	}
}

// ── query: editor başarı yolu ───────────────────────────────────────────────

func TestPromQLQueryEditorSuccess(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	for _, method := range []string{"POST", "GET"} {
		w := e.do(t, method, "/api/promql/query", url.Values{
			"query": {"up"}, "cluster": {promqlClusterA}, "time": {unixStr(promqlT0)},
		}, "editor-1", auth.RoleEditor)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status %d: %s", method, w.Code, w.Body.String())
		}
		body := decodePromQLBody(t, w)
		if body["status"] != "success" {
			t.Fatalf("status: %v", body)
		}
		data := body["data"].(map[string]any)
		if data["resultType"] != "vector" || len(data["result"].([]any)) != 2 {
			t.Fatalf("data: %v", data)
		}
		// Sonuç VERBATİM: "NaN" dize olarak kalır (sanitizeFloats yok).
		if !strings.Contains(w.Body.String(), `"NaN"`) {
			t.Errorf("NaN örneği korunmadı: %s", w.Body.String())
		}
		m := promqlMeta(t, body)
		if m["clusterId"] != clusterIDOf(promqlClusterA) || m["effectiveQuery"] != "up" ||
			m["series"] != float64(2) || m["totalSeries"] != float64(2) || m["truncated"] != false || m["stepRaised"] != false {
			t.Errorf("meta: %v", m)
		}
		if _, ok := m["durationMs"]; !ok {
			t.Error("meta.durationMs yok")
		}
		if _, ok := m["step"]; ok {
			t.Error("instant meta step taşımamalı")
		}
		up := e.fake.last(t)
		if up.Method != "POST" || up.Path != "/api/v1/query" || up.Form.Get("query") != "up" ||
			up.Form.Get("time") != unixStr(promqlT0) || up.Form.Get("partial_response") != "false" ||
			up.Form.Get("timeout") != "30s" || up.Form.Has("dedup") {
			t.Errorf("upstream isteği (karar 5): %+v", up)
		}
		a, d := e.oneAudit(t)
		if a.TargetID != clusterIDOf(promqlClusterA) || a.ActorID != "editor-1" {
			t.Errorf("audit hedef/aktör: %+v", a)
		}
		if d["status"] != "ok" || d["errorType"] != "" || d["mode"] != "instant" || d["cluster"] != promqlClusterA ||
			d["query"] != "up" || d["series"] != float64(2) || d["truncated"] != false || d["step"] != float64(0) {
			t.Errorf("audit details: %v", d)
		}
		if d["start"] != "2026-09-21T14:13:20.000Z" || d["end"] != d["start"] {
			t.Errorf("instant audit start=end=değerlendirme anı: %v / %v", d["start"], d["end"])
		}
	}
	// time verilmezse sunucu "şimdi"yi AÇIKÇA gönderir.
	before := time.Now().Add(-time.Second).Unix()
	if w := e.do(t, "GET", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("time'sız sorgu %d", w.Code)
	}
	sent, _ := strconv.ParseFloat(e.fake.last(t).Form.Get("time"), 64)
	if int64(sent) < before || int64(sent) > time.Now().Add(time.Second).Unix() {
		t.Errorf("time verilmeyince şimdi gönderilmeli, giden %q", e.fake.last(t).Form.Get("time"))
	}
	// Ad yerine id ile de çözülür (ClusterByRef).
	if w := e.do(t, "GET", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {clusterIDOf(promqlClusterA)}}, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("id ile cluster %d", w.Code)
	}
}

// v0.10.953 — timeoutS ve partialResponse ayarları Thanos'a ULAŞIR.
// TestPromQLQueryEditorSuccess'teki timeout=30s / partial_response=false
// sıfır ConsoleLimits'in normalize değerleriyle aynı: ayar hiç geçmese de
// yeşildi. Burada varsayılandan farklı değerler.
func TestPromQLSettingsReachUpstream(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	cfg := defaultPromQLConsoleSettings()
	cfg.TimeoutS = 7           // geçerli (5–120), thanos 30s varsayılanından farklı
	cfg.PartialResponse = true // sıfır değerden farklı
	withPromQLConsoleSettings(t, cfg)
	for _, tc := range []struct {
		path string
		form url.Values
	}{
		{"/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}, "time": {unixStr(promqlT0)}}},
		{"/api/promql/query_range", url.Values{"query": {"up"}, "cluster": {promqlClusterA}, "start": {unixStr(promqlT0 - 60)}, "end": {unixStr(promqlT0)}}},
	} {
		if w := e.do(t, "POST", tc.path, tc.form, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if up := e.fake.last(t); up.Form.Get("timeout") != "7s" || up.Form.Get("partial_response") != "true" {
			t.Errorf("%s upstream form (karar 5/13): %v", tc.path, up.Form)
		}
	}
	if w := e.do(t, "GET", "/api/promql/labels", url.Values{"cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("labels: %d %s", w.Code, w.Body.String())
	}
	// metadata timeout= göndermez (consoleMetaForm); yalnız partial_response.
	if up := e.fake.last(t); up.Form.Get("partial_response") != "true" {
		t.Errorf("labels upstream form: %v", up.Form)
	}
}

// Karar 12: query/query_range sonuçları önbelleğe GİRMEZ.
func TestPromQLQueryResultsAreNotCached(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	form := url.Values{"query": {"up"}, "cluster": {promqlClusterA}, "time": {unixStr(promqlT0)}}
	for i := 0; i < 3; i++ {
		w := e.do(t, "POST", "/api/promql/query", form, "editor-1", auth.RoleEditor)
		if w.Code != http.StatusOK || w.Header().Get("X-Cache") != "" {
			t.Fatalf("koşu %d: %d X-Cache=%q", i, w.Code, w.Header().Get("X-Cache"))
		}
	}
	if n := e.fake.count(); n != 3 {
		t.Fatalf("3 koşu 3 upstream çağrısı olmalı, %d", n)
	}
	if n := len(e.audits()); n != 3 {
		t.Fatalf("3 koşu 3 audit satırı olmalı, %d", n)
	}
}

// ── query_range: adım bütçesi + aralık korkuluğu (karar 4, 7) ───────────────

func TestPromQLQueryRangeStepMeta(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	const week = int64(7 * 24 * 3600)
	cases := []struct {
		name                 string
		rangeS               int64
		step, points         string
		wantStep             float64
		wantRaised           bool
		wantRequested        float64
		wantUpstreamStepForm string
	}{
		{"otomatik 1h → minStep tabanı 15s", 3600, "", "", 15, false, 0, "15"},
		{"otomatik 6h points=100 → ceil(21600/100)=216s", 6 * 3600, "", "100", 216, false, 0, "216"},
		{"otomatik 7g → ceil(604800/1000)=605s", week, "", "", 605, false, 0, "605"},
		{"elle 90s 1h → aynen", 3600, "90s", "", 90, false, 0, "90"},
		{"elle 1m (Prometheus birimi) → 60s", 3600, "1m", "", 60, false, 0, "60"},
		{"elle 1s 1h → minStep 15s'e yükselir", 3600, "1", "", 15, true, 1, "15"},
		{"elle 1s 7g → 11k tavanı ceil(604800/11000)=55s", week, "1s", "", 55, true, 1, "55"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			form := url.Values{"query": {"rate(x[5m])"}, "cluster": {promqlClusterA},
				"start": {unixStr(promqlT0 - c.rangeS)}, "end": {unixStr(promqlT0)}}
			if c.step != "" {
				form.Set("step", c.step)
			}
			if c.points != "" {
				form.Set("points", c.points)
			}
			w := e.do(t, "POST", "/api/promql/query_range", form, "editor-1", auth.RoleEditor)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			body := decodePromQLBody(t, w)
			m := promqlMeta(t, body)
			if m["step"] != c.wantStep || m["stepRaised"] != c.wantRaised {
				t.Fatalf("meta step=%v raised=%v, beklenen %v/%v", m["step"], m["stepRaised"], c.wantStep, c.wantRaised)
			}
			if c.wantRaised {
				if m["requestedStep"] != c.wantRequested {
					t.Errorf("requestedStep %v", m["requestedStep"])
				}
				ws, _ := body["warnings"].([]any)
				found := false
				for _, x := range ws {
					if strings.Contains(fmt.Sprint(x), "step raised") {
						found = true
					}
				}
				if !found {
					t.Errorf("yükseltme görünür bir uyarı taşımalı: %v", body["warnings"])
				}
			} else if _, ok := m["requestedStep"]; ok {
				t.Errorf("yükseltilmeyen adımda requestedStep olmamalı: %v", m)
			}
			up := e.fake.last(t)
			if up.Path != "/api/v1/query_range" || up.Form.Get("step") != c.wantUpstreamStepForm ||
				up.Form.Get("max_source_resolution") != "auto" || up.Form.Get("partial_response") != "false" {
				t.Errorf("upstream: %+v", up.Form)
			}
			if pts := float64(c.rangeS) / c.wantStep; pts > 11000 {
				t.Errorf("etkin adım 11k noktayı aşıyor: %v", pts)
			}
			_, d := e.oneAudit(t)
			if d["mode"] != "range" || d["step"] != c.wantStep || d["status"] != "ok" {
				t.Errorf("audit: %v", d)
			}
		})
	}
	// Upstream uyarısı zarfta kalır.
	w := e.do(t, "GET", "/api/promql/query_range", url.Values{"query": {"x"}, "cluster": {promqlClusterA},
		"start": {unixStr(promqlT0 - 60)}, "end": {unixStr(promqlT0)}}, "editor-1", auth.RoleEditor)
	if !strings.Contains(w.Body.String(), "upstream note") {
		t.Errorf("Thanos warnings kayboldu: %s", w.Body.String())
	}
}

func TestPromQLQueryRangeGuardrail7d(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	const week = int64(7 * 24 * 3600)
	run := func(rng int64) *httptest.ResponseRecorder {
		return e.do(t, "POST", "/api/promql/query_range", url.Values{"query": {"up"}, "cluster": {promqlClusterA},
			"start": {unixStr(promqlT0 - rng)}, "end": {unixStr(promqlT0)}}, "editor-1", auth.RoleEditor)
	}
	if w := run(week); w.Code != http.StatusOK {
		t.Fatalf("tam 7g geçmeli: %d %s", w.Code, w.Body.String())
	}
	e.audits()
	calls := e.fake.count()

	w := run(week + 1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("7g+1s → %d, beklenen 400", w.Code)
	}
	body := decodePromQLBody(t, w)
	if body["status"] != "error" || body["errorType"] != "guardrail" || body["guardrail"] != "maxRange" ||
		body["limit"] != float64(168) || body["unit"] != "h" {
		t.Fatalf("guardrail gövdesi: %v", body)
	}
	if e.fake.count() != calls {
		t.Fatal("reddedilen aralık Thanos'a gitti — sessiz clamp YOK, sorgu da YOK")
	}
	a, d := e.oneAudit(t)
	if d["status"] != "rejected" || d["errorType"] != "guardrail" || a.TargetID != clusterIDOf(promqlClusterA) {
		t.Errorf("reddin audit satırı: %+v %v", a, d)
	}
	if n := len(e.historyEntries(t, "editor-1")); n != 1 {
		t.Errorf("korkuluk reddi geçmişe girmemeli: %d giriş", n)
	}
}

// ── 400 / 404 / 413: yerel ret ─────────────────────────────────────────────

func TestPromQLBadParams(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	base := func(kv ...string) url.Values {
		v := url.Values{"query": {"up"}, "cluster": {promqlClusterA},
			"start": {unixStr(promqlT0 - 3600)}, "end": {unixStr(promqlT0)}}
		for i := 0; i+1 < len(kv); i += 2 {
			if kv[i+1] == "<del>" {
				v.Del(kv[i])
			} else {
				v.Set(kv[i], kv[i+1])
			}
		}
		return v
	}
	cases := []struct {
		name, path string
		form       url.Values
		wantCode   int
		wantType   string
		wantMsg    string
	}{
		{"query yok", "/api/promql/query", base("query", "<del>"), 400, "bad_data", "query parameter is required"},
		{"query boşluk", "/api/promql/query", base("query", "   "), 400, "bad_data", "query parameter is required"},
		{"query 8192 bayt üstü", "/api/promql/query", base("query", strings.Repeat("a", maxPromQLQueryLen+1)), 400, "bad_data", "8192"},
		{"cluster yok", "/api/promql/query", base("cluster", "<del>"), 400, "bad_data", "cluster parameter is required"},
		{"time bozuk", "/api/promql/query", base("time", "yesterday"), 400, "bad_data", `invalid parameter "time"`},
		{"time NaN", "/api/promql/query", base("time", "NaN"), 400, "bad_data", `invalid parameter "time"`},
		{"start yok", "/api/promql/query_range", base("start", "<del>"), 400, "bad_data", "start parameter is required"},
		{"end yok", "/api/promql/query_range", base("end", "<del>"), 400, "bad_data", "end parameter is required"},
		{"start bozuk", "/api/promql/query_range", base("start", "2026-13-01"), 400, "bad_data", `invalid parameter "start"`},
		{"step bozuk", "/api/promql/query_range", base("step", "5x"), 400, "bad_data", `invalid parameter "step"`},
		{"step sıfır", "/api/promql/query_range", base("step", "0"), 400, "bad_data", "zero or negative"},
		{"step negatif", "/api/promql/query_range", base("step", "-15"), 400, "bad_data", "zero or negative"},
		{"points bozuk", "/api/promql/query_range", base("points", "many"), 400, "bad_data", `invalid parameter "points"`},
		{"end < start", "/api/promql/query_range", base("start", unixStr(promqlT0), "end", unixStr(promqlT0-1)), 400, "bad_data", "before start"},
		{"bilinmeyen cluster", "/api/promql/query", base("cluster", "cluster-zzz"), 404, "not_found", "unknown or disabled cluster"},
		{"kapalı cluster", "/api/promql/query", base("cluster", "cluster-off"), 404, "not_found", "unknown or disabled cluster"},
		{"istek gövdesi 64 KiB üstü", "/api/promql/query", base("query", strings.Repeat("x", 70<<10)), 413, "bad_data", "64 KiB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := e.do(t, "POST", c.path, c.form, "editor-1", auth.RoleEditor)
			if w.Code != c.wantCode {
				t.Fatalf("status %d, beklenen %d: %s", w.Code, c.wantCode, w.Body.String())
			}
			body := decodePromQLBody(t, w)
			if body["status"] != "error" || body["errorType"] != c.wantType || !strings.Contains(fmt.Sprint(body["error"]), c.wantMsg) {
				t.Fatalf("gövde: %v", body)
			}
			_, d := e.oneAudit(t)
			if d["status"] != "rejected" || d["errorType"] != c.wantType {
				t.Errorf("audit: %v", d)
			}
			if q, _ := d["query"].(string); len(q) > maxPromQLQueryLen {
				t.Errorf("audit sorguyu %d bayta kırpmalı, %d", maxPromQLQueryLen, len(q))
			}
		})
	}
	if n := e.fake.count(); n != 0 {
		t.Fatalf("yerel ret Thanos'a %d çağrı yaptı", n)
	}
	if e.hist.upsertCount() != 0 {
		t.Fatal("yerel ret geçmişe yazdı")
	}
	// Doğrulama limiterdan ÖNCE: bozuk istek bütçe yakmaz. Dakikada 1
	// koşuluk bir kullanıcı üç bozuk istekten sonra hâlâ koşabilmeli.
	withPromQLConsoleSettings(t, func() promqlConsoleSettings {
		c := defaultPromQLConsoleSettings()
		c.PerUserPerMin = 1
		return c
	}())
	for i := 0; i < 3; i++ {
		e.do(t, "POST", "/api/promql/query", base("time", "bozuk"), "editor-2", auth.RoleEditor)
	}
	if w := e.do(t, "POST", "/api/promql/query", base(), "editor-2", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("bozuk istekler bütçe yaktı: %d %s", w.Code, w.Body.String())
	}
}

func TestPromQLParseTimeAndDuration(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Time
	}{
		{"1790000000", time.Unix(1790000000, 0).UTC()},
		{"1790000000.1234", time.Unix(1790000000, 123*int64(time.Millisecond)).UTC()},
		{"0", time.Unix(0, 0).UTC()},
		{"2026-09-26T12:00:00Z", time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)},
		{"2026-09-26T15:00:00.5+03:00", time.Date(2026, 9, 26, 12, 0, 0, 500*int(time.Millisecond), time.UTC)},
	} {
		got, err := promqlParseTime(c.in)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("promqlParseTime(%q) = %v, %v; beklenen %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "abc", "NaN", "Inf", "-Inf", "-1", "1e20", "2026-09-26", "12:00"} {
		if _, err := promqlParseTime(bad); err == nil {
			t.Errorf("promqlParseTime(%q) hata vermeliydi", bad)
		}
	}
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"15", 15 * time.Second}, {"0.5", 500 * time.Millisecond}, {"30s", 30 * time.Second},
		{"5m", 5 * time.Minute}, {"1h30m", 90 * time.Minute}, {"1d", 24 * time.Hour},
		{"1w", 7 * 24 * time.Hour}, {"500ms", 500 * time.Millisecond}, {"1m30s", 90 * time.Second},
		{"2h0m5s", 2*time.Hour + 5*time.Second},
	} {
		got, err := promqlParseDuration(c.in)
		if err != nil || got != c.want {
			t.Errorf("promqlParseDuration(%q) = %v, %v; beklenen %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "5x", "1.5m", "m", "s5", "5s1m", "NaN", "Inf", "1e300", "99999999999999999999s", "300000y"} {
		if _, err := promqlParseDuration(bad); err == nil {
			t.Errorf("promqlParseDuration(%q) hata vermeliydi", bad)
		}
	}
}

// ── upstream hataları: 413 / 422 / 429 / 504 / 502 / bad_data ─────────────

func TestPromQLUpstreamBodyCap413(t *testing.T) {
	var big strings.Builder
	big.WriteString(`{"status":"success","data":{"resultType":"vector","result":[`)
	for i := 0; i < 20000; i++ {
		if i > 0 {
			big.WriteByte(',')
		}
		fmt.Fprintf(&big, `{"metric":{"__name__":"up","pod":"pod-%06d"},"value":[1790000000,"1"]}`, i)
	}
	big.WriteString(`]}}`)
	if big.Len() <= 1<<20 {
		t.Fatalf("gövde 1 MiB'ı aşmalı: %d", big.Len())
	}
	e := newPromQLTestEnv(t, func(w http.ResponseWriter, _ *http.Request, _ promqlFakeReq) {
		fmt.Fprint(w, big.String())
	})
	cfg := defaultPromQLConsoleSettings()
	cfg.MaxBodyMiB = 1
	withPromQLConsoleSettings(t, cfg)

	w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, beklenen 413: %s", w.Code, w.Body.String())
	}
	body := decodePromQLBody(t, w)
	if body["errorType"] != "response_too_large" || !strings.Contains(fmt.Sprint(body["error"]), "1 MiB") ||
		!strings.Contains(fmt.Sprint(body["error"]), "larger step") {
		t.Fatalf("gövde: %v", body)
	}
	assertNoEndpointLeak(t, w.Body.String(), e.fake.URL)
	_, d := e.oneAudit(t)
	if d["status"] != "error" || d["errorType"] != "response_too_large" {
		t.Errorf("audit: %v", d)
	}
	if h := e.historyEntries(t, "editor-1"); len(h) != 1 || h[0].Status != "error" || h[0].ErrorType != "response_too_large" {
		t.Errorf("koşan-ama-düşen sorgu geçmişe error olarak girmeli: %+v", h)
	}
}

func TestPromQLSeriesCapTruncatedMeta(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"status":"success","data":{"resultType":"vector","result":[`)
	for i := 0; i < 12; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"metric":{"i":"%d"},"value":[1790000000,"1"]}`, i)
	}
	b.WriteString(`]}}`)
	e := newPromQLTestEnv(t, func(w http.ResponseWriter, _ *http.Request, _ promqlFakeReq) { fmt.Fprint(w, b.String()) })
	cfg := defaultPromQLConsoleSettings()
	cfg.MaxSeries = 10
	withPromQLConsoleSettings(t, cfg)
	w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"x"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor)
	body := decodePromQLBody(t, w)
	m := promqlMeta(t, body)
	if m["series"] != float64(10) || m["totalSeries"] != float64(12) || m["truncated"] != true {
		t.Fatalf("meta: %v", m)
	}
	if n := len(body["data"].(map[string]any)["result"].([]any)); n != 10 {
		t.Fatalf("result %d seri, beklenen 10", n)
	}
	_, d := e.oneAudit(t)
	if d["series"] != float64(10) || d["truncated"] != true {
		t.Errorf("audit: %v", d)
	}
}

func TestPromQLUpstreamErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		code       int
		body       string
		wantCode   int
		wantType   string
		wantPos    string
		wantErrSub string
	}{
		{"bad_data konumu aynen geçer", 400,
			`{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": 1:5: parse error: unexpected character: '!'"}`,
			400, "bad_data", "1:5", "parse error"},
		{"execution → 422", 422,
			`{"status":"error","errorType":"execution","error":"many-to-many matching not allowed"}`,
			422, "execution", "", "many-to-many"},
		{"Thanos timeout → 504", 503,
			`{"status":"error","errorType":"timeout","error":"query timed out in expression evaluation"}`,
			504, "timeout", "", "timed out"},
		{"JSON'suz 5xx → 502 unavailable", 502, `<html>bad gateway</html>`, 502, "unavailable", "", "unavailable"},
		{"401 → 502 kimlik reddi", 401, ``, 502, "unavailable", "", "credentials"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newPromQLTestEnv(t, func(w http.ResponseWriter, _ *http.Request, _ promqlFakeReq) {
				w.WriteHeader(c.code)
				fmt.Fprint(w, c.body)
			})
			w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"up !"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor)
			if w.Code != c.wantCode {
				t.Fatalf("status %d, beklenen %d: %s", w.Code, c.wantCode, w.Body.String())
			}
			body := decodePromQLBody(t, w)
			if body["status"] != "error" || body["errorType"] != c.wantType || !strings.Contains(fmt.Sprint(body["error"]), c.wantErrSub) {
				t.Fatalf("gövde: %v", body)
			}
			if pos, _ := body["position"].(string); pos != c.wantPos {
				t.Errorf("position %q, beklenen %q", pos, c.wantPos)
			}
			m := promqlMeta(t, body)
			if m["effectiveQuery"] != "up !" || m["clusterId"] != clusterIDOf(promqlClusterA) {
				t.Errorf("koşan sorgunun hatası meta taşımalı: %v", m)
			}
			assertNoEndpointLeak(t, w.Body.String(), e.fake.URL)
			_, d := e.oneAudit(t)
			if d["status"] != "error" || d["errorType"] != c.wantType {
				t.Errorf("audit: %v", d)
			}
		})
	}
}

func TestPromQLUnreachable502NoEndpointLeak(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	dead := httptest.NewServer(http.NotFoundHandler())
	endpoint := dead.URL
	dead.Close() // bağlantı reddedilir
	e.s.thanos.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{{Name: promqlClusterA, URL: endpoint, Enabled: true}}})

	for _, path := range []string{"/api/promql/query", "/api/promql/labels"} {
		form := url.Values{"query": {"up"}, "cluster": {promqlClusterA}}
		method := "POST"
		if path == "/api/promql/labels" {
			form, method = url.Values{"cluster": {promqlClusterA}}, "GET"
		}
		w := e.do(t, method, path, form, "editor-1", auth.RoleEditor)
		if w.Code != http.StatusBadGateway {
			t.Fatalf("%s status %d, beklenen 502: %s", path, w.Code, w.Body.String())
		}
		body := decodePromQLBody(t, w)
		if body["errorType"] != "unavailable" || !strings.Contains(fmt.Sprint(body["error"]), "cluster-a") {
			t.Fatalf("%s gövde: %v", path, body)
		}
		assertNoEndpointLeak(t, w.Body.String(), endpoint)
	}
	a, d := e.oneAudit(t) // yalnız query audit'lenir; labels değil
	if d["errorType"] != "unavailable" || strings.Contains(a.Details, endpoint) {
		t.Errorf("audit: %v", a.Details)
	}
}

// 504 burada ÇAĞIRANIN istek-bağlamı son tarihinden (150 ms) gelir;
// timeoutS ayarının sürdüğü son tarih internal/thanos TestConsoleTimeout ve
// TestPromQLSettingsReachUpstream'deki timeout=7s form kontrolüyle örtülü.
func TestPromQLTimeout504(t *testing.T) {
	e := newPromQLTestEnv(t, func(w http.ResponseWriter, r *http.Request, _ promqlFakeReq) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	began := time.Now()
	w := e.doCtx(t, ctx, "POST", "/api/promql/query_range", url.Values{"query": {"up"}, "cluster": {promqlClusterA},
		"start": {unixStr(promqlT0 - 60)}, "end": {unixStr(promqlT0)}}, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status %d, beklenen 504: %s", w.Code, w.Body.String())
	}
	if time.Since(began) > 3*time.Second {
		t.Fatal("son tarih uygulanmadı")
	}
	body := decodePromQLBody(t, w)
	if body["errorType"] != "timeout" {
		t.Fatalf("gövde: %v", body)
	}
	assertNoEndpointLeak(t, w.Body.String(), e.fake.URL)
	_, d := e.oneAudit(t)
	if d["status"] != "error" || d["errorType"] != "timeout" {
		t.Errorf("audit: %v", d)
	}
	// Geçmiş yazımı isteğin son tarihine bağlı değil (WithoutCancel).
	if h := e.historyEntries(t, "editor-1"); len(h) != 1 || h[0].ErrorType != "timeout" {
		t.Errorf("zaman aşımına uğrayan koşu geçmişe girmeli: %+v", h)
	}
}

func TestPromQLCanceled499(t *testing.T) {
	started := make(chan struct{}, 1)
	e := newPromQLTestEnv(t, func(w http.ResponseWriter, r *http.Request, _ promqlFakeReq) {
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	w := e.doCtx(t, ctx, "POST", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor)
	if w.Code != statusClientClosedRequest || w.Body.Len() != 0 {
		t.Fatalf("iptal → 499 gövdesiz, %d %q", w.Code, w.Body.String())
	}
	_, d := e.oneAudit(t)
	if d["status"] != "error" || d["errorType"] != "canceled" {
		t.Errorf("audit: %v", d)
	}
	if e.hist.upsertCount() != 0 {
		t.Error("iptal edilen koşu geçmişe girmemeli")
	}
}

// v0.10.953 — Thanos'un KENDİ "canceled" cevabı (HTTP 499) bizim
// istemcimizin vazgeçmesi değildir: 502 unavailable JSON gövdesi + geçmişe
// error olarak girer (gövdesiz 499 + geçmişten düşme DEĞİL).
// TestPromQLCanceled499 (bizim bağlamımız iptal) değişmeden geçer.
func TestPromQLUpstreamCanceledIsUnavailable(t *testing.T) {
	e := newPromQLTestEnv(t, func(w http.ResponseWriter, _ *http.Request, _ promqlFakeReq) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(499)
		fmt.Fprint(w, `{"status":"error","errorType":"canceled","error":"query was canceled in expression evaluation"}`)
	})
	w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status %d, beklenen 502: %q", w.Code, w.Body.String())
	}
	body := decodePromQLBody(t, w)
	if body["errorType"] != "unavailable" || !strings.Contains(fmt.Sprint(body["error"]), "canceled") {
		t.Fatalf("gövde: %v", body)
	}
	_, d := e.oneAudit(t)
	if d["status"] != "error" || d["errorType"] != "unavailable" {
		t.Errorf("audit: %v", d)
	}
	if h := e.historyEntries(t, "editor-1"); len(h) != 1 || h[0].Status != "error" || h[0].ErrorType != "unavailable" {
		t.Errorf("upstream iptali geçmişe error olarak girmeli: %+v", h)
	}
}

// ── 429: eşzamanlılık + dakika bütçesi (karar 3) ───────────────────────────

func TestPromQLConcurrency429(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	e := newPromQLTestEnv(t, func(w http.ResponseWriter, r *http.Request, req promqlFakeReq) {
		if req.Form.Get("query") == "slow" {
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		promqlDefaultThanos(w, r, req)
	})
	cfg := defaultPromQLConsoleSettings()
	cfg.PerUserConcurrency = 1
	withPromQLConsoleSettings(t, cfg)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- e.do(t, "POST", "/api/promql/query", url.Values{"query": {"slow"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor)
	}()
	<-started
	w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("ikinci eşzamanlı koşu → 429 + Retry-After 1, %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	body := decodePromQLBody(t, w)
	if body["errorType"] != "rate_limited" || body["guardrail"] != "perUserConcurrency" || body["limit"] != float64(1) {
		t.Fatalf("gövde: %v", body)
	}
	// Başka kullanıcı etkilenmez.
	if w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}}, "editor-2", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("editor-2 → %d", w.Code)
	}
	close(release)
	if first := <-done; first.Code != http.StatusOK {
		t.Fatalf("ilk koşu %d", first.Code)
	}
	var rejected, ok int
	for _, a := range e.audits() {
		var d map[string]any
		_ = json.Unmarshal([]byte(a.Details), &d)
		switch d["status"] {
		case "rejected":
			rejected++
			if d["errorType"] != "rate_limited" {
				t.Errorf("ret errorType: %v", d)
			}
		case "ok":
			ok++
		}
	}
	if rejected != 1 || ok != 2 {
		t.Errorf("audit: %d rejected / %d ok, beklenen 1/2", rejected, ok)
	}
	// Slot boşaldı: aynı kullanıcı yeniden koşabilir.
	if w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("release sonrası %d", w.Code)
	}
	for _, h := range e.historyEntries(t, "editor-1") {
		if h.Query == "up" && h.Status != "ok" {
			t.Errorf("429 geçmişe girmemeli: %+v", h)
		}
	}
}

func TestPromQLPerMinute429RetryAfter(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	cfg := defaultPromQLConsoleSettings()
	cfg.PerUserPerMin = 2
	withPromQLConsoleSettings(t, cfg)
	form := url.Values{"query": {"up"}, "cluster": {promqlClusterA}}
	for i := 0; i < 2; i++ {
		if w := e.do(t, "POST", "/api/promql/query", form, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
			t.Fatalf("koşu %d: %d", i, w.Code)
		}
	}
	w := e.do(t, "POST", "/api/promql/query", form, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "50" {
		t.Fatalf("3. koşu → 429 + Retry-After 50 (sahte saat :10), %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	if body := decodePromQLBody(t, w); body["guardrail"] != "perUserPerMin" || body["retryAfterS"] != float64(50) {
		t.Fatalf("gövde: %v", body)
	}
	e.clk.set(time.Date(2026, 9, 26, 12, 1, 0, 0, time.UTC))
	if w := e.do(t, "POST", "/api/promql/query", form, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("pencere sıfırlandı, %d", w.Code)
	}
}

// ── paylaşımlı querier: effectiveQuery + konum geri çevirisi ───────────────

func TestPromQLSharedQuerierEffectiveQueryAndPosition(t *testing.T) {
	e := newPromQLTestEnv(t, func(w http.ResponseWriter, r *http.Request, req promqlFakeReq) {
		q := req.Form.Get("query")
		if i := strings.Index(q, "BAD"); i >= 0 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": 1:%d: parse error: unexpected identifier \"BAD\""}`, i+1)
			return
		}
		promqlDefaultThanos(w, r, req)
	})
	w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"sum(rate(http_requests_total[5m])) # it's a note"},
		"cluster": {promqlClusterB}}, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	m := promqlMeta(t, decodePromQLBody(t, w))
	eff, _ := m["effectiveQuery"].(string)
	if eff != e.fake.last(t).Form.Get("query") || !strings.Contains(eff, `cluster="cluster-b"`) || strings.Contains(eff, "#") {
		t.Fatalf("meta.effectiveQuery Thanos'a giden ifade olmalı (yorum silinmiş + matcher): %q / giden %q", eff, e.fake.last(t).Form.Get("query"))
	}
	if m["clusterId"] != clusterIDOf(promqlClusterB) {
		t.Errorf("clusterId %v", m["clusterId"])
	}
	e.audits()

	// Konum: Thanos etkin sorgudaki sütunu söyler; position kullanıcınınki.
	w = e.do(t, "POST", "/api/promql/query", url.Values{"query": {"up + BAD"}, "cluster": {promqlClusterB}}, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
	body := decodePromQLBody(t, w)
	if body["position"] != "1:6" {
		t.Fatalf("position kullanıcının sorgusunda 1:6 olmalı: %v", body)
	}
	effBad := promqlMeta(t, body)["effectiveQuery"].(string)
	if !strings.Contains(fmt.Sprint(body["error"]), fmt.Sprintf("1:%d", strings.Index(effBad, "BAD")+1)) {
		t.Errorf("mesaj etkin konumu taşır: %v", body["error"])
	}
}

// ── Metadata: önbellek, bütçe, pencere, enjeksiyon (karar 3, 9) ────────────

func TestPromQLMetadataCacheHitSkipsUpstreamAndBudget(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	cfg := defaultPromQLConsoleSettings()
	cfg.MetaPerUserPerMin = 1
	withPromQLConsoleSettings(t, cfg)
	form := url.Values{"cluster": {promqlClusterA}, "match[]": {"up"}, "start": {unixStr(promqlT0 - 600)}, "end": {unixStr(promqlT0)}}

	w := e.do(t, "GET", "/api/promql/labels", form, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusOK || w.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("ilk çağrı %d X-Cache=%q: %s", w.Code, w.Header().Get("X-Cache"), w.Body.String())
	}
	w = e.do(t, "GET", "/api/promql/labels", form, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("X-Cache"), "HIT") {
		t.Fatalf("aynı çağrı isabet olmalı ve bütçe (1/dk) ONU saymamalı: %d X-Cache=%q %s", w.Code, w.Header().Get("X-Cache"), w.Body.String())
	}
	if n := e.fake.count(); n != 1 {
		t.Fatalf("isabet upstream'e gitti: %d çağrı", n)
	}
	// Farklı anahtar (label values) bütçeyi aşar → 429.
	w = e.do(t, "GET", "/api/promql/label/job/values", form, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("bütçe dolu → 429 + Retry-After, %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	if body := decodePromQLBody(t, w); body["guardrail"] != "metaPerUserPerMin" {
		t.Fatalf("gövde: %v", body)
	}
	if n := e.fake.count(); n != 1 {
		t.Fatalf("reddedilen metadata upstream'e gitti: %d", n)
	}
	// Metadata bütçesi sorgu bütçesinden ayrı: sorgu hâlâ koşar.
	if w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("sorgu bütçesi metadata'dan etkilenmemeli: %d", w.Code)
	}
	rows := e.audits()
	if len(rows) != 1 || rows[0].Action != "promql.query" {
		t.Fatalf("metadata audit yazmamalı; yalnız sorgu satırı beklenir: %+v", rows)
	}
}

// Bütçe yalnız ÖN PLAN isteğinden düşer: SWR arka plan tazelemesi (cache.go
// refreshKey kendi bağlamını kurar) kimsenin metadata bütçesini yakmaz.
// v0.10.953 — handler'ın GERÇEK kapanışı STALE dalı + refreshKey üzerinden
// sürülür (önceki sürüm kapanışın bir KOPYASINI çağırıyordu; servePromQLMeta
// koşulsuz ücret alsa da geçerdi).
func TestPromQLMetadataChargeOnlyForegroundRequests(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	mc := &seamMemCache{} // cache_seam_test.go: gerçek L2, STALE mümkün
	e.s.cache = mc
	cfg := defaultPromQLConsoleSettings()
	cfg.MetaPerUserPerMin = 1
	withPromQLConsoleSettings(t, cfg)
	form := url.Values{"cluster": {promqlClusterA}, "match[]": {"up"}, "start": {unixStr(promqlT0 - 600)}, "end": {unixStr(promqlT0)}}

	// editor-2 yuvayı doldurur (editor-2'nin bütçesinden).
	if w := e.do(t, "GET", "/api/promql/labels", form, "editor-2", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("seed: %d", w.Code)
	}
	var key string
	waitFor(t, "async L2 set", func() bool {
		mc.mu.Lock()
		defer mc.mu.Unlock()
		for k := range mc.m {
			key = k
		}
		return key != ""
	})
	// Zarfı yaşlandır: TTL geçmiş ama TTL×staleFactor içinde → sonraki okuma STALE.
	raw, _, _ := mc.Get(context.Background(), key)
	_, body, ok := unwrapEnvelope(raw)
	if !ok {
		t.Fatalf("L2 zarfı çözülemedi: %s", raw)
	}
	env, _ := json.Marshal(cacheEnvelope{Written: time.Now().Add(-2 * promqlMetaCacheTTL).UnixNano(), Body: body})
	mc.mu.Lock()
	mc.m[key] = env
	mc.mu.Unlock()
	e.s.l1.del(key)

	// editor-1 bayat isabet alır; handler'ın GERÇEK kapanışı arka planda refreshKey ile koşar.
	w := e.do(t, "GET", "/api/promql/labels", form, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusOK || w.Header().Get("X-Cache") != "STALE" {
		t.Fatalf("stale: %d %q", w.Code, w.Header().Get("X-Cache"))
	}
	waitFor(t, "background refresh", func() bool { return e.fake.count() == 2 })
	waitFor(t, "refreshed L2", func() bool {
		r, _, _ := mc.Get(context.Background(), key)
		wr, _, ok := unwrapEnvelope(r)
		return ok && time.Since(wr) < promqlMetaCacheTTL
	})
	// Tazeleme kimseden ücret almadı: editor-1'in yeni anahtardaki TEK ön plan isteği geçer.
	if w := e.do(t, "GET", "/api/promql/label/job/values", form, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("arka plan tazelemesi editor-1'in metadata bütçesini yaktı: %d %s", w.Code, w.Body.String())
	}
	if n := len(promqlLimits.metaInflight); n != 0 {
		t.Fatalf("tazeleme / ön plan slot sızdırdı: %v", promqlLimits.metaInflight)
	}
}

// v0.10.953 — metadata eşzamanlılık kapısı UÇTAN UCA: slot upstream
// çağrısı boyunca tutulur (varsayılan kapı 4); 5. farklı-anahtarlı ıska 429
// metaPerUserConcurrency ve upstream'e GİTMEZ; başka kullanıcı ve query
// kapısı etkilenmez; slotlar bırakılınca yeniden alınır.
func TestPromQLMetadataConcurrency429(t *testing.T) {
	release := make(chan struct{})
	got := make(chan struct{}, 8)
	e := newPromQLTestEnv(t, func(w http.ResponseWriter, r *http.Request, req promqlFakeReq) {
		if strings.HasPrefix(req.Path, "/api/v1/label/held") { // yalnız tutulanlar bekler: kapısız 5. hızlı döner
			got <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		promqlDefaultThanos(w, r, req)
	})
	var relOnce sync.Once
	unblock := func() { relOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock) // başarısızlıkta da tutulanlar bırakılsın (sunucu Close'u beklemesin)
	cap4 := promqlMetaConcurrency(defaultPromQLConsoleSettings())
	form := url.Values{"cluster": {promqlClusterA}, "start": {unixStr(promqlT0 - 600)}, "end": {unixStr(promqlT0)}}
	done := make(chan *httptest.ResponseRecorder, cap4)
	for i := 0; i < cap4; i++ {
		path := fmt.Sprintf("/api/promql/label/held%d/values", i) // geçerli ad: U__ kaçışı yok // farklı anahtar: singleflight birleştirmesin
		go func() { done <- e.do(t, "GET", path, form, "editor-1", auth.RoleEditor) }()
	}
	for i := 0; i < cap4; i++ {
		select {
		case <-got:
		case <-time.After(3 * time.Second):
			t.Fatalf("%d. upstream çağrısı başlamadı", i+1)
		}
	}
	w := e.do(t, "GET", "/api/promql/label/l-extra/values", form, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("5. eşzamanlı metadata → 429 + Retry-After 1, %d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
	if body := decodePromQLBody(t, w); body["guardrail"] != "metaPerUserConcurrency" || body["limit"] != float64(cap4) {
		t.Fatalf("gövde: %v", body)
	}
	if n := e.fake.count(); n != cap4 {
		t.Fatalf("reddedilen metadata upstream'e gitti: %d çağrı", n)
	}
	if w := e.do(t, "GET", "/api/promql/labels", form, "editor-2", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("editor-2 etkilenmemeli: %d", w.Code)
	}
	if w := e.do(t, "POST", "/api/promql/query", url.Values{"query": {"up"}, "cluster": {promqlClusterA}}, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("meta slotları query kapısını etkilememeli: %d", w.Code)
	}
	unblock()
	for i := 0; i < cap4; i++ {
		if r := <-done; r.Code != http.StatusOK {
			t.Fatalf("tutulan metadata %d", r.Code)
		}
	}
	if w := e.do(t, "GET", "/api/promql/label/l-extra/values", form, "editor-1", auth.RoleEditor); w.Code != http.StatusOK {
		t.Fatalf("slotlar bırakıldıktan sonra %d", w.Code)
	}
	if n := len(promqlLimits.metaInflight); n != 0 {
		t.Fatalf("metaInflight boşalmalı: %v", promqlLimits.metaInflight)
	}
}

// v0.10.953 — singleflight paylaşılan hata yeniden deneme kuralı (SAF).
func TestPromQLMetaShouldRetry(t *testing.T) {
	guard := &promqlGuardError{Status: 429, ErrorType: "rate_limited", Guardrail: "metaPerUserPerMin", RetryAfter: time.Second, Msg: "x"}
	leaderCanceled := &thanos.ConsoleError{Type: thanos.ConsoleErrCanceled, Message: "request canceled"}
	cases := []struct {
		name   string
		err    error
		ran    bool
		ownCtx error
		want   bool
	}{
		{"başkasının 429'u, kapanış koşmadı", guard, false, nil, true},
		{"kendi 429'u (kapanış koştu)", guard, true, nil, false},
		{"sarılı 429, koşmadı", fmt.Errorf("wrap: %w", guard), false, nil, true},
		{"liderin iptali (ConsoleError canceled), koşmadı", leaderCanceled, false, nil, true},
		{"liderin iptali (sarılı context.Canceled), koşmadı", fmt.Errorf("wrap: %w", context.Canceled), false, nil, true},
		{"kendi iptali (kapanış koştu)", leaderCanceled, true, nil, false},
		{"kendi bağlamı bitti → deneme yok", guard, false, context.Canceled, false},
		{"kendi bağlamı bitti, lider iptali", leaderCanceled, false, context.Canceled, false},
		{"paylaşılan timeout yeniden denenmez", &thanos.ConsoleError{Type: thanos.ConsoleErrTimeout, Message: "t"}, false, nil, false},
		{"paylaşılan upstream hatası yeniden denenmez", &thanos.ConsoleError{Type: thanos.ConsoleErrUnavailable, Message: "u"}, false, nil, false},
		{"düz hata", errors.New("boom"), false, nil, false},
		{"nil", nil, false, nil, false},
	}
	for _, c := range cases {
		if got := promqlMetaShouldRetry(c.err, c.ran, c.ownCtx); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}

// v0.10.953 — başka bir çağıranın 429'u singleflight ile paylaşılırsa bu
// istek KENDİ bütçesiyle bir kez daha dener (çağrı yerindeki kablolama:
// "if false && …" bu testi kırar).
func TestPromQLMetadataSharedGuardErrorRetriedWithOwnBudget(t *testing.T) {
	e := newPromQLTestEnv(t, nil)
	cfg := defaultPromQLConsoleSettings()
	form := url.Values{"cluster": {promqlClusterA}, "match[]": {"up"}, "start": {unixStr(promqlT0 - 600)}, "end": {unixStr(promqlT0)}}
	c, _ := e.s.thanos.ClusterByRef(promqlClusterA)
	start, end, _ := promqlMetaWindow(time.Unix(promqlT0-600, 0), time.Unix(promqlT0, 0), time.Now(), cfg)
	key := promqlMetaKeyFor(c, promqlMetaKindLabels, "", []string{"up"}, start, end, promqlMetaLimit(0), cfg)

	started, release := make(chan struct{}), make(chan struct{})
	sharedCh := make(chan bool, 1)
	go func() {
		_, _, shared := e.s.sf.Do(key, func() (any, error) {
			close(started)
			<-release
			return nil, &promqlGuardError{Status: 429, ErrorType: "rate_limited", Guardrail: "metaPerUserPerMin", RetryAfter: time.Second, Msg: "x"}
		})
		sharedCh <- shared
	}()
	<-started
	doneB := make(chan *httptest.ResponseRecorder, 1)
	go func() { doneB <- e.do(t, "GET", "/api/promql/labels", form, "editor-2", auth.RoleEditor) }()
	time.Sleep(50 * time.Millisecond)
	select {
	case w := <-doneB:
		t.Fatalf("B yuvaya katılmadı — anahtar uyuşmazlığı (%d)", w.Code)
	default:
	}
	close(release)
	if !<-sharedCh {
		t.Fatal("B yuvayı paylaşmadı: test hiçbir şey kanıtlamıyor")
	}
	w := <-doneB
	if w.Code != http.StatusOK {
		t.Fatalf("paylaşılan 429 B'ye sızdı: %d %s", w.Code, w.Body.String())
	}
	if n := e.fake.count(); n != 1 {
		t.Fatalf("B'nin kendi denemesi upstream'e bir kez gitmeli: %d", n)
	}
}

// v0.10.953 — liderin istemcisi giderse (otomatik tamamlama tuş vuruşunda
// vazgeçti) yuvayı paylaşan canlı takipçi gövdesiz 499 ALMAZ: kendi
// bağlamıyla yeniden dener.
func TestPromQLMetadataLeaderCancelDoesNotLeakToFollower(t *testing.T) {
	got := make(chan struct{}, 4)
	release := make(chan struct{})
	e := newPromQLTestEnv(t, func(w http.ResponseWriter, r *http.Request, req promqlFakeReq) {
		got <- struct{}{}
		select {
		case <-r.Context().Done():
			return
		case <-release:
		}
		promqlDefaultThanos(w, r, req)
	})
	// Açık pencere: A ile B 30 s ızgara sınırına denk gelse de aynı anahtar.
	form := url.Values{"cluster": {promqlClusterA}, "start": {unixStr(promqlT0 - 600)}, "end": {unixStr(promqlT0)}}
	const path = "/api/promql/label/__name__/values"

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	doneA := make(chan *httptest.ResponseRecorder, 1)
	go func() { doneA <- e.doCtx(t, ctxA, "GET", path, form, "editor-1", auth.RoleEditor) }()
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("A'nın upstream çağrısı başlamadı")
	}
	doneB := make(chan *httptest.ResponseRecorder, 1)
	go func() { doneB <- e.do(t, "GET", path, form, "editor-2", auth.RoleEditor) }()
	time.Sleep(50 * time.Millisecond)
	select {
	case w := <-doneB:
		t.Fatalf("B yuvaya katılmadı (%d)", w.Code)
	default:
	}
	if n := e.fake.count(); n != 1 {
		t.Fatalf("B yuvaya katılmalıydı, kendi çağrısını yapmamalı: %d", n)
	}
	cancelA()
	a := <-doneA
	close(release)
	b := <-doneB
	if a.Code != statusClientClosedRequest || a.Body.Len() != 0 {
		t.Fatalf("A (iptal) → 499 gövdesiz, %d %q", a.Code, a.Body.String())
	}
	if b.Code != http.StatusOK {
		t.Fatalf("liderin iptali canlı B'ye sızdı: %d %q", b.Code, b.Body.String())
	}
	if data, _ := decodePromQLBody(t, b)["data"].([]any); len(data) == 0 {
		t.Fatalf("B veri taşımalı: %s", b.Body.String())
	}
	if n := e.fake.count(); n != 2 {
		t.Fatalf("B'nin kendi denemesi ikinci upstream çağrısı olmalı: %d", n)
	}
}

// v0.10.953 — önbelleğe giren metadata değeri ~4 MiB ile sınırlı (L1 bayt
// tavansız, Redis 180 s). Upstream gövde tavanı (maxBodyMiB) DEĞİŞMEDİ:
// limit'i yok sayan bir Thanos'ta sunucu kesimi hâlâ çalışır (413 değil).
func TestPromQLMetadataCachedBytesCapped(t *testing.T) {
	stringList := func(n int, val func(i int) string) string {
		var b strings.Builder
		b.WriteString(`{"status":"success","data":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('"')
			b.WriteString(val(i))
			b.WriteByte('"')
		}
		b.WriteString(`]}`)
		return b.String()
	}
	form := url.Values{"cluster": {promqlClusterA}, "start": {unixStr(promqlT0 - 600)}, "end": {unixStr(promqlT0)}}

	t.Run("1000 × 10 KB değer → kırpılır, truncated", func(t *testing.T) {
		big := stringList(1000, func(i int) string { return fmt.Sprintf("%010d", i) + strings.Repeat("v", 10<<10-10) })
		e := newPromQLTestEnv(t, func(w http.ResponseWriter, _ *http.Request, _ promqlFakeReq) { fmt.Fprint(w, big) })
		w := e.do(t, "GET", "/api/promql/label/big/values", form, "editor-1", auth.RoleEditor)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %.300s", w.Code, w.Body.String())
		}
		if n := w.Body.Len(); n > 4300000 {
			t.Fatalf("gövde %d bayt, ~4.1 MiB altında olmalı", n)
		}
		body := decodePromQLBody(t, w)
		data, _ := body["data"].([]any)
		m := promqlMeta(t, body)
		if len(data) == 0 || len(data) >= 1000 || m["truncated"] != true || m["total"] != float64(1000) {
			t.Fatalf("kırpma: %d değer, meta %v", len(data), m)
		}
	})
	t.Run("60k kısa değer (limit yok sayılır, >4 MiB upstream) → 200, 1000 değer", func(t *testing.T) {
		huge := stringList(60000, func(i int) string { return fmt.Sprintf("value-%066d", i) })
		if len(huge) <= promqlMetaMaxCachedBytes {
			t.Fatalf("upstream gövdesi 4 MiB'ı aşmalı: %d", len(huge))
		}
		e := newPromQLTestEnv(t, func(w http.ResponseWriter, _ *http.Request, _ promqlFakeReq) { fmt.Fprint(w, huge) })
		w := e.do(t, "GET", "/api/promql/label/__name__/values", form, "editor-1", auth.RoleEditor)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d (413 değil 200 beklenir): %.300s", w.Code, w.Body.String())
		}
		body := decodePromQLBody(t, w)
		m := promqlMeta(t, body)
		if data, _ := body["data"].([]any); len(data) != 1000 || m["truncated"] != true || m["total"] != float64(60000) {
			t.Fatalf("sunucu limiti: %d değer, meta %v", len(data), m)
		}
	})
}

func TestPromQLMetadataParamsAndInjection(t *testing.T) {
	e := newPromQLTestEnv(t, nil)

	// Paylaşımlı querier, match[] yok → sentez; pencere yok → son 1h.
	w := e.do(t, "GET", "/api/promql/labels", url.Values{"cluster": {promqlClusterB}}, "editor-1", auth.RoleEditor)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	up := e.fake.last(t)
	if got := up.Form["match[]"]; len(got) != 1 || got[0] != `{cluster="cluster-b"}` {
		t.Fatalf("paylaşımlı querier'da match[] sentezlenmeli: %v", got)
	}
	s, _ := strconv.ParseFloat(up.Form.Get("start"), 64)
	en, _ := strconv.ParseFloat(up.Form.Get("end"), 64)
	if d := en - s; d < 3600-31 || d > 3600+31 {
		t.Fatalf("varsayılan pencere ~1h olmalı: start=%v end=%v", s, en)
	}
	body := decodePromQLBody(t, w)
	m := promqlMeta(t, body)
	if m["clusterId"] != clusterIDOf(promqlClusterB) || fmt.Sprint(m["effectiveMatch"]) != `[{cluster="cluster-b"}]` || m["limit"] != float64(1000) {
		t.Fatalf("meta: %v", m)
	}
	if _, ok := m["durationMs"]; ok {
		t.Error("önbellekli meta durationMs taşımamalı (isabette bayat olurdu)")
	}

	// Kullanıcı match[]'ine AYNI matcher enjekte edilir.
	e.do(t, "GET", "/api/promql/series", url.Values{"cluster": {promqlClusterB}, "match[]": {"up", `http_requests_total{job="a"}`}}, "editor-1", auth.RoleEditor)
	if got := e.fake.last(t).Form["match[]"]; len(got) != 2 || !strings.Contains(got[0], `cluster="cluster-b"`) || !strings.Contains(got[1], `cluster="cluster-b"`) {
		t.Fatalf("her match[] enjekte edilmeli: %v", got)
	}

	// Limit sunucuda: 3 değer, limit=2 → 2 değer + truncated.
	w = e.do(t, "GET", "/api/promql/label/job/values", url.Values{"cluster": {promqlClusterA}, "limit": {"2"}}, "editor-1", auth.RoleEditor)
	body = decodePromQLBody(t, w)
	if data := body["data"].([]any); len(data) != 2 {
		t.Fatalf("limit uygulanmadı: %v", data)
	}
	if m := promqlMeta(t, body); m["truncated"] != true || m["total"] != float64(3) || m["limit"] != float64(2) {
		t.Fatalf("meta: %v", m)
	}
	if up := e.fake.last(t); up.Method != "GET" || up.Path != "/api/v1/label/job/values" || up.Form.Get("limit") != "3" {
		t.Fatalf("label values upstream: %+v", up)
	}

	// Pencere maxRange'e KELEPÇELENİR (ret değil).
	w = e.do(t, "GET", "/api/promql/labels", url.Values{"cluster": {promqlClusterA},
		"start": {unixStr(promqlT0 - 30*24*3600)}, "end": {unixStr(promqlT0)}}, "editor-1", auth.RoleEditor)
	m = promqlMeta(t, decodePromQLBody(t, w))
	if w.Code != http.StatusOK || m["end"].(float64)-m["start"].(float64) != 7*24*3600 {
		t.Fatalf("30g pencere 7g'ye kelepçelenmeli: %d %v", w.Code, m)
	}

	// Series: dizi etiket setleri.
	w = e.do(t, "GET", "/api/promql/series", url.Values{"cluster": {promqlClusterA}, "match[]": {"up"}}, "editor-1", auth.RoleEditor)
	if data := decodePromQLBody(t, w)["data"].([]any); len(data) != 2 {
		t.Fatalf("series: %v", data)
	}

	calls := e.fake.count()
	for _, c := range []struct {
		name, path string
		form       url.Values
		wantCode   int
		wantMsg    string
	}{
		{"cluster yok", "/api/promql/labels", url.Values{}, 400, "cluster parameter is required"},
		{"bilinmeyen cluster", "/api/promql/labels", url.Values{"cluster": {"cluster-zzz"}}, 404, "unknown or disabled cluster"},
		{"limit bozuk", "/api/promql/labels", url.Values{"cluster": {promqlClusterA}, "limit": {"x"}}, 400, `invalid parameter "limit"`},
		{"start bozuk", "/api/promql/labels", url.Values{"cluster": {promqlClusterA}, "start": {"dün"}}, 400, `invalid parameter "start"`},
		{"end < start", "/api/promql/labels", url.Values{"cluster": {promqlClusterA}, "start": {unixStr(promqlT0)}, "end": {unixStr(promqlT0 - 60)}}, 400, "before start"},
		{"21 match[]", "/api/promql/series", url.Values{"cluster": {promqlClusterA}, "match[]": strings.Split(strings.Repeat("up,", 21)[:3*21-1], ",")}, 400, "at most 20"},
		{"URL modelinde seçicisiz series", "/api/promql/series", url.Values{"cluster": {promqlClusterA}}, 400, "at least one match[]"},
	} {
		w := e.do(t, "GET", c.path, c.form, "editor-1", auth.RoleEditor)
		if w.Code != c.wantCode || !strings.Contains(fmt.Sprint(decodePromQLBody(t, w)["error"]), c.wantMsg) {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
	}
	if e.fake.count() != calls {
		t.Fatal("reddedilen metadata istekleri upstream'e gitti")
	}
	if rows := e.audits(); len(rows) != 0 {
		t.Fatalf("metadata audit YAZMAZ (karar 2): %+v", rows)
	}
}

func TestPromQLWriteConsoleFailureNeverEchoesUnknownErrors(t *testing.T) {
	w := httptest.NewRecorder()
	writePromQLConsoleFailure(w, errors.New(`Post "http://thanos.example.invalid:9090/api/v1/query": dial tcp: lookup thanos.example.invalid: no such host`), nil)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "example.invalid") || strings.Contains(w.Body.String(), "dial tcp") {
		t.Fatalf("ham hata yankılandı: %s", w.Body.String())
	}
	if body := decodePromQLBody(t, w); body["errorType"] != "internal" || body["status"] != "error" {
		t.Fatalf("gövde: %v", body)
	}
	w = httptest.NewRecorder()
	writePromQLConsoleFailure(w, fmt.Errorf("sarılı: %w", context.Canceled), nil)
	if w.Code != statusClientClosedRequest || w.Body.Len() != 0 {
		t.Fatalf("iptal 499 gövdesiz: %d %q", w.Code, w.Body.String())
	}
}
