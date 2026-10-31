package ldap

// orgchart.go — v0.10.1733 (kullanıcı detay sayfası S3, operatör onayı
// 2026-10-31): "AD ile denetle" (kullanıcıyı servis hesabıyla yeniden oku),
// Dizin sekmesi (ham öznitelikler) ve Organizasyon kartı (yönetici zinciri
// en çok 4 üst + doğrudan bağlılar ≤50).
//
// Maliyet: denetim/dizin = 1 arama; organizasyon = 1 (kullanıcı) + ≤4
// (base-scope yönetici okuması) + 1 (manager=<dn> araması, boyut sınırı 50).
// Hepsi tek bağlantı, admin bind. Çağıran (api) sonucu önbelleğe alır.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"unicode/utf8"

	goldap "github.com/go-ldap/ldap/v3"
)

const (
	// OrgMaxManagers — yönetici zinciri tavanı (operatör Q2: 4 üst yeter).
	OrgMaxManagers = 4
	// OrgMaxReports — doğrudan bağlı tavanı.
	OrgMaxReports = 50
)

// DirectoryPerson — organizasyon düğümü.
type DirectoryPerson struct {
	DN         string `json:"dn"`
	Name       string `json:"name"`
	Title      string `json:"title,omitempty"`
	Department string `json:"department,omitempty"`
	Email      string `json:"email,omitempty"`
	Username   string `json:"username,omitempty"`
	EmployeeID string `json:"employeeId,omitempty"`
}

// OrgChart — kullanıcı + yönetici zinciri (en yakın önce) + doğrudan bağlılar.
type OrgChart struct {
	Self             DirectoryPerson   `json:"self"`
	Managers         []DirectoryPerson `json:"managers"`
	Reports          []DirectoryPerson `json:"reports"`
	ReportsTruncated bool              `json:"reportsTruncated,omitempty"`
}

// DirectoryRead — tek kullanıcının yeniden okunması: kimlik projeksiyonu
// (giriş yolunun yazdığı alanlar) + okunabilen bütün öznitelikler.
type DirectoryRead struct {
	User       LDAPUser            `json:"user"`
	Attributes map[string][]string `json:"attributes"`
}

// ErrDirectoryUserNotFound — kullanıcı dizinde yok (silinmiş / kapsam dışı).
var ErrDirectoryUserNotFound = errors.New("user not found in directory")

// entryAttributes — girişin TÜM öznitelikleri; ikili değerler (foto, GUID,
// geçersiz UTF-8) "[N bytes]" özetine iner, ham gönderilmez.
func entryAttributes(e *goldap.Entry) map[string][]string {
	attrs := make(map[string][]string, len(e.Attributes))
	for _, a := range e.Attributes {
		vals := make([]string, 0, len(a.Values))
		for i, v := range a.Values {
			if a.Name == "thumbnailPhoto" || a.Name == "jpegPhoto" || !utf8.ValidString(v) {
				vals = append(vals, fmt.Sprintf("[%d bytes]", len(a.ByteValues[i])))
				continue
			}
			vals = append(vals, v)
		}
		attrs[a.Name] = vals
	}
	return attrs
}

// searchOneUser — giriş filtresiyle tek kullanıcı ("*" öznitelikler). Birden
// çok eşleşme belirsizliktir: keyfî ilki ASLA seçilmez.
func searchOneUser(conn *goldap.Conn, c Config, username string) (*goldap.Entry, error) {
	filter := resolveUserFilter(c.UserSearchFilter, goldap.EscapeFilter(username))
	req := goldap.NewSearchRequest(c.BaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
		2, 30, false, filter, []string{"*"}, nil)
	res, err := conn.Search(req)
	if err != nil && !(goldap.IsErrorWithCode(err, goldap.LDAPResultSizeLimitExceeded) && res != nil && len(res.Entries) > 0) {
		return nil, fmt.Errorf("directory search: %w", err)
	}
	if res == nil || len(res.Entries) == 0 {
		return nil, ErrDirectoryUserNotFound
	}
	if len(res.Entries) > 1 {
		return nil, fmt.Errorf("directory search returned %d entries for %q (filter too loose?)", len(res.Entries), username)
	}
	return res.Entries[0], nil
}

