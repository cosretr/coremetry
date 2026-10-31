package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ldap"
)

// user_upsert_sites_test.go — v0.10.1732 (kullanıcı detay sayfası S2).
// users ReplacingMergeTree: UpsertUser TÜM satırı değiştirir. Yeni kolon
// (directory JSON) yalnız satırı TAM okuma (GetUserBy*, userSelectExpr)
// üstünden değiştiren ya da bilinçli taşıyan yollarda korunur. v0.10.1731'de
// iki yol yarım satır yazıyordu (provisionLDAPUser, özel rol silme). Bu test
// her UpsertUser çağrı noktasını sayar: yeni bir nokta eklenirse sayı tutmaz
// ve ekleyen kişi aşağıdaki tabloya yolun neden tam satır taşıdığını yazar.

var upsertUserSites = map[string]int{
	// loginViaLDAP (carryLdapLoginRow), provisionLDAPUser (u = *existing),
	// OIDC ilk giriş (yeni satır), setUserRole (GetUserByID), createUser (yeni).
	"api.go": 5,
	// convertLdapAccountToOIDC: nu := u (tam satır).
	"auth_oidc_ldap_link.go": 1,
	// arayüz bildirimi + rol-claim düşürme (nu := u, tam satır).
	"auth_permissions.go": 2,
	// özel rol silme (GetUserByIDAnyState) + setUserCustomRole (GetUserByID).
	"custom_roles.go": 2,
	// clearUserRoleLock (GetUserByID).
	"user_detail.go": 1,
}

func TestUpsertUserCallSitesAccounted(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(b), "UpsertUser("); n > 0 {
			got[f] = n
		}
	}
	for f, n := range got {
		if upsertUserSites[f] != n {
			t.Errorf("%s: %d UpsertUser çağrısı, tablo %d — yeni yol tam satırı (directory, role_locked, foto …) taşıyor mu? Tabloya gerekçeyle ekle.", f, n, upsertUserSites[f])
		}
	}
	for f, n := range upsertUserSites {
		if got[f] != n {
			t.Errorf("%s: tablo %d, kaynak %d", f, n, got[f])
		}
	}
}

func TestDirectoryCarriedThroughLdapLogin(t *testing.T) {
	stored := json.RawMessage(`{"title":"Kıdemli Mühendis","employeeId":"E12345"}`)
	existing := &chstore.User{ID: "u1", Directory: stored}
	// Seyrek giriş: dizin hiçbir öznitelik döndürmedi → saklı değer kalır.
	if got := carryLdapLoginRow(existing, chstore.User{Directory: directoryJSON(ldap.DirectoryInfo{})}); string(got.Directory) != string(stored) {
		t.Fatalf("seyrek giriş dizini silmemeli: %s", got.Directory)
	}
	// Dolu giriş: bütünüyle yenilenir.
	fresh := directoryJSON(ldap.DirectoryInfo{Title: "Müdür"})
	if got := carryLdapLoginRow(existing, chstore.User{Directory: fresh}); string(got.Directory) != `{"title":"Müdür"}` {
		t.Fatalf("dolu giriş yenilemeli: %s", got.Directory)
	}
}
