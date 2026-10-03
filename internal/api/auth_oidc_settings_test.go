package api

// auth_oidc_settings_test.go — v0.10.1067 (OIDC Settings'ten yönetilir)
// HTTP sözleşmesi, SAHTE IdP'ye karşı (httptest loopback; dış ağ YOK).
//
// NE ÇİVİLİYOR: rotalar defterden kayıtlı (api.go'da yok); üç uç da
// yalnız admin (viewer/editor 403, kimliksiz 401, yan etkisiz); GET ve PUT
// cevabı secret taşımaz; boş secret'lı PUT kayıtlıyı korur; audit satırı
// secret taşımaz; keşfi bozuk PUT 400 ve yazmaz, enabled:false her zaman
// 200; başarılı PUT canlı istemciyi takaslar (public /api/auth/config
// düğmesi aynı istekte değişir) ve config:oidc yayınlar; test ucu hiçbir
// şey yazmaz, hatada 200+{ok:false} ve IdP gövdesini sızdırmaz.

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
)

const oidcTestSecret = "api-secret-never-echo"

type oidcFakeStore struct {
	mu   sync.Mutex
	rows map[string][]byte
	puts int
}

func (f *oidcFakeStore) GetSetting(_ context.Context, k string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[k], nil
}

func (f *oidcFakeStore) PutSetting(_ context.Context, k string, v []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	f.rows[k] = append([]byte(nil), v...)
	return nil
}

// oidcRecCache — Publish'i kaydeden noop önbellek.
type oidcRecCache struct {
	cache.Cache
	mu  sync.Mutex
	pub []string
}

func (c *oidcRecCache) Publish(_ context.Context, _ string, msg []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pub = append(c.pub, string(msg))
	return nil
}

type oidcTestEnv struct {
	s     *Server
	mux   *http.ServeMux
	store *oidcFakeStore
	cache *oidcRecCache
	idp   *httptest.Server
	idpOK *atomic.Bool
}

