package api

// auth_oidc_tls_test.go — v0.10.1112 (operatör: "oidc bağlantısının tls
// kontrolünü kapatma seçeneği de olsun sertifikaya takılıyor.")
//
// NE ÇİVİLİYOR (HTTP sözleşmesi; TLS davranışı auth/oidc_tls_test.go'da):
// tlsCACertPEM + tlsInsecureSkipVerify PUT→GET gidiş-dönüşü (secret değil,
// aynen döner); audit eski→yeni (CA için PEM değil özet); çözülemeyen PEM /
// özel anahtar 400 ve yazmaz; ~64 KB CA gövde tavanına takılmaz; test ucu
// formun KAYDEDİLMEMİŞ TLS alanlarını kullanır.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

func testCAPEM(t *testing.T, cn string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}))
}

func (e *oidcTestEnv) tlsBody(secret, caPEM string, skip bool) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(e.body(secret)), &m)
	m["tlsCACertPEM"], m["tlsInsecureSkipVerify"] = caPEM, skip
	b, _ := json.Marshal(m)
	return string(b)
}

type tlsAuditDetails struct {
	CA struct {
		Old     []string `json:"old"`
		New     []string `json:"new"`
		Changed bool     `json:"changed"`
	} `json:"tlsCACert"`
	Skip struct {
		Old bool `json:"old"`
		New bool `json:"new"`
	} `json:"tlsInsecureSkipVerify"`
}

func lastTLSAudit(t *testing.T, e *oidcTestEnv) (tlsAuditDetails, string) {
	t.Helper()
	au := e.audits()
	if len(au) != 1 || au[0].Action != "settings.oidc.update" {
		t.Fatalf("audit yok/yanlış: %+v", au)
	}
	var d tlsAuditDetails
	if err := json.Unmarshal([]byte(au[0].Details), &d); err != nil {
		t.Fatalf("audit ayrıntısı çözülemedi: %v %s", err, au[0].Details)
	}
	return d, au[0].Details
}

