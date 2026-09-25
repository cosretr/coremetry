package api

// oracle_routes.go — v0.10.580, Oracle hata tablosu AŞAMA 1 (audit:
// docs/audit/oracle-error-log-2026-09-09.md §7). Yalnız datasource
// tanımı, credential ve bağlantı testi; poller / metrik / Problem
// Aşama 2-3'te.
//
// api.go'ya SIFIR satır: kayıt route_registry.go defterine init()'ten
// düşüyor (preferences_routes.go emsali, v0.10.247). buildMux defteri ad
// sırasıyla boşaltır ve TestMuxRoutePatterns çakışmayı görür.
//
//	GET  /api/settings/oracle       admin — snapshot (şifre MASKELİ,
//	                                 passwordRef bir referans olduğu için görünür)
//	PUT  /api/settings/oracle       admin — tüm liste atomik; audit
//	                                 settings.oracle.update
//	POST /api/settings/oracle/test  admin — formdaki TEK kaynağı KAYDETMEDEN
//	                                 dener; başarısızlıkta 200 + ok:false
//	GET  /api/oracle/status         her rol — kaynak başına ad/etkin/son hata
//	                                 (şifresiz); viewer state'i GÖRMELİ
//
// İlk üçü admin (influx/vmetrics gerekçesi): kaydedilen credential
// operatörün üretim veritabanını okur, GET'te bile host/şema/tablo var.
//
// serveCached YOK: durum ucu bellek-içi ve pod-yerel (CH round-trip yok),
// influx durum ucundaki "işçi belleği CACHE DIŞI" duruşunun aynısı. Cache
// burada yalnız bayat bir rozet üretirdi.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/oracle"
)

func init() { registerRoutesExtra("oracle", (*Server).registerOracleRoutes) }

func (s *Server) registerOracleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/oracle", auth.RequireRole(auth.RoleAdmin, s.getOracleSettings))
	mux.HandleFunc("PUT /api/settings/oracle", auth.RequireRole(auth.RoleAdmin, s.putOracleSettings))
	mux.HandleFunc("POST /api/settings/oracle/test", auth.RequireRole(auth.RoleAdmin, s.testOracleSource))
	mux.HandleFunc("GET /api/oracle/status", s.getOracleStatus)
	// v0.10.897 (Aşama 3 dilim D) — öğrenilmiş op→servis haritası: salt okuma +
	// sıfırlama (blob system_settings'te; worker 5 dk'da bir yeniden okur).
	mux.HandleFunc("GET /api/settings/oracle/{id}/learned", auth.RequireRole(auth.RoleAdmin, s.getOracleLearned))
	mux.HandleFunc("POST /api/settings/oracle/{id}/learned/reset", auth.RequireRole(auth.RoleAdmin, s.resetOracleLearned))
}

// SetOracle — Oracle kaynak servisi (v0.10.580; main.go her rolde çağırır,
// api pod'u Settings'i servis ediyor). nil-safe: handler'lar 503.
func (s *Server) SetOracle(o *oracle.Service) { s.oracle = o }

// oracleStore — s.store'u dar arayüze GÜVENLİ çevirir. Doğrudan geçirmek
// TİPLİ-NİL üretir: nil *chstore.Store bir arayüz değerine sarıldığında
// `store == nil` YANLIŞ döner ve paketin nil-guard'ı atlanıp ilk alan
// erişiminde panic'lenir.
func (s *Server) oracleStore() oracle.SettingsStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

func (s *Server) getOracleSettings(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		http.Error(w, "oracle not available", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, s.oracle.Snapshot())
}

func (s *Server) putOracleSettings(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		http.Error(w, "oracle not available", http.StatusServiceUnavailable)
		return
	}
	var in oracle.Settings
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	cfg, err := oracle.Normalize(in, s.oracle.CurrentSettings(), oracle.NewSourceID)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// WithoutCancel: istemci cevabı beklemeden kapatırsa yazım yarıda
	// kalmasın (thanos_handlers.go dersi).
	if err := s.oracle.SavePersisted(context.WithoutCancel(r.Context()), s.oracleStore(), cfg); err != nil {
		writeErr(w, err)
		return
	}
	s.publishConfigReload(r.Context(), "oracle")
	names := make([]string, 0, len(cfg.Sources))
	enabled := 0
	// v0.10.902 — özel SQL her aralıkta üretim DB'sinde koşar: "kim, ne zaman,
	// hangi sorgu" izlenebilsin diye kaynak başına kip + metnin özeti (metnin
	// kendisi değil).
	queries := map[string]string{}
	for _, src := range cfg.Sources {
		names = append(names, src.Name)
		if src.Enabled {
			enabled++
		}
		if oracle.IsCustom(src) {
			sum := sha256.Sum256([]byte(src.CustomSQL))
			queries[src.Name] = fmt.Sprintf("custom sha=%x len=%d window=%d", sum[:6], len(src.CustomSQL), src.WindowMin)
		}
	}
	// details'a ŞİFRE/DSN girmez — yalnız sayılar ve adlar.
	details, _ := json.Marshal(map[string]any{
		"sources": len(cfg.Sources), "enabled": enabled, "names": names, "customQueries": queries,
	})
	s.audit(r, "settings.oracle.update", "settings", "oracle_sources", string(details))
	writeJSON(w, s.oracle.Snapshot())
}

