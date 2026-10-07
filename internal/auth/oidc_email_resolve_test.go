package auth

// oidc_email_resolve_test.go — v0.10.1121 (operatör, prod: "[oidc] callback
// failed: class=email_missing"; id_token'da email yok, preferred_username =
// AD sicili).
//
// NE ÇİVİLİYOR (sahte IdP httptest + sahte dizin; dış ağ YOK): zincir sırası
// ve ilk isabette durma (id_token e-postası varken UserInfo çağrılmaz);
// UserInfo e-postası + email_verified kapısı; UserInfo sub ≠ id_token sub ⇒
// RED; usernameFallback KAPALI ⇒ eski davranış (email_missing); dizin e-postası
// DOĞRULANMIŞ sayılır (ViaTrust değil), alan adı listesi uygulanır; belirsiz /
// hatalı dizin ⇒ RED; dizin yok ya da e-postasız ⇒ ldap_username; claim adı
// doğrulama + yükleme savunması; başarısızlık logu claim ADLARINI taşır, değer
// asla.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var sprintf = fmt.Sprintf

const testUsername = "N0000001"

// fakeDir — OIDCDirectory sahtesi.
type fakeDir struct {
	enabled  bool
	mail     string
	notFound bool // F3: dizinde kayıt yok
	err      error
	calls    atomic.Int32
	got      atomic.Value
}

func (f *fakeDir) Enabled() bool { return f.enabled }
func (f *fakeDir) LookupEmailByUsername(_ context.Context, u string) (string, bool, error) {
	f.calls.Add(1)
	f.got.Store(u)
	if f.err != nil {
		return "", false, f.err
	}
	return f.mail, !f.notFound, nil
}

type ambErr struct{}

func (ambErr) Error() string    { return "ambiguous" }
func (ambErr) Ambiguous() bool  { return true }
func (ambErr) Category() string { return "ambiguous" }

// catErr — kategorili dizin hatası (ldap.LookupError'ın sahtesi); metin
// kullanıcı adı TAŞIR — loga girmemeli.
type catErr struct{ cat string }

func (e catErr) Error() string    { return "bind failed for N0000001 dn=CN=x" }
func (e catErr) Category() string { return e.cat }

// resolveService — e-postasız id_token (preferred_username dolu) üreten sahte
// IdP + ayar.
func resolveService(t *testing.T, mut func(*OIDCSettings)) (*OIDCService, *fakeIdP) {
	t.Helper()
	idp := newFakeIdP(t)
	idp.set(func(f *fakeIdP) {
		f.claims = map[string]any{"email": "", "email_verified": false, "preferred_username": testUsername}
	})
	o := newDevService(t, newFakeOIDCStore())
	in := validInput(idp.URL)
	in.AllowedDomains = []string{"corp.example.test"}
	if mut != nil {
		mut(&in)
	}
	if _, err := o.SaveSettings(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	return o, idp
}

func exchange(o *OIDCService) (*OIDCClaims, error) {
	return o.Exchange(context.Background(), "code", "verifier", "")
}

func TestResolveIDTokenEmailStopsChain(t *testing.T) {
	idp := newFakeIdP(t)
	o := loginService(t, idp)
	idp.set(func(f *fakeIdP) { f.userinfo = map[string]any{"sub": "u-1", "email": "other@example.test"} })
	cl, err := exchange(o)
	if err != nil || cl.Email != "user@example.test" || cl.EmailSource != OIDCEmailSourceIDToken {
		t.Fatalf("id_token e-postası: %+v %v", cl, err)
	}
	if n := idp.userinfoHits.Load(); n != 0 {
		t.Fatalf("id_token e-postası varken UserInfo çağrıldı (%d)", n)
	}
}

func TestResolveUserInfoEmail(t *testing.T) {
	o, idp := resolveService(t, nil)
	cases := []struct {
		name     string
		ui       map[string]any
		class    string
		verified bool
	}{
		{"doğrulanmış", map[string]any{"sub": "u-1", "email": "first.last@corp.example.test", "email_verified": true}, "", true},
		{"email_verified yok → id_token false taşınır (F5)", map[string]any{"sub": "u-1", "email": "first.last@corp.example.test"}, "email_unverified", false},
		{`"true" dizgisi`, map[string]any{"sub": "u-1", "email": "first.last@corp.example.test", "email_verified": "true"}, "", true},
		{"doğrulanmamış + güven kapalı", map[string]any{"sub": "u-1", "email": "first.last@corp.example.test", "email_verified": false}, "email_unverified", false},
		{"alan adı dışı", map[string]any{"sub": "u-1", "email": "first.last@evil.test", "email_verified": true}, "email_domain_denied", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			idp.set(func(f *fakeIdP) { f.userinfo = c.ui })
			cl, err := exchange(o)
			if c.class != "" {
				if OIDCLoginErrorClass(err) != c.class {
					t.Fatalf("%s bekleniyordu: %v", c.class, err)
				}
				return
			}
			if err != nil || cl.Email != "first.last@corp.example.test" || cl.EmailSource != OIDCEmailSourceUserInfo ||
				cl.EmailVerified != c.verified || cl.ViaTrust {
				t.Fatalf("UserInfo e-postası: %+v %v", cl, err)
			}
		})
	}
}

