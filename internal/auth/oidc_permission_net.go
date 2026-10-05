package auth

// oidc_permission_net.go — v0.10.1111 (operatör: "Anahtarsız olmaz mı").
//
// Müşterinin merkezi login'i özel başlık gönderemiyor; yetki servisi
// (POST /api/auth/permissions) anahtarsız da çalışabilmeli. Anahtarsız kipte
// sınır AĞ olur: IP/CIDR izin listesi. Bu dosya o listenin saf parçaları:
// ayrıştırma/normalize/doğrulama ve çağıranın IP'sinin çözümü.
//
// ÇAĞIRANIN IP'Sİ — neden api.clientIP DEĞİL: clientIP X-Forwarded-For'un
// İLK girişine koşulsuz güvenir (audit/forensik için yeterli); bir erişim
// sınırı için değil — XFF'i çağıran yazar, ingress çoğunlukla EKLER (ör.
// OpenShift router "Append"), doğrudan pod erişiminde tamamen çağıranındır.
// Burada: doğrudan TCP eşi (RemoteAddr) güvenilen vekil listesinde DEĞİLSE
// çağıran odur ve XFF hiç okunmaz; listedeyse XFF SAĞDAN sola yürünür, ilk
// güvenilmeyen adres çağırandır ("rightmost untrusted"). Ayrıştırılamayan
// bir XFF girişi → çözüm yok → reddedilir (fail-closed). Güvenilen vekil
// listesi boşsa XFF hiç okunmaz.

import (
	"fmt"
	"net/netip"
	"strings"
)

// parsePermissionCIDR — "10.0.0.0/8", "2001:db8::/32" ya da tek IP
// ("203.0.113.7" → /32, IPv6 → /128). Bölge (%eth0) ve IPv4-in-IPv6 önek
// reddedilir; ana bitleri maskelenir. SAF.
func parsePermissionCIDR(v string) (netip.Prefix, error) {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "/") {
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return netip.Prefix{}, err
		}
		if p.Addr().Is4In6() {
			return netip.Prefix{}, fmt.Errorf("IPv4 adresini düz yazın")
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(v)
	if err != nil {
		return netip.Prefix{}, err
	}
	if a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("bölge kimliği olamaz")
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// canonicalPermissionCIDR — kayıt biçimi: tek IP düz ("203.0.113.7"), önek
// maskeli ("10.1.2.3/8" → "10.0.0.0/8"). Ayrıştırılamayan girdi AYNEN döner
// (doğrulama yakalasın). SAF.
func canonicalPermissionCIDR(v string) string {
	p, err := parsePermissionCIDR(v)
	if err != nil {
		return strings.TrimSpace(v)
	}
	if !strings.Contains(v, "/") {
		return p.Addr().String()
	}
	return p.String()
}

// normalizeCIDRList — satır/virgül/boşluk ayraçlı girdiler bölünür,
// kanonikleşir, tekrarlar atılır. Boş → nil. SAF.
func normalizeCIDRList(in []string) []string {
	parts := splitList(in)
	for i, v := range parts {
		parts[i] = canonicalPermissionCIDR(v)
	}
	out := dedupe(parts)
	if len(out) == 0 {
		return nil
	}
	return out
}

// validateCIDRList — ≤PermissionMaxCIDRs, her giriş ayrıştırılabilir. Hata
// metni girdiyi kırparak gösterir (secret değil). SAF.
func validateCIDRList(field, label string, list []string) error {
	if len(list) > PermissionMaxCIDRs {
		return &OIDCSettingsError{field, fmt.Sprintf("%s en çok %d giriş olabilir", label, PermissionMaxCIDRs)}
	}
	for _, v := range list {
		if _, err := parsePermissionCIDR(v); err != nil {
			shown := v
			if len(shown) > 64 {
				shown = shown[:64] + "…"
			}
			return &OIDCSettingsError{field, fmt.Sprintf("%s: geçersiz CIDR/IP %q", label, shown)}
		}
	}
	return nil
}

// parsePrefixes — doğrulanmış liste → önekler. Ayrıştırılamayan giriş varsa
// ok=false (çağıran fail-closed davranır). SAF.
func parsePrefixes(list []string) ([]netip.Prefix, bool) {
	out := make([]netip.Prefix, 0, len(list))
	for _, v := range list {
		p, err := parsePermissionCIDR(v)
		if err != nil {
			return nil, false
		}
		out = append(out, p)
	}
	return out, true
}

func prefixesContain(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parseHostAddr — "ip", "ip:port", "[v6]:port"; bölge atılır, IPv4-in-IPv6
// düzleşir. SAF.
func parseHostAddr(v string) (netip.Addr, bool) {
	v = strings.TrimSpace(v)
	a, err := netip.ParseAddr(v)
	if err != nil {
		ap, perr := netip.ParseAddrPort(v)
		if perr != nil {
			return netip.Addr{}, false
		}
		a = ap.Addr()
	}
	return a.WithZone("").Unmap(), true
}

// PermissionCallerIP — çağıranın IP'si (dosya başlığı). remoteAddr =
// r.RemoteAddr, xff = r.Header.Values("X-Forwarded-For"). ok=false →
// çözülemedi (reddet). SAF.
func PermissionCallerIP(remoteAddr string, xff []string, trusted []netip.Prefix) (netip.Addr, bool) {
	peer, ok := parseHostAddr(remoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	if len(trusted) == 0 || !prefixesContain(trusted, peer) {
		return peer, true
	}
	var hops []string
	for _, h := range xff {
		for _, p := range strings.Split(h, ",") {
			if p = strings.TrimSpace(p); p != "" {
				hops = append(hops, p)
			}
		}
	}
	if len(hops) == 0 {
		return peer, true
	}
	var last netip.Addr
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseHostAddr(hops[i])
		if !ok {
			return netip.Addr{}, false
		}
		if !prefixesContain(trusted, a) {
			return a, true
		}
		last = a
	}
	return last, true // zincirin tamamı güvenilen vekil → en soldaki
}

// CallerAllowed — izin listesi boşsa her çağıran; doluysa çözülen IP
// listede olmalı. Çözülemeyen IP reddedilir. Dönen adres yalnız teşhis logu
// içindir (geçersizse "").
func (c PermissionServiceConfig) CallerAllowed(remoteAddr string, xff []string) (bool, string) {
	if len(c.AllowedCIDRs) == 0 {
		return true, ""
	}
	ip, ok := PermissionCallerIP(remoteAddr, xff, c.TrustedProxies)
	if !ok {
		return false, ""
	}
	return prefixesContain(c.AllowedCIDRs, ip), ip.String()
}
