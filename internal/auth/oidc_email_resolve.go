package auth

// oidc_email_resolve.go — v0.10.1121 (operatör, prod: "[oidc] callback
// failed: class=email_missing" — HERKES için). Kurumsal IdP (LDAP
// federasyonlu Keycloak) id_token'a `email` koymuyor; `preferred_username`
// AD sAMAccountName'i (sicil, ör. "n0000001") taşıyor. Aynı kullanıcılar
// Coremetry LDAP girişiyle sorunsuz giriyor (dizin kaydında mail dolu).
// TERCİH EDİLEN düzeltme IdP'de: LDAP federasyonunda `mail` → `email`
// eşleyicisi + `email` client scope'u Default ve "Add to ID token" açık
// (docs/SSO-PERMISSION-SERVICE.md). Bu dosya Coremetry tarafındaki ÇÖZÜM
// ZİNCİRİ — id_token'da e-posta YOKSA, ilk isabette durur:
//
//  1. id_token `email` (Exchange; davranış aynen — bu dosyaya gelinmez).
//  2. UserInfo `email` — HER ZAMAN denenir (keşifte userinfo ucu varsa):
//     aynı sınırlı HTTP istemcisi (ctx'teki oidc.ClientContext, TLS ayarı
//     dahil), ≤5 s. UserInfo `sub` ≠ id_token `sub` ⇒ RED
//     (userinfo_sub_mismatch; OIDC Core §5.3.2). email_verified UserInfo'dan;
//     UserInfo göndermiyorsa id_token'daki değer taşınır (güvenlik incelemesi
//     F5 — id_token `false` diyorsa UserInfo e-postası "claim yok" diye
//     doğrulanmış gibi geçmesin). Kapı id_token'la aynı (decideOIDCEmail:
//     güven anahtarı + alan adı). Uç hatası (ağ, 401…) yumuşak: sonraki adım.
//  3. usernameFallback AÇIKSA (varsayılan KAPALI): yapılandırılan claim
//     (usernameClaim, varsayılan preferred_username; YALNIZ o claim okunur,
//     başka ad tahmin edilmez) önce imzası/issuer'ı/nonce'u DOĞRULANMIŞ
//     id_token'dan, yoksa sub'ı eşleşmiş UserInfo'dan.
//     a) LDAP yapılandırılmışsa servis hesabıyla dizinde TEK öznitelikte TAM
//     eşleşme (ldap/oidc_lookup.go: sAMAccountName ya da ayarlı benzersiz
//     UserAttribute, AD'de devre dışı hesap hariç, kaçışlı filtre, sizeLimit
//     2, ≤5 s). Birden çok kayıt ⇒ RED (username_ambiguous); dizin hatası ⇒
//     RED (directory_lookup_failed) + kategorili, seyreltilmiş log satırı (F6).
//     Kayıtta e-posta varsa DOĞRULANMIŞ sayılır (dizin yetkili kaynak) —
//     ViaTrust DEĞİL; izinli alan adı listesi uygulanır. Kayıt YOKSA ⇒
//     email_missing (F3: dizin açıkken users tablosundaki bayat bir
//     ldap_username satırına DÜŞÜLMEZ — dizinden silinmiş hesap girmesin).
//     b) Kayıt var ama e-postası yok, ya da LDAP yapılandırılmamış:
//     EmailSource=ldap_username, Email boş — callback MEVCUT, devre dışı
//     olmayan kullanıcıyı users.ldap_username ile (büyük-küçük harf duyarsız,
//     LIMIT 2 — iki satır ⇒ username_ambiguous, F4) bulur ve onunla açar;
//     kullanıcının kayıtlı e-postasına izinli alan adı listesi uygulanır;
//     yoksa email_missing. Yeni kullanıcı bu yoldan AÇILMAZ (e-postası yok).
//     a ve b'de OIDCClaims.ViaUsername: admin hesabı YALNIZ
//     usernameFallbackAllowAdmin açıkken açılır (F2, api auth_permissions.go).
//
// Hiçbiri isabet etmezse sınıf email_missing (DEĞİŞMEDİ) + tek log satırı:
// denenen adımlar ve id_token/UserInfo'daki claim ADLARI (değer asla).
//
// ÖN KOŞUL (3. adım): kullanıcı adı claim'ini kullanıcı IdP'de DEĞİŞTİREMEMELİ
// (Keycloak: LDAP federasyonu read-only / "Edit username" kapalı). Kullanıcının
// kendisinin düzenleyebildiği profil claim'leri (name, nickname, email …)
// seçilemez (validateUsernameClaim). Kullanıcı adı yalnız yazdırılabilir ASCII
// (Unicode katlamasıyla — ör. KELVIN SIGN → "k" — başka bir hesaba
// çarpışmasın) ve `@` içeremez (UPN/e-posta biçimi kullanıcı adı sayılmaz, F1).
// Brokered (dış) IdP, self-registration ya da düzenlenebilir kullanıcı adı
// olan kurulumda bu adım GÜVENSİZDİR.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// E-posta kaynağı (OIDCClaims.EmailSource).
const (
	OIDCEmailSourceIDToken      = "id_token"
	OIDCEmailSourceUserInfo     = "userinfo"
	OIDCEmailSourceLDAP         = "ldap"
	OIDCEmailSourceLdapUsername = "ldap_username"
)

