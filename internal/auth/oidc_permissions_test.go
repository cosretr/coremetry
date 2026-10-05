package auth

// oidc_permissions_test.go — v0.10.1110 (merkezi login yetki servisi +
// token claim'inden rol).
//
// NE ÇİVİLİYOR: rol → claim değeri (özel rol katalogda varsa
// COREMETRY_ROLE_<AD>, yoksa taban rol); claim → rol sırası ADMIN > EDITOR >
// VIEWER, tanınmayan/boş → ""; claim biçimleri (dizi, tek dizgi, null);
// anahtar karşılaştırması (boş asla eşleşmez); yetki servisi doğrulaması
// (anahtar ≥16, yer tutucu yok, hata metni anahtarı taşımaz) + TTL kıskacı;
// anahtar hiçbir snapshot / %v / JSON'da görünmez ve boş PUT kayıtlıyı
// korur; giriş akışı: roleFromClaim KAPALIYKEN claim olsa da rol çözülmez
// (operatör kararı 2026-10-05), açıkken access token (JWT, aynı JWKS) önce,
// opak/doğrulanamayan access token'da id_token claim'i; claim yalnız dizi
// (F2), standart kimlik claim'i ad olarak reddedilir; blobdaki geçersiz yetki
// alanları SSO'yu düşürmez (N4).

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPermissionsForRole(t *testing.T) {
	cases := []struct {
		role, custom string
		exists       bool
		want         []string
	}{
		{RoleAdmin, "", false, []string{PermissionAdmin}},
		{RoleEditor, "", false, []string{PermissionEditor}},
		{RoleViewer, "", false, []string{PermissionViewer}},
		{RoleViewer, "svc-orders", true, []string{"COREMETRY_ROLE_SVC_ORDERS"}},
		{RoleViewer, "svc-orders", false, []string{PermissionViewer}}, // katalogda yok → taban
		{RoleViewer, "ödeme ekibi", true, []string{"COREMETRY_ROLE_DEME_EKIBI"}},
		{RoleViewer, "!!!", true, []string{PermissionViewer}},        // ad boşa iner → taban
		{RoleEditor, "svc-orders", true, []string{PermissionEditor}}, // özel rol yalnız viewer'da
		{"root", "", false, nil},
		{"", "", false, nil},
	}
	for _, c := range cases {
		got := PermissionsForRole(c.role, c.custom, c.exists)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("PermissionsForRole(%q,%q,%v) = %v, beklenen %v", c.role, c.custom, c.exists, got, c.want)
		}
	}
}

func TestRoleFromPermissions(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{PermissionViewer}, RoleViewer},
		{[]string{PermissionEditor}, RoleEditor},
		{[]string{PermissionAdmin}, RoleAdmin},
		{[]string{PermissionViewer, PermissionAdmin}, RoleAdmin},   // sıra: admin önce
		{[]string{PermissionViewer, PermissionEditor}, RoleEditor}, // editor > viewer
		{[]string{" coremetry_editor "}, RoleEditor},               // büyük/küçük harf + boşluk
		{[]string{"COREMETRY_ROLE_SVC_ORDERS"}, ""},                // özel rol → rol değişmez
		{[]string{"OTHER_APP_ADMIN"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := RoleFromPermissions(c.in); got != c.want {
			t.Errorf("RoleFromPermissions(%v) = %q, beklenen %q", c.in, got, c.want)
		}
	}
}

func TestParsePermissionsClaim(t *testing.T) {
	cases := []struct {
		raw     string
		want    []string
		present bool
	}{
		{`["COREMETRY_ADMIN","X"]`, []string{"COREMETRY_ADMIN", "X"}, true},
		{`[]`, nil, false},
		{`["", 7]`, nil, false},
		// F2: yalnız dizi — tek dizgi (boşluk/virgül ayraçlı) claim YOK sayılır.
		{`"COREMETRY_VIEWER COREMETRY_EDITOR"`, nil, false},
		{`"Ali COREMETRY_ADMIN"`, nil, false},
		{`"a,b"`, nil, false},
		{`null`, nil, false},
		{``, nil, false},
		{`42`, nil, false},
		{`{"x":1}`, nil, false},
	}
	for _, c := range cases {
		got, present := parsePermissionsClaim(json.RawMessage(c.raw))
		if fmt.Sprint(got) != fmt.Sprint(c.want) || present != c.present {
			t.Errorf("parse(%s) = %v,%v; beklenen %v,%v", c.raw, got, present, c.want, c.present)
		}
	}
}

