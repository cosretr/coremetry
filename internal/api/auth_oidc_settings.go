package api

// auth_oidc_settings.go — v0.10.1067 (operatör: "Settings'ten
// yönetebilsem Helm'e göre daha iyi olur").
//
// api.go BÜYÜMEYECEK kuralı (TestApiGoDoesNotGrow): yüzeyin rotaları
// kendi dosyasında, kayıt init()'te registerRoutesExtra ile.
//
//	GET  /api/settings/oidc        admin — etkin yapılandırma, secret YOK (clientSecretStored)
//	PUT  /api/settings/oidc        admin — kaydet + canlı takas + audit settings.oidc.update + publishConfigReload("oidc")
//
// v0.10.1110 — aynı blob merkezi login yetki servisinin ayarlarını da taşır
// (permissionService*, permissionsClaim, roleFromClaim; uç
// auth_permissions.go). Anahtar clientSecret gibi: GET'te yok
// (permissionServiceKeySet), boş PUT kayıtlıyı korur, audit'e girmez.
// v0.10.1111 — permissionServiceAllowNoKey / AllowedCIDRs / TrustedProxies
// (secret değil: GET'te ve audit'te aynen).
// v0.10.1112 — tlsCACertPEM / tlsInsecureSkipVerify (IdP TLS güveni,
// auth/oidc_tls.go): secret değil, GET'te aynen; audit'e eski→yeni girer
// (CA için PEM değil özet: CN + SHA-256 parmak izi + bitiş). Test ucu formun
// KAYDEDİLMEMİŞ TLS ayarını kullanır.
// v0.10.1120 — trustUnverifiedEmail (auth/oidc_email_trust.go): secret
// değil, GET'te aynen; audit'e eski→yeni; izinli alan adı listesi boşken 400.
// v0.10.1121 — usernameFallback / usernameClaim (auth/oidc_email_resolve.go):
// secret değil, GET'te aynen; audit'e eski→yeni; geçersiz claim adı 400.
//	POST /api/settings/oidc/test   admin — yalnız keşif (≤10 s); hiçbir şey yazmaz
//
// ÜÇÜ DE ADMIN ve yalnız OTURUM kullanıcısı: API token'ı (UserID
// "token:<id>", chat_source_code.go sourceCodeCallerAllowed emsali) admin
// rolünde olsa bile 403 — IdP yapılandırması otomasyonla değiştirilecek bir
// şey değil ve bir token sızıntısı SSO'yu saldırgan IdP'ye çeviremesin. GET
// viewer'a açılmadı: cevap IdP adresini, client id'yi ve izinli alan
// adlarını taşıyor ve Settings sayfası zaten admin-only. Login sayfasının
// ihtiyacı (SSO açık mı + düğme etiketi) public /api/auth/config'te.
//
// Test ucu iç-ağ okuyucusu OLAMAZ: admin + oturum, https, dial anında
// loopback/link-local/metadata reddi, süre/boyut sınırı, ve TEK genel hata
// cümlesi (durum kodu, gövde, JSON ayrımı yok) — auth/oidc.go. Özel ağ
// (banka içi IdP) serbest; kalan "bu özel https ana makinesi ayakta mı"
// bilgisinin dengeleyicisi: her test çağrısı audit'lenir (settings.oidc.test,
// issuer + ok; gövde/secret yok). Bağlantı denemesinin başarısızlığı
// operatörün sorusuna başarılı bir cevaptır: 200 + {ok:false, error}
// (vmetrics_handlers.go emsali).
//
// oidcLoginFail — api.go oidcCallback'in token değişimi hatası: kullanıcıya
// genel mesaj, loga yalnız sabit sınıf. IdP'nin token/JWKS cevap gövdesi
// (oauth2 RetrieveError, go-oidc) ne /login/?error='a ne loga gider.
//
// Mantık (normalize/doğrulama/öncelik/secret birleştirme/takas)
// internal/auth/oidc_settings.go'da; burası yalnız HTTP kabuğu.

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("auth-oidc-settings", (*Server).registerOIDCSettingsRoutes) }

