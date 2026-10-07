package api

// auth_oidc_email_resolve_test.go — v0.10.1121 (operatör, prod: "[oidc]
// callback failed: class=email_missing"; id_token'da email yok,
// preferred_username = AD sicili).
//
// NE ÇİVİLİYOR (callback tarafı; zincir auth/oidc_email_resolve_test.go'da):
// EmailSource=ldap_username → users.ldap_username (küçük harf) ile MEVCUT
// kullanıcı; güven anahtarı değil (yerel/LDAP admin de açılır); bulunamazsa
// email_missing ve YENİ kullanıcı AÇILMAZ; devre dışı hesap reddi; kayıtlı
// e-postaya izinli alan adı listesi; başarı logu `email resolved via <kaynak>
// user id=<id>` e-posta taşımaz; usernameFallback/usernameClaim PUT→GET +
// audit eski→yeni; geçersiz claim adı 400, yan etki yok.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// ldapLookupFake — GetActiveUsersByLdapUsername sahtesi: SQL gibi devre dışı
// satırları atar, küçük harf karşılaştırır, en çok 2 satır döner.
type ldapLookupFake struct {
	users map[string][]chstore.User
	got   []string
}

func (f *ldapLookupFake) GetActiveUsersByLdapUsername(_ context.Context, username string) ([]chstore.User, error) {
	f.got = append(f.got, username)
	var out []chstore.User
	for _, u := range f.users[strings.ToLower(username)] {
		if !u.Disabled && len(out) < 2 {
			out = append(out, u)
		}
	}
	return out, nil
}

func captureAPILog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

