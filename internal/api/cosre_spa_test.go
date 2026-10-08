package api

// cosre_spa_test.go — v0.10.1125: /cosre bağımsız CoSRE sohbet sayfası.
// Sunucu tarafında yeni kod YOK — bu test, sayfanın dayandığı iki mevcut
// sözleşmeyi çiviler:
//   1. spaHandler /cosre (ve sorgulu /cosre?chat=…, sonda '/' ile) için
//      kök index.html'i döner — react-router sayfayı URL'den kurar; bir gün
//      SPA yedeğine yol listesi eklenirse /cosre düşmesin.
//   2. OIDC ?next= süzgeci /cosre'yi kabul eder — giriş sonrası operatör
//      sohbet sayfasına geri gelir (v0.10.1123 derin bağlantı dönüşü).

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestSPAHandlerServesCosre(t *testing.T) {
	const index = "<!doctype html><title>spa-index</title>"
	root := fstest.MapFS{
		"index.html":        {Data: []byte(index)},
		"favicon.svg":       {Data: []byte("<svg/>")},
		"assets/app-abc.js": {Data: []byte("console.log(1)")},
	}
	h := spaHandler(root)
	for _, p := range []string{"/cosre", "/cosre/", "/cosre?chat=c1"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", p, rec.Code)
		}
		body, _ := io.ReadAll(rec.Body)
		if string(body) != index {
			t.Fatalf("%s: body %q, want SPA index", p, body)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("%s: Cache-Control %q, want no-cache (index revalidates)", p, cc)
		}
	}
	// Uzantılı eksik dosya hâlâ gerçek 404 (yedek yalnız uzantısız yollara).
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cosre.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/cosre.js: status %d, want 404", rec.Code)
	}
}

func TestSanitizeOIDCNextAllowsCosre(t *testing.T) {
	for _, in := range []string{"/cosre", "/cosre?chat=c1"} {
		if got := sanitizeOIDCNext(in); got != in {
			t.Fatalf("sanitizeOIDCNext(%q) = %q, want unchanged", in, got)
		}
	}
}
