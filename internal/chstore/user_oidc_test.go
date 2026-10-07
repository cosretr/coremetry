package chstore

// user_oidc_test.go — v0.10.1121 (güvenlik incelemesi F4): OIDC kullanıcı
// adı okumasının sorgu şekli — devre dışı satırlar dışarıda, LIMIT 2 (çağıran
// iki satırı belirsiz sayar), max_execution_time sınırı, girdi lowerUTF8 ile.
// CH gerektirmez (kaynak taraması); boş girdi / sütun yok ağsız (nil, nil).

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestGetActiveUsersByLdapUsernameQueryShape(t *testing.T) {
	src, err := os.ReadFile("user_oidc.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		"WHERE disabled = 0 AND ldap_username != '' AND lowerUTF8(ldap_username) = ?",
		"LIMIT 2",
		"SETTINGS max_execution_time = 5",
		"FROM users FINAL",
	} {
		if !strings.Contains(string(src), must) {
			t.Errorf("sorgu %q içermiyor", must)
		}
	}
	s := &Store{} // conn nil: aşağıdaki iki yol ağa çıkmamalı
	if u, err := s.GetActiveUsersByLdapUsername(context.Background(), "n0000001"); u != nil || err != nil {
		t.Fatalf("sütun yokken (nil, nil): %v %v", u, err)
	}
	s.hasLdapUsernameCol = true
	if u, err := s.GetActiveUsersByLdapUsername(context.Background(), ""); u != nil || err != nil {
		t.Fatalf("boş girdi (nil, nil): %v %v", u, err)
	}
}
