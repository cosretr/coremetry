package auth

// oidc_settings_test.go — v0.10.1067 (OIDC Settings'ten yönetilir) +
// güvenlik incelemesi düzeltmeleri.
//
// NE ÇİVİLİYOR: normalize/doğrulama tablosu (viewer üstü rol alan adı ister,
// ASCII alan adı, rune sınırı); secret hiçbir snapshot'ta / %v / %#v / JSON'da
// görünmez; boş secret kayıtlıyı YALNIZ issuer + client id aynıysa korur;
// keşfi bozuk enabled:true reddedilir, enabled:false her zaman kaydedilir;
// canlı takas; öncelik (blob > config.yaml); yenileme değişim tespitli,
// issuer aynıyken ağsız yeniden kurulum (alan adı peer'da hemen), uygulanamayan
// yapılandırmada active:false; depo okuma hatasında SSO kapalı (config.yaml'a
// düşmez). AĞ SINIRI: http/loopback issuer, https→http yönlendirme, loopback /
// link-local / metadata / eşlemeli adres dial'da reddedilir, ÖZEL IP serbest;
// keşif hataları tek genel cümle; token ucu / JWKS gövdesi hata
// metnine girmez; email_verified=false reddedilir. Sahte IdP httptest
// (127.0.0.1); dış ağ YOK.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/config"
)

const testSecret = "s3cr3t-value-never-echo"

// devBoot — httptest (http://127.0.0.1) için config.yaml dev bayrağı.
var devBoot = config.OIDCConfig{AllowInsecureIssuer: true}

// ── Sahte depo + sahte IdP ───────────────────────────────────────────────────

type fakeOIDCStore struct {
	mu     sync.Mutex
	rows   map[string][]byte
	puts   int
	getErr error
}

func newFakeOIDCStore() *fakeOIDCStore { return &fakeOIDCStore{rows: map[string][]byte{}} }

func (f *fakeOIDCStore) GetSetting(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.rows[key], nil
}

func (f *fakeOIDCStore) PutSetting(_ context.Context, key string, v []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	f.rows[key] = append([]byte(nil), v...)
	return nil
}

func (f *fakeOIDCStore) raw() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[OIDCSettingsKey]
}

func (f *fakeOIDCStore) set(fn func(f *fakeOIDCStore)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeOIDCStore) storedSettings(t *testing.T) oidcStored {
	t.Helper()
	var s oidcStored
	if err := json.Unmarshal(f.raw(), &s); err != nil {
		t.Fatalf("blob çözülemedi: %v", err)
	}
	return s
}

type fakeIdP struct {
	*httptest.Server
	hits        atomic.Int32 // keşif istekleri
	key         *rsa.PrivateKey
	mu          sync.Mutex
	status      int    // keşif: 0 → 200
	issuer      string // "" → kendi URL'i
	redirect    string // doluysa keşif 302
	big         bool
	tokenStatus int            // 0 → id_token döner
	jwksStatus  int            // 0 → anahtar döner
	claims      map[string]any // id_token ek/ezen claim'ler
}

