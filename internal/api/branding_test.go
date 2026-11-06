package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1904 — denetim ayrıntısı login sayfasının dört alanını taşır; logo
// data URI'si yalnız bayt sayısıyla yazılır.
func TestBrandingAuditDetailsCarryLoginFields(t *testing.T) {
	raw := brandingAuditDetails(chstore.BrandingSettings{
		AppName: "Coremetry", LogoDataURI: "data:image/png;base64,AAAA",
		OrgName: "Acme", LoginSummary: "off", LoginQuote: "on", LoginTopology: "named",
	})
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"orgName": "Acme", "loginSummary": "off", "loginQuote": "on", "loginTopology": "named"} {
		if got[k] != want {
			t.Errorf("%s = %v, want %s", k, got[k], want)
		}
	}
	if got["logoBytes"] != float64(len("data:image/png;base64,AAAA")) {
		t.Errorf("logoBytes: %v", got["logoBytes"])
	}
	if strings.Contains(string(raw), "base64") {
		t.Error("logo data URI denetim satırına yazılmamalı")
	}
}

// Taşınan rotalar hâlâ kayıtlı: GET herkese açık (SkipPath), PUT admin kapılı
// (kimliksiz istek 401, handler'a ulaşmaz).
func TestBrandingRoutesRegisteredAfterMove(t *testing.T) {
	s := &Server{}
	mux := s.buildMux()
	for _, c := range []struct{ method, path string }{{"GET", "/api/branding"}, {"PUT", "/api/branding"}} {
		_, pattern := mux.Handler(httptest.NewRequest(c.method, c.path, nil))
		if pattern != c.method+" "+c.path {
			t.Errorf("%s %s kayıtlı değil (pattern=%q)", c.method, c.path, pattern)
		}
	}
	if !auth.SkipPath(http.MethodGet, "/api/branding") || auth.SkipPath(http.MethodPut, "/api/branding") {
		t.Error("GET /api/branding public, PUT değil")
	}
}
