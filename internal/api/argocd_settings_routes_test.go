package api

// v0.10.957 — Argo CD ayar yüzeyi (Rollouts v2 P1.4) HTTP sözleşmesi, SAHTE
// Thanos'a karşı (httptest; canlı çağrı YOK). Kapsam: kayıt defteri + rol
// kapısı (yalnız admin), GET varsayılanlar/bounds/token durumu, PUT
// gidiş-dönüşü (sahte depo) + audit + alan yollu 400'ler + düz token reddi,
// keşif probe'u (hub yok → 400 guardrail, kaydetmez, label-values
// parametreleri, küme etiketi enjeksiyon anahtarı, upstream hatası URL
// sızdırmadan eşlenir, meşgul kapısı) ve kablo pinleri (reload case,
// config-import listesi, main.go boot + 30 s yenileme).
//
// v0.10.974 — Argo CD ayar sekmesi onayının (2026-09-27) dört arka uç kuralı:
// boş tokenRef kayıtlıyı korur / clearTokenRef kaldırır (blob'a, cevaba,
// audit'e hiç girmez), kayıtlı kimlik değiştirme 400, bağlı instance'ı olan
// hub kaldırma 400, PUT başka pod'un az önce yazdığı bloba karşı birleşir;
// keşifte ikinci tur uygulama (anlık count, sahte /api/v1/query) ve shard
// (`pod` label-values) sayımı aynı bütçeden, sayım hatası 200'ü bozmaz; hub
// Thanos 403 metni FE'nin eşlediği biçimde pinli.
//
// v0.10.978 — iyimser ön koşul (expectedUpdatedAt → 409 stale; depo okunamazken
// 503 unavailable; pod içi argocdPutMu ile atomik: eşzamanlı ikiz PUT'un
// ikincisi 409) ve keşifte "yetki yok" (401/403 → errorType unauthorized).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// ── Sahte Thanos (label values + v0.10.974 anlık sorgu) ────────────────────

type argoFakeReq struct {
	Method, Path string
	Form         url.Values
}

type argoFakeThanos struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []argoFakeReq
	// values — etiket adı → match[] içinde aranan alt dize → değerler.
	// İlk eşleşen alt dize kazanır (daha özgül olan önce yazılır).
	values map[string][]argoFakeRule
	// queries — v0.10.974 — /api/v1/query: `query` içinde aranan alt dize →
	// vector data.result JSON'u (ilk eşleşen; yoksa []).
	queries []argoFakeQueryRule
	fail    func(req argoFakeReq) (int, string) // 0 = başarı
}

type argoFakeQueryRule struct {
	contains string
	result   string
}

type argoFakeRule struct {
	contains string
	vals     []string
}

