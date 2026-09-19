package api

import (
	"net/http"
	"net/url"
	"strings"
)

// v0.10.804 — dış skill denetimi 2026-09-19 M4 (agents-mcp: HTTP'ye açık
// MCP'de Origin doğrula). Eski cors() HER Origin'i yansıtıp
// Access-Control-Allow-Credentials: true yazıyordu: tarayıcıdaki herhangi
// bir site, çerez-JWT'li oturumla /api/* (ve tools/call) cevabını
// OKUYABİLİRDİ (SameSite=Lax çerezi çapraz-site fetch'te göndermediği için
// bugün fiilen kapalıydı; ama doğruluk bir çerez bayrağına asılıydı —
// bkz. feedback-correctness-held-by-a-setting).
//
// Politika: bir Origin şunlardan biriyse "izinli":
//   - aynı-origin: Origin'in host[:port]'u isteğin Host başlığına eşit
//     (büyük/küçük harf duyarsız; tarayıcı aynı-origin POST'ta da Origin
//     gönderir, o yüzden bu dal olmadan kendi UI'mız /api/mcp'de 403 yerdi),
//   - COREMETRY_PUBLIC_URL'in origin'i (ters proxy Host'u yeniden yazsa da),
//   - COREMETRY_ALLOWED_ORIGINS listesinde.
//
// İzinsiz Origin'de CORS başlığı YAZILMAZ (tarayıcı cevabı engeller);
// preflight 403; /api/mcp* handler'a girmeden 403 döner. Origin başlığı
// olmayan istek (curl, sunucu-sunucu, aynı-origin GET) CORS'a tabi değil,
// başlık yazılmaz ve geçer.
type corsPolicy struct {
	allowed map[string]struct{} // normalizeOrigin edilmiş origin'ler
}

// newCORSPolicy — SAF: liste + PublicURL origin'i (parse edilemeyen/boş
// girdiler sessizce düşer; "*" allowlist değildir, yok sayılır).
func newCORSPolicy(origins []string, publicURL string) corsPolicy {
	p := corsPolicy{allowed: map[string]struct{}{}}
	for _, o := range append(append([]string{}, origins...), publicURL) {
		if n := normalizeOrigin(o); n != "" {
			p.allowed[n] = struct{}{}
		}
	}
	return p
}

// normalizeOrigin — "HTTPS://Apm.Example.com:443/x" → "https://apm.example.com:443";
// şema+host yoksa "".
func normalizeOrigin(o string) string {
	o = strings.TrimSpace(o)
	if o == "" || o == "*" || o == "null" {
		return ""
	}
	u, err := url.Parse(o)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

// allow — SAF: bkz. tip yorumu. host = isteğin Host başlığı.
func (p corsPolicy) allow(origin, host string) bool {
	n := normalizeOrigin(origin)
	if n == "" {
		return false
	}
	if _, ok := p.allowed[n]; ok {
		return true
	}
	if h := strings.ToLower(strings.TrimSpace(host)); h != "" && strings.HasSuffix(n, "://"+h) {
		return true
	}
	return false
}

// middleware — CORS başlıklarını yalnız izinli Origin'e yazar.
func (p corsPolicy) middleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			// Vary her durumda: izinli origin için yazılmış cevap bir
			// paylaşımlı önbellekten başka origin'e servis edilmesin.
			w.Header().Add("Vary", "Origin")
			if !p.allow(origin, r.Host) {
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				h.ServeHTTP(w, r) // başlıksız: tarayıcı cevabı okuyamaz
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// requireOrigin — /api/mcp* için: Origin var ve izinsizse 403 (handler'a
// girmeden); Origin yoksa (CLI/ajan istemcileri) aynen geçer.
func (p corsPolicy) requireOrigin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !p.allow(origin, r.Host) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"origin not allowed"}`))
			return
		}
		h(w, r)
	}
}

// SetAllowedOrigins — main.go: COREMETRY_ALLOWED_ORIGINS + COREMETRY_PUBLIC_URL.
func (s *Server) SetAllowedOrigins(origins []string, publicURL string) {
	s.cors = newCORSPolicy(origins, publicURL)
}
