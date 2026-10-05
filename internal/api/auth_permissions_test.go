package api

// auth_permissions_test.go — v0.10.1110 (merkezi login yetki servisi).
//
// NE ÇİVİLİYOR: rota defterden kayıtlı (api.go'ya dokunulmadı) ve yalnız
// POST oturumsuz (auth.SkipPath); 404 kapalı/anahtarsız, 401 yanlış/eksik
// anahtar (gövdesiz, gövde doğrulamasından ÖNCE), 400 bozuk/eksik/aşırı
// gövde; 200 e-posta eşleşmesi, 200 ldap_username = username VE =
// registrationNumber; TANIMSIZ kullanıcı → 200 + varsayılan rol (viewer;
// operatör kararı 2026-10-05), 204 YALNIZ devre dışı hesap; subject AYNEN yankı; ttl
// yapılandırmadan; özel rol; depo hatası 500 ve metni sızmaz. Settings PUT
// round-trip: anahtar GET/PUT cevabında ve audit'te yok, boş PUT korur.
// OIDC callback (oidcLoginUser): disabled kullanıcı reddedilir, yeniden
// AÇILMAZ (güvenlik incelemesi F1b); claim'den rol YALNIZ düşürür (F1a),
// yeni kullanıcıya dokunmaz (defaultRole callback'te, operatör kararı
// 2026-10-05), claim yok/kapalıyken rol korunur; son admin düşürülmez;
// audit istek IP'si + önceki rol (N6).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const permTestKey = "9d1f6b2e7a4c3058e1b2"

type permFakeLookup struct {
	mu      sync.Mutex
	byEmail map[string]*chstore.User
	byLdap  map[string]*chstore.User
	err     error
	calls   []string
}

func (f *permFakeLookup) GetUserByEmailAnyState(_ context.Context, email string) (*chstore.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "email:"+email)
	if f.err != nil {
		return nil, f.err
	}
	return f.byEmail[email], nil
}

func (f *permFakeLookup) GetUserByLdapUsername(_ context.Context, u string) (*chstore.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ldap:"+u)
	if f.err != nil {
		return nil, f.err
	}
	return f.byLdap[u], nil
}

type permTestEnv struct {
	*oidcTestEnv
	lookup *permFakeLookup
}

