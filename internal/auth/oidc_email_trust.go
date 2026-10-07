package auth

// oidc_email_trust.go — v0.10.1120 (operatör, prod: "[oidc] callback failed:
// class=email_unverified"). Kurumsal IdP (AD/LDAP federasyonlu Keycloak)
// e-postayı doğrulamadan `email_verified=false` gönderiyor; v0.10.1067
// sıkılaştırması (claim VARSA ve false ise giriş yok) bu kurulumda herkesi
// dışarıda bırakıyor. TERCİH EDİLEN düzeltme IdP tarafında: Keycloak
// "Trust Email" (User federation → LDAP → Advanced settings → Trust Email +
// Sync all users; Identity providers → Trust Email). Bu dosya Coremetry
// tarafındaki AÇIK-SEÇİM anahtarıdır:
//
//   - trustUnverifiedEmail (varsayılan KAPALI) `auth_oidc` blobunda;
//   - YALNIZ izinli alan adları listesi DOLUYKEN etkilidir: doğrulanmamış
//     e-posta ancak kurumun kendi alan adlarından biriyle girebilir (alan
//     adı kontrolü aynen uygulanır). Liste boşken PUT 400 döner; blob elle
//     yazılmışsa yüklemede kapalı sayılır ve çalışma anında da (karar
//     fonksiyonu) etkisizdir — iki katmanlı savunma;
//   - claim true ya da HİÇ yoksa davranış değişmez;
//   - yalnız bu anahtar sayesinde kabul edilen giriş bir WARNING satırı
//     bırakır — tam e-posta DEĞİL, yalnız alan adı (izinli listeden, sınırlı
//     kardinalite) ve alan adı başına saatte en çok bir kez.
//
// ÖN KOŞUL / RİSK: IdP kullanıcının e-postasını kendisinin belirlemesine ya
// da değiştirmesine İZİN VERMEMELİ (self-registration yok, profilde e-posta
// düzenleme yok, sosyal/brokered IdP yok) — yoksa e-posta çarpışmasıyla hesap
// ele geçirme. Bu yüzden anahtar sayesinde gelen giriş (OIDCClaims.ViaTrust)
// yerel/LDAP ya da admin hesabını BAĞLAMAZ (api auth_permissions.go
// oidcLoginUser → email_unverified_privileged); yeni kullanıcı varsayılan
// rolle açılır, mevcut oidc viewer/editor girer.
//
// Yetki servisi, rol mantığı ve TLS kuralları bu anahtardan BAĞIMSIZ.

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// oidcEmailDecision — id_token e-posta kapısının sonucu. Class "" = kabul.
type oidcEmailDecision struct {
	Class    string // "" | email_unverified | email_missing | email_domain_denied
	Verified bool   // claim true mu (OIDCClaims.EmailVerified)
	ViaTrust bool   // YALNIZ trustUnverifiedEmail sayesinde kabul edildi
}

// trustUnverifiedEffective — anahtar yalnız izinli alan adı listesi doluyken
// etkili. SAF.
func trustUnverifiedEffective(trust bool, allowedDomains []string) bool {
	if !trust {
		return false
	}
	for _, d := range allowedDomains {
		if strings.TrimSpace(d) != "" {
			return true
		}
	}
	return false
}

// emailDomainAllowed — izinli alan adı listesi; boş liste = herkes. SAF.
func emailDomainAllowed(allowedDomains []string, email string) bool {
	if len(allowedDomains) == 0 {
		return true
	}
	dom := domainOf(email)
	if dom == "" {
		return false
	}
	for _, d := range allowedDomains {
		if strings.EqualFold(strings.TrimSpace(d), dom) {
			return true
		}
	}
	return false
}

// decideOIDCEmail — Exchange'in e-posta kapısı (sıra v0.10.1067 ile aynı:
// doğrulama → e-posta var mı → alan adı). SAF.
func decideOIDCEmail(rawVerified json.RawMessage, email string, allowedDomains []string, trustUnverified bool) oidcEmailDecision {
	verified, present := parseEmailVerified(rawVerified)
	d := oidcEmailDecision{Verified: verified}
	if present && !verified {
		if !trustUnverifiedEffective(trustUnverified, allowedDomains) {
			d.Class = "email_unverified"
			return d
		}
		d.ViaTrust = true
	}
	if email == "" {
		d.Class, d.ViaTrust = "email_missing", false
		return d
	}
	// Güvenlik incelemesi — YALNIZ güven anahtarıyla kabul edilen (ViaTrust)
	// girişte ASCII dışı e-posta reddedilir: alan adı karşılaştırması
	// (EqualFold) ve callback'in ToLower'ı Unicode katlar (ör. KELVIN SIGN
	// U+212A → "k"), doğrulanmamış bir adres izinli alan adına ya da var olan
	// bir hesaba çarpışabilirdi. Doğrulanmış / claim'siz giriş ESKİSİ GİBİ
	// (Türkçe karakterli e-posta mümkün).
	if d.ViaTrust && !isASCII(email) {
		d.Class, d.ViaTrust = "email_invalid", false
		return d
	}
	if !emailDomainAllowed(allowedDomains, email) {
		d.Class, d.ViaTrust = "email_domain_denied", false
		return d
	}
	return d
}

// trustedLoginLogEvery — aynı alan adı için WARNING satırları arası en az süre.
const trustedLoginLogEvery = time.Hour

// trustedLoginLog — alan adı → son WARNING zamanı. Anahtarlar izinli alan adı
// listesinden (≤50), büyüme sınırlı.
var trustedLoginLog sync.Map

// logTrustedUnverifiedLogin — kabul YALNIZ anahtar sayesinde olduysa, alan adı
// başına saatte bir satır. Tam e-posta loga GİRMEZ.
func logTrustedUnverifiedLogin(email string, now time.Time, logf func(string, ...any)) {
	dom := domainOf(email)
	if prev, ok := trustedLoginLog.Load(dom); ok {
		if t, _ := prev.(time.Time); now.Sub(t) < trustedLoginLogEvery {
			return
		}
	}
	trustedLoginLog.Store(dom, now)
	logf("[auth] WARNING: OIDC email_verified=false accepted (trustUnverifiedEmail) domain=%s", dom)
}

// warnTrustUnverifiedEmail — ayar YÜKLEMESİ başına bir kez (blob değişimi /
// PUT), warnInsecureOIDCTLS emsali.
func warnTrustUnverifiedEmail(s OIDCSettings, logf func(string, ...any)) {
	if !s.Enabled || !s.TrustUnverifiedEmail {
		return
	}
	logf("[auth] WARNING: OIDC doğrulanmamış e-postaya güveniliyor (trustUnverifiedEmail) issuer=%s domains=%d — "+
		"email_verified=false olan girişler izinli alan adlarıyla kabul edilir; tercih edilen düzeltme IdP'de "+
		"\"Trust Email\"", s.IssuerURL, len(s.AllowedDomains))
}

// trustUnverifiedEmailMsg — kayıt reddi (400) ve yükleme uyarısı metni.
const trustUnverifiedEmailMsg = "İzinli alan adları boşken doğrulanmamış e-postaya güvenilemez " +
	"(cannot trust unverified email while allowed domains is empty)"
