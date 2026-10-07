package ldap

// oidc_lookup_test.go — v0.10.1121 (OIDC e-posta çözüm zinciri, kullanıcı
// adı → dizin e-postası). NE ÇİVİLİYOR: filtre TEK öznitelikte TAM eşleşme +
// kaçışlı (joker / enjeksiyon yok), UPN/mail/benzersiz olmayan öznitelik
// eşleşme anahtarı olamaz, AD'de devre dışı hesap dışlanır (F1); `@` içeren
// ad aranmaz; sizeLimit 2; 0 kayıt → bulunamadı, e-postasız kayıt → bulundu
// ama e-posta yok (F3); iki kayıt ya da boyut sınırı hatası BELİRSİZ; hata
// kategorileri bind / timeout / search / ambiguous (F6); EmailAttribute önce,
// sonra mail; '@' içermeyen değer e-posta sayılmaz.

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
)

type fakeSingle struct {
	res  *goldap.SearchResult
	err  error
	last *goldap.SearchRequest
}

func (f *fakeSingle) Search(req *goldap.SearchRequest) (*goldap.SearchResult, error) {
	f.last = req
	return f.res, f.err
}

func (f *fakeSingle) Close() error { return nil }

func category(err error) string {
	var c interface{ Category() string }
	if errors.As(err, &c) {
		return c.Category()
	}
	return ""
}

func TestOIDCUsernameFilterSingleAttrEscaped(t *testing.T) {
	got := oidcUsernameFilter(Config{}, goldap.EscapeFilter("n0000001*)(mail=*"))
	want := `(&(objectClass=person)(sAMAccountName=n0000001\2a\29\28mail=\2a)` + adDisabledClause + `)`
	if got != want {
		t.Fatalf("filtre:\n got %s\nwant %s", got, want)
	}
	if strings.Contains(got, "userPrincipalName") || strings.Contains(got, "(|") {
		t.Fatalf("UPN OR'u kalmamalı: %s", got)
	}
	// Ayarlı benzersiz öznitelik (OpenLDAP uid) — AD devre dışı koşulu yok.
	if f := oidcUsernameFilter(Config{UserAttribute: "uid"}, "x"); f != "(&(objectClass=person)(uid=x))" {
		t.Fatalf("uid: %s", f)
	}
	// E-posta biçimli / benzersiz olmayan / geçersiz öznitelik → sAMAccountName.
	for _, ua := range []string{"mail", "userPrincipalName", "UPN", "cn", "displayName", "bad attr)", ""} {
		attr := oidcMatchAttr(Config{UserAttribute: ua})
		if ua == "UPN" {
			if attr != "UPN" { // bilinmeyen ama geçerli ad: operatörün seçimi
				t.Errorf("UPN: %s", attr)
			}
			continue
		}
		if attr != "sAMAccountName" {
			t.Errorf("UserAttribute=%q → %s", ua, attr)
		}
	}
}