// newPermTestEnv — OIDC test ortamı + yetki servisi (SSO KAPALI: servis
// SSO'dan bağımsız) + sahte kullanıcı deposu.
func newPermTestEnv(t *testing.T, enabled bool) *permTestEnv {
	t.Helper()
	e := newOIDCTestEnv(t)
	in := auth.OIDCSettings{PermissionServiceEnabled: enabled, PermissionServiceKey: permTestKey, PermissionTTLSeconds: 120}
	if _, err := e.s.oidc.SaveSettings(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	lk := &permFakeLookup{
		byEmail: map[string]*chstore.User{
			"user12345@example.test": {ID: "u-email", Email: "user12345@example.test", Role: auth.RoleEditor},
			"off@example.test":       {ID: "u-off-mail", Email: "off@example.test", Role: auth.RoleAdmin, Disabled: true},
		},
		byLdap: map[string]*chstore.User{
			"user12345": {ID: "u-ldap-name", Email: "a@example.test", Role: auth.RoleAdmin},
			"67890":     {ID: "u-ldap-reg", Email: "b@example.test", Role: auth.RoleViewer},
			"svc-off":   {ID: "u-off", Email: "c@example.test", Role: auth.RoleAdmin, Disabled: true},
		},
	}
	prev := permissionsLookup
	permissionsLookup = func(*Server) permUserLookup { return lk }
	t.Cleanup(func() { permissionsLookup = prev })
	e.s.registerAuthPermissionsRoutes(e.mux)
	return &permTestEnv{oidcTestEnv: e, lookup: lk}
}

func (e *permTestEnv) call(t *testing.T, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/auth/permissions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(PermissionAuthHeader, key)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func permBody(userID, username, email, reg string) string {
	b, _ := json.Marshal(map[string]string{"userId": userID, "username": username, "email": email, "registrationNumber": reg})
	return string(b)
}

func TestAuthPermissionsRouteRegisteredAndSessionExempt(t *testing.T) {
	src, err := os.ReadFile("auth_permissions.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoComments(string(src)), `registerRoutesExtra("auth-permissions", (*Server).registerAuthPermissionsRoutes)`) {
		t.Fatal("auth_permissions.go init() defter kaydı yok")
	}
	if b, _ := os.ReadFile("api.go"); strings.Contains(string(b), "/api/auth/permissions") {
		t.Fatal("api.go /api/auth/permissions içeriyor — api.go büyümez")
	}
	mux := (&Server{}).buildMux()
	if _, pat := mux.Handler(httptest.NewRequest("POST", "/api/auth/permissions", nil)); pat != "POST /api/auth/permissions" {
		t.Fatalf("POST kalıbı = %q", pat)
	}
	if !auth.SkipPath(http.MethodPost, "/api/auth/permissions") {
		t.Fatal("POST /api/auth/permissions oturum middleware'inden muaf değil")
	}
	if auth.SkipPath(http.MethodGet, "/api/auth/permissions") || auth.SkipPath(http.MethodPost, "/api/auth/permissions/x") {
		t.Fatal("muafiyet POST /api/auth/permissions'tan geniş")
	}
}

func TestAuthPermissionsDisabled404(t *testing.T) {
	// Hiç ayar yok.
	e := newOIDCTestEnv(t)
	e.s.registerAuthPermissionsRoutes(e.mux)
	pe := &permTestEnv{oidcTestEnv: e}
	if w := pe.call(t, permTestKey, permBody("12345", "12345", "user12345@example.test", "12345")); w.Code != http.StatusNotFound || w.Body.Len() != 0 {
		t.Fatalf("ayarsız → %d %q, beklenen 404 gövdesiz", w.Code, w.Body.String())
	}
	// Anahtar kayıtlı ama servis kapalı.
	p := newPermTestEnv(t, false)
	if w := p.call(t, permTestKey, permBody("12345", "12345", "user12345@example.test", "12345")); w.Code != http.StatusNotFound {
		t.Fatalf("kapalı → %d, beklenen 404", w.Code)
	}
	if len(p.lookup.calls) != 0 {
		t.Fatal("kapalı serviste depo okundu")
	}
}

func TestAuthPermissionsUnauthorized401(t *testing.T) {
	e := newPermTestEnv(t, true)
	for _, key := range []string{"", "wrong-key-0000000000", permTestKey + "x", strings.ToUpper(permTestKey)} {
		// Bozuk gövde: 401 gövde doğrulamasından ÖNCE gelmeli.
		w := e.call(t, key, `{bozuk`)
		if w.Code != http.StatusUnauthorized || w.Body.Len() != 0 {
			t.Errorf("anahtar %q → %d %q, beklenen 401 gövdesiz", key, w.Code, w.Body.String())
		}
	}
	if len(e.lookup.calls) != 0 {
		t.Fatal("yetkisiz istek depoyu okudu")
	}
}

func TestAuthPermissionsBadRequest400(t *testing.T) {
	e := newPermTestEnv(t, true)
	for name, body := range map[string]string{
		"bozuk JSON":             `{"registrationNumber":`,
		"registrationNumber yok": permBody("12345", "12345", "user12345@example.test", ""),
		"boşluk registration":    permBody("12345", "12345", "user12345@example.test", "   "),
		"4 KB üstü":              `{"registrationNumber":"12345","pad":"` + strings.Repeat("x", 5000) + `"}`,
		"alan çok uzun":          permBody(strings.Repeat("1", 300), "12345", "", "12345"),
	} {
		if w := e.call(t, permTestKey, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s → %d, beklenen 400", name, w.Code)
		}
	}
}

// Tanımsız kullanıcıya bildirilen rol SSO varsayılan rolünü izler.
func TestAuthPermissionsUnknownUserGetsDefaultRole(t *testing.T) {
	e := newPermTestEnv(t, true)
	in := auth.OIDCSettings{PermissionServiceEnabled: true, PermissionTTLSeconds: 120, DefaultRole: auth.RoleEditor}
	if _, err := e.s.oidc.SaveSettings(context.Background(), in); err != nil { // SSO kapalı; anahtar korunur
		t.Fatal(err)
	}
	out := decodePerm(t, e.call(t, permTestKey, permBody("1", "nobody", "nobody@example.test", "00000")))
	if len(out.Permissions) != 1 || out.Permissions[0] != auth.PermissionEditor || out.Subject != "00000" || out.TTLSeconds != 120 {
		t.Fatalf("varsayılan rol editor bekleniyordu: %+v", out)
	}
}

func decodePerm(t *testing.T, w *httptest.ResponseRecorder) permissionsResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("durum %d, beklenen 200 (%s)", w.Code, w.Body.String())
	}
	var out permissionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAuthPermissionsMatching(t *testing.T) {
	e := newPermTestEnv(t, true)
	cases := []struct {
		name               string
		body               string
		status             int
		wantPerm, wantSubj string
	}{
		{"e-posta eşleşmesi (büyük harf girdi)", permBody("12345", "nobody", "User12345@Example.TEST", "12345"), 200, auth.PermissionEditor, "12345"},
		{"ldap_username = username", permBody("12345", "USER12345", "", "nomatch"), 200, auth.PermissionAdmin, "nomatch"},
		{"ldap_username = registrationNumber", permBody("x", "nobody", "unknown@example.test", "67890"), 200, auth.PermissionViewer, "67890"},
		{"tanımsız kullanıcı → 200 viewer (204 DEĞİL)", permBody("1", "nobody", "nobody@example.test", "00000"), 200, auth.PermissionViewer, "00000"},
		{"tanımsız, e-postasız → 200 viewer", permBody("1", "", "", "55555"), 200, auth.PermissionViewer, "55555"},
		{"disabled (ldap) → 204", permBody("1", "svc-off", "", "00001"), 204, "", ""},
		{"disabled (e-posta) → 204", permBody("1", "nobody", "off@example.test", "00002"), 204, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := e.call(t, permTestKey, c.body)
			if c.status == http.StatusNoContent {
				if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
					t.Fatalf("→ %d %q, beklenen 204 gövdesiz", w.Code, w.Body.String())
				}
				return
			}
			out := decodePerm(t, w)
			if out.Subject != c.wantSubj || len(out.Permissions) != 1 || out.Permissions[0] != c.wantPerm || out.TTLSeconds != 120 {
				t.Fatalf("cevap %+v; beklenen subject=%s perm=%s ttl=120", out, c.wantSubj, c.wantPerm)
			}
			if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
				t.Fatalf("Cache-Control = %q", cc)
			}
		})
	}
}