func newArgoFakeThanos(t *testing.T) *argoFakeThanos {
	t.Helper()
	f := &argoFakeThanos{values: map[string][]argoFakeRule{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		req := argoFakeReq{Method: r.Method, Path: r.URL.Path, Form: r.Form}
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		fail := f.fail
		f.mu.Unlock()
		if fail != nil {
			if code, body := fail(req); code != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				fmt.Fprint(w, body)
				return
			}
		}
		if req.Path == "/api/v1/query" {
			result := "[]"
			for _, rule := range f.queries {
				if strings.Contains(req.Form.Get("query"), rule.contains) {
					result = rule.result
					break
				}
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":%s}}`, result)
			return
		}
		const pre, suf = "/api/v1/label/", "/values"
		if !strings.HasPrefix(req.Path, pre) || !strings.HasSuffix(req.Path, suf) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		label := strings.TrimSuffix(strings.TrimPrefix(req.Path, pre), suf)
		match := strings.Join(req.Form["match[]"], " ")
		vals := []string{}
		for _, rule := range f.values[label] {
			if strings.Contains(match, rule.contains) {
				vals = rule.vals
				break
			}
		}
		raw, _ := json.Marshal(vals)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"success","data":%s}`, raw)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *argoFakeThanos) requests() []argoFakeReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]argoFakeReq(nil), f.reqs...)
}

// argoFakeStore — system_settings'in bellek içi ikizi.
type argoFakeStore struct {
	mu     sync.Mutex
	rows   map[string][]byte
	puts   int
	getErr error // v0.10.974 — PUT öncesi LoadPersisted hatası (logla, sür)
}

func (f *argoFakeStore) GetSetting(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.rows[key], nil
}

func (f *argoFakeStore) PutSetting(_ context.Context, key string, v []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	f.rows[key] = v
	return nil
}

type argoTestEnv struct {
	s     *Server
	mux   *http.ServeMux
	svc   *argocd.SettingsService
	store *argoFakeStore
	fake  *argoFakeThanos
}

const (
	argoHubName    = "cluster-a" // hub: paylaşımlı querier (ThanosLabelName=cluster)
	argoTargetName = "cluster-b"
)

// newArgoTestEnv — sahte Thanos (hub + hedef + devre dışı kayıt), noop
// önbellek, sahte depo, taze ayar servisi. Paket global'leri geri konur.
func newArgoTestEnv(t *testing.T) *argoTestEnv {
	t.Helper()
	f := newArgoFakeThanos(t)
	th := thanos.New()
	th.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{
		{Name: argoHubName, URL: f.URL, ThanosLabelName: "cluster", Enabled: true},
		{Name: argoTargetName, URL: f.URL, Enabled: true},
		{Name: "cluster-off", URL: f.URL, Enabled: false},
	}})
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats(),
		auditQ: make(chan chstore.AuditEntry, 64), thanos: th}
	st := &argoFakeStore{rows: map[string][]byte{}}
	svc := argocd.NewSettingsService()

	prevSvc := argocdSettingsSvc.Load()
	prevStore := argocdSettingsStoreOf
	SetArgoCDSettings(svc)
	argocdSettingsStoreOf = func(*Server) argocd.Store { return st }
	t.Cleanup(func() {
		argocdSettingsSvc.Store(prevSvc)
		argocdSettingsStoreOf = prevStore
		argocdDiscoverBusy.Store(false)
	})
	// v0.10.990 — keşif testleri istek SIRASINI pinler: varsayılan sıralı kip.
	// Eşzamanlı kipi TestArgoCDDiscoverManyJobsParallel açıkça dener.
	argoDiscoverLimits(t, argocdDiscoverMaxJobs, argocdDiscoverMaxCalls, 1)
	mux := http.NewServeMux()
	s.registerArgoCDSettingsRoutes(mux)
	return &argoTestEnv{s: s, mux: mux, svc: svc, store: st, fake: f}
}

// argoDiscoverLimits — v0.10.990 — keşfin iş / çağrı tavanını ve
// eşzamanlılığını test süresince değiştirir (paket değişkenleri geri konur).
func argoDiscoverLimits(t *testing.T, jobs, calls, parallel int) {
	t.Helper()
	pj, pc, pp := argocdDiscoverMaxJobs, argocdDiscoverMaxCalls, argocdDiscoverParallel
	argocdDiscoverMaxJobs, argocdDiscoverMaxCalls, argocdDiscoverParallel = jobs, calls, parallel
	t.Cleanup(func() { argocdDiscoverMaxJobs, argocdDiscoverMaxCalls, argocdDiscoverParallel = pj, pc, pp })
}

func (e *argoTestEnv) do(t *testing.T, method, path, body, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		req = req.WithContext(auth.ContextWithClaims(req.Context(),
			&auth.Claims{UserID: "u-" + role, Email: role + "@example.test", Role: role}))
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func (e *argoTestEnv) audits() []chstore.AuditEntry {
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

func argoJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("gövde JSON değil (%d): %v — %s", w.Code, err, w.Body.String())
	}
	return m
}

func argoClusterID(name string) string { return thanos.ClusterConfig{Name: name}.EffectiveID() }

// ── Kayıt + rol kapısı ─────────────────────────────────────────────────────

func TestArgoCDRoutesRegisteredViaRegistry(t *testing.T) {
	src, err := os.ReadFile("argocd_settings_routes.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoComments(string(src)), `registerRoutesExtra("argocd-settings", (*Server).registerArgoCDSettingsRoutes)`) {
		t.Fatal("argocd_settings_routes.go init() defter kaydı yok")
	}
	if b, _ := os.ReadFile("api.go"); strings.Contains(strings.ToLower(string(b)), "argocd") {
		t.Fatal("api.go argocd içeriyor — api.go büyümez (defter üzerinden)")
	}
	mux := (&Server{}).buildMux()
	for _, p := range []struct{ m, path, want string }{
		{"GET", "/api/settings/argocd", "GET /api/settings/argocd"},
		{"PUT", "/api/settings/argocd", "PUT /api/settings/argocd"},
		{"POST", "/api/settings/argocd/discover", "POST /api/settings/argocd/discover"},
	} {
		if _, pat := mux.Handler(httptest.NewRequest(p.m, p.path, nil)); pat != p.want {
			t.Errorf("%s %s → %q, beklenen %q", p.m, p.path, pat, p.want)
		}
	}
}

func TestArgoCDRoutesAdminOnly(t *testing.T) {
	e := newArgoTestEnv(t)
	for _, r := range []struct{ m, path string }{
		{"GET", "/api/settings/argocd"}, {"PUT", "/api/settings/argocd"}, {"POST", "/api/settings/argocd/discover"},
	} {
		for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
			if w := e.do(t, r.m, r.path, `{}`, role); w.Code != http.StatusForbidden {
				t.Errorf("%s %s rol %s → %d, beklenen 403", r.m, r.path, role, w.Code)
			}
		}
		if w := e.do(t, r.m, r.path, `{}`, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s kimliksiz → %d, beklenen 401", r.m, r.path, w.Code)
		}
	}
	if e.store.puts != 0 || len(e.fake.requests()) != 0 || len(e.audits()) != 0 {
		t.Fatal("reddedilen istek yan etki bıraktı")
	}
}

// ── GET / PUT ──────────────────────────────────────────────────────────────

func TestArgoCDSettingsGetDefaults(t *testing.T) {
	e := newArgoTestEnv(t)
	w := e.do(t, "GET", "/api/settings/argocd", "", auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %d %s", w.Code, w.Body)
	}
	m := argoJSON(t, w)
	for _, k := range []string{"settings", "resolved", "defaults", "bounds", "tokens"} {
		if _, ok := m[k]; !ok {
			t.Errorf("GET %q alanı yok: %s", k, w.Body)
		}
	}
	if hubs, ok := m["hubs"].([]any); !ok || len(hubs) != 0 {
		t.Errorf("hub yokken hubs boş liste olmalı: %v", m["hubs"])
	}
	res := m["resolved"].(map[string]any)
	if rd := res["reader"].(map[string]any); rd["maxSeries"] != float64(50000) || rd["maxBodyMiB"] != float64(64) || rd["timeoutS"] != float64(30) {
		t.Errorf("resolved reader karar 2 varsayılanları: %v", rd)
	}
	if res["enabled"] != false {
		t.Errorf("varsayılan kapalı: %v", res)
	}
	// v0.10.957 — iki hub (§5.6): üst düzey hubClusterId/injectClusterLabel YOK.
	for _, k := range []string{"hubClusterId", "injectClusterLabel"} {
		if _, ok := res[k]; ok {
			t.Errorf("üst düzey %q olmamalı (hubs[] taşır): %v", k, res)
		}
	}
	if b := m["bounds"].(map[string]any)["reader.maxSeries"].(map[string]any); b["max"] != float64(50000) {
		t.Errorf("bounds: %v", b)
	}
}

func TestArgoCDSettingsNotWired(t *testing.T) {
	e := newArgoTestEnv(t)
	argocdSettingsSvc.Store(nil)
	for _, r := range []struct{ m, path string }{{"GET", "/api/settings/argocd"}, {"PUT", "/api/settings/argocd"}, {"POST", "/api/settings/argocd/discover"}} {
		if w := e.do(t, r.m, r.path, `{}`, auth.RoleAdmin); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s bağlı değil → %d, beklenen 503", r.path, w.Code)
		}
	}
}

func argoValidBody(hubID string) string {
	return `{"enabled":true,"hubs":[{"clusterId":"` + hubID + `","injectClusterLabel":false}],"envList":["PROD","uat"],
	 "instances":[{"id":"team-a-prod","hubNamespace":"team-a-prod","metricsJob":"team-a-prod-metrics",
	   "apiUrl":"HTTPS://ArgoCD-Team-A.Apps.Example.Invalid/","tokenRef":"env:COREMETRY_TEST_ARGOCD_UNSET_957","enabled":true}],
	 "pins":[{"clusterId":"` + argoClusterID(argoTargetName) + `","namespace":"checkout","workloadKind":"deployment","workload":"checkout-api",
	   "instanceId":"team-a-prod","appNamespace":"team-a-prod","appName":"p-team-a-checkout-prod-b"}],
	 "reader":{"timeoutS":40}}`
}

func TestArgoCDSettingsPutRoundTrip(t *testing.T) {
	e := newArgoTestEnv(t)
	hub := argoClusterID(argoHubName)
	w := e.do(t, "PUT", "/api/settings/argocd", argoValidBody(hub), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	raw := e.store.rows[argocd.SettingsKey]
	if e.store.puts != 1 || len(raw) == 0 {
		t.Fatalf("blob system_settings[%q]'a yazılmalı: puts=%d", argocd.SettingsKey, e.store.puts)
	}
	var saved argocd.Settings
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Hubs) != 1 || saved.Hubs[0].ClusterID != hub || saved.Hubs[0].Inject() || saved.Instances[0].HubClusterID != hub ||
		saved.Instances[0].APIURL != "https://argocd-team-a.apps.example.invalid" ||
		strings.Join(saved.EnvList, ",") != "prod,uat" || saved.Pins[0].WorkloadKind != "Deployment" || saved.UpdatedAt == 0 {
		t.Fatalf("kanonik blob yazılmalı (tek hub → instance hub'ı tamamlanır): %s", raw)
	}
	// Audit: tek satır, doğru eylem/kaynak; blob ref taşır, token değil.
	rows := e.audits()
	if len(rows) != 1 || rows[0].Action != "settings.argocd.update" || rows[0].TargetKind != "settings" || rows[0].TargetID != argocd.SettingsKey {
		t.Fatalf("audit: %+v", rows)
	}
	if !strings.Contains(rows[0].Details, `"tokenRef":"env:COREMETRY_TEST_ARGOCD_UNSET_957"`) {
		t.Errorf("audit details blobu taşımalı: %s", rows[0].Details)
	}
	// Canlı servis bu pod'da hemen güncel; GET aynı değerleri döner.
	if e.svc.Resolved().Reader.TimeoutS != 40 {
		t.Fatalf("canlı ayar: %+v", e.svc.Resolved().Reader)
	}
	m := argoJSON(t, e.do(t, "GET", "/api/settings/argocd", "", auth.RoleAdmin))
	if hs := m["settings"].(map[string]any)["hubs"].([]any); len(hs) != 1 || hs[0].(map[string]any)["clusterId"] != hub {
		t.Fatalf("GET gidiş-dönüş: %v", m["settings"])
	}
	h := m["hubs"].([]any)[0].(map[string]any)
	if h["id"] != hub || h["name"] != argoHubName || h["enabled"] != true || h["found"] != true || h["injectClusterLabel"] != false {
		t.Errorf("hub bilgisi: %v", h)
	}
	tok := m["tokens"].(map[string]any)["team-a-prod"].(map[string]any)
	if tok["tokenRef"] != "env:COREMETRY_TEST_ARGOCD_UNSET_957" || tok["resolved"] != false || tok["error"] == "" {
		t.Errorf("çözülemeyen ref rozeti (fail-closed): %v", tok)
	}
	// Başka bir pod aynı blobu yükler.
	other := argocd.NewSettingsService()
	if err := other.LoadPersisted(context.Background(), e.store); err != nil || len(other.Current().Hubs) != 1 || other.Current().Hubs[0].ClusterID != hub {
		t.Fatalf("öteki pod: %v %+v", err, other.Current())
	}
}

func TestArgoCDSettingsPutValidation(t *testing.T) {
	e := newArgoTestEnv(t)
	hub := argoClusterID(argoHubName)
	cases := []struct {
		name, body, field string
	}{
		{"düz token", `{"instances":[{"id":"a","hubNamespace":"a","token":"eyJhbGci"}]}`, "instances[0].token"},
		{"bilinmeyen hub", `{"hubs":[{"clusterId":"c-00000000"}]}`, "hubs[0].clusterId"},
		{"açık + hub yok", `{"enabled":true}`, "hubs"},
		{"devre dışı hub + açık", `{"enabled":true,"hubs":[{"clusterId":"` + argoClusterID("cluster-off") + `"}]}`, "hubs[0].clusterId"},
		// v0.10.957 — tek-hub şekli sessizce atılmaz (§5.6 iki hub)
		{"eski üst düzey hubClusterId", `{"hubClusterId":"` + hub + `"}`, "hubClusterId"},
		{"iki hub + instance hub'ı yok", `{"hubs":[{"clusterId":"` + hub + `"},{"clusterId":"` + argoClusterID(argoTargetName) + `"}],
		  "instances":[{"id":"a","hubNamespace":"a"}]}`, "instances[0].hubClusterId"},
		{"apiUrl userinfo", `{"hubs":[{"clusterId":"` + hub + `"}],"instances":[{"id":"a","hubNamespace":"a","apiUrl":"https://u:p@argocd.example.invalid"}]}`, "instances[0].apiUrl"},
		{"tokenRef şemasız", `{"hubs":[{"clusterId":"` + hub + `"}],"instances":[{"id":"a","hubNamespace":"a","tokenRef":"plaintext"}]}`, "instances[0].tokenRef"},
		{"reader karar 2 tavanı", `{"reader":{"maxSeries":60000}}`, "reader.maxSeries"},
		// v0.10.974 — istek-yalnız clearTokenRef
		{"clearTokenRef bool değil", `{"hubs":[{"clusterId":"` + hub + `"}],"instances":[{"id":"a","hubNamespace":"a","clearTokenRef":"yes"}]}`, "instances[0].clearTokenRef"},
		{"clearTokenRef + dolu tokenRef", `{"hubs":[{"clusterId":"` + hub + `"}],"instances":[{"id":"a","hubNamespace":"a","tokenRef":"env:A","clearTokenRef":true}]}`, "instances[0].clearTokenRef"},
		{"bozuk JSON", `{"enabled":`, ""},
	}
	for _, c := range cases {
		w := e.do(t, "PUT", "/api/settings/argocd", c.body, auth.RoleAdmin)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, beklenen 400 — %s", c.name, w.Code, w.Body)
			continue
		}
		m := argoJSON(t, w)
		if f, _ := m["field"].(string); f != c.field {
			t.Errorf("%s: field %q, beklenen %q (%v)", c.name, f, c.field, m)
		}
		if msg, _ := m["error"].(string); msg == "" || (c.field != "" && !strings.HasPrefix(msg, c.field+": ")) {
			t.Errorf("%s: error alan yolunu taşımalı: %q", c.name, msg)
		}
	}
	if e.store.puts != 0 || len(e.audits()) != 0 || e.svc.Current().Enabled {
		t.Fatal("reddedilen PUT yazmamalı / audit etmemeli / canlıyı değiştirmemeli")
	}
	// Geçerli ama depo yok → 503 (panik değil).
	argocdSettingsStoreOf = func(*Server) argocd.Store { return nil }
	if w := e.do(t, "PUT", "/api/settings/argocd", `{"hubs":[{"clusterId":"`+hub+`"}]}`, auth.RoleAdmin); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("depo yok → %d, beklenen 503", w.Code)
	}
}

// ── Keşif probe'u ──────────────────────────────────────────────────────────

func TestArgoCDDiscoverGuardrails(t *testing.T) {
	e := newArgoTestEnv(t)
	cases := []struct{ name, body string }{
		{"hub yok (kayıtlı ya da gövdede)", ``},
		{"bilinmeyen hub", `{"hubClusterId":"c-00000000"}`},
		{"devre dışı hub", `{"hubClusterId":"` + argoClusterID("cluster-off") + `"}`},
		{"bozuk gövde", `{"hubClusterId":`},
	}
	for _, c := range cases {
		w := e.do(t, "POST", "/api/settings/argocd/discover", c.body, auth.RoleAdmin)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, beklenen 400 — %s", c.name, w.Code, w.Body)
			continue
		}
		if m := argoJSON(t, w); m["errorType"] != "guardrail" {
			t.Errorf("%s: errorType %v, beklenen guardrail", c.name, m["errorType"])
		}
	}
	if n := len(e.fake.requests()); n != 0 {
		t.Fatalf("korkuluk upstream'e gitmemeli, %d istek", n)
	}
	// Çözülemeyen tokenRef'li hub → fail-closed (istek YOK).
	e.s.thanos.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{
		{Name: argoHubName, URL: e.fake.URL, AuthType: "bearer", TokenRef: "env:COREMETRY_TEST_THANOS_UNSET_957", Enabled: true},
	}})
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+argoClusterID(argoHubName)+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusBadRequest || argoJSON(t, w)["errorType"] != "guardrail" || len(e.fake.requests()) != 0 {
		t.Fatalf("çözülemeyen hub tokenRef → 400 guardrail + 0 istek: %d %s (%d istek)", w.Code, w.Body, len(e.fake.requests()))
	}
	if len(e.audits()) != 0 {
		t.Fatal("korkuluk audit yazmamalı (hiçbir şey koşmadı)")
	}
}

func seedArgoFake(f *argoFakeThanos) {
	f.values["job"] = []argoFakeRule{{"argocd_app_info", []string{"team-a-prod-metrics", "team-b-uat-metrics"}}}
	f.values["namespace"] = []argoFakeRule{
		{`job="team-a-prod-metrics"`, []string{"team-a-prod"}},
		{`job="team-b-uat-metrics"`, []string{"team-b-uat"}},
	}
	f.values["exported_namespace"] = []argoFakeRule{
		{`job="team-a-prod-metrics"`, []string{"team-a-prod", "team-a-apps"}}, // durum C
		// team-b: exported_namespace YOK (honorLabels: true) → durum B
	}
	// v0.10.974 — sayım turu: controller pod'ları (shard) ve anlık count.
	f.values["pod"] = []argoFakeRule{
		{`job="team-a-prod-metrics"`, []string{"argocd-application-controller-0", "argocd-application-controller-1", "argocd-application-controller-2"}},
		// team-b: pod etiketi yok → shardCount nil + not
	}
	f.queries = []argoFakeQueryRule{
		{`job="team-a-prod-metrics"`, `[{"metric":{"namespace":"team-a-prod"},"value":[1700000000,"1184"]}]`},
		{`job="team-b-uat-metrics"`, `[{"metric":{},"value":[1700000000,"57"]}]`},
	}
}

func TestArgoCDDiscoverCandidatesReadOnly(t *testing.T) {
	e := newArgoTestEnv(t)
	seedArgoFake(e.fake)
	hub := argoClusterID(argoHubName)
	// Kayıtlı hub (PUT değil; canlı servise doğrudan).
	e.svc.Configure(argocd.Settings{Hubs: []argocd.Hub{{ClusterID: hub}},
		Instances: []argocd.Instance{{ID: "prod-a", HubClusterID: hub, HubNamespace: "team-a-prod", MetricsJob: "team-a-prod-metrics"}}})
	w := e.do(t, "POST", "/api/settings/argocd/discover", ``, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("keşif %d %s", w.Code, w.Body)
	}
	var res struct {
		HubClusterID       string             `json:"hubClusterId"`
		HubName            string             `json:"hubName"`
		InjectClusterLabel bool               `json:"injectClusterLabel"`
		Candidates         []argocd.Candidate `json:"candidates"`
		Calls              int                `json:"calls"`
		Saved              bool               `json:"saved"`
		Window             struct{ Start, End int64 }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	// v0.10.974 — çağrı: 1 iş + 2×2 aday turu + 2×(1 count + 1 pod) sayım turu.
	if res.HubClusterID != hub || res.HubName != argoHubName || !res.InjectClusterLabel || res.Saved || res.Calls != 9 {
		t.Fatalf("zarf: %+v", res)
	}
	if res.Window.End-res.Window.Start != 3600_000 {
		t.Errorf("metadata penceresi açıkça 1 sa olmalı (§5.4 kural 4): %+v", res.Window)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("2 aday: %+v", res.Candidates)
	}
	a, b := res.Candidates[0], res.Candidates[1]
	if a.MetricsJob != "team-a-prod-metrics" || a.NamespaceCase != "C" || !a.AppsAnyNamespace || a.ConfiguredID != "prod-a" || a.HubNamespace != "team-a-prod" {
		t.Errorf("A aday (C durumu, kayıtlı eşleşme): %+v", a)
	}
	if b.MetricsJob != "team-b-uat-metrics" || b.NamespaceCase != "B" || b.HubNamespace != "team-b-uat" || b.ID != "team-b-uat" {
		t.Errorf("B aday: %+v", b)
	}
	if a.HubClusterID != hub || b.HubClusterID != hub {
		t.Errorf("adaylar probe edilen hub'ı taşımalı (§5.6): %q %q", a.HubClusterID, b.HubClusterID)
	}
	// v0.10.974 — sayımlar: A count by (namespace) satırı + 3 pod; B iş geneli
	// count() + pod yok (not).
	if a.AppCount == nil || *a.AppCount != 1184 || a.ShardCount == nil || *a.ShardCount != 3 || a.ShardCountTruncated || a.CountNote != "" {
		t.Errorf("A sayımları: %+v", a)
	}
	if b.AppCount == nil || *b.AppCount != 57 || b.ShardCount != nil || b.CountNote != "pod etiketi yok" {
		t.Errorf("B sayımları (iş geneli count, pod yok): %+v", b)
	}
	if strings.Contains(w.Body.String(), "countsIncomplete") || strings.Contains(w.Body.String(), `"incomplete"`) {
		t.Errorf("bütçe yeterliyken countsIncomplete/incomplete yok: %s", w.Body)
	}
	// Salt-okunur: depo dokunulmadı, canlı ayar aynı.
	if e.store.puts != 0 || len(e.svc.Current().Instances) != 1 {
		t.Fatal("keşif KAYDETMEMELİ")
	}
	// Upstream: label values GET (sınırlı, pencereli, partial_response=false,
	// her seçici argocd_app_info + hub'ın küme matcher'ı) ve v0.10.974 — iş
	// başına tek anlık count POST'u (time = pencere sonu, partial_response=false,
	// küme matcher'ı enjekte).
	reqs := e.fake.requests()
	end := reqs[0].Form.Get("end")
	var queries []string
	for _, r := range reqs {
		if r.Path == "/api/v1/query" {
			q := r.Form.Get("query")
			queries = append(queries, q)
			// v0.10.974 — timeout=15s: çağrı başına keşif tavanı (argocdDiscoverCallTimeout);
			// ConsoleLimits'ten düşerse varsayılan 30s giderdi.
			if r.Method != http.MethodPost || r.Form.Get("partial_response") != "false" || r.Form.Get("time") != end || end == "" ||
				r.Form.Get("timeout") != "15s" {
				t.Errorf("anlık count: POST, partial_response=false, time=pencere sonu (%s), timeout=15s: %s %v", end, r.Method, r.Form)
			}
			if !strings.Contains(q, `argocd_app_info{cluster="`+argoHubName+`",job="`) || !strings.HasPrefix(q, "count") ||
				!strings.Contains(q, "group by (namespace, exported_namespace, name)") {
				t.Errorf("count sorgusu: %s", q)
			}
			continue
		}
		if r.Method != http.MethodGet || !strings.HasPrefix(r.Path, "/api/v1/label/") {
			t.Errorf("label-values GET ya da /api/v1/query POST beklenir: %s %s", r.Method, r.Path)
		}
		if r.Form.Get("start") == "" || r.Form.Get("end") == "" || r.Form.Get("limit") == "" || r.Form.Get("partial_response") != "false" {
			t.Errorf("start/end/limit/partial_response=false: %v", r.Form)
		}
		for _, m := range r.Form["match[]"] {
			if !strings.HasPrefix(m, "argocd_app_info{") || !strings.Contains(m, `cluster="`+argoHubName+`"`) {
				t.Errorf("seçici: %s", m)
			}
		}
	}
	if len(queries) != 2 ||
		queries[0] != `count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info{cluster="cluster-a",job="team-a-prod-metrics"}))` ||
		queries[1] != `count(group by (namespace, exported_namespace, name) (argocd_app_info{cluster="cluster-a",job="team-b-uat-metrics"}))` {
		t.Errorf("iş başına tek count (A: by namespace, B: iş geneli): %q", queries)
	}
	rows := e.audits()
	if len(rows) != 1 || rows[0].Action != "settings.argocd.discover" || !strings.Contains(rows[0].Details, `"candidates":2`) ||
		!strings.Contains(rows[0].Details, `"countsIncomplete":false`) || !strings.Contains(rows[0].Details, `"calls":9`) {
		t.Fatalf("keşif audit: %+v", rows)
	}
}

func TestArgoCDDiscoverInjectSwitch(t *testing.T) {
	e := newArgoTestEnv(t)
	seedArgoFake(e.fake)
	hub := argoClusterID(argoHubName)
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+hub+`","injectClusterLabel":false}`, auth.RoleAdmin)
	if w.Code != http.StatusOK || argoJSON(t, w)["injectClusterLabel"] != false {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	reqs := e.fake.requests()
	if len(reqs) == 0 {
		t.Fatal("istek yok")
	}
	nq := 0
	for _, r := range reqs {
		for _, m := range append(append([]string(nil), r.Form["match[]"]...), r.Form["query"]...) {
			if strings.Contains(m, "cluster=") {
				t.Fatalf("injectClusterLabel=false iken matcher enjekte edildi: %s", m)
			}
		}
		if r.Path == "/api/v1/query" {
			nq++
		}
	}
	if nq != 2 {
		t.Fatalf("v0.10.974 — sayım sorguları da etiketsiz koşmalı (2 iş → 2 count): %d", nq)
	}
	// v0.10.957 — enjeksiyon HUB BAŞINA (§5.6): kayıtlı hub false, gövde
	// enjeksiyon söylemiyor → hub'ın kendi ayarı (matcher YOK).
	f := false
	e.svc.Configure(argocd.Settings{Hubs: []argocd.Hub{{ClusterID: hub, InjectClusterLabel: &f}}})
	before := len(e.fake.requests())
	if w := e.do(t, "POST", "/api/settings/argocd/discover", ``, auth.RoleAdmin); w.Code != http.StatusOK || argoJSON(t, w)["injectClusterLabel"] != false {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, r := range e.fake.requests()[before:] {
		for _, m := range r.Form["match[]"] {
			if strings.Contains(m, "cluster=") {
				t.Fatalf("hub'ın kayıtlı injectClusterLabel=false ayarı uygulanmadı: %s", m)
			}
		}
	}
	// Kayıtlı ayar false, gövde true → gövde kazanır (tek seferlik deneme).
	before = len(e.fake.requests())
	if w := e.do(t, "POST", "/api/settings/argocd/discover", `{"injectClusterLabel":true}`, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if m := e.fake.requests()[before].Form["match[]"]; len(m) == 0 || !strings.Contains(m[0], `cluster="`+argoHubName+`"`) {
		t.Fatalf("gövde true → matcher: %v", m)
	}
}

func TestArgoCDDiscoverUpstreamErrorsNoLeak(t *testing.T) {
	e := newArgoTestEnv(t)
	hub := argoClusterID(argoHubName)
	e.fake.fail = func(argoFakeReq) (int, string) {
		return http.StatusServiceUnavailable, `{"status":"error","errorType":"unavailable","error":"store ` + e.fake.URL + ` down"}`
	}
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+hub+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("upstream 503 → %d, beklenen 502 — %s", w.Code, w.Body)
	}
	assertArgoNoLeak(t, w.Body.String(), e.fake.URL)
	if m := argoJSON(t, w); m["errorType"] != "unavailable" || m["error"] == "" {
		t.Errorf("hata gövdesi: %v", m)
	}
	if rows := e.audits(); len(rows) != 1 || rows[0].Action != "settings.argocd.discover" || !strings.Contains(rows[0].Details, `"status":502`) {
		t.Fatalf("başarısız keşif de audit'lenir: %+v", rows)
	}

	// Yalnız bir işin sorgusu düşer → 200, o aday Error taşır (URL'siz).
	seedArgoFake(e.fake)
	e.fake.fail = func(r argoFakeReq) (int, string) {
		if strings.Contains(strings.Join(r.Form["match[]"], " "), `job="team-b-uat-metrics"`) {
			return http.StatusInternalServerError, `{"status":"error","errorType":"internal","error":"boom at ` + e.fake.URL + `"}`
		}
		return 0, ""
	}
	w = e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+hub+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("kısmi hata 200 olmalı: %d %s", w.Code, w.Body)
	}
	assertArgoNoLeak(t, w.Body.String(), e.fake.URL)
	if !strings.Contains(w.Body.String(), `"error":"internal`) {
		t.Errorf("başarısız iş adayı hata taşımalı: %s", w.Body)
	}

	// Erişilemez hub → 502, sızıntı yok.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	e.s.thanos.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{{Name: argoHubName, URL: deadURL, Enabled: true}}})
	w = e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+hub+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("erişilemez hub → %d — %s", w.Code, w.Body)
	}
	assertArgoNoLeak(t, w.Body.String(), deadURL)
}

func assertArgoNoLeak(t *testing.T, body, endpoint string) {
	t.Helper()
	u, _ := url.Parse(endpoint)
	for _, leak := range []string{endpoint, u.Host, "127.0.0.1", "dial tcp", "Get \""} {
		if leak != "" && strings.Contains(body, leak) {
			t.Fatalf("gövde %q sızdırıyor: %s", leak, body)
		}
	}
}

func TestArgoCDDiscoverBusy(t *testing.T) {
	e := newArgoTestEnv(t)
	argocdDiscoverBusy.Store(true)
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+argoClusterID(argoHubName)+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusTooManyRequests || len(e.fake.requests()) != 0 {
		t.Fatalf("koşan keşif varken 429 + 0 istek: %d (%d istek)", w.Code, len(e.fake.requests()))
	}
}

// ── Kablo pinleri ──────────────────────────────────────────────────────────

func TestArgoCDSettingsReloadSignal(t *testing.T) {
	e := newArgoTestEnv(t)
	hub := argoClusterID(argoHubName)
	e.store.rows[argocd.SettingsKey] = []byte(`{"hubs":[{"clusterId":"` + hub + `"}],"updatedAt":5}`)
	e.s.reloadConfigOnSignal(context.Background(), "argocd")
	if hs := e.svc.Current().Hubs; len(hs) != 1 || hs[0].ClusterID != hub {
		t.Fatalf("peer sinyali blobu yüklemeli: %+v", e.svc.Current())
	}
	// Store'suz / servissiz pod paniklemez.
	argocdSettingsStoreOf = func(*Server) argocd.Store { return nil }
	(&Server{}).reloadConfigOnSignal(context.Background(), "argocd")
	argocdSettingsSvc.Store(nil)
	(&Server{}).reloadConfigOnSignal(context.Background(), "argocd")
}

func TestArgoCDSettingsWiring(t *testing.T) {
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return stripGoComments(string(b))
	}
	src := read("argocd_settings_routes.go")
	for _, want := range []string{
		`s.publishConfigReload(r.Context(), "argocd")`,
		`s.audit(r, "settings.argocd.update", "settings", argocd.SettingsKey,`,
		`s.audit(r, "settings.argocd.discover", "settings", argocd.SettingsKey,`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("argocd_settings_routes.go %q içermiyor", want)
		}
	}
	cacheGo := read("cache.go")
	i := strings.Index(cacheGo, "func (s *Server) reloadConfigOnSignal")
	if i < 0 || !regexp.MustCompile(`case "argocd":\s*s\.reloadArgoCDSettings\(ctx\)`).MatchString(cacheGo[i:]) {
		t.Error(`reloadConfigOnSignal: case "argocd" → s.reloadArgoCDSettings(ctx) yok`)
	}
	if iox := read("config_iox.go"); !strings.Contains(iox, `"argocd",`) || !strings.Contains(iox, `"rollouts",`) {
		t.Error("config_iox.go import sonrası yeniden yayın listesinde argocd/rollouts yok")
	}
	mainGo := read("../../main.go")
	for _, want := range []string{
		"argocdSettings := argocd.NewSettingsService()",
		"argocdSettings.LoadPersisted(ctx, store)",
		`cfgRefresh.Add("argocd", func(ctx context.Context) error { return argocdSettings.LoadPersisted(ctx, store) })`,
		"api.SetArgoCDSettings(argocdSettings)",
	} {
		if !strings.Contains(mainGo, want) {
			t.Errorf("main.go %q içermiyor (boot hidrasyonu / 30 s yenileme / bağlama)", want)
		}
	}
	// cfgRefresh.Add, Start'tan ÖNCE (kayıtlar tamamlanınca tek döngü).
	if a, b := strings.Index(mainGo, `cfgRefresh.Add("argocd"`), strings.Index(mainGo, "go cfgRefresh.Start("); a < 0 || b < 0 || a > b {
		t.Error(`cfgRefresh.Add("argocd") cfgRefresh.Start'tan önce olmalı`)
	}
}