func TestResolveUserInfoSubMismatchRejected(t *testing.T) {
	dir := &fakeDir{enabled: true, mail: "first.last@corp.example.test"}
	o, idp := resolveService(t, func(s *OIDCSettings) { s.UsernameFallback = true })
	o.SetDirectory(dir)
	for _, sub := range []string{"u-2", ""} {
		idp.set(func(f *fakeIdP) {
			f.userinfo = map[string]any{"sub": sub, "email": "first.last@corp.example.test", "preferred_username": "n0000002"}
		})
		if _, err := exchange(o); OIDCLoginErrorClass(err) != "userinfo_sub_mismatch" {
			t.Fatalf("sub=%q: userinfo_sub_mismatch bekleniyordu: %v", sub, err)
		}
	}
	if dir.calls.Load() != 0 {
		t.Fatal("sub uyuşmazlığında zincir sürdü (dizin arandı)")
	}
}

func TestResolveFallbackOffKeepsEmailMissing(t *testing.T) {
	dir := &fakeDir{enabled: true, mail: "first.last@corp.example.test"}
	o, idp := resolveService(t, nil) // usernameFallback varsayılan KAPALI
	o.SetDirectory(dir)
	idp.set(func(f *fakeIdP) { f.userinfo = map[string]any{"sub": "u-1"} }) // e-postasız UserInfo
	buf := captureLog(t)
	if _, err := exchange(o); OIDCLoginErrorClass(err) != "email_missing" {
		t.Fatalf("anahtar kapalıyken email_missing bekleniyordu: %v", err)
	}
	if dir.calls.Load() != 0 {
		t.Fatal("anahtar kapalıyken dizin arandı")
	}
	out := buf.String()
	if !strings.Contains(out, "[oidc] email resolution failed: class=email_missing tried=id_token,userinfo claims=") ||
		!strings.Contains(out, "preferred_username") || !strings.Contains(out, "email_verified") {
		t.Fatalf("başarısızlık logu eksik: %s", out)
	}
	if strings.Contains(strings.ToLower(out), strings.ToLower(testUsername)) || strings.Contains(out, "u-1") {
		t.Fatalf("log claim DEĞERİ taşıyor: %s", out)
	}
	// UserInfo ucu hata verirse (404) yumuşak geçilir, adım işaretlenir.
	idp.set(func(f *fakeIdP) { f.userinfo = nil })
	buf.Reset()
	if _, err := exchange(o); OIDCLoginErrorClass(err) != "email_missing" ||
		!strings.Contains(buf.String(), "tried=id_token,userinfo(error)") {
		t.Fatalf("UserInfo hatası yumuşak olmalı: %v %s", err, buf.String())
	}
}

