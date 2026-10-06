package auth

// oidc_tls.go — v0.10.1112 (operatör: "oidc bağlantısının tls kontrolünü
// kapatma seçeneği de olsun sertifikaya takılıyor.")
//
// Müşterinin IdP'si kurum içi bir CA ile imzalı; Coremetry'nin keşif / JWKS /
// token çağrıları sistem kök sertifikalarıyla doğrulanamıyor. İki seçenek,
// ikisi de `auth_oidc` blobunda ve Settings > SSO'da:
//
//   - tlsCACertPEM (TERCİH EDİLEN): bir ya da birden çok PEM CERTIFICATE
//     bloğu, SİSTEM havuzuna EKLENİR (yerine geçmez) — kamuya açık bir IdP'ye
//     geçişte de çalışır. Secret değil (açık sertifika): GET'te döner,
//     audit'e özet (CN + SHA-256 parmak izi) girer. ≤64 KB; özel anahtar
//     bloğu açık bir hatayla reddedilir. Güvenlik incelemesi: denetim SSO
//     kapalıyken de koşar (F1) ve YALNIZ çözülen sertifikalar yeniden
//     kodlanıp saklanır — bloklar arası metin düşer (N2, canonicalOIDCCACert).
//   - tlsInsecureSkipVerify (SON ÇARE, varsayılan kapalı): sertifika
//     doğrulaması hiç yapılmaz → ortadaki-adam saldırısına açık. Açıkken her
//     ayar yüklemesinde bir kez WARN loglanır.
//
// ZORUNLU KALAN: https kuralı (skip-verify düz http'ye İZİN VERMEZ — http yalnız
// config.yaml `allow_insecure_issuer`) ve dial koruması (loopback /
// link-local / metadata reddi) — ikisi de oidcNetPolicy'de, TLS'ten bağımsız.
//
// TEK TAŞIMA KURUCUSU: newOIDCHTTPClient (oidc.go) bu dosyadaki
// oidcTLSConfig'i kullanır; keşif, JWKS (go-oidc RemoteKeySet — sağlayıcı
// bağlamındaki istemci), token değişimi + id_token doğrulaması (Exchange),
// access token doğrulaması (v0.10.1110 atVerifier, aynı JWKS), userinfo
// (sağlayıcının istemcisi) ve Settings "Bağlantıyı test et" hep o istemciden
// geçer. Çivi: oidc_tls_test.go (kaynak taraması + httptest TLS davranışı).

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
)

// OIDCCACertPEMMax — özel CA alanının bayt tavanı.
const OIDCCACertPEMMax = 64 << 10

// oidcCASummaryMax — audit özetinde listelenen en çok sertifika.
const oidcCASummaryMax = 10

// oidcTLSOptions — bir yapılandırmanın IdP TLS güveni. Karşılaştırılabilir
// (önbellekli keşif yalnız AYNI TLS ayarıyla yeniden kullanılır).
type oidcTLSOptions struct {
	caPEM      string
	skipVerify bool
}

func (s OIDCSettings) tlsOptions() oidcTLSOptions {
	return oidcTLSOptions{caPEM: s.TLSCACertPEM, skipVerify: s.TLSInsecureSkipVerify}
}

// normalizePEM — CRLF → LF, baş/son boşluk kırpılır. SAF.
func normalizePEM(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}