// ReadUser — kullanıcıyı servis hesabıyla yeniden okur (giriş şifresi
// gerekmez). Grup/rol çözülmez: denetim rolü DEĞİŞTİRMEZ.
func (s *Service) ReadUser(ctx context.Context, username string) (*DirectoryRead, error) {
	c := s.rawConfig()
	if !c.Enabled {
		return nil, errors.New("ldap disabled")
	}
	conn, err := bindAdmin(c)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	e, err := searchOneUser(conn, c, username)
	if err != nil {
		return nil, err
	}
	return &DirectoryRead{
		User: LDAPUser{
			DN:          e.DN,
			Username:    firstNonEmpty(e.GetAttributeValue(c.UserAttribute), username),
			Email:       e.GetAttributeValue(c.EmailAttribute),
			DisplayName: e.GetAttributeValue(c.DisplayAttribute),
			Department:  teamFor(e, c),
			Company:     dirText(e, "company", "o"),
			Photo:       photoFromEntry(e),
			Directory:   directoryFromEntry(e, c.DirectoryAttrs),
		},
		Attributes: entryAttributes(e),
	}, nil
}

// personAttrs — organizasyon düğümü için istenecek öznitelikler.
func personAttrs(c Config) []string {
	out := []string{"dn", c.DisplayAttribute, c.EmailAttribute, c.UserAttribute, "cn"}
	for _, spec := range []string{c.DirectoryAttrs.Title, c.DirectoryAttrs.Department, c.DirectoryAttrs.EmployeeID, c.DirectoryAttrs.Manager} {
		out = append(out, attrList(spec)...)
	}
	return out
}

// personFromEntry — düğüm + o düğümün yönetici DN'i.
func personFromEntry(e *goldap.Entry, c Config) (DirectoryPerson, string) {
	d := directoryFromEntry(e, c.DirectoryAttrs)
	name := strings.TrimSpace(e.GetAttributeValue(c.DisplayAttribute))
	if name == "" {
		name = firstNonEmpty(strings.TrimSpace(e.GetAttributeValue("cn")), cnFromDN(e.DN))
	}
	return DirectoryPerson{
		DN: e.DN, Name: name, Title: d.Title, Department: d.Department,
		Email:      strings.ToLower(strings.TrimSpace(e.GetAttributeValue(c.EmailAttribute))),
		Username:   strings.ToLower(strings.TrimSpace(e.GetAttributeValue(c.UserAttribute))),
		EmployeeID: d.EmployeeID,
	}, d.ManagerDN
}

// walkManagerChain — SAF: start DN'den yukarı en çok maxUp yönetici okur
// (en yakın önce). Döngü (A→B→A) ve kendini gösteren yönetici durur; okuma
// hatası zinciri o noktada keser ve hatayı döndürür (o ana kadarki düğümler
// geçerli). maxUp tavanı sözleşmedir: AD'deki derin zincirler (GM'ye 8-10
// kademe) LDAP okumasını sınırsız büyütmesin.
func walkManagerChain(selfDN, start string, maxUp int, fetch func(dn string) (DirectoryPerson, string, error)) ([]DirectoryPerson, error) {
	out := []DirectoryPerson{}
	seen := map[string]bool{strings.ToLower(selfDN): true}
	dn := start
	for len(out) < maxUp && strings.TrimSpace(dn) != "" {
		key := strings.ToLower(dn)
		if seen[key] {
			break
		}
		seen[key] = true
		p, next, err := fetch(dn)
		if err != nil {
			return out, err
		}
		out = append(out, p)
		dn = next
	}
	return out, nil
}

