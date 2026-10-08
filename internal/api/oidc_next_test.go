package api

// oidc_next_test.go — v0.10.1123: OIDC sonrası sunucu tarafı derin bağlantı
// dönüşü. Açık yönlendirme sınırı (sanitizeOIDCNext) tablo testi + çerez
// yaşam döngüsü (start'ta yazılır, callback'in her çıkışında silinir) +
// başarı hedefi (çerez → yol, yoksa "/").

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
)

func TestSanitizeOIDCNext(t *testing.T) {
	long := "/services/" + strings.Repeat("a", oidcNextMaxLen)
	cases := []struct {
		name, in, want string
	}{
		{"valid deep link", "/services/svc-orders?x=1#tab", "/services/svc-orders?x=1#tab"},
		{"root", "/", "/"},
		{"encoded query kept", "/traces?filters=%5B%7B%22k%22%3A1%7D%5D", "/traces?filters=%5B%7B%22k%22%3A1%7D%5D"},
		{"protocol-relative", "//evil.example", ""},
		{"backslash", `/\evil`, ""},
		{"backslash later", `/a\b`, ""},
		{"absolute https", "https://x", ""},
		{"javascript scheme", "javascript:alert(1)", ""},
		{"encoded double slash", "/%2F%2Fevil", ""},
		{"encoded backslash", "/%5Cevil", ""},
		{"login", "/login", ""},
		{"login query", "/login?error=x", ""},
		{"login sub", "/login/", ""},
		{"public", "/public/trace?token=abc", ""},
		{"api", "/api/x", ""},
		{"api bare", "/api", ""},
		{"api encoded", "/%61pi/x", ""},
		{"CRLF", "/x\r\nSet-Cookie: a=b", ""},
		{"encoded CRLF", "/x%0d%0aSet-Cookie:a", ""},
		{"NUL", "/x\x00", ""},
		{"very long", long, ""},
		{"empty", "", ""},
		{"relative", "services", ""},
		{"bad escape", "/%zz", ""},
		{"dotdot to api", "/x/../api/foo", ""},
		{"encoded dotdot to api", "/%2e%2e/api/foo", ""},
		{"mixed-case encoded dot", "/x/%2E./login", ""},
		{"single dot", "/./services", ""},
		{"dots inside name ok", "/services/svc.orders..v2", "/services/svc.orders..v2"},
	}
	for _, c := range cases {
		if got := sanitizeOIDCNext(c.in); got != c.want {
			t.Errorf("%s: sanitizeOIDCNext(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
	// Uzunluk sınırı tam 2048'de kabul eder.
	edge := "/" + strings.Repeat("a", oidcNextMaxLen-1)
	if sanitizeOIDCNext(edge) != edge {
		t.Error("2048 baytlık yol reddedildi")
	}
}

func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestSetOIDCNextCookie(t *testing.T) {
	want := "/services/svc-orders?x=1;y=\"z\"#tab"
	w := httptest.NewRecorder()
	setOIDCNextCookie(w, httptest.NewRequest("GET", "/api/auth/oidc/start?next="+url.QueryEscape(want), nil))
	c := findCookie(w, oidcNextCookie)
	if c == nil {
		t.Fatal("next çerezi yazılmadı")
	}
	ref := setOIDCCookie(oidcNextCookie, "")
	if !c.HttpOnly || c.Path != ref.Path || c.MaxAge != ref.MaxAge || c.SameSite != ref.SameSite {
		t.Fatalf("çerez nitelikleri diğer OIDC çerezleriyle aynı değil: %+v", c)
	}
	// Geri okuma: değer bozulmadan döner.
	req := httptest.NewRequest("GET", "/api/auth/oidc/callback", nil)
	req.AddCookie(c)
	if got := oidcNextTarget(req); got != want {
		t.Fatalf("hedef %q, want %q", got, want)
	}

	for _, bad := range []string{"", "//evil.example", "/api/x", "https://x"} {
		w := httptest.NewRecorder()
		setOIDCNextCookie(w, httptest.NewRequest("GET", "/api/auth/oidc/start?next="+url.QueryEscape(bad), nil))
		// Geçersiz/yok → önceki girişten kalmış çerez silinir (aynı Path).
		c := findCookie(w, oidcNextCookie)
		if c == nil || c.MaxAge >= 0 || c.Value != "" || c.Path != ref.Path {
			t.Errorf("geçersiz next %q: bayat çerez silinmedi: %+v", bad, c)
		}
	}
}

func TestOIDCNextTarget(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	cases := []struct {
		name   string
		cookie *string
		want   string
	}{
		{"absent → /", nil, "/"},
		{"valid", ptr(enc("/services/svc-orders?x=1#tab")), "/services/svc-orders?x=1#tab"},
		{"tampered open redirect", ptr(enc("//evil.example")), "/"},
		{"tampered api", ptr(enc("/api/x")), "/"},
		{"not base64", ptr("%%%"), "/"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/api/auth/oidc/callback", nil)
		if c.cookie != nil {
			req.AddCookie(&http.Cookie{Name: oidcNextCookie, Value: *c.cookie})
		}
		if got := oidcNextTarget(req); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func ptr(s string) *string { return &s }

// Callback'in hata çıkışları da next dahil tüm uçuştaki çerezleri siler
// (set edildikleri Path ile — farklı Path tarayıcıda silmez).
func TestOIDCCallbackClearsNextCookieOnFailure(t *testing.T) {
	e := newOIDCTestEnv(t)
	if w := e.do(t, "PUT", "/api/settings/oidc", e.body(oidcTestSecret), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	for _, path := range []string{
		"/api/auth/oidc/callback?error=access_denied",
		"/api/auth/oidc/callback",
		"/api/auth/oidc/callback?code=c1&state=WRONG",
	} {
		req := httptest.NewRequest("GET", path, nil)
		for k, v := range map[string]string{oidcStateCookie: "s1", oidcNonceCookie: "n1", oidcVerifierCookie: "v1",
			oidcNextCookie: base64.RawURLEncoding.EncodeToString([]byte("/services/svc-orders"))} {
			req.AddCookie(&http.Cookie{Name: k, Value: v})
		}
		w := httptest.NewRecorder()
		e.s.oidcCallback(w, req)
		if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/login/?error=") {
			t.Fatalf("%s: Location %q", path, loc)
		}
		for _, name := range []string{oidcStateCookie, oidcNonceCookie, oidcVerifierCookie, oidcNextCookie} {
			c := findCookie(w, name)
			if c == nil || c.MaxAge >= 0 || c.Path != "/api/auth/oidc/" {
				t.Errorf("%s: %s silinmedi: %+v", path, name, c)
			}
		}
	}
}

// Başarı yolu: clearOIDCCookies + oidcNextTarget birlikte — hedef çerezden,
// çerez yanıtta siliniyor. (Tam token takası sahte imzalı ID token ister;
// callback'in son satırı bu iki yardımcıyı aynen çağırır.)
func TestOIDCSuccessRedirectUsesAndClearsNext(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/auth/oidc/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: oidcNextCookie,
		Value: base64.RawURLEncoding.EncodeToString([]byte("/services/svc-orders?x=1#tab"))})
	w := httptest.NewRecorder()
	clearOIDCCookies(w)
	http.Redirect(w, req, oidcNextTarget(req), http.StatusFound)
	if loc := w.Header().Get("Location"); loc != "/services/svc-orders?x=1#tab" {
		t.Fatalf("Location %q", loc)
	}
	if c := findCookie(w, oidcNextCookie); c == nil || c.MaxAge >= 0 {
		t.Fatalf("next çerezi silinmedi: %+v", c)
	}
}
