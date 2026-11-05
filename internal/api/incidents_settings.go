package api

// incidents_settings.go — v0.10.1826 (operatör kararı 2026-11-05: "Incident
// sekmesi yerine Anomalies olsun; eski incident'lar devre dışı kalsın";
// teyit: "şu anki incidents akışı zaten sağlıklı çalışmıyor"). Plan
// docs/anomaly/learning-loop-audit.md §16.1 E1.
//
//   GET /api/settings/incidents → {"enabled":bool}            admin
//   PUT /api/settings/incidents {"enabled":bool}              admin · audit incidents.settings
//
// Eski Incidents akışının TEK ana anahtarı (varsayılan KAPALI). Depo
// chstore/incidents_settings.go (boot + 30 s cfgRefresh + atomic); PUT
// config-reload yayınlar ki API pod'ları 30 s beklemeden aynı değeri
// sunsun. Viewer / editor durumu Settings'ten DEĞİL kabuk yükünden görür:
// /api/auth/session · /api/auth/me kullanıcı gövdesinde
// `features.incidents` (shellFeatures) — ikinci istek, polling yok.
//
// Neden api.go'da değil: TestApiGoDoesNotGrow. Neden mevcut bir aile
// blobunun (anomaly_promotion …) alanı değil: anahtar tek bir aileye değil
// incident yüzeyinin tamamına ait (kapılar, bildirim, Problems listesi,
// menü); ayrıntı chstore dosyasının başlığında.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("incidents-settings", (*Server).registerIncidentsSettingsRoutes) }

func (s *Server) registerIncidentsSettingsRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/settings/incidents", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getIncidentsSettings)))
	mux.Handle("PUT /api/settings/incidents", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putIncidentsSettings)))
}

// incidentsReloadSvc — config-reload kanal adı (cache.go reloadConfigOnSignal).
const incidentsReloadSvc = "incidents"

func (s *Server) getIncidentsSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, chstore.IncidentsSettings{Enabled: chstore.IncidentsEnabled()})
}

func (s *Server) putIncidentsSettings(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	var in chstore.IncidentsSettings
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	prev := chstore.IncidentsEnabled()
	saved, err := s.store.SaveIncidentsSettings(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "incidents.settings", "settings", chstore.IncidentsSettingsKey,
		fmt.Sprintf(`{"before":{"enabled":%v},"after":{"enabled":%v}}`, prev, saved.Enabled))
	s.publishConfigReload(r.Context(), incidentsReloadSvc)
	// Problems listesi / rozet anahtarı anahtar durumunu taşır; yine de eski
	// görünümü hemen düşür (read-your-writes).
	s.invalidateInboxCaches(r)
	writeJSON(w, saved)
}

// reloadIncidentsSettings — peer pod'un config-reload kolu.
func (s *Server) reloadIncidentsSettings(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	return s.store.LoadIncidentsSettings(ctx)
}

// shellFeatures — SAF (v0.10.1826): kabuğun kullanıcı gövdesiyle aldığı
// sunucu bayrakları. `incidents` = eski Incidents akışı açık mı; FE menü,
// Problems türleri, arşiv sayfası ve Settings kutuları bunu okur.
func shellFeatures(incidentsOn bool) map[string]any {
	return map[string]any{"incidents": incidentsOn}
}

// incidentsRetiredMsg — elle ilan 410 metni (FE aynen gösterir).
const incidentsRetiredMsg = "Incidents akışı emekli; Anomalies kullanın"

// createIncidentAllowed — SAF: elle incident ilanı yalnız ana anahtar açıkken.
func createIncidentAllowed(incidentsOn bool) bool { return incidentsOn }