func (s *Server) registerOIDCSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/oidc", auth.RequireRole(auth.RoleAdmin, oidcSessionOnly(s.getOIDCSettings)))
	mux.HandleFunc("PUT /api/settings/oidc", auth.RequireRole(auth.RoleAdmin, oidcSessionOnly(s.putOIDCSettings)))
	mux.HandleFunc("POST /api/settings/oidc/test", auth.RequireRole(auth.RoleAdmin, oidcSessionOnly(s.testOIDCSettings)))
}

// oidcSessionOnly — API token principal'ını reddeder (rol kapısının İÇİNDE).
func oidcSessionOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := auth.FromContext(r.Context())
		if c == nil || strings.HasPrefix(strings.TrimSpace(c.UserID), "token:") {
			writeJSONError(w, http.StatusForbidden, "SSO ayarı yalnız oturum açmış yönetici tarafından değiştirilebilir (API token kabul edilmez)")
			return
		}
		h(w, r)
	}
}

// oidcSettingsBodyMax — gövde tavanı. Form birkaç yüz bayt + v0.10.1112 özel
// CA alanı (≤64 KB, JSON kaçışıyla biraz büyür) — tavan CA tavanının iki katı.
const oidcSettingsBodyMax = 2 * auth.OIDCCACertPEMMax

// oidcSettingsInput — PUT/test gövdesi. auth.OIDCSettings.ClientSecret
// `json:"-"` olduğu için secret buradaki DIŞ alandan çözülür.
type oidcSettingsInput struct {
	auth.OIDCSettings
	ClientSecret string `json:"clientSecret"`
	// v0.10.1110 — yetki servisi anahtarı da `json:"-"`; boş = kayıtlıyı koru.
	PermissionServiceKey string `json:"permissionServiceKey"`
}

func (s *Server) getOIDCSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.oidc.Snapshot())
}

func decodeOIDCSettings(w http.ResponseWriter, r *http.Request) (auth.OIDCSettings, bool) {
	var in oidcSettingsInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, oidcSettingsBodyMax)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON gövdesi")
		return auth.OIDCSettings{}, false
	}
	out := in.OIDCSettings
	out.ClientSecret = in.ClientSecret
	out.PermissionServiceKey = in.PermissionServiceKey
	return out, true
}