const (
	// UsernameClaimDefault — usernameClaim boşsa.
	UsernameClaimDefault = "preferred_username"
	oidcUserInfoTimeout  = 5 * time.Second
	oidcDirLookupTimeout = 6 * time.Second // dizin kendi ≤5 s sınırını uygular; bu üst güvence
	oidcUsernameMax      = 256
	oidcLogClaimNamesMax = 50
)

// OIDCDirectory — kullanıcı adı → e-posta (main: *ldap.Service). Lookup
// found=false = kayıt yok; found=true + "" = kayıt var, e-postası yok.
// Hatalar `Category() string` (bind / timeout / search / ambiguous) ve
// birden çok kayıtta `Ambiguous() bool` arayüzünü karşılar.
type OIDCDirectory interface {
	Enabled() bool
	LookupEmailByUsername(ctx context.Context, username string) (mail string, found bool, err error)
}

type oidcDirBox struct{ d OIDCDirectory }

// SetDirectory — boot'ta bir kez (main); nil = dizin yok.
func (o *OIDCService) SetDirectory(d OIDCDirectory) {
	if o == nil {
		return
	}
	if d == nil {
		o.dir.Store(nil)
		return
	}
	o.dir.Store(&oidcDirBox{d: d})
}

func (o *OIDCService) directory() OIDCDirectory {
	if b := o.dir.Load(); b != nil {
		return b.d
	}
	return nil
}

// applyEmailOptions — ayardaki e-posta seçenekleri canlı istemciye.
func (c *oidcClient) applyEmailOptions(s OIDCSettings) {
	c.trustUnverifiedEmail = s.TrustUnverifiedEmail
	c.usernameFallback = s.UsernameFallback
	c.usernameClaim = s.UsernameClaim
	c.usernameAllowAdmin = s.UsernameFallback && s.UsernameFallbackAllowAdmin
}

// UsernameFallbackAllowAdmin — F2: kullanıcı adıyla eşleşen giriş admin
// hesabını açabilir mi (canlı istemciden; SSO kapalıyken false).
func (o *OIDCService) UsernameFallbackAllowAdmin() bool {
	if c := o.client(); c != nil {
		return c.usernameAllowAdmin
	}
	return false
}

// dirLogCategories — F6: loga giden dizin hata kategorileri (sabit küme).
var dirLogCategories = map[string]bool{"bind": true, "timeout": true, "search": true, "ambiguous": true}

// dirErrCategory — hatanın sabit kategorisi; bilinmeyen = "other". SAF.
func dirErrCategory(err error) string {
	var c interface{ Category() string }
	if errors.As(err, &c) && dirLogCategories[c.Category()] {
		return c.Category()
	}
	if dirAmbiguous(err) {
		return "ambiguous"
	}
	return "other"
}

// dirFailLogEvery — aynı kategori için log satırları arası en az süre.
const dirFailLogEvery = time.Minute

// dirFailLog — kategori → son satır zamanı (≤5 anahtar).
var dirFailLog sync.Map

// logDirectoryFailure — F6: kategori başına dakikada en çok bir satır;
// kullanıcı adı / hata metni (DN, sunucu cevabı) TAŞIMAZ.
func logDirectoryFailure(err error, now time.Time, logf func(string, ...any)) {
	cat := dirErrCategory(err)
	if prev, ok := dirFailLog.Load(cat); ok {
		if t, _ := prev.(time.Time); now.Sub(t) < dirFailLogEvery {
			return
		}
	}
	dirFailLog.Store(cat, now)
	logf("[oidc] directory lookup failed: category=%s", cat)
}

