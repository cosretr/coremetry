package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/cilcenk/coremetry/internal/config"
)

// OIDCService is the optional SSO layer.
//
// v0.10.1067 (operatör: "Settings'ten yönetebilsem Helm'e göre daha iyi
// olur") — servis artık bir TUTUCU: main her zaman dolu bir *OIDCService
// kurar, canlı istemci (keşif sonucu + oauth2 config + sınırlı HTTP
// istemcisi) atomik pointer'da durur ve Settings PUT'u / 30 s yenileme /
// peer sinyali onu restart'sız değiştirir. nil istemci = SSO kapalı;
// api.go'daki çağrı yerleri (Enabled → AuthURL/Exchange) imza değişmeden
// canlı istemciyi okur. Ayar kaynağı ve öncelik kuralı oidc_settings.go
// başlığında.
type OIDCService struct {
	live atomic.Pointer[oidcClient] // nil = SSO kapalı

	// applyMu Load/Save'i sıralar ve ağ (keşif) süresince tutulur; mu yalnız
	// st'yi korur ve ASLA ağ beklerken tutulmaz — Snapshot keşfi beklemez.
	applyMu   sync.Mutex
	mu        sync.Mutex
	boot      config.OIDCConfig
	publicURL string
	store     OIDCSettingsStore
	uiPolicy  oidcNetPolicy // Settings kaynağı + test ucu
	st        oidcState     // mu altında
}

// oidcClient — bir yapılandırmanın keşfedilmiş, değişmez hâli. Takas
// bütün olarak yapılır: yarım güncellenmiş istemci görülmez.
type oidcClient struct {
	cfg      config.OIDCConfig
	disc     *OIDCDiscovery // aynı issuer'la yeniden kurulumda ağsız kullanılır
	httpCli  *http.Client   // keşif + JWKS + token değişimi — hepsi sınırlı
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
}

func (o *OIDCService) client() *oidcClient {
	if o == nil {
		return nil
	}
	return o.live.Load()
}

// Enabled is true when a live, discovered configuration exists. Safe on a
// nil receiver.
func (o *OIDCService) Enabled() bool { return o.client() != nil }

func (o *OIDCService) DisplayName() string {
	if c := o.client(); c != nil {
		return c.cfg.DisplayName
	}
	return ""
}

func (o *OIDCService) DefaultRole() string {
	if c := o.client(); c != nil {
		return c.cfg.DefaultRole
	}
	return ""
}

// AuthURL builds the IdP redirect URL with PKCE + nonce + state.
func (o *OIDCService) AuthURL(state, nonce, codeChallenge string) string {
	c := o.client()
	if c == nil {
		return "/login/?error=" + url.QueryEscape("oidc not configured")
	}
	return c.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// OIDCClaims is the subset of id_token claims we care about.
type OIDCClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Subject       string `json:"-"`
	Nonce         string `json:"-"`
}

// OIDCLoginError — giriş akışı hatası. v0.10.1067: token ucu (oauth2
// RetrieveError) ve JWKS (go-oidc) hata metinlerine IdP cevap GÖVDESİNİ
// koyuyor; IdP adresleri Settings'ten geldiği için o gövde kullanıcıya ya da
// loga ulaşırsa giriş akışı bir iç-ağ okuma kanalına dönüşür. Error() yalnız
// sabit sınıfı döner; alttaki hata Unwrap ile kodda kalır, metne girmez.
type OIDCLoginError struct {
	Class string
	err   error
}

func (e *OIDCLoginError) Error() string { return "oidc login failed: " + e.Class }
func (e *OIDCLoginError) Unwrap() error { return e.err }

func loginErr(class string, err error) error { return &OIDCLoginError{Class: class, err: err} }

// OIDCLoginErrorClass — log için sabit sınıf ("unknown" tipsiz hata).
func OIDCLoginErrorClass(err error) string {
	var le *OIDCLoginError
	if errors.As(err, &le) {
		return le.Class
	}
	return "unknown"
}