// parseOIDCCACertPEM — PEM metni → sertifikalar. Bloklar arası düz metin
// (CA paketlerindeki "# Subject: …" yorumları) yok sayılır; blok türü YALNIZ
// CERTIFICATE. Hata *OIDCSettingsError (alan tlsCACertPEM); metni PEM içeriğini
// taşımaz. SAF.
func parseOIDCCACertPEM(text string) ([]*x509.Certificate, error) {
	bad := func(msg string) error { return &OIDCSettingsError{"tlsCACertPEM", "Özel CA sertifikası: " + msg} }
	if len(text) > OIDCCACertPEMMax {
		return nil, bad("çok büyük (en çok 64 KB)")
	}
	if strings.Contains(text, "PRIVATE KEY-----") { // bozuk kodlu blok da (pem.Decode atlar)
		return nil, bad(privateKeyMsg)
	}
	var certs []*x509.Certificate
	rest := []byte(text)
	for n := 1; ; n++ {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		switch {
		case strings.Contains(blk.Type, "PRIVATE KEY"):
			return nil, bad(privateKeyMsg)
		case blk.Type != "CERTIFICATE":
			return nil, bad(fmt.Sprintf("yalnız CERTIFICATE blokları kabul edilir (%d. blok: %q)", n, clipBlockType(blk.Type)))
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return nil, bad(fmt.Sprintf("%d. sertifika çözülemedi", n))
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, bad("PEM sertifikası bulunamadı (-----BEGIN CERTIFICATE----- … -----END CERTIFICATE-----)")
	}
	return certs, nil
}

const privateKeyMsg = "özel anahtar (PRIVATE KEY) yapıştırılmış — yalnız CA sertifikası (BEGIN CERTIFICATE) girin; özel anahtarı paylaşmayın"

func clipBlockType(t string) string {
	if len(t) > 40 {
		return t[:40] + "…"
	}
	return t
}

// validateOIDCCACert — boş geçerli (özel CA yok). Güvenlik incelemesi F1:
// SSO kapalıyken de çağrılır (tavan + özel anahtar reddi + çözüm). SAF.
func validateOIDCCACert(text string) error {
	_, err := canonicalOIDCCACert(text)
	return err
}

// canonicalOIDCCACert — saklanan biçim (güvenlik incelemesi N2): YALNIZ
// çözülen sertifikalar, pem.EncodeToMemory ile yeniden kodlanmış, satır
// sonuyla birleşik. Bloklar arası her metin (yorum satırları; pem.Decode'un
// atladığı, dize denetimini de aşan zırhlı bir PGP özel anahtar bloğu) DÜŞER —
// GET ve blob kanonik biçimi taşır. normalizePEM ile idempotent. Boş → "". SAF.
func canonicalOIDCCACert(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	certs, err := parseOIDCCACertPEM(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range certs {
		b.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
	}
	return strings.TrimSpace(b.String()), nil
}

// oidcTLSConfig — OIDC istemcisinin TLS yapılandırması. Özel CA SİSTEM
// havuzuna eklenir; skip-verify yalnız sertifika doğrulamasını kapatır
// (şema ve dial kuralları oidcNetPolicy'de, burada DEĞİL).
func oidcTLSConfig(t oidcTLSOptions) (*tls.Config, error) {
	var c *tls.Config
	if oidcTestTLSConfig != nil {
		c = oidcTestTLSConfig.Clone()
	} else {
		c = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if t.caPEM != "" {
		certs, err := parseOIDCCACertPEM(t.caPEM)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		for _, cert := range certs {
			pool.AddCert(cert)
		}
		c.RootCAs = pool
	}
	if t.skipVerify {
		// Operatörün bilinçli seçimi (Settings > SSO, "önerilmez" uyarısıyla);
		// her ayar yüklemesinde WARN loglanır (warnInsecureOIDCTLS).
		c.InsecureSkipVerify = true
	}
	return c, nil
}

// warnInsecureOIDCTLS — skip-verify açık ve SSO etkinken tek WARN satırı.
// Çağıranlar ayar YÜKLEMESİ başına bir kez çağırır (blob değişimi / PUT).
func warnInsecureOIDCTLS(s OIDCSettings, logf func(string, ...any)) {
	if !s.Enabled || !s.TLSInsecureSkipVerify {
		return
	}
	logf("[auth] WARNING: OIDC TLS sertifika doğrulaması KAPALI (tlsInsecureSkipVerify) issuer=%s — "+
		"ortadaki-adam saldırısına açık; mümkünse Settings > SSO'ya CA sertifikası ekleyin", s.IssuerURL)
}

// OIDCCACertSummary — audit için sertifika özeti: "CN · sha256:<16 hex> ·
// bitiş YYYY-MM-DD" (en çok 10 + "+N sertifika daha"). Boş → boş dizi;
// çözülemeyen → tek "geçersiz PEM (N bayt)". PEM'in kendisi audit'e girmez
// (64 KB'a kadar). SAF.
func OIDCCACertSummary(text string) []string {
	out := []string{}
	if text == "" {
		return out
	}
	certs, err := parseOIDCCACertPEM(text)
	if err != nil {
		return append(out, fmt.Sprintf("geçersiz PEM (%d bayt)", len(text)))
	}
	for i, c := range certs {
		if i == oidcCASummaryMax {
			out = append(out, fmt.Sprintf("+%d sertifika daha", len(certs)-oidcCASummaryMax))
			break
		}
		sum := sha256.Sum256(c.Raw)
		name := c.Subject.CommonName
		if name == "" {
			name = c.Subject.String()
		}
		if len(name) > 120 {
			name = name[:120] + "…"
		}
		out = append(out, fmt.Sprintf("%s · sha256:%s · bitiş %s", name, hex.EncodeToString(sum[:8]), c.NotAfter.UTC().Format("2006-01-02")))
	}
	return out
}