func TestAuthPermissionsSubjectEchoedVerbatimAndOrder(t *testing.T) {
	e := newPermTestEnv(t, true)
	e.lookup.calls = nil
	w := e.call(t, permTestKey, permBody("12345", " 67890 ", "", " 67890 "))
	out := decodePerm(t, w)
	if out.Subject != " 67890 " {
		t.Fatalf("subject AYNEN yankılanmadı: %q", out.Subject)
	}
	// Sıra: e-posta boş → atlanır; username ve registrationNumber aynı → tek okuma.
	if strings.Join(e.lookup.calls, ",") != "ldap:67890" {
		t.Fatalf("okuma sırası/sayısı yanlış: %v", e.lookup.calls)
	}
	// Ham JSON alan adları sözleşmedeki gibi.
	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	for _, k := range []string{"subject", "permissions", "ttlSeconds"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("cevapta %q yok: %s", k, w.Body.String())
		}
	}
	// E-posta eşleşirse ldap okunmaz.
	e.lookup.calls = nil
	e.call(t, permTestKey, permBody("12345", "user12345", "user12345@example.test", "12345"))
	if strings.Join(e.lookup.calls, ",") != "email:user12345@example.test" {
		t.Fatalf("e-posta eşleşmesinden sonra ek okuma: %v", e.lookup.calls)
	}
}