// Exchange completes the auth code flow: token exchange, id_token verify,
// nonce check, claim extraction, email_verified + domain whitelist.
func (o *OIDCService) Exchange(ctx context.Context, code, codeVerifier, expectedNonce string) (*OIDCClaims, error) {
	c := o.client()
	if c == nil {
		return nil, loginErr("not_configured", nil)
	}
	// Token değişimi de sınırlı istemciyle (süre, yönlendirme, dial koruması,
	// gövde tavanı) — varsayılan istemciyle DEĞİL.
	ctx = oidc.ClientContext(ctx, c.httpCli)
	tok, err := c.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, loginErr("token_exchange", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return nil, loginErr("id_token_missing", nil)
	}
	idTok, err := c.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, loginErr("id_token_verify", err)
	}
	if expectedNonce != "" && idTok.Nonce != expectedNonce {
		return nil, loginErr("nonce_mismatch", nil)
	}
	var raw struct {
		Email         string          `json:"email"`
		EmailVerified json.RawMessage `json:"email_verified"`
	}
	if err := idTok.Claims(&raw); err != nil {
		return nil, loginErr("claims_decode", err)
	}
	verified, present := parseEmailVerified(raw.EmailVerified)
	// v0.10.1067 güvenlik sıkılaştırması: claim VARSA ve false ise giriş yok
	// (doğrulanmamış e-posta başkasının hesabına bağlanamasın). Claim
	// yoksa (bazı IdP'ler hiç göndermiyor) eski davranış.
	if present && !verified {
		return nil, loginErr("email_unverified", nil)
	}
	cl := &OIDCClaims{Email: raw.Email, EmailVerified: verified, Subject: idTok.Subject, Nonce: idTok.Nonce}
	if cl.Email == "" {
		return nil, loginErr("email_missing", nil)
	}
	if !c.allowEmail(cl.Email) {
		return nil, loginErr("email_domain_denied", nil)
	}
	return cl, nil
}

