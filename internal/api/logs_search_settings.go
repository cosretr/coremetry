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
// v0.10.1853 (Log Search Faz 2 dilim 2.2) — `businessFields`: alan kenar
// çubuğunun "İş alanları" bölümü, etiket → ES alan adayları (ilk eşleşen aday
// kullanılır; çözüm istemcide eşleme listesine karşı, lib/logBusinessFields).
// Varsayılan BOŞ (alan adı uydurulmaz; boşken bölüm gizli). Sınırlar: ≤20
// tanım, etiket 1–64 karakter (büyük/küçük harf duyarsız tekil), tanım başına
// 1–8 aday, aday 1–256 karakter, boşluk / tırnak / joker / ters bölü yok.
// PUT TAM blob yazar (system_settings tam-satır: istemci maxSample'ı da yollar).
// Yalnız Elasticsearch log yolunda anlamlı; CH'de eşleme keşfi yok → bölüm gizli.
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
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	MaxSample      int                 `json:"maxSample"`
	BusinessFields []logsBusinessField `json:"businessFields"` // v0.10.1853
}

// logsBusinessField — v0.10.1853: kenar çubuğu "İş alanları" tanımı.
type logsBusinessField struct {
	Label  string   `json:"label"`
	Fields []string `json:"fields"`
}

const (
	logsBusinessFieldsMax  = 20
	logsBusinessLabelMax   = 64
	logsBusinessCandMax    = 8
	logsBusinessFieldNmMax = 256
)

// normalizeLogsSearchSettings — SAF okuma-yolu kelepçesi. Bozuk iş alanı
// tanımları okumada SESSİZCE düşer (yazım yolu 400 verir).
func normalizeLogsSearchSettings(c logsSearchSettings) logsSearchSettings {
	if c.MaxSample < logsSampleMin || c.MaxSample > logsSampleHardCap {
		c.MaxSample = logsSampleDefaultMax
	}
	c.BusinessFields, _ = sanitizeLogsBusinessFields(c.BusinessFields, false)
	return c
}

// validateLogsSearchSettings — SAF PUT doğrulaması: 0 = varsayılan; aralık
// dışı 400 (okuma kelepçesi bozuk satırlar içindir, yazım hatasını gizlemez).
func validateLogsSearchSettings(in logsSearchSettings) (logsSearchSettings, error) {
	if in.MaxSample != 0 && (in.MaxSample < logsSampleMin || in.MaxSample > logsSampleHardCap) {
		return in, fmt.Errorf("maxSample %d–%d olmalı (0 = varsayılan %d)", logsSampleMin, logsSampleHardCap, logsSampleDefaultMax)
	}
	if _, err := sanitizeLogsBusinessFields(in.BusinessFields, true); err != nil {
		return in, err
	}
	return normalizeLogsSearchSettings(in), nil
}

// logsBusinessFieldNameOK — SAF: ES alan yolu adayı (boşluk/kontrol, tırnak,
// joker, ters bölü yok — sorguya ve önbellek anahtarına aynen girer).
func logsBusinessFieldNameOK(f string) bool {
	if f == "" || len(f) > logsBusinessFieldNmMax {
		return false
	}
	for _, r := range f {
		if r <= ' ' || r == 0x7f || strings.ContainsRune("\"'*?\\`", r) {
			return false
		}
	}
	return true
}

// sanitizeLogsBusinessFields — SAF: kırp + tekilleştir + sınırla. strict
// (PUT) ilk ihlalde hata döner; değilse (okuma) ihlal eden tanım düşer.
// Dönüş hiçbir zaman nil değil (JSON `[]`).
func sanitizeLogsBusinessFields(in []logsBusinessField, strict bool) ([]logsBusinessField, error) {
	out := make([]logsBusinessField, 0, len(in))
	labels := map[string]bool{}
	for i, d := range in {
		label := strings.TrimSpace(d.Label)
		var bad string
		switch {
		case label == "":
			bad = "etiket boş"
		case utf8.RuneCountInString(label) > logsBusinessLabelMax:
			bad = fmt.Sprintf("etiket en çok %d karakter", logsBusinessLabelMax)
		case labels[strings.ToLower(label)]:
			bad = fmt.Sprintf("etiket %q tekrar ediyor", label)
		}
		var fields []string
		seen := map[string]bool{}
		for _, f := range d.Fields {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			if !logsBusinessFieldNameOK(f) && bad == "" {
				bad = fmt.Sprintf("alan adı %q geçersiz (boşluk, tırnak, joker, ters bölü olamaz; en çok %d karakter)", f, logsBusinessFieldNmMax)
			}
			seen[f] = true
			fields = append(fields, f)
		}
		switch {
		case bad != "":
		case len(fields) == 0:
			bad = "en az bir alan adayı gerekli"
		case len(fields) > logsBusinessCandMax:
			bad = fmt.Sprintf("tanım başına en çok %d aday", logsBusinessCandMax)
		}
		if bad == "" && len(out) >= logsBusinessFieldsMax {
			bad = fmt.Sprintf("en çok %d iş alanı", logsBusinessFieldsMax)
		}
		if bad != "" {
			if strict {
				return nil, fmt.Errorf("iş alanı %d: %s", i+1, bad)
			}
			continue
		}
		labels[strings.ToLower(label)] = true
		out = append(out, logsBusinessField{Label: label, Fields: fields})
	}
	return out, nil
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
