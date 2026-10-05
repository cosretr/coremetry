package auth

// oidc_permission_net_test.go — v0.10.1111 (operatör: "Anahtarsız olmaz mı").
//
// NE ÇİVİLİYOR: çağıranın IP'si — doğrudan eş güvenilen vekil değilse XFF
// HİÇ okunmaz (sahte XFF izin listesini aşamaz), vekilse XFF sağdan ilk
// güvenilmeyen giriş, bozuk giriş → çözüm yok (red); izin listesi boşsa her
// çağıran; CIDR normalize/doğrulama (tek IP, maske, IPv6, bölge reddi, ≤32);
// anahtarsız kip doğrulaması (allowNoKey + anahtarsız geçerli; açık +
// anahtarsız + !allowNoKey 400) ve Usable; blobdaki bozuk CIDR yalnız servisi
// kapatır (N4 kalıbı).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

func mustPrefixes(t *testing.T, vs ...string) []netip.Prefix {
	t.Helper()
	ps, ok := parsePrefixes(vs)
	if !ok {
		t.Fatalf("önek ayrıştırılamadı: %v", vs)
	}
	return ps
}

func TestPermissionCallerIP(t *testing.T) {
	proxies := mustPrefixes(t, "10.0.0.0/8", "fd00::/8")
	cases := []struct {
		name    string
		remote  string
		xff     []string
		trusted []netip.Prefix
		want    string // "" = çözülemedi
	}{
		{"vekil listesi boş: XFF yok sayılır", "198.51.100.9:5555", []string{"203.0.113.7"}, nil, "198.51.100.9"},
		{"eş güvenilmeyen: sahte XFF yok sayılır", "198.51.100.9:5555", []string{"203.0.113.7"}, proxies, "198.51.100.9"},
		{"eş vekil, tek XFF", "10.1.2.3:443", []string{"203.0.113.7"}, proxies, "203.0.113.7"},
		{"eş vekil, ekleyen ingress: soldaki sahte giriş atlanır", "10.1.2.3:443", []string{"203.0.113.7, 198.51.100.20"}, proxies, "198.51.100.20"},
		{"eş vekil, zincirde iç vekil atlanır", "10.1.2.3:443", []string{"198.51.100.20, 10.9.9.9"}, proxies, "198.51.100.20"},
		{"çoklu XFF başlığı birleşir", "10.1.2.3:443", []string{"203.0.113.7", "198.51.100.20"}, proxies, "198.51.100.20"},
		{"eş vekil, XFF yok → eş", "10.1.2.3:443", nil, proxies, "10.1.2.3"},
		{"zincirin tamamı vekil → en soldaki", "10.1.2.3:443", []string{"10.4.4.4, 10.5.5.5"}, proxies, "10.4.4.4"},
		{"bozuk XFF girişi → red", "10.1.2.3:443", []string{"bozuk"}, proxies, ""},
		{"port taşıyan XFF girişi", "10.1.2.3:443", []string{"198.51.100.20:61000"}, proxies, "198.51.100.20"},
		{"IPv6 eş + XFF", "[fd00::1]:443", []string{"2001:db8::7"}, proxies, "2001:db8::7"},
		{"IPv4-in-IPv6 eş düzleşir", "[::ffff:10.1.2.3]:443", []string{"203.0.113.7"}, proxies, "203.0.113.7"},
		{"çıplak RemoteAddr", "198.51.100.9", nil, nil, "198.51.100.9"},
		{"bozuk RemoteAddr → red", "pipe", nil, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ip, ok := PermissionCallerIP(c.remote, c.xff, c.trusted)
			if c.want == "" {
				if ok {
					t.Fatalf("çözülmemeliydi, %v döndü", ip)
				}
				return
			}
			if !ok || ip.String() != c.want {
				t.Fatalf("= %v,%v; beklenen %s", ip, ok, c.want)
			}
		})
	}
}