func newOIDCTestEnv(t *testing.T) *oidcTestEnv {
	t.Helper()
	var ok atomic.Bool
	ok.Store(true)
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"TOKEN-BODY-LEAK"}`))
			return
		}
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		if !ok.Load() {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("IDP-BODY-MUST-NOT-LEAK"))
			return
		}
		base := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": base, "authorization_endpoint": base + "/authorize",
			"token_endpoint": base + "/token", "jwks_uri": base + "/jwks",
		})
	}))
	t.Cleanup(idp.Close)
	st := &oidcFakeStore{rows: map[string][]byte{}}
	noop, _ := cache.NewNoop()
	rc := &oidcRecCache{Cache: noop}
	svc := auth.NewOIDCService(config.OIDCConfig{AllowInsecureIssuer: true}, "https://apm.example.test", st) // httptest http://127.0.0.1
	if err := svc.LoadPersisted(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := &Server{cache: rc, l1: newL1Cache(64), stats: newCacheStats(),
		auditQ: make(chan chstore.AuditEntry, 64), oidc: svc}
	mux := http.NewServeMux()
	s.registerOIDCSettingsRoutes(mux)
	return &oidcTestEnv{s: s, mux: mux, store: st, cache: rc, idp: idp, idpOK: &ok}
}

func (e *oidcTestEnv) do(t *testing.T, method, path, body, role string) *httptest.ResponseRecorder {
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

// loginConfig — giriş sayfasının okuduğu public yapılandırma (authConfig
// handler'ı doğrudan; rota burada YENİDEN kaydedilmez, make audit CHECK 7).
func (e *oidcTestEnv) loginConfig() *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.s.authConfig(w, httptest.NewRequest("GET", "/", nil))
	return w
}

func (e *oidcTestEnv) audits() []chstore.AuditEntry {
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

func (e *oidcTestEnv) body(secret string) string {
	b, _ := json.Marshal(map[string]any{
		"enabled": true, "issuerUrl": e.idp.URL, "clientId": "coremetry", "clientSecret": secret,
		"scopes": []string{"openid", "email"}, "displayName": "Kurumsal SSO", "defaultRole": "viewer",
		"allowedDomains": []string{"Example.TEST"},
	})
	return string(b)
}

func TestOIDCSettingsRoutesRegisteredViaRegistry(t *testing.T) {
	src, err := os.ReadFile("auth_oidc_settings.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoComments(string(src)), `registerRoutesExtra("auth-oidc-settings", (*Server).registerOIDCSettingsRoutes)`) {
		t.Fatal("auth_oidc_settings.go init() defter kaydı yok")
	}
	if b, _ := os.ReadFile("api.go"); strings.Contains(string(b), "/api/settings/oidc") {
		t.Fatal("api.go /api/settings/oidc içeriyor — api.go büyümez")
	}
	mux := (&Server{}).buildMux()
	for _, p := range []struct{ m, path, want string }{
		{"GET", "/api/settings/oidc", "GET /api/settings/oidc"},
		{"PUT", "/api/settings/oidc", "PUT /api/settings/oidc"},
		{"POST", "/api/settings/oidc/test", "POST /api/settings/oidc/test"},
	} {
		if _, pat := mux.Handler(httptest.NewRequest(p.m, p.path, nil)); pat != p.want {
			t.Errorf("%s %s → %q, beklenen %q", p.m, p.path, pat, p.want)
		}
	}
}

func TestOIDCSettingsRoutesAdminOnly(t *testing.T) {
	e := newOIDCTestEnv(t)
	for _, r := range []struct{ m, path string }{
		{"GET", "/api/settings/oidc"}, {"PUT", "/api/settings/oidc"}, {"POST", "/api/settings/oidc/test"},
	} {
		for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
			if w := e.do(t, r.m, r.path, e.body(oidcTestSecret), role); w.Code != http.StatusForbidden {
				t.Errorf("%s %s rol %s → %d, beklenen 403", r.m, r.path, role, w.Code)
			}
		}
		if w := e.do(t, r.m, r.path, e.body(oidcTestSecret), ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s kimliksiz → %d, beklenen 401", r.m, r.path, w.Code)
		}
	}
	if e.store.puts != 0 || len(e.audits()) != 0 || len(e.cache.pub) != 0 || e.s.oidc.Enabled() {
		t.Fatal("reddedilen istek yan etki bıraktı")
	}
}

func TestOIDCSettingsPutGetSecretAuditAndHotSwap(t *testing.T) {
	e := newOIDCTestEnv(t)
	if w := e.loginConfig(); strings.Contains(w.Body.String(), `"displayName"`) {
		t.Fatalf("başlangıçta SSO düğmesi olmamalı: %s", w.Body)
	}
	w := e.do(t, "PUT", "/api/settings/oidc", e.body(oidcTestSecret), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), oidcTestSecret) || !strings.Contains(w.Body.String(), `"clientSecretStored":true`) {
		t.Fatalf("PUT cevabı secret sızdırıyor ya da stored yok: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), `"allowedDomains":["example.test"]`) ||
		!strings.Contains(w.Body.String(), `"redirectUrl":"https://apm.example.test/api/auth/oidc/callback"`) {
		t.Fatalf("normalize edilmemiş: %s", w.Body)
	}
	// Canlı takas: public login yapılandırması AYNI süreçte SSO düğmesini gösterir.
	if w := e.loginConfig(); !strings.Contains(w.Body.String(), `"displayName":"Kurumsal SSO"`) {
		t.Fatalf("SSO düğmesi restart'sız gelmedi: %s", w.Body)
	}
	if len(e.cache.pub) != 1 || e.cache.pub[0] != "config:oidc" {
		t.Fatalf("config:oidc yayınlanmadı: %v", e.cache.pub)
	}
	au := e.audits()
	if len(au) != 1 || au[0].Action != "settings.oidc.update" || au[0].TargetID != "oidc" {
		t.Fatalf("audit yok/yanlış: %+v", au)
	}
	if strings.Contains(au[0].Details, oidcTestSecret) || !strings.Contains(au[0].Details, `"clientSecretChanged":true`) {
		t.Fatalf("audit secret taşıyor ya da değişim bayrağı yok: %s", au[0].Details)
	}
	// GET secret yok.
	g := e.do(t, "GET", "/api/settings/oidc", "", auth.RoleAdmin)
	if g.Code != http.StatusOK || strings.Contains(g.Body.String(), oidcTestSecret) ||
		!strings.Contains(g.Body.String(), `"source":"settings"`) || !strings.Contains(g.Body.String(), `"active":true`) {
		t.Fatalf("GET %d %s", g.Code, g.Body)
	}
	// Boş secret → kayıtlı korunur; audit "değişmedi" der.
	if w := e.do(t, "PUT", "/api/settings/oidc", e.body(""), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("boş secret PUT %d %s", w.Code, w.Body)
	}
	if !strings.Contains(string(e.store.rows[auth.OIDCSettingsKey]), oidcTestSecret) {
		t.Fatal("boş secret kayıtlıyı sildi")
	}
	if au := e.audits(); len(au) != 1 || !strings.Contains(au[0].Details, `"clientSecretChanged":false`) {
		t.Fatalf("ikinci audit: %+v", au)
	}
}

func TestOIDCSettingsPutRejectsBrokenDiscoveryAcceptsDisabled(t *testing.T) {
	e := newOIDCTestEnv(t)
	e.idpOK.Store(false)
	w := e.do(t, "PUT", "/api/settings/oidc", e.body(oidcTestSecret), auth.RoleAdmin)
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "IDP-BODY") ||
		!strings.Contains(w.Body.String(), "keşfi başarısız") {
		t.Fatalf("bozuk keşif PUT %d %s", w.Code, w.Body)
	}
	if e.store.puts != 0 || len(e.audits()) != 0 || len(e.cache.pub) != 0 {
		t.Fatal("bozuk kayıt yan etki bıraktı")
	}
	// Doğrulama hatası (openid yok) da 400.
	bad := strings.Replace(e.body(oidcTestSecret), `"openid",`, "", 1)
	if w := e.do(t, "PUT", "/api/settings/oidc", bad, auth.RoleAdmin); w.Code != http.StatusBadRequest {
		t.Fatalf("openid'siz PUT %d %s", w.Code, w.Body)
	}
	// Kapatma anahtarı her zaman geçer.
	if w := e.do(t, "PUT", "/api/settings/oidc", `{"enabled":false,"issuerUrl":"ftp://x"}`, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("enabled:false PUT %d %s", w.Code, w.Body)
	}
	if e.store.puts != 1 || len(e.audits()) != 1 {
		t.Fatal("kapatma kaydı/audit yazılmadı")
	}
}

func TestOIDCSettingsTestEndpoint(t *testing.T) {
	e := newOIDCTestEnv(t)
	w := e.do(t, "POST", "/api/settings/oidc/test", e.body(""), auth.RoleAdmin)
	var ok struct {
		OK        bool                `json:"ok"`
		Discovery *auth.OIDCDiscovery `json:"discovery"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ok); err != nil || !ok.OK || ok.Discovery == nil ||
		ok.Discovery.TokenEndpoint != e.idp.URL+"/token" {
		t.Fatalf("test ok: %d %s", w.Code, w.Body)
	}
	e.idpOK.Store(false)
	w = e.do(t, "POST", "/api/settings/oidc/test", e.body(""), auth.RoleAdmin)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":false`) ||
		strings.Contains(w.Body.String(), "IDP-BODY") || strings.Contains(w.Body.String(), "502") ||
		!strings.Contains(w.Body.String(), "kimlik sağlayıcıya ulaşılamadı") {
		t.Fatalf("test hata (tek genel cümle bekleniyordu): %d %s", w.Code, w.Body)
	}
	if e.store.puts != 0 || e.s.oidc.Enabled() {
		t.Fatal("test ucu yazdı ya da canlıya dokundu")
	}
	// Yoklama izi: her test çağrısı audit'lenir — issuer + ok; secret/gövde yok.
	au := e.audits()
	if len(au) != 2 || au[0].Action != "settings.oidc.test" || au[1].Action != "settings.oidc.test" {
		t.Fatalf("test ucu audit'i yok/yanlış: %+v", au)
	}
	if !strings.Contains(au[0].Details, `"ok":true`) || !strings.Contains(au[1].Details, `"ok":false`) ||
		!strings.Contains(au[1].Details, e.idp.URL) {
		t.Fatalf("audit ayrıntısı: %s / %s", au[0].Details, au[1].Details)
	}
	for _, a := range au {
		if strings.Contains(a.Details, oidcTestSecret) || strings.Contains(a.Details, "IDP-BODY") ||
			strings.Contains(a.Details, "ulaşılamadı") {
			t.Fatalf("audit secret/gövde/hata taşıyor: %s", a.Details)
		}
	}
}