// forbiddenUsernameClaims — kullanıcı adı claim'i olarak SEÇİLEMEZ: kullanıcının
// IdP profilinden düzenleyebildiği alanlar, e-posta (zincirin kendisi) ve
// protokol claim'leri.
var forbiddenUsernameClaims = map[string]bool{
	"email": true, "email_verified": true, "name": true, "given_name": true, "family_name": true,
	"middle_name": true, "nickname": true, "profile": true, "picture": true, "website": true,
	"gender": true, "birthdate": true, "zoneinfo": true, "locale": true, "phone_number": true,
	"phone_number_verified": true, "address": true, "updated_at": true,
	"iss": true, "aud": true, "exp": true, "iat": true, "nbf": true, "nonce": true, "at_hash": true,
	"c_hash": true, "azp": true, "auth_time": true, "acr": true, "amr": true, "sid": true, "jti": true,
	"typ": true, "scope": true,
}

// validateUsernameClaim — 1-64, harf/rakam ve `_ - . :`; yasaklı ad değil.
// "" = normalize edilmemiş girdi → varsayılan (NormalizeOIDCSettings). SAF.
func validateUsernameClaim(c string) error {
	if c == "" {
		return nil
	}
	if !validPermissionsClaim(c) {
		return &OIDCSettingsError{"usernameClaim", "Kullanıcı adı claim'i 1-64 karakter; harf, rakam ve _ - . : olabilir"}
	}
	if forbiddenUsernameClaims[strings.ToLower(c)] {
		return &OIDCSettingsError{"usernameClaim", "Kullanıcı adı claim'i e-posta, profil ya da protokol claim'i olamaz (ör. preferred_username)"}
	}
	return nil
}

