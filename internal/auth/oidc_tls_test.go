package auth

// oidc_tls_test.go — v0.10.1112 (operatör: "oidc bağlantısının tls
// kontrolünü kapatma seçeneği de olsun sertifikaya takılıyor.")
//
// NE ÇİVİLİYOR:
//   - Doğrulama: özel CA ≤64 KB, ≥1 CERTIFICATE bloğu, çoklu sertifika (arada
//     yorum satırı) geçer; çöp / bozuk blok / özel anahtar / başka blok türü /
//     tavan aşımı 400 (alan tlsCACertPEM) — SSO kapalıyken DE (inceleme F1).
//   - Kanonik saklama (inceleme N2): yalnız çözülen sertifikalar yeniden
//     kodlanır; yorumlar ve pem.Decode'un atladığı zırhlı PGP özel anahtar
//     bloğu bloba/GET'e girmez.
//   - Gidiş-dönüş: blob ↔ snapshot ↔ peer; String() PEM basmaz.
//   - DAVRANIŞ (httptest TLS, kurum içi CA ile imzalı sertifika, dış ağ YOK):
//     varsayılan ayarla keşif DÜŞER; özel CA ile keşif + kayıt + giriş (token
//     ucu, id_token JWKS, v0.10.1110 access token JWKS) GEÇER; skip-verify ile
//     de geçer; skip-verify ne düz http'ye ne loopback'e izin verir; TLS ayarı
//     değişince önbellekli keşif kullanılmaz (yeni güven gerçekten sınanır).
//   - Bozuk blob: çözülemeyen CA SSO'yu sessizce düşürmez — CA'sız uygulanır,
//     lastError yazılır; WARN ayar yüklemesi başına bir kez.
//   - KAYNAK ÇİVİSİ: internal/auth'ta OIDC HTTP istemcisi yalnız
//     newOIDCHTTPClient'ta kurulur; her ClientContext / discoverOIDC çağrısı
//     o kurucunun istemcisini taşır.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/config"
)

// ── Test PKI: kurum içi CA + onunla imzalı IdP sertifikası ──────────────────

type testPKI struct {
	caPEM     string
	caKeyPEM  string
	serverTLS tls.Certificate
}

func newTestPKI(t *testing.T, cn string, ip string) testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "idp.example.test"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"idp.example.test"}, IPAddresses: []net.IP{net.ParseIP(ip)},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(caKey)
	return testPKI{
		caPEM:     string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		caKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
		serverTLS: tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey},
	}
}

// ── Saf: doğrulama tablosu ───────────────────────────────────────────────────