func (s *Server) testOracleSource(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		http.Error(w, "oracle not available", http.StatusServiceUnavailable)
		return
	}
	var in oracle.SourceConfig
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	// Kapalı bir taslağı da denemek meşru: doğrulama etkin-kaynak
	// kurallarıyla koşsun ki eksik alan testte değil formda görünsün.
	in.Enabled = true
	cfg, err := oracle.Normalize(oracle.Settings{Sources: []oracle.SourceConfig{in}}, s.oracle.CurrentSettings(), oracle.NewSourceID)
	if err != nil {
		// Doğrulama hatası da BAŞARILI bir cevaptır: 200 + ok:false.
		writeJSON(w, oracle.TestResult{OK: false, Error: err.Error(), Columns: []string{}})
		return
	}
	// v0.10.768 — pencere (5/15/60) + CH trace araması (özet "hangi servis").
	opt := oracle.TestOptions{WindowMin: oracle.ClampTestWindow(parseInt(r.URL.Query().Get("windowMin"), 0))}
	if s.store != nil {
		opt.TraceLookup = s.oracleTraceLookup              // v0.10.917 — CH + Tempo emniyet ağı
		opt.AliveServices = s.store.ListActiveServiceNames // v0.10.908 — pod adından servis
	}
	res := s.oracle.TestWith(r.Context(), cfg.Sources[0], opt)
	// v0.10.855 (scale-audit) — canlı Oracle bağlantısı, operatör DSN/şifresiyle: iz bırakır.
	s.audit(r, "settings.oracle.test", "settings", cfg.Sources[0].Name, fmt.Sprintf("ok=%v windowMin=%d", res.OK, opt.WindowMin))
	writeJSON(w, res)
}

// oracleStatusPayload — Aşama 1 dürüstlüğü: poller YOK, dolayısıyla
// "son hata" bu pod'da koşmuş bağlantı testinin izidir. Uydurma bir
// "sağlıklı" satırı basmıyoruz.
type oracleStatusPayload struct {
	// v0.10.601 — worker liderinin poll durumu (system_settings blobu; influx
	// v0.10.333 duruşu). nil = henüz yayın yok.
	Poll        *oracle.WorkerStatusSnapshot `json:"poll,omitempty"`
	Sources     []oracle.SourceStatus        `json:"sources"`
	GeneratedAt int64                        `json:"generatedAt"`
}

func (s *Server) getOracleStatus(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		http.Error(w, "oracle not available", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, oracleStatusPayload{
		Sources:     s.oracle.Status(),
		Poll:        s.oraclePollStatus(r.Context()),
		GeneratedAt: time.Now().UnixMilli(),
	})
}

// oraclePollStatus — v0.10.601: poll işçisi yalnız worker liderinde koşar;
// Settings'i servis eden API pod'u durumu blobdan okur. Hata = nil (durum
// ucu poll blobu yüzünden 500 vermez).
func (s *Server) oraclePollStatus(ctx context.Context) *oracle.WorkerStatusSnapshot {
	if s.store == nil {
		return nil
	}
	raw, err := s.store.GetSetting(ctx, oracle.WorkerStatusKey)
	if err != nil {
		return nil
	}
	snap, ok := oracle.DecodeWorkerStatus(raw)
	if !ok {
		return nil
	}
	return snap
}

// getOracleLearned — v0.10.897: kaynağın öğrenilmiş op→servis haritası (blob).
func (s *Server) getOracleLearned(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "source id required", http.StatusBadRequest)
		return
	}
	raw, err := s.store.GetSetting(r.Context(), "oracle_opsvc:"+id)
	if err != nil {
		writeErr(w, err)
		return
	}
	m := oracle.LearnedMap{V: 1, Entries: map[string]*oracle.LearnedEntry{}}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	if m.Entries == nil {
		m.Entries = map[string]*oracle.LearnedEntry{}
	}
	writeJSON(w, map[string]any{"sourceId": id, "count": len(m.Entries), "entries": m.Entries})
}

// resetOracleLearned — v0.10.897: haritayı boşaltır (audit).
func (s *Server) resetOracleLearned(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "source id required", http.StatusBadRequest)
		return
	}
	if err := s.store.PutSetting(r.Context(), "oracle_opsvc:"+id, []byte(`{"v":1,"entries":{}}`)); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "settings.oracle.learned_reset", "settings", id, "op→servis haritası sıfırlandı")
	writeJSON(w, map[string]any{"ok": true, "sourceId": id})
}
