package api

// v0.10.1558 — kullanıcı engelleme: engelleme isteğinin reddi. LDAP girişinin
// engelli satıra çarpması chstore/user_blocked_test.go'da (loginBlockedQuery).

import (
	"net/http"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestBlockRefusal(t *testing.T) {
	viewer := &chstore.User{ID: "u1", Role: auth.RoleViewer}
	admin := &chstore.User{ID: "a1", Role: auth.RoleAdmin}
	cases := []struct {
		name      string
		caller    string
		target    *chstore.User
		lastAdmin bool
		want      int
	}{
		{"viewer engellenir", "a1", viewer, false, 0},
		{"admin (başka admin varken)", "a2", admin, false, 0},
		{"yok", "a1", nil, false, http.StatusNotFound},
		{"silinmiş", "a1", &chstore.User{ID: "u2", Disabled: true}, false, http.StatusNotFound},
		{"zaten engelli", "a1", &chstore.User{ID: "u3", Disabled: true, Blocked: true}, false, http.StatusNotFound},
		{"kendini", "u1", viewer, false, http.StatusBadRequest},
		{"son admin", "a2", admin, true, http.StatusBadRequest},
		// lastAdmin yalnız admin hedefte anlamlı.
		{"lastAdmin ama hedef viewer", "a1", viewer, true, 0},
	}
	for _, c := range cases {
		if got, _ := blockRefusal(c.caller, c.target, c.lastAdmin); got != c.want {
			t.Errorf("%s: code = %d, want %d", c.name, got, c.want)
		}
	}
}