// API token principal'ı (UserID "token:…") admin rolünde olsa da 403.
func TestOIDCSettingsRejectsAPITokenPrincipal(t *testing.T) {
	e := newOIDCTestEnv(t)
	for _, r := range []struct{ m, path string }{
		{"GET", "/api/settings/oidc"}, {"PUT", "/api/settings/oidc"}, {"POST", "/api/settings/oidc/test"},
	} {
		req := httptest.NewRequest(r.m, r.path, strings.NewReader(e.body(oidcTestSecret)))
		req = req.WithContext(auth.ContextWithClaims(req.Context(),
			&auth.Claims{UserID: "token:t1", Email: "bot@example.test", Role: auth.RoleAdmin}))
		w := httptest.NewRecorder()
		e.mux.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s token principal → %d, beklenen 403", r.m, r.path, w.Code)
		}
	}
	if e.store.puts != 0 || len(e.audits()) != 0 || e.s.oidc.Enabled() {
		t.Fatal("token principal yan etki bıraktı")
	}
}

func TestOIDCSettingsPutRulesSecretCarryAndRoleDomains(t *testing.T) {
	e := newOIDCTestEnv(t)
	if w := e.do(t, "PUT", "/api/settings/oidc", e.body(oidcTestSecret), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("ilk PUT %d %s", w.Code, w.Body)
	}
	puts := e.store.puts
	// Client id değişti + boş secret → kayıtlı secret yeni kimliğe TAŞINMAZ.
	moved := strings.Replace(e.body(""), `"clientId":"coremetry"`, `"clientId":"baska-istemci"`, 1)
	w := e.do(t, "PUT", "/api/settings/oidc", moved, auth.RoleAdmin)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "yeniden girilmeli") {
		t.Fatalf("secret taşıma reddedilmedi: %d %s", w.Code, w.Body)
	}
	// viewer dışı varsayılan rol alan adısız → 400.
	adminNoDomains := strings.Replace(strings.Replace(e.body(""), `"defaultRole":"viewer"`, `"defaultRole":"admin"`, 1),
		`"allowedDomains":["Example.TEST"]`, `"allowedDomains":[]`, 1)
	if w := e.do(t, "PUT", "/api/settings/oidc", adminNoDomains, auth.RoleAdmin); w.Code != http.StatusBadRequest {
		t.Fatalf("admin rolü alan adısız kabul edildi: %d %s", w.Code, w.Body)
	}
	if e.store.puts != puts {
		t.Fatal("reddedilen PUT yazdı")
	}
}