func TestResolveUsernameViaDirectory(t *testing.T) {
	o, idp := resolveService(t, func(s *OIDCSettings) { s.UsernameFallback = true })
	_ = idp
	cases := []struct {
		name   string
		dir    *fakeDir
		class  string
		source string
		email  string
	}{
		{"dizin e-postası → doğrulanmış", &fakeDir{enabled: true, mail: "First.Last@corp.example.test"}, "", OIDCEmailSourceLDAP, "first.last@corp.example.test"},
		{"belirsiz → red", &fakeDir{enabled: true, err: ambErr{}}, "username_ambiguous", "", ""},
		{"dizin hatası → red", &fakeDir{enabled: true, err: errors.New("dial")}, "directory_lookup_failed", "", ""},
		{"alan adı dışı → red", &fakeDir{enabled: true, mail: "first.last@other.example.test"}, "email_domain_denied", "", ""},
		{"dizinde e-posta yok → ldap_username", &fakeDir{enabled: true}, "", OIDCEmailSourceLdapUsername, ""},
		{"dizin açık + kayıt yok → email_missing (bayat satıra düşülmez)", &fakeDir{enabled: true, notFound: true}, "email_missing", "", ""},
		{"dizin kapalı → ldap_username", &fakeDir{enabled: false, mail: "x@corp.example.test"}, "", OIDCEmailSourceLdapUsername, ""},
		{"dizin yok → ldap_username", nil, "", OIDCEmailSourceLdapUsername, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.dir != nil {
				o.SetDirectory(c.dir)
			} else {
				o.SetDirectory(nil)
			}
			cl, err := exchange(o)
			if c.class != "" {
				if OIDCLoginErrorClass(err) != c.class {
					t.Fatalf("%s bekleniyordu: %v", c.class, err)
				}
				return
			}
			if err != nil || cl.EmailSource != c.source || cl.Email != c.email || cl.ViaTrust || !cl.ViaUsername ||
				cl.Username != strings.ToLower(testUsername) {
				t.Fatalf("beklenmeyen sonuç: %+v %v", cl, err)
			}
			if c.source == OIDCEmailSourceLDAP {
				if !cl.EmailVerified {
					t.Fatal("dizin e-postası doğrulanmış sayılmalı")
				}
				if got, _ := c.dir.got.Load().(string); got != testUsername {
					t.Fatalf("dizine giden kullanıcı adı %q", got)
				}
			}
			if c.source == OIDCEmailSourceLdapUsername && (len(cl.ResolutionTried) == 0 || len(cl.ClaimNames) == 0) {
				t.Fatalf("başarısızlık logu için adım/claim adı taşınmalı: %+v", cl)
			}
		})
	}
}

func TestResolveUsernameClaimSelection(t *testing.T) {
	dir := &fakeDir{enabled: true, mail: "first.last@corp.example.test"}
	o, idp := resolveService(t, func(s *OIDCSettings) { s.UsernameFallback = true; s.UsernameClaim = "upn" })
	o.SetDirectory(dir)
	// Yalnız yapılandırılan claim okunur: preferred_username dolu ama upn yok → email_missing.
	// (Claim adı "upn" olsa da değerde `@` olamaz — F1.)
	if _, err := exchange(o); OIDCLoginErrorClass(err) != "email_missing" || dir.calls.Load() != 0 {
		t.Fatalf("yapılandırılmamış claim kullanıldı: %v calls=%d", err, dir.calls.Load())
	}
	// upn sub'ı eşleşen UserInfo'dan.
	idp.set(func(f *fakeIdP) { f.userinfo = map[string]any{"sub": "u-1", "upn": "n0000001"} })
	if cl, err := exchange(o); err != nil || cl.EmailSource != OIDCEmailSourceLDAP {
		t.Fatalf("UserInfo'daki claim kullanılmalı: %+v %v", cl, err)
	}
	// ASCII dışı / kontrol karakterli kullanıcı adı geçersiz.
	for _, bad := range []string{"Kate", "a\nb", strings.Repeat("a", 300), "n0000001@corp.example.test"} {
		idp.set(func(f *fakeIdP) {
			f.userinfo = nil
			f.claims = map[string]any{"email": "", "upn": bad}
		})
		before := dir.calls.Load()
		if _, err := exchange(o); OIDCLoginErrorClass(err) != "email_missing" || dir.calls.Load() != before {
			t.Fatalf("geçersiz kullanıcı adı %q kabul edildi: %v", bad, err)
		}
	}
}

