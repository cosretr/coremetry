package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// v0.10.804 (dış skill denetimi M4) — CORS Origin allowlist: her Origin'i
// yansıtan cors() gitti; izinli = aynı-origin ∪ PublicURL ∪ liste.

func TestCORSPolicyAllow(t *testing.T) {
	p := newCORSPolicy([]string{"https://Apm.Example.com", " http://10.0.0.5:8080 ", "*", "bozuk", ""}, "https://public.example.com/")
	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"aynı-origin", "http://apm.internal:8080", "apm.internal:8080", true},
		{"aynı-origin büyük/küçük harf", "HTTP://APM.Internal:8080", "apm.internal:8080", true},
		{"aynı host farklı port", "http://apm.internal:9090", "apm.internal:8080", false},
		{"allowlist (harf duyarsız)", "https://apm.example.com", "other", true},
		{"allowlist port'lu", "http://10.0.0.5:8080", "other", true},
		{"PublicURL origin'i", "https://public.example.com", "backend:8080", true},
		{"yabancı origin", "https://evil.example", "apm.internal:8080", false},
		{"null origin", "null", "apm.internal:8080", false},
		{"yıldız allowlist değil", "*", "apm.internal:8080", false},
		{"boş origin", "", "apm.internal:8080", false},
		{"host boş", "https://x.example", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.allow(c.origin, c.host); got != c.want {
				t.Errorf("allow(%q,%q)=%v, istenen %v", c.origin, c.host, got, c.want)
			}
		})
	}
	// Sıfır değer (setter çağrılmamış Server) panik yapmaz, yalnız aynı-origin.
	var zero corsPolicy
	if !zero.allow("http://h:1", "h:1") || zero.allow("http://x:1", "h:1") {
		t.Error("sıfır politika: yalnız aynı-origin izinli olmalı")
	}
}

func TestCORSMiddlewareHeaders(t *testing.T) {
	p := newCORSPolicy([]string{"https://ok.example"}, "")
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	do := func(method, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://apm.internal:8080/api/x", nil)
		r.Host = "apm.internal:8080"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		p.middleware(ok).ServeHTTP(w, r)
		return w
	}
	t.Run("izinli origin → yansıt + credentials + Vary", func(t *testing.T) {
		w := do(http.MethodGet, "https://ok.example")
		if w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") != "https://ok.example" ||
			w.Header().Get("Access-Control-Allow-Credentials") != "true" || w.Header().Get("Vary") != "Origin" {
			t.Errorf("başlıklar: %d %v", w.Code, w.Header())
		}
	})
	t.Run("aynı-origin → izinli", func(t *testing.T) {
		if w := do(http.MethodPost, "http://apm.internal:8080"); w.Header().Get("Access-Control-Allow-Origin") == "" {
			t.Errorf("aynı-origin başlıksız kaldı: %v", w.Header())
		}
	})
	t.Run("izinsiz origin → başlık yok, handler yine koşar, Vary var", func(t *testing.T) {
		w := do(http.MethodGet, "https://evil.example")
		if w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") != "" ||
			w.Header().Get("Access-Control-Allow-Credentials") != "" || w.Header().Get("Vary") != "Origin" {
			t.Errorf("izinsiz: %d %v", w.Code, w.Header())
		}
	})
	t.Run("izinsiz preflight → 403", func(t *testing.T) {
		if w := do(http.MethodOptions, "https://evil.example"); w.Code != http.StatusForbidden {
			t.Errorf("preflight %d", w.Code)
		}
	})
	t.Run("izinli preflight → 204", func(t *testing.T) {
		if w := do(http.MethodOptions, "https://ok.example"); w.Code != http.StatusNoContent {
			t.Errorf("preflight %d", w.Code)
		}
	})
	t.Run("Origin yok → CORS başlığı yok, geçer (eski '*' de yok)", func(t *testing.T) {
		w := do(http.MethodGet, "")
		if w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("origin'siz: %d %v", w.Code, w.Header())
		}
	})
}

func TestCORSRequireOrigin(t *testing.T) {
	p := newCORSPolicy(nil, "https://public.example.com")
	called := false
	h := p.requireOrigin(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusOK) })
	do := func(origin string) int {
		called = false
		r := httptest.NewRequest(http.MethodPost, "http://backend:8080/api/mcp", nil)
		r.Host = "backend:8080"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	if code := do("https://evil.example"); code != http.StatusForbidden || called {
		t.Errorf("yabancı origin: %d called=%v", code, called)
	}
	if code := do("https://public.example.com"); code != http.StatusOK || !called {
		t.Errorf("PublicURL origin: %d called=%v", code, called)
	}
	if code := do("http://backend:8080"); code != http.StatusOK || !called {
		t.Errorf("aynı-origin: %d called=%v", code, called)
	}
	if code := do(""); code != http.StatusOK || !called {
		t.Errorf("Origin'siz CLI: %d called=%v", code, called)
	}
}

// Ulaşılabilirlik pini: politika listen zincirinde ve üç MCP rotasında.
func TestCORSPolicyWired(t *testing.T) {
	src, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "s.cors.middleware(s.auth.Middleware(") {
		t.Error("listen zinciri s.cors.middleware kullanmıyor")
	}
	if strings.Contains(s, "func cors(") {
		t.Error("eski yansıtan cors() hâlâ api.go'da")
	}
	for _, route := range []string{`"GET /api/mcp/sse"`, `"POST /api/mcp/messages"`, `"POST /api/mcp"`} {
		i := strings.Index(s, route)
		if i < 0 {
			t.Fatalf("rota yok: %s", route)
		}
		line := s[i:min(i+120, len(s))]
		if !strings.Contains(line, "s.cors.requireOrigin(") {
			t.Errorf("%s requireOrigin ile sarılı değil: %s", route, line)
		}
	}
	mainSrc, err := os.ReadFile("../../main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainSrc), "srv.SetAllowedOrigins(cfg.AllowedOrigins, cfg.PublicURL)") {
		t.Error("main.go SetAllowedOrigins çağırmıyor")
	}
}
