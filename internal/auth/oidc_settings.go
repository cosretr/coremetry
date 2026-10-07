package auth

// oidc_settings.go — v0.10.1067 (operatör: "Settings'ten yönetebilsem
// Helm'e göre daha iyi olur").
//
// OIDC/SSO bugüne dek yalnız config.yaml `auth.oidc` (Helm
// `config.auth.oidc` + `oidcClientSecret`) ile kuruluyor, keşif boot'ta
// bir kez koşuyordu; değişiklik = Helm upgrade + restart. Şimdi ayar
// system_settings'te tek JSON blob (anahtar `auth_oidc`, invariant #6):
// LoadPersisted boot'ta + 30 s yenilemede (değişim tespitli), SaveSettings
// admin PUT'unda; ikisi de canlı istemciyi atomik takaslar.
//
// ÖNCELİK: kayıtlı blob VARSA config.yaml'ın `auth.oidc`'sinin ÖNÜNE
// geçer; blob YOKSA config.yaml kaynak kalır (Helm'le kurulu mevcut
// kurulumlar aynen çalışır). İlk Settings açılışında form etkin
// yapılandırmayla dolu gelir; secret yalnız "kayıtlı" diye görünür.
// system_settings OKUNAMAZSA (satır yok değil, hata) SSO KAPALI başlar ve
// yenilemede yeniden denenir — config.yaml'a düşülmez (admin Settings'te
// kapatmış olabilir).
//
// SECRET: client_secret HİÇBİR GET'te dönmez (OIDCSnapshot alanı yok,
// yalnız ClientSecretStored; OIDCSettings.ClientSecret `json:"-"`, kalıcı
// biçim özel oidcStored); PUT'ta boş secret kayıtlıyı YALNIZ issuer ve
// client id değişmemişse korur — değiştiyse 400 (yoksa tek bir girişle
// kayıtlı secret saldırganın token ucuna gönderilirdi); audit ve log
// satırları secret taşımaz (String/GoString maskeli).
//
// KAPATMA ANAHTARI: enabled:false HER ZAMAN kaydedilir (doğrulama ve keşif
// koşmaz). enabled:true kayıt, keşif BAŞARILI olmadan yazılmaz.
//
// Yerel kullanıcı/parola girişi bu katmandan bağımsız, her zaman açık.
//
// v0.10.1112 — IdP TLS güveni (tlsCACertPEM / tlsInsecureSkipVerify) aynı
// blobda; kurallar ve tek taşıma kurucusu oidc_tls.go'da.
//
// v0.10.1120 — trustUnverifiedEmail (email_verified=false'u izinli alan
// adlarıyla kabul) aynı blobda; kurallar oidc_email_trust.go'da.
//
// v0.10.1121 — usernameFallback / usernameClaim (e-posta yoksa kullanıcı
// adıyla eşleştir) aynı blobda; kurallar oidc_email_resolve.go'da.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/config"
)

// OIDCSettingsKey — system_settings anahtarı.
const OIDCSettingsKey = "auth_oidc"

const (
	OIDCSourceSettings = "settings"
	OIDCSourceConfig   = "config"

	oidcCallbackPath   = "/api/auth/oidc/callback"
	oidcDisplayNameMax = 40
	oidcClientIDMax    = 256
	oidcURLMax         = 512
	oidcMaxDomains     = 50
	oidcMaxScopes      = 20

	// v0.10.1110 — merkezi login yetki servisi (oidc_permissions.go).
	PermissionTTLDefault   = 300
	PermissionTTLMin       = 60
	PermissionTTLMax       = 3600
	PermissionClaimDefault = "permissions"
	permissionKeyMinLen    = 16
	permissionKeyMaxLen    = 256
	permissionClaimMax     = 64
	// v0.10.1111 — IP/CIDR izin listesi + güvenilen vekiller (oidc_permission_net.go).
	PermissionMaxCIDRs = 32
)

var oidcDefaultScopes = []string{"openid", "email", "profile"}

// OIDCSettingsStore — system_settings'in dar yüzü (*chstore.Store bunu
// karşılar; auth chstore'u import etmez).
type OIDCSettingsStore interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
}

// OIDCSettings — etkin/istenen yapılandırma. ClientSecret `json:"-"`:
// bu tip kazara JSON'a dökülse bile secret çıkmaz. PUT gövdesi api
// katmanında ayrı tipten çözülür, kalıcı biçim oidcStored.
type OIDCSettings struct {
	Enabled        bool     `json:"enabled"`
	IssuerURL      string   `json:"issuerUrl"`
	ClientID       string   `json:"clientId"`
	ClientSecret   string   `json:"-"`
	RedirectURL    string   `json:"redirectUrl"`
	Scopes         []string `json:"scopes"`
	DisplayName    string   `json:"displayName"`
	DefaultRole    string   `json:"defaultRole"`
	AllowedDomains []string `json:"allowedDomains"`
	// v0.10.1110 — merkezi login yetki servisi + token claim'inden rol
	// (oidc_permissions.go). PermissionServiceKey bir SECRET: `json:"-"`,
	// kalıcı biçim oidcStored, cevapta yalnız PermissionServiceKeySet.
	PermissionServiceEnabled bool   `json:"permissionServiceEnabled"`
	PermissionServiceKey     string `json:"-"`
	PermissionTTLSeconds     int    `json:"permissionTTLSeconds"`
	PermissionsClaim         string `json:"permissionsClaim"`
	RoleFromClaim            bool   `json:"roleFromClaim"`
	// v0.10.1111 — operatör kararı "Anahtarsız olmaz mı": AllowNoKey açıkken
	// X-Coremetry-Auth-Key HİÇ denetlenmez (kayıtlı anahtar olsa da). Ağ
	// sınırı AllowedCIDRs (boş = IP kısıtı yok); X-Forwarded-For yalnız
	// doğrudan eş TrustedProxies'teyse okunur (oidc_permission_net.go).
	PermissionServiceAllowNoKey     bool     `json:"permissionServiceAllowNoKey"`
	PermissionServiceAllowedCIDRs   []string `json:"permissionServiceAllowedCIDRs"`
	PermissionServiceTrustedProxies []string `json:"permissionServiceTrustedProxies"`
	// v0.10.1112 — IdP TLS güveni (oidc_tls.go). Secret DEĞİL (açık
	// sertifika): GET'te aynen döner. Skip-verify https kuralını ve dial
	// korumasını GEVŞETMEZ.
	TLSCACertPEM          string `json:"tlsCACertPEM"`
	TLSInsecureSkipVerify bool   `json:"tlsInsecureSkipVerify"`
	// v0.10.1120 — email_verified=false'u kabul et (oidc_email_trust.go).
	// YALNIZ izinli alan adı listesi doluyken geçerli; boşken PUT 400.
	TrustUnverifiedEmail bool `json:"trustUnverifiedEmail"`
	// v0.10.1121 — id_token/UserInfo'da e-posta yoksa kullanıcı adıyla eşleştir
	// (AD/LDAP dizini → e-posta; yoksa users.ldap_username). Varsayılan KAPALI;
	// UsernameClaim varsayılan preferred_username (oidc_email_resolve.go).
	UsernameFallback bool   `json:"usernameFallback"`
	UsernameClaim    string `json:"usernameClaim"`
	// UsernameFallbackAllowAdmin — güvenlik incelemesi F2: kullanıcı adıyla
	// eşleşen giriş admin hesabını YALNIZ bu ayrı açık-seçimle açar (varsayılan
	// kapalı; usernameFallback kapalıyken anlamsız → normalize false yapar).
	UsernameFallbackAllowAdmin bool `json:"usernameFallbackAllowAdmin"`
}