func TestLooksLikeJWT(t *testing.T) {
	jwt := b64u([]byte(`{"alg":"RS256"}`)) + "." + b64u([]byte(`{}`)) + ".sig"
	for tok, want := range map[string]bool{
		jwt:                true,
		"at":               false,
		"a.b.c":            false, // başlık JSON değil
		"":                 false,
		"opaque-token-xyz": false,
		b64u([]byte(`{"typ":"JWT"}`)) + ".e30.sig": false, // alg yok
	} {
		if got := looksLikeJWT(tok); got != want {
			t.Errorf("looksLikeJWT(%q) = %v, beklenen %v", tok, got, want)
		}
	}
}

func TestPermissionKeyEqual(t *testing.T) {
	const k = "0123456789abcdef-key"
	if !permissionKeyEqual(k, k) {
		t.Fatal("aynı anahtar eşleşmedi")
	}
	for _, got := range []string{"", "0123456789abcdef-kez", k + "x", k[:5]} {
		if permissionKeyEqual(k, got) {
			t.Errorf("yanlış anahtar eşleşti: %q", got)
		}
	}
	if permissionKeyEqual("", "") {
		t.Fatal("boş kayıtlı anahtar boş girdiyle eşleşti")
	}
}

const testPermKey = "f3a9c1d07e5b4a28b6c2"

func TestValidatePermissionSettings(t *testing.T) {
	cases := []struct {
		name  string
		in    OIDCSettings
		field string
	}{
		{"kapalı + anahtarsız geçer", OIDCSettings{}, ""},
		{"açık + anahtarsız", OIDCSettings{PermissionServiceEnabled: true}, "permissionServiceKey"},
		{"kısa anahtar", OIDCSettings{PermissionServiceEnabled: true, PermissionServiceKey: "short-key-123"}, "permissionServiceKey"},
		{"yer tutucu", OIDCSettings{PermissionServiceEnabled: true, PermissionServiceKey: "CHANGE_ME_0123456789abcdef"}, "permissionServiceKey"},
		{"boşluklu", OIDCSettings{PermissionServiceEnabled: true, PermissionServiceKey: "abcdefgh ijklmnopq"}, "permissionServiceKey"},
		{"çok uzun", OIDCSettings{PermissionServiceKey: strings.Repeat("a", 257)}, "permissionServiceKey"},
		{"geçerli", OIDCSettings{PermissionServiceEnabled: true, PermissionServiceKey: testPermKey}, ""},
		{"TTL aralık dışı (ham)", OIDCSettings{PermissionTTLSeconds: 10}, "permissionTTLSeconds"},
		{"claim adı geçersiz", OIDCSettings{PermissionsClaim: "perm issions"}, "permissionsClaim"},
		{"claim adı geçerli", OIDCSettings{PermissionsClaim: "realm_access.roles"}, ""},
		{"standart claim: name", OIDCSettings{PermissionsClaim: "name"}, "permissionsClaim"},
		{"standart claim: preferred_username", OIDCSettings{PermissionsClaim: "preferred_username"}, "permissionsClaim"},
		{"standart claim: Email (büyük harf)", OIDCSettings{PermissionsClaim: "Email"}, "permissionsClaim"},
		{"standart claim: sub", OIDCSettings{PermissionsClaim: "sub"}, "permissionsClaim"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// SSO kapalıyken de denetlenir (servis SSO'dan bağımsız).
			err := ValidateOIDCSettings(c.in, false)
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
			if c.in.PermissionServiceKey != "" && strings.Contains(err.Error(), c.in.PermissionServiceKey) {
				t.Fatal("hata metni anahtarı yankılıyor")
			}
		})
	}
}

