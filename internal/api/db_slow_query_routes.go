package api

// db_slow_query_routes.go — v0.10.325: yavaş SQL dedektörü ayarları
// (system_settings['db_slow_query']). Defterden kayıt (api.go büyümez).

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("db-slow-query", (*Server).registerDBSlowQueryRoutes) }

func (s *Server) registerDBSlowQueryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/db-slow-query", auth.RequireRole(auth.RoleAdmin, s.getDBSlowQuery))
	mux.HandleFunc("PUT /api/settings/db-slow-query", auth.RequireRole(auth.RoleAdmin, s.putDBSlowQuery))
}

func (s *Server) getDBSlowQuery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.GetDBSlowQuery(r.Context()))
}

// validateDBSlowQuery — saf; sınırlar operatör hatasını erken yakalar.
func validateDBSlowQuery(c chstore.DBSlowQueryConfig) error {
	if c.ThresholdMs < 100 || c.ThresholdMs > 600000 {
		return fmt.Errorf("thresholdMs must be between 100 and 600000")
	}
	if c.CriticalMs < c.ThresholdMs || c.CriticalMs > 3600000 {
		return fmt.Errorf("criticalMs must be >= thresholdMs and <= 3600000")
	}
	if c.MinExecutions < 1 || c.MinExecutions > 1000000 {
		return fmt.Errorf("minExecutions must be between 1 and 1000000")
	}
	if c.ForBuckets < 1 || c.ForBuckets > 12 {
		return fmt.Errorf("forBuckets must be between 1 and 12")
	}
	if c.CooldownSec < 0 || c.CooldownSec > 86400 {
		return fmt.Errorf("cooldownSec must be between 0 and 86400")
	}
	if h := c.Health; h != nil {
		// v0.10.1073 — db-health vidaları (aynı blob).
		if h.ErrorPct < 0.1 || h.ErrorPct > 100 {
			return fmt.Errorf("health.errorPct must be between 0.1 and 100")
		}
		if h.P99Ms < 50 || h.P99Ms > 600000 {
			return fmt.Errorf("health.p99Ms must be between 50 and 600000")
		}
		if h.P99RiseFactor < 1 || h.P99RiseFactor > 100 {
			return fmt.Errorf("health.p99RiseFactor must be between 1 and 100")
		}
		if h.MinCallerCalls < 1 || h.MinCallerCalls > 1000000 {
			return fmt.Errorf("health.minCallerCalls must be between 1 and 1000000")
		}
		if h.MinCalls < 1 || h.MinCalls > 10000000 {
			return fmt.Errorf("health.minCalls must be between 1 and 10000000")
		}
		if h.MinCallers < 1 || h.MinCallers > 50 {
			return fmt.Errorf("health.minCallers must be between 1 and 50")
		}
		if h.MaxNewPerTick < 1 || h.MaxNewPerTick > 200 {
			return fmt.Errorf("health.maxNewPerTick must be between 1 and 200")
		}
	}
	return nil
}

// keepStoredHealth — SAF (v0.10.1073): PUT gövdesi `health` taşımıyorsa
// (alanı bilmeyen eski sekme) saklı değer korunur; yoksa Normalize
// varsayılanı yazar ve operatörün kapattığı db-health sessizce geri açılırdı.
func keepStoredHealth(in, stored chstore.DBSlowQueryConfig) chstore.DBSlowQueryConfig {
	if in.Health == nil && stored.Health != nil {
		h := *stored.Health
		in.Health = &h
	}
	return in
}

func (s *Server) putDBSlowQuery(w http.ResponseWriter, r *http.Request) {
	var c chstore.DBSlowQueryConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&c); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := validateDBSlowQuery(c); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if c.Health == nil {
		stored, err := s.store.LoadDBSlowQuery(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		c = keepStoredHealth(c, stored)
	}
	saved, err := s.store.SaveDBSlowQuery(r.Context(), c)
	if err != nil {
		writeErr(w, err)
		return
	}
	h := chstore.NormalizeDBHealth(chstore.DBHealthConfig{})
	if saved.Health != nil {
		h = *saved.Health
	}
	s.audit(r, "settings.db_slow_query.update", "settings", "db_slow_query",
		fmt.Sprintf(`{"enabled":%v,"thresholdMs":%v,"criticalMs":%v,"minExecutions":%d,"forBuckets":%d,"cooldownSec":%d,`+
			`"health":{"enabled":%v,"errorPct":%v,"p99Ms":%v,"p99RiseFactor":%v,"minCalls":%d,"minCallerCalls":%d,"minCallers":%d,"maxNewPerTick":%d}}`,
			saved.Enabled, saved.ThresholdMs, saved.CriticalMs, saved.MinExecutions, saved.ForBuckets, saved.CooldownSec,
			h.On(), h.ErrorPct, h.P99Ms, h.P99RiseFactor, h.MinCalls, h.MinCallerCalls, h.MinCallers, h.MaxNewPerTick))
	writeJSON(w, saved)
}