// https://kubernetes.default.svc yalnız hub kaydında (audit §3.2/§5.3):
// lane R2 thanos_clusters'ta işaretsiz kabul eder, kural argocd PUT'unda
// canlı anlık görüntüye karşı işler.
func TestArgoCDSettingsPutInClusterURLOnlyOnHub(t *testing.T) {
	e := newArgoTestEnv(t)
	e.s.thanos.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{
		{Name: argoHubName, URL: e.fake.URL, Enabled: true, APIServerURLs: []string{"https://api.cluster-a.example.invalid:6443"}},
		{Name: argoTargetName, URL: e.fake.URL, Enabled: true, APIServerURLs: []string{"https://kubernetes.default.svc:6443"}},
	}})
	w := e.do(t, "PUT", "/api/settings/argocd", `{"hubs":[{"clusterId":"`+argoClusterID(argoHubName)+`"}]}`, auth.RoleAdmin)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("hub olmayan kayıtta in-cluster URL → 400, %d %s", w.Code, w.Body)
	}
	if m := argoJSON(t, w); m["field"] != "hubs" || !strings.Contains(m["error"].(string), argoTargetName) {
		t.Fatalf("alan + bağlı kayıt adı: %v", m)
	}
	// Hub o kaydın kendisiyse geçerli.
	if w := e.do(t, "PUT", "/api/settings/argocd", `{"hubs":[{"clusterId":"`+argoClusterID(argoTargetName)+`"}]}`, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("hub kaydında in-cluster URL geçerli: %d %s", w.Code, w.Body)
	}
}