func TestValidateOIDCCACertPEM(t *testing.T) {
	a := newTestPKI(t, "Kurum İç CA 1", "10.20.30.41")
	b := newTestPKI(t, "Kurum İç CA 2", "10.20.30.41")
	bundle := "# Subject: CN=Kurum İç CA 1\n" + a.caPEM + "\n# Subject: CN=Kurum İç CA 2\n" + b.caPEM
	garbageBlock := "-----BEGIN CERTIFICATE-----\n" + strings.Repeat("QUJD", 16) + "\n-----END CERTIFICATE-----\n"
	pubKey := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte{1, 2, 3}}))
	cases := []struct {
		name string
		pem  string
		want string // "" = geçerli; dolu = hata metninde geçen parça
	}{
		{"boş = özel CA yok", "", ""},
		{"tek sertifika", a.caPEM, ""},
		{"çoklu sertifika + yorum satırları", bundle, ""},
		{"CRLF satır sonları", strings.ReplaceAll(a.caPEM, "\n", "\r\n"), ""},
		{"çöp metin", "merhaba dünya", "PEM sertifikası bulunamadı"},
		{"CERTIFICATE bloğu çözülemiyor", garbageBlock, "1. sertifika çözülemedi"},
		{"ikinci blok bozuk", a.caPEM + garbageBlock, "2. sertifika çözülemedi"},
		{"yalnız özel anahtar", a.caKeyPEM, "özel anahtar"},
		{"sertifika + özel anahtar", a.caPEM + a.caKeyPEM, "özel anahtar"},
		{"bozuk kodlu özel anahtar da", a.caPEM + "-----BEGIN PRIVATE KEY-----\n!!\n-----END PRIVATE KEY-----\n", "özel anahtar"},
		{"başka blok türü", pubKey, "yalnız CERTIFICATE"},
		{"64 KB üstü", a.caPEM + "\n#" + strings.Repeat("x", OIDCCACertPEMMax), "çok büyük"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NormalizeOIDCSettings(validInput("https://idp.example.test"), testPublicURL)
			s.TLSCACertPEM = normalizePEM(c.pem)
			err := ValidateOIDCSettings(s, false)
			if c.want == "" {
				if err != nil {
					t.Fatalf("beklenmeyen hata: %v", err)
				}
				return
			}
			var ve *OIDCSettingsError
			if !errors.As(err, &ve) || ve.Field != "tlsCACertPEM" || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("tlsCACertPEM hatası (%q) bekleniyordu, gelen %v", c.want, err)
			}
			for _, body := range []string{a.caPEM, a.caKeyPEM} {
				if line := strings.Split(body, "\n")[1]; strings.Contains(err.Error(), line[:20]) {
					t.Fatalf("hata metni PEM içeriği taşıyor: %v", err)
				}
			}
		})
	}
	// Güvenlik incelemesi F1: SSO kapalıyken de CA denetlenir (tavan + özel
	// anahtar + çözüm) — yoksa enabled:false gövdesiyle özel anahtar bloba
	// yazılıp GET'te yankılanırdı. CA'sız kapatma (çöp issuer) yine geçer.
	for name, bad := range map[string]string{
		"çöp": "çöp", "özel anahtar": a.caKeyPEM, "64 KB üstü": a.caPEM + "\n#" + strings.Repeat("x", OIDCCACertPEMMax),
	} {
		var ve *OIDCSettingsError
		if err := ValidateOIDCSettings(OIDCSettings{Enabled: false, TLSCACertPEM: bad}, false); !errors.As(err, &ve) || ve.Field != "tlsCACertPEM" {
			t.Fatalf("enabled:false + %s kabul edildi: %v", name, err)
		}
	}
	if err := ValidateOIDCSettings(OIDCSettings{Enabled: false, IssuerURL: "ftp://x", DefaultRole: "root", TLSCACertPEM: a.caPEM}, false); err != nil {
		t.Fatalf("enabled:false + geçerli CA reddedildi: %v", err)
	}
	// Çoklu sertifika gerçekten iki sertifika verir; özet ikisini de sayar.
	if certs, err := parseOIDCCACertPEM(normalizePEM(bundle)); err != nil || len(certs) != 2 {
		t.Fatalf("paket: %d sertifika, %v", len(certs), err)
	}
	sum := OIDCCACertSummary(normalizePEM(bundle))
	if len(sum) != 2 || !strings.HasPrefix(sum[0], "Kurum İç CA 1 · sha256:") || !strings.Contains(sum[1], "Kurum İç CA 2") {
		t.Fatalf("özet: %v", sum)
	}
	if got := OIDCCACertSummary(""); got == nil || len(got) != 0 {
		t.Fatalf("boş özet nil ya da dolu: %#v", got)
	}
	if got := OIDCCACertSummary("çöp"); len(got) != 1 || !strings.HasPrefix(got[0], "geçersiz PEM") {
		t.Fatalf("geçersiz özet: %v", got)
	}
}

// pgpPrivateBlock — zırhlı PGP özel anahtar bloğu: sağlama satırı (=…)
// base64'ü bozduğu için pem.Decode ATLAR, "PRIVATE KEY-----" dize denetimine
// de takılmaz (son satır "PRIVATE KEY BLOCK-----"). Kanonik saklama düşürür.
const pgpPrivateBlock = "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nlQOYBGVsZWN0AQgAsecretkeymaterial\n=Ab1c\n-----END PGP PRIVATE KEY BLOCK-----"

