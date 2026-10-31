package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// presence_online_test.go — v0.10.1736 "Kim çevrimiçi" her role.
// Sözleşme: GET /api/presence/online oturumlu her role 200, oturumsuza
// 401; satır yalnız {id, name, team, lastSeenAt} — e-posta / rol asla;
// admin'e özel GET /api/users değişmez (viewer 403).

func TestShapeOnlineUsers(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC).UnixNano()
	m := time.Minute.Nanoseconds()
	users := []chstore.User{
		{ID: "u1", Email: "zeynep@example.test", FullName: "Zeynep K", Team: "platform"},
		{ID: "u2", Email: "ali@example.test"},
		{ID: "u3", Email: "eski@example.test", FullName: "Eski"},
		{ID: "u4", Email: "kapali@example.test", FullName: "Kapalı", Disabled: true},
		{ID: "u5", Email: "yok@example.test", FullName: "Damgasız"},
		{ID: "u6", Email: "bora@example.test", FullName: "  bora  "},
	}
	seen := map[string]int64{
		"u1": now - m, "u2": now - 4*m, "u3": now - 5*m, "u4": now, "u6": now - 2*m,
	}
	cases := []struct {
		name  string
		limit int
		want  []onlineUser
	}{
		{"eşik, devre dışı, ad geri-düşmesi, sıra", 200, []onlineUser{
			{ID: "u2", Name: "ali", LastSeenAt: now - 4*m},
			{ID: "u6", Name: "bora", LastSeenAt: now - 2*m},
			{ID: "u1", Name: "Zeynep K", Team: "platform", LastSeenAt: now - m},
		}},
		{"tavan sıralamadan sonra", 2, []onlineUser{
			{ID: "u2", Name: "ali", LastSeenAt: now - 4*m},
			{ID: "u6", Name: "bora", LastSeenAt: now - 2*m},
		}},
	}
	for _, c := range cases {
		if got := shapeOnlineUsers(users, seen, now, c.limit); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
	if got := shapeOnlineUsers(nil, nil, now, 200); got == nil || len(got) != 0 {
		t.Errorf("boş sonuç boş dilim olmalı, got %#v", got)
	}
}

func TestOnlineDisplayName(t *testing.T) {
	cases := []struct{ full, email, want string }{
		{"Ada Y", "ada@example.test", "Ada Y"},
		{"", "ada@example.test", "ada"},
		{"   ", "ada.y@example.test", "ada.y"},
		{"", "yerel", "yerel"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := onlineDisplayName(c.full, c.email); got != c.want || strings.Contains(got, "@") {
			t.Errorf("(%q,%q) = %q want %q", c.full, c.email, got, c.want)
		}
	}
}

func TestPresenceOnlineCacheKeyCarriesOnlyWindow(t *testing.T) {
	if presenceOnlineCacheKey != "presence:online:v1:w=300000" {
		t.Fatalf("anahtar = %q", presenceOnlineCacheKey)
	}
}

func presenceTestServer() *Server {
	fc := &fakeCache{}
	return &Server{cache: fc, l1: newL1Cache(8), stats: newCacheStats(), presence: newPresenceTracker(fc)}
}

// Yanıt alan kümesi: e-posta ve rol sızmaz.
func TestServePresenceOnlineFields(t *testing.T) {
	s := presenceTestServer()
	s.presence.last["u1"] = time.Now().Add(-time.Minute) // Redis'siz yerel damga
	load := func(context.Context) ([]chstore.User, error) {
		return []chstore.User{{ID: "u1", Email: "ada@example.test", Role: auth.RoleAdmin, Team: "sre", FullName: "", Org: "Org X"}}, nil
	}
	req := httptest.NewRequest("GET", "/api/presence/online", nil)
	req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u9", Role: auth.RoleViewer}))
	rec := httptest.NewRecorder()
	s.servePresenceOnline(rec, req, load)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "@") || strings.Contains(body, "example.test") || strings.Contains(body, "Org X") {
		t.Fatalf("e-posta/dizin sızdı: %s", body)
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("decode: %v %s", err, body)
	}
	keys := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"id", "lastSeenAt", "name", "team"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("alanlar %v want %v", keys, want)
	}
	if rows[0]["name"] != "ada" {
		t.Fatalf("name = %v", rows[0]["name"])
	}
}

// Gerçek mux: rota kayıtlı, oturumsuz 401, her rol 200 (önbellek tohumlu —
// store'a inilmez), admin'e özel /api/users viewer'a 403 kalır.
func TestPresenceOnlineRouteGate(t *testing.T) {
	s := presenceTestServer()
	s.l1.set(presenceOnlineCacheKey, []byte("[]"), time.Minute)
	mux := s.buildMux()

	do := func(path string, c *auth.Claims) (int, string) {
		req := httptest.NewRequest("GET", path, nil)
		if c != nil {
			req = req.WithContext(auth.ContextWithClaims(req.Context(), c))
		}
		h, pattern := mux.Handler(req)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, pattern
	}

	if code, pattern := do("/api/presence/online", nil); pattern != "GET /api/presence/online" || code != http.StatusUnauthorized {
		t.Fatalf("oturumsuz: kalıp %q kod %d (401 bekleniyor)", pattern, code)
	}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin} {
		if code, _ := do("/api/presence/online", &auth.Claims{UserID: "u-" + role, Role: role}); code != http.StatusOK {
			t.Errorf("%s: %d (200 bekleniyor)", role, code)
		}
	}
	if code, _ := do("/api/users", &auth.Claims{UserID: "v", Role: auth.RoleViewer}); code != http.StatusForbidden {
		t.Errorf("/api/users viewer: %d (403 bekleniyor — Users listesi admin'e özel kalır)", code)
	}
}