func newFakeIdPOpt(t *testing.T, useTLS bool) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: key}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		status, issuer, redirect, huge, tokSt, jwksSt := f.status, f.issuer, f.redirect, f.big, f.tokenStatus, f.jwksStatus
		extra := f.claims
		f.mu.Unlock()
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			f.hits.Add(1)
			if redirect != "" {
				http.Redirect(w, r, redirect, http.StatusFound)
				return
			}
			if status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("INTERNAL-BODY-MUST-NOT-LEAK"))
				return
			}
			if issuer == "" {
				issuer = f.URL
			}
			doc := map[string]any{
				"issuer": issuer, "authorization_endpoint": f.URL + "/authorize",
				"token_endpoint": f.URL + "/token", "userinfo_endpoint": f.URL + "/userinfo",
				"jwks_uri": f.URL + "/jwks", "scopes_supported": []string{"openid", "email", "profile"},
				"id_token_signing_alg_values_supported": []string{"RS256", "none"},
			}
			if huge {
				doc["padding"] = strings.Repeat("x", oidcMaxDiscoveryBytes+10)
			}
			_ = json.NewEncoder(w).Encode(doc)
		case "/token":
			if tokSt != 0 {
				w.WriteHeader(tokSt)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"TOKEN-BODY-LEAK"}`))
				return
			}
			claims := map[string]any{
				"iss": f.URL, "aud": "coremetry", "sub": "u-1", "email": "user@example.test",
				"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
			}
			for k, v := range extra {
				claims[k] = v
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at", "token_type": "Bearer", "expires_in": 300,
				"id_token": signTestJWT(t, f.key, claims),
			})
		case "/jwks":
			if jwksSt != 0 {
				w.WriteHeader(jwksSt)
				_, _ = w.Write([]byte("JWKS-BODY-LEAK"))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
				"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
				"n": b64u(f.key.N.Bytes()), "e": b64u(big.NewInt(int64(f.key.E)).Bytes()),
			}}})
		default:
			http.NotFound(w, r)
		}
	})
	if useTLS {
		f.Server = httptest.NewTLSServer(h)
	} else {
		f.Server = httptest.NewServer(h)
	}
	t.Cleanup(f.Close)
	return f
}

func newFakeIdP(t *testing.T) *fakeIdP { return newFakeIdPOpt(t, false) }

func (f *fakeIdP) set(fn func(f *fakeIdP)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func signTestJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	hdr := b64u([]byte(`{"alg":"RS256","kid":"k1","typ":"JWT"}`))
	pb, _ := json.Marshal(claims)
	signing := hdr + "." + b64u(pb)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + b64u(sig)
}

func validInput(issuer string) OIDCSettings {
	return OIDCSettings{
		Enabled: true, IssuerURL: issuer, ClientID: "coremetry", ClientSecret: testSecret,
		Scopes: []string{"openid", "email"}, DisplayName: "Kurumsal SSO", DefaultRole: "viewer",
	}
}

const testPublicURL = "https://apm.example.test"

func newDevService(t *testing.T, st *fakeOIDCStore) *OIDCService {
	t.Helper()
	o := NewOIDCService(devBoot, testPublicURL, st)
	if err := o.LoadPersisted(context.Background()); err != nil {
		t.Fatal(err)
	}
	return o
}

func netClass(err error) string {
	var ne *oidcNetError
	if errors.As(err, &ne) {
		return ne.Class
	}
	return ""
}

// ── Saf: normalize + doğrulama ───────────────────────────────────────────────

func TestNormalizeOIDCSettings(t *testing.T) {
	cases := []struct {
		name string
		in   OIDCSettings
		pub  string
		chk  func(t *testing.T, s OIDCSettings)
	}{
		{"varsayılanlar", OIDCSettings{}, testPublicURL, func(t *testing.T, s OIDCSettings) {
			if strings.Join(s.Scopes, ",") != "openid,email,profile" || s.DisplayName != "SSO" || s.DefaultRole != "viewer" {
				t.Fatalf("varsayılan yok: %+v", s)
			}
			if s.RedirectURL != "https://apm.example.test/api/auth/oidc/callback" {
				t.Fatalf("redirect varsayılanı: %q", s.RedirectURL)
			}
		}},
		{"public URL yoksa redirect boş kalır", OIDCSettings{}, "", func(t *testing.T, s OIDCSettings) {
			if s.RedirectURL != "" {
				t.Fatalf("redirect %q", s.RedirectURL)
			}
		}},
		{"public URL sondaki / tek", OIDCSettings{}, "https://apm.example.test/", func(t *testing.T, s OIDCSettings) {
			if s.RedirectURL != "https://apm.example.test/api/auth/oidc/callback" {
				t.Fatalf("redirect %q", s.RedirectURL)
			}
		}},
		{"alan adları küçük harf, @ atılır, tekil", OIDCSettings{AllowedDomains: []string{" Example.TEST ", "@corp.example.test", "example.test"}}, "", func(t *testing.T, s OIDCSettings) {
			if strings.Join(s.AllowedDomains, ",") != "example.test,corp.example.test" {
				t.Fatalf("domains %v", s.AllowedDomains)
			}
		}},
		{"kapsamlar tek dizgiden bölünür, harf korunur", OIDCSettings{Scopes: []string{"openid email,Groups", "email"}}, "", func(t *testing.T, s OIDCSettings) {
			if strings.Join(s.Scopes, ",") != "openid,email,Groups" {
				t.Fatalf("scopes %v", s.Scopes)
			}
		}},
		{"issuer sondaki / KORUNUR, kırpılır", OIDCSettings{IssuerURL: "  https://idp.example.test/  ", DefaultRole: " Editor "}, "", func(t *testing.T, s OIDCSettings) {
			if s.IssuerURL != "https://idp.example.test/" || s.DefaultRole != "editor" {
				t.Fatalf("%q %q", s.IssuerURL, s.DefaultRole)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.chk(t, NormalizeOIDCSettings(c.in, c.pub)) })
	}
}

func TestValidateOIDCSettings(t *testing.T) {
	base := func(mut func(*OIDCSettings)) OIDCSettings {
		s := NormalizeOIDCSettings(validInput("https://idp.example.test"), testPublicURL)
		mut(&s)
		return s
	}
	cases := []struct {
		name     string
		s        OIDCSettings
		insecure bool
		field    string // "" = geçerli
	}{
		{"geçerli", base(func(*OIDCSettings) {}), false, ""},
		{"kapalı her zaman geçer (çöp issuer)", OIDCSettings{Enabled: false, IssuerURL: "ftp://x", DefaultRole: "root"}, false, ""},
		{"issuer boş", base(func(s *OIDCSettings) { s.IssuerURL = "" }), false, "issuerUrl"},
		{"http issuer reddedilir", base(func(s *OIDCSettings) { s.IssuerURL = "http://idp.example.test" }), false, "issuerUrl"},
		{"http localhost da reddedilir (istisna yok)", base(func(s *OIDCSettings) { s.IssuerURL = "http://localhost:8080/realms/x" }), false, "issuerUrl"},
		{"http 127.0.0.1 reddedilir", base(func(s *OIDCSettings) { s.IssuerURL = "http://127.0.0.1:5556" }), false, "issuerUrl"},
		{"http yalnız config.yaml dev bayrağıyla", base(func(s *OIDCSettings) { s.IssuerURL = "http://127.0.0.1:5556" }), true, ""},
		{"issuer kimlik bilgili", base(func(s *OIDCSettings) { s.IssuerURL = "https://u:p@idp.example.test" }), false, "issuerUrl"},
		{"issuer sorgulu", base(func(s *OIDCSettings) { s.IssuerURL = "https://idp.example.test/?x=1" }), false, "issuerUrl"},
		{"client id yok", base(func(s *OIDCSettings) { s.ClientID = "" }), false, "clientId"},
		{"secret yok", base(func(s *OIDCSettings) { s.ClientSecret = "" }), false, "clientSecret"},
		{"redirect yok", base(func(s *OIDCSettings) { s.RedirectURL = "" }), false, "redirectUrl"},
		{"redirect göreli", base(func(s *OIDCSettings) { s.RedirectURL = "/api/auth/oidc/callback" }), false, "redirectUrl"},
		{"openid yok", base(func(s *OIDCSettings) { s.Scopes = []string{"email", "profile"} }), false, "scopes"},
		{"rol geçersiz", base(func(s *OIDCSettings) { s.DefaultRole = "root" }), false, "defaultRole"},
		{"admin rolü alan adısız reddedilir", base(func(s *OIDCSettings) { s.DefaultRole = "admin" }), false, "defaultRole"},
		{"editor rolü alan adısız reddedilir", base(func(s *OIDCSettings) { s.DefaultRole = "editor" }), false, "defaultRole"},
		{"editor rolü alan adıyla geçerli", base(func(s *OIDCSettings) { s.DefaultRole = "editor"; s.AllowedDomains = []string{"example.test"} }), false, ""},
		{"etiket 40 Türkçe harf geçer", base(func(s *OIDCSettings) { s.DisplayName = strings.Repeat("ş", 40) }), false, ""},
		{"etiket 41 harf", base(func(s *OIDCSettings) { s.DisplayName = strings.Repeat("ş", 41) }), false, "displayName"},
		{"alan adı noktasız", base(func(s *OIDCSettings) { s.AllowedDomains = []string{"localdomain"} }), false, "allowedDomains"},
		{"alan adı e-posta", base(func(s *OIDCSettings) { s.AllowedDomains = []string{"a@example.test"} }), false, "allowedDomains"},
		{"alan adı ASCII değil", base(func(s *OIDCSettings) { s.AllowedDomains = []string{"örnek.example.test"} }), false, "allowedDomains"},
		{"punycode geçer", base(func(s *OIDCSettings) { s.AllowedDomains = []string{"xn--rnek-zoa.example.test"} }), false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateOIDCSettings(c.s, c.insecure)
			if c.field == "" {
				if err != nil {
					t.Fatalf("beklenmeyen hata: %v", err)
				}
				return
			}
			var ve *OIDCSettingsError
			if !errors.As(err, &ve) || ve.Field != c.field {
				t.Fatalf("alan %q bekleniyordu, gelen %v", c.field, err)
			}
		})
	}
}

func TestOIDCSettingsSecretNotMarshalableOrPrintable(t *testing.T) {
	s := validInput("https://idp.example.test")
	b, _ := json.Marshal(s)
	for name, out := range map[string]string{
		"%v": fmt.Sprintf("%v", s), "%+v": fmt.Sprintf("%+v", s), "%#v": fmt.Sprintf("%#v", s),
		"String()": s.String(), "json": string(b),
	} {
		if strings.Contains(out, testSecret) {
			t.Errorf("%s secret basıyor: %s", name, out)
		}
	}
}

func TestBlockedIPAndParseEmailVerified(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": true, "0.0.0.0": true, "::1": true, "fe80::1": true, "fd00::1": false,
		"::ffff:10.0.0.1": true, "::ffff:8.8.8.8": true, "ff02::1": true, "not-an-ip": true,
		"8.8.8.8": false, "2001:4860:4860::8888": false,
	} {
		if got := blockedIP(host); got != want {
			t.Errorf("blockedIP(%s)=%v, beklenen %v", host, got, want)
		}
	}
	for raw, want := range map[string][2]bool{
		``: {false, false}, `null`: {false, false}, `true`: {true, true}, `false`: {false, true},
		`"false"`: {false, true}, `"TRUE"`: {true, true}, `1`: {false, false},
	} {
		v, p := parseEmailVerified(json.RawMessage(raw))
		if v != want[0] || p != want[1] {
			t.Errorf("parseEmailVerified(%s)=(%v,%v)", raw, v, p)
		}
	}
}

// ── Kaydet / secret / kapatma anahtarı ───────────────────────────────────────

func TestSaveSecretNeverEchoedAndEmptyPreserves(t *testing.T) {
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	o := newDevService(t, st)
	snap, err := o.SaveSettings(context.Background(), validInput(idp.URL))
	if err != nil {
		t.Fatalf("kayıt: %v", err)
	}
	b, _ := json.Marshal(snap)
	if strings.Contains(string(b), testSecret) || !snap.ClientSecretStored {
		t.Fatalf("snapshot secret sızdırıyor ya da stored yok: %s", b)
	}
	in := validInput(idp.URL)
	in.ClientSecret = ""
	in.DisplayName = "Yeni etiket"
	if _, err := o.SaveSettings(context.Background(), in); err != nil {
		t.Fatalf("boş secret'lı kayıt: %v", err)
	}
	if stored := st.storedSettings(t); stored.ClientSecret != testSecret || stored.DisplayName != "Yeni etiket" {
		t.Fatalf("secret korunmadı ya da etiket yazılmadı: %+v", stored.settings())
	}
	if o.client().oauth.ClientSecret != testSecret {
		t.Fatal("canlı istemci secret'ı kaybetti")
	}
}

func TestSaveRefusesSecretCarryOverOnIdentityChange(t *testing.T) {
	idp, other := newFakeIdP(t), newFakeIdP(t)
	st := newFakeOIDCStore()
	o := newDevService(t, st)
	if _, err := o.SaveSettings(context.Background(), validInput(idp.URL)); err != nil {
		t.Fatal(err)
	}
	puts := st.puts
	for name, mut := range map[string]func(*OIDCSettings){
		"issuer değişti":    func(s *OIDCSettings) { s.IssuerURL = other.URL },
		"client id değişti": func(s *OIDCSettings) { s.ClientID = "baska-istemci" },
	} {
		t.Run(name, func(t *testing.T) {
			in := validInput(idp.URL)
			in.ClientSecret = ""
			mut(&in)
			_, err := o.SaveSettings(context.Background(), in)
			var ve *OIDCSettingsError
			if !errors.As(err, &ve) || ve.Field != "clientSecret" || !strings.Contains(err.Error(), "yeniden girilmeli") {
				t.Fatalf("taşıma reddedilmedi: %v", err)
			}
			if st.puts != puts || other.hits.Load() != 0 {
				t.Fatal("reddedilen kayıt yazdı ya da yeni IdP'ye gitti")
			}
		})
	}
	// Kapalı + kimlik değişti: kaydedilir ama secret TAŞINMAZ.
	in := validInput(other.URL)
	in.Enabled, in.ClientSecret = false, ""
	if _, err := o.SaveSettings(context.Background(), in); err != nil {
		t.Fatalf("kapatma reddedildi: %v", err)
	}
	if st.storedSettings(t).ClientSecret != "" {
		t.Fatal("kapalı kayıtta secret yeni kimliğe taşındı")
	}
}

func TestSaveEmptySecretFallsBackToConfigYAMLSecret(t *testing.T) {
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	boot := config.OIDCConfig{Enabled: true, IssuerURL: idp.URL, ClientID: "coremetry",
		ClientSecret: testSecret, RedirectURL: testPublicURL + "/api/auth/oidc/callback",
		Scopes: []string{"openid", "email"}, DisplayName: "Helm SSO", DefaultRole: "viewer",
		AllowInsecureIssuer: true}
	o := NewOIDCService(boot, testPublicURL, st)
	if err := o.LoadPersisted(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap := o.Snapshot()
	if snap.Source != OIDCSourceConfig || !snap.ClientSecretStored || !snap.Active || snap.DisplayName != "Helm SSO" {
		t.Fatalf("config.yaml ön-dolumu yanlış: %+v", snap)
	}
	in := validInput(idp.URL)
	in.ClientSecret = ""
	if _, err := o.SaveSettings(context.Background(), in); err != nil {
		t.Fatalf("kayıt: %v", err)
	}
	if st.storedSettings(t).ClientSecret != testSecret {
		t.Fatal("config.yaml secret'ı bloba taşınmadı")
	}
}

func TestSaveRejectsBrokenDiscoveryButAcceptsDisabled(t *testing.T) {
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	o := newDevService(t, st)
	// Önbellek etkisini dışlamak için bozuk keşif BAŞKA bir issuer'la denenir.
	broken := newFakeIdP(t)
	if _, err := o.SaveSettings(context.Background(), validInput(idp.URL)); err != nil {
		t.Fatal(err)
	}
	goodRaw := append([]byte(nil), st.raw()...)
	puts := st.puts
	for _, tc := range []struct {
		name  string
		mut   func(f *fakeIdP)
		class string
		msg   string
	}{
		{"IdP 500", func(f *fakeIdP) { f.status = http.StatusInternalServerError }, "status", oidcUnreachableMsg},
		{"issuer uyuşmuyor", func(f *fakeIdP) { f.issuer = "https://other.example.test" }, "issuer_mismatch", "issuer uyuşmuyor"},
		{"belge çok büyük", func(f *fakeIdP) { f.big = true }, "too_large", oidcUnreachableMsg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broken.set(func(f *fakeIdP) { f.status, f.issuer, f.big = 0, "", false })
			broken.set(tc.mut)
			_, err := o.SaveSettings(context.Background(), validInput(broken.URL))
			var de *OIDCDiscoveryError
			if !errors.As(err, &de) || netClass(err) != tc.class {
				t.Fatalf("keşif hatası %s bekleniyordu: %v", tc.class, err)
			}
			// Tek genel cümle: gövde, durum kodu, belgedeki issuer YOK.
			if de.Err.Error() != tc.msg || strings.Contains(err.Error(), "other.example.test") {
				t.Fatalf("hata metni ayrıntı taşıyor: %v", err)
			}
			if st.puts != puts || string(st.raw()) != string(goodRaw) {
				t.Fatal("bozuk yapılandırma depoya yazıldı")
			}
			if !o.Enabled() || o.DisplayName() != "Kurumsal SSO" {
				t.Fatal("canlı istemci bozuk kayıtla değişti")
			}
		})
	}
	snap, err := o.SaveSettings(context.Background(), OIDCSettings{Enabled: false, IssuerURL: "ftp://garbage", DefaultRole: "root"})
	if err != nil {
		t.Fatalf("enabled:false reddedildi: %v", err)
	}
	if o.Enabled() || snap.Active || snap.Enabled {
		t.Fatal("kapatma canlıya geçmedi")
	}
}

// ── Canlı takas + öncelik + yenileme ─────────────────────────────────────────

func TestHotSwapWithoutRestart(t *testing.T) {
	a, b := newFakeIdP(t), newFakeIdP(t)
	o := newDevService(t, newFakeOIDCStore())
	if o.Enabled() {
		t.Fatal("başlangıçta kapalı olmalı")
	}
	if _, err := o.SaveSettings(context.Background(), validInput(a.URL)); err != nil {
		t.Fatal(err)
	}
	if u := o.AuthURL("st", "n", "c"); !strings.HasPrefix(u, a.URL+"/authorize?") {
		t.Fatalf("A'ya gitmedi: %s", u)
	}
	in := validInput(b.URL) // issuer değişti → secret yeniden girilir
	in.DisplayName = "B"
	if _, err := o.SaveSettings(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if u := o.AuthURL("st", "n", "c"); !strings.HasPrefix(u, b.URL+"/authorize?") || o.DisplayName() != "B" {
		t.Fatalf("B'ye takaslanmadı: %s", u)
	}
}

func TestPrecedenceBlobOverridesConfigYAML(t *testing.T) {
	cfgIdP, blobIdP := newFakeIdP(t), newFakeIdP(t)
	st := newFakeOIDCStore()
	blob := NormalizeOIDCSettings(validInput(blobIdP.URL), testPublicURL)
	blob.DisplayName = "Blob"
	raw, _ := json.Marshal(blob.stored())
	st.rows[OIDCSettingsKey] = raw
	boot := config.OIDCConfig{Enabled: true, IssuerURL: cfgIdP.URL, ClientID: "c", ClientSecret: "x",
		RedirectURL: testPublicURL + "/cb", Scopes: []string{"openid"}, DisplayName: "Config", AllowInsecureIssuer: true}
	o := NewOIDCService(boot, testPublicURL, st)
	if err := o.LoadPersisted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if o.DisplayName() != "Blob" || o.Snapshot().Source != OIDCSourceSettings || cfgIdP.hits.Load() != 0 {
		t.Fatalf("blob config.yaml'ın önüne geçmedi (display=%q, cfg keşif=%d)", o.DisplayName(), cfgIdP.hits.Load())
	}
	st.set(func(f *fakeOIDCStore) { delete(f.rows, OIDCSettingsKey) })
	if err := o.LoadPersisted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if o.DisplayName() != "Config" || o.Snapshot().Source != OIDCSourceConfig {
		t.Fatalf("config.yaml'a dönülmedi: %q", o.DisplayName())
	}
}

func TestRefreshChangeDetectionPeerDomainsAndStale(t *testing.T) {
	idp, idp2 := newFakeIdP(t), newFakeIdP(t)
	st := newFakeOIDCStore()
	podA, podB := newDevService(t, st), newDevService(t, st)
	in := validInput(idp.URL)
	in.AllowedDomains = []string{"example.test"}
	if _, err := podA.SaveSettings(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if err := podB.LoadPersisted(context.Background()); err != nil || !podB.Enabled() {
		t.Fatalf("peer yeni ayarı almadı: %v", err)
	}
	hits := idp.hits.Load()
	for i := 0; i < 3; i++ {
		_ = podB.LoadPersisted(context.Background())
	}
	if idp.hits.Load() != hits {
		t.Fatalf("değişmeyen blob için keşif koştu (%d → %d)", hits, idp.hits.Load())
	}
	// Yalnız alan adı değişir + IdP o an kapalı: peer AĞSIZ yeniden kurar.
	idp.set(func(f *fakeIdP) { f.status = http.StatusServiceUnavailable })
	in.ClientSecret, in.AllowedDomains = "", []string{"corp.example.test"}
	if _, err := podA.SaveSettings(context.Background(), in); err != nil {
		t.Fatalf("issuer aynıyken kayıt ağ gerektirdi: %v", err)
	}
	if err := podB.LoadPersisted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if podB.AllowEmail("u@example.test") || !podB.AllowEmail("u@corp.example.test") || idp.hits.Load() != hits {
		t.Fatal("peer eski alan adlarıyla kaldı ya da ağa çıktı")
	}
	// Issuer değişti ve peer keşfi başarısız: eski istemci sessizce SÜRMEZ.
	in2 := validInput(idp2.URL)
	in2.AllowedDomains = []string{"corp.example.test"}
	if _, err := podA.SaveSettings(context.Background(), in2); err != nil {
		t.Fatal(err)
	}
	idp2.set(func(f *fakeIdP) { f.status = http.StatusServiceUnavailable })
	if err := podB.LoadPersisted(context.Background()); err == nil {
		t.Fatal("peer keşif hatası görünmedi")
	}
	if snap := podB.Snapshot(); snap.Active || podB.Enabled() || snap.LastError == "" {
		t.Fatalf("uygulanamayan yapılandırmada active:true: %+v", snap)
	}
	idp2.set(func(f *fakeIdP) { f.status = 0 })
	if err := podB.LoadPersisted(context.Background()); err != nil || !podB.Enabled() {
		t.Fatalf("yenileme yeniden denemedi: %v", err)
	}
}

func TestBootReadErrorStartsDisabledNotConfigYAML(t *testing.T) {
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	st.getErr = errors.New("ch down")
	boot := config.OIDCConfig{Enabled: true, IssuerURL: idp.URL, ClientID: "c", ClientSecret: "x",
		RedirectURL: testPublicURL + "/cb", Scopes: []string{"openid"}, DisplayName: "Config", AllowInsecureIssuer: true}
	o := NewOIDCService(boot, testPublicURL, st)
	if err := o.LoadPersisted(context.Background()); err == nil || o.Enabled() || idp.hits.Load() != 0 {
		t.Fatal("okuma hatasında config.yaml'a düşüldü")
	}
	// Admin Settings'te kapatmıştı; depo dönünce o uygulanır.
	off, _ := json.Marshal(oidcStored{Enabled: false})
	st.set(func(f *fakeOIDCStore) { f.getErr = nil; f.rows[OIDCSettingsKey] = off })
	if err := o.LoadPersisted(context.Background()); err != nil || o.Enabled() || o.Snapshot().Source != OIDCSourceSettings {
		t.Fatalf("yeniden deneme yanlış: %v", err)
	}
	// Bozuk blob, yüklüyken: son-iyi ve kaynak korunur.
	st.set(func(f *fakeOIDCStore) { f.rows[OIDCSettingsKey] = []byte("{bozuk") })
	if err := o.LoadPersisted(context.Background()); err == nil || o.Snapshot().Source != OIDCSourceSettings {
		t.Fatal("bozuk blob son-iyiyi bozdu")
	}
}

func TestBootDiscoveryFailureRetriesOnRefresh(t *testing.T) {
	idp := newFakeIdP(t)
	idp.set(func(f *fakeIdP) { f.status = http.StatusServiceUnavailable })
	boot := config.OIDCConfig{Enabled: true, IssuerURL: idp.URL, ClientID: "c", ClientSecret: "x",
		RedirectURL: testPublicURL + "/cb", Scopes: []string{"openid"}, DisplayName: "SSO"}
	o := NewOIDCService(boot, testPublicURL, newFakeOIDCStore())
	if err := o.LoadPersisted(context.Background()); err == nil || o.Enabled() || o.Snapshot().LastError == "" {
		t.Fatal("IdP kapalıyken etkin olmamalı, hata görünmeli")
	}
	idp.set(func(f *fakeIdP) { f.status = 0 })
	if err := o.LoadPersisted(context.Background()); err != nil || !o.Enabled() {
		t.Fatalf("yenileme yeniden denemedi: %v", err)
	}
}

// ── Ağ sınırı ────────────────────────────────────────────────────────────────

// Özel ağ (banka içi IdP) SERBEST; loopback / link-local (metadata) /
// eşlemeli adres ve https dışı yönlendirme reddedilir. Özel IP'ye giden dial,
// denetimden SONRA test dikişiyle httptest TLS IdP'ye yönlendirilir.
func TestNetworkGuardPrivateAllowedLoopbackLinkLocalRefused(t *testing.T) {
	ctx := context.Background()
	strict := NewOIDCService(config.OIDCConfig{}, "", newFakeOIDCStore())
	plain := newFakeIdP(t)

	// 1) loopback http hedef (istisna yok).
	if _, err := strict.TestDiscovery(ctx, OIDCSettings{IssuerURL: plain.URL}); err == nil || plain.hits.Load() != 0 {
		t.Fatalf("http://127.0.0.1 kabul edildi: %v", err)
	}

	tlsIdP := newFakeIdPOpt(t, true)
	tc := tlsIdP.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tc.ServerName = "example.com" // httptest sertifikasının adı; istek host'u özel IP
	var mu sync.Mutex
	var dialed []string
	idpAddr := strings.TrimPrefix(tlsIdP.URL, "https://")
	oidcTestTLSConfig = tc
	oidcTestDialTarget = func(a string) string {
		mu.Lock()
		defer mu.Unlock()
		dialed = append(dialed, a)
		return idpAddr
	}
	t.Cleanup(func() { oidcTestTLSConfig, oidcTestDialTarget = nil, nil })
	port := tlsIdP.URL[strings.LastIndex(tlsIdP.URL, ":")+1:]

	// 2) özel IP literali → İZİNLİ, IdP'ye ulaşılır.
	priv := "https://10.20.30.40:" + port
	tlsIdP.set(func(f *fakeIdP) { f.issuer = priv })
	d, err := strict.TestDiscovery(ctx, OIDCSettings{IssuerURL: priv})
	if err != nil || d.Issuer != priv || tlsIdP.hits.Load() != 1 {
		t.Fatalf("özel IP'deki IdP'ye ulaşılamadı: %v", err)
	}
	mu.Lock()
	if len(dialed) == 0 || dialed[0] != "10.20.30.40:"+port {
		t.Fatalf("dial özel IP'ye sabitlenmedi: %v", dialed)
	}
	mu.Unlock()

	// 3) loopback literal / localhost adı / metadata / fe80 / eşlemeli → dial'da red, IdP'ye istek yok.
	for _, iss := range []string{
		"https://127.0.0.1:" + port, "https://localhost:" + port, "https://169.254.169.254",
		"https://[fe80::1]:" + port, "https://[::ffff:10.20.30.40]:" + port,
	} {
		before := tlsIdP.hits.Load()
		if _, err := strict.TestDiscovery(ctx, OIDCSettings{IssuerURL: iss}); netClass(err) != "dial_blocked" ||
			err.Error() != oidcUnreachableMsg {
			t.Errorf("%s reddedilmedi: %v (%s)", iss, err, netClass(err))
		}
		if tlsIdP.hits.Load() != before {
			t.Errorf("%s: engellenen hedefe istek ulaştı", iss)
		}
	}

	// 4) https (özel) → http://127.0.0.1 yönlendirmesi reddedilir; hedefe ulaşılmaz.
	tlsIdP.set(func(f *fakeIdP) { f.redirect = plain.URL + "/.well-known/openid-configuration" })
	before := plain.hits.Load()
	if _, err := strict.TestDiscovery(ctx, OIDCSettings{IssuerURL: priv}); err == nil || err.Error() != oidcUnreachableMsg {
		t.Fatalf("https dışı yönlendirme izlendi: %v", err)
	}
	if plain.hits.Load() != before {
		t.Fatal("http yönlendirme hedefine istek gitti")
	}
}

func TestDiscoveryRejectsEndpointQueryAndFragment(t *testing.T) {
	d := &OIDCDiscovery{AuthorizationEndpoint: "https://idp.example.test/auth", TokenEndpoint: "https://idp.example.test/token",
		JWKSURI: "https://idp.example.test/jwks"}
	p := oidcNetPolicy{}
	if err := checkDiscoveryURLs(d, p); err != nil {
		t.Fatalf("temiz uçlar reddedildi: %v", err)
	}
	for _, mut := range []func(*OIDCDiscovery){
		func(d *OIDCDiscovery) { d.TokenEndpoint += "?x=1" },
		func(d *OIDCDiscovery) { d.JWKSURI += "#f" },
		func(d *OIDCDiscovery) { d.AuthorizationEndpoint = "http://idp.example.test/auth" },
		func(d *OIDCDiscovery) { d.UserinfoEndpoint = "https://idp.example.test/ui?" },
	} {
		c := *d
		mut(&c)
		if err := checkDiscoveryURLs(&c, p); netClass(err) != "endpoint_invalid" {
			t.Errorf("geçersiz uç kabul edildi: %+v → %v", c, err)
		}
	}
}

// ── Giriş akışı: gövde sızıntısı + email_verified ────────────────────────────

func loginService(t *testing.T, idp *fakeIdP) *OIDCService {
	t.Helper()
	o := newDevService(t, newFakeOIDCStore())
	if _, err := o.SaveSettings(context.Background(), validInput(idp.URL)); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestExchangeNeverSurfacesTokenOrJWKSBody(t *testing.T) {
	idp := newFakeIdP(t)
	o := loginService(t, idp)
	idp.set(func(f *fakeIdP) { f.tokenStatus = http.StatusBadRequest })
	_, err := o.Exchange(context.Background(), "code", "verifier", "")
	if OIDCLoginErrorClass(err) != "token_exchange" || strings.Contains(err.Error(), "TOKEN-BODY-LEAK") ||
		strings.Contains(fmt.Sprintf("%v", err), "TOKEN-BODY-LEAK") {
		t.Fatalf("token gövdesi hata metnine girdi: %v", err)
	}
	idp.set(func(f *fakeIdP) { f.tokenStatus, f.jwksStatus = 0, http.StatusInternalServerError })
	_, err = o.Exchange(context.Background(), "code", "verifier", "")
	if OIDCLoginErrorClass(err) != "id_token_verify" || strings.Contains(err.Error(), "JWKS-BODY-LEAK") {
		t.Fatalf("JWKS gövdesi hata metnine girdi: %v", err)
	}
}

func TestExchangeEmailVerified(t *testing.T) {
	idp := newFakeIdP(t)
	o := loginService(t, idp)
	cases := []struct {
		name  string
		claim any // nil = yok
		class string
	}{
		{"claim yok → eski davranış", nil, ""},
		{"true", true, ""},
		{"false reddedilir", false, "email_unverified"},
		{`"false" dizgisi reddedilir`, "false", "email_unverified"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idp.set(func(f *fakeIdP) {
				f.claims = map[string]any{}
				if c.claim != nil {
					f.claims["email_verified"] = c.claim
				}
			})
			cl, err := o.Exchange(context.Background(), "code", "verifier", "")
			if c.class == "" {
				if err != nil || cl.Email != "user@example.test" {
					t.Fatalf("geçerli giriş reddedildi: %v", err)
				}
				return
			}
			if OIDCLoginErrorClass(err) != c.class {
				t.Fatalf("%s bekleniyordu: %v", c.class, err)
			}
		})
	}
}
