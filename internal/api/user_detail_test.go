package api

import (
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ldap"
)

// user_detail_test.go — v0.10.1731 (kullanıcı detay sayfası + rol kilidi).
// Belirti: admin'in bir LDAP hesabına elle verdiği rol, kullanıcı AÇIK bir AD
// grup eşlemesine düştüğü sürece bir sonraki girişte eziliyordu. Kilit
// (RoleLocked) girişte korunmalı ve whole-row replace'te taşınmalı.

func TestResolveLdapLoginRoleRespectsLock(t *testing.T) {
	locked := &chstore.User{AuthProvider: "ldap", Role: "admin", RoleLocked: true}
	unlocked := &chstore.User{AuthProvider: "ldap", Role: "admin"}
	cases := []struct {
		name      string
		existing  *chstore.User
		groupRole string
		fromGroup bool
		want      string
	}{
		{"kilitli + açık eşleşme viewer → admin korunur", locked, "viewer", true, "admin"},
		{"kilitli + eşleşme yok → admin korunur", locked, "viewer", false, "admin"},
		{"kilitsiz + açık eşleşme viewer → AD kazanır", unlocked, "viewer", true, "viewer"},
	}
	for _, c := range cases {
		if got := resolveLdapLoginRole(c.existing, c.groupRole, c.fromGroup); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestCarryLdapLoginRow(t *testing.T) {
	existing := &chstore.User{
		ID: "u1", CreatedAt: 7, Team: "platform-sre", CustomRole: "ops", PasswordHash: "h",
		LastLoginAt: 9, RoleLocked: true, FullName: "Ayşe Yılmaz", Org: "Acme", LdapUsername: "ayilmaz",
		Photo: []byte("old"),
	}
	// Seyrek dizin girişi: ad/kuruluş/kullanıcı adı yok, foto yok.
	got := carryLdapLoginRow(existing, chstore.User{Email: "ayse@example.com", Role: "viewer", AuthProvider: "ldap"})
	want := chstore.User{
		ID: "u1", CreatedAt: 7, Team: "platform-sre", CustomRole: "ops", PasswordHash: "h",
		LastLoginAt: 9, RoleLocked: true, FullName: "Ayşe Yılmaz", Org: "Acme", LdapUsername: "ayilmaz",
		Email: "ayse@example.com", Role: "viewer", AuthProvider: "ldap",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("seyrek giriş taşıması:\n got %+v\nwant %+v", got, want)
	}
	// Dolu dizin girişi: taze değerler kazanır.
	got = carryLdapLoginRow(existing, chstore.User{FullName: "Ayşe Y.", Org: "Acme Teknoloji", LdapUsername: "ayse"})
	if got.FullName != "Ayşe Y." || got.Org != "Acme Teknoloji" || got.LdapUsername != "ayse" || !got.RoleLocked {
		t.Fatalf("dolu giriş: %+v", got)
	}
	if got := carryLdapLoginRow(nil, chstore.User{Email: "x"}); got.Email != "x" || got.ID != "" {
		t.Fatalf("ilk giriş: %+v", got)
	}
}

func TestUserLdapGroups(t *testing.T) {
	snap := &ldap.Snapshot{
		Groups: map[string]ldap.Group{
			"g1": {UID: "g1", CN: "payments-core", DN: "CN=payments-core,DC=example,DC=com"},
			"g2": {UID: "g2", CN: "Coremetry-Admins", DN: "CN=Coremetry-Admins,DC=example,DC=com"},
			"g3": {UID: "g3", CN: "orders-api", DN: "CN=orders-api,DC=example,DC=com"},
		},
		UserGroups: map[string][]string{
			"ayilmaz":          {"g1", "g2"},
			"ayse@example.com": {"g2", "g3", "gone"},
		},
	}
	u := &chstore.User{Email: "Ayse@Example.com", LdapUsername: "ayilmaz"}
	aliases := userGroupAliases(u)
	if !reflect.DeepEqual(aliases, []string{"ayilmaz", "ayse@example.com", "ayse"}) {
		t.Fatalf("aliases %v", aliases)
	}
	got := userLdapGroups(snap, aliases)
	var cns []string
	for _, g := range got {
		cns = append(cns, g.CN)
	}
	if !reflect.DeepEqual(cns, []string{"Coremetry-Admins", "orders-api", "payments-core"}) {
		t.Fatalf("gruplar %v", cns)
	}
	if got := userLdapGroups(nil, aliases); len(got) != 0 || got == nil {
		t.Fatalf("nil snapshot boş dilim (JSON [] ) dönmeli: %#v", got)
	}
}
