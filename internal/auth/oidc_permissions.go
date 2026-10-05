package auth

// oidc_permissions.go — v0.10.1110 (merkezi login yetki servisi + token
// claim'inden rol).
//
// Müşterinin merkezi OIDC girişi, kullanıcı oturumu başına BİR kez (token
// yenilemede değil) Coremetry'nin POST /api/auth/permissions ucunu çağırır
// ve dönen `permissions` dizisini access token'a claim olarak gömer. Bu
// dosya o sözleşmenin auth tarafı:
//
//   - PermissionService / PermissionKeyMatches: uç (internal/api/
//     auth_permissions.go) anahtarı HİÇ görmez; karşılaştırma burada,
//     iki tarafın SHA-256 özeti üzerinden crypto/subtle ile (uzunluk da
//     sızmaz).
//   - PermissionsForRole: Coremetry rolü → claim değeri. Roller Coremetry'de
//     yönetilmeye devam eder; servis yalnız RAPORLAR.
//   - claimRole (Exchange'ten): roleFromClaim VARSAYILAN KAPALI — kapalıyken
//     claim hiç okunmaz, giriş rolü bugünkü gibi: ilk girişte defaultRole
//     (viewer), sonra Coremetry Users sayfasında atanan rol (operatör kararı
//     2026-10-05: "oidc ile kullanıcılar login olduğunda yine default viewer
//     olsun"). Açıkken claim ACCESS
//     token'dan okunur (id_token ile AYNI JWKS'le imza doğrulanır); access
//     token opak ya da doğrulanamıyorsa id_token'daki aynı adlı claim'e
//     düşülür. Claim yok / boş / tanınmayan değer → rol değişmez (müşteri,
//     servis cevap vermediğinde token'ı claim'siz basıyor; Coremetry'de
//     atanmış rol claim'siz girişte KORUNUR).
//   - Rolü depoya yazmak burada DEĞİL: Exchange yalnız OIDCClaims.ClaimRole'ü
//     doldurur; yazım api katmanında (auth_permissions.go oidcLoginUser,
//     callback'ten) — yalnız DÜŞÜRÜR, ilk kez gelen kullanıcıyı açmaz,
//     disabled kullanıcıyı reddeder, audit'e istek IP'si girer.
//   - Claim adı standart kimlik/profil claim'i olamaz (reservedClaimNames).

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log"
	"strings"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Claim değerleri — müşteri sözleşmesi.
const (
	PermissionAdmin      = "COREMETRY_ADMIN"
	PermissionEditor     = "COREMETRY_EDITOR"
	PermissionViewer     = "COREMETRY_VIEWER"
	PermissionRolePrefix = "COREMETRY_ROLE_"
)

// PermissionServiceConfig — ucun ihtiyacı; anahtarın KENDİSİ yok.
type PermissionServiceConfig struct {
	Enabled    bool
	KeySet     bool
	TTLSeconds int
	// DefaultRole — SSO varsayılan rolü (geçersizse viewer): Coremetry'de
	// karşılığı olmayan kullanıcıya bildirilen rol (operatör kararı
	// 2026-10-05: "herkesin viewer rolünde login olabilmesi gerekir").
	DefaultRole string
}

// Usable — servis açık VE anahtar kayıtlı (değilse uç 404).
func (c PermissionServiceConfig) Usable() bool { return c.Enabled && c.KeySet }

func (o *OIDCService) effective() OIDCSettings {
	if o == nil {
		return NormalizeOIDCSettings(OIDCSettings{}, "")
	}
	return NormalizeOIDCSettings(o.state().eff, o.publicURL)
}

// PermissionService — etkin (son-iyi) ayarın yetki servisi kısmı. SSO'nun
// canlı istemcisinden BAĞIMSIZ: IdP keşfi düşse de servis cevap verir.
func (o *OIDCService) PermissionService() PermissionServiceConfig {
	s := o.effective()
	return PermissionServiceConfig{
		Enabled:    s.PermissionServiceEnabled,
		KeySet:     s.PermissionServiceKey != "",
		TTLSeconds: s.PermissionTTLSeconds,
		DefaultRole: func() string {
			if IsValidRole(s.DefaultRole) {
				return s.DefaultRole
			}
			return RoleViewer
		}(),
	}
}

// PermissionKeyMatches — sabit-zamanlı karşılaştırma. Kayıtlı anahtar yoksa
// ya da gelen boşsa false. Özet üzerinden: ConstantTimeCompare uzunluk
// farkında erken döner, sabit 32 baytlık özet bunu kapatır.
func (o *OIDCService) PermissionKeyMatches(got string) bool {
	want := o.effective().PermissionServiceKey
	return permissionKeyEqual(want, got)
}