func TestAuthPermissionsCustomRoleAndStoreError(t *testing.T) {
	e := newPermTestEnv(t, true)
	svc := auth.NewService("0123456789abcdef0123456789abcdef0123456789", 0)
	if err := svc.UpsertCustomRole(context.Background(), &memRoleStore{}, auth.CustomRole{Name: "svc-orders", Pages: []string{"/services"}}); err != nil {
		t.Fatal(err)
	}
	e.s.auth = svc
	e.lookup.byEmail["user12345@example.test"] = &chstore.User{ID: "u-c", Role: auth.RoleViewer, CustomRole: "svc-orders"}
	out := decodePerm(t, e.call(t, permTestKey, permBody("1", "", "user12345@example.test", "12345")))
	if out.Permissions[0] != "COREMETRY_ROLE_SVC_ORDERS" {
		t.Fatalf("özel rol: %v", out.Permissions)
	}
	e.lookup.byEmail["user12345@example.test"].CustomRole = "deleted-role"
	out = decodePerm(t, e.call(t, permTestKey, permBody("1", "", "user12345@example.test", "12345")))
	if out.Permissions[0] != auth.PermissionViewer {
		t.Fatalf("katalogda olmayan özel rol taban role inmeli: %v", out.Permissions)
	}
	e.lookup.err = errors.New("dial tcp 10.0.0.1:9000: CH-INTERNAL-DETAIL")
	w := e.call(t, permTestKey, permBody("1", "", "user12345@example.test", "12345"))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "CH-INTERNAL-DETAIL") {
		t.Fatalf("depo hatası → %d %q", w.Code, w.Body.String())
	}
}

type memRoleStore struct{ raw []byte }

func (m *memRoleStore) GetCustomRolesRaw(context.Context) ([]byte, error) { return m.raw, nil }
func (m *memRoleStore) PutCustomRolesRaw(_ context.Context, b []byte) error {
	m.raw = b
	return nil
}

// ── Settings PUT round-trip ──────────────────────────────────────────────────

func TestOIDCSettingsPermissionServiceRoundTrip(t *testing.T) {
	e := newOIDCTestEnv(t)
	body := func(key string, ttl int) string {
		m := map[string]any{"enabled": false, "permissionServiceEnabled": true, "permissionTTLSeconds": ttl,
			"permissionsClaim": "permissions", "roleFromClaim": true}
		if key != "" {
			m["permissionServiceKey"] = key
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	w := e.do(t, "PUT", "/api/settings/oidc", body(permTestKey, 900), auth.RoleAdmin)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), permTestKey) {
		t.Fatalf("PUT → %d, anahtar yankısı? %s", w.Code, w.Body.String())
	}
	var snap auth.OIDCSnapshot
	_ = json.Unmarshal(w.Body.Bytes(), &snap)
	if !snap.PermissionServiceEnabled || !snap.PermissionServiceKeySet || snap.PermissionTTLSeconds != 900 || !snap.RoleFromClaim {
		t.Fatalf("snapshot yanlış: %+v", snap)
	}
	// Boş anahtarlı PUT kayıtlıyı korur.
	if w := e.do(t, "PUT", "/api/settings/oidc", body("", 30), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("boş anahtarlı PUT → %d %s", w.Code, w.Body.String())
	}
	if !e.s.oidc.PermissionKeyMatches(permTestKey) || e.s.oidc.PermissionService().TTLSeconds != 60 {
		t.Fatalf("anahtar korunmadı ya da TTL kıskaçlanmadı: %+v", e.s.oidc.PermissionService())
	}
	g := e.do(t, "GET", "/api/settings/oidc", "", auth.RoleAdmin)
	if strings.Contains(g.Body.String(), permTestKey) || !strings.Contains(g.Body.String(), `"permissionServiceKeySet":true`) {
		t.Fatalf("GET anahtar sızdırıyor ya da keySet yok: %s", g.Body.String())
	}
	// Audit: yeni alanlar var, anahtar yok.
	as := e.audits()
	if len(as) != 2 {
		t.Fatalf("2 audit bekleniyordu, %d", len(as))
	}
	for _, a := range as {
		if strings.Contains(a.Details, permTestKey) || !strings.Contains(a.Details, `"roleFromClaim":true`) ||
			!strings.Contains(a.Details, `"permissionServiceEnabled":true`) {
			t.Fatalf("audit ayrıntısı yanlış: %s", a.Details)
		}
	}
	if !strings.Contains(as[0].Details, `"permissionServiceKeyChanged":true`) || !strings.Contains(as[1].Details, `"permissionServiceKeyChanged":false`) {
		t.Fatalf("keyChanged bayrağı yanlış: %s | %s", as[0].Details, as[1].Details)
	}
	// Kısa anahtar 400, yazmaz.
	puts := e.store.puts
	if w := e.do(t, "PUT", "/api/settings/oidc", body("short", 300), auth.RoleAdmin); w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "short") {
		t.Fatalf("kısa anahtar → %d %s", w.Code, w.Body.String())
	}
	if e.store.puts != puts {
		t.Fatal("reddedilen PUT yazdı")
	}
}

