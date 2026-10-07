package api

// auth_oidc_email_trust_test.go — v0.10.1120 (operatör, prod: "[oidc]
// callback failed: class=email_unverified"; kurumsal IdP email_verified=false
// gönderiyor).
//
// NE ÇİVİLİYOR (HTTP sözleşmesi; karar tablosu auth/oidc_email_trust_test.go'da):
// trustUnverifiedEmail PUT→GET gidiş-dönüşü; audit eski→yeni; izinli alan adı
// listesi boşken 400 + TR/EN metin, hiçbir şey yazılmaz/audit'lenmez; kapatma
// anahtarı mutlak (SSO kapalı gövde kaydedilir), aynı çiftle yeniden açma 400.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// Güvenlik incelemesi — güven anahtarıyla (ViaTrust) gelen giriş yerel/LDAP ya
// da admin hesabına bağlanmaz; yeni kullanıcı ve oidc viewer/editor etkilenmez;
// doğrulanmış giriş değişmedi.
func TestOIDCLoginUserViaTrustLinking(t *testing.T) {
	const email = "user12345@example.test"
	cases := []struct {
		name     string
		existing *chstore.User
		viaTrust bool
		wantErr  bool
		wantNil  bool
		wantRole string
	}{
		{"güven + yerel hesap → red", &chstore.User{ID: "u1", Email: email, Role: auth.RoleViewer, AuthProvider: "local"}, true, true, false, ""},
		{"güven + sağlayıcısı boş (depoda local) → red", &chstore.User{ID: "u1", Email: email, Role: auth.RoleEditor}, true, true, false, ""},
		{"güven + LDAP hesabı → red", &chstore.User{ID: "u1", Email: email, Role: auth.RoleViewer, AuthProvider: "ldap"}, true, true, false, ""},
		{"güven + oidc admin → red", &chstore.User{ID: "u1", Email: email, Role: auth.RoleAdmin, AuthProvider: "oidc"}, true, true, false, ""},
		{"güven + oidc editor → giriş", &chstore.User{ID: "u1", Email: email, Role: auth.RoleEditor, AuthProvider: "oidc"}, true, false, false, auth.RoleEditor},
		{"güven + oidc viewer → giriş", &chstore.User{ID: "u1", Email: email, Role: auth.RoleViewer, AuthProvider: "oidc"}, true, false, false, auth.RoleViewer},
		{"güven + yeni kullanıcı → (nil,nil): callback varsayılan rolle açar", nil, true, false, true, ""},
		{"doğrulanmış + yerel admin → giriş (değişmedi)", &chstore.User{ID: "u1", Email: email, Role: auth.RoleAdmin, AuthProvider: "local"}, false, false, false, auth.RoleAdmin},
		{"doğrulanmış + oidc admin → giriş (değişmedi)", &chstore.User{ID: "u1", Email: email, Role: auth.RoleAdmin, AuthProvider: "oidc"}, false, false, false, auth.RoleAdmin},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newOIDCTestEnv(t)
			st := &claimFakeStore{users: map[string]*chstore.User{}, admins: 1}
			if c.existing != nil {
				st.users[email] = c.existing
			}
			prev := oidcUserStoreFor
			oidcUserStoreFor = func(*Server) oidcUserStore { return st }
			t.Cleanup(func() { oidcUserStoreFor = prev })
			req := httptest.NewRequest("GET", "/api/auth/oidc/callback", nil)
			u, err := e.s.oidcLoginUser(req, email, &auth.OIDCClaims{Email: email, ViaTrust: c.viaTrust})
			if c.wantErr {
				var pe *oidcLoginPageError
				if !errors.As(err, &pe) || pe.Class != "email_unverified_privileged" || u != nil || len(st.ups) != 0 ||
					!strings.Contains(err.Error(), "Bu hesap doğrulanmamış e-posta ile SSO'dan açılamaz") ||
					!strings.Contains(err.Error(), "Trust Email") {
					t.Fatalf("email_unverified_privileged bekleniyordu: u=%v err=%v", u, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.wantNil {
				if u != nil || len(st.ups) != 0 {
					t.Fatalf("yeni kullanıcıya dokunuldu: %v", u)
				}
				return
			}
			if u == nil || u.Role != c.wantRole {
				t.Fatalf("dönen kullanıcı %v, beklenen rol %s", u, c.wantRole)
			}
		})
	}
}

// Giriş sayfasına yönlendirme: callback oidcLoginUser hatasını oidcFail ile
// basar; metin TR/EN cümleyi aynen taşır (e-posta yok).
func TestOIDCEmailUnverifiedPrivilegedRedirect(t *testing.T) {
	e := newOIDCTestEnv(t)
	w := httptest.NewRecorder()
	e.s.oidcFail(w, httptest.NewRequest("GET", "/api/auth/oidc/callback", nil), errOIDCEmailUnverifiedPrivileged.Error())
	loc := w.Header().Get("Location")
	if w.Code != http.StatusFound || !strings.HasPrefix(loc, "/login/?error=") {
		t.Fatalf("yönlendirme yok: %d %s", w.Code, loc)
	}
	q, _ := url.ParseQuery(strings.TrimPrefix(loc, "/login/?"))
	if q.Get("error") != errOIDCEmailUnverifiedPrivileged.Error() || strings.Contains(loc, "%40") {
		t.Fatalf("metin yanlış: %s", loc)
	}
}

func (e *oidcTestEnv) trustBody(secret string, trust bool, domains []string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(e.body(secret)), &m)
	m["trustUnverifiedEmail"] = trust
	m["allowedDomains"] = domains
	b, _ := json.Marshal(m)
	return string(b)
}