func TestOIDCSettingsTLSFieldsRoundTripAndAudit(t *testing.T) {
	e := newOIDCTestEnv(t)
	ca, _ := testCAPEM(t, "Kurum İç CA")
	// 1) İlk kayıt: CA + skip açık.
	w := e.do(t, "PUT", "/api/settings/oidc", e.tlsBody(oidcTestSecret, ca, true), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	d, raw := lastTLSAudit(t, e)
	if len(d.CA.Old) != 0 || len(d.CA.New) != 1 || !strings.HasPrefix(d.CA.New[0], "Kurum İç CA · sha256:") || !d.CA.Changed ||
		d.Skip.Old || !d.Skip.New {
		t.Fatalf("audit eski→yeni yanlış: %s", raw)
	}
	if strings.Contains(raw, "BEGIN CERTIFICATE") || strings.Contains(raw, oidcTestSecret) {
		t.Fatalf("audit PEM ya da secret taşıyor: %s", raw)
	}
	// GET aynen döner (secret değil).
	g := e.do(t, "GET", "/api/settings/oidc", "", auth.RoleAdmin)
	var snap auth.OIDCSnapshot
	if err := json.Unmarshal(g.Body.Bytes(), &snap); err != nil || snap.TLSCACertPEM != strings.TrimSpace(ca) || !snap.TLSInsecureSkipVerify {
		t.Fatalf("GET TLS alanları: %v %s", err, g.Body)
	}
	// İnceleme N2: bloklar arası metin (yorum + zırhlı PGP özel anahtar bloğu)
	// saklanmaz; GET kanonik biçimi döner.
	pgp := "-----BEGIN PGP PRIVATE KEY BLOCK-----\n\nlQOYBGVsZWN0AQgAsecretkeymaterial\n=Ab1c\n-----END PGP PRIVATE KEY BLOCK-----"
	if w := e.do(t, "PUT", "/api/settings/oidc", e.tlsBody("", "# yorum\n"+ca+"\n"+pgp, true), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("PGP bloklu PUT %d %s", w.Code, w.Body)
	}
	_ = e.audits()
	g = e.do(t, "GET", "/api/settings/oidc", "", auth.RoleAdmin)
	if err := json.Unmarshal(g.Body.Bytes(), &snap); err != nil || snap.TLSCACertPEM != strings.TrimSpace(ca) ||
		strings.Contains(string(e.store.rows[auth.OIDCSettingsKey]), "PGP") || strings.Contains(g.Body.String(), "yorum") {
		t.Fatalf("kanonik saklama yok: %s", g.Body)
	}
	// 2) CA kaldırıldı, skip kapandı: eski→yeni ters yönde.
	if w := e.do(t, "PUT", "/api/settings/oidc", e.tlsBody("", "", false), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("ikinci PUT %d %s", w.Code, w.Body)
	}
	d, raw = lastTLSAudit(t, e)
	if len(d.CA.Old) != 1 || len(d.CA.New) != 0 || !d.CA.Changed || !d.Skip.Old || d.Skip.New {
		t.Fatalf("ikinci audit: %s", raw)
	}
	// 3) Değişmeyen alan: changed false, eski = yeni.
	if w := e.do(t, "PUT", "/api/settings/oidc", e.tlsBody("", "", false), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("üçüncü PUT %d %s", w.Code, w.Body)
	}
	if d, raw = lastTLSAudit(t, e); d.CA.Changed || d.Skip.Old || d.Skip.New {
		t.Fatalf("değişmeyen audit: %s", raw)
	}
}

func TestOIDCSettingsTLSRejectsBadPEMAndAllowsLargeBundle(t *testing.T) {
	e := newOIDCTestEnv(t)
	ca, key := testCAPEM(t, "Kurum İç CA")
	for name, bad := range map[string]string{
		"çöp":          "merhaba",
		"özel anahtar": ca + key,
		"64 KB üstü":   ca + "\n#" + strings.Repeat("x", auth.OIDCCACertPEMMax),
	} {
		w := e.do(t, "PUT", "/api/settings/oidc", e.tlsBody(oidcTestSecret, bad, false), auth.RoleAdmin)
		keyLine := strings.Split(key, "\n")[1][:20] // cevap anahtar gövdesini yankılamaz
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Özel CA sertifikası") ||
			strings.Contains(w.Body.String(), keyLine) {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// Güvenlik incelemesi F1: SSO kapalıyken de — özel anahtar bloba/GET'e girmez.
	disabled, _ := json.Marshal(map[string]any{"enabled": false, "tlsCACertPEM": key})
	if w := e.do(t, "PUT", "/api/settings/oidc", string(disabled), auth.RoleAdmin); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "özel anahtar") {
		t.Fatalf("enabled:false + özel anahtar: %d %s", w.Code, w.Body)
	}
	if e.store.puts != 0 || len(e.audits()) != 0 {
		t.Fatal("reddedilen TLS PUT'u yan etki bıraktı")
	}
	// ~64 KB'lık geçerli paket gövde tavanına takılmaz; audit özeti sınırlı.
	n := auth.OIDCCACertPEMMax / (len(ca) + 1)
	bundle := strings.TrimSpace(strings.Repeat(ca+"\n", n))
	if len(bundle) < auth.OIDCCACertPEMMax-2*len(ca) {
		t.Fatalf("paket küçük: %d", len(bundle))
	}
	w := e.do(t, "PUT", "/api/settings/oidc", e.tlsBody(oidcTestSecret, bundle, false), auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("~64 KB paket: %d %.300s", w.Code, w.Body)
	}
	d, raw := lastTLSAudit(t, e)
	if len(d.CA.New) != 11 || !strings.Contains(d.CA.New[10], "sertifika daha") || len(raw) > 4096 {
		t.Fatalf("büyük paket audit özeti sınırsız ya da yanlış (%d bayt): %.500s", len(raw), raw)
	}
}

// "Bağlantıyı test et" formun kaydedilmemiş TLS alanlarını kullanır: kayıtlı
// ayar yokken gövdedeki çözülemeyen CA test cevabında görünür.
func TestOIDCSettingsTestEndpointUsesUnsavedTLSFields(t *testing.T) {
	e := newOIDCTestEnv(t)
	_, key := testCAPEM(t, "Kurum İç CA")
	w := e.do(t, "POST", "/api/settings/oidc/test", e.tlsBody("", key, false), auth.RoleAdmin)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":false`) ||
		!strings.Contains(w.Body.String(), "özel anahtar") {
		t.Fatalf("test ucu formun CA alanını kullanmadı: %d %s", w.Code, w.Body)
	}
	ca, _ := testCAPEM(t, "Kurum İç CA")
	w = e.do(t, "POST", "/api/settings/oidc/test", e.tlsBody("", ca, true), auth.RoleAdmin)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("geçerli TLS alanlarıyla test: %d %s", w.Code, w.Body)
	}
	if e.store.puts != 0 {
		t.Fatal("test ucu yazdı")
	}
	// İnceleme N3: yoklama izi hangi TLS güveniyle koşulduğunu taşır (PEM değil).
	au := e.audits()
	if len(au) != 2 {
		t.Fatalf("test audit sayısı: %+v", au)
	}
	var d struct {
		Skip     *bool `json:"tlsInsecureSkipVerify"`
		CustomCA *bool `json:"customCA"`
	}
	for i, want := range [][2]bool{{false, true}, {true, true}} {
		if err := json.Unmarshal([]byte(au[i].Details), &d); err != nil || d.Skip == nil || d.CustomCA == nil ||
			*d.Skip != want[0] || *d.CustomCA != want[1] || strings.Contains(au[i].Details, "BEGIN") {
			t.Fatalf("test audit %d: %s", i, au[i].Details)
		}
	}
	e.do(t, "POST", "/api/settings/oidc/test", e.body(""), auth.RoleAdmin)
	if au := e.audits(); len(au) != 1 || !strings.Contains(au[0].Details, `"customCA":false`) ||
		!strings.Contains(au[0].Details, `"tlsInsecureSkipVerify":false`) {
		t.Fatalf("TLS'siz test audit'i: %+v", au)
	}
}
