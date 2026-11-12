package api

// incidents_settings_test.go — v0.10.1826 (operatör kararı 2026-11-05:
// "Incident sekmesi yerine Anomalies olsun; eski incident'lar devre dışı
// kalsın"). Ana anahtar kapalıyken elle ilan 410 + Türkçe mesaj; açıkken
// istek bugünkü yola girer (gövde doğrulaması). Kabuk bayrağı kullanıcı
// gövdesinde. Rotalar api.go'dan api_incidents.go'ya taşındı — kayıt pini.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func withIncidentsOn(t *testing.T, on bool) {
	t.Helper()
	prev := chstore.IncidentsEnabled()
	chstore.SetIncidentsEnabled(on)
	t.Cleanup(func() { chstore.SetIncidentsEnabled(prev) })
}

func TestCreateIncidentGoneWhenRetired(t *testing.T) {
	withIncidentsOn(t, false)
	s := &Server{}
	w := httptest.NewRecorder()
	s.createIncident(w, httptest.NewRequest(http.MethodPost, "/api/incidents", strings.NewReader(`{"title":"x"}`)))
	if w.Code != http.StatusGone {
		t.Fatalf("kapalı: 410 bekleniyordu, %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Incidents akışı emekli; Anomalies kullanın") {
		t.Fatalf("kapalı: Türkçe mesaj yok: %s", w.Body.String())
	}
}

func TestCreateIncidentPassesGateWhenEnabled(t *testing.T) {
	withIncidentsOn(t, true)
	s := &Server{}
	w := httptest.NewRecorder()
	// Bozuk gövde: kapıyı geçen istek bugünkü doğrulamaya düşer (400), 410 DEĞİL.
	s.createIncident(w, httptest.NewRequest(http.MethodPost, "/api/incidents", strings.NewReader(`{`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("açık: bugünkü 400 bekleniyordu, %d", w.Code)
	}
}

func TestCreateIncidentAllowedPure(t *testing.T) {
	if createIncidentAllowed(false) || !createIncidentAllowed(true) {
		t.Fatal("elle ilan yalnız açık anahtarda")
	}
}

func TestShellFeaturesCarriesIncidentsFlag(t *testing.T) {
	for _, on := range []bool{false, true} {
		if got := shellFeatures(on)["incidents"]; got != on {
			t.Errorf("incidents=%v → %v", on, got)
		}
	}
	withIncidentsOn(t, true)
	p := (&Server{}).userPayload(&chstore.User{ID: "u", Email: "a@example.com", Role: "viewer"})
	f, ok := p["features"].(map[string]any)
	if !ok || f["incidents"] != true {
		t.Fatalf("userPayload features.incidents taşımalı: %v", p["features"])
	}
}

// TestIncidentsSettingsAlwaysRetired — v0.10.2024 (operatör 2026-11-12:
// prod'da kayıtlı anahtar açık kaldığı için menü Incidents gösterip
// Anomalies'i gizliyordu; "sen default yap onu"). GET hep kapalı; PUT
// açma isteği 410 ve deposuz Server'da bile panik/yazma yok (store nil).
func TestIncidentsSettingsAlwaysRetired(t *testing.T) {
	for _, on := range []bool{false, true} {
		withIncidentsOn(t, on)
		w := httptest.NewRecorder()
		(&Server{}).getIncidentsSettings(w, httptest.NewRequest(http.MethodGet, "/api/settings/incidents", nil))
		if !strings.Contains(w.Body.String(), `"enabled":false`) {
			t.Errorf("süreç bayrağı=%v: GET hep kapalı olmalı, gövde %s", on, w.Body.String())
		}
	}
	withIncidentsOn(t, false)
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"enabled":true}`, http.StatusGone},
		{`{"enabled":false}`, http.StatusOK},
		{`{`, http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		(&Server{}).putIncidentsSettings(w, httptest.NewRequest(http.MethodPut, "/api/settings/incidents", strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Errorf("PUT %s: %d, istenen %d", tc.body, w.Code, tc.code)
		}
		if tc.code == http.StatusGone && !strings.Contains(w.Body.String(), incidentsRetiredMsg) {
			t.Errorf("PUT açma: emekli mesajı yok: %s", w.Body.String())
		}
	}
	if chstore.IncidentsEnabled() {
		t.Fatal("PUT açma isteği süreç bayrağını açmamalı")
	}
}

// TestIncidentRoutesRegisteredOutsideAPIGo — rotalar api_incidents.go'da;
// ayar dosyası artık yazmıyor (v0.10.2024), config-reload kolu yok.
func TestIncidentRoutesRegisteredOutsideAPIGo(t *testing.T) {
	api, _ := os.ReadFile("api.go")
	if strings.Contains(string(api), `" /api/incidents`) || strings.Contains(string(api), `"GET    /api/incidents`) {
		t.Error("api.go /api/incidents kaydı taşımamalı")
	}
	inc, _ := os.ReadFile("api_incidents.go")
	if !strings.Contains(string(inc), `registerRoutesExtra("incidents", (*Server).registerIncidentRoutes)`) {
		t.Error("api_incidents.go registerRoutesExtra kaydı yok")
	}
	set, _ := os.ReadFile("incidents_settings.go")
	for _, want := range []string{
		`registerRoutesExtra("incidents-settings"`,
		`auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putIncidentsSettings))`,
	} {
		if !strings.Contains(string(set), want) {
			t.Errorf("incidents_settings.go: %q yok", want)
		}
	}
	for _, bad := range []string{"SaveIncidentsSettings", "LoadIncidentsSettings", "publishConfigReload"} {
		if strings.Contains(string(set), bad) {
			t.Errorf("incidents_settings.go %q taşımamalı (anahtar kalktı, v0.10.2024)", bad)
		}
	}
}