// String / GoString — %v, %+v, %#v ile kazara loglansa bile secret basılmasın.
func (s OIDCSettings) String() string {
	return fmt.Sprintf("{enabled:%v issuer:%q clientId:%q secretSet:%v redirect:%q scopes:%v display:%q role:%q domains:%v permSvc:%v permKeySet:%v permTTL:%d claim:%q roleFromClaim:%v permNoKey:%v permCIDRs:%v permProxies:%v tlsCABytes:%d tlsSkipVerify:%v trustUnverifiedEmail:%v usernameFallback:%v usernameClaim:%q usernameAllowAdmin:%v}",
		s.Enabled, s.IssuerURL, s.ClientID, s.ClientSecret != "", s.RedirectURL,
		s.Scopes, s.DisplayName, s.DefaultRole, s.AllowedDomains,
		s.PermissionServiceEnabled, s.PermissionServiceKey != "", s.PermissionTTLSeconds,
		s.PermissionsClaim, s.RoleFromClaim,
		s.PermissionServiceAllowNoKey, s.PermissionServiceAllowedCIDRs, s.PermissionServiceTrustedProxies,
		len(s.TLSCACertPEM), s.TLSInsecureSkipVerify, s.TrustUnverifiedEmail,
		s.UsernameFallback, s.UsernameClaim, s.UsernameFallbackAllowAdmin)
}

func (s OIDCSettings) GoString() string { return "auth.OIDCSettings" + s.String() }

// oidcStored — blobun kalıcı biçimi (secret dahil). Yalnız bu paketin
// depo yolu kullanır; hiçbir cevaba girmez.
type oidcStored struct {
	Enabled        bool     `json:"enabled"`
	IssuerURL      string   `json:"issuerUrl"`
	ClientID       string   `json:"clientId"`
	ClientSecret   string   `json:"clientSecret,omitempty"`
	RedirectURL    string   `json:"redirectUrl"`
	Scopes         []string `json:"scopes"`
	DisplayName    string   `json:"displayName"`
	DefaultRole    string   `json:"defaultRole"`
	AllowedDomains []string `json:"allowedDomains"`
	// v0.10.1110 — alan SIRASI OIDCSettings ile birebir (tip dönüşümü).
	PermissionServiceEnabled bool   `json:"permissionServiceEnabled,omitempty"`
	PermissionServiceKey     string `json:"permissionServiceKey,omitempty"`
	PermissionTTLSeconds     int    `json:"permissionTTLSeconds,omitempty"`
	PermissionsClaim         string `json:"permissionsClaim,omitempty"`
	RoleFromClaim            bool   `json:"roleFromClaim,omitempty"`
	// v0.10.1111
	PermissionServiceAllowNoKey     bool     `json:"permissionServiceAllowNoKey,omitempty"`
	PermissionServiceAllowedCIDRs   []string `json:"permissionServiceAllowedCIDRs,omitempty"`
	PermissionServiceTrustedProxies []string `json:"permissionServiceTrustedProxies,omitempty"`
	// v0.10.1112
	TLSCACertPEM          string `json:"tlsCACertPEM,omitempty"`
	TLSInsecureSkipVerify bool   `json:"tlsInsecureSkipVerify,omitempty"`
	// v0.10.1120
	TrustUnverifiedEmail bool `json:"trustUnverifiedEmail,omitempty"`
	// v0.10.1121
	UsernameFallback           bool   `json:"usernameFallback,omitempty"`
	UsernameClaim              string `json:"usernameClaim,omitempty"`
	UsernameFallbackAllowAdmin bool   `json:"usernameFallbackAllowAdmin,omitempty"`
}

func (s OIDCSettings) stored() oidcStored   { return oidcStored(s) }
func (s oidcStored) settings() OIDCSettings { return OIDCSettings(s) }