func TestValidateUsernameClaim(t *testing.T) {
	for _, ok := range []string{"", "preferred_username", "upn", "sAMAccountName", "registrationNumber", "urn:x.y-z"} {
		if err := validateUsernameClaim(ok); err != nil {
			t.Errorf("%q reddedildi: %v", ok, err)
		}
	}
	for _, bad := range []string{"email", "Email", "name", "nickname", "given_name", "aud", "nonce", "a b", "x/y", strings.Repeat("a", 65)} {
		var se *OIDCSettingsError
		if err := validateUsernameClaim(bad); !errors.As(err, &se) || se.Field != "usernameClaim" {
			t.Errorf("%q kabul edildi: %v", bad, err)
		}
	}
	if s := NormalizeOIDCSettings(OIDCSettings{}, ""); s.UsernameClaim != UsernameClaimDefault || s.UsernameFallback {
		t.Fatalf("varsayılanlar: %+v", s)
	}
	in := validInput("https://idp.example.test")
	in.UsernameClaim = "email"
	if err := ValidateOIDCSettings(NormalizeOIDCSettings(in, ""), false); err == nil {
		t.Fatal("yasaklı claim adı kaydedilebildi")
	}
	in.Enabled = false // SSO kapalıyken de denetlenir (blob'a çöp yazılmasın)
	if err := ValidateOIDCSettings(NormalizeOIDCSettings(in, ""), false); err == nil {
		t.Fatal("SSO kapalıyken yasaklı claim adı kaydedilebildi")
	}
}

func TestUsernameFallbackSettingsRoundTripAndBadBlob(t *testing.T) {
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	o := newDevService(t, st)
	in := validInput(idp.URL)
	in.UsernameFallback, in.UsernameClaim = true, "upn"
	snap, err := o.SaveSettings(context.Background(), in)
	if err != nil || !snap.UsernameFallback || snap.UsernameClaim != "upn" {
		t.Fatalf("kayıt: %+v %v", snap, err)
	}
	if s := st.storedSettings(t); !s.UsernameFallback || s.UsernameClaim != "upn" {
		t.Fatalf("blob: %+v", s)
	}
	if c := o.client(); c == nil || !c.usernameFallback || c.usernameClaim != "upn" {
		t.Fatal("canlı istemciye uygulanmadı")
	}
	// Elle yazılmış geçersiz claim: SSO düşmez, eşleştirme kapanır.
	st.set(func(f *fakeOIDCStore) {
		f.rows[OIDCSettingsKey] = []byte(strings.Replace(string(f.rows[OIDCSettingsKey]), `"usernameClaim":"upn"`, `"usernameClaim":"email"`, 1))
	})
	_ = o.LoadPersisted(context.Background())
	snap = o.Snapshot()
	if !snap.Active || snap.UsernameFallback || snap.UsernameClaim != UsernameClaimDefault ||
		!strings.Contains(snap.LastError, "usernameClaim geçersiz") {
		t.Fatalf("geçersiz blob savunması: %+v", snap)
	}
}

// F5 — UserInfo email_verified göndermiyorsa id_token'daki değer taşınır.
func TestResolveUserInfoCarriesIDTokenEmailVerified(t *testing.T) {
	o, idp := resolveService(t, nil) // id_token: email_verified=false
	idp.set(func(f *fakeIdP) { f.userinfo = map[string]any{"sub": "u-1", "email": "first.last@corp.example.test"} })
	if _, err := exchange(o); OIDCLoginErrorClass(err) != "email_unverified" {
		t.Fatalf("id_token false taşınmalı → email_unverified: %v", err)
	}
	// UserInfo kendi değerini gönderirse o kullanılır.
	idp.set(func(f *fakeIdP) {
		f.userinfo = map[string]any{"sub": "u-1", "email": "first.last@corp.example.test", "email_verified": true}
	})
	if cl, err := exchange(o); err != nil || !cl.EmailVerified {
		t.Fatalf("UserInfo true: %+v %v", cl, err)
	}
	// id_token'da da yoksa eski davranış (claim yok = kabul, doğrulanmamış).
	idp.set(func(f *fakeIdP) {
		f.claims = map[string]any{"email": "", "preferred_username": testUsername}
		f.userinfo = map[string]any{"sub": "u-1", "email": "first.last@corp.example.test"}
	})
	if cl, err := exchange(o); err != nil || cl.EmailVerified || cl.EmailSource != OIDCEmailSourceUserInfo {
		t.Fatalf("iki tarafta da yok: %+v %v", cl, err)
	}
}

