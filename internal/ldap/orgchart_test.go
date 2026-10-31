package ldap

import (
	"errors"
	"fmt"
	"testing"
)

// orgchart_test.go — v0.10.1733 (kullanıcı detay sayfası S3, Organizasyon
// kartı). Yönetici zinciri en çok OrgMaxManagers (4) üst okur: AD'de GM'ye
// 8-10 kademe zincirler var ve her kademe bir LDAP okuması. Döngü (A→B→A)
// ve kendini gösteren yönetici sonsuz döngüye girmemeli.

func chainFetch(managers map[string]string, calls *int) func(string) (DirectoryPerson, string, error) {
	return func(dn string) (DirectoryPerson, string, error) {
		*calls++
		if dn == "CN=broken" {
			return DirectoryPerson{}, "", errors.New("read failed")
		}
		return DirectoryPerson{DN: dn, Name: dn}, managers[dn], nil
	}
}

func TestWalkManagerChainDepthCap(t *testing.T) {
	// 10 kademelik zincir: CN=m1 → m2 → … → m10.
	m := map[string]string{}
	for i := 1; i < 10; i++ {
		m[fmt.Sprintf("CN=m%d", i)] = fmt.Sprintf("CN=m%d", i+1)
	}
	calls := 0
	got, err := walkManagerChain("CN=self", "CN=m1", OrgMaxManagers, chainFetch(m, &calls))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || calls != 4 || OrgMaxManagers != 4 {
		t.Fatalf("tavan 4 üst: %d düğüm, %d okuma", len(got), calls)
	}
	if got[0].DN != "CN=m1" || got[3].DN != "CN=m4" {
		t.Fatalf("sıra en yakın önce: %+v", got)
	}
}

func TestWalkManagerChainStops(t *testing.T) {
	cases := []struct {
		name    string
		start   string
		m       map[string]string
		want    int
		wantErr bool
	}{
		{"yönetici yok", "", nil, 0, false},
		{"tepe (yöneticisiz)", "CN=gm", map[string]string{}, 1, false},
		{"döngü A→B→A", "CN=a", map[string]string{"CN=a": "CN=b", "CN=b": "CN=a"}, 2, false},
		{"kendini gösteren", "CN=self", map[string]string{}, 0, false},
		{"büyük/küçük harf döngüsü", "CN=a", map[string]string{"CN=a": "cn=A"}, 1, false},
		{"okuma hatası zinciri keser", "CN=a", map[string]string{"CN=a": "CN=broken"}, 1, true},
	}
	for _, c := range cases {
		calls := 0
		got, err := walkManagerChain("CN=self", c.start, OrgMaxManagers, chainFetch(c.m, &calls))
		if len(got) != c.want || (err != nil) != c.wantErr {
			t.Errorf("%s: %d düğüm (want %d), err=%v", c.name, len(got), c.want, err)
		}
	}
}

func TestDirectoryConfigDigest(t *testing.T) {
	base := Config{Host: "ldap.example.com", BaseDN: "DC=example,DC=com", DirectoryAttrs: defaultDirectoryAttrs}
	d0 := directoryConfigDigest(base)
	if directoryConfigDigest(base) != d0 {
		t.Fatal("deterministik olmalı")
	}
	alt := base
	alt.DirectoryAttrs.Manager = "managedBy"
	if directoryConfigDigest(alt) == d0 {
		t.Fatal("öznitelik eşlemesi özete girmeli")
	}
	alt = base
	alt.BaseDN = "OU=Staff,DC=example,DC=com"
	if directoryConfigDigest(alt) == d0 {
		t.Fatal("baseDN özete girmeli")
	}
	alt = base
	alt.BindPassword = "secret"
	if directoryConfigDigest(alt) != d0 {
		t.Fatal("şifre özete girmemeli")
	}
}