// v0.10.957 — inceleme: Argo CD İKİ hub'da koşar (operatör, audit §5.6);
// keşif HUB BAŞINA. İki hub kayıtlıyken gövde hub seçmezse hangi hub'a
// gidileceği belirsiz → 400 guardrail ve upstream'e İSTEK YOK. Gövde hub'ı
// seçince yalnız o hub probe edilir, adaylar o hub'ı taşır.
func TestArgoCDDiscoverTwoHubsNeedsChoice(t *testing.T) {
	e := newArgoTestEnv(t)
	seedArgoFake(e.fake)
	hubA, hubB := argoClusterID(argoHubName), argoClusterID(argoTargetName)
	e.svc.Configure(argocd.Settings{Hubs: []argocd.Hub{{ClusterID: hubA}, {ClusterID: hubB}}})
	w := e.do(t, "POST", "/api/settings/argocd/discover", ``, auth.RoleAdmin)
	if w.Code != http.StatusBadRequest || argoJSON(t, w)["errorType"] != "guardrail" || len(e.fake.requests()) != 0 {
		t.Fatalf("iki hub + seçim yok → 400 guardrail, 0 istek: %d %s (%d istek)", w.Code, w.Body, len(e.fake.requests()))
	}
	w = e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+hubB+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("seçili hub → 200: %d %s", w.Code, w.Body)
	}
	var res struct {
		HubClusterID string             `json:"hubClusterId"`
		Candidates   []argocd.Candidate `json:"candidates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.HubClusterID != hubB || len(res.Candidates) == 0 {
		t.Fatalf("hub B probe edilmeli: %+v", res)
	}
	for _, c := range res.Candidates {
		if c.HubClusterID != hubB {
			t.Fatalf("aday seçili hub'ı taşımalı: %+v", c)
		}
	}
}

// v0.10.957 — inceleme: ÇÖZÜLMÜŞ token değeri hiçbir HTTP cevabında, kalıcı
// blobda ya da audit satırında görünmez (CLAUDE.md "secrets never echoed
// back"); rozet yalnız ref + çözüldü bilgisini taşır, değer yalnız bellekte
// (Token) durur. Mevcut testler yalnız ÇÖZÜLEMEYEN ref'i sınıyordu.
func TestArgoCDSettingsResolvedTokenNeverEchoed(t *testing.T) {
	const secret = "s3cr3t-argocd-token-957"
	t.Setenv("COREMETRY_TEST_ARGOCD_SET_957", secret)
	e := newArgoTestEnv(t)
	hub := argoClusterID(argoHubName)
	body := `{"hubs":[{"clusterId":"` + hub + `"}],"instances":[{"id":"team-a-prod","hubNamespace":"team-a-prod",
	  "tokenRef":"env:COREMETRY_TEST_ARGOCD_SET_957"}]}`
	put := e.do(t, "PUT", "/api/settings/argocd", body, auth.RoleAdmin)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", put.Code, put.Body)
	}
	get := e.do(t, "GET", "/api/settings/argocd", "", auth.RoleAdmin)
	leaks := []string{put.Body.String(), get.Body.String(), string(e.store.rows[argocd.SettingsKey])}
	for _, a := range e.audits() {
		leaks = append(leaks, a.Details)
	}
	for i, b := range leaks {
		if strings.Contains(b, secret) {
			t.Fatalf("çözülen token sızdı (#%d): %s", i, b)
		}
	}
	tok := argoJSON(t, get)["tokens"].(map[string]any)["team-a-prod"].(map[string]any)
	if tok["resolved"] != true || tok["tokenRef"] != "env:COREMETRY_TEST_ARGOCD_SET_957" {
		t.Fatalf("rozet: %v", tok)
	}
	if v, err := e.svc.Token("team-a-prod"); err != nil || v != secret {
		t.Fatalf("değer bellekte çözülmeli: %q %v", v, err)
	}
}

// ── v0.10.974 — dört arka uç kuralı (HTTP) ────────────────────────────────

func argoTokenRefs(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	var s argocd.Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("blob: %v — %s", err, raw)
	}
	out := map[string]string{}
	for _, inst := range s.Instances {
		out[inst.ID] = inst.TokenRef
	}
	return out
}

// BE1: ref yaz → aynı id boş ref ile PUT (kayıtlı korunur: depo blobu,
// tokens haritası, audit) → clearTokenRef ile PUT (ref gider). Bayrak
// hiçbir kalıcı ya da dönen yüzeyde görünmez.
func TestArgoCDSettingsPutTokenRefKeepThenClear(t *testing.T) {
	e := newArgoTestEnv(t)
	hub := argoClusterID(argoHubName)
	const ref = "env:COREMETRY_TEST_ARGOCD_KEEP_968"
	body := func(inst string) string {
		return `{"hubs":[{"clusterId":"` + hub + `"}],"instances":[` + inst + `,{"id":"team-b-uat","hubNamespace":"team-b-uat"}]}`
	}
	noFlag := func(step string, w *httptest.ResponseRecorder) {
		t.Helper()
		surfaces := []string{w.Body.String(), string(e.store.rows[argocd.SettingsKey])}
		for _, a := range e.audits() {
			surfaces = append(surfaces, a.Details)
		}
		for i, b := range surfaces {
			if strings.Contains(strings.ToLower(b), "cleartokenref") {
				t.Fatalf("%s: clearTokenRef istek-yalnız, #%d yüzeyde görünmemeli: %s", step, i, b)
			}
		}
	}

	w := e.do(t, "PUT", "/api/settings/argocd", body(`{"id":"team-a-prod","hubNamespace":"team-a-prod","tokenRef":"`+ref+`"}`), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("ref yaz: %d %s", w.Code, w.Body)
	}
	noFlag("ref yaz", w)

	w = e.do(t, "PUT", "/api/settings/argocd", body(`{"id":"team-a-prod","hubNamespace":"team-a-prod","tokenRef":""}`), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("boş ref: %d %s", w.Code, w.Body)
	}
	if got := argoTokenRefs(t, e.store.rows[argocd.SettingsKey]); got["team-a-prod"] != ref || got["team-b-uat"] != "" {
		t.Fatalf("boş ref kayıtlıyı korumalı (depo blobu): %v", got)
	}
	if tok, ok := argoJSON(t, w)["tokens"].(map[string]any)["team-a-prod"].(map[string]any); !ok || tok["tokenRef"] != ref {
		t.Fatalf("PUT cevabı tokens haritası kayıtlı ref'i taşımalı: %s", w.Body)
	}
	rows := e.audits()
	if len(rows) != 1 || rows[0].Action != "settings.argocd.update" || !strings.Contains(rows[0].Details, `"tokenRef":"`+ref+`"`) {
		t.Fatalf("audit details BİRLEŞMİŞ blobu taşımalı: %+v", rows)
	}

	w = e.do(t, "PUT", "/api/settings/argocd", body(`{"id":"team-a-prod","hubNamespace":"team-a-prod","tokenRef":"","clearTokenRef":true}`), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("clearTokenRef: %d %s", w.Code, w.Body)
	}
	if got := argoTokenRefs(t, e.store.rows[argocd.SettingsKey]); got["team-a-prod"] != "" {
		t.Fatalf("clearTokenRef ref'i kaldırmalı: %v", got)
	}
	if toks := argoJSON(t, w)["tokens"].(map[string]any); len(toks) != 0 {
		t.Fatalf("tokens haritasında id kalmamalı: %v", toks)
	}
	if strings.Contains(w.Body.String(), ref) || strings.Contains(string(e.store.rows[argocd.SettingsKey]), ref) {
		t.Fatalf("kaldırılan ref görünmemeli: %s", w.Body)
	}
	noFlag("clearTokenRef", w)
}

// PUT, 30 s yenilemeyi beklemeden başka pod'un az önce yazdığı bloba karşı
// birleşir (LoadPersisted önce); depo okuma hatası PUT'u düşürmez.
func TestArgoCDSettingsPutMergesAgainstPeerBlob(t *testing.T) {
	e := newArgoTestEnv(t)
	hub := argoClusterID(argoHubName)
	const ref = "env:COREMETRY_TEST_ARGOCD_PEER_968"
	if w := e.do(t, "PUT", "/api/settings/argocd", `{"hubs":[{"clusterId":"`+hub+`"}],"instances":[{"id":"team-a-prod","hubNamespace":"team-a-prod","tokenRef":"`+ref+`"}]}`, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("pod A: %d %s", w.Code, w.Body)
	}
	// Pod B: bellek bayat (hiç yüklenmemiş varsayılan).
	SetArgoCDSettings(argocd.NewSettingsService())
	if w := e.do(t, "PUT", "/api/settings/argocd", `{"hubs":[{"clusterId":"`+hub+`"}],"instances":[{"id":"team-a-prod","hubNamespace":"team-a-prod"}]}`, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("pod B: %d %s", w.Code, w.Body)
	}
	if got := argoTokenRefs(t, e.store.rows[argocd.SettingsKey]); got["team-a-prod"] != ref {
		t.Fatalf("pod B pod A'nın blobuna karşı birleşmeli (ref korunur): %v", got)
	}
	e.store.getErr = fmt.Errorf("ch down")
	if w := e.do(t, "PUT", "/api/settings/argocd", `{"hubs":[{"clusterId":"`+hub+`"}],"instances":[{"id":"team-a-prod","hubNamespace":"team-a-prod"}]}`, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("depo okuma hatası logla-sür: %d %s", w.Code, w.Body)
	}
	if got := argoTokenRefs(t, e.store.rows[argocd.SettingsKey]); got["team-a-prod"] != ref {
		t.Fatalf("bellekteki blobla birleşmeli: %v", got)
	}
}

// v0.10.978 — iyimser ön koşul (v0.10.974'te ertelenen "iki admin, son yazan
// kazanır" kararı, onaylandı): PUT gövdesindeki istek-yalnız expectedUpdatedAt,
// PUT öncesi TAZE yüklenen kalıcı blobun updatedAt'iyle karşılaştırılır.
// Tutmazsa 409 {error, errorType:"stale", updatedAt:<kayıtlı>} — yazım yok,
// audit yok, canlı ayar değişmez (400 duruşu). Gönderilmemişse kabul (API/token
// çağıranlar, eski bundle) ama audit details `"precondition":"none"` taşır;
// tutarsa `"precondition":"updatedAt"` + `expectedUpdatedAt`. Karşılaştırma
// LoadPersisted'tan SONRA: B pod'unun belleği bayatken A pod'unun damgası tutar.
//
// Adım 6: kalıcı blob OKUNAMIYORKEN ön koşullu PUT 503 {errorType:
// "unavailable"} — yazım yok, audit yok (bellekteki bayat bloba karşı doğrulama
// yanlış audit + kayıp yazım olurdu); ön koşulsuz PUT logla-sür (v0.10.974).
//
// Eski kod bu testi geçemez: bayat damga 200 döner ve audit details
// "precondition" taşımaz; depo hatasında ön koşullu PUT 200 alıp peer'ın yeni
// blobunu ezer ve audit "precondition":"updatedAt" der.
func TestArgoCDSettingsPutStalePrecondition(t *testing.T) {
	e := newArgoTestEnv(t)
	hub := argoClusterID(argoHubName)
	body := func(pre string) string {
		return `{` + pre + `"hubs":[{"clusterId":"` + hub + `"}],"instances":[{"id":"team-a-prod","hubNamespace":"team-a-prod"}]}`
	}
	stampOf := func(w *httptest.ResponseRecorder) int64 {
		t.Helper()
		var g struct {
			Settings argocd.Settings `json:"settings"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil || g.Settings.UpdatedAt == 0 {
			t.Fatalf("cevapta settings.updatedAt yok: %v %s", err, w.Body)
		}
		return g.Settings.UpdatedAt
	}

	// 1) Ön koşulsuz PUT: kabul, audit "precondition":"none" (blob da orada).
	w := e.do(t, "PUT", "/api/settings/argocd", body(""), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("ön koşulsuz PUT: %d %s", w.Code, w.Body)
	}
	first := stampOf(w)
	rows := e.audits()
	if len(rows) != 1 || !strings.Contains(rows[0].Details, `"precondition":"none"`) || strings.Contains(rows[0].Details, "expectedUpdatedAt") ||
		!strings.Contains(rows[0].Details, `"id":"team-a-prod"`) {
		t.Fatalf("audit details precondition:none + blob taşımalı: %+v", rows)
	}
	// GET'in verdiği damga = PUT cevabındaki damga (FE bunu geri gönderir).
	if got := stampOf(e.do(t, "GET", "/api/settings/argocd", "", auth.RoleAdmin)); got != first {
		t.Fatalf("GET updatedAt %d ≠ PUT %d", got, first)
	}

	// 2) Tutan ön koşul: kabul, audit "precondition":"updatedAt" + gönderilen damga.
	w = e.do(t, "PUT", "/api/settings/argocd", body(fmt.Sprintf(`"expectedUpdatedAt":%d,`, first)), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("tutan ön koşul: %d %s", w.Code, w.Body)
	}
	second := stampOf(w)
	if second == first {
		t.Fatal("ikinci kayıt yeni damga almalı")
	}
	rows = e.audits()
	if len(rows) != 1 || !strings.Contains(rows[0].Details, `"precondition":"updatedAt"`) ||
		!strings.Contains(rows[0].Details, fmt.Sprintf(`"expectedUpdatedAt":%d`, first)) {
		t.Fatalf("audit details tutan ön koşulu taşımalı: %+v", rows)
	}

	// 3) Bayat damga (ilk kaydın damgası): 409, gövde şekli, yazım/audit/canlı yok.
	puts := e.store.puts
	w = e.do(t, "PUT", "/api/settings/argocd", body(fmt.Sprintf(`"expectedUpdatedAt":%d,"enabled":true,`, first)), auth.RoleAdmin)
	if w.Code != http.StatusConflict {
		t.Fatalf("bayat damga → 409: %d %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type %q", ct)
	}
	var stale struct {
		Error     string  `json:"error"`
		ErrorType string  `json:"errorType"`
		UpdatedAt int64   `json:"updatedAt"`
		Field     *string `json:"field"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &stale); err != nil {
		t.Fatalf("409 gövdesi JSON değil: %v %s", err, w.Body)
	}
	if stale.ErrorType != "stale" || stale.UpdatedAt != second || stale.Field != nil ||
		stale.Error != "ayarlar bu sayfa yüklendikten sonra başka biri tarafından değiştirildi — yeniden yükleyin" {
		t.Fatalf("409 gövdesi: %s", w.Body)
	}
	if e.store.puts != puts || len(e.audits()) != 0 || e.svc.Current().Enabled || e.svc.Current().UpdatedAt != second {
		t.Fatal("409 yazmamalı / audit etmemeli / canlıyı değiştirmemeli")
	}

	// 4) Biçim: dize → 400 alan yollu (ön koşul ayrıştırma hatası bayatlık değil).
	w = e.do(t, "PUT", "/api/settings/argocd", body(`"expectedUpdatedAt":"`+fmt.Sprint(second)+`",`), auth.RoleAdmin)
	if w.Code != http.StatusBadRequest || argoJSON(t, w)["field"] != "expectedUpdatedAt" {
		t.Fatalf("dize damga → 400 expectedUpdatedAt: %d %s", w.Code, w.Body)
	}

	// 5) Pod B: bellek bayat (hiç yüklenmemiş varsayılan, updatedAt 0) ama
	// karşılaştırma PUT öncesi LoadPersisted'tan SONRA → A'nın damgası tutar.
	SetArgoCDSettings(argocd.NewSettingsService())
	w = e.do(t, "PUT", "/api/settings/argocd", body(fmt.Sprintf(`"expectedUpdatedAt":%d,`, second)), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("pod B tutan damga: %d %s", w.Code, w.Body)
	}
	// … ve bayat damga pod B'de de 409 (bellekteki 0'a değil kalıcı bloba karşı).
	SetArgoCDSettings(argocd.NewSettingsService())
	if w = e.do(t, "PUT", "/api/settings/argocd", body(fmt.Sprintf(`"expectedUpdatedAt":%d,`, first)), auth.RoleAdmin); w.Code != http.StatusConflict {
		t.Fatalf("pod B bayat damga → 409: %d %s", w.Code, w.Body)
	}

	// 6) v0.10.978 — depo okunamıyor + ön koşul: 503 unavailable, yazım/audit
	//    yok, peer'ın yeni blobu korunur (eski kod: bellekteki bayat bloba karşı
	//    200, audit "precondition":"updatedAt", peer'ın yazımı kayıp). Ön
	//    koşulsuz PUT'ta logla-sür (v0.10.974) korunur.
	SetArgoCDSettings(argocd.NewSettingsService())
	w = e.do(t, "PUT", "/api/settings/argocd", body(""), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("pod B taban: %d %s", w.Code, w.Body)
	}
	base := stampOf(w)
	_ = e.audits()
	var newer argocd.Settings
	if err := json.Unmarshal(e.store.rows[argocd.SettingsKey], &newer); err != nil {
		t.Fatal(err)
	}
	newer.UpdatedAt, newer.EnvList = base+1_000_000_000, []string{"from-pod-a"} // peer daha yeni yazdı
	e.store.rows[argocd.SettingsKey], _ = json.Marshal(newer)
	e.store.getErr = fmt.Errorf("ch down")
	puts = e.store.puts
	w = e.do(t, "PUT", "/api/settings/argocd", body(fmt.Sprintf(`"expectedUpdatedAt":%d,`, base)), auth.RoleAdmin)
	if w.Code != http.StatusServiceUnavailable || argoJSON(t, w)["errorType"] != "unavailable" {
		t.Fatalf("depo okunamadı + ön koşul → 503 unavailable: %d %s", w.Code, w.Body)
	}
	if e.store.puts != puts || len(e.audits()) != 0 || argocdSettingsSvc.Load().Current().UpdatedAt != base { // e.svc pod A'nın servisi; pod B'ninki bağlı olan
		t.Fatal("503 yazmamalı / audit etmemeli / canlıyı değiştirmemeli")
	}
	if got := argocdEnvListOf(t, e.store.rows[argocd.SettingsKey]); len(got) != 1 || got[0] != "from-pod-a" {
		t.Fatalf("peer'ın yeni blobu ezilmemeli: %v", got)
	}
	w = e.do(t, "PUT", "/api/settings/argocd", body(""), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("ön koşulsuz PUT depo hatasında logla-sür kalmalı: %d %s", w.Code, w.Body)
	}
	if rows = e.audits(); len(rows) != 1 || !strings.Contains(rows[0].Details, `"precondition":"none"`) {
		t.Fatalf("ön koşulsuz audit: %+v", rows)
	}
	e.store.getErr = nil
}

// argocdEnvListOf — sahte depodaki blobun envList'i (v0.10.978 adım 6).
func argocdEnvListOf(t *testing.T, raw []byte) []string {
	t.Helper()
	var s argocd.Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("blob JSON değil: %v", err)
	}
	return s.EnvList
}

// argoGatedStore — v0.10.978 — PutSetting'i ZAMANLI kapıyla tutan sahte depo:
// ilk çağrı ya ikinci çağrı gelene ya da ~300 ms geçene dek bekler, ikinci
// çağrı kapıyı açar. Sabit "iki çağrı gelsin" kapısı argocdPutMu altında
// KİLİTLENİRDİ (B, A kilidi PutSetting içinde tutarken PutSetting'e hiç
// ulaşamaz); zaman aşımı kapıyı her iki kodda da açar.
type argoGatedStore struct {
	*argoFakeStore
	mu    sync.Mutex
	calls int
	once  sync.Once
	first chan struct{}
}

func (g *argoGatedStore) PutSetting(ctx context.Context, key string, v []byte) error {
	g.mu.Lock()
	g.calls++
	n := g.calls
	g.mu.Unlock()
	if n == 1 {
		select {
		case <-g.first:
		case <-time.After(300 * time.Millisecond):
		}
	} else {
		g.once.Do(func() { close(g.first) })
	}
	return g.argoFakeStore.PutSetting(ctx, key, v)
}

// v0.10.978 — ön koşul pod içinde ATOMİK (argocdPutMu): aynı expectedUpdatedAt'i
// taşıyan iki eşzamanlı PUT'tan yalnız biri 200 alır, öteki 409; tek yazım
// (tohum + kazanan), tek audit, canlı ayarda yalnız kazananın düzenlemesi.
//
// Eski kod bu testi geçemez: A kapılı PutSetting'te beklerken B karşılaştırmayı
// bellekteki (hâlâ t1) bloba karşı geçer, ikisi de yazar → 200/200, puts=3,
// A'nın düzenlemesi B'ninkiyle sessizce ezilir.
func TestArgoCDSettingsPutSerialisedPerPod(t *testing.T) {
	e := newArgoTestEnv(t)
	hub := argoClusterID(argoHubName)
	body := func(pre string) string {
		return `{` + pre + `"hubs":[{"clusterId":"` + hub + `"}],"instances":[{"id":"team-a-prod","hubNamespace":"team-a-prod"}]}`
	}
	w := e.do(t, "PUT", "/api/settings/argocd", body(""), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("tohum: %d %s", w.Code, w.Body)
	}
	t1 := e.svc.Current().UpdatedAt
	_ = e.audits()
	gated := &argoGatedStore{argoFakeStore: e.store, first: make(chan struct{})}
	argocdSettingsStoreOf = func(*Server) argocd.Store { return gated } // env cleanup geri koyar

	var wg sync.WaitGroup
	var wA, wB *httptest.ResponseRecorder
	wg.Add(1)
	go func() {
		defer wg.Done()
		wA = e.do(t, "PUT", "/api/settings/argocd", body(fmt.Sprintf(`"expectedUpdatedAt":%d,"enabled":true,`, t1)), auth.RoleAdmin)
	}()
	time.Sleep(20 * time.Millisecond) // A kilidi alıp kapıda beklesin
	wB = e.do(t, "PUT", "/api/settings/argocd", body(fmt.Sprintf(`"expectedUpdatedAt":%d,"envList":["prod"],`, t1)), auth.RoleAdmin)
	wg.Wait()

	codes := []int{wA.Code, wB.Code}
	sort.Ints(codes)
	if codes[0] != http.StatusOK || codes[1] != http.StatusConflict {
		t.Fatalf("tam bir 200 + bir 409 beklenir: A=%d B=%d (%s | %s)", wA.Code, wB.Code, wA.Body, wB.Body)
	}
	if e.store.puts != 2 || gated.calls != 1 {
		t.Fatalf("tohum + kazanan = 2 yazım (puts=%d, kapılı çağrı=%d)", e.store.puts, gated.calls)
	}
	if rows := e.audits(); len(rows) != 1 || !strings.Contains(rows[0].Details, `"precondition":"updatedAt"`) {
		t.Fatalf("tek audit (kazanan): %+v", rows)
	}
	cur := e.svc.Current()
	aWon := wA.Code == http.StatusOK
	if cur.Enabled != aWon || (len(cur.EnvList) == 1) != !aWon || cur.UpdatedAt == t1 {
		t.Fatalf("canlıda yalnız kazananın düzenlemesi olmalı (A kazandı=%v): enabled=%v envList=%v", aWon, cur.Enabled, cur.EnvList)
	}
	// 409 gövdesi kazananın damgasını taşır (FE yeniden yükler).
	loser := wB
	if !aWon {
		loser = wA
	}
	var lb struct { // int64: map[string]any float64'e yuvarlar (put.go sameStamp)
		ErrorType string `json:"errorType"`
		UpdatedAt int64  `json:"updatedAt"`
	}
	if err := json.Unmarshal(loser.Body.Bytes(), &lb); err != nil || lb.ErrorType != "stale" || lb.UpdatedAt != cur.UpdatedAt {
		t.Fatalf("kaybeden 409 gövdesi: %v %s", err, loser.Body)
	}
}

// BE3: kayıtlı yuvayı yeni kimlikle almak 400 instances[0].id; yazım ve
// settings.argocd.update audit'i YOK.
func TestArgoCDSettingsPutRenameRejected(t *testing.T) {
	e := newArgoTestEnv(t)
	hub := argoClusterID(argoHubName)
	if w := e.do(t, "PUT", "/api/settings/argocd", `{"hubs":[{"clusterId":"`+hub+`"}],"instances":[{"id":"team-a-prod","hubNamespace":"team-a-prod"}]}`, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("ilk kayıt: %d %s", w.Code, w.Body)
	}
	e.audits()
	puts := e.store.puts
	w := e.do(t, "PUT", "/api/settings/argocd", `{"hubs":[{"clusterId":"`+hub+`"}],"instances":[{"id":"team-a-gitops","hubNamespace":"team-a-prod"}]}`, auth.RoleAdmin)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("yeniden adlandırma → 400: %d %s", w.Code, w.Body)
	}
	m := argoJSON(t, w)
	msg, _ := m["error"].(string)
	if m["field"] != "instances[0].id" || !strings.HasPrefix(msg, "instances[0].id: ") ||
		!strings.Contains(msg, `"team-a-gitops" kayıtlı "team-a-prod" instance'ının yerini alıyor (`+argoHubName+`/team-a-prod)`) {
		t.Fatalf("gövde: %v", m)
	}
	if e.store.puts != puts || len(e.audits()) != 0 || e.svc.Current().Instances[0].ID != "team-a-prod" {
		t.Fatal("reddedilen yeniden adlandırma yazmamalı / audit etmemeli")
	}
}

