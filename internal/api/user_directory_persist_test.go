package api

// user_directory_persist_test.go — v0.10.1741 (operator-reported): "AD ile
// denetle" fark panelinde yeni değerler (ünvan, departman, sicil, telefon,
// ofis, yönetici) göründü, kartlar "—" kaldı. Denetim cevabı hesaplanan
// satırı döndürüyordu, yazılanı değil: users.directory kolonu probe'da
// kapalıyken (küme kipinde ertelenen ALTER) UpsertUser kolonu sessizce
// düşürür, cevap yine "written" der. Cevap artık yeniden okunan satırdır ve
// dizinin gerçekten saklanıp saklanmadığını söyler. Adlar SENTETİK.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ldap"
)

// fakeUserRow — tek satırlık users; dropDirectory = kolon yok (probe false).
type fakeUserRow struct {
	row           chstore.User
	dropDirectory bool
	writes        int
}

func (f *fakeUserRow) upsert(u chstore.User) error {
	if f.dropDirectory {
		u.Directory = nil
	}
	f.row = u
	f.writes++
	return nil
}

func (f *fakeUserRow) reread() (*chstore.User, error) {
	u := f.row
	return &u, nil
}

func TestDirectoryCheckReturnsPersistedRow(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	before := chstore.User{ID: "u1", Email: "user1@example.com", AuthProvider: "ldap", LdapUsername: "user1", Team: "team-a"}
	read := ldap.LDAPUser{Username: "user1", Directory: ldap.DirectoryInfo{
		Title: "Uzman", Department: "Dept A", EmployeeID: "E100", Mobile: "+90 555 000 0000", Office: "Ofis 1",
		ManagerDN: "CN=Manager One,OU=Users,DC=example,DC=com", ManagerName: "Manager One"}}

	t.Run("kolon var: yazılan dizin cevapta", func(t *testing.T) {
		st := &fakeUserRow{row: before}
		resp, err := runDirectoryCheck(before, read, now, st.upsert, st.reread)
		if err != nil {
			t.Fatal(err)
		}
		d := parseDirectory(resp.After.Directory)
		if !resp.Written || st.writes != 1 || d.Title != "Uzman" || d.EmployeeID != "E100" || d.Office != "Ofis 1" {
			t.Fatalf("yazılan dizin cevapta olmalı: written=%v writes=%d after=%s", resp.Written, st.writes, resp.After.Directory)
		}
		if !resp.DirectoryStored {
			t.Fatal("dizin saklandı → DirectoryStored=true")
		}
		if d.SyncedAt != now.UnixNano() {
			t.Fatalf("syncedAt damgası = %d, want %d", d.SyncedAt, now.UnixNano())
		}
	})

	t.Run("kolon yok: cevap yazılanı söyler, uydurmaz", func(t *testing.T) {
		st := &fakeUserRow{row: before, dropDirectory: true}
		resp, err := runDirectoryCheck(before, read, now, st.upsert, st.reread)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.After.Directory) != 0 {
			t.Fatalf("saklanmayan dizin cevapta görünmemeli: %s", resp.After.Directory)
		}
		if resp.DirectoryStored {
			t.Fatal("dizin saklanamadı → DirectoryStored=false")
		}
		if len(resp.Diff) == 0 {
			t.Fatal("fark paneli okunanı yine göstermeli")
		}
	})

	t.Run("fark yok ama damga eski: yine yazılır (taze)", func(t *testing.T) {
		stale, _ := json.Marshal(ldap.DirectoryInfo{Title: "Uzman", Department: "Dept A", EmployeeID: "E100",
			Mobile: "+90 555 000 0000", Office: "Ofis 1", ManagerDN: read.Directory.ManagerDN, ManagerName: "Manager One"})
		b := before
		b.Directory = stale
		st := &fakeUserRow{row: b}
		resp, err := runDirectoryCheck(b, read, now, st.upsert, st.reread)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Diff) != 0 || st.writes != 1 || parseDirectory(resp.After.Directory).SyncedAt != now.UnixNano() {
			t.Fatalf("damgasız satır tazelenmeli: diff=%v writes=%d after=%s", resp.Diff, st.writes, resp.After.Directory)
		}
	})
}