// ── OIDC callback: oidcLoginUser (disabled reddi + claim rolü yalnız düşürür) ─

type claimFakeStore struct {
	users  map[string]*chstore.User
	admins int64
	ups    []chstore.User
	upErr  error
}

func (f *claimFakeStore) GetUserByEmailAnyState(_ context.Context, email string) (*chstore.User, error) {
	if u := f.users[email]; u != nil {
		c := *u
		return &c, nil
	}
	return nil, nil
}
func (f *claimFakeStore) UpsertUser(_ context.Context, u chstore.User) error {
	if f.upErr != nil {
		return f.upErr
	}
	f.ups = append(f.ups, u)
	return nil
}
func (f *claimFakeStore) CountAdmins(context.Context) (int64, error) { return f.admins, nil }

func TestOIDCCallbackUsesLoginUserHelper(t *testing.T) {
	b, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *Server) oidcCallback(")
	j := strings.Index(src[i:], "\nfunc ")
	body := src[i : i+j]
	if !strings.Contains(body, "s.oidcLoginUser(r, email, claims)") || strings.Contains(body, "s.store.GetUserByEmail(") {
		t.Fatal("oidcCallback kullanıcıyı oidcLoginUser ile okumuyor (disabled reddi + claim rolü atlanır)")
	}
}