func TestNormalizePermissionDefaultsAndClamp(t *testing.T) {
	for in, want := range map[int]int{0: 300, -5: 300, 30: 60, 60: 60, 120: 120, 3600: 3600, 9999: 3600} {
		if got := NormalizeOIDCSettings(OIDCSettings{PermissionTTLSeconds: in}, "").PermissionTTLSeconds; got != want {
			t.Errorf("TTL %d → %d, beklenen %d", in, got, want)
		}
	}
	n := NormalizeOIDCSettings(OIDCSettings{PermissionsClaim: "  ", PermissionServiceKey: "  " + testPermKey + " "}, "")
	if n.PermissionsClaim != PermissionClaimDefault || n.PermissionServiceKey != testPermKey || n.RoleFromClaim {
		t.Fatalf("varsayılanlar yanlış (roleFromClaim varsayılan KAPALI olmalı): %+v", n)
	}
}

func TestPermissionKeySavedNeverEchoedAndEmptyPreserves(t *testing.T) {
	st := newFakeOIDCStore()
	o := newDevService(t, st)
	if o.PermissionService().Usable() || o.PermissionKeyMatches("") {
		t.Fatal("varsayılan: servis kapalı olmalı")
	}
	in := OIDCSettings{PermissionServiceEnabled: true, PermissionServiceKey: testPermKey, PermissionTTLSeconds: 120}
	snap, err := o.SaveSettings(context.Background(), in) // SSO kapalı: keşif yok
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(snap)
	if strings.Contains(string(b), testPermKey) || !snap.PermissionServiceKeySet || snap.PermissionTTLSeconds != 120 {
		t.Fatalf("snapshot anahtar sızdırıyor ya da keySet yok: %s", b)
	}
	if s := fmt.Sprintf("%v %+v %#v", o.state().eff, o.state().eff, o.state().eff); strings.Contains(s, testPermKey) {
		t.Fatal("String/GoString anahtarı basıyor")
	}
	if eb, _ := json.Marshal(o.state().eff); strings.Contains(string(eb), testPermKey) {
		t.Fatal("OIDCSettings JSON'u anahtarı taşıyor")
	}
	// Boş anahtarlı ikinci kayıt: kayıtlı korunur, diğer alan yazılır.
	in.PermissionServiceKey, in.PermissionTTLSeconds = "", 600
	if _, err := o.SaveSettings(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if got := st.storedSettings(t); got.PermissionServiceKey != testPermKey || got.PermissionTTLSeconds != 600 {
		t.Fatalf("anahtar korunmadı / TTL yazılmadı: ttl=%d keySet=%v", got.PermissionTTLSeconds, got.PermissionServiceKey != "")
	}
	cfg := o.PermissionService()
	if !cfg.Usable() || cfg.TTLSeconds != 600 || !o.PermissionKeyMatches(testPermKey) || o.PermissionKeyMatches(testPermKey+"x") {
		t.Fatalf("servis yapılandırması yanlış: %+v", cfg)
	}
	// Peer pod blobdan aynı yapılandırmayı kurar.
	peer := newDevService(t, st)
	if !peer.PermissionKeyMatches(testPermKey) {
		t.Fatal("peer anahtarı blobdan okumadı")
	}
}

// ── Giriş akışı: claim → rol (yalnız ÇÖZÜM; yazım api oidcLoginUser'da) ─────

func TestExchangeRoleFromClaim(t *testing.T) {
	idp := newFakeIdP(t)
	o := loginService(t, idp)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		roleOn     bool
		claimName  string
		atClaims   map[string]any // nil → opak access token
		atForeign  bool           // JWKS'te olmayan anahtarla imzalı
		idClaims   map[string]any
		wantRole   string
		wantSource string
	}{
		{name: "KAPALI: claim var ama rol çözülmez (operatör kararı 2026-10-05)",
			atClaims: map[string]any{"permissions": []string{PermissionAdmin}},
			idClaims: map[string]any{"permissions": []string{PermissionAdmin}}},
		{name: "access token JWT → editor", roleOn: true,
			atClaims: map[string]any{"permissions": []string{PermissionEditor}},
			wantRole: RoleEditor, wantSource: "access_token"},
		{name: "sıra: viewer+admin → admin", roleOn: true,
			atClaims: map[string]any{"permissions": []string{PermissionViewer, PermissionAdmin}},
			wantRole: RoleAdmin, wantSource: "access_token"},
		{name: "geçerli JWT'de claim yok → id_token'a DÜŞÜLMEZ, rol değişmez", roleOn: true,
			atClaims: map[string]any{}, idClaims: map[string]any{"permissions": []string{PermissionAdmin}},
			wantSource: "access_token"},
		{name: "opak access token → id_token claim'i", roleOn: true,
			idClaims: map[string]any{"permissions": []string{PermissionViewer}},
			wantRole: RoleViewer, wantSource: "id_token"},
		{name: "opak + claim yok → rol değişmez", roleOn: true,
			wantSource: "id_token"},
		{name: "imzası doğrulanamayan JWT → id_token claim'i", roleOn: true,
			atClaims: map[string]any{"permissions": []string{PermissionAdmin}}, atForeign: true,
			idClaims: map[string]any{"permissions": []string{PermissionEditor}},
			wantRole: RoleEditor, wantSource: "id_token"},
		{name: "yalnız tanınmayan değer → rol değişmez", roleOn: true,
			atClaims:   map[string]any{"permissions": []string{"COREMETRY_ROLE_SVC_ORDERS"}},
			wantSource: "access_token"},
		{name: "boş dizi → rol değişmez", roleOn: true,
			atClaims: map[string]any{"permissions": []string{}}, wantSource: "access_token"},
		{name: "F2: tek dizgi claim yok sayılır", roleOn: true,
			atClaims:   map[string]any{"permissions": "COREMETRY_ADMIN"},
			wantSource: "access_token"},
		{name: "özel claim adı", roleOn: true, claimName: "cm_perms",
			atClaims: map[string]any{"cm_perms": []string{PermissionEditor}, "permissions": []string{PermissionAdmin}},
			wantRole: RoleEditor, wantSource: "access_token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := validInput(idp.URL)
			in.RoleFromClaim, in.PermissionsClaim = c.roleOn, c.claimName
			if _, err := o.SaveSettings(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			idp.set(func(f *fakeIdP) {
				f.claims, f.accessClaims, f.accessKey = c.idClaims, c.atClaims, nil
				if c.atForeign {
					f.accessKey = otherKey
				}
			})
			cl, err := o.Exchange(context.Background(), "code", "verifier", "")
			if err != nil {
				t.Fatalf("giriş: %v", err)
			}
			if cl.ClaimRole != c.wantRole || cl.ClaimRoleSource != c.wantSource {
				t.Fatalf("rol=%q kaynak=%q; beklenen %q/%q", cl.ClaimRole, cl.ClaimRoleSource, c.wantRole, c.wantSource)
			}
		})
	}
}

