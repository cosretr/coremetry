package api

// logs_search_settings.go — v0.10.1831 (Log Search Faz 1 dilim 1.8; audit
// §8 "örnek boyutu", açık soru §9-6 karar: varsayılan 500).
//
// /logs örnek boyutunun ÜST SINIRI tek system_settings blobunda: anahtar
// `logs_search` (CLAUDE.md invariant 6). Boot'ta hidre edilir, 30 s'de bir
// yenilenir (main.go), PUT anında bu pod'da canlı değiştirilir ve
// publishConfigReload ile peer pod'lara yayılır (cache.go
// reloadConfigOnSignal "logs_search").
//
//   GET /api/settings/logs-search   tüm roller (viewer sınırı GÖRÜR, salt-okunur)
//   PUT /api/settings/logs-search   admin; audit `logs_search.settings`
//
// Sınırlar (maxSample): 10 … 5000, varsayılan 2000.
//   · 5000 tavanı: ES `index.max_result_window` varsayılanı 10.000 — `size`
//     search_after ile bile bu sınırı aşamaz, tek istek yarısında kalır;
//     dar `_source` ile satır ~1–2 KB → 5000 satır ≈ 5–10 MB tek yanıt
//     (tarayıcı + Go JSON belleği için tavan). Biriktirme tamponu 10.000
//     ("daha fazla yükle" iki tam örnek).
//   · 10 tabanı: URL `smp` alt sınırıyla aynı (audit "10..üst sınır").
// CH backend'i aynı sınırı uygular (chstore logsMaxLimit = 5000).
//
// Neden ayrı dosya + blob: Server alanları api.go'da ve api.go BÜYÜMEZ
// (TestApiGoDoesNotGrow); paket düzeyi atomik promql_console emsali.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("logs_search_settings", (*Server).registerLogsSearchSettingsRoutes) }

func (s *Server) registerLogsSearchSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/logs-search", s.getLogsSearchSettings)
	mux.Handle("PUT /api/settings/logs-search",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putLogsSearchSettings)))
}

const logsSearchSettingKey = "logs_search"

const (
	logsSampleMin        = 10
	logsSampleHardCap    = 5000
	logsSampleDefaultMax = 2000
	// logsSampleDefault — `smp` yokken istek boyu (açık soru §9-6 karar 500).
	logsSampleDefault = 500
)

// logsSearchSettings — `logs_search` blobu (FE lib/types.ts LogsSearchSettings).
type logsSearchSettings struct {
	MaxSample int `json:"maxSample"`
}

// normalizeLogsSearchSettings — SAF okuma-yolu kelepçesi.
func normalizeLogsSearchSettings(c logsSearchSettings) logsSearchSettings {
	if c.MaxSample < logsSampleMin || c.MaxSample > logsSampleHardCap {
		c.MaxSample = logsSampleDefaultMax
	}
	return c
}

// validateLogsSearchSettings — SAF PUT doğrulaması: 0 = varsayılan; aralık
// dışı 400 (okuma kelepçesi bozuk satırlar içindir, yazım hatasını gizlemez).
func validateLogsSearchSettings(in logsSearchSettings) (logsSearchSettings, error) {
	if in.MaxSample != 0 && (in.MaxSample < logsSampleMin || in.MaxSample > logsSampleHardCap) {
		return in, fmt.Errorf("maxSample %d–%d olmalı (0 = varsayılan %d)", logsSampleMin, logsSampleHardCap, logsSampleDefaultMax)
	}
	return normalizeLogsSearchSettings(in), nil
}

func decodeLogsSearchSettings(raw []byte) logsSearchSettings {
	var c logsSearchSettings
	if len(raw) > 0 && json.Unmarshal(raw, &c) != nil {
		c = logsSearchSettings{}
	}
	return normalizeLogsSearchSettings(c)
}

var logsSearchCfg atomic.Pointer[logsSearchSettings]

