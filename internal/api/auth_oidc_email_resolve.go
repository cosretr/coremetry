package api

// auth_oidc_email_resolve.go — v0.10.1121 (operatör, prod: "[oidc] callback
// failed: class=email_missing"). OIDC e-posta çözüm zincirinin callback
// tarafı; zincirin kendisi auth/oidc_email_resolve.go'da.
//
// api.go BÜYÜMEYECEK kuralı (TestApiGoDoesNotGrow): callback'in tek satırı
// (auto-provision logu) buradaki yardımcıya döndü; satır sayısı aynı.
//
//   - EmailSource=ldap_username (dizin yok / dizin kaydında e-posta yok,
//     usernameFallback açık): kullanıcı ADANMIŞ okumayla
//     (GetActiveUsersByLdapUsername: devre dışı satırlar yok sayılır, LIMIT 2,
//     max_execution_time) users.ldap_username ile (küçük harf; depo lowerUTF8
//     karşılaştırır) bulunur; iki satır ⇒ username_ambiguous (keyfî ilk satır
//     seçilmez — güvenlik incelemesi F4). Kayıtlı e-postaya izinli alan adı
//     listesi uygulanır. Bulunamazsa email_missing (sınıf DEĞİŞMEDİ) ve YENİ
//     kullanıcı açılmaz (e-postası yok).
//   - Kullanıcı adıyla kurulan kimlik (claims.ViaUsername — ldap ve
//     ldap_username kaynakları) güven anahtarı (ViaTrust) DEĞİLDİR: yerel /
//     LDAP / oidc viewer-editor hesabı açılır; ADMIN hesabı yalnız
//     usernameFallbackAllowAdmin açıkken (F2) — yoksa username_admin_refused.
//   - Başarıda tek satır: `[oidc] email resolved via <kaynak> user id=<id>`
//     (e-posta yok). id_token kaynağında satır yok (eski davranış).

import (
	"context"
	"log"
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// oidcLdapUserLookup — OIDC'ye adanmış users.ldap_username okuması
// (*chstore.Store karşılar; ≤2 aktif satır).
type oidcLdapUserLookup interface {
	GetActiveUsersByLdapUsername(ctx context.Context, username string) ([]chstore.User, error)
}

// oidcLdapLookupFor — YALNIZ test dikişi; üretimde s.store.
var oidcLdapLookupFor = func(s *Server) oidcLdapUserLookup { return s.store }

// errOIDCEmailMissing — kullanıcı adıyla da eşleşme yok. Sayfa metni genel.
var errOIDCEmailMissing error = &oidcLoginPageError{Class: "email_missing", Msg: oidcLoginFailedMsg}

// errOIDCEmailDomainDenied — eşleşen kullanıcının e-postası izinli alan adı
// listesinde değil.
var errOIDCEmailDomainDenied error = &oidcLoginPageError{Class: "email_domain_denied", Msg: oidcLoginFailedMsg}

// errOIDCUsernameAmbiguous — kullanıcı adı birden çok aktif kullanıcıya eşleşti.
var errOIDCUsernameAmbiguous error = &oidcLoginPageError{Class: "username_ambiguous", Msg: oidcLoginFailedMsg}

// oidcClassUsernameAdminRefused — F2: kullanıcı adıyla eşleşen giriş admin
// hesabını açmadı (usernameFallbackAllowAdmin kapalı).
const oidcClassUsernameAdminRefused = "username_admin_refused"

var errOIDCUsernameAdminRefused error = &oidcLoginPageError{
	Class: oidcClassUsernameAdminRefused,
	Msg: "Yönetici hesabı SSO'da kullanıcı adı eşleştirmesiyle açılamaz; parola/LDAP ile girin veya IdP e-posta göndersin " +
		"(an admin account cannot be opened via SSO username matching; sign in with password/LDAP or have the IdP send the email)",
}

// oidcUsernameLinkAllowed — kullanıcı adıyla kurulan kimlik bu hesaba
// bağlanabilir mi: admin değilse her sağlayıcı (local / ldap / oidc); admin
// yalnız açık-seçimle. SAF.
func oidcUsernameLinkAllowed(u chstore.User, allowAdmin bool) bool {
	return u.Role != auth.RoleAdmin || allowAdmin
}

// oidcUserByLdapUsername — EmailSource=ldap_username yolu. (nil, nil) DÖNMEZ:
// e-postasız yeni kullanıcı açılamaz.
func (s *Server) oidcUserByLdapUsername(r *http.Request, claims *auth.OIDCClaims) (*chstore.User, error) {
	var u *chstore.User
	if claims.Username != "" {
		us, err := oidcLdapLookupFor(s).GetActiveUsersByLdapUsername(r.Context(), claims.Username)
		if err != nil {
			return nil, err
		}
		if len(us) > 1 {
			log.Printf("[oidc] callback failed: class=username_ambiguous rows=%d", len(us))
			return nil, errOIDCUsernameAmbiguous
		}
		if len(us) == 1 {
			u = &us[0]
		}
	}
	if u == nil {
		auth.LogEmailResolutionFailed(log.Printf, "email_missing", claims.ResolutionTried, claims.ClaimNames)
		log.Printf("[oidc] callback failed: class=email_missing")
		return nil, errOIDCEmailMissing
	}
	if !s.oidc.AllowEmail(u.Email) {
		log.Printf("[oidc] callback failed: class=email_domain_denied user id=%s", u.ID)
		return nil, errOIDCEmailDomainDenied
	}
	return u, nil
}

// oidcProvisionRole — callback'in YENİ kullanıcı rolü (api.go'daki satır).
// Güvenlik incelemesi F2'nin tamamlayıcısı: kullanıcı adıyla kurulan kimlik
// (ViaUsername) usernameFallbackAllowAdmin kapalıyken DefaultRole=admin olsa
// da admin AÇILMAZ — rol editor'a indirilir. Diğer durumlarda DefaultRole
// aynen (geçerlilik denetimi çağıranda). SAF.
func oidcProvisionRole(defaultRole string, claims *auth.OIDCClaims, allowAdmin bool) string {
	if claims != nil && claims.ViaUsername && !allowAdmin && defaultRole == auth.RoleAdmin {
		return auth.RoleEditor
	}
	return defaultRole
}

// logOIDCEmailResolved —zincirin id_token dışı kaynağıyla başarı (e-posta yok).
func logOIDCEmailResolved(claims *auth.OIDCClaims, userID string) {
	if claims == nil || claims.EmailSource == "" || claims.EmailSource == auth.OIDCEmailSourceIDToken {
		return
	}
	log.Printf("[oidc] email resolved via %s user id=%s", claims.EmailSource, userID)
}

// logOIDCProvisioned — callback'in auto-provision logu (api.go'dan taşındı) +
// çözüm satırı.
func logOIDCProvisioned(u *chstore.User, claims *auth.OIDCClaims) {
	log.Printf("[oidc] auto-provisioned user %q (role=%s)", u.Email, u.Role)
	logOIDCEmailResolved(claims, u.ID)
}
