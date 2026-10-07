package ldap

// oidc_lookup.go — v0.10.1121 (operatör, prod: "[oidc] callback failed:
// class=email_missing"). Kurumsal IdP (LDAP federasyonlu Keycloak) id_token'a
// `email` koymuyor ama `preferred_username` = AD sAMAccountName (ör.
// "n0000001") gönderiyor. OIDC callback'in e-posta çözüm zincirinin 3. adımı
// (auth/oidc_email_resolve.go) bu kullanıcı adını servis hesabıyla dizinde
// arar ve e-postayı DİZİNDEN alır — LDAP girişinin kullanıcıyı açtığı
// özniteliğin aynısıyla (EmailAttribute; boşsa `mail`), böylece aynı satıra
// düşer.
//
// SINIRLAR (güvenlik incelemesi F1): TEK öznitelikte tam eşleşme — ayarlı
// UserAttribute, yoksa sAMAccountName. `mail` / `userPrincipalName` (ve
// bilinen benzersiz olmayan öznitelikler: cn, displayName, name …) eşleşme
// anahtarı OLAMAZ → sAMAccountName'e düşülür; `@` içeren kullanıcı adı hiç
// aranmaz (UPN/e-posta biçimi başka bir hesaba çarpışmasın). sAMAccountName
// (AD) aramasında devre dışı hesaplar dışlanır
// (userAccountControl ACCOUNTDISABLE biti). Filtre kaçışlı, sizeLimit 2
// (ikinci kayıt = BELİRSİZ ⇒ hata; keyfî ilk kayıt asla seçilmez), timeLimit
// 5 s ve çağrının tamamı ≤ oidcLookupTimeout. Log satırı kullanıcı adı /
// e-posta TAŞIMAZ; hata kategorisi (bind / timeout / search / ambiguous)
// Category() ile auth katmanına verilir (F6).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
)

// oidcLookupTimeout — dizin aramasının toplam süresi (bağlan + bind + arama).
const oidcLookupTimeout = 5 * time.Second

// adDisabledClause — AD userAccountControl ACCOUNTDISABLE (0x2) biti
// (LDAP_MATCHING_RULE_BIT_AND). Yalnız sAMAccountName (AD) aramasına eklenir:
// şemasında userAccountControl olmayan dizinde (OpenLDAP) filtre "Undefined"
// olur ve hiçbir kaydı döndürmezdi.
const adDisabledClause = "(!(userAccountControl:1.2.840.113556.1.4.803:=2))"

// Hata kategorileri (F6) — auth katmanı loglar; kullanıcı adı taşımaz.
const (
	LookupCatBind      = "bind"
	LookupCatTimeout   = "timeout"
	LookupCatSearch    = "search"
	LookupCatAmbiguous = "ambiguous"
)

// LookupError — kategorili dizin hatası; alttaki hata Unwrap ile kodda kalır.
type LookupError struct {
	Cat string
	err error
}

func (e *LookupError) Error() string    { return "ldap username lookup failed: " + e.Cat }
func (e *LookupError) Unwrap() error    { return e.err }
func (e *LookupError) Category() string { return e.Cat }

// AmbiguousUserError — kullanıcı adı birden çok dizin kaydına eşleşti.
// auth paketi ldap'ı import etmez; Ambiguous() arayüzüyle tanır.
type AmbiguousUserError struct{ N int }

func (e *AmbiguousUserError) Error() string {
	return fmt.Sprintf("ldap: username matched %d directory entries (ambiguous)", e.N)
}

// Ambiguous — auth.OIDCDirectory sözleşmesi (errors.As ile arayüz).
func (e *AmbiguousUserError) Ambiguous() bool { return true }

// Category — F6 log kategorisi.
func (e *AmbiguousUserError) Category() string { return LookupCatAmbiguous }

// singleSearcher — *goldap.Conn karşılar; test sahte dizin verir.
type singleSearcher interface {
	Search(req *goldap.SearchRequest) (*goldap.SearchResult, error)
}

// validAttrName — LDAP öznitelik adı (AttributeDescription, seçeneksiz). SAF.
func validAttrName(a string) bool {
	if a == "" || len(a) > 64 {
		return false
	}
	for i := 0; i < len(a); i++ {
		b := a[i]
		if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-') {
			return false
		}
	}
	return true
}

// nonUniqueMatchAttrs — eşleşme anahtarı OLAMAYAN öznitelikler (F1): e-posta
// biçimliler (mail / UPN — kullanıcı adı ≠ e-posta, ve IdP kullanıcısının
// e-postası başka bir hesabın UPN'ine çarpabilir) ve bilinen benzersiz
// olmayan ad öznitelikleri.
var nonUniqueMatchAttrs = map[string]bool{
	"mail": true, "userprincipalname": true, "proxyaddresses": true, "othermailbox": true,
	"cn": true, "name": true, "displayname": true, "givenname": true, "sn": true,
	"description": true, "department": true, "ou": true, "o": true, "company": true,
}