// F6 — dizin hatası kategori başına seyreltilmiş tek satır; kullanıcı adı /
// hata metni yok.
func TestDirectoryFailureLogCategorized(t *testing.T) {
	dirFailLog.Range(func(k, _ any) bool { dirFailLog.Delete(k); return true })
	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, strings.TrimSpace(sprintf(f, a...))) }
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	logDirectoryFailure(catErr{"bind"}, now, logf)
	logDirectoryFailure(catErr{"bind"}, now.Add(10*time.Second), logf) // seyreltildi
	logDirectoryFailure(catErr{"timeout"}, now, logf)
	logDirectoryFailure(ambErr{}, now, logf)
	logDirectoryFailure(errors.New("raw N0000001"), now, logf)
	logDirectoryFailure(catErr{"evil\nline"}, now.Add(30*time.Second), logf) // bilinmeyen kategori → other (seyreltildi)
	logDirectoryFailure(catErr{"bind"}, now.Add(2*time.Minute), logf)
	want := []string{
		"[oidc] directory lookup failed: category=bind",
		"[oidc] directory lookup failed: category=timeout",
		"[oidc] directory lookup failed: category=ambiguous",
		"[oidc] directory lookup failed: category=other",
		"[oidc] directory lookup failed: category=bind",
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("satırlar:\n%v", lines)
	}
	// Uçtan uca: Exchange dizin hatasında satırı bırakır, kullanıcı adını değil.
	dirFailLog.Range(func(k, _ any) bool { dirFailLog.Delete(k); return true })
	o, _ := resolveService(t, func(s *OIDCSettings) { s.UsernameFallback = true })
	o.SetDirectory(&fakeDir{enabled: true, err: catErr{"search"}})
	buf := captureLog(t)
	if _, err := exchange(o); OIDCLoginErrorClass(err) != "directory_lookup_failed" {
		t.Fatalf("directory_lookup_failed bekleniyordu: %v", err)
	}
	if !strings.Contains(buf.String(), "category=search") || strings.Contains(strings.ToLower(buf.String()), "n0000001") {
		t.Fatalf("log: %s", buf.String())
	}
}

// F2 — usernameFallbackAllowAdmin yalnız eşleştirme açıkken; canlı istemciye
// uygulanır; blob/snapshot gidiş-dönüşü.
func TestUsernameFallbackAllowAdminSetting(t *testing.T) {
	if s := NormalizeOIDCSettings(OIDCSettings{UsernameFallbackAllowAdmin: true}, ""); s.UsernameFallbackAllowAdmin {
		t.Fatal("eşleştirme kapalıyken admin açık-seçimi düşmeli")
	}
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	o := newDevService(t, st)
	in := validInput(idp.URL)
	in.UsernameFallback, in.UsernameFallbackAllowAdmin = true, true
	snap, err := o.SaveSettings(context.Background(), in)
	if err != nil || !snap.UsernameFallbackAllowAdmin || !o.UsernameFallbackAllowAdmin() {
		t.Fatalf("açık-seçim: %+v %v", snap, err)
	}
	if s := st.storedSettings(t); !s.UsernameFallbackAllowAdmin {
		t.Fatalf("blob: %+v", s)
	}
	in.UsernameFallback = false
	if snap, err := o.SaveSettings(context.Background(), in); err != nil || snap.UsernameFallbackAllowAdmin || o.UsernameFallbackAllowAdmin() {
		t.Fatalf("eşleştirme kapanınca admin açık-seçimi düşmeli: %+v %v", snap, err)
	}
	var nilSvc *OIDCService
	if nilSvc.UsernameFallbackAllowAdmin() {
		t.Fatal("nil servis")
	}
}
