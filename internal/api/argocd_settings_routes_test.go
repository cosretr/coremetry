package api

// v0.10.957 — Argo CD ayar yüzeyi (Rollouts v2 P1.4) HTTP sözleşmesi, SAHTE
// Thanos'a karşı (httptest; canlı çağrı YOK). Kapsam: kayıt defteri + rol
// kapısı (yalnız admin), GET varsayılanlar/bounds/token durumu, PUT
// gidiş-dönüşü (sahte depo) + audit + alan yollu 400'ler + düz token reddi,
// keşif probe'u (hub yok → 400 guardrail, kaydetmez, label-values
// parametreleri, küme etiketi enjeksiyon anahtarı, upstream hatası URL
// sızdırmadan eşlenir, meşgul kapısı) ve kablo pinleri (reload case,
// config-import listesi, main.go boot + 30 s yenileme).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// ── Sahte Thanos (yalnız label values) ─────────────────────────────────────

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
	fail   func(req argoFakeReq) (int, string) // 0 = başarı
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
	mu   sync.Mutex
	rows map[string][]byte
	puts int
}

func (f *argoFakeStore) GetSetting(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	mux := http.NewServeMux()
	s.registerArgoCDSettingsRoutes(mux)
	return &argoTestEnv{s: s, mux: mux, svc: svc, store: st, fake: f}
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
	if res.HubClusterID != hub || res.HubName != argoHubName || !res.InjectClusterLabel || res.Saved || res.Calls != 5 {
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
	// Salt-okunur: depo dokunulmadı, canlı ayar aynı.
	if e.store.puts != 0 || len(e.svc.Current().Instances) != 1 {
		t.Fatal("keşif KAYDETMEMELİ")
	}
	// Upstream: yalnız label values GET, sınırlı, pencereli, partial_response=false,
	// her seçici argocd_app_info + hub'ın küme matcher'ı.
	for _, r := range e.fake.requests() {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.Path, "/api/v1/label/") {
			t.Errorf("yalnız label-values GET beklenir: %s %s", r.Method, r.Path)
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
	rows := e.audits()
	if len(rows) != 1 || rows[0].Action != "settings.argocd.discover" || !strings.Contains(rows[0].Details, `"candidates":2`) {
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
	for _, r := range reqs {
		for _, m := range r.Form["match[]"] {
			if strings.Contains(m, "cluster=") {
				t.Fatalf("injectClusterLabel=false iken matcher enjekte edildi: %s", m)
			}
		}
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