func permissionKeyEqual(want, got string) bool {
	if want == "" || got == "" {
		return false
	}
	a := sha256.Sum256([]byte(want))
	b := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// PermissionsForRole — Coremetry kullanıcısının rolü → claim değerleri.
// Özel rol (yalnız viewer tabanında anlamlı, custom_roles.go) katalogda
// VARSA COREMETRY_ROLE_<AD>, yoksa taban rol. Tanınmayan taban rol → nil
// (uç 204 döner). SAF.
func PermissionsForRole(role, customRole string, customExists bool) []string {
	switch role {
	case RoleAdmin:
		return []string{PermissionAdmin}
	case RoleEditor:
		return []string{PermissionEditor}
	case RoleViewer:
		if customRole != "" && customExists {
			if n := permissionRoleName(customRole); n != "" {
				return []string{PermissionRolePrefix + n}
			}
		}
		return []string{PermissionViewer}
	}
	return nil
}

// permissionRoleName — BÜYÜK harf; ASCII harf/rakam dışı her rün "_"
// (claim değeri tek kelime kalsın; Türkçe harf Unicode büyütmeden sonra
// ASCII değilse "_"). Baştaki/sondaki "_" kırpılır. SAF.
func permissionRoleName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(name)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if unicode.IsPrint(r) || r == ' ' {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// RoleFromPermissions — claim değerleri → Coremetry rolü; sıra ADMIN >
// EDITOR > VIEWER (ilk eşleşen). Eşleşme yoksa "". SAF.
func RoleFromPermissions(vals []string) string {
	has := map[string]bool{}
	for _, v := range vals {
		has[strings.ToUpper(strings.TrimSpace(v))] = true
	}
	switch {
	case has[PermissionAdmin]:
		return RoleAdmin
	case has[PermissionEditor]:
		return RoleEditor
	case has[PermissionViewer]:
		return RoleViewer
	}
	return ""
}

// parsePermissionsClaim — claim değeri YALNIZ dizgi dizisi (sözleşme).
// Güvenlik incelemesi F2: tek dizgi (boşluk/virgül ayraçlı) KABUL EDİLMEZ —
// kullanıcının etkileyebildiği serbest metin bir claim'e (ör. ad, unvan)
// yanlışlıkla bağlanırsa "Ali COREMETRY_ADMIN" gibi bir değer rol
// taşımasın. Dizi dışı her biçim "claim yok". present = en az bir değer. SAF.
func parsePermissionsClaim(raw json.RawMessage) (vals []string, present bool) {
	t := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(t, "[") {
		return nil, false
	}
	var arr []any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	for _, v := range arr {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			vals = append(vals, strings.TrimSpace(s))
		}
	}
	return vals, len(vals) > 0
}

// looksLikeJWT — üç parça ve başlık "alg" taşıyan JSON. Opak token false. SAF.
func looksLikeJWT(tok string) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return false
	}
	hb, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[0], "="))
	if err != nil {
		return false
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	return json.Unmarshal(hb, &hdr) == nil && hdr.Alg != ""
}

// claimSource — doğrulanmış token (go-oidc *IDToken); claimFrom tek claim'i okur.
type claimSource interface{ Claims(v any) error }

func claimFrom(tok claimSource, name string) (vals []string, present bool) {
	if tok == nil {
		return nil, false
	}
	var all map[string]json.RawMessage
	if err := tok.Claims(&all); err != nil {
		return nil, false
	}
	return parsePermissionsClaim(all[name])
}

// claimRole — roleFromClaim açıkken rol + kaynak ("access_token" |
// "id_token"). ctx Exchange'in sınırlı-istemcili bağlamı (JWKS aynı
// istemciyle). Kapalıysa ("", "").
func (o *OIDCService) claimRole(ctx context.Context, c *oidcClient, accessToken string, idTok *oidc.IDToken) (role, source string) {
	s := o.effective()
	if !s.RoleFromClaim {
		return "", ""
	}
	if looksLikeJWT(accessToken) && c.atVerifier != nil {
		at, err := c.atVerifier.Verify(ctx, accessToken)
		if err == nil {
			vals, _ := claimFrom(at, s.PermissionsClaim)
			return RoleFromPermissions(vals), "access_token"
		}
		// Gövde/ayrıntı YOK (oidc.go OIDCLoginError gerekçesi) — yalnız sınıf.
		log.Printf("[oidc] access token signature not verifiable — falling back to id_token claim")
	}
	vals, _ := claimFrom(idTok, s.PermissionsClaim)
	return RoleFromPermissions(vals), "id_token"
}