// liveOIDCEnvAdmin — izinli alan adı example.test ile canlı istemcili ortam;
// allowAdmin = usernameFallback + usernameFallbackAllowAdmin.
func liveOIDCEnvAdmin(t *testing.T, allowAdmin bool) *oidcTestEnv {
	t.Helper()
	e := newOIDCTestEnv(t)
	var m map[string]any
	_ = json.Unmarshal([]byte(e.body(oidcTestSecret)), &m)
	m["usernameFallback"] = true
	m["usernameFallbackAllowAdmin"] = allowAdmin
	b, _ := json.Marshal(m)
	if w := e.do(t, "PUT", "/api/settings/oidc", string(b), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	_ = e.audits()
	if e.s.oidc.UsernameFallbackAllowAdmin() != allowAdmin {
		t.Fatal("admin açık-seçimi canlı istemciye uygulanmadı")
	}
	return e
}

func liveOIDCEnv(t *testing.T) *oidcTestEnv { return liveOIDCEnvAdmin(t, false) }

func TestOIDCLoginUserByLdapUsername(t *testing.T) {
	const uname = "n0000001"
	u := func(role, provider string) chstore.User {
		return chstore.User{ID: "u1", Email: "first.last@example.test", Role: role, AuthProvider: provider, LdapUsername: uname}
	}
	disabled := u(auth.RoleViewer, "ldap")
	disabled.Disabled = true
	other := u(auth.RoleViewer, "ldap")
	other.ID = "u2"
	foreign := u(auth.RoleViewer, "ldap")
	foreign.Email = "first.last@other.test"
	cases := []struct {
		name       string
		rows       []chstore.User
		allowAdmin bool
		wantErr    error
	}{
		{"LDAP viewer → giriş (operatör akışı)", []chstore.User{u(auth.RoleViewer, "ldap")}, false, nil},
		{"LDAP editor → giriş", []chstore.User{u(auth.RoleEditor, "ldap")}, false, nil},
		{"yerel editor → giriş", []chstore.User{u(auth.RoleEditor, "local")}, false, nil},
		{"oidc viewer → giriş", []chstore.User{u(auth.RoleViewer, "oidc")}, false, nil},
		{"admin + açık-seçim yok → red (F2)", []chstore.User{u(auth.RoleAdmin, "local")}, false, errOIDCUsernameAdminRefused},
		{"LDAP admin + açık-seçim yok → red (F2)", []chstore.User{u(auth.RoleAdmin, "ldap")}, false, errOIDCUsernameAdminRefused},
		{"admin + açık-seçim → giriş", []chstore.User{u(auth.RoleAdmin, "local")}, true, nil},
		{"yok → email_missing, yeni kullanıcı açılmaz", nil, false, errOIDCEmailMissing},
		{"yalnız devre dışı satır → yok sayılır, email_missing (F4)", []chstore.User{disabled}, false, errOIDCEmailMissing},
		{"iki aktif satır → belirsiz (F4)", []chstore.User{u(auth.RoleViewer, "ldap"), other}, false, errOIDCUsernameAmbiguous},
		{"alan adı dışı → red", []chstore.User{foreign}, false, errOIDCEmailDomainDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := liveOIDCEnvAdmin(t, c.allowAdmin)
			lk := &ldapLookupFake{users: map[string][]chstore.User{uname: c.rows}}
			st := &claimFakeStore{users: map[string]*chstore.User{}, admins: 2}
			prevL, prevS := oidcLdapLookupFor, oidcUserStoreFor
			oidcLdapLookupFor = func(*Server) oidcLdapUserLookup { return lk }
			oidcUserStoreFor = func(*Server) oidcUserStore { return st }
			t.Cleanup(func() { oidcLdapLookupFor, oidcUserStoreFor = prevL, prevS })
			buf := captureAPILog(t)
			claims := &auth.OIDCClaims{EmailSource: auth.OIDCEmailSourceLdapUsername, Username: uname, ViaUsername: true,
				ResolutionTried: []string{"id_token", "userinfo", "ldap_username"}, ClaimNames: []string{"preferred_username", "sub"}}
			got, err := e.s.oidcLoginUser(httptest.NewRequest("GET", "/api/auth/oidc/callback", nil), "", claims)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) || got != nil || len(st.ups) != 0 {
					t.Fatalf("err=%v u=%v; beklenen %v", err, got, c.wantErr)
				}
				if c.wantErr == errOIDCEmailMissing && !strings.Contains(buf.String(),
					"[oidc] email resolution failed: class=email_missing tried=id_token,userinfo,ldap_username claims=preferred_username,sub") {
					t.Fatalf("başarısızlık logu: %s", buf.String())
				}
				if c.wantErr == errOIDCUsernameAdminRefused {
					var pe *oidcLoginPageError
					if !errors.As(err, &pe) || pe.Class != "username_admin_refused" ||
						!strings.Contains(err.Error(), "Yönetici hesabı") || !strings.Contains(err.Error(), "admin account") ||
						!strings.Contains(buf.String(), "class=username_admin_refused user id=u1") {
						t.Fatalf("sayfa hatası/log: %v %s", err, buf.String())
					}
				}
				return
			}
			if err != nil || got == nil || got.ID != "u1" || got.Role != c.rows[0].Role {
				t.Fatalf("giriş bekleniyordu: u=%v err=%v", got, err)
			}
			if len(lk.got) != 1 || lk.got[0] != uname {
				t.Fatalf("ldap_username okuması: %v", lk.got)
			}
			out := buf.String()
			if !strings.Contains(out, "[oidc] email resolved via ldap_username user id=u1") || strings.Contains(out, "@") {
				t.Fatalf("başarı logu (e-postasız) bekleniyordu: %s", out)
			}
		})
	}
}