// OIDCSnapshot — GET cevabı. Secret alanı YOK.
type OIDCSnapshot struct {
	Enabled            bool     `json:"enabled"`
	IssuerURL          string   `json:"issuerUrl"`
	ClientID           string   `json:"clientId"`
	ClientSecretStored bool     `json:"clientSecretStored"`
	RedirectURL        string   `json:"redirectUrl"`
	DefaultRedirectURL string   `json:"defaultRedirectUrl,omitempty"`
	Scopes             []string `json:"scopes"`
	DisplayName        string   `json:"displayName"`
	DefaultRole        string   `json:"defaultRole"`
	AllowedDomains     []string `json:"allowedDomains"`
	// v0.10.1110 — yetki servisi. Anahtar YOK; yalnız "kayıtlı mı".
	PermissionServiceEnabled bool   `json:"permissionServiceEnabled"`
	PermissionServiceKeySet  bool   `json:"permissionServiceKeySet"`
	PermissionTTLSeconds     int    `json:"permissionTTLSeconds"`
	PermissionsClaim         string `json:"permissionsClaim"`
	RoleFromClaim            bool   `json:"roleFromClaim"`
	// v0.10.1111 — anahtarsız kip + ağ sınırı (secret değil, aynen döner).
	PermissionServiceAllowNoKey     bool     `json:"permissionServiceAllowNoKey"`
	PermissionServiceAllowedCIDRs   []string `json:"permissionServiceAllowedCIDRs"`
	PermissionServiceTrustedProxies []string `json:"permissionServiceTrustedProxies"`
	// v0.10.1112 — IdP TLS güveni (secret değil, aynen döner).
	TLSCACertPEM          string `json:"tlsCACertPEM"`
	TLSInsecureSkipVerify bool   `json:"tlsInsecureSkipVerify"`
	// v0.10.1120 — doğrulanmamış e-postaya güven (secret değil).
	TrustUnverifiedEmail bool `json:"trustUnverifiedEmail"`
	// v0.10.1121 — kullanıcı adıyla eşleştirme (secret değil).
	UsernameFallback           bool   `json:"usernameFallback"`
	UsernameClaim              string `json:"usernameClaim"`
	UsernameFallbackAllowAdmin bool   `json:"usernameFallbackAllowAdmin"`
	// Source — etkin yapılandırmanın kaynağı: settings (blob) | config (config.yaml).
	Source string `json:"source"`
	// Active — bu pod'da canlı istemci var mı (giriş düğmesi). İstenen
	// yapılandırma uygulanamadıysa false — eski istemci sessizce sürmez.
	Active bool `json:"active"`
	// LastError — son uygulama hatası; secret ya da IdP gövdesi içermez.
	LastError string `json:"lastError,omitempty"`
}

// OIDCSettingsError — doğrulama hatası (HTTP 400).
type OIDCSettingsError struct {
	Field string
	Msg   string
}

func (e *OIDCSettingsError) Error() string { return e.Msg }

// OIDCDiscoveryError — kayıt keşif başarısız olduğu için reddedildi (HTTP 400).
type OIDCDiscoveryError struct{ Err error }

func (e *OIDCDiscoveryError) Error() string {
	return "kimlik sağlayıcı keşfi başarısız: " + e.Err.Error()
}
func (e *OIDCDiscoveryError) Unwrap() error { return e.Err }

type oidcState struct {
	loaded  bool
	source  string
	eff     OIDCSettings // etkin/istenen yapılandırma (secret dahil) — dışarı çıkmaz
	lastRaw []byte       // son okunan blob (değişim tespiti)
	lastErr string
	retry   bool // istenen yapılandırma uygulanamadı (geçici) → sonraki tikte yeniden dene
}

// NewOIDCService — boot yapılandırması (config.yaml; Settings kaynağının ağ
// sınırı da buradan) + public URL (varsayılan redirect) + ayar deposu.
// Her zaman dolu döner; canlı istemci LoadPersisted ile kurulur.
func NewOIDCService(boot config.OIDCConfig, publicURL string, store OIDCSettingsStore) *OIDCService {
	return &OIDCService{boot: boot, publicURL: strings.TrimSpace(publicURL), store: store, uiPolicy: newUIPolicy(boot)}
}

func (o *OIDCService) policyFor(source string) oidcNetPolicy {
	if source == OIDCSourceConfig {
		return configPolicy
	}
	return o.uiPolicy
}

// DefaultOIDCRedirectURL — public URL biliniyorsa `<public>/api/auth/oidc/callback`. SAF.
func DefaultOIDCRedirectURL(publicURL string) string {
	p := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if p == "" {
		return ""
	}
	return p + oidcCallbackPath
}

