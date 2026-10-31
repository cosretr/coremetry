package ldap

// directory.go — v0.10.1732 (kullanıcı detay sayfası S2, operatör onayı
// 2026-10-31: "AD'den daha fazla bilgi"). Girişte ve dizin aramalarında
// kullanıcının kurumsal öznitelikleri (ünvan, departman, müdürlük, sicil,
// telefon, ofis, yönetici) okunur; users.directory JSON kolonuna yazılır.
//
// Öznitelik ADLARI Settings › LDAP'ta eşlenir (ldap blob'unun
// directoryAttrs alanı); varsayılanlar AD adları. Her alan virgülle ayrılmış
// bir geri-düşme listesi alır: ilk boş olmayan değer kazanır
// ("employeeID,employeeNumber"). Boş alan = o bilgi okunmaz. Seviye (level)
// varsayılanı BOŞ: AD'de standart bir karşılığı yok.

import (
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
)

// DirectoryAttrConfig — dizin özniteliği eşlemesi (Settings › LDAP).
type DirectoryAttrConfig struct {
	Title      string `json:"title"`
	Department string `json:"department"`
	Division   string `json:"division"`
	EmployeeID string `json:"employeeId"`
	Mobile     string `json:"mobile"`
	Office     string `json:"office"`
	Manager    string `json:"manager"` // DN değerli öznitelik
	Level      string `json:"level"`
}

// AD varsayılanları.
var defaultDirectoryAttrs = DirectoryAttrConfig{
	Title:      "title",
	Department: "department",
	Division:   "division",
	EmployeeID: "employeeID,employeeNumber",
	Mobile:     "mobile,telephoneNumber",
	Office:     "physicalDeliveryOfficeName",
	Manager:    "manager",
}

// normalize — tamamen boş blob (eski kurulum) AD varsayılanlarını alır.
// Kısmi doldurulmuş bir blob OLDUĞU gibi kalır: admin bir alanı bilinçli
// boşalttıysa (o bilgi okunmasın) varsayılan geri gelmemeli.
func (d *DirectoryAttrConfig) normalize() {
	if *d == (DirectoryAttrConfig{}) {
		*d = defaultDirectoryAttrs
	}
}

// DirectoryInfo — bir kullanıcının okunan dizin öznitelikleri. users.directory
// kolonunun JSON şekli; boş alan yazılmaz.
type DirectoryInfo struct {
	Title       string `json:"title,omitempty"`
	Department  string `json:"department,omitempty"`
	Division    string `json:"division,omitempty"`
	EmployeeID  string `json:"employeeId,omitempty"`
	Mobile      string `json:"mobile,omitempty"`
	Office      string `json:"office,omitempty"`
	ManagerDN   string `json:"managerDn,omitempty"`
	ManagerName string `json:"managerName,omitempty"` // DN'nin ilk CN'i
	Level       string `json:"level,omitempty"`
}

// Empty — hiçbir öznitelik okunmadı mı (seyrek giriş saklı değeri silmez).
func (d DirectoryInfo) Empty() bool { return d == (DirectoryInfo{}) }

// attrList — "a, b ,c" → [a b c] (boşlar atılır).
func attrList(spec string) []string {
	var out []string
	for _, p := range strings.Split(spec, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// directoryFetchAttrs — SAF: aramaların istemesi gereken öznitelik adları.
func directoryFetchAttrs(c DirectoryAttrConfig) []string {
	var out []string
	for _, spec := range []string{c.Title, c.Department, c.Division, c.EmployeeID, c.Mobile, c.Office, c.Manager, c.Level} {
		out = append(out, attrList(spec)...)
	}
	return out
}

// directoryFromAttrs — SAF: get(ad) → değer eşlemesinden DirectoryInfo.
// Her alan kendi geri-düşme listesinin ilk boş olmayan değerini alır.
func directoryFromAttrs(get func(string) string, c DirectoryAttrConfig) DirectoryInfo {
	first := func(spec string) string {
		for _, a := range attrList(spec) {
			if v := strings.TrimSpace(get(a)); v != "" {
				return v
			}
		}
		return ""
	}
	d := DirectoryInfo{
		Title:      first(c.Title),
		Department: first(c.Department),
		Division:   first(c.Division),
		EmployeeID: first(c.EmployeeID),
		Mobile:     first(c.Mobile),
		Office:     first(c.Office),
		ManagerDN:  first(c.Manager),
		Level:      first(c.Level),
	}
	d.ManagerName = cnFromDN(d.ManagerDN)
	return d
}

// directoryFromEntry — dizin girişinden DirectoryInfo.
func directoryFromEntry(e *goldap.Entry, c DirectoryAttrConfig) DirectoryInfo {
	return directoryFromAttrs(e.GetAttributeValue, c)
}

// cnFromDN — SAF: DN'nin ilk cn= RDN değeri ("CN=Mehmet Demir,OU=…" →
// "Mehmet Demir"). Ayrıştırılamayan / CN'siz DN → "".
func cnFromDN(dn string) string {
	if strings.TrimSpace(dn) == "" {
		return ""
	}
	parsed, err := goldap.ParseDN(dn)
	if err != nil || len(parsed.RDNs) == 0 {
		return ""
	}
	for _, a := range parsed.RDNs[0].Attributes {
		if strings.EqualFold(a.Type, "cn") {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}