// oidcMatchAttr — tek eşleşme özniteliği: ayarlı UserAttribute geçerli ve
// benzersiz-nitelikliyse o, değilse sAMAccountName. SAF.
func oidcMatchAttr(c Config) string {
	ua := strings.TrimSpace(c.UserAttribute)
	if validAttrName(ua) && !nonUniqueMatchAttrs[strings.ToLower(ua)] {
		return ua
	}
	return "sAMAccountName"
}

// oidcUsernameFilter — TEK öznitelikte TAM eşleşme; username ÇAĞIRANDA
// kaçışlanmış gelir. AD (sAMAccountName) aramasında devre dışı hesap
// dışlanır. SAF.
func oidcUsernameFilter(c Config, escaped string) string {
	attr := oidcMatchAttr(c)
	f := "(&(objectClass=person)(" + attr + "=" + escaped + ")"
	if strings.EqualFold(attr, "sAMAccountName") {
		f += adDisabledClause
	}
	return f + ")"
}

// oidcEmailAttrs — okunacak e-posta öznitelikleri, öncelik sırasıyla:
// LDAP girişinin kullandığı EmailAttribute, sonra `mail`. SAF.
func oidcEmailAttrs(c Config) []string {
	out := []string{}
	if ea := strings.TrimSpace(c.EmailAttribute); validAttrName(ea) {
		out = append(out, ea)
	}
	if len(out) == 0 || !strings.EqualFold(out[0], "mail") {
		out = append(out, "mail")
	}
	return out
}

// lookupEmailWith — tek arama + belirsizlik kapısı. found=false: kayıt yok;
// found=true, mail="": kayıt var ama e-postası yok. '@' içermeyen değer
// e-posta sayılmaz.
func lookupEmailWith(conn singleSearcher, c Config, username string) (mail string, found bool, err error) {
	attrs := oidcEmailAttrs(c)
	req := goldap.NewSearchRequest(
		c.BaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
		2, int(oidcLookupTimeout/time.Second), false,
		oidcUsernameFilter(c, goldap.EscapeFilter(username)),
		append([]string{"dn"}, attrs...),
		nil,
	)
	res, err := conn.Search(req)
	if err != nil {
		// Boyut sınırı aşıldı = en az iki kayıt = belirsiz.
		if goldap.IsErrorWithCode(err, goldap.LDAPResultSizeLimitExceeded) {
			n := 2
			if res != nil && len(res.Entries) > n {
				n = len(res.Entries)
			}
			return "", false, &AmbiguousUserError{N: n}
		}
		cat := LookupCatSearch
		if goldap.IsErrorWithCode(err, goldap.LDAPResultTimeLimitExceeded) || goldap.IsErrorWithCode(err, goldap.ErrorNetwork) {
			cat = LookupCatTimeout
		}
		return "", false, &LookupError{Cat: cat, err: err}
	}
	if res == nil || len(res.Entries) == 0 {
		return "", false, nil
	}
	if len(res.Entries) > 1 {
		return "", false, &AmbiguousUserError{N: len(res.Entries)}
	}
	e := res.Entries[0]
	for _, a := range attrs {
		v := strings.ToLower(strings.TrimSpace(e.GetAttributeValue(a)))
		if strings.Contains(v, "@") && !strings.ContainsAny(v, " \t\r\n") {
			return v, true, nil
		}
	}
	return "", true, nil
}

// lookupDial — YALNIZ test dikişi; üretimde servis hesabıyla bağlanır.
var lookupDial = func(c Config) (interface {
	singleSearcher
	Close() error
}, error) {
	return bindAdmin(c)
}

// LookupEmailByUsername — auth.OIDCDirectory: OIDC kullanıcı adını servis
// hesabıyla dizinde arar. found=false = kayıt yok (`@` içeren ad hiç
// aranmaz); found=true + mail="" = kayıt var, e-postası yok;
// *AmbiguousUserError = birden çok kayıt; *LookupError = bind / timeout /
// search. Toplam ≤5 s.
func (s *Service) LookupEmailByUsername(ctx context.Context, username string) (mail string, found bool, err error) {
	if !s.Enabled() {
		return "", false, &LookupError{Cat: LookupCatBind, err: errors.New("ldap not enabled")}
	}
	username = strings.TrimSpace(username)
	if username == "" || strings.Contains(username, "@") {
		return "", false, nil
	}
	c := s.rawConfig()
	c.Normalize()
	ctx, cancel := context.WithTimeout(ctx, oidcLookupTimeout)
	defer cancel()
	type result struct {
		mail  string
		found bool
		err   error
	}
	dial := lookupDial         // gorutin dışında okunur (test dikişi yarışsız)
	ch := make(chan result, 1) // tamponlu: zaman aşımında gorutin sızmaz, bağlantı süreleriyle biter
	go func() {
		conn, err := dial(c)
		if err != nil {
			ch <- result{err: &LookupError{Cat: LookupCatBind, err: err}}
			return
		}
		defer conn.Close()
		m, f, err := lookupEmailWith(conn, c, username)
		ch <- result{m, f, err}
	}()
	select {
	case r := <-ch:
		return r.mail, r.found, r.err
	case <-ctx.Done():
		return "", false, &LookupError{Cat: LookupCatTimeout, err: ctx.Err()}
	}
}