type trustAuditDetails struct {
	Trust *struct {
		Old bool `json:"old"`
		New bool `json:"new"`
	} `json:"trustUnverifiedEmail"`
}

func lastTrustAudit(t *testing.T, e *oidcTestEnv) (trustAuditDetails, string) {
	t.Helper()
	au := e.audits()
	if len(au) != 1 || au[0].Action != "settings.oidc.update" {
		t.Fatalf("audit yok/yanlış: %+v", au)
	}
	var d trustAuditDetails
	if err := json.Unmarshal([]byte(au[0].Details), &d); err != nil || d.Trust == nil {
		t.Fatalf("audit ayrıntısı çözülemedi / alan yok: %v %s", err, au[0].Details)
	}
	return d, au[0].Details
}

func TestOIDCSettingsTrustUnverifiedEmailRoundTripAndAudit(t *testing.T) {
	e := newOIDCTestEnv(t)
	doms := []string{"example.test"}
	// 1) Açılış: false → true.
	if w := e.do(t, "PUT", "/api/settings/oidc", e.trustBody(oidcTestSecret, true, doms), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	if d, raw := lastTrustAudit(t, e); d.Trust.Old || !d.Trust.New {
		t.Fatalf("audit eski→yeni yanlış: %s", raw)
	}
	g := e.do(t, "GET", "/api/settings/oidc", "", auth.RoleAdmin)
	var snap auth.OIDCSnapshot
	if err := json.Unmarshal(g.Body.Bytes(), &snap); err != nil || !snap.TrustUnverifiedEmail {
		t.Fatalf("GET trustUnverifiedEmail: %v %s", err, g.Body)
	}
	if !strings.Contains(string(e.store.rows[auth.OIDCSettingsKey]), `"trustUnverifiedEmail":true`) {
		t.Fatalf("blob alanı taşımıyor: %s", e.store.rows[auth.OIDCSettingsKey])
	}
	// 2) Kapanış: true → false.
	if w := e.do(t, "PUT", "/api/settings/oidc", e.trustBody("", false, doms), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("ikinci PUT %d %s", w.Code, w.Body)
	}
	if d, raw := lastTrustAudit(t, e); !d.Trust.Old || d.Trust.New {
		t.Fatalf("ikinci audit: %s", raw)
	}
}

func TestOIDCSettingsTrustUnverifiedEmailRequiresAllowedDomains(t *testing.T) {
	e := newOIDCTestEnv(t)
	for name, body := range map[string]string{
		"boş liste":     e.trustBody(oidcTestSecret, true, []string{}),
		"yalnız boşluk": e.trustBody(oidcTestSecret, true, []string{" , "}),
	} {
		w := e.do(t, "PUT", "/api/settings/oidc", body, auth.RoleAdmin)
		if w.Code != http.StatusBadRequest ||
			!strings.Contains(w.Body.String(), "İzinli alan adları boşken doğrulanmamış e-postaya güvenilemez") ||
			!strings.Contains(w.Body.String(), "allowed domains is empty") {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if e.store.puts != 0 || len(e.audits()) != 0 {
		t.Fatal("reddedilen PUT yan etki bıraktı")
	}
	// Kapatma anahtarı mutlak: SSO kapalı + boş liste + güven → kaydedilir.
	var m map[string]any
	_ = json.Unmarshal([]byte(e.trustBody(oidcTestSecret, true, []string{})), &m)
	m["enabled"] = false
	disabled, _ := json.Marshal(m)
	if w := e.do(t, "PUT", "/api/settings/oidc", string(disabled), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("SSO kapalı gövde reddedildi: %d %s", w.Code, w.Body)
	}
	if e.store.puts != 1 || len(e.audits()) != 1 {
		t.Fatal("kapalı kayıt yazılmadı/audit'lenmedi")
	}
	// Aynı çiftle yeniden açma → 400, yazım yok.
	m["enabled"] = true
	reenable, _ := json.Marshal(m)
	if w := e.do(t, "PUT", "/api/settings/oidc", string(reenable), auth.RoleAdmin); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "İzinli alan adları boşken doğrulanmamış e-postaya güvenilemez") {
		t.Fatalf("yeniden açma: %d %s", w.Code, w.Body)
	}
	if e.store.puts != 1 || len(e.audits()) != 0 {
		t.Fatal("reddedilen yeniden açma yan etki bıraktı")
	}
}
