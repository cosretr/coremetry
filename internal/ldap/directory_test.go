package ldap

import (
	"reflect"
	"testing"
)

// directory_test.go — v0.10.1732 (kullanıcı detay sayfası S2: dizin
// öznitelikleri). Eşleme Settings › LDAP'tan gelir; geri-düşme listesi ilk
// dolu değeri almalı, boş eşleme o bilgiyi okumamalı, eski (boş) blob AD
// varsayılanlarını almalı ama bilinçli boşaltılmış alan geri gelmemeli.

func TestDirectoryFromAttrs(t *testing.T) {
	attrs := map[string]string{
		"title":                      "Kıdemli Mühendis",
		"department":                 "Teknoloji",
		"division":                   "Ödeme Sistemleri",
		"employeeNumber":             "E12345",
		"telephoneNumber":            "+90 000 000 00 00",
		"physicalDeliveryOfficeName": "Genel Müdürlük · Kat 4",
		"manager":                    "CN=Mehmet Demir,OU=Users,DC=example,DC=com",
	}
	get := func(a string) string { return attrs[a] }
	got := directoryFromAttrs(get, defaultDirectoryAttrs)
	want := DirectoryInfo{
		Title: "Kıdemli Mühendis", Department: "Teknoloji", Division: "Ödeme Sistemleri",
		EmployeeID: "E12345", Mobile: "+90 000 000 00 00", Office: "Genel Müdürlük · Kat 4",
		ManagerDN: "CN=Mehmet Demir,OU=Users,DC=example,DC=com", ManagerName: "Mehmet Demir",
	}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	attrs["employeeID"] = "E999"
	attrs["mobile"] = "+90 5xx"
	if g := directoryFromAttrs(get, defaultDirectoryAttrs); g.EmployeeID != "E999" || g.Mobile != "+90 5xx" {
		t.Fatalf("geri-düşme listesinin ilki kazanmalı: %+v", g)
	}
	cfg := defaultDirectoryAttrs
	cfg.Title, cfg.Level = "", "extensionAttribute5"
	attrs["extensionAttribute5"] = "D2"
	if g := directoryFromAttrs(get, cfg); g.Title != "" || g.Level != "D2" {
		t.Fatalf("boş eşleme okunmamalı, seviye eşlemesi okunmalı: %+v", g)
	}
	if !directoryFromAttrs(func(string) string { return "" }, defaultDirectoryAttrs).Empty() {
		t.Fatal("öznitelik yok → Empty")
	}
}

func TestDirectoryAttrConfigNormalize(t *testing.T) {
	var d DirectoryAttrConfig
	d.normalize()
	if d != defaultDirectoryAttrs {
		t.Fatalf("boş blob varsayılan almalı: %+v", d)
	}
	partial := DirectoryAttrConfig{Title: "title"}
	partial.normalize()
	if partial != (DirectoryAttrConfig{Title: "title"}) {
		t.Fatalf("kısmi blob olduğu gibi kalmalı: %+v", partial)
	}
}

func TestDirectoryFetchAttrs(t *testing.T) {
	got := directoryFetchAttrs(DirectoryAttrConfig{EmployeeID: "employeeID, employeeNumber", Manager: "manager"})
	if !reflect.DeepEqual(got, []string{"employeeID", "employeeNumber", "manager"}) {
		t.Fatalf("%v", got)
	}
}

func TestCnFromDN(t *testing.T) {
	cases := map[string]string{
		"CN=Mehmet Demir,OU=Users,DC=example,DC=com": "Mehmet Demir",
		"cn=Can Kaya,dc=example,dc=com":              "Can Kaya",
		"OU=Users,DC=example,DC=com":                 "",
		"":                                           "",
		"not a dn":                                   "",
	}
	for in, want := range cases {
		if got := cnFromDN(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