// BE4: bağlı instance'ı olan hub kaldırılamaz (400, yazım yok, audit yok);
// aynı PUT'ta instance'ları taşımak kaldırmayı serbest bırakır.
func TestArgoCDSettingsPutHubRemoval(t *testing.T) {
	e := newArgoTestEnv(t)
	hubA, hubB := argoClusterID(argoHubName), argoClusterID(argoTargetName)
	two := `{"hubs":[{"clusterId":"` + hubA + `"},{"clusterId":"` + hubB + `"}],"instances":[
	  {"id":"team-a-prod","hubClusterId":"` + hubA + `","hubNamespace":"team-a-prod"},
	  {"id":"team-c-prod","hubClusterId":"` + hubB + `","hubNamespace":"team-c-prod"}]}`
	if w := e.do(t, "PUT", "/api/settings/argocd", two, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("iki hub: %d %s", w.Code, w.Body)
	}
	e.audits()
	puts := e.store.puts
	w := e.do(t, "PUT", "/api/settings/argocd", `{"hubs":[{"clusterId":"`+hubA+`"}],"instances":[
	  {"id":"team-a-prod","hubClusterId":"`+hubA+`","hubNamespace":"team-a-prod"},
	  {"id":"team-c-prod","hubClusterId":"`+hubB+`","hubNamespace":"team-c-prod"}]}`, auth.RoleAdmin)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bağlı hub kaldırma → 400: %d %s", w.Code, w.Body)
	}
	m := argoJSON(t, w)
	want := `instances[1].hubClusterId: hub "` + argoTargetName + `" (` + hubB + `) listeden çıkarılıyor ama 1 instance ona bağlı — ` +
		`instance'ları başka hub'a taşıyın ya da hub'ı listede bırakın`
	if m["field"] != "instances[1].hubClusterId" || m["error"] != want {
		t.Fatalf("gövde:\n got %v\nwant %s", m, want)
	}
	if e.store.puts != puts || len(e.audits()) != 0 || len(e.svc.Current().Hubs) != 2 {
		t.Fatal("reddedilen hub kaldırma yazmamalı / audit etmemeli")
	}
	w = e.do(t, "PUT", "/api/settings/argocd", `{"hubs":[{"clusterId":"`+hubA+`"}],"instances":[
	  {"id":"team-a-prod","hubClusterId":"`+hubA+`","hubNamespace":"team-a-prod"},
	  {"id":"team-c-prod","hubClusterId":"`+hubA+`","hubNamespace":"team-c-prod"}]}`, auth.RoleAdmin)
	if w.Code != http.StatusOK || len(e.svc.Current().Hubs) != 1 {
		t.Fatalf("taşı + kaldır aynı PUT'ta → 200: %d %s", w.Code, w.Body)
	}
}

