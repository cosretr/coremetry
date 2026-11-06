package auth

// skippath_login_summary_test.go — v0.10.1905: anonim login özeti ucu
// SkipPath'te YALNIZ GET ve TAM yol için muaftır (giriş sayfası oturumdan önce
// okur). Alt yol, eğik çizgili yol, benzer önek ve diğer metotlar kimlik ister.

import (
	"net/http"
	"testing"
)

func TestSkipPathLoginSummaryGETExactOnly(t *testing.T) {
	const p = "/api/public/login-summary"
	if !SkipPath(http.MethodGet, p) {
		t.Fatalf("GET %s muaf olmalı", p)
	}
	if !SkipPath(http.MethodOptions, p) {
		t.Errorf("OPTIONS zaten muaf (CORS ön-kontrolü)")
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead} {
		if SkipPath(m, p) {
			t.Errorf("%s %s muaf olmamalı", m, p)
		}
	}
	for _, path := range []string{p + "/", p + "/x", p + "x", "/api/public/login-summar", "/api/public/login-summary/../branding-x", "/api/public/"} {
		if SkipPath(http.MethodGet, path) {
			t.Errorf("GET %s muaf olmamalı", path)
		}
	}
}