func TestPermissionCallerAllowed(t *testing.T) {
	open := PermissionServiceConfig{}
	if ok, _ := open.CallerAllowed("pipe", []string{"bozuk"}); !ok {
		t.Fatal("izin listesi boşken her çağıran geçmeli")
	}
	c := PermissionServiceConfig{
		AllowedCIDRs:   mustPrefixes(t, "203.0.113.0/24", "2001:db8::/32"),
		TrustedProxies: mustPrefixes(t, "10.0.0.0/8"),
	}
	for _, tc := range []struct {
		remote string
		xff    []string
		want   bool
	}{
		{"203.0.113.7:1", nil, true},
		{"198.51.100.9:1", nil, false},
		{"198.51.100.9:1", []string{"203.0.113.7"}, false}, // sahte XFF, eş güvenilmeyen
		{"10.0.0.5:1", []string{"203.0.113.7"}, true},
		{"10.0.0.5:1", []string{"203.0.113.7, 198.51.100.9"}, false}, // sağdaki gerçek çağıran
		{"10.0.0.5:1", nil, false},                                   // vekilin kendisi listede değil
		{"10.0.0.5:1", []string{"bozuk"}, false},
		{"[2001:db8::5]:1", nil, true},
	} {
		if got, _ := c.CallerAllowed(tc.remote, tc.xff); got != tc.want {
			t.Errorf("CallerAllowed(%s, %v) = %v, beklenen %v", tc.remote, tc.xff, got, tc.want)
		}
	}
}

func TestNormalizeCIDRList(t *testing.T) {
	got := normalizeCIDRList([]string{"10.1.2.3/8\n203.0.113.7, 10.0.0.0/8", " 2001:DB8::1/32 ; ::ffff:198.51.100.1", "bozuk"})
	want := []string{"10.0.0.0/8", "203.0.113.7", "2001:db8::/32", "198.51.100.1", "bozuk"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("= %v, beklenen %v", got, want)
	}
	if normalizeCIDRList([]string{" ", ""}) != nil {
		t.Fatal("boş liste nil olmalı")
	}
}

func TestValidatePermissionNetSettings(t *testing.T) {
	many := make([]string, PermissionMaxCIDRs+1)
	for i := range many {
		many[i] = fmt.Sprintf("10.0.%d.0/24", i)
	}
	cases := []struct {
		name  string
		in    OIDCSettings
		field string
	}{
		{"anahtarsız kip + anahtar yok geçerli", OIDCSettings{PermissionServiceEnabled: true, PermissionServiceAllowNoKey: true}, ""},
		{"açık + anahtar yok + anahtarsız kapalı", OIDCSettings{PermissionServiceEnabled: true}, "permissionServiceKey"},
		{"anahtarsız kip + kısa anahtar yine reddedilir", OIDCSettings{PermissionServiceEnabled: true, PermissionServiceAllowNoKey: true, PermissionServiceKey: "short"}, "permissionServiceKey"},
		{"geçerli CIDR + tek IP + IPv6", OIDCSettings{PermissionServiceAllowedCIDRs: []string{"10.0.0.0/8", "203.0.113.7", "2001:db8::/32"}}, ""},
		{"bozuk CIDR", OIDCSettings{PermissionServiceAllowedCIDRs: []string{"10.0.0.0/33"}}, "permissionServiceAllowedCIDRs"},
		{"ad değil IP", OIDCSettings{PermissionServiceAllowedCIDRs: []string{"idp.example.test"}}, "permissionServiceAllowedCIDRs"},
		{"bölge kimliği", OIDCSettings{PermissionServiceAllowedCIDRs: []string{"fe80::1%eth0"}}, "permissionServiceAllowedCIDRs"},
		{"IPv4-in-IPv6 önek", OIDCSettings{PermissionServiceAllowedCIDRs: []string{"::ffff:10.0.0.0/104"}}, "permissionServiceAllowedCIDRs"},
		{"32'den fazla", OIDCSettings{PermissionServiceAllowedCIDRs: many}, "permissionServiceAllowedCIDRs"},
		{"tam 32", OIDCSettings{PermissionServiceAllowedCIDRs: many[:PermissionMaxCIDRs]}, ""},
		{"bozuk vekil", OIDCSettings{PermissionServiceTrustedProxies: []string{"x/8"}}, "permissionServiceTrustedProxies"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateOIDCSettings(NormalizeOIDCSettings(c.in, ""), false)
			if c.field == "" {
				if err != nil {
					t.Fatalf("beklenmeyen hata: %v", err)
				}
				return
			}
			var se *OIDCSettingsError
			if err == nil || !errors.As(err, &se) || se.Field != c.field {
				t.Fatalf("%s hatası bekleniyordu: %v", c.field, err)
			}
		})
	}
}

