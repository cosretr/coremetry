package api

// oidc_next.go — v0.10.1123 (operatör: "OIDC girişinden sonra '/' değil,
// başta istediğim sayfaya düşeyim"). Derin bağlantı dönüşü artık SUNUCU
// tarafında: Login.tsx SSO düğmesi /api/auth/oidc/start?next=<yol> çağırır,
// oidcStart yolu süzüp kısa ömürlü HttpOnly coremetry_oidc_next çerezine
// yazar, oidcCallback başarıda o yola (yeniden süzerek) yönlendirir.
// sessionStorage (frontend/src/lib/postLoginRedirect.ts) yalnız yedek:
// sekme kapsamlıydı, IdP akışı başka sekmede/pencerede bitince kayboluyordu.
//
// Açık yönlendirme sınırı sanitizeOIDCNext — frontend sanitizeRedirect ile
// AYNI kurallar (iki uç birbirinden habersiz değişmesin: tablo testleri
// oidc_next_test.go + postLoginRedirect.test.ts). Tek fark: 2048 sınırı
// FE'de sanitizeRedirect'te değil oidcStartHref'te (sessionStorage yedeği
// her uzunluğu tutar; uzun bağlantı next'siz gider).

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	oidcNextCookie = "coremetry_oidc_next"
	oidcNextMaxLen = 2048
)

// sanitizeOIDCNext yalnız aynı-köken uygulama içi yolu kabul eder; aksi ""
// döner. Kurallar: tek '/' ile başlar ('//' değil), ters bölü yok, şema/host
// yok, kontrol karakteri (CR/LF dahil) yok, ≤2048 bayt; yol kısmının
// yüzde-çözülmüş hâli de aynı denetimden geçer (/%2F%2Fevil, /%61pi/x) ve
// /login, /public/, /api/ altında olamaz.
func sanitizeOIDCNext(raw string) string {
	if raw == "" || len(raw) > oidcNextMaxLen || !utf8.ValidString(raw) {
		return ""
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}
	if strings.Contains(raw, `\`) || hasControlChar(raw) {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return ""
	}
	p := raw
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	dec, err := url.PathUnescape(p)
	if err != nil || !utf8.ValidString(dec) {
		return ""
	}
	if !strings.HasPrefix(dec, "/") || strings.HasPrefix(dec, "//") ||
		strings.Contains(dec, `\`) || hasControlChar(dec) {
		return ""
	}
	// İnceleme düzeltmesi: '.'/'..' segmenti (ham ya da %2e) yasak — yoksa
	// /x/../api/foo blok listesini atlar (tarayıcı çözümlemesi /api/foo).
	if hasDotSegment(p) || hasDotSegment(dec) {
		return ""
	}
	for _, blocked := range []string{"/login", "/public", "/api"} {
		if dec == blocked || strings.HasPrefix(dec, blocked+"/") {
			return ""
		}
	}
	return raw
}

func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

func hasControlChar(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// setOIDCNextCookie — oidcStart: geçerli ?next= varsa diğer OIDC çerezleriyle
// aynı nitelik/TTL ile saklar; geçersiz/yoksa önceki (yarım kalmış bir
// girişten kalan) çerezi SİLER — bayat hedefe dönülmesin. Değer base64url:
// net/http çerez değerindeki ';', '"', boşluk vb.'yi düşürür, sorgulu derin
// bağlantı bozulmasın.
func setOIDCNextCookie(w http.ResponseWriter, r *http.Request) {
	next := sanitizeOIDCNext(r.URL.Query().Get("next"))
	if next == "" {
		c := setOIDCCookie(oidcNextCookie, "")
		c.MaxAge = -1
		http.SetCookie(w, c)
		return
	}
	http.SetCookie(w, setOIDCCookie(oidcNextCookie, base64.RawURLEncoding.EncodeToString([]byte(next))))
}

// oidcNextTarget — oidcCallback başarı hedefi: çerezdeki yol (yeniden
// süzülür; çerez kurcalanmış olabilir) yoksa "/".
func oidcNextTarget(r *http.Request) string {
	c, err := r.Cookie(oidcNextCookie)
	if err != nil {
		return "/"
	}
	b, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return "/"
	}
	if next := sanitizeOIDCNext(string(b)); next != "" {
		return next
	}
	return "/"
}

// clearOIDCCookies — callback'in HER çıkışında (başarı + hata) uçuştaki
// çerezleri siler. Path set edildiği yolla aynı olmalı: farklı Path ile
// MaxAge:-1 tarayıcıda o çerezi SİLMEZ (önceki "/" yolu yalnız 10 dk
// TTL'e güveniyordu).
func clearOIDCCookies(w http.ResponseWriter) {
	for _, name := range []string{oidcStateCookie, oidcNonceCookie, oidcVerifierCookie, oidcNextCookie} {
		c := setOIDCCookie(name, "")
		c.MaxAge = -1
		http.SetCookie(w, c)
	}
}