// Sayım hatası 200'ü bozmaz: aday kalır, error/incomplete yok, sayı nil + not.
func TestArgoCDDiscoverCountFailureKeeps200(t *testing.T) {
	e := newArgoTestEnv(t)
	seedArgoFake(e.fake)
	hub := argoClusterID(argoHubName)
	e.fake.fail = func(r argoFakeReq) (int, string) {
		switch {
		case r.Path == "/api/v1/query":
			return http.StatusServiceUnavailable, `{"status":"error","errorType":"timeout","error":"query timed out at ` + e.fake.URL + `"}`
		case r.Path == "/api/v1/label/pod/values" && strings.Contains(strings.Join(r.Form["match[]"], " "), "team-a-prod-metrics"):
			return http.StatusInternalServerError, `{"status":"error","errorType":"internal","error":"boom"}`
		}
		return 0, ""
	}
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+hub+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("yalnız sayım düştü → 200: %d %s", w.Code, w.Body)
	}
	assertArgoNoLeak(t, w.Body.String(), e.fake.URL)
	var res argocdDiscoverResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 2 || res.Incomplete || res.CountsIncomplete || res.Calls != 9 {
		t.Fatalf("zarf: %+v", res)
	}
	a, b := res.Candidates[0], res.Candidates[1]
	if a.Error != "" || a.AppCount != nil || a.ShardCount != nil || a.CountNote != "uygulama sayısı okunamadı: timeout; shard sayısı okunamadı: internal" {
		t.Errorf("A: %+v", a)
	}
	if b.Error != "" || b.AppCount != nil || b.CountNote != "uygulama sayısı okunamadı: timeout; pod etiketi yok" {
		t.Errorf("B: %+v", b)
	}
}