// splitList — virgül / boşluk / satır sonu ayraçlı tek girdiyi de
// kabul eder (UI dizi yollar; elle yazılmış "openid email" de olur). SAF.
func splitList(in []string) []string {
	var out []string
	for _, v := range in {
		out = append(out, strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == ';'
		})...)
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// NormalizeOIDCSettings — kırpma + varsayılanlar. SAF. Issuer'ın sondaki
// "/"'ı KORUNUR: keşif belgesindeki issuer ile birebir eşleşmeli (ör.
// Auth0 "https://tenant.example.test/").
func NormalizeOIDCSettings(in OIDCSettings, publicURL string) OIDCSettings {
	s := in
	s.IssuerURL = strings.TrimSpace(s.IssuerURL)
	s.ClientID = strings.TrimSpace(s.ClientID)
	s.ClientSecret = strings.TrimSpace(s.ClientSecret)
	s.RedirectURL = strings.TrimSpace(s.RedirectURL)
	if s.RedirectURL == "" {
		s.RedirectURL = DefaultOIDCRedirectURL(publicURL)
	}
	// Kapsamlar büyük-küçük harf DUYARLI (RFC 6749) — küçültülmez.
	s.Scopes = dedupe(splitList(s.Scopes))
	if len(s.Scopes) == 0 {
		s.Scopes = append([]string(nil), oidcDefaultScopes...)
	}
	s.DisplayName = strings.TrimSpace(s.DisplayName)
	if s.DisplayName == "" {
		s.DisplayName = "SSO"
	}
	s.DefaultRole = strings.ToLower(strings.TrimSpace(s.DefaultRole))
	if s.DefaultRole == "" {
		s.DefaultRole = RoleViewer
	}
	doms := splitList(s.AllowedDomains)
	for i, d := range doms {
		doms[i] = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "@")
	}
	s.AllowedDomains = dedupe(doms)
	s.PermissionServiceKey = strings.TrimSpace(s.PermissionServiceKey)
	s.PermissionTTLSeconds = clampPermissionTTL(s.PermissionTTLSeconds)
	s.PermissionsClaim = strings.TrimSpace(s.PermissionsClaim)
	if s.PermissionsClaim == "" {
		s.PermissionsClaim = PermissionClaimDefault
	}
	s.PermissionServiceAllowedCIDRs = normalizeCIDRList(s.PermissionServiceAllowedCIDRs)
	s.PermissionServiceTrustedProxies = normalizeCIDRList(s.PermissionServiceTrustedProxies)
	s.TLSCACertPEM = normalizePEM(s.TLSCACertPEM)
	s.UsernameClaim = strings.TrimSpace(s.UsernameClaim)
	if s.UsernameClaim == "" {
		s.UsernameClaim = UsernameClaimDefault
	}
	// F2 — admin açık-seçimi yalnız eşleştirme açıkken anlamlı.
	s.UsernameFallbackAllowAdmin = s.UsernameFallbackAllowAdmin && s.UsernameFallback
	return s
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// ValidateOIDCSettings — SSO alanları yalnız Enabled iken; kapatma her zaman
// geçer. v0.10.1110: yetki servisi alanları SSO'dan BAĞIMSIZ her zaman
// denetlenir (servis SSO kapalıyken de açık olabilir; geçersiz anahtarla
// açılamaz). v0.10.1112: özel CA da her zaman (F1). allowInsecure =
// config.yaml allow_insecure_issuer (http issuer). SAF.
func ValidateOIDCSettings(s OIDCSettings, allowInsecure bool) error {
	if err := validatePermissionSettings(s); err != nil {
		return err
	}
	// v0.10.1112 (güvenlik incelemesi F1) — özel CA SSO kapalıyken de
	// denetlenir: tavan + özel anahtar reddi + x509 çözümü. Yoksa
	// enabled:false gövdesiyle bir özel anahtar bloba yazılır ve GET'te
	// yankılanırdı. Ağ/keşif denetimi kapalıyken yine koşmaz.
	if err := validateOIDCCACert(s.TLSCACertPEM); err != nil {
		return err
	}
	// v0.10.1121 — kullanıcı adı claim'i her zaman (blob'a çöp yazılmasın).
	if err := validateUsernameClaim(s.UsernameClaim); err != nil {
		return err
	}
	if !s.Enabled {
		return nil
	}
	// v0.10.1120 — doğrulanmamış e-postaya güven YALNIZ dolu izinli alan adı
	// listesiyle. Kapatma anahtarı mutlak: SSO kapalıyken denetlenmez (kayıt
	// her zaman geçer); yeniden açan kayıt bu çiftle 400 alır. Yükleme yolu
	// bunu çağırmadan önce bayrağı düşürür (LoadPersisted) — SSO düşmez.
	if s.TrustUnverifiedEmail && !trustUnverifiedEffective(true, s.AllowedDomains) {
		return &OIDCSettingsError{"trustUnverifiedEmail", trustUnverifiedEmailMsg}
	}
	if err := ValidateOIDCIssuer(s.IssuerURL, allowInsecure); err != nil {
		return err
	}
	if s.ClientID == "" {
		return &OIDCSettingsError{"clientId", "Client ID gerekli"}
	}
	if len(s.ClientID) > oidcClientIDMax {
		return &OIDCSettingsError{"clientId", "Client ID çok uzun"}
	}
	if s.ClientSecret == "" {
		return &OIDCSettingsError{"clientSecret", "Client secret gerekli"}
	}
	if s.RedirectURL == "" {
		return &OIDCSettingsError{"redirectUrl", "Yönlendirme adresi gerekli (public URL tanımlı değil)"}
	}
	ru, err := url.Parse(s.RedirectURL)
	if err != nil || ru.Host == "" || (ru.Scheme != "https" && ru.Scheme != "http") || len(s.RedirectURL) > oidcURLMax {
		return &OIDCSettingsError{"redirectUrl", "Yönlendirme adresi tam bir http(s) URL olmalı"}
	}
	hasOpenID := false
	for _, sc := range s.Scopes {
		if sc == "openid" {
			hasOpenID = true
		}
	}
	if !hasOpenID {
		return &OIDCSettingsError{"scopes", "Kapsamlar 'openid' içermeli"}
	}
	if len(s.Scopes) > oidcMaxScopes {
		return &OIDCSettingsError{"scopes", "Çok fazla kapsam"}
	}
	if utf8.RuneCountInString(s.DisplayName) > oidcDisplayNameMax {
		return &OIDCSettingsError{"displayName", "Düğme etiketi en çok 40 karakter"}
	}
	if !IsValidRole(s.DefaultRole) {
		return &OIDCSettingsError{"defaultRole", "Varsayılan rol admin, editor ya da viewer olmalı"}
	}
	if len(s.AllowedDomains) > oidcMaxDomains {
		return &OIDCSettingsError{"allowedDomains", "Çok fazla alan adı"}
	}
	for _, d := range s.AllowedDomains {
		if !isASCII(d) {
			return &OIDCSettingsError{"allowedDomains", fmt.Sprintf("Alan adı ASCII olmalı (punycode: xn--…): %q", d)}
		}
		if !strings.Contains(d, ".") || strings.ContainsAny(d, "@/: ") {
			return &OIDCSettingsError{"allowedDomains", fmt.Sprintf("Geçersiz alan adı: %q", d)}
		}
	}
	// viewer üstü rol, IdP'deki HERKESE otomatik verilmesin.
	if s.DefaultRole != RoleViewer && len(s.AllowedDomains) == 0 {
		return &OIDCSettingsError{"defaultRole", "viewer dışındaki varsayılan rol için izinli alan adı gerekli"}
	}
	return nil
}