func currentLogsSearchSettings() logsSearchSettings {
	if c := logsSearchCfg.Load(); c != nil {
		return *c
	}
	return normalizeLogsSearchSettings(logsSearchSettings{})
}

func setLogsSearchSettings(c logsSearchSettings) {
	n := normalizeLogsSearchSettings(c)
	logsSearchCfg.Store(&n)
}

// logsSampleLimit — SAF: istek `limit`i → kelepçeli boy. Eksik/0 → 100
// (bugünkü sunucu varsayılanı: servis sekmesi, trace çekmeceleri, araçlar
// kendi boyunu yollar); üst sınır maxSample. Sunucuda alt sınır YOK —
// `limit=1` gibi dar okumalar (kalıcı link) aynen kalır; /logs `smp`
// tabanı (10) istemcide (lib/logsSample.ts).
func logsSampleLimit(want, maxSample int) int {
	if want <= 0 {
		return 100
	}
	if want > maxSample {
		return maxSample
	}
	return want
}

// LoadLogsSearchSettings — boot + yenileme + peer sinyali. Okuma hatasında
// son iyi değer korunur.
func (s *Server) LoadLogsSearchSettings(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	raw, err := s.store.GetSetting(ctx, logsSearchSettingKey)
	if err != nil {
		log.Printf("[logs] logs_search ayar okuması: %v (son değer korunuyor)", err)
		return
	}
	setLogsSearchSettings(decodeLogsSearchSettings(raw))
}

func (s *Server) StartLogsSearchSettingsRefresh(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.LoadLogsSearchSettings(ctx)
		}
	}
}

type logsSearchSettingsResponse struct {
	logsSearchSettings
	Defaults logsSearchSettings `json:"defaults"`
	Min      int                `json:"min"`
	HardCap  int                `json:"hardCap"`
	// SampleDefault — `smp` yokken istek boyu (FE varsayılanı aynı sayıyı okur).
	SampleDefault int `json:"sampleDefault"`
}

func newLogsSearchSettingsResponse(c logsSearchSettings) logsSearchSettingsResponse {
	return logsSearchSettingsResponse{logsSearchSettings: c, Defaults: normalizeLogsSearchSettings(logsSearchSettings{}),
		Min: logsSampleMin, HardCap: logsSampleHardCap, SampleDefault: logsSampleDefault}
}

func (s *Server) getLogsSearchSettings(w http.ResponseWriter, r *http.Request) {
	c := currentLogsSearchSettings()
	if s != nil && s.store != nil {
		if raw, err := s.store.GetSetting(r.Context(), logsSearchSettingKey); err == nil {
			c = decodeLogsSearchSettings(raw)
		} else {
			log.Printf("[logs] logs_search GET okuması: %v (canlı değer dönüyor)", err)
		}
	}
	writeJSON(w, newLogsSearchSettingsResponse(c))
}

const logsSearchSettingsMaxBody = 16 << 10

func (s *Server) putLogsSearchSettings(w http.ResponseWriter, r *http.Request) {
	var in logsSearchSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, logsSearchSettingsMaxBody)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	c, err := validateLogsSearchSettings(in)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu bağlı değil")
		return
	}
	raw, err := json.Marshal(c)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.PutSetting(context.WithoutCancel(r.Context()), logsSearchSettingKey, raw); err != nil {
		writeErr(w, err)
		return
	}
	setLogsSearchSettings(c)
	s.publishConfigReload(r.Context(), "logs_search")
	s.audit(r, "logs_search.settings", "settings", logsSearchSettingKey, string(raw))
	writeJSON(w, newLogsSearchSettingsResponse(c))
}

// logsSampleParam — URL `limit` → kelepçeli istek boyu (/logs `smp`'yi
// `limit` olarak yollar).
func logsSampleParam(raw string) int {
	n, _ := strconv.Atoi(raw)
	return logsSampleLimit(n, currentLogsSearchSettings().MaxSample)
}