// Bütçe ikinci turda biter: aday listesi TAM (50 iş, hatasız, incomplete
// yok), kalan adaylar "sayım atlandı" notu, sonuç countsIncomplete; toplam
// çağrı ≤150 (1 + 50×2 aday turu = 101; sayıma 49 kalır). v0.10.990 —
// üretim tavanları 500 iş / 2000 çağrı; aritmetik burada küçük sayılarla
// ve sıralı kipte pinlenir.
func TestArgoCDDiscoverCountBudgetExhausted(t *testing.T) {
	e := newArgoTestEnv(t)
	argoDiscoverLimits(t, 50, 150, 1)
	hub := argoClusterID(argoHubName)
	var jobs []string
	for i := 0; i < argocdDiscoverMaxJobs; i++ {
		job, ns := fmt.Sprintf("job-%02d", i), fmt.Sprintf("ns-%02d", i)
		jobs = append(jobs, job)
		sel := `job="` + job + `"`
		e.fake.values["namespace"] = append(e.fake.values["namespace"], argoFakeRule{sel, []string{ns}})
		e.fake.values["exported_namespace"] = append(e.fake.values["exported_namespace"], argoFakeRule{sel, []string{ns}})
		e.fake.queries = append(e.fake.queries, argoFakeQueryRule{sel, `[{"metric":{"namespace":"` + ns + `"},"value":[1700000000,"7"]}]`})
	}
	e.fake.values["job"] = []argoFakeRule{{"argocd_app_info", jobs}}
	e.fake.values["pod"] = []argoFakeRule{{"argocd_app_info", []string{"argocd-application-controller-0"}}}
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+hub+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res argocdDiscoverResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Calls != argocdDiscoverMaxCalls || len(e.fake.requests()) != argocdDiscoverMaxCalls || res.Incomplete || !res.CountsIncomplete {
		t.Fatalf("zarf: calls=%d istek=%d incomplete=%v countsIncomplete=%v", res.Calls, len(e.fake.requests()), res.Incomplete, res.CountsIncomplete)
	}
	if len(res.Candidates) != argocdDiscoverMaxJobs {
		t.Fatalf("aday listesi tam olmalı: %d", len(res.Candidates))
	}
	for i, c := range res.Candidates {
		if c.Error != "" {
			t.Fatalf("sayım bütçesi adayı düşürmemeli / hata yazmamalı: %+v", c)
		}
		switch {
		case i < 24: // 24 iş × 2 çağrı = 48
			if c.AppCount == nil || *c.AppCount != 7 || c.ShardCount == nil || *c.ShardCount != 1 || c.CountNote != "" {
				t.Fatalf("aday %d tam sayılmalı: %+v", i, c)
			}
		case i == 24: // 150. çağrı count; pod'a bütçe kalmadı
			if c.AppCount == nil || c.ShardCount != nil || c.CountNote != argocd.CountNoteBudget {
				t.Fatalf("aday 24 yalnız uygulama sayısı: %+v", c)
			}
		default:
			if c.AppCount != nil || c.ShardCount != nil || c.CountNote != argocd.CountNoteBudget {
				t.Fatalf("aday %d atlanmalı: %+v", i, c)
			}
		}
	}
	if rows := e.audits(); len(rows) != 1 || !strings.Contains(rows[0].Details, `"countsIncomplete":true`) {
		t.Fatalf("audit countsIncomplete taşımalı: %+v", rows)
	}
}

// Hatalı aday (iş sorgusu düştü) hiç sayım çağrısı almaz.
func TestArgoCDDiscoverErrorCandidatesNoCountCalls(t *testing.T) {
	e := newArgoTestEnv(t)
	seedArgoFake(e.fake)
	hub := argoClusterID(argoHubName)
	e.fake.fail = func(r argoFakeReq) (int, string) {
		if strings.Contains(strings.Join(r.Form["match[]"], " "), `job="team-b-uat-metrics"`) {
			return http.StatusInternalServerError, `{"status":"error","errorType":"internal","error":"boom"}`
		}
		return 0, ""
	}
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+hub+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res argocdDiscoverResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	// 1 iş + team-a 2 + team-b 1 (düştü) + team-a sayım 2.
	if res.Calls != 6 || len(res.Candidates) != 2 {
		t.Fatalf("zarf: %+v", res)
	}
	if b := res.Candidates[1]; b.Error == "" || b.AppCount != nil || b.ShardCount != nil || b.CountNote != "" {
		t.Fatalf("hatalı aday sayım taşımaz: %+v", b)
	}
	if a := res.Candidates[0]; a.AppCount == nil || a.ShardCount == nil {
		t.Fatalf("sağlam aday sayılır: %+v", a)
	}
	for _, r := range e.fake.requests() {
		if strings.Contains(r.Form.Get("query"), "team-b-uat-metrics") ||
			(r.Path == "/api/v1/label/pod/values" && strings.Contains(strings.Join(r.Form["match[]"], " "), "team-b-uat-metrics")) {
			t.Fatalf("hatalı aday için sayım çağrısı yapıldı: %s %v", r.Path, r.Form)
		}
	}
}

// v0.10.978 — "yetki yok" (v0.10.974'te ertelenen karar, onaylandı): hub
// Thanos'un 401/403'ü artık genel "unavailable" değil, sourcestate sözlüğüyle
// `errorType: "unauthorized"` + `upstreamStatus` (401|403) + `hubClusterId`
// taşır; HTTP kodu 502 KALIR (424 bu depoda upstream kimlik reddi için
// kullanılmıyor — rollup_routes'ta "tablo yok"). Gövde ve audit satırı
// yapılandırılmış URL'yi, host'u ya da çözülmüş token'ı ASLA taşımaz; audit
// yine tek satır (settings.argocd.discover) — şekli değişmez.
//
// Eski kod bu testi geçemez: errorType "unavailable", upstreamStatus /
// hubClusterId alanları yok, aday hatası "unavailable: …" öneklidir.
func TestArgoCDDiscoverHubUnauthorized(t *testing.T) {
	const secret = "sa-token-978-never-echoed"
	for _, tc := range []struct {
		name string
		code int
		body string // JSON'suz (oauth-proxy HTML'i) ya da JSON errorType'lı: HTTP kodu kazanır
	}{
		{"403 html", http.StatusForbidden, "<html>Forbidden</html>"},
		{"401 html", http.StatusUnauthorized, "<html>Unauthorized</html>"},
		{"403 json bad_data", http.StatusForbidden, `{"status":"error","errorType":"bad_data","error":"forbidden namespace at ` + "%URL%" + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newArgoTestEnv(t)
			hub := argoClusterID(argoHubName)
			e.s.thanos.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{
				{Name: argoHubName, URL: e.fake.URL, ThanosLabelName: "cluster", Enabled: true, Token: secret},
			}})
			e.fake.fail = func(argoFakeReq) (int, string) {
				return tc.code, strings.ReplaceAll(tc.body, "%URL%", e.fake.URL)
			}
			w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+hub+`"}`, auth.RoleAdmin)
			if w.Code != http.StatusBadGateway {
				t.Fatalf("hub %d → 502 kalır: %d %s", tc.code, w.Code, w.Body)
			}
			body := w.Body.String()
			assertArgoNoLeak(t, body, e.fake.URL)
			if strings.Contains(body, secret) {
				t.Fatalf("gövde token'ı yankılıyor: %s", body)
			}
			m := argoJSON(t, w)
			if m["errorType"] != "unauthorized" || m["upstreamStatus"] != float64(tc.code) || m["hubClusterId"] != hub || m["error"] == "" {
				t.Fatalf("hata gövdesi: %v", m)
			}
			if tc.body[0] == '<' {
				// FE sözleşmesi (argocdDiscovery.ts parseDiscoverError / failureState) bu şekle pinli.
				want := fmt.Sprintf(`{"error":"thanos rejected the cluster credentials (HTTP %d)","errorType":"unauthorized","upstreamStatus":%d,"hubClusterId":"%s"}`, tc.code, tc.code, hub)
				if got := strings.TrimSpace(body); got != want {
					t.Fatalf("gövde:\n got %s\nwant %s", got, want)
				}
			}
			rows := e.audits()
			if len(rows) != 1 || rows[0].Action != "settings.argocd.discover" || rows[0].TargetKind != "settings" || rows[0].TargetID != argocd.SettingsKey {
				t.Fatalf("audit tek satır ve şekli değişmez: %+v", rows)
			}
			d := rows[0].Details
			if !strings.Contains(d, `"status":502`) || !strings.Contains(d, `"errorType":"unauthorized"`) ||
				strings.Contains(d, secret) || strings.Contains(d, e.fake.URL) || strings.Contains(d, "upstreamStatus") {
				t.Fatalf("audit details: %s", d)
			}
			if len(e.fake.requests()) != 1 {
				t.Fatalf("iş listesi reddedilince tur durur: %d istek", len(e.fake.requests()))
			}
		})
	}

	// Yalnız bir işin label-values çağrısı 403 → 200 ve o aday "unauthorized: …"
	// öneki taşır (FE ERR_TR); tur ve öteki aday etkilenmez, sayım turu da
	// 403'ü aynı sözlükle not eder.
	t.Run("per-job 403", func(t *testing.T) {
		e := newArgoTestEnv(t)
		seedArgoFake(e.fake)
		hub := argoClusterID(argoHubName)
		e.fake.fail = func(r argoFakeReq) (int, string) {
			sel := strings.Join(r.Form["match[]"], " ")
			switch {
			case r.Path != "/api/v1/query" && strings.Contains(sel, `job="team-b-uat-metrics"`):
				return http.StatusForbidden, "<html>Forbidden</html>"
			case r.Path == "/api/v1/query" && strings.Contains(r.Form.Get("query"), `job="team-a-prod-metrics"`):
				return http.StatusUnauthorized, `{"status":"error","errorType":"unavailable","error":"token expired at ` + e.fake.URL + `"}`
			}
			return 0, ""
		}
		w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+hub+`"}`, auth.RoleAdmin)
		if w.Code != http.StatusOK {
			t.Fatalf("tek iş 403 → 200 kalır: %d %s", w.Code, w.Body)
		}
		assertArgoNoLeak(t, w.Body.String(), e.fake.URL)
		var res argocdDiscoverResult
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Candidates) != 2 || res.Incomplete {
			t.Fatalf("zarf: %+v", res)
		}
		if a := res.Candidates[0]; a.Error != "" || a.AppCount != nil || !strings.HasPrefix(a.CountNote, argocd.AppCountFailNote("unauthorized")) {
			t.Errorf("A: sayım 401'i unauthorized diye not eder: %+v", a)
		}
		if b := res.Candidates[1]; b.MetricsJob != "team-b-uat-metrics" || b.Error != "unauthorized: thanos rejected the cluster credentials (HTTP 403)" {
			t.Errorf("B: aday hatası unauthorized önekli: %+v", b)
		}
	})
}