// parseEmailVerified — bool ya da "true"/"false" dizgisi (bazı IdP'ler). SAF.
func parseEmailVerified(raw json.RawMessage) (verified, present bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return false, false
	}
	switch strings.ToLower(strings.Trim(s, `"`)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// AllowEmail enforces the optional domain whitelist. Empty list = allow all.
func (o *OIDCService) AllowEmail(email string) bool {
	c := o.client()
	if c == nil {
		return false
	}
	return c.allowEmail(email)
}

func (c *oidcClient) allowEmail(email string) bool {
	if len(c.cfg.AllowedDomains) == 0 {
		return true
	}
	dom := domainOf(email)
	if dom == "" {
		return false
	}
	for _, d := range c.cfg.AllowedDomains {
		if strings.EqualFold(strings.TrimSpace(d), dom) {
			return true
		}
	}
	return false
}

func domainOf(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// ── Ağ sınırı (discovery / JWKS / token) ─────────────────────────────────────
//
// v0.10.1067 — IdP adresleri Settings'ten (yani bir HTTP isteğinden) geldiği
// an, sunucunun bu adreslere yaptığı her istek bir SSRF yüzeyidir: keşif,
// keşif belgesinin gösterdiği JWKS ve token uçları dahil. Kurallar:
//   - https zorunlu; http yalnız config.yaml `allow_insecure_issuer` ile (dev);
//   - dial anında loopback / link-local (169.254/16 — bulut metadata adresi
//     dahil — ve fe80::/10) / belirtilmemiş / multicast / IPv4-eşlemeli IPv6
//     adres reddedilir; DNS adı önce çözülür, çözülen adreslerin HEPSİ temizse
//     bağlantı o IP'ye sabitlenir (DNS rebinding kapalı);
//   - özel ağ (RFC1918, fc00::/7) SERBEST: operatörün IdP'si banka ağı içinde
//     ve "Settings'ten yönet" Helm anahtarı gerektirmemeli (operatör kararı).
//     Kalan "özel bir https ana makinesi ayakta mı" bilgisini yalnız oturum
//     admin'i öğrenebilir; test ucu audit'lenir, cevap tek genel cümle;
//   - ≤10 s, ≤3 yönlendirme (her biri aynı kurallar), cevap gövdesi ≤1 MiB;
//   - hata metninde gövde, durum kodu ya da ayrıntı YOK (tek genel cümle).
// config.yaml kaynaklı OIDC bu sınırın DIŞINDA (operatör yazdı; eski davranış).
// go-oidc'nin NewProvider'ı kullanılmıyor: 200 dışı cevapta gövdenin tamamını
// hataya koyuyor. Sağlayıcı oidc.ProviderConfig'ten ağsız kurulur.

const (
	oidcHTTPTimeout       = 10 * time.Second
	oidcMaxDiscoveryBytes = 1 << 20
	oidcMaxRedirects      = 3
	oidcMaxFieldLen       = 512
	oidcMaxScopesListed   = 50
)

// oidcNetPolicy — bir yapılandırma kaynağının ağ sınırı.
type oidcNetPolicy struct {
	unrestricted  bool // config.yaml kaynağı
	allowInsecure bool // config.yaml allow_insecure_issuer (dev)
}

func newUIPolicy(c config.OIDCConfig) oidcNetPolicy {
	return oidcNetPolicy{allowInsecure: c.AllowInsecureIssuer}
}

var configPolicy = oidcNetPolicy{unrestricted: true}

var errOIDCDialBlocked = errors.New("oidc: hedef adres engelli")

// oidcTestTLSConfig — YALNIZ test dikişi (httptest TLS sertifikasına güven);
// üretimde nil, sistem kök sertifikaları.
var oidcTestTLSConfig *tls.Config

// oidcTestDialTarget — YALNIZ test dikişi: denetimden SONRA gerçek dial
// adresini değiştirir (ör. özel IP literali → httptest 127.0.0.1). Üretimde nil.
var oidcTestDialTarget func(addr string) string

// blockedIP — dial korumasının reddettiği adres sınıfları. Özel ağ (RFC1918,
// fc00::/7) BİLEREK serbest — bkz. bölüm başlığı. SAF.
func blockedIP(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	if ip.To4() != nil && strings.Contains(host, ":") {
		return true // IPv4-eşlemeli IPv6 (::ffff:a.b.c.d)
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
}

// guardedDial — ad çözümü + denetim + çözülen IP'ye sabitlenmiş bağlantı.
// Çözülen adreslerden BİRİ bile engelliyse reddedilir.
func guardedDial(p oidcNetPolicy, base *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	target := func(a string) string {
		if oidcTestDialTarget != nil {
			return oidcTestDialTarget(a)
		}
		return a
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if p.unrestricted || p.allowInsecure {
			return base.DialContext(ctx, network, target(addr))
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, errOIDCDialBlocked
		}
		var ips []string
		if net.ParseIP(host) != nil {
			ips = []string{host} // literal: ham yazımla denetle (eşlemeli IPv6)
		} else {
			res, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, r := range res {
				ips = append(ips, r.IP.String())
			}
		}
		if len(ips) == 0 {
			return nil, errOIDCDialBlocked
		}
		for _, ip := range ips {
			if blockedIP(ip) {
				return nil, errOIDCDialBlocked
			}
		}
		var lastErr error
		for _, ip := range ips {
			c, err := base.DialContext(ctx, network, target(net.JoinHostPort(ip, port)))
			if err == nil {
				return c, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// checkURL — şema kuralı (+ kimlik bilgisi yok). SAF.
func (p oidcNetPolicy) checkURL(u *url.URL) error {
	if u == nil || u.Host == "" {
		return errors.New("tam bir URL olmalı (https://…)")
	}
	if u.User != nil {
		return errors.New("URL kullanıcı adı/parola içeremez")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if p.unrestricted || p.allowInsecure {
			return nil
		}
	}
	return errors.New("https olmalı")
}

// limitedBody — cevap gövdesi tavanı (go-oidc JWKS ve oauth2 token okuması
// sınırsız/ham okuyor; tavan taşıma katmanında).
type limitedBody struct {
	io.Reader
	io.Closer
}

type limitedBodyRT struct{ base http.RoundTripper }

func (l limitedBodyRT) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := l.base.RoundTrip(r)
	if err == nil && resp.Body != nil {
		resp.Body = limitedBody{Reader: io.LimitReader(resp.Body, oidcMaxDiscoveryBytes+1), Closer: resp.Body}
	}
	return resp, err
}

// newOIDCHTTPClient — keşif + JWKS + token istemcisi.
func newOIDCHTTPClient(p oidcNetPolicy) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = guardedDial(p, &net.Dialer{Timeout: oidcHTTPTimeout})
	if oidcTestTLSConfig != nil {
		tr.TLSClientConfig = oidcTestTLSConfig
	}
	return &http.Client{
		Timeout:   oidcHTTPTimeout,
		Transport: limitedBodyRT{base: tr},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= oidcMaxRedirects {
				return errors.New("çok fazla yönlendirme")
			}
			return p.checkURL(req.URL)
		},
	}
}

// OIDCDiscovery — keşif belgesinden DIŞARI verilen alanlar (ham gövde değil).
type OIDCDiscovery struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorizationEndpoint"`
	TokenEndpoint         string   `json:"tokenEndpoint"`
	UserinfoEndpoint      string   `json:"userinfoEndpoint,omitempty"`
	JWKSURI               string   `json:"jwksUri"`
	ScopesSupported       []string `json:"scopesSupported,omitempty"`
	// UnsupportedScopes — istenen ama IdP'nin scopes_supported listesinde
	// olmayan kapsamlar (liste yayınlanmışsa). Uyarıdır, hata değil.
	UnsupportedScopes []string `json:"unsupportedScopes,omitempty"`
	signingAlgs       []string
}

// oidcNetError — keşif hatası. Error() TEK genel cümle (iç-ağ yoklaması
// için ayrım vermesin); Class yalnız kodda/testte.
type oidcNetError struct {
	Class string
	msg   string
}

func (e *oidcNetError) Error() string { return e.msg }

const oidcUnreachableMsg = "kimlik sağlayıcıya ulaşılamadı"

func netErr(class string) error { return &oidcNetError{Class: class, msg: oidcUnreachableMsg} }

// go-oidc'nin desteklediği imza algoritmaları (oidc.NewProvider aynı
// süzgeci uyguluyordu; ProviderConfig uygulamıyor).
var oidcSupportedAlgs = map[string]bool{
	oidc.RS256: true, oidc.RS384: true, oidc.RS512: true,
	oidc.ES256: true, oidc.ES384: true, oidc.ES512: true,
	oidc.PS256: true, oidc.PS384: true, oidc.PS512: true,
	oidc.EdDSA: true,
}

func clipField(s string) string {
	if len(s) > oidcMaxFieldLen {
		return s[:oidcMaxFieldLen] + "…"
	}
	return s
}

// checkDiscoveryURLs — keşif belgesinin uç noktaları: zorunlular dolu,
// hepsi politika şemasında, sorgu/parça yok.
func checkDiscoveryURLs(d *OIDCDiscovery, p oidcNetPolicy) error {
	bad := &oidcNetError{Class: "endpoint_invalid", msg: "keşif belgesindeki uç noktalar geçersiz (https, sorgusuz olmalı)"}
	for i, v := range []string{d.AuthorizationEndpoint, d.TokenEndpoint, d.JWKSURI, d.UserinfoEndpoint} {
		if v == "" {
			if i == 3 { // userinfo isteğe bağlı
				continue
			}
			return netErr("missing_fields")
		}
		u, err := url.Parse(v)
		if err != nil || p.checkURL(u) != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return bad
		}
	}
	return nil
}

// discoverOIDC — issuer'ın /.well-known/openid-configuration belgesini
// okur ve doğrular.
func discoverOIDC(ctx context.Context, cli *http.Client, issuer string, p oidcNetPolicy) (*OIDCDiscovery, error) {
	u, err := url.Parse(issuer)
	if err != nil || p.checkURL(u) != nil {
		return nil, &oidcNetError{Class: "issuer_invalid", msg: "issuer URL https olmalı"}
	}
	ctx, cancel := context.WithTimeout(ctx, oidcHTTPTimeout)
	defer cancel()
	wellKnown := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return nil, netErr("request")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		if errors.Is(err, errOIDCDialBlocked) {
			return nil, netErr("dial_blocked")
		}
		return nil, netErr("transport")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, netErr("status") // durum kodu ve gövde BİLEREK yok
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || len(body) > oidcMaxDiscoveryBytes {
		return nil, netErr("too_large")
	}
	var doc struct {
		Issuer      string   `json:"issuer"`
		AuthURL     string   `json:"authorization_endpoint"`
		TokenURL    string   `json:"token_endpoint"`
		UserInfoURL string   `json:"userinfo_endpoint"`
		JWKSURL     string   `json:"jwks_uri"`
		Scopes      []string `json:"scopes_supported"`
		Algs        []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, netErr("bad_json")
	}
	if doc.Issuer != issuer {
		// Belgedeki issuer YANKILANMAZ.
		return nil, &oidcNetError{Class: "issuer_mismatch", msg: "issuer uyuşmuyor"}
	}
	out := &OIDCDiscovery{
		Issuer:                clipField(doc.Issuer),
		AuthorizationEndpoint: clipField(doc.AuthURL),
		TokenEndpoint:         clipField(doc.TokenURL),
		UserinfoEndpoint:      clipField(doc.UserInfoURL),
		JWKSURI:               clipField(doc.JWKSURL),
	}
	if err := checkDiscoveryURLs(out, p); err != nil {
		return nil, err
	}
	for _, sc := range doc.Scopes {
		if len(out.ScopesSupported) >= oidcMaxScopesListed {
			break
		}
		if sc != "" && len(sc) <= 64 {
			out.ScopesSupported = append(out.ScopesSupported, sc)
		}
	}
	for _, a := range doc.Algs {
		if oidcSupportedAlgs[a] {
			out.signingAlgs = append(out.signingAlgs, a)
		}
	}
	return out, nil
}

// buildOIDCClient — keşif (ya da aynı issuer'ın önbellekli keşfi, ağsız) +
// doğrulayıcı + oauth2 config. Eksik zorunlu alan ağa çıkmadan reddedilir.
// JWKS ilk doğrulamada, token değişimi girişte — ikisi de aynı sınırlı
// istemciyle.
func buildOIDCClient(ctx context.Context, cfg config.OIDCConfig, p oidcNetPolicy, cached *OIDCDiscovery) (*oidcClient, error) {
	missing := []string{}
	if cfg.IssuerURL == "" {
		missing = append(missing, "issuer_url")
	}
	if cfg.ClientID == "" {
		missing = append(missing, "client_id")
	}
	if cfg.ClientSecret == "" {
		missing = append(missing, "client_secret")
	}
	if cfg.RedirectURL == "" {
		missing = append(missing, "redirect_url")
	}
	if len(missing) > 0 {
		return nil, &oidcMissingError{fields: missing}
	}
	httpCli := newOIDCHTTPClient(p)
	d := cached
	if d != nil && d.Issuer == cfg.IssuerURL {
		// Önbellekli keşif başka bir politikayla alınmış olabilir: uç
		// noktalar BU politikaya göre yeniden denetlenir.
		if err := checkDiscoveryURLs(d, p); err != nil {
			return nil, err
		}
	} else {
		var err error
		if d, err = discoverOIDC(ctx, httpCli, cfg.IssuerURL, p); err != nil {
			return nil, err
		}
	}
	pc := &oidc.ProviderConfig{
		IssuerURL:   d.Issuer,
		AuthURL:     d.AuthorizationEndpoint,
		TokenURL:    d.TokenEndpoint,
		UserInfoURL: d.UserinfoEndpoint,
		JWKSURL:     d.JWKSURI,
		Algorithms:  d.signingAlgs,
	}
	prov := pc.NewProvider(oidc.ClientContext(context.Background(), httpCli))
	return &oidcClient{
		cfg:      cfg,
		disc:     d,
		httpCli:  httpCli,
		provider: prov,
		verifier: prov.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     prov.Endpoint(),
			Scopes:       cfg.Scopes,
		},
	}, nil
}

// oidcMissingError — kalıcı yapılandırma eksiği; yenileme döngüsü bunu
// her tikte yeniden denemez (geçici keşif hatasının aksine).
type oidcMissingError struct{ fields []string }

func (e *oidcMissingError) Error() string {
	return "oidc enabled but missing: " + strings.Join(e.fields, ", ")
}

// ── PKCE + state helpers ─────────────────────────────────────────────────────

// RandomURLToken returns a base64url-encoded random string of nBytes
// entropy — used for state, nonce, and PKCE code_verifier.
func RandomURLToken(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// PKCEChallenge returns base64url(SHA256(verifier)) — the S256 method
// from RFC 7636.
func PKCEChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
