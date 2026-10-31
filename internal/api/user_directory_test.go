package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ldap"
)

// user_directory_test.go — v0.10.1733 (kullanıcı detay sayfası S3: "AD ile
// denetle", Dizin sekmesi, Organizasyon). Denetim giriş yolunun kuralını
// izlemeli (boş dizin değeri saklıyı silmez, rol/kilit dokunulmaz), farkı
// alan alan bildirmeli; önbellek anahtarı bütün girdileri taşımalı.

func TestApplyDirectoryReadAndDiff(t *testing.T) {
	before := chstore.User{
		ID: "u1", Email: "ayse@example.com", Role: "editor", RoleLocked: true, CustomRole: "",
		FullName: "Ayşe Yılmaz", Org: "Acme", Team: "platform-sre", LdapUsername: "ayilmaz",
		Photo: []byte("old"), Directory: json.RawMessage(`{"title":"Mühendis","employeeId":"E12345"}`),
	}
	read := ldap.LDAPUser{
		DisplayName: "Ayşe Yılmaz", Company: "", Department: "payments-core", Username: "AYilmaz",
		Directory: ldap.DirectoryInfo{Title: "Kıdemli Mühendis", EmployeeID: "E12345"},
	}
	after := applyDirectoryRead(before, read)
	if after.Role != "editor" || !after.RoleLocked || after.Email != before.Email || after.Org != "Acme" {
		t.Fatalf("rol/kilit/e-posta dokunulmaz, boş şirket saklıyı silmez: %+v", after)
	}
	if after.Team != "payments-core" || after.LdapUsername != "ayilmaz" || after.HasPhoto {
		t.Fatalf("dizin değerleri uygulanmalı (foto dizini izler): %+v", after)
	}
	var fields []string
	for _, d := range userDirectoryDiff(before, after) {
		fields = append(fields, d.Field)
	}
	want := []string{"team", "photo", "directory.title"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fark alanları %v, want %v", fields, want)
	}
	if d := userDirectoryDiff(after, after); len(d) != 0 {
		t.Fatalf("aynı satır fark üretmemeli: %+v", d)
	}
	// Dizin hiçbir öznitelik döndürmedi → saklı dizin kalır.
	if got := applyDirectoryRead(before, ldap.LDAPUser{Photo: []byte("old")}); string(got.Directory) != string(before.Directory) {
		t.Fatalf("boş okuma dizini silmemeli: %s", got.Directory)
	}
}

func TestUserDirectoryCacheKey(t *testing.T) {
	a := userDirectoryCacheKey("org", "u1\x00ayilmaz", 1)
	for _, other := range []string{
		userDirectoryCacheKey("dir", "u1\x00ayilmaz", 1),
		userDirectoryCacheKey("org", "u2\x00ayilmaz", 1),
		userDirectoryCacheKey("org", "u1\x00ayse", 1),
		userDirectoryCacheKey("org", "u1\x00ayilmaz", 2),
	} {
		if other == a {
			t.Fatalf("anahtar bir girdiyi düşürüyor: %q", a)
		}
	}
}

func TestLinkOrgAccounts(t *testing.T) {
	chart := &ldap.OrgChart{
		Self:     ldap.DirectoryPerson{Name: "Ayşe Yılmaz", Email: "ayse@example.com"},
		Managers: []ldap.DirectoryPerson{{Name: "Mehmet Demir", Username: "mdemir"}, {Name: "GM", Email: "gm@example.com"}},
		Reports:  []ldap.DirectoryPerson{{Name: "Can Kaya", Username: "ckaya"}},
	}
	users := []chstore.User{
		{ID: "a", Email: "Ayse@Example.com"},
		{ID: "m", Email: "mdemir@example.com"},
		{ID: "x", Email: "ckaya@example.com", Disabled: true},
	}
	got := linkOrgAccounts(chart, users)
	if got.Self.AccountID != "a" || got.Managers[0].AccountID != "m" || got.Managers[1].AccountID != "" {
		t.Fatalf("bağlama: %+v", got)
	}
	if got.Reports[0].AccountID != "" {
		t.Fatal("pasif hesap bağlanmamalı")
	}
}