// v0.10.974 — sayım sonucu MaxSeries (argocdCountMaxSeries = 101) tavanında
// kesilirse kesilen namespace'in adayı sayı almaz, "kesildi" notu alır; bu bir
// bütçe durumu değildir (countsIncomplete / incomplete yok). MaxSeries
// ConsoleLimits'ten düşerse varsayılan 500 satırın hepsi okunur ve aday yanlış
// bir sayı taşırdı — bu test o gerilemeyi yakalar.
func TestArgoCDDiscoverCountTruncated(t *testing.T) {
	e := newArgoTestEnv(t)
	seedArgoFake(e.fake)
	rows := make([]string, 0, argocdCountMaxSeries+1)
	for i := 0; i < argocdCountMaxSeries; i++ {
		rows = append(rows, fmt.Sprintf(`{"metric":{"namespace":"ns-%03d"},"value":[1700000000,"1"]}`, i))
	}
	rows = append(rows, `{"metric":{"namespace":"team-a-prod"},"value":[1700000000,"1184"]}`) // 102. satır: kesilir
	e.fake.queries = append([]argoFakeQueryRule{{`job="team-a-prod-metrics"`, "[" + strings.Join(rows, ",") + "]"}}, e.fake.queries...)
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+argoClusterID(argoHubName)+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res argocdDiscoverResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 2 || res.Incomplete || res.CountsIncomplete {
		t.Fatalf("zarf (kesik sayım bütçe değildir): %+v", res)
	}
	a := res.Candidates[0]
	if a.Error != "" || a.AppCount != nil || a.CountNote != argocd.AppCountFailNote("sonuç seri tavanında kesildi") {
		t.Fatalf("A: kesilen namespace sayı almaz, not alır: %+v", a)
	}
	if a.ShardCount == nil || *a.ShardCount != 3 {
		t.Fatalf("A: shard sayımı etkilenmez: %+v", a)
	}
}

// v0.10.974 — bağlam sayım turunda biterse (istemci koptu / 60 sn doldu) kalan
// adaylar "okunamadı: canceled" DEĞİL, tam olarak bütçe notu alır ve sonuç
// countsIncomplete taşır (spec: "budget or context is exhausted"); aday
// listesi tam, 200 bozulmaz. countsIncomplete tek başına yetmez — bağlam
// dalı silinse de sonraki çağrının bütçe denetimi bayrağı yine kurardı; bu
// yüzden NOT birebir karşılaştırılır.
func TestArgoCDDiscoverCountCtxExhausted(t *testing.T) {
	e := newArgoTestEnv(t)
	seedArgoFake(e.fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.fake.fail = func(r argoFakeReq) (int, string) {
		if r.Path != "/api/v1/query" {
			return 0, ""
		}
		cancel() // yanıttan ÖNCE: bağlam iptali deterministik
		return http.StatusServiceUnavailable, `{"status":"error","errorType":"unavailable","error":"down"}`
	}
	req := httptest.NewRequest("POST", "/api/settings/argocd/discover", strings.NewReader(`{"hubClusterId":"`+argoClusterID(argoHubName)+`"}`))
	req = req.WithContext(auth.ContextWithClaims(ctx, &auth.Claims{UserID: "u-admin", Email: "admin@example.test", Role: auth.RoleAdmin}))
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sayım turunda bağlam bitti → yine 200: %d %s", w.Code, w.Body)
	}
	var res argocdDiscoverResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.CountsIncomplete || res.Incomplete || len(res.Candidates) != 2 {
		t.Fatalf("zarf: countsIncomplete=%v incomplete=%v aday=%d", res.CountsIncomplete, res.Incomplete, len(res.Candidates))
	}
	for i, c := range res.Candidates {
		if c.Error != "" || c.AppCount != nil || c.ShardCount != nil || c.CountNote != argocd.CountNoteBudget {
			t.Fatalf("aday %d: tam olarak bütçe notu beklenir (okunamadı değil): %+v", i, c)
		}
	}
}

// argoSeedJobs — n ayrı iş (instance başına bir `job`), her biri tek
// namespace (durum A), 7 uygulama, 1 shard.
func argoSeedJobs(f *argoFakeThanos, n int) {
	var jobs []string
	for i := 0; i < n; i++ {
		job, ns := fmt.Sprintf("job-%03d", i), fmt.Sprintf("ns-%03d", i)
		jobs = append(jobs, job)
		sel := `job="` + job + `"`
		f.values["namespace"] = append(f.values["namespace"], argoFakeRule{sel, []string{ns}})
		f.values["exported_namespace"] = append(f.values["exported_namespace"], argoFakeRule{sel, []string{ns}})
		f.queries = append(f.queries, argoFakeQueryRule{sel, `[{"metric":{"namespace":"` + ns + `"},"value":[1700000000,"7"]}]`})
	}
	f.values["job"] = []argoFakeRule{{"argocd_app_info", jobs}}
	f.values["pod"] = []argoFakeRule{{"argocd_app_info", []string{"argocd-application-controller-0"}}}
}

// v0.10.990 — Operator-reported: "sadece ilk 50'yi bulduğu için eksikleri
// oluyor". Instance başına ayrı `job` taşıyan hub'da 50'den fazla iş vardı;
// eski tavan (50 iş, 150 çağrı) 51. instance'ı hiç aday yapmıyordu. 120 iş
// üretim tavanları ve eşzamanlı kiple TAM keşfedilir: kesik yok, eksik yok,
// her aday sayılı, aday sırası iş sırası, çağrı sayısı = giden istek.
func TestArgoCDDiscoverManyJobsParallel(t *testing.T) {
	e := newArgoTestEnv(t)
	argoDiscoverLimits(t, 500, 2000, 4)
	const n = 120
	argoSeedJobs(e.fake, n)
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+argoClusterID(argoHubName)+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res argocdDiscoverResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.JobsTruncated || res.Incomplete || res.CountsIncomplete {
		t.Fatalf("120 iş tavanların altında: truncated=%v incomplete=%v countsIncomplete=%v", res.JobsTruncated, res.Incomplete, res.CountsIncomplete)
	}
	if len(res.Candidates) != n {
		t.Fatalf("her iş bir aday olmalı: %d / %d", len(res.Candidates), n)
	}
	// 1 iş listesi + iş başına 2 (namespace, exported_namespace) + 1 count + 1 pod.
	if want := 1 + n*4; res.Calls != want || len(e.fake.requests()) != want {
		t.Fatalf("calls=%d istek=%d, istenen %d", res.Calls, len(e.fake.requests()), want)
	}
	for i, c := range res.Candidates {
		job, ns := fmt.Sprintf("job-%03d", i), fmt.Sprintf("ns-%03d", i)
		if c.MetricsJob != job || c.HubNamespace != ns || c.Error != "" || c.NamespaceCase != "A" {
			t.Fatalf("aday %d sırası/şekli: %+v", i, c)
		}
		if c.AppCount == nil || *c.AppCount != 7 || c.ShardCount == nil || *c.ShardCount != 1 || c.CountNote != "" {
			t.Fatalf("aday %d sayılmalı: %+v", i, c)
		}
	}
}

// v0.10.990 — eşzamanlı kipte bütçe: ayırma ile sayım tek kilit altında,
// yani tavan aşılmaz ve calls gerçekten giden istek sayısıdır. Hiçbir iş
// düşmez: bütçeye sığmayan iş "skipped" hatalı aday olur, sonuç incomplete.
func TestArgoCDDiscoverParallelBudgetNeverExceeded(t *testing.T) {
	e := newArgoTestEnv(t)
	const n, maxCalls = 60, 100
	argoDiscoverLimits(t, 500, maxCalls, 4)
	argoSeedJobs(e.fake, n)
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+argoClusterID(argoHubName)+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res argocdDiscoverResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Calls > maxCalls || res.Calls != len(e.fake.requests()) {
		t.Fatalf("bütçe: calls=%d istek=%d tavan=%d", res.Calls, len(e.fake.requests()), maxCalls)
	}
	if !res.Incomplete || len(res.Candidates) != n {
		t.Fatalf("zarf: incomplete=%v aday=%d", res.Incomplete, len(res.Candidates))
	}
	skipped := 0
	for _, c := range res.Candidates {
		if strings.Contains(c.Error, "budget exhausted") {
			skipped++
		}
	}
	// 1 + 2×k ≤ 100 → en çok 49 iş probe edilir; kalan en az 11 iş atlanır.
	if skipped < n-49 {
		t.Fatalf("atlanan iş sayısı: %d (en az %d beklenir)", skipped, n-49)
	}
}

// v0.10.997 — Operator-reported: "Hub kaldıramıyorum instance varsa". Arayüz
// artık hub'ı instance'larıyla birlikte tek PUT'ta çıkarıyor; sunucunun bunu
// KABUL ettiği burada pinli (BE4 yalnız "hub çıkıyor, instance kalıyor"u
// reddeder). Prod senaryosu ayrıca: çıkarılan hub'ın Remote Cluster kaydı
// çoktan silinmiş ("bilinmeyen kayıt") — o hub da instance'larıyla çıkarılabilir.
func TestArgoCDSettingsPutHubRemovedWithItsInstances(t *testing.T) {
	e := newArgoTestEnv(t)
	hubA, hubB := argoClusterID(argoHubName), argoClusterID(argoTargetName)
	var insts []string
	for i := 0; i < 190; i++ {
		insts = append(insts, fmt.Sprintf(`{"id":"team-%03d-2","hubClusterId":"%s","hubNamespace":"team-%03d","metricsJob":"team-%03d-metrics","enabled":true,"discovered":true}`, i, hubB, i, i))
	}
	keep := `{"id":"team-a-prod","hubClusterId":"` + hubA + `","hubNamespace":"team-a-prod","enabled":true}`
	two := `{"enabled":true,"hubs":[{"clusterId":"` + hubA + `"},{"clusterId":"` + hubB + `"}],"instances":[` + keep + `,` + strings.Join(insts, ",") + `]}`
	if w := e.do(t, "PUT", "/api/settings/argocd", two, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("iki hub + 191 instance: %d %s", w.Code, w.Body)
	}
	if n := len(e.svc.Current().Instances); n != 191 {
		t.Fatalf("191 instance kayıtlı olmalı: %d", n)
	}
	// Hub B'nin Remote Cluster kaydı SİLİNDİ (prod: "bilinmeyen kayıt").
	e.s.thanos.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{
		{Name: argoHubName, URL: e.fake.URL, ThanosLabelName: "cluster", Enabled: true},
	}})
	one := `{"enabled":true,"hubs":[{"clusterId":"` + hubA + `"}],"instances":[` + keep + `]}`
	w := e.do(t, "PUT", "/api/settings/argocd", one, auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("hub + 190 instance'ı tek PUT'ta çıkarma → 200: %d %s", w.Code, w.Body)
	}
	cur := e.svc.Current()
	if len(cur.Hubs) != 1 || cur.Hubs[0].ClusterID != hubA || len(cur.Instances) != 1 || cur.Instances[0].ID != "team-a-prod" {
		t.Fatalf("kalan: hubs=%+v instances=%d", cur.Hubs, len(cur.Instances))
	}
}