// F2 — dizin e-postasıyla (EmailSource=ldap, ViaUsername) gelen giriş de
// admin hesabını açık-seçim olmadan açmaz; viewer etkilenmez.
func TestOIDCLoginUserViaUsernameEmailPathAdmin(t *testing.T) {
	const email = "first.last@example.test"
	for _, allow := range []bool{false, true} {
		e := liveOIDCEnvAdmin(t, allow)
		st := &claimFakeStore{users: map[string]*chstore.User{
			email: {ID: "a1", Email: email, Role: auth.RoleAdmin, AuthProvider: "ldap"},
		}, admins: 2}
		prev := oidcUserStoreFor
		oidcUserStoreFor = func(*Server) oidcUserStore { return st }
		req := httptest.NewRequest("GET", "/api/auth/oidc/callback", nil)
		got, err := e.s.oidcLoginUser(req, email, &auth.OIDCClaims{Email: email, EmailSource: auth.OIDCEmailSourceLDAP, ViaUsername: true})
		oidcUserStoreFor = prev
		if allow != (err == nil && got != nil) || (!allow && !errors.Is(err, errOIDCUsernameAdminRefused)) {
			t.Fatalf("allow=%v: u=%v err=%v", allow, got, err)
		}
		// ViaUsername olmadan (id_token e-postası) admin değişmeden girer.
		oidcUserStoreFor = func(*Server) oidcUserStore { return st }
		got, err = e.s.oidcLoginUser(req, email, &auth.OIDCClaims{Email: email, EmailSource: auth.OIDCEmailSourceIDToken})
		oidcUserStoreFor = prev
		if err != nil || got == nil {
			t.Fatalf("id_token yolu etkilenmemeli: %v", err)
		}
	}
}

func TestOIDCEmailResolvedLogLines(t *testing.T) {
	e := liveOIDCEnv(t)
	st := &claimFakeStore{users: map[string]*chstore.User{
		"first.last@example.test": {ID: "u7", Email: "first.last@example.test", Role: auth.RoleViewer, AuthProvider: "ldap"},
	}, admins: 1}
	prev := oidcUserStoreFor
	oidcUserStoreFor = func(*Server) oidcUserStore { return st }
	t.Cleanup(func() { oidcUserStoreFor = prev })
	buf := captureAPILog(t)
	req := httptest.NewRequest("GET", "/api/auth/oidc/callback", nil)
	for _, src := range []string{auth.OIDCEmailSourceUserInfo, auth.OIDCEmailSourceLDAP, auth.OIDCEmailSourceIDToken} {
		buf.Reset()
		u, err := e.s.oidcLoginUser(req, "first.last@example.test", &auth.OIDCClaims{Email: "first.last@example.test", EmailSource: src})
		if err != nil || u == nil {
			t.Fatalf("%s: %v", src, err)
		}
		has := strings.Contains(buf.String(), "[oidc] email resolved via "+src+" user id=u7")
		if (src == auth.OIDCEmailSourceIDToken) == has || strings.Contains(buf.String(), "first.last") {
			t.Fatalf("%s log satırı: %q", src, buf.String())
		}
	}
	// Yeni kullanıcı: callback'in auto-provision satırı + çözüm satırı.
	buf.Reset()
	logOIDCProvisioned(&chstore.User{ID: "u9", Email: "new@example.test", Role: auth.RoleViewer},
		&auth.OIDCClaims{EmailSource: auth.OIDCEmailSourceLDAP})
	if !strings.Contains(buf.String(), `[oidc] auto-provisioned user "new@example.test" (role=viewer)`) ||
		!strings.Contains(buf.String(), "[oidc] email resolved via ldap user id=u9") {
		t.Fatalf("provision logu: %s", buf.String())
	}
}

func (e *oidcTestEnv) fallbackBody(secret string, on bool, claim string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(e.body(secret)), &m)
	m["usernameFallback"] = on
	m["usernameClaim"] = claim
	b, _ := json.Marshal(m)
	return string(b)
}