// ValidateOIDCIssuer — https zorunlu (http yalnız allowInsecure), kimlik
// bilgisi / sorgu / parça yok. SAF.
func ValidateOIDCIssuer(issuer string, allowInsecure bool) error {
	if issuer == "" {
		return &OIDCSettingsError{"issuerUrl", "Issuer URL gerekli"}
	}
	if len(issuer) > oidcURLMax {
		return &OIDCSettingsError{"issuerUrl", "Issuer URL çok uzun"}
	}
	u, err := url.Parse(issuer)
	if err != nil {
		return &OIDCSettingsError{"issuerUrl", "Issuer URL çözülemedi"}
	}
	if err := (oidcNetPolicy{allowInsecure: allowInsecure}).checkURL(u); err != nil {
		return &OIDCSettingsError{"issuerUrl", "Issuer URL " + err.Error()}
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return &OIDCSettingsError{"issuerUrl", "Issuer URL sorgu ya da # içeremez"}
	}
	return nil
}

// clampPermissionTTL — 0/negatif → varsayılan 300; [60, 3600]. SAF.
func clampPermissionTTL(v int) int {
	switch {
	case v <= 0:
		return PermissionTTLDefault
	case v < PermissionTTLMin:
		return PermissionTTLMin
	case v > PermissionTTLMax:
		return PermissionTTLMax
	}
	return v
}

// reservedClaimNames — yetki claim'i olarak SEÇİLEMEYEN standart kimlik /
// profil / protokol claim'leri (OIDC Core §5.1 + JWT kayıtlı adlar).
// Güvenlik incelemesi F2: bunların bir kısmını kullanıcı IdP profilinden
// kendisi düzenleyebilir; rol oradan okunmasın.
var reservedClaimNames = map[string]bool{
	"sub": true, "name": true, "given_name": true, "family_name": true, "middle_name": true,
	"nickname": true, "preferred_username": true, "profile": true, "picture": true, "website": true,
	"email": true, "email_verified": true, "gender": true, "birthdate": true, "zoneinfo": true,
	"locale": true, "phone_number": true, "phone_number_verified": true, "address": true,
	"updated_at": true, "iss": true, "aud": true, "exp": true, "iat": true, "nonce": true,
	"at_hash": true, "azp": true,
}

