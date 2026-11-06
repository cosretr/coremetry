package api

// branding.go — v0.10.1904 (yeni login sayfası, docs/frontend/login-redesign.md §5.7).
//
// api.go BÜYÜMEYECEK kuralı (TestApiGoDoesNotGrow): getBranding / putBranding
// ve iki rotası api.go'dan buraya taşındı; kayıt init()'te registerRoutesExtra
// ile. Davranış aynı — yalnız denetim ayrıntısına login sayfasının dört yeni
// alanı eklendi (orgName, loginSummary, loginQuote, loginTopology).
//
//   GET /api/branding   — herkese açık (auth.SkipPath, yalnız GET): giriş sayfası
//                         oturumdan ÖNCE logo + metinleri okur.
//   PUT /api/branding   — yalnız admin; gövde 256 KB ile sınırlı (logo data URI'si
//                         system_settings'i şişirmesin).

import (
	"encoding/json"
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("branding", (*Server).registerBrandingRoutes) }

func (s *Server) registerBrandingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/branding", s.getBranding)
	mux.HandleFunc("PUT /api/branding", auth.RequireRole(auth.RoleAdmin, s.putBranding))
}

// brandingBodyMax — PUT gövde tavanı (~30 KB'lık PNG'ler için fazlasıyla yeter).
const brandingBodyMax = 256 * 1024

// getBranding returns the admin-customised branding overlay (logo + strings +
// primary color + login page switches). Public — the login page fetches this
// before the operator has a session so the logo + login title can render on
// first paint. An empty struct is a valid response (no overrides saved yet);
// the SPA falls back to the bundled Coremetry defaults in that case.
func (s *Server) getBranding(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.GetBranding(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, b)
}

// putBranding overwrites the saved overlay. Admin-gated by the mux. Capped at
// 256 KB to keep a misconfigured logo upload from bloating system_settings.
func (s *Server) putBranding(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, brandingBodyMax)
	var b chstore.BrandingSettings
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, "invalid body (or > 256 KB): "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.PutBranding(r.Context(), b); err != nil {
		writeErr(w, err)
		return
	}
	// v0.5.325 — Scale-audit gap: every admin mutation writes an audit row.
	// Branding edits change customer-visible logos + link/footer copy, so the
	// compliance trail matters.
	s.audit(r, "settings.branding.update", "settings", "branding", string(brandingAuditDetails(b)))
	writeJSON(w, b)
}

// brandingAuditDetails — SAF: denetim ayrıntısı. Logo yalnız bayt sayısıyla
// (data URI'nin kendisi denetim satırına yazılmaz). v0.10.1904: login sayfası
// alanları normalize EDİLMEMİŞ gönderilen değerle yazılır (operatörün girdiği).
func brandingAuditDetails(b chstore.BrandingSettings) []byte {
	details, _ := json.Marshal(map[string]any{
		"appName":       b.AppName,
		"loginTitle":    b.LoginTitle,
		"footerText":    b.FooterText,
		"language":      b.Language,
		"primaryColor":  b.PrimaryColor,
		"logoBytes":     len(b.LogoDataURI),
		"orgName":       b.OrgName,
		"loginSummary":  b.LoginSummary,
		"loginQuote":    b.LoginQuote,
		"loginTopology": b.LoginTopology,
	})
	return details
}