// stringClaim — claim JSON dizgisiyse kırpılmış değeri. SAF.
func stringClaim(m map[string]json.RawMessage, name string) string {
	raw, ok := m[name]
	if !ok {
		return ""
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// validOIDCUsername — yazdırılabilir ASCII (iç boşluk serbest), ≤256, `@`
// yok (F1). SAF.
func validOIDCUsername(v string) bool {
	if v == "" || len(v) > oidcUsernameMax || strings.Contains(v, "@") {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return false
		}
	}
	return true
}

// usernameFromClaims — yapılandırılan claim; geçersiz değer = yok. SAF.
func usernameFromClaims(m map[string]json.RawMessage, claim string) (string, bool) {
	v := stringClaim(m, claim)
	if !validOIDCUsername(v) {
		return "", false
	}
	return v, true
}

// logSafeClaimNames — log için claim ADLARI: sıralı, tekil, ≤50; güvenli
// karakter kümesi dışındaki ad "?" (log enjeksiyonu yok). DEĞER YOK. SAF.
func logSafeClaimNames(maps ...map[string]json.RawMessage) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range maps {
		for k := range m {
			if !validPermissionsClaim(k) {
				k = "?"
			}
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	if len(out) > oidcLogClaimNamesMax {
		out = out[:oidcLogClaimNamesMax]
	}
	return out
}

// LogEmailResolutionFailed — zincir isabetsiz bitti: sınıf + denenen adımlar +
// claim ADLARI (değer yok). api katmanı da (ldap_username isabetsiz) kullanır.
func LogEmailResolutionFailed(logf func(string, ...any), class string, tried, claimNames []string) {
	logf("[oidc] email resolution failed: class=%s tried=%s claims=%s",
		class, strings.Join(tried, ","), strings.Join(claimNames, ","))
}

// dirAmbiguous — dizin hatası "birden çok kayıt" mı.
func dirAmbiguous(err error) bool {
	var a interface{ Ambiguous() bool }
	return errors.As(err, &a) && a.Ambiguous()
}

// resolveMissingEmail — id_token'da e-posta yok: zincirin 2. ve 3. adımı
// (dosya başlığı). ctx Exchange'in sınırlı istemcili bağlamı (ClientContext).
func (o *OIDCService) resolveMissingEmail(ctx context.Context, c *oidcClient, tok *oauth2.Token, idTok *oidc.IDToken) (*OIDCClaims, error) {
	idClaims := map[string]json.RawMessage{}
	if err := idTok.Claims(&idClaims); err != nil {
		return nil, loginErr("claims_decode", err)
	}
	tried := []string{OIDCEmailSourceIDToken}
	var uiClaims map[string]json.RawMessage
	fail := func(class string, err error) (*OIDCClaims, error) {
		LogEmailResolutionFailed(log.Printf, class, tried, logSafeClaimNames(idClaims, uiClaims))
		return nil, loginErr(class, err)
	}
	base := func(src string) *OIDCClaims {
		return &OIDCClaims{EmailSource: src, Subject: idTok.Subject, Nonce: idTok.Nonce}
	}

	// 2) UserInfo.
	if c.provider != nil && c.provider.UserInfoEndpoint() != "" && tok != nil && tok.AccessToken != "" {
		uctx, cancel := context.WithTimeout(ctx, oidcUserInfoTimeout)
		ui, err := c.provider.UserInfo(uctx, oauth2.StaticTokenSource(tok))
		cancel()
		if err != nil {
			// Hata metni IdP gövdesini taşıyabilir — loga/kullanıcıya gitmez.
			tried = append(tried, OIDCEmailSourceUserInfo+"(error)")
		} else {
			tried = append(tried, OIDCEmailSourceUserInfo)
			if ui.Subject == "" || ui.Subject != idTok.Subject {
				// OIDC Core §5.3.2: eşleşmeyen sub'lı UserInfo cevabı KULLANILMAZ.
				return fail("userinfo_sub_mismatch", nil)
			}
			uiClaims = map[string]json.RawMessage{}
			if err := ui.Claims(&uiClaims); err != nil {
				return fail("claims_decode", err)
			}
			if email := stringClaim(uiClaims, "email"); email != "" {
				// F5 — UserInfo email_verified göndermiyorsa id_token'ınki taşınır.
				ev := uiClaims["email_verified"]
				if _, present := parseEmailVerified(ev); !present {
					ev = idClaims["email_verified"]
				}
				dec := decideOIDCEmail(ev, email, c.cfg.AllowedDomains, c.trustUnverifiedEmail)
				if dec.Class != "" {
					return fail(dec.Class, nil)
				}
				if dec.ViaTrust {
					logTrustedUnverifiedLogin(email, time.Now(), log.Printf)
				}
				cl := base(OIDCEmailSourceUserInfo)
				cl.Email, cl.EmailVerified, cl.ViaTrust = email, dec.Verified, dec.ViaTrust
				return cl, nil
			}
		}
	}

	// 3) Kullanıcı adı — yalnız açık seçimle.
	if !c.usernameFallback {
		return fail("email_missing", nil)
	}
	claim := c.usernameClaim
	if claim == "" {
		claim = UsernameClaimDefault
	}
	username, ok := usernameFromClaims(idClaims, claim)
	if !ok && uiClaims != nil { // uiClaims yalnız sub eşleştiyse dolu
		username, ok = usernameFromClaims(uiClaims, claim)
	}
	if !ok {
		tried = append(tried, "username(missing)")
		return fail("email_missing", nil)
	}
	if dir := o.directory(); dir != nil && dir.Enabled() {
		tried = append(tried, OIDCEmailSourceLDAP)
		dctx, cancel := context.WithTimeout(ctx, oidcDirLookupTimeout)
		mail, found, err := dir.LookupEmailByUsername(dctx, username)
		cancel()
		if err != nil {
			logDirectoryFailure(err, time.Now(), log.Printf)
			if dirAmbiguous(err) {
				return fail("username_ambiguous", err)
			}
			return fail("directory_lookup_failed", err)
		}
		if !found {
			// F3 — dizin açık ve kayıt yok: bayat ldap_username satırına düşülmez.
			tried = append(tried, "ldap(not_found)")
			return fail("email_missing", nil)
		}
		if mail != "" {
			if !emailDomainAllowed(c.cfg.AllowedDomains, mail) {
				return fail("email_domain_denied", nil)
			}
			cl := base(OIDCEmailSourceLDAP)
			cl.Email, cl.EmailVerified, cl.Username = strings.ToLower(mail), true, strings.ToLower(username)
			cl.ViaUsername = true
			return cl, nil
		}
	}
	tried = append(tried, OIDCEmailSourceLdapUsername)
	cl := base(OIDCEmailSourceLdapUsername)
	cl.Username = strings.ToLower(username)
	cl.ViaUsername = true
	cl.ResolutionTried = tried
	cl.ClaimNames = logSafeClaimNames(idClaims, uiClaims)
	return cl, nil
}