func TestLookupEmailWith(t *testing.T) {
	c := Config{BaseDN: "DC=corp,DC=example,DC=test", EmailAttribute: "mail"}
	one := func(attrs map[string][]string) *goldap.SearchResult {
		return &goldap.SearchResult{Entries: []*goldap.Entry{entry("CN=N0000001,DC=corp,DC=example,DC=test", attrs)}}
	}
	cases := []struct {
		name  string
		cfg   Config
		res   *goldap.SearchResult
		err   error
		mail  string
		found bool
		cat   string // "" = hata yok
	}{
		{"tek kayıt + mail", c, one(map[string][]string{"mail": {"First.Last@corp.example.test"}}), nil, "first.last@corp.example.test", true, ""},
		{"kayıt yok → bulunamadı", c, &goldap.SearchResult{}, nil, "", false, ""},
		{"e-postasız kayıt → bulundu", c, one(map[string][]string{"sAMAccountName": {"N0000001"}}), nil, "", true, ""},
		{"@ içermeyen değer", c, one(map[string][]string{"mail": {"N0000001"}}), nil, "", true, ""},
		{"iki kayıt → belirsiz", c, &goldap.SearchResult{Entries: []*goldap.Entry{entry("a", nil), entry("b", nil)}}, nil, "", false, LookupCatAmbiguous},
		{"boyut sınırı → belirsiz", c, &goldap.SearchResult{Entries: []*goldap.Entry{entry("a", nil)}},
			goldap.NewError(goldap.LDAPResultSizeLimitExceeded, errors.New("size")), "", false, LookupCatAmbiguous},
		{"süre sınırı → timeout", c, nil, goldap.NewError(goldap.LDAPResultTimeLimitExceeded, errors.New("t")), "", false, LookupCatTimeout},
		{"dizin hatası → search", c, nil, errors.New("boom"), "", false, LookupCatSearch},
		{"EmailAttribute önce (LDAP girişiyle aynı satır)", Config{EmailAttribute: "userPrincipalName"},
			one(map[string][]string{"userPrincipalName": {"n0000001@corp.example.test"}, "mail": {"first.last@corp.example.test"}}),
			nil, "n0000001@corp.example.test", true, ""},
		{"EmailAttribute boşsa mail", Config{EmailAttribute: "userPrincipalName"},
			one(map[string][]string{"mail": {"first.last@corp.example.test"}}), nil, "first.last@corp.example.test", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSingle{res: tc.res, err: tc.err}
			got, found, err := lookupEmailWith(f, tc.cfg, "N0000001")
			if tc.cat != "" {
				if category(err) != tc.cat {
					t.Fatalf("kategori %q bekleniyordu: %v", tc.cat, err)
				}
				if tc.cat == LookupCatAmbiguous {
					var a *AmbiguousUserError
					if !errors.As(err, &a) || !a.Ambiguous() {
						t.Fatalf("Ambiguous arayüzü yok: %v", err)
					}
				}
			} else if err != nil || got != tc.mail || found != tc.found {
				t.Fatalf("got %q found=%v %v, want %q found=%v", got, found, err, tc.mail, tc.found)
			}
			if f.last == nil || f.last.SizeLimit != 2 || f.last.TimeLimit != 5 || f.last.Scope != goldap.ScopeWholeSubtree {
				t.Fatalf("arama sınırları: %+v", f.last)
			}
		})
	}
}

func TestLookupEmailByUsernameDisabledDialAndAt(t *testing.T) {
	s := New()
	if _, _, err := s.LookupEmailByUsername(context.Background(), "n0000001"); err == nil {
		t.Fatal("dizin kapalıyken hata bekleniyordu")
	}
	s.Configure(Config{Enabled: true, Host: "ldap.example.test", BaseDN: "DC=example,DC=test", EmailAttribute: "mail"})
	prev := lookupDial
	t.Cleanup(func() { lookupDial = prev })
	var dials atomic.Int32
	lookupDial = func(Config) (interface {
		singleSearcher
		Close() error
	}, error) {
		dials.Add(1)
		return &fakeSingle{res: &goldap.SearchResult{Entries: []*goldap.Entry{
			entry("CN=x", map[string][]string{"mail": {"first.last@example.test"}})}}}, nil
	}
	if m, found, err := s.LookupEmailByUsername(context.Background(), " n0000001 "); err != nil || !found || m != "first.last@example.test" {
		t.Fatalf("got %q %v %v", m, found, err)
	}
	// `@` içeren kullanıcı adı hiç aranmaz (UPN/e-posta biçimi).
	before := dials.Load()
	if m, found, err := s.LookupEmailByUsername(context.Background(), "n0000001@corp.example.test"); err != nil || found || m != "" || dials.Load() != before {
		t.Fatalf("@ içeren ad arandı: %q %v %v", m, found, err)
	}
	// Bağlantı/bind hatası → bind kategorisi.
	lookupDial = func(Config) (interface {
		singleSearcher
		Close() error
	}, error) {
		return nil, errors.New("ldap bind: invalid credentials")
	}
	if _, _, err := s.LookupEmailByUsername(context.Background(), "n0000001"); category(err) != LookupCatBind {
		t.Fatalf("bind kategorisi bekleniyordu: %v", err)
	}
	// Süre aşımı: bağlantı hiç dönmezse çağrı bağlamla biter.
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	lookupDial = func(Config) (interface {
		singleSearcher
		Close() error
	}, error) {
		<-block
		return nil, errors.New("late")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.LookupEmailByUsername(ctx, "n0000001"); category(err) != LookupCatTimeout {
		t.Fatalf("zaman aşımı bekleniyordu: %v", err)
	}
}