// N4 (güvenlik incelemesi): depodaki / içe aktarılmış blobda yetki servisi
// alanları geçersizse SSO DÜŞMEZ — yalnız servis kapanır, lastError yazılır.
func TestLoadPersistedBadPermissionFieldsKeepSSO(t *testing.T) {
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	good := validInput(idp.URL).stored()
	good.PermissionServiceEnabled, good.PermissionServiceKey = true, "short" // < 16
	good.RoleFromClaim, good.PermissionsClaim = true, "name"                 // standart claim
	raw, _ := json.Marshal(good)
	st.set(func(f *fakeOIDCStore) { f.rows[OIDCSettingsKey] = raw })
	o := NewOIDCService(devBoot, testPublicURL, st)
	if err := o.LoadPersisted(context.Background()); err != nil {
		t.Fatalf("SSO yüklenmeliydi: %v", err)
	}
	if !o.Enabled() {
		t.Fatal("geçersiz yetki alanları SSO'yu düşürdü")
	}
	if o.PermissionService().Usable() || o.PermissionKeyMatches("short") || o.effective().RoleFromClaim {
		t.Fatal("yetki servisi / claim'den rol kapanmadı")
	}
	snap := o.Snapshot()
	if !strings.Contains(snap.LastError, "yetki servisi") || strings.Contains(snap.LastError, "short") {
		t.Fatalf("lastError yanlış ya da anahtarı taşıyor: %q", snap.LastError)
	}
}