func TestOIDCLoginUser(t *testing.T) {
	const email = "user12345@example.test"
	user := func(role string, disabled bool) *chstore.User {
		return &chstore.User{ID: "u1", Email: email, Role: role, Disabled: disabled, AuthProvider: "oidc"}
	}
	cases := []struct {
		name      string
		existing  *chstore.User
		admins    int64
		claimRole string // "" = claim yok ya da roleFromClaim KAPALI (Exchange doldurmaz)
		upErr     error
		wantErr   error
		wantNil   bool   // yeni kullanıcı → callback defaultRole ile açar
		wantRole  string // dönen kullanıcının rolü
		wantWrite bool
	}{
		{name: "yeni kullanıcı: yazma yok, callback defaultRole (viewer) ile açar", claimRole: auth.RoleAdmin, wantNil: true},
		{name: "claim yok / roleFromClaim kapalı: kayıtlı admin rolü korunur", existing: user(auth.RoleAdmin, false), admins: 1, wantRole: auth.RoleAdmin},
		{name: "claim yok / kapalı: yeni kullanıcı yine açılmaz", wantNil: true},
		{name: "disabled → giriş reddi, yeniden açılmaz", existing: user(auth.RoleViewer, true), wantErr: errOIDCAccountDisabled},
		{name: "disabled + claim → yine red", existing: user(auth.RoleAdmin, true), claimRole: auth.RoleAdmin, wantErr: errOIDCAccountDisabled},
		{name: "YÜKSELTME yok: viewer + claim admin", existing: user(auth.RoleViewer, false), claimRole: auth.RoleAdmin, wantRole: auth.RoleViewer},
		{name: "YÜKSELTME yok: editor + claim admin", existing: user(auth.RoleEditor, false), claimRole: auth.RoleAdmin, wantRole: auth.RoleEditor},
		{name: "aynı rol → yazma yok", existing: user(auth.RoleEditor, false), claimRole: auth.RoleEditor, wantRole: auth.RoleEditor},
		{name: "düşürme: editor → viewer", existing: user(auth.RoleEditor, false), claimRole: auth.RoleViewer, wantRole: auth.RoleViewer, wantWrite: true},
		{name: "düşürme: ikinci admin → editor", existing: user(auth.RoleAdmin, false), admins: 2, claimRole: auth.RoleEditor, wantRole: auth.RoleEditor, wantWrite: true},
		{name: "son admin düşürülmez", existing: user(auth.RoleAdmin, false), admins: 1, claimRole: auth.RoleViewer, wantRole: auth.RoleAdmin},
		{name: "geçersiz rol → yazma yok", existing: user(auth.RoleEditor, false), claimRole: "root", wantRole: auth.RoleEditor},
		{name: "yazma hatası girişi düşürmez, kayıtlı rol", existing: user(auth.RoleEditor, false), claimRole: auth.RoleViewer, upErr: errors.New("ch down"), wantRole: auth.RoleEditor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newOIDCTestEnv(t)
			st := &claimFakeStore{users: map[string]*chstore.User{}, admins: c.admins, upErr: c.upErr}
			if c.existing != nil {
				st.users[email] = c.existing
			}
			prev := oidcUserStoreFor
			oidcUserStoreFor = func(*Server) oidcUserStore { return st }
			t.Cleanup(func() { oidcUserStoreFor = prev })
			req := httptest.NewRequest("GET", "/api/auth/oidc/callback", nil)
			req.Header.Set("X-Forwarded-For", "203.0.113.7")
			u, err := e.s.oidcLoginUser(req, email, &auth.OIDCClaims{Email: email, ClaimRole: c.claimRole, ClaimRoleSource: "access_token"})
			as := e.audits()
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) || u != nil || len(st.ups) != 0 {
					t.Fatalf("err=%v u=%v ups=%d; beklenen %v", err, u, len(st.ups), c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.wantNil {
				if u != nil || len(st.ups) != 0 || len(as) != 0 {
					t.Fatalf("yeni kullanıcıya dokunuldu: u=%v ups=%v", u, st.ups)
				}
				return
			}
			if u == nil || u.Role != c.wantRole {
				t.Fatalf("dönen rol %v, beklenen %s", u, c.wantRole)
			}
			if !c.wantWrite {
				if len(st.ups) != 0 || len(as) != 0 {
					t.Fatalf("yazma/audit beklenmiyordu: %+v %d", st.ups, len(as))
				}
				return
			}
			if len(st.ups) != 1 || st.ups[0].Role != c.wantRole || st.ups[0].ID != "u1" {
				t.Fatalf("yazma yanlış: %+v", st.ups)
			}
			if len(as) != 1 {
				t.Fatalf("1 audit bekleniyordu: %+v", as)
			}
			a := as[0]
			if a.Action != "user.set_role_from_claim" || a.TargetID != "u1" || a.IP != "203.0.113.7" ||
				a.ActorRole != c.existing.Role || !strings.Contains(a.Details, `"from":"`+c.existing.Role+`"`) ||
				!strings.Contains(a.Details, `"to":"`+c.wantRole+`"`) {
				t.Fatalf("audit yanlış (IP, önceki rol aktör rolü, from→to): %+v", a)
			}
		})
	}
}