// validPermissionsClaim — claim adı: 1-64, harf/rakam ve `_ - . :`. SAF.
func validPermissionsClaim(c string) bool {
	if c == "" || len(c) > permissionClaimMax {
		return false
	}
	for i := 0; i < len(c); i++ {
		b := c[i]
		ok := (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') ||
			b == '_' || b == '-' || b == '.' || b == ':'
		if !ok {
			return false
		}
	}
	return true
}

// validatePermissionSettings — yetki servisi alanları (normalize edilmiş
// girdi). Anahtar: dolu ise ≥16, ≤256, yazdırılabilir ASCII, yer tutucu
// değil (secret_strength.go weakSecretMarkers); servis açıksa zorunlu —
// v0.10.1111: AllowNoKey açıkken değil. CIDR listeleri ≤32, her giriş
// geçerli IPv4/IPv6 CIDR ya da tek IP. Hata metni anahtarı TAŞIMAZ. SAF.
func validatePermissionSettings(s OIDCSettings) error {
	k := s.PermissionServiceKey
	if s.PermissionServiceEnabled && !s.PermissionServiceAllowNoKey && k == "" {
		return &OIDCSettingsError{"permissionServiceKey", "Yetki servisi açıkken paylaşılan anahtar gerekli (ya da \"Anahtarsız kabul et\")"}
	}
	if err := validateCIDRList("permissionServiceAllowedCIDRs", "IP izin listesi", s.PermissionServiceAllowedCIDRs); err != nil {
		return err
	}
	if err := validateCIDRList("permissionServiceTrustedProxies", "Güvenilen vekil listesi", s.PermissionServiceTrustedProxies); err != nil {
		return err
	}
	if k != "" {
		if len(k) < permissionKeyMinLen {
			return &OIDCSettingsError{"permissionServiceKey", "Paylaşılan anahtar en az 16 karakter olmalı"}
		}
		if len(k) > permissionKeyMaxLen {
			return &OIDCSettingsError{"permissionServiceKey", "Paylaşılan anahtar çok uzun (en çok 256)"}
		}
		for i := 0; i < len(k); i++ {
			if k[i] < 0x21 || k[i] > 0x7e {
				return &OIDCSettingsError{"permissionServiceKey", "Paylaşılan anahtar boşluksuz yazdırılabilir ASCII olmalı"}
			}
		}
		low := strings.ToLower(k)
		for _, m := range weakSecretMarkers {
			if strings.Contains(low, m) {
				return &OIDCSettingsError{"permissionServiceKey", "Paylaşılan anahtar yer tutucu değerde — rastgele bir değer üretin (openssl rand -hex 32)"}
			}
		}
	}
	// 0 / "" = normalize edilmemiş girdi → varsayılan (NormalizeOIDCSettings).
	if t := s.PermissionTTLSeconds; t != 0 && (t < PermissionTTLMin || t > PermissionTTLMax) {
		return &OIDCSettingsError{"permissionTTLSeconds", "TTL 60–3600 saniye olmalı"}
	}
	if s.PermissionsClaim != "" && !validPermissionsClaim(s.PermissionsClaim) {
		return &OIDCSettingsError{"permissionsClaim", "Claim adı 1-64 karakter; harf, rakam ve _ - . : olabilir"}
	}
	if reservedClaimNames[strings.ToLower(s.PermissionsClaim)] {
		return &OIDCSettingsError{"permissionsClaim", "Claim adı standart bir kimlik/profil claim'i olamaz (ör. permissions kullanın)"}
	}
	return nil
}

// disablePermissionService — geçersiz yetki servisi alanlarıyla kaydedilmiş /
// içe aktarılmış blobda YALNIZ servisi kapatır (anahtar düşer, claim'den rol
// kapanır, varsayılanlar); SSO alanlarına dokunmaz. SAF.
func disablePermissionService(s OIDCSettings) OIDCSettings {
	s.PermissionServiceEnabled = false
	s.PermissionServiceKey = ""
	s.PermissionServiceAllowNoKey = false
	s.PermissionServiceAllowedCIDRs = nil
	s.PermissionServiceTrustedProxies = nil
	s.RoleFromClaim = false
	s.PermissionsClaim = PermissionClaimDefault
	s.PermissionTTLSeconds = PermissionTTLDefault
	return s
}

func settingsFromConfig(c config.OIDCConfig) OIDCSettings {
	return OIDCSettings{
		Enabled: c.Enabled, IssuerURL: c.IssuerURL, ClientID: c.ClientID,
		ClientSecret: c.ClientSecret, RedirectURL: c.RedirectURL,
		Scopes: c.Scopes, DisplayName: c.DisplayName, DefaultRole: c.DefaultRole,
		AllowedDomains: c.AllowedDomains,
	}
}

func (s OIDCSettings) toConfig() config.OIDCConfig {
	return config.OIDCConfig{
		Enabled: s.Enabled, IssuerURL: s.IssuerURL, ClientID: s.ClientID,
		ClientSecret: s.ClientSecret, RedirectURL: s.RedirectURL,
		Scopes: s.Scopes, DisplayName: s.DisplayName, DefaultRole: s.DefaultRole,
		AllowedDomains: s.AllowedDomains,
	}
}

func (o *OIDCService) state() oidcState {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.st
}

// cachedDiscovery — canlı istemcinin keşfi, issuer aynıysa (ağsız yeniden
// kurulum). v0.10.1112: TLS ayarı (özel CA / skip-verify) değiştiyse önbellek
// KULLANILMAZ — yeni güven ayarı kayıttan önce gerçek bir el sıkışmayla
// sınanır (yoksa IdP'ye bağlanamayan bir TLS ayarı "kaydedildi" der, giriş
// token ucunda düşerdi).
func (o *OIDCService) cachedDiscovery(issuer string, t oidcTLSOptions) *OIDCDiscovery {
	if c := o.live.Load(); c != nil && c.cfg.IssuerURL == issuer && c.tls == t {
		return c.disc
	}
	return nil
}

// LoadPersisted — boot + 30 s yenileme + peer sinyali. Blob yoksa
// config.yaml uygulanır; blob değişmemişse (ve bekleyen yeniden deneme
// yoksa) hiçbir şey yapılmaz. Issuer aynı kaldıkça yeniden kurulum ağsız
// (önbellekli keşif) — alan adı / rol / etiket / kapsam değişikliği peer'da
// sessizce eski kalamaz. Keşif, çağıranın (sinyal yolu 5 s) bağlamından
// AYRILMIŞ kendi ≤10 s süresiyle koşar.
func (o *OIDCService) LoadPersisted(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	var raw []byte
	var readErr error
	if o.store != nil {
		raw, readErr = o.store.GetSetting(ctx, OIDCSettingsKey)
	}
	st := o.state()
	if readErr != nil {
		if !st.loaded {
			// Hiç yüklenmedi ve depo okunamıyor: SSO KAPALI, yenilemede tekrar.
			// config.yaml'a düşülmez — admin Settings'te kapatmış olabilir.
			o.mu.Lock()
			o.st.lastErr, o.st.retry = "ayar deposu okunamadı", true
			o.mu.Unlock()
		}
		return fmt.Errorf("oidc ayarı okunamadı: %w", readErr)
	}
	if len(raw) == 0 {
		if st.loaded && st.source == OIDCSourceConfig && !st.retry {
			return nil
		}
		return o.apply(ctx, NormalizeOIDCSettings(settingsFromConfig(o.boot), o.publicURL), OIDCSourceConfig, nil)
	}
	if st.loaded && st.source == OIDCSourceSettings && bytes.Equal(raw, st.lastRaw) && !st.retry {
		return nil
	}
	var stored oidcStored
	if err := json.Unmarshal(raw, &stored); err != nil {
		return o.badBlob(raw, errors.New("oidc blobu çözülemedi"))
	}
	s := NormalizeOIDCSettings(stored.settings(), o.publicURL)
	// v0.10.1110 (güvenlik incelemesi N4): yetki servisi alanları geçersizse
	// SSO DÜŞMEZ — yalnız servis kapanır (log + lastError). Geçersiz blob
	// elle yazılmış ya da içe aktarılmış olabilir; PUT yolu zaten 400 verir.
	var warns []string
	if permErr := validatePermissionSettings(s); permErr != nil {
		s = disablePermissionService(s)
		warns = append(warns, "yetki servisi ayarı geçersiz — servis kapatıldı: "+permErr.Error())
	}
	// v0.10.1112 — aynı kalıp: çözülemeyen özel CA SSO'yu sessizce DÜŞÜRMEZ;
	// yapılandırma özel CA'sız (sistem kökleriyle) uygulanır, lastError yazılır.
	// SSO kapalıyken de (F1: geçersiz CA etkin ayarda/GET'te kalmasın);
	// geçerliyse kanonik biçim (N2: bloklar arası metin düşer).
	if ca, caErr := canonicalOIDCCACert(s.TLSCACertPEM); caErr != nil {
		s.TLSCACertPEM = ""
		warns = append(warns, "özel CA sertifikası geçersiz — yok sayıldı: "+caErr.Error())
	} else {
		s.TLSCACertPEM = ca
	}
	// v0.10.1120 — aynı kalıp (derinlemesine savunma): elle yazılmış blobda
	// izinli alan adı listesi boşken trustUnverifiedEmail SSO'yu düşürmez,
	// KAPALI sayılır (Exchange'deki karar da listeyi ayrıca denetler).
	if s.TrustUnverifiedEmail && !trustUnverifiedEffective(true, s.AllowedDomains) {
		s.TrustUnverifiedEmail = false
		warns = append(warns, "trustUnverifiedEmail yok sayıldı — "+trustUnverifiedEmailMsg)
	}
	// v0.10.1121 — aynı kalıp: geçersiz kullanıcı adı claim'i SSO'yu düşürmez;
	// eşleştirme kapanır, claim varsayılana döner.
	if err := validateUsernameClaim(s.UsernameClaim); err != nil {
		s.UsernameFallback, s.UsernameClaim, s.UsernameFallbackAllowAdmin = false, UsernameClaimDefault, false
		warns = append(warns, "usernameClaim geçersiz — kullanıcı adıyla eşleştirme kapatıldı: "+err.Error())
	}
	if err := ValidateOIDCSettings(s, o.uiPolicy.allowInsecure); err != nil {
		return o.badBlob(raw, fmt.Errorf("oidc blobu geçersiz: %w", err))
	}
	if !bytes.Equal(raw, st.lastRaw) { // ayar yüklemesi başına bir kez (yeniden denemede değil)
		warnInsecureOIDCTLS(s, log.Printf)
		warnTrustUnverifiedEmail(s, log.Printf)
	}
	applyErr := o.apply(ctx, s, OIDCSourceSettings, raw)
	if len(warns) > 0 {
		msg := strings.Join(warns, " · ")
		log.Printf("[auth] %s", msg)
		o.mu.Lock()
		if applyErr == nil {
			o.st.lastErr = msg
		} else {
			o.st.lastErr = msg + " · " + o.st.lastErr
		}
		o.mu.Unlock()
	}
	return applyErr
}

// badBlob — bozuk/geçersiz blob: yüklüyse canlı istemci ve kaynak yerinde
// kalır (son-iyi); hiç yüklenmemişse SSO kapalı. lastRaw işaretlenir ki aynı
// blob her tikte yeniden loglanmasın.
func (o *OIDCService) badBlob(raw []byte, err error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.st.loaded {
		o.st.loaded = true
		o.st.source = OIDCSourceSettings
		o.live.Store(nil)
	}
	o.st.lastErr = err.Error()
	o.st.lastRaw = raw
	o.st.retry = false
	return err
}

// apply — istenen yapılandırmayı uygular. Ağ (keşif) mu DIŞINDA; takas
// mu altında. Uygulanamazsa canlı istemci KALDIRILIR (eski IdP/alan adı
// sessizce sürmesin; Snapshot active:false) ve geçici hata sonraki tikte
// yeniden denenir.
func (o *OIDCService) apply(ctx context.Context, s OIDCSettings, source string, raw []byte) error {
	var cli *oidcClient
	var err error
	if s.Enabled {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), oidcHTTPTimeout)
		t := s.tlsOptions()
		cli, err = buildOIDCClient(dctx, s.toConfig(), o.policyFor(source), t, o.cachedDiscovery(s.IssuerURL, t))
		cancel()
		if cli != nil {
			cli.applyEmailOptions(s)
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.st.loaded, o.st.source, o.st.eff, o.st.lastRaw = true, source, s, raw
	if err != nil {
		o.live.Store(nil)
		o.st.lastErr = err.Error()
		var miss *oidcMissingError
		o.st.retry = !errors.As(err, &miss)
		return err
	}
	o.live.Store(cli)
	o.st.lastErr, o.st.retry = "", false
	if cli != nil {
		log.Printf("[auth] OIDC ready — source=%s issuer=%s display=%q", source, s.IssuerURL, s.DisplayName)
	}
	return nil
}

// SaveSettings — admin PUT. Boş secret kayıtlıyı YALNIZ issuer ve client id
// aynıysa korur. enabled:true ise keşif BAŞARILI olmadan yazılmaz (issuer
// aynıysa önbellekli keşif — IdP kesintisinde alan adı daraltılabilsin);
// enabled:false her zaman yazılır. Başarıda canlı istemci aynı anda takaslanır.
func (o *OIDCService) SaveSettings(ctx context.Context, in OIDCSettings) (OIDCSnapshot, error) {
	_, next, err := o.SaveSettingsWithPrev(ctx, in)
	return next, err
}

// SaveSettingsWithPrev — SaveSettings + kayıttan HEMEN ÖNCEKİ snapshot, ikisi
// de applyMu altında (güvenlik incelemesi N5: audit'in eski→yeni çifti
// eşzamanlı bir PUT'la karışmasın). Hata durumunda prev de boş döner.
func (o *OIDCService) SaveSettingsWithPrev(ctx context.Context, in OIDCSettings) (prevSnap, next OIDCSnapshot, err error) {
	if o == nil || o.store == nil {
		return OIDCSnapshot{}, OIDCSnapshot{}, errors.New("oidc ayar deposu yok")
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	prevSnap = o.Snapshot() // mu'yu kısa tutar; applyMu başka bir kaydı dışarıda tutar
	if next, err = o.saveLocked(ctx, in); err != nil {
		return OIDCSnapshot{}, OIDCSnapshot{}, err
	}
	return prevSnap, next, nil
}

// saveLocked — SaveSettings gövdesi; çağıran applyMu'yu tutar.
func (o *OIDCService) saveLocked(ctx context.Context, in OIDCSettings) (OIDCSnapshot, error) {
	prev := NormalizeOIDCSettings(o.state().eff, o.publicURL)
	s := NormalizeOIDCSettings(in, o.publicURL)
	// v0.10.1110 — yetki servisi anahtarı kimliğe bağlı değil: boş girdi
	// kayıtlıyı HER ZAMAN korur (clientSecret kalıbı, kimlik koşulu yok).
	if s.PermissionServiceKey == "" {
		s.PermissionServiceKey = prev.PermissionServiceKey
	}
	if s.ClientSecret == "" && prev.ClientSecret != "" {
		if s.IssuerURL == prev.IssuerURL && s.ClientID == prev.ClientID {
			s.ClientSecret = prev.ClientSecret
		} else if s.Enabled {
			return OIDCSnapshot{}, &OIDCSettingsError{"clientSecret",
				"Issuer ya da Client ID değişti — client secret yeniden girilmeli"}
		}
		// Kapalı + kimlik değişti: kayıtlı secret yeni kimliğe TAŞINMAZ (düşer).
	}
	if err := ValidateOIDCSettings(s, o.uiPolicy.allowInsecure); err != nil {
		return OIDCSnapshot{}, err
	}
	// N2 — yalnız çözülen sertifikalar saklanır (doğrulamadan geçti; hata olamaz).
	ca, err := canonicalOIDCCACert(s.TLSCACertPEM)
	if err != nil {
		return OIDCSnapshot{}, err
	}
	s.TLSCACertPEM = ca
	var cli *oidcClient
	if s.Enabled {
		t := s.tlsOptions()
		c, err := buildOIDCClient(ctx, s.toConfig(), o.uiPolicy, t, o.cachedDiscovery(s.IssuerURL, t))
		if err != nil {
			return OIDCSnapshot{}, &OIDCDiscoveryError{Err: err}
		}
		c.applyEmailOptions(s)
		cli = c
	}
	raw, err := json.Marshal(s.stored())
	if err != nil {
		return OIDCSnapshot{}, err
	}
	if err := o.store.PutSetting(ctx, OIDCSettingsKey, raw); err != nil {
		return OIDCSnapshot{}, err
	}
	warnInsecureOIDCTLS(s, log.Printf)
	warnTrustUnverifiedEmail(s, log.Printf)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.live.Store(cli)
	o.st = oidcState{loaded: true, source: OIDCSourceSettings, eff: s, lastRaw: raw}
	return o.snapshotLocked(), nil
}

// TestDiscovery — "Bağlantıyı test et": issuer doğrulaması + keşif (Settings
// ağ sınırıyla); hiçbir şey yazmaz, canlı istemciye dokunmaz. Keşif secret
// kullanmaz. v0.10.1112: formun KAYDEDİLMEMİŞ TLS ayarıyla (özel CA /
// skip-verify) — operatör kaydetmeden sınar.
func (o *OIDCService) TestDiscovery(ctx context.Context, in OIDCSettings) (*OIDCDiscovery, error) {
	s := NormalizeOIDCSettings(in, "")
	if err := ValidateOIDCIssuer(s.IssuerURL, o.uiPolicy.allowInsecure); err != nil {
		return nil, err
	}
	cli, err := newOIDCHTTPClient(o.uiPolicy, s.tlsOptions()) // CA çözülemezse OIDCSettingsError
	if err != nil {
		return nil, err
	}
	d, err := discoverOIDC(ctx, cli, s.IssuerURL, o.uiPolicy)
	if err != nil {
		return nil, err
	}
	if len(d.ScopesSupported) > 0 {
		sup := map[string]bool{}
		for _, sc := range d.ScopesSupported {
			sup[sc] = true
		}
		for _, sc := range s.Scopes {
			if !sup[sc] {
				d.UnsupportedScopes = append(d.UnsupportedScopes, sc)
			}
		}
	}
	return d, nil
}

// Snapshot — GET cevabı (secret yok). Ağ beklemez (mu keşif sırasında tutulmaz).
func (o *OIDCService) Snapshot() OIDCSnapshot {
	if o == nil {
		return OIDCSnapshot{Source: OIDCSourceConfig, Scopes: []string{}, AllowedDomains: []string{},
			PermissionTTLSeconds: PermissionTTLDefault, PermissionsClaim: PermissionClaimDefault,
			PermissionServiceAllowedCIDRs: []string{}, PermissionServiceTrustedProxies: []string{},
			UsernameClaim: UsernameClaimDefault}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.snapshotLocked()
}

func (o *OIDCService) snapshotLocked() OIDCSnapshot {
	s := NormalizeOIDCSettings(o.st.eff, o.publicURL)
	src := o.st.source
	if src == "" {
		src = OIDCSourceConfig
	}
	return OIDCSnapshot{
		Enabled:                         s.Enabled,
		IssuerURL:                       s.IssuerURL,
		ClientID:                        s.ClientID,
		ClientSecretStored:              s.ClientSecret != "",
		RedirectURL:                     s.RedirectURL,
		DefaultRedirectURL:              DefaultOIDCRedirectURL(o.publicURL),
		Scopes:                          append([]string{}, s.Scopes...),
		DisplayName:                     s.DisplayName,
		DefaultRole:                     s.DefaultRole,
		AllowedDomains:                  append([]string{}, s.AllowedDomains...),
		PermissionServiceEnabled:        s.PermissionServiceEnabled,
		PermissionServiceKeySet:         s.PermissionServiceKey != "",
		PermissionTTLSeconds:            s.PermissionTTLSeconds,
		PermissionsClaim:                s.PermissionsClaim,
		RoleFromClaim:                   s.RoleFromClaim,
		PermissionServiceAllowNoKey:     s.PermissionServiceAllowNoKey,
		PermissionServiceAllowedCIDRs:   append([]string{}, s.PermissionServiceAllowedCIDRs...),
		PermissionServiceTrustedProxies: append([]string{}, s.PermissionServiceTrustedProxies...),
		TLSCACertPEM:                    s.TLSCACertPEM,
		TLSInsecureSkipVerify:           s.TLSInsecureSkipVerify,
		TrustUnverifiedEmail:            s.TrustUnverifiedEmail,
		UsernameFallback:                s.UsernameFallback,
		UsernameClaim:                   s.UsernameClaim,
		UsernameFallbackAllowAdmin:      s.UsernameFallbackAllowAdmin,
		Source:                          src,
		Active:                          o.live.Load() != nil,
		LastError:                       o.st.lastErr,
	}
}
