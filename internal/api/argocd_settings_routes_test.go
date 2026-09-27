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
// çağrı ≤150 (1 + 50×2 aday turu = 101; sayıma 49 kalır).
func TestArgoCDDiscoverCountBudgetExhausted(t *testing.T) {
	e := newArgoTestEnv(t)
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

// FE'nin "Thanos kimlik bilgisini reddetti" eşlemesi bu metne dayanır: hub
// Thanos 401/403 (JSON'suz gövde, ör. oauth-proxy) → 502 unavailable + sabit
// metin. Davranış değişmedi; metin pinlendi.
func TestArgoCDDiscoverHub403TextPinned(t *testing.T) {
	e := newArgoTestEnv(t)
	e.fake.fail = func(argoFakeReq) (int, string) { return http.StatusForbidden, "<html>Forbidden</html>" }
	w := e.do(t, "POST", "/api/settings/argocd/discover", `{"hubClusterId":"`+argoClusterID(argoHubName)+`"}`, auth.RoleAdmin)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("hub 403 → 502: %d %s", w.Code, w.Body)
	}
	const want = `{"error":"thanos rejected the cluster credentials (HTTP 403)","errorType":"unavailable"}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Fatalf("gövde:\n got %s\nwant %s", got, want)
	}
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
