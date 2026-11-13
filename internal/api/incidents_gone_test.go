package api

// incidents_gone_test.go — v0.10.2075 Incidents ölü kod 5 (operatör onayı
// 2026-11-13). Dokuz /api/incidents* rotası + /api/settings/incidents tek
// 410 Gone işleyicisine indi. Belirti olmasın diye: rota silinirse yol SPA
// catch-all'ına düşer ve 200 + index.html döner (eski istemci sessizce
// "başarı" görür). Burada metot × yol → 410 + JSON gövdesi, rol sınıfı ve
// mux'ta catch-all yerine bu kalıbın eşleştiği çivilenir.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

var incidentsGonePaths = []string{
	"/api/incidents",
	"/api/incidents/inc-1",
	"/api/incidents/inc-1/ack",
	"/api/incidents/inc-1/resolve",
	"/api/incidents/inc-1/note",
	"/api/incidents/inc-1/timeline",
	"/api/incidents/inc-1/problems",
	"/api/settings/incidents",
}

var incidentsGoneMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete,
}

func incidentsGoneReq(method, path, role string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(`{"title":"x"}`))
	if role != "" {
		r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{Email: "a@example.com", Role: role}))
	}
	return r
}

func TestIncidentsGoneAllMethodsAllPaths(t *testing.T) {
	mux := (&Server{}).buildMux()
	for _, p := range incidentsGonePaths {
		for _, m := range incidentsGoneMethods {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, incidentsGoneReq(m, p, auth.RoleAdmin))
			if w.Code != http.StatusGone {
				t.Errorf("%s %s: %d, istenen 410", m, p, w.Code)
				continue
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("%s %s: Content-Type %q", m, p, ct)
			}
			if m == http.MethodHead {
				continue // HEAD gövdesiz
			}
			var body map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Errorf("%s %s: JSON değil: %q", m, p, w.Body.String())
				continue
			}
			if body["error"] != incidentsRetiredMsg || !strings.Contains(body["error"], "Anomalies") {
				t.Errorf("%s %s: gövde %v", m, p, body)
			}
		}
	}
}

// Rol sınıfı eski rota tablosuyla aynı: okuma viewer'a açık, yazma
// editor+, ayar yolu admin; kimliksiz istek 401 (bilgi sızmaz).
func TestIncidentsGoneRoleClass(t *testing.T) {
	mux := (&Server{}).buildMux()
	cases := []struct {
		method, path, role string
		want               int
	}{
		{http.MethodGet, "/api/incidents", auth.RoleViewer, http.StatusGone},
		{http.MethodGet, "/api/incidents/inc-1/timeline", auth.RoleViewer, http.StatusGone},
		{http.MethodPost, "/api/incidents/inc-1/ack", auth.RoleViewer, http.StatusForbidden},
		{http.MethodPost, "/api/incidents/inc-1/ack", auth.RoleEditor, http.StatusGone},
		{http.MethodPut, "/api/incidents/inc-1", auth.RoleEditor, http.StatusGone},
		{http.MethodGet, "/api/settings/incidents", auth.RoleEditor, http.StatusForbidden},
		{http.MethodPut, "/api/settings/incidents", auth.RoleAdmin, http.StatusGone},
		{http.MethodPost, "/api/incidents/inc-1/ack", "", http.StatusUnauthorized},
		{http.MethodGet, "/api/settings/incidents", "", http.StatusUnauthorized},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, incidentsGoneReq(c.method, c.path, c.role))
		if w.Code != c.want {
			t.Errorf("%s %s rol=%q: %d, istenen %d", c.method, c.path, c.role, w.Code, c.want)
		}
	}
}

// SPA catch-all'ına düşmez: mux bu yolları "/" kalıbıyla değil kendi
// kalıbıyla eşler.
func TestIncidentsGoneNotServedBySPA(t *testing.T) {
	if _, ok := extraRouteRegistrars["incidents-gone"]; !ok {
		t.Fatal("incidents-gone defterde yok")
	}
	for _, gone := range []string{"incidents", "incidents-settings"} {
		if _, ok := extraRouteRegistrars[gone]; ok {
			t.Errorf("eski kayıt %q geri gelmemeli", gone)
		}
	}
	mux := (&Server{}).buildMux()
	for _, p := range incidentsGonePaths {
		for _, m := range incidentsGoneMethods {
			_, pattern := mux.Handler(httptest.NewRequest(m, p, nil))
			if pattern == "/" || !strings.Contains(pattern, "incidents") {
				t.Errorf("%s %s: kalıp %q — SPA catch-all'ına düşüyor", m, p, pattern)
			}
		}
	}
	// Komşu yollar etkilenmez.
	if _, p := mux.Handler(httptest.NewRequest(http.MethodPut, "/api/status-page/incidents/x/publish", nil)); p != "PUT    /api/status-page/incidents/{id}/publish" {
		t.Errorf("status-page publish kalıbı değişti: %q", p)
	}
}

// withIncidentsOn — eski akış dallarını sınayan testler için süreç bayrağı
// (Inbox testleri de kullanır); t.Cleanup ile geri alınır.
func withIncidentsOn(t *testing.T, on bool) {
	t.Helper()
	prev := chstore.IncidentsEnabled()
	chstore.SetIncidentsEnabled(on)
	t.Cleanup(func() { chstore.SetIncidentsEnabled(prev) })
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