// Token ucunun hata gövdesi (oauth2 RetrieveError) ne giriş sayfasına ne loga.
func TestOIDCCallbackHidesIdPBody(t *testing.T) {
	e := newOIDCTestEnv(t)
	if w := e.do(t, "PUT", "/api/settings/oidc", e.body(oidcTestSecret), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	req := httptest.NewRequest("GET", "/api/auth/oidc/callback?code=c1&state=s1", nil)
	for k, v := range map[string]string{oidcStateCookie: "s1", oidcNonceCookie: "n1", oidcVerifierCookie: "v1"} {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	w := httptest.NewRecorder()
	e.s.oidcCallback(w, req)
	loc := w.Header().Get("Location")
	if w.Code != http.StatusFound || !strings.HasPrefix(loc, "/login/?error=") {
		t.Fatalf("callback %d %q", w.Code, loc)
	}
	for name, out := range map[string]string{"Location": loc, "log": buf.String()} {
		if strings.Contains(out, "TOKEN-BODY-LEAK") || strings.Contains(out, "invalid_grant") {
			t.Errorf("%s IdP gövdesini taşıyor: %s", name, out)
		}
	}
	if !strings.Contains(buf.String(), "class=token_exchange") {
		t.Fatalf("log sınıfı yok: %s", buf.String())
	}
}