func TestPermissionServiceKeylessUsable(t *testing.T) {
	st := newFakeOIDCStore()
	o := newDevService(t, st)
	in := OIDCSettings{PermissionServiceEnabled: true, PermissionServiceAllowNoKey: true,
		PermissionServiceAllowedCIDRs: []string{"203.0.113.0/24"}, PermissionServiceTrustedProxies: []string{"10.0.0.0/8"}}
	snap, err := o.SaveSettings(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if snap.PermissionServiceKeySet || !snap.PermissionServiceAllowNoKey ||
		fmt.Sprint(snap.PermissionServiceAllowedCIDRs) != "[203.0.113.0/24]" || fmt.Sprint(snap.PermissionServiceTrustedProxies) != "[10.0.0.0/8]" {
		t.Fatalf("snapshot yanlış: %+v", snap)
	}
	cfg := o.PermissionService()
	if !cfg.Usable() || !cfg.AllowNoKey || len(cfg.AllowedCIDRs) != 1 || len(cfg.TrustedProxies) != 1 {
		t.Fatalf("anahtarsız servis kullanılabilir olmalı: %+v", cfg)
	}
	// Peer pod blobdan aynısını kurar.
	if p := newDevService(t, st).PermissionService(); !p.Usable() || !p.AllowNoKey || len(p.AllowedCIDRs) != 1 {
		t.Fatalf("peer yapılandırması yanlış: %+v", p)
	}
	// Anahtarsız kip kapanınca anahtar yoksa servis kayıt edilemez.
	in.PermissionServiceAllowNoKey = false
	if _, err := o.SaveSettings(context.Background(), in); err == nil {
		t.Fatal("anahtarsız kip kapalı + anahtar yok kaydedilmemeliydi")
	}
}

// N4 kalıbı: blobda bozuk CIDR → SSO düşmez, yalnız servis kapanır; ağ
// alanları da sıfırlanır (bozuk liste "kısıt yok"a dönüşüp ucu açmasın).
func TestLoadPersistedBadCIDRDisablesServiceOnly(t *testing.T) {
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	b := validInput(idp.URL).stored()
	b.PermissionServiceEnabled, b.PermissionServiceAllowNoKey = true, true
	b.PermissionServiceAllowedCIDRs = []string{"203.0.113.0/24", "10.0.0.0/99"}
	raw, _ := json.Marshal(b)
	st.set(func(f *fakeOIDCStore) { f.rows[OIDCSettingsKey] = raw })
	o := NewOIDCService(devBoot, testPublicURL, st)
	if err := o.LoadPersisted(context.Background()); err != nil {
		t.Fatalf("SSO yüklenmeliydi: %v", err)
	}
	if !o.Enabled() {
		t.Fatal("bozuk CIDR SSO'yu düşürdü")
	}
	cfg := o.PermissionService()
	if cfg.Usable() || cfg.AllowNoKey || len(cfg.AllowedCIDRs) != 0 {
		t.Fatalf("servis kapanmadı: %+v", cfg)
	}
	if !strings.Contains(o.Snapshot().LastError, "IP izin listesi") {
		t.Fatalf("lastError: %q", o.Snapshot().LastError)
	}
}

// Savunma: doğrulamadan kaçmış (elle kurulmuş) bozuk liste servisi KAPATIR.
func TestPermissionServiceUnparsableListFailsClosed(t *testing.T) {
	o := newDevService(t, newFakeOIDCStore())
	o.mu.Lock()
	o.st.eff = OIDCSettings{PermissionServiceEnabled: true, PermissionServiceAllowNoKey: true,
		PermissionServiceAllowedCIDRs: []string{"bozuk"}}
	o.mu.Unlock()
	if o.PermissionService().Usable() {
		t.Fatal("ayrıştırılamayan izin listesiyle servis açık kaldı")
	}
}