// OrgChart — kullanıcının yönetici zinciri + doğrudan bağlıları.
func (s *Service) OrgChart(ctx context.Context, username string) (*OrgChart, error) {
	c := s.rawConfig()
	if !c.Enabled {
		return nil, errors.New("ldap disabled")
	}
	mgrAttrs := attrList(c.DirectoryAttrs.Manager)
	if len(mgrAttrs) == 0 {
		return nil, errors.New("yönetici özniteliği eşlenmemiş (Settings › LDAP › Dizin öznitelikleri)")
	}
	conn, err := bindAdmin(c)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	e, err := searchOneUser(conn, c, username)
	if err != nil {
		return nil, err
	}
	self, mgrDN := personFromEntry(e, c)
	attrs := personAttrs(c)
	fetch := func(dn string) (DirectoryPerson, string, error) {
		req := goldap.NewSearchRequest(dn, goldap.ScopeBaseObject, goldap.NeverDerefAliases,
			1, 15, false, "(objectClass=*)", attrs, nil)
		res, err := conn.Search(req)
		if err != nil {
			return DirectoryPerson{}, "", fmt.Errorf("manager read %q: %w", dn, err)
		}
		if len(res.Entries) == 0 {
			return DirectoryPerson{}, "", fmt.Errorf("manager %q not found", dn)
		}
		p, next := personFromEntry(res.Entries[0], c)
		return p, next, nil
	}
	managers, chainErr := walkManagerChain(e.DN, mgrDN, OrgMaxManagers, fetch)
	out := &OrgChart{Self: self, Managers: managers, Reports: []DirectoryPerson{}}
	// Doğrudan bağlılar: eşlenen İLK yönetici özniteliği = <kullanıcı DN>.
	// Boyut sınırı +1: tavan aşıldı mı bilinsin.
	req := goldap.NewSearchRequest(c.BaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases,
		OrgMaxReports+1, 30, false,
		fmt.Sprintf("(%s=%s)", mgrAttrs[0], goldap.EscapeFilter(e.DN)), attrs, nil)
	res, err := conn.Search(req)
	if err != nil && !(goldap.IsErrorWithCode(err, goldap.LDAPResultSizeLimitExceeded) && res != nil) {
		return out, fmt.Errorf("direct reports search: %w", err)
	}
	if res != nil {
		for _, re := range res.Entries {
			p, _ := personFromEntry(re, c)
			out.Reports = append(out.Reports, p)
		}
	}
	sort.Slice(out.Reports, func(i, j int) bool {
		return strings.ToLower(out.Reports[i].Name) < strings.ToLower(out.Reports[j].Name)
	})
	if len(out.Reports) > OrgMaxReports || (err != nil) {
		out.ReportsTruncated = true
		if len(out.Reports) > OrgMaxReports {
			out.Reports = out.Reports[:OrgMaxReports]
		}
	}
	return out, chainErr
}

// DirectoryConfigDigest — önbellek anahtarı için okuma sonucunu etkileyen
// yapılandırmanın FNV-64a özeti (bağlantı + filtre + öznitelik eşlemesi;
// şifre HARİÇ). Eşleme değişince eski sonuç anahtar dışı kalır.
func (s *Service) DirectoryConfigDigest() uint64 { return directoryConfigDigest(s.rawConfig()) }

func directoryConfigDigest(c Config) uint64 {
	b, _ := json.Marshal(struct {
		Host, BaseDN, Filter, UserAttr, EmailAttr, DisplayAttr, TeamAttr, TeamRegex string
		Port                                                                        int
		Attrs                                                                       DirectoryAttrConfig
	}{c.Host, c.BaseDN, c.UserSearchFilter, c.UserAttribute, c.EmailAttribute, c.DisplayAttribute,
		c.TeamAttribute, c.TeamRegex, c.Port, c.DirectoryAttrs})
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}