func TestOIDCSettingsUsernameFallbackRoundTripAndAudit(t *testing.T) {
	e := newOIDCTestEnv(t)
	var first map[string]any
	_ = json.Unmarshal([]byte(e.fallbackBody(oidcTestSecret, true, "upn")), &first)
	first["usernameFallbackAllowAdmin"] = true
	fb, _ := json.Marshal(first)
	if w := e.do(t, "PUT", "/api/settings/oidc", string(fb), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	type pair struct {
		Old any `json:"old"`
		New any `json:"new"`
	}
	var d struct {
		Fallback *pair `json:"usernameFallback"`
		Claim    *pair `json:"usernameClaim"`
		Admin    *pair `json:"usernameFallbackAllowAdmin"`
	}
	au := e.audits()
	if len(au) != 1 || json.Unmarshal([]byte(au[0].Details), &d) != nil || d.Fallback == nil || d.Claim == nil ||
		d.Fallback.Old != false || d.Fallback.New != true || d.Claim.Old != auth.UsernameClaimDefault || d.Claim.New != "upn" ||
		d.Admin == nil || d.Admin.Old != false || d.Admin.New != true {
		t.Fatalf("audit eski→yeni: %+v", au)
	}
	g := e.do(t, "GET", "/api/settings/oidc", "", auth.RoleAdmin)
	var snap auth.OIDCSnapshot
	if err := json.Unmarshal(g.Body.Bytes(), &snap); err != nil || !snap.UsernameFallback || snap.UsernameClaim != "upn" ||
		!snap.UsernameFallbackAllowAdmin {
		t.Fatalf("GET: %v %s", err, g.Body)
	}
	// Boş claim → varsayılan; kapatma.
	if w := e.do(t, "PUT", "/api/settings/oidc", e.fallbackBody("", false, ""), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("ikinci PUT %d %s", w.Code, w.Body)
	}
	au = e.audits()
	if len(au) != 1 || json.Unmarshal([]byte(au[0].Details), &d) != nil || d.Fallback.Old != true || d.Fallback.New != false ||
		d.Claim.New != auth.UsernameClaimDefault || d.Admin.Old != true || d.Admin.New != false {
		t.Fatalf("ikinci audit: %+v", au)
	}
	// Geçersiz / yasaklı claim → 400, yazım ve audit yok.
	puts := e.store.puts
	for _, bad := range []string{"email", "a b", "name"} {
		w := e.do(t, "PUT", "/api/settings/oidc", e.fallbackBody("", true, bad), auth.RoleAdmin)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Kullanıcı adı claim'i") {
			t.Fatalf("%q: %d %s", bad, w.Code, w.Body)
		}
	}
	if e.store.puts != puts || len(e.audits()) != 0 {
		t.Fatal("reddedilen PUT yan etki bıraktı")
	}
}

// F2 tamamlayıcısı — kullanıcı adıyla kurulan kimlikle açılan YENİ kullanıcı,
// açık-seçim yokken DefaultRole=admin olsa da admin olmaz (editor'a iner).
func TestOIDCProvisionRoleCapsAdminViaUsername(t *testing.T) {
	via := &auth.OIDCClaims{ViaUsername: true}
	cases := []struct {
		name   string
		def    string
		claims *auth.OIDCClaims
		allow  bool
		want   string
	}{
		{"ViaUsername + admin + açık-seçim yok → editor", auth.RoleAdmin, via, false, auth.RoleEditor},
		{"ViaUsername + admin + açık-seçim → admin", auth.RoleAdmin, via, true, auth.RoleAdmin},
		{"ViaUsername + editor → editor", auth.RoleEditor, via, false, auth.RoleEditor},
		{"ViaUsername + viewer → viewer", auth.RoleViewer, via, false, auth.RoleViewer},
		{"id_token e-postası + admin → admin (değişmedi)", auth.RoleAdmin, &auth.OIDCClaims{EmailSource: auth.OIDCEmailSourceIDToken}, false, auth.RoleAdmin},
		{"nil claims → değişmedi", auth.RoleAdmin, nil, false, auth.RoleAdmin},
	}
	for _, c := range cases {
		if got := oidcProvisionRole(c.def, c.claims, c.allow); got != c.want {
			t.Errorf("%s: %s, beklenen %s", c.name, got, c.want)
		}
	}
	src, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "role := oidcProvisionRole(s.oidc.DefaultRole(), claims, s.oidc.UsernameFallbackAllowAdmin())") {
		t.Fatal("callback yeni kullanıcı rolünü oidcProvisionRole'dan almıyor")
	}
}
