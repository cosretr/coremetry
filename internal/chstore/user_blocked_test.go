package chstore

// v0.10.1558 — kullanıcı engelleme: disabled kolonunun üç değeri. Engelli
// satır 1 yazılırsa listeden düşer ve engeli kaldırılamaz; 0 yazılırsa
// hesap giriş yapabilir. Kodlama ile scan tarafının okuması aynı sabite bağlı.

import (
	"strings"
	"testing"
)

func TestUserDisabledCode(t *testing.T) {
	cases := []struct {
		name string
		u    User
		want uint8
	}{
		{"aktif", User{}, 0},
		{"silinmiş", User{Disabled: true}, 1},
		{"engelli", User{Disabled: true, Blocked: true}, userBlocked},
		// Blocked tek başına da engel yazar — Disabled'ı unutan bir çağıran
		// hesabı aktif satıra çeviremesin.
		{"yalnız Blocked", User{Blocked: true}, userBlocked},
	}
	for _, c := range cases {
		if got := userDisabledCode(c.u); got != c.want {
			t.Errorf("%s: userDisabledCode = %d, want %d", c.name, got, c.want)
		}
	}
	if userBlocked == 0 || userBlocked == 1 {
		t.Fatalf("userBlocked = %d çakışıyor (0 aktif, 1 silinmiş)", userBlocked)
	}
}

// Asıl regresyon: loginViaLDAP engelli satırı göremiyor ve hesabı yeni viewer
// satırıyla yeniden açıyordu. Kapı, kimliğe ait engelli satırları SAYAR —
// tekil "en iyi satır" okuması silinmiş eski satırı (disabled=1) seçip engeli
// delerdi.
func TestLoginBlockedQuery(t *testing.T) {
	cases := []struct {
		name            string
		hasLdap         bool
		email, username string
		wantArgs        int
		wantUsername    bool
	}{
		{"e-posta + kullanıcı adı", true, "a@x", "a", 2, true},
		{"kolon yok", false, "a@x", "a", 1, false},
		{"kullanıcı adı boş", true, "a@x", "", 1, false},
	}
	for _, c := range cases {
		q, args := loginBlockedQuery(c.hasLdap, c.email, c.username)
		if !strings.Contains(q, "disabled = 2") {
			t.Errorf("%s: engelli değerini (userBlocked=%d) süzmüyor: %s", c.name, userBlocked, q)
		}
		if strings.Contains(q, "ORDER BY") || strings.Contains(q, "LIMIT") {
			t.Errorf("%s: tekil satır okuması — sayım olmalı: %s", c.name, q)
		}
		if !strings.Contains(q, "max_execution_time") {
			t.Errorf("%s: SETTINGS max_execution_time yok", c.name)
		}
		if len(args) != c.wantArgs || strings.Count(q, "?") != c.wantArgs {
			t.Errorf("%s: args=%d placeholders=%d, want %d", c.name, len(args), strings.Count(q, "?"), c.wantArgs)
		}
		if got := strings.Contains(q, "ldap_username"); got != c.wantUsername {
			t.Errorf("%s: ldap_username kolu = %v, want %v", c.name, got, c.wantUsername)
		}
	}
	if userBlocked != 2 {
		t.Fatalf("userBlocked = %d ama sorgu ve ListUsers 2 yazıyor", userBlocked)
	}
}