func (s *Server) putOIDCSettings(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "OIDC servisi yok")
		return
	}
	in, ok := decodeOIDCSettings(w, r)
	if !ok {
		return
	}
	secretChanged := strings.TrimSpace(in.ClientSecret) != ""
	permKeyChanged := strings.TrimSpace(in.PermissionServiceKey) != ""
	// v0.10.1112 — TLS alanlarının eski→yeni audit'i; prev kayıtla AYNI kilit
	// altında alınır (güvenlik incelemesi N5).
	prev, snap, err := s.oidc.SaveSettingsWithPrev(r.Context(), in)
	if err != nil {
		var verr *auth.OIDCSettingsError
		var derr *auth.OIDCDiscoveryError
		switch {
		case errors.As(err, &verr), errors.As(err, &derr):
			writeJSONError(w, http.StatusBadRequest, err.Error())
		default:
			writeErr(w, err)
		}
		return
	}
	s.publishConfigReload(r.Context(), "oidc")
	// Audit satırı secret TAŞIMAZ — yalnız "değişti mi" bayrağı.
	details, _ := json.Marshal(map[string]any{
		"enabled": snap.Enabled, "issuerUrl": snap.IssuerURL, "clientId": snap.ClientID,
		"redirectUrl": snap.RedirectURL, "scopes": snap.Scopes, "displayName": snap.DisplayName,
		"defaultRole": snap.DefaultRole, "allowedDomains": snap.AllowedDomains,
		"clientSecretChanged": secretChanged,
		// v0.10.1110 — yetki servisi; anahtar YOK, yalnız "değişti mi".
		"permissionServiceEnabled": snap.PermissionServiceEnabled, "permissionTTLSeconds": snap.PermissionTTLSeconds,
		"permissionsClaim": snap.PermissionsClaim, "roleFromClaim": snap.RoleFromClaim,
		"permissionServiceKeyChanged": permKeyChanged,
		// v0.10.1111 — anahtarsız kip + ağ sınırı (secret değil).
		"permissionServiceAllowNoKey":     snap.PermissionServiceAllowNoKey,
		"permissionServiceAllowedCIDRs":   snap.PermissionServiceAllowedCIDRs,
		"permissionServiceTrustedProxies": snap.PermissionServiceTrustedProxies,
		// v0.10.1112 — IdP TLS güveni, eski→yeni. CA'nın kendisi değil özeti.
		"tlsCACert": map[string]any{
			"old": auth.OIDCCACertSummary(prev.TLSCACertPEM), "new": auth.OIDCCACertSummary(snap.TLSCACertPEM),
			"changed": prev.TLSCACertPEM != snap.TLSCACertPEM,
		},
		"tlsInsecureSkipVerify": map[string]any{"old": prev.TLSInsecureSkipVerify, "new": snap.TLSInsecureSkipVerify},
		// v0.10.1120 — doğrulanmamış e-postaya güven, eski→yeni.
		"trustUnverifiedEmail": map[string]any{"old": prev.TrustUnverifiedEmail, "new": snap.TrustUnverifiedEmail},
		// v0.10.1121 — e-posta yoksa kullanıcı adıyla eşleştirme, eski→yeni.
		"usernameFallback":           map[string]any{"old": prev.UsernameFallback, "new": snap.UsernameFallback},
		"usernameClaim":              map[string]any{"old": prev.UsernameClaim, "new": snap.UsernameClaim},
		"usernameFallbackAllowAdmin": map[string]any{"old": prev.UsernameFallbackAllowAdmin, "new": snap.UsernameFallbackAllowAdmin},
	})
	s.audit(r, "settings.oidc.update", "settings", "oidc", string(details))
	writeJSON(w, snap)
}

func (s *Server) testOIDCSettings(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeOIDCSettings(w, r)
	if !ok {
		return
	}
	d, err := s.oidc.TestDiscovery(r.Context(), in)
	// Yoklama izi: issuer + sonuç; gövde, hata ayrıntısı ve secret YOK.
	issuer := strings.TrimSpace(in.IssuerURL)
	if len(issuer) > 512 {
		issuer = issuer[:512]
	}
	// v0.10.1112 (güvenlik incelemesi N3) — hangi TLS güveniyle yoklandı:
	// skip-verify bayrağı + özel CA var mı (PEM değil).
	details, _ := json.Marshal(map[string]any{"issuerUrl": issuer, "ok": err == nil,
		"tlsInsecureSkipVerify": in.TLSInsecureSkipVerify, "customCA": strings.TrimSpace(in.TLSCACertPEM) != ""})
	s.audit(r, "settings.oidc.test", "settings", "oidc", string(details))
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "discovery": d})
}

// oidcLoginFailedMsg — giriş sayfasında görünen TEK metin.
const oidcLoginFailedMsg = "SSO girişi tamamlanamadı — tekrar deneyin ya da yöneticinize başvurun"

// oidcLoginFail — bkz. dosya başlığı. Log satırı yalnız sabit sınıf (≤200 bayt).
func (s *Server) oidcLoginFail(w http.ResponseWriter, r *http.Request, err error) {
	class := auth.OIDCLoginErrorClass(err)
	if len(class) > 200 {
		class = class[:200]
	}
	log.Printf("[oidc] callback failed: class=%s", class)
	s.oidcFail(w, r, oidcLoginFailedMsg)
}
