package api

// auth_permissions_keyless_test.go — v0.10.1111 (operatör: "Anahtarsız olmaz
// mı"; müşterinin merkezi login'i özel başlık gönderemiyor).
//
// NE ÇİVİLİYOR: kontrol sırası 404 (kapalı) → 403 (IP izin listesi) → 401
// (anahtar; anahtarsız kipte hiç) → 400 (gövde) → eşleme; 403 gövdesiz ve
// depoya ulaşmaz; anahtarsız kipte başlık HİÇ denetlenmez (kayıtlı anahtar
// olsa da); sahte X-Forwarded-For, doğrudan eş güvenilen vekil değilse izin
// listesini aşamaz; anahtar kipi değişmedi. Settings PUT: bozuk CIDR / >32 →
// 400 ve yazmaz; allowNoKey + anahtarsız geçerli; açık + anahtarsız +
// !allowNoKey 400; alanlar GET'te ve audit'te aynen, kanonik biçimde.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

type permNetCase struct {
	name string
	// ayar
	enabled, allowNoKey bool
	storedKey           string
	cidrs, proxies      []string
	// istek
	remote, xff, key, body string
	// beklenen
	status      int
	readsLookup bool
}

func runPermNetCase(t *testing.T, c permNetCase) {
	t.Helper()
	e := newOIDCTestEnv(t)
	in := auth.OIDCSettings{PermissionServiceEnabled: c.enabled, PermissionServiceKey: c.storedKey,
		PermissionServiceAllowNoKey: c.allowNoKey, PermissionServiceAllowedCIDRs: c.cidrs,
		PermissionServiceTrustedProxies: c.proxies, PermissionTTLSeconds: 120}
	if _, err := e.s.oidc.SaveSettings(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	lk := &permFakeLookup{byEmail: map[string]*chstore.User{}, byLdap: map[string]*chstore.User{}}
	prev := permissionsLookup
	permissionsLookup = func(*Server) permUserLookup { return lk }
	t.Cleanup(func() { permissionsLookup = prev })
	e.s.registerAuthPermissionsRoutes(e.mux)

	req := httptest.NewRequest("POST", "/api/auth/permissions", strings.NewReader(c.body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = c.remote
	if c.xff != "" {
		req.Header.Set("X-Forwarded-For", c.xff)
	}
	if c.key != "" {
		req.Header.Set(PermissionAuthHeader, c.key)
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	if w.Code != c.status {
		t.Fatalf("→ %d %q, beklenen %d", w.Code, w.Body.String(), c.status)
	}
	if c.status == http.StatusForbidden || c.status == http.StatusUnauthorized || c.status == http.StatusNotFound {
		if w.Body.Len() != 0 {
			t.Fatalf("%d gövdesiz olmalı: %q", c.status, w.Body.String())
		}
	}
	if got := len(lk.calls) > 0; got != c.readsLookup {
		t.Fatalf("depo okuması = %v, beklenen %v (%v)", got, c.readsLookup, lk.calls)
	}
}

func TestAuthPermissionsCheckOrder(t *testing.T) {
	good := permBody("1", "nobody", "nobody@example.test", "00000")
	const bad = `{bozuk`
	allow := []string{"203.0.113.0/24"}
	proxy := []string{"10.0.0.0/8"}
	cases := []permNetCase{
		// 404 her şeyden önce.
		{name: "kapalı + anahtarsız → 404", allowNoKey: true, cidrs: allow, remote: "198.51.100.9:1", body: bad, status: 404},
		// 403, anahtardan ve gövdeden ÖNCE.
		{name: "IP reddi anahtardan önce (anahtar yok, gövde bozuk)", enabled: true, storedKey: permTestKey, cidrs: allow, remote: "198.51.100.9:1", body: bad, status: 403},
		{name: "IP reddi doğru anahtarla da", enabled: true, storedKey: permTestKey, cidrs: allow, remote: "198.51.100.9:1", key: permTestKey, body: good, status: 403},
		{name: "IP izinli + anahtar yanlış → 401", enabled: true, storedKey: permTestKey, cidrs: allow, remote: "203.0.113.7:1", key: "wrong-key-0000000000", body: bad, status: 401},
		{name: "IP izinli + anahtar doğru → 200", enabled: true, storedKey: permTestKey, cidrs: allow, remote: "203.0.113.7:1", key: permTestKey, body: good, status: 200, readsLookup: true},
		// Anahtarsız kip.
		{name: "anahtarsız, IP kısıtı yok, başlık yok → 200", enabled: true, allowNoKey: true, remote: "198.51.100.9:1", body: good, status: 200, readsLookup: true},
		{name: "anahtarsız + CIDR eşleşir → 200", enabled: true, allowNoKey: true, cidrs: allow, remote: "203.0.113.7:1", body: good, status: 200, readsLookup: true},
		{name: "anahtarsız + CIDR eşleşmez → 403", enabled: true, allowNoKey: true, cidrs: allow, remote: "198.51.100.9:1", body: good, status: 403},
		{name: "anahtarsız + kayıtlı anahtar, başlık yok → 200 (başlık denetlenmez)", enabled: true, allowNoKey: true, storedKey: permTestKey, remote: "198.51.100.9:1", body: good, status: 200, readsLookup: true},
		{name: "anahtarsız + yanlış başlık → 200 (başlık denetlenmez)", enabled: true, allowNoKey: true, storedKey: permTestKey, remote: "198.51.100.9:1", key: "wrong-key-0000000000", body: good, status: 200, readsLookup: true},
		{name: "anahtarsız + bozuk gövde → 400 (IP'den sonra)", enabled: true, allowNoKey: true, cidrs: allow, remote: "203.0.113.7:1", body: bad, status: 400},
		// Çağıranın IP'si: sahte XFF.
		{name: "güvenilmeyen eş + izinli sahte XFF → 403", enabled: true, allowNoKey: true, cidrs: allow, proxies: proxy, remote: "198.51.100.9:1", xff: "203.0.113.7", body: good, status: 403},
		{name: "vekil listesi boş: XFF hiç okunmaz → 403", enabled: true, allowNoKey: true, cidrs: allow, remote: "10.0.0.5:1", xff: "203.0.113.7", body: good, status: 403},
		{name: "güvenilen vekil arkasından izinli çağıran → 200", enabled: true, allowNoKey: true, cidrs: allow, proxies: proxy, remote: "10.0.0.5:1", xff: "203.0.113.7", body: good, status: 200, readsLookup: true},
		{name: "ekleyen vekil: soldaki sahte izinli giriş işe yaramaz → 403", enabled: true, allowNoKey: true, cidrs: allow, proxies: proxy, remote: "10.0.0.5:1", xff: "203.0.113.7, 198.51.100.9", body: good, status: 403},
		{name: "bozuk XFF → 403", enabled: true, allowNoKey: true, cidrs: allow, proxies: proxy, remote: "10.0.0.5:1", xff: "unknown", body: good, status: 403},
		// Anahtar kipi değişmedi.
		{name: "anahtar kipi: başlık yok → 401", enabled: true, storedKey: permTestKey, remote: "198.51.100.9:1", body: bad, status: 401},
		{name: "anahtar kipi: doğru anahtar → 200", enabled: true, storedKey: permTestKey, remote: "198.51.100.9:1", key: permTestKey, body: good, status: 200, readsLookup: true},
		{name: "anahtar kipi: doğru anahtar + bozuk gövde → 400", enabled: true, storedKey: permTestKey, remote: "198.51.100.9:1", key: permTestKey, body: bad, status: 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runPermNetCase(t, c) })
	}
}

func TestOIDCSettingsPermissionNetRoundTrip(t *testing.T) {
	e := newOIDCTestEnv(t)
	put := func(m map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(m)
		return e.do(t, "PUT", "/api/settings/oidc", string(b), auth.RoleAdmin)
	}
	base := func() map[string]any {
		return map[string]any{"enabled": false, "permissionServiceEnabled": true, "permissionServiceAllowNoKey": true}
	}
	// allowNoKey + anahtarsız geçerli; listeler kanonikleşir.
	m := base()
	m["permissionServiceAllowedCIDRs"] = []string{"203.0.113.7", "10.1.2.3/8", "2001:DB8::/32", "10.0.0.0/8"}
	m["permissionServiceTrustedProxies"] = []string{"10.42.0.0/16"}
	w := put(m)
	if w.Code != http.StatusOK {
		t.Fatalf("anahtarsız PUT → %d %s", w.Code, w.Body.String())
	}
	var snap auth.OIDCSnapshot
	_ = json.Unmarshal(w.Body.Bytes(), &snap)
	if !snap.PermissionServiceAllowNoKey || snap.PermissionServiceKeySet ||
		fmt.Sprint(snap.PermissionServiceAllowedCIDRs) != "[203.0.113.7 10.0.0.0/8 2001:db8::/32]" ||
		fmt.Sprint(snap.PermissionServiceTrustedProxies) != "[10.42.0.0/16]" {
		t.Fatalf("snapshot yanlış: %+v", snap)
	}
	g := e.do(t, "GET", "/api/settings/oidc", "", auth.RoleAdmin)
	for _, want := range []string{`"permissionServiceAllowNoKey":true`, `"permissionServiceAllowedCIDRs":["203.0.113.7","10.0.0.0/8","2001:db8::/32"]`, `"permissionServiceTrustedProxies":["10.42.0.0/16"]`} {
		if !strings.Contains(g.Body.String(), want) {
			t.Fatalf("GET %s içermiyor: %s", want, g.Body.String())
		}
	}
	as := e.audits()
	if len(as) != 1 || !strings.Contains(as[0].Details, `"permissionServiceAllowNoKey":true`) ||
		!strings.Contains(as[0].Details, `"permissionServiceAllowedCIDRs":["203.0.113.7"`) ||
		!strings.Contains(as[0].Details, `"permissionServiceTrustedProxies":["10.42.0.0/16"]`) {
		t.Fatalf("audit yanlış: %+v", as)
	}

	// Reddedilenler: 400 ve yazma yok.
	many := make([]string, auth.PermissionMaxCIDRs+1)
	for i := range many {
		many[i] = fmt.Sprintf("10.0.%d.0/24", i)
	}
	puts := e.store.puts
	for name, mut := range map[string]func(map[string]any){
		"bozuk CIDR":              func(m map[string]any) { m["permissionServiceAllowedCIDRs"] = []string{"10.0.0.0/33"} },
		"32'den fazla CIDR":       func(m map[string]any) { m["permissionServiceAllowedCIDRs"] = many },
		"bozuk vekil":             func(m map[string]any) { m["permissionServiceTrustedProxies"] = []string{"ingress"} },
		"anahtar yok, kip kapalı": func(m map[string]any) { m["permissionServiceAllowNoKey"] = false },
	} {
		m := base()
		mut(m)
		if w := put(m); w.Code != http.StatusBadRequest {
			t.Errorf("%s → %d %s, beklenen 400", name, w.Code, w.Body.String())
		}
	}
	if e.store.puts != puts {
		t.Fatal("reddedilen PUT yazdı")
	}
}