// Güvenlik incelemesi N2 (kanonik saklama) + F1 (kapalıyken de denetim,
// kayıt ve yükleme yollarında) + N5 (önceki snapshot kayıtla aynı kilitte).
func TestOIDCCACertCanonicalStorageAndDisabledValidation(t *testing.T) {
	a := newTestPKI(t, "Kurum İç CA 1", tlsIdPIP)
	b := newTestPKI(t, "Kurum İç CA 2", tlsIdPIP)
	want := strings.TrimSpace(a.caPEM) + "\n" + strings.TrimSpace(b.caPEM)
	withJunk := "# Subject: CN=Kurum İç CA 1\n" + a.caPEM + "\nnot: bu satır da düşer\n" + pgpPrivateBlock + "\n" + b.caPEM
	if err := validateOIDCCACert(withJunk); err != nil {
		t.Fatalf("yorum + PGP bloğu doğrulamada (beklendiği gibi) geçmeliydi: %v", err)
	}
	got, err := canonicalOIDCCACert(normalizePEM(withJunk))
	if err != nil || got != want {
		t.Fatalf("kanonik biçim:\n%q\nbeklenen\n%q (%v)", got, want, err)
	}
	if again, _ := canonicalOIDCCACert(got); again != got || normalizePEM(got) != got {
		t.Fatal("kanonik biçim idempotent değil")
	}

	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	o := newDevService(t, st)
	in := validInput(idp.URL)
	in.TLSCACertPEM = a.caPEM + "\n" + pgpPrivateBlock
	before := o.Snapshot()
	prev, snap, err := o.SaveSettingsWithPrev(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if prev.TLSCACertPEM != before.TLSCACertPEM || prev.Active != before.Active || prev.Source != before.Source {
		t.Fatalf("prev kayıttan önceki snapshot değil: %+v", prev)
	}
	stored := st.storedSettings(t).TLSCACertPEM
	for name, v := range map[string]string{"blob": stored, "snapshot": snap.TLSCACertPEM, "canlı": o.client().tls.caPEM} {
		if v != strings.TrimSpace(a.caPEM) || strings.Contains(v, "PGP") || strings.Contains(v, "secretkeymaterial") {
			t.Fatalf("%s yalnız sertifikayı taşımıyor: %q", name, v)
		}
	}

	// F1 — kapalı kayıtta özel anahtar 400, depo değişmez; geçerli CA kanonik saklanır.
	puts := st.puts
	off := OIDCSettings{Enabled: false, TLSCACertPEM: a.caKeyPEM}
	var ve *OIDCSettingsError
	if _, err := o.SaveSettings(context.Background(), off); !errors.As(err, &ve) || ve.Field != "tlsCACertPEM" {
		t.Fatalf("enabled:false + özel anahtar kaydedildi: %v", err)
	}
	if st.puts != puts || strings.Contains(string(st.raw()), "PRIVATE") {
		t.Fatal("reddedilen kapalı kayıt depoya yazıldı")
	}
	off.TLSCACertPEM = "# yorum\n" + b.caPEM
	if snap, err := o.SaveSettings(context.Background(), off); err != nil || snap.TLSCACertPEM != strings.TrimSpace(b.caPEM) {
		t.Fatalf("kapalı kayıtta CA kanonik değil: %q %v", snap.TLSCACertPEM, err)
	}

	// F1 — depodaki KAPALI blobda özel anahtar: yüklemede düşer, GET'te yok, lastError.
	bad, _ := json.Marshal(oidcStored{Enabled: false, TLSCACertPEM: a.caKeyPEM})
	st2 := newFakeOIDCStore()
	st2.rows[OIDCSettingsKey] = bad
	o2 := NewOIDCService(devBoot, testPublicURL, st2)
	_ = o2.LoadPersisted(context.Background())
	if s2 := o2.Snapshot(); s2.TLSCACertPEM != "" || !strings.Contains(s2.LastError, "özel anahtar") {
		t.Fatalf("kapalı blobdaki özel anahtar GET'te ya da lastError yok: %+v", s2)
	}
}

func TestOIDCTLSConfigBuilder(t *testing.T) {
	p := newTestPKI(t, "Kurum İç CA", "10.20.30.41")
	c, err := oidcTLSConfig(oidcTLSOptions{})
	if err != nil || c.InsecureSkipVerify || c.RootCAs != nil || c.MinVersion != tls.VersionTLS12 {
		t.Fatalf("varsayılan: %+v %v", c, err)
	}
	c, err = oidcTLSConfig(oidcTLSOptions{caPEM: p.caPEM})
	if err != nil || c.RootCAs == nil || c.InsecureSkipVerify {
		t.Fatalf("özel CA: %v", err)
	}
	c, err = oidcTLSConfig(oidcTLSOptions{skipVerify: true})
	if err != nil || !c.InsecureSkipVerify {
		t.Fatalf("skip-verify: %v", err)
	}
	if _, err := oidcTLSConfig(oidcTLSOptions{caPEM: "çöp"}); err == nil {
		t.Fatal("çözülemeyen CA ile istemci kuruldu")
	}
}

// ── Gidiş-dönüş + String ─────────────────────────────────────────────────────

func TestOIDCTLSSettingsRoundTrip(t *testing.T) {
	idp := newFakeIdP(t) // http + devBoot: TLS alanları burada yalnız taşınır
	p := newTestPKI(t, "Kurum İç CA", "10.20.30.41")
	st := newFakeOIDCStore()
	o := newDevService(t, st)
	in := validInput(idp.URL)
	in.TLSCACertPEM = "\r\n" + strings.ReplaceAll(p.caPEM, "\n", "\r\n") + "\r\n"
	in.TLSInsecureSkipVerify = true
	snap, err := o.SaveSettings(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(p.caPEM)
	if snap.TLSCACertPEM != want || !snap.TLSInsecureSkipVerify {
		t.Fatalf("snapshot TLS alanları: %q %v", snap.TLSCACertPEM, snap.TLSInsecureSkipVerify)
	}
	if stored := st.storedSettings(t); stored.TLSCACertPEM != want || !stored.TLSInsecureSkipVerify {
		t.Fatalf("blob TLS alanları: %+v", stored.settings())
	}
	b, _ := json.Marshal(snap)
	if !strings.Contains(string(b), `"tlsInsecureSkipVerify":true`) || !strings.Contains(string(b), `"tlsCACertPEM":"-----BEGIN CERTIFICATE-----`) {
		t.Fatalf("GET gövdesi TLS alanlarını taşımıyor: %s", b)
	}
	peer := newDevService(t, st)
	if ps := peer.Snapshot(); ps.TLSCACertPEM != want || !ps.TLSInsecureSkipVerify || !ps.Active {
		t.Fatalf("peer TLS alanlarını almadı: %+v", ps)
	}
	if peer.client().tls != (oidcTLSOptions{caPEM: want, skipVerify: true}) {
		t.Fatalf("canlı istemci TLS ayarı: %+v", peer.client().tls)
	}
	for _, out := range []string{in.String(), fmt.Sprintf("%v", in), fmt.Sprintf("%#v", in)} {
		if strings.Contains(out, "BEGIN CERTIFICATE") || !strings.Contains(out, "tlsSkipVerify:true") {
			t.Fatalf("String PEM basıyor ya da bayrak yok: %s", out)
		}
	}
	// Alanlar boşaltılınca boşalır (omitempty blob, snapshot "" / false).
	in.ClientSecret, in.TLSCACertPEM, in.TLSInsecureSkipVerify = "", "", false
	if snap, err = o.SaveSettings(context.Background(), in); err != nil || snap.TLSCACertPEM != "" || snap.TLSInsecureSkipVerify {
		t.Fatalf("temizleme: %+v %v", snap, err)
	}
	if strings.Contains(string(st.raw()), "tlsCACertPEM") || strings.Contains(string(st.raw()), "tlsInsecureSkipVerify") {
		t.Fatalf("boş TLS alanı bloba yazıldı: %s", st.raw())
	}
}

// ── Davranış: kurum içi CA ile imzalı httptest TLS IdP ───────────────────────

const tlsIdPIP = "10.20.30.41" // özel ağ (izinli); dial test dikişiyle httptest'e

// tlsIdP — sertifikası p'nin CA'sıyla imzalı, URL'i özel IP'li sahte IdP.
// Keşif belgesindeki TÜM uçlar (token, JWKS) da o IP'de; dial her şeyi
// 127.0.0.1'deki sunucuya yönlendirir (denetimden SONRA).
func tlsIdP(t *testing.T, p testPKI) *fakeIdP {
	t.Helper()
	idp := newFakeIdPServe(t, func(h http.Handler) *httptest.Server {
		s := httptest.NewUnstartedServer(h)
		s.TLS = &tls.Config{Certificates: []tls.Certificate{p.serverTLS}}
		s.StartTLS()
		return s
	})
	real := strings.TrimPrefix(idp.URL, "https://")
	port := real[strings.LastIndex(real, ":")+1:]
	idp.URL = "https://" + tlsIdPIP + ":" + port
	oidcTestDialTarget = func(string) string { return real }
	t.Cleanup(func() { oidcTestDialTarget = nil })
	return idp
}

func TestOIDCTLSBehaviourCustomCAAndSkipVerify(t *testing.T) {
	p := newTestPKI(t, "Kurum İç CA", tlsIdPIP)
	idp := tlsIdP(t, p)
	other := newTestPKI(t, "Başka CA", tlsIdPIP)
	ctx := context.Background()
	strict := func() *OIDCService { // Settings kaynağı, varsayılan ağ sınırı (https + dial koruması)
		o := NewOIDCService(config.OIDCConfig{}, testPublicURL, newFakeOIDCStore())
		if err := o.LoadPersisted(ctx); err != nil {
			t.Fatal(err)
		}
		return o
	}
	login := func(t *testing.T, o *OIDCService) {
		t.Helper()
		lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		idp.set(func(f *fakeIdP) {
			f.accessClaims = map[string]any{"permissions": []string{PermissionEditor}}
		})
		cl, err := o.Exchange(lctx, "code", "verifier", "")
		if err != nil {
			t.Fatalf("giriş (token + JWKS) TLS'te düştü: %v (%s)", err, OIDCLoginErrorClass(err))
		}
		// v0.10.1110 access token yolu: JWT imzası aynı istemciyle çekilen JWKS'le doğrulandı.
		if cl.Email != "user@example.test" || cl.ClaimRole != RoleEditor || cl.ClaimRoleSource != "access_token" {
			t.Fatalf("claim'ler: %+v", cl)
		}
	}
	withTLS := func(ca string, skip bool) OIDCSettings {
		in := validInput(idp.URL)
		in.TLSCACertPEM, in.TLSInsecureSkipVerify, in.RoleFromClaim = ca, skip, true
		return in
	}

	t.Run("varsayılan: kurum içi CA bilinmiyor → keşif düşer, kayıt reddedilir", func(t *testing.T) {
		o := strict()
		if _, err := o.TestDiscovery(ctx, withTLS("", false)); netClass(err) != "transport" || err.Error() != oidcUnreachableMsg {
			t.Fatalf("doğrulanamayan sertifika kabul edildi: %v (%s)", err, netClass(err))
		}
		if _, err := o.SaveSettings(ctx, withTLS("", false)); err == nil {
			t.Fatal("doğrulanamayan IdP'yle kayıt geçti")
		}
		if _, err := o.TestDiscovery(ctx, withTLS(other.caPEM, false)); netClass(err) != "transport" {
			t.Fatalf("yanlış CA kabul edildi: %v", err)
		}
	})

	t.Run("özel CA: test + kayıt + giriş (token, JWKS, access token) geçer", func(t *testing.T) {
		o := strict()
		d, err := o.TestDiscovery(ctx, withTLS(p.caPEM, false))
		if err != nil || d.Issuer != idp.URL || d.JWKSURI != idp.URL+"/jwks" {
			t.Fatalf("özel CA ile keşif: %v", err)
		}
		if _, err := o.SaveSettings(ctx, withTLS(other.caPEM+"\n"+p.caPEM, false)); err != nil { // çoklu: 2. sertifika imzalıyor
			t.Fatalf("özel CA ile kayıt: %v", err)
		}
		login(t, o)
	})

	t.Run("skip-verify: test + kayıt + giriş geçer", func(t *testing.T) {
		o := strict()
		if _, err := o.TestDiscovery(ctx, withTLS("", true)); err != nil {
			t.Fatalf("skip-verify keşif: %v", err)
		}
		if _, err := o.SaveSettings(ctx, withTLS("", true)); err != nil {
			t.Fatalf("skip-verify kayıt: %v", err)
		}
		login(t, o)
		// TLS ayarı değişti (skip kapandı, CA yok): önbellekli keşif KULLANILMAZ,
		// yeni güven sınanır → kayıt reddedilir, canlı istemci değişmez.
		in := withTLS("", false)
		in.ClientSecret = ""
		var de *OIDCDiscoveryError
		if _, err := o.SaveSettings(ctx, in); !errors.As(err, &de) {
			t.Fatalf("TLS değişimi önbellekli keşifle geçti: %v", err)
		}
		if o.client().tls != (oidcTLSOptions{skipVerify: true}) {
			t.Fatal("reddedilen kayıt canlı istemciyi değiştirdi")
		}
	})

	t.Run("skip-verify https kuralını ve dial korumasını GEVŞETMEZ", func(t *testing.T) {
		o := strict()
		port := idp.URL[strings.LastIndex(idp.URL, ":")+1:]
		plainHTTP := withTLS("", true)
		plainHTTP.IssuerURL = "http://" + tlsIdPIP + ":" + port
		before := idp.hits.Load()
		if _, err := o.TestDiscovery(ctx, plainHTTP); err == nil || !strings.Contains(err.Error(), "https") {
			t.Fatalf("skip-verify düz http'ye izin verdi: %v", err)
		}
		var ve *OIDCSettingsError
		if _, err := o.SaveSettings(ctx, plainHTTP); !errors.As(err, &ve) || ve.Field != "issuerUrl" {
			t.Fatalf("skip-verify ile http issuer kaydedildi: %v", err)
		}
		for _, iss := range []string{"https://127.0.0.1:" + port, "https://169.254.169.254"} {
			in := withTLS("", true)
			in.IssuerURL = iss
			if _, err := o.TestDiscovery(ctx, in); netClass(err) != "dial_blocked" {
				t.Errorf("skip-verify ile %s engellenmedi: %v (%s)", iss, err, netClass(err))
			}
		}
		if idp.hits.Load() != before {
			t.Fatal("reddedilen hedefe istek ulaştı")
		}
	})

	t.Run("test ucu çözülemeyen CA'yı açık hatayla reddeder, ağa çıkmaz", func(t *testing.T) {
		before := idp.hits.Load()
		_, err := strict().TestDiscovery(ctx, withTLS(p.caKeyPEM, false))
		var ve *OIDCSettingsError
		if !errors.As(err, &ve) || ve.Field != "tlsCACertPEM" || idp.hits.Load() != before {
			t.Fatalf("özel anahtar test ucunda: %v", err)
		}
	})
}

// ── Bozuk blob + WARN ────────────────────────────────────────────────────────

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestLoadPersistedBadCAKeepsSSOUpWithoutCA(t *testing.T) {
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	blob := NormalizeOIDCSettings(validInput(idp.URL), testPublicURL)
	blob.TLSCACertPEM = "-----BEGIN CERTIFICATE-----\nbozuk\n-----END CERTIFICATE-----"
	raw, _ := json.Marshal(blob.stored())
	st.rows[OIDCSettingsKey] = raw
	buf := captureLog(t)
	o := NewOIDCService(devBoot, testPublicURL, st)
	if err := o.LoadPersisted(context.Background()); err != nil {
		t.Fatalf("bozuk CA SSO'yu düşürdü: %v", err)
	}
	snap := o.Snapshot()
	if !o.Enabled() || !snap.Active || snap.TLSCACertPEM != "" || !strings.Contains(snap.LastError, "özel CA sertifikası geçersiz") {
		t.Fatalf("bozuk CA: SSO açık + CA'sız + lastError bekleniyordu: %+v", snap)
	}
	if !strings.Contains(buf.String(), "özel CA sertifikası geçersiz") {
		t.Fatalf("log satırı yok: %s", buf.String())
	}

	// CA'sız bağlanamayan (kurum içi CA'lı) IdP'de: SSO kapalı, lastError iki nedeni de taşır.
	p := newTestPKI(t, "Kurum İç CA", tlsIdPIP)
	tidp := tlsIdP(t, p)
	blob2 := NormalizeOIDCSettings(validInput(tidp.URL), testPublicURL)
	blob2.TLSCACertPEM = p.caKeyPEM
	raw2, _ := json.Marshal(blob2.stored())
	st2 := newFakeOIDCStore()
	st2.rows[OIDCSettingsKey] = raw2
	o2 := NewOIDCService(config.OIDCConfig{}, testPublicURL, st2)
	if err := o2.LoadPersisted(context.Background()); err == nil || o2.Enabled() {
		t.Fatal("CA'sız doğrulanamayan IdP etkin oldu")
	}
	if le := o2.Snapshot().LastError; !strings.Contains(le, "özel anahtar") || !strings.Contains(le, oidcUnreachableMsg) {
		t.Fatalf("lastError iki nedeni taşımıyor: %q", le)
	}
}

func TestInsecureSkipVerifyWarnsOncePerSettingsLoad(t *testing.T) {
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	podA, podB := newDevService(t, st), newDevService(t, st)
	buf := captureLog(t)
	warns := func() int { return strings.Count(buf.String(), "TLS sertifika doğrulaması KAPALI") }
	in := validInput(idp.URL)
	in.TLSInsecureSkipVerify = true
	if _, err := podA.SaveSettings(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if warns() != 1 {
		t.Fatalf("PUT'ta WARN %d kez", warns())
	}
	for i := 0; i < 3; i++ { // 30 s yenileme: blob değişmedi → sessiz
		_ = podA.LoadPersisted(context.Background())
	}
	if err := podB.LoadPersisted(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = podB.LoadPersisted(context.Background())
	if warns() != 2 {
		t.Fatalf("WARN ayar yüklemesi başına bir kez değil: %d\n%s", warns(), buf.String())
	}
	in.ClientSecret, in.TLSInsecureSkipVerify = "", false
	if _, err := podA.SaveSettings(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	_ = podB.LoadPersisted(context.Background())
	if warns() != 2 {
		t.Fatalf("kapalıyken WARN: %d", warns())
	}
}

// ── Kaynak çivisi: tek taşıma kurucusu ───────────────────────────────────────

// Her OIDC HTTP yolu newOIDCHTTPClient'tan geçmeli: başka bir http.Client /
// Transport / TLS yapılandırması kurulmaz, varsayılan istemci kullanılmaz,
// go-oidc'nin ağa kendi çıkan yardımcıları (NewProvider keşfi,
// NewRemoteKeySet) çağrılmaz, her ClientContext ve discoverOIDC çağrısı
// kurucunun ürettiği istemciyi taşır.
func TestOIDCHTTPPathsUseSharedTransportBuilder(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	type hit struct{ file, fn, what string }
	var hits []hit
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			builderCalled := false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "newOIDCHTTPClient" {
						builderCalled = true
					}
				}
				return true
			})
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				add := func(what string) { hits = append(hits, hit{f, fd.Name.Name, what}) }
				switch x := n.(type) {
				case *ast.CompositeLit:
					if sel, ok := x.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Client" {
						if pk, ok := sel.X.(*ast.Ident); ok && pk.Name == "http" {
							add("http.Client{}")
						}
					}
					if id, ok := x.Type.(*ast.Ident); ok && id.Name == "oidcClient" {
						add("oidcClient{}")
					}
				case *ast.SelectorExpr:
					if pk, ok := x.X.(*ast.Ident); ok {
						switch pk.Name + "." + x.Sel.Name {
						case "http.DefaultClient", "http.DefaultTransport", "http.Get", "http.Post", "http.Head",
							"oidc.NewProvider", "oidc.NewRemoteKeySet", "oauth2.NewClient":
							add(pk.Name + "." + x.Sel.Name)
						}
					}
					if x.Sel.Name == "TLSClientConfig" {
						add("TLSClientConfig")
					}
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					var name string
					if ok {
						if pk, ok := sel.X.(*ast.Ident); ok {
							name = pk.Name + "." + sel.Sel.Name
						}
					} else if id, ok := x.Fun.(*ast.Ident); ok {
						name = id.Name
					}
					switch name {
					case "oidc.ClientContext":
						arg := ""
						if len(x.Args) == 2 {
							switch a := x.Args[1].(type) {
							case *ast.Ident:
								arg = a.Name
							case *ast.SelectorExpr:
								if r, ok := a.X.(*ast.Ident); ok {
									arg = r.Name + "." + a.Sel.Name
								}
							}
						}
						// c.httpCli: oidcClient alanı (yalnız buildOIDCClient doldurur);
						// httpCli: aynı fonksiyonda kurucudan.
						if arg == "c.httpCli" || (arg == "httpCli" && builderCalled) {
							add("ClientContext(ok)")
						} else {
							add("ClientContext(" + arg + ")")
						}
					case "discoverOIDC":
						if builderCalled {
							add("discoverOIDC(ok)")
						} else {
							add("discoverOIDC(no-builder)")
						}
					}
				}
				return true
			})
		}
	}
	seen := map[string]bool{}
	for _, h := range hits {
		k := h.file + "/" + h.fn + "/" + h.what
		seen[k] = true
		switch {
		case h.what == "ClientContext(ok)" || h.what == "discoverOIDC(ok)":
			// kurucunun istemcisi — her yerde serbest
		case h.file == "oidc.go" && h.fn == "newOIDCHTTPClient" &&
			(h.what == "http.Client{}" || h.what == "http.DefaultTransport" || h.what == "TLSClientConfig"):
			// kurucunun kendisi
		case h.file == "oidc.go" && h.fn == "buildOIDCClient" && h.what == "oidcClient{}":
			// c.httpCli'yi dolduran TEK yer (httpCli kurucudan)
		default:
			t.Errorf("OIDC HTTP yolu ortak kurucudan geçmiyor: %s", k)
		}
	}
	// Kurucunun kendisi ve üç ana yol (keşif, giriş, test) gerçekten görüldü.
	for _, must := range []string{
		"oidc.go/newOIDCHTTPClient/http.Client{}", "oidc.go/newOIDCHTTPClient/TLSClientConfig",
		"oidc.go/buildOIDCClient/oidcClient{}",
		"oidc.go/Exchange/ClientContext(ok)", "oidc.go/buildOIDCClient/ClientContext(ok)",
		"oidc.go/buildOIDCClient/discoverOIDC(ok)", "oidc_settings.go/TestDiscovery/discoverOIDC(ok)",
	} {
		if !seen[must] {
			t.Errorf("beklenen yol bulunamadı (tarama bozuk mu?): %s", must)
		}
	}
	// Kurucu TLS'i oidcTLSConfig'ten alır.
	src, _ := os.ReadFile("oidc.go")
	if !strings.Contains(string(src), "tlsCfg, err := oidcTLSConfig(t)") || !strings.Contains(string(src), "tr.TLSClientConfig = tlsCfg") {
		t.Error("newOIDCHTTPClient TLS yapılandırmasını oidcTLSConfig'ten almıyor")
	}
}
