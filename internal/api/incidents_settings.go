package api

// incidents_settings.go — v0.10.1826 Incidents emekliliği; v0.10.2024
// (operatör 2026-11-12: menüde hâlâ "Incidents" görünüyordu — kayıtlı ana
// anahtar açık kalmış kurulum; "sen default yap onu"): anahtar KALKTI.
//
//   GET /api/settings/incidents → {"enabled":false}           admin
//   PUT /api/settings/incidents {"enabled":true}  → 410        admin
//   PUT /api/settings/incidents {"enabled":false} → {"enabled":false}, yazmaz
//
// Eski akış kayıtlı değerden bağımsız HER ZAMAN emekli (chstore
// incidents_settings.go). PUT hiçbir şey yazmaz → audit yok, config-reload
// yok. Viewer / editor durumu kabuk yükünden görür: /api/auth/session ·
// /api/auth/me `features.incidents` (shellFeatures) — üretimde hep false.
//
// Neden rotalar silinmedi: açma isteğine sessiz 404/SPA-200 yerine açık
// 410 + Türkçe mesaj (eski istemci / betik ne olduğunu görür). Neden
// api.go'da değil: TestApiGoDoesNotGrow.

import (
	"encoding/json"
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("incidents-settings", (*Server).registerIncidentsSettingsRoutes) }

func (s *Server) registerIncidentsSettingsRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/settings/incidents", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getIncidentsSettings)))
	mux.Handle("PUT /api/settings/incidents", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putIncidentsSettings)))
}

func (s *Server) getIncidentsSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, chstore.IncidentsSettings{Enabled: false})
}

func (s *Server) putIncidentsSettings(w http.ResponseWriter, r *http.Request) {
	var in chstore.IncidentsSettings
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if in.Enabled {
		writeJSONError(w, http.StatusGone, incidentsRetiredMsg)
		return
	}
	writeJSON(w, chstore.IncidentsSettings{Enabled: false})
}

// shellFeatures — SAF (v0.10.1826): kabuğun kullanıcı gövdesiyle aldığı
// sunucu bayrakları. `incidents` = eski Incidents akışı açık mı; üretimde
// chstore.IncidentsEnabled() hep false (v0.10.2024).
func shellFeatures(incidentsOn bool) map[string]any {
	return map[string]any{"incidents": incidentsOn}
}

// incidentsRetiredMsg — elle ilan / açma isteği 410 metni (FE aynen gösterir).
const incidentsRetiredMsg = "Incidents akışı emekli; Anomalies kullanın"

// createIncidentAllowed — SAF: elle incident ilanı yalnız akış açıkken
// (üretimde hiç; testler eski dalı SetIncidentsEnabled ile kurar).
func createIncidentAllowed(incidentsOn bool) bool { return incidentsOn }
