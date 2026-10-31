package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// user_access_test.go — v0.10.1734 (kullanıcı detay sayfası S4: Yetkiler +
// Bildirimler). Coremetry'de kişi başına abonelik yok; sekme kullanıcıya
// ulaşan kanal ve ekip yollarını doğru ayırmalı (doğrudan adres ≠ takım).

func TestResolveUserPermissions(t *testing.T) {
	cases := []struct {
		role, custom string
		pages        []string
		scope        string
		wantPages    []string
	}{
		{"admin", "", nil, "all", []string{}},
		{"editor", "ops", []string{"/inbox"}, "all", []string{}},
		{"viewer", "", nil, "viewer", []string{}},
		{"viewer", "ops", []string{"/traces", "/inbox"}, "pages", []string{"/inbox", "/traces"}},
		{"viewer", "gone", nil, "missing", []string{}},
	}
	for _, c := range cases {
		got := resolveUserPermissions(c.role, c.custom, c.pages)
		if got.Scope != c.scope || !reflect.DeepEqual(got.Pages, c.wantPages) {
			t.Errorf("%s/%s: %+v", c.role, c.custom, got)
		}
	}
}

func TestUserNotifyReach(t *testing.T) {
	cfg := func(r ...string) json.RawMessage { b, _ := json.Marshal(map[string]any{"recipients": r}); return b }
	channels := []chstore.NotificationChannel{
		{ID: "c1", Name: "SRE mail", Type: "email", Enabled: true, Config: cfg("oncall@example.com, Ayse@Example.com")},
		{ID: "c2", Name: "Başka", Type: "email", Config: cfg("someone@example.com")},
		{ID: "c3", Name: "Slack", Type: "slack", Config: json.RawMessage(`{"webhookUrl":"ayse@example.com"}`)},
		{ID: "c4", Name: "Bozuk", Type: "email", Config: json.RawMessage(`{`)},
	}
	tc := chstore.TeamContacts{Enabled: true, Contacts: map[string]string{
		"platform-sre":  "platform@example.com",
		"payments-core": "pay@example.com,ayse@example.com",
		"orders-api":    "orders@example.com",
	}}
	got := userNotifyReach("ayse@example.com", " Platform-SRE ", channels, tc)
	if len(got.Channels) != 1 || got.Channels[0].ID != "c1" {
		t.Fatalf("kanallar: %+v", got.Channels)
	}
	var teams []string
	var direct []bool
	for _, r := range got.TeamRoutes {
		teams, direct = append(teams, r.Team), append(direct, r.Direct)
	}
	if !reflect.DeepEqual(teams, []string{"payments-core", "platform-sre"}) || !reflect.DeepEqual(direct, []bool{true, false}) {
		t.Fatalf("ekip yolları %v %v", teams, direct)
	}
	if !got.TeamRoutingEnabled {
		t.Fatal("ekip yönlendirme durumu taşınmalı")
	}
	empty := userNotifyReach("nobody@example.com", "", channels, tc)
	if len(empty.Channels) != 0 || len(empty.TeamRoutes) != 0 || empty.Channels == nil || empty.TeamRoutes == nil {
		t.Fatalf("eşleşme yok → boş dizi (JSON []): %+v", empty)
	}
}
