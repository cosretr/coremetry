package api

// ai_settings_profiles.go — v0.10.175 (operatör: "birden fazla model
// eklenebilsin, ayrı endpoint'ler de olabilir; varsayılanı admin seçer").
//
// api.go BÜYÜMEYECEK kuralı (registerVMetricsRoutes emsali): profil uçları
// burada, kayıt ai_routes.go'da (AI ucu sicili). Hepsi admin + audit; anahtar
// hiçbir yanıtta dönmez (hasKey), boş anahtar = mevcut korunur (Secrets in
// Settings kuralı). Her yazım publishConfigReload("ai") ile öteki pod'lara
// duyurulur (StartConfigRefresh de 30 s'de okur).
//
//   PUT    /api/settings/ai/profiles/{id}          upsert
//   DELETE /api/settings/ai/profiles/{id}          (varsayılan silinemez → 400)
//   POST   /api/settings/ai/profiles/{id}/default  varsayılan yap
//   POST   /api/settings/ai/profiles/{id}/test     bağlantı yoklaması (200 + {ok,error})
//   PUT    /api/settings/ai/surface-map            {intent?, background?} → profil kimliği ("" = varsayılan)
//
// Test uçu: bir bağlantı denemesinin başarısızlığı operatörün sorusuna
// BAŞARILI cevaptır → 200 + {ok:false,error} (vmetrics_handlers emsali).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func (s *Server) registerAISettingsProfileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/settings/ai/profiles/{id}", auth.RequireRole(auth.RoleAdmin, s.putAIProfile))
	mux.HandleFunc("DELETE /api/settings/ai/profiles/{id}", auth.RequireRole(auth.RoleAdmin, s.deleteAIProfile))
	mux.HandleFunc("POST /api/settings/ai/profiles/{id}/default", auth.RequireRole(auth.RoleAdmin, s.defaultAIProfile))
	mux.HandleFunc("POST /api/settings/ai/profiles/{id}/test", auth.RequireRole(auth.RoleAdmin, s.testAIProfile))
	mux.HandleFunc("PUT /api/settings/ai/surface-map", auth.RequireRole(auth.RoleAdmin, s.putAISurfaceMap))
}

// aiProfileView — API görünümü: anahtar YOK, hasKey VAR.
type aiProfileView struct {
	ID          string   `json:"id"`
	Label       string   `json:"label,omitempty"`
	Provider    string   `json:"provider"`
	BaseURL     string   `json:"baseUrl,omitempty"`
	Model       string   `json:"model,omitempty"`
	SkipTLS     bool     `json:"skipTls,omitempty"`
	HasKey      bool     `json:"hasKey"`
	MaxTokens   int      `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TimeoutS    int      `json:"timeoutS,omitempty"`
	Thinking    string   `json:"thinking,omitempty"` // v0.10.534 (modelcaps): "" | off | on
	// v0.10.2458 — admin beyanı: uç Qwen3 enable_thinking anahtarını kabul eder.
	ThinkingSwitch bool `json:"thinkingSwitch,omitempty"`
	// v0.10.1302 — CoSRE araç seti: Toolset ayarın kendisi ("" otomatik |
	// simple | full); EffectiveToolset otomatik kuralın sonucu dahil yürürlükteki
	// değer (rozet için; model adından, modelcaps.ResolveToolset).
	Toolset          string `json:"toolset,omitempty"`
	EffectiveToolset string `json:"effectiveToolset"`
	Default          bool   `json:"default,omitempty"`
	// v0.10.1138 — sohbet seçicisinin kısa açıklaması + rol allowlist'i (boş = herkes).
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	Deep        bool     `json:"deep,omitempty"` // v0.10.1150 — "Derin düşün" profili
	// v0.10.1238 — her LLM isteğine eklenen başlıklar (yalnız admin görür).
	Headers map[string]string `json:"headers,omitempty"`
	// v0.10.1451 (karar 8) — "Yerel (self-hosted) model": kod/wiki içeriği yalnız buna.
	LocalOnly bool `json:"localOnly,omitempty"`
	// v0.10.1456 (C1) — openai-uyumlu araç tanımlarında strict:true (guided decoding).
	StrictToolArgs bool `json:"strictToolArgs,omitempty"`
}

func aiProfileViews(profiles []copilot.ModelProfile, defaultID string) []aiProfileView {
	out := make([]aiProfileView, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, aiProfileView{
			ID: p.ID, Label: p.Label, Provider: p.Provider, BaseURL: p.BaseURL, Model: p.Model, SkipTLS: p.SkipTLS,
			HasKey: p.APIKey != "", MaxTokens: p.MaxTokens, Temperature: p.Temperature, TimeoutS: p.TimeoutS,
			Thinking: p.Thinking, ThinkingSwitch: p.ThinkingSwitch, Default: p.ID == defaultID,
			Toolset: p.Toolset, EffectiveToolset: modelcaps.ResolveToolset(p.Toolset, p.Model), // v0.10.1302
			Description: p.Description, Roles: p.Roles, Deep: p.Deep, Headers: p.Headers,
			LocalOnly:      p.LocalOnly,      // v0.10.1451
			StrictToolArgs: p.StrictToolArgs, // v0.10.1456 (C1)
		})
	}
	return out
}

// aiProfilesPayload — GET /api/settings/ai'ın profil bölümü ve yazım uçlarının
// cevabı; tek RLock'lık snapshot (yırtık okuma yok, #15).
func (s *Server) aiProfilesPayload() map[string]any {
	profiles, def, surface := s.copilot.ProfilesSnapshot()
	return map[string]any{
		"profiles":       aiProfileViews(profiles, def),
		"defaultProfile": def,
		"surfaceMap":     copilot.GroupsFromSurfaceMap(surface),
	}
}

// profileErr — ErrProfileNotFound → 404, diğer → 400 (#12).
func profileErr(w http.ResponseWriter, err error) {
	if errors.Is(err, copilot.ErrProfileNotFound) {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSONError(w, http.StatusBadRequest, err.Error())
}

// resolveLegacyAPIKey — eski düz form: boş anahtar = MEVCUT korunur (kutunun
// ipucu bunu söylüyordu ama sunucu "" yazıyordu — v0.8.13'ten beri çelişki,
// dilim B ile kolay ulaşılır oldu, #5); açık temizleme clearKey ile.
func resolveLegacyAPIKey(in, current string, clear bool) string {
	if clear {
		return ""
	}
	if in == "" {
		return current
	}
	return in
}

// aiProfileInput — PUT /api/settings/ai/profiles/{id} gövdesi.
type aiProfileInput struct {
	Label       string   `json:"label"`
	Provider    string   `json:"provider"`
	BaseURL     string   `json:"baseUrl"`
	APIKey      string   `json:"apiKey"`
	Model       string   `json:"model"`
	SkipTLS     bool     `json:"skipTls"`
	MaxTokens   int      `json:"maxTokens"`
	Temperature *float64 `json:"temperature"`
	TimeoutS    int      `json:"timeoutS"`
	Thinking    string   `json:"thinking"` // v0.10.534
	// v0.10.2458 — nil = MEVCUT değer korunur (StrictToolArgs emsali).
	ThinkingSwitch *bool    `json:"thinkingSwitch"`
	Toolset        string   `json:"toolset"`     // v0.10.1302 — "" otomatik | simple | full
	Description    string   `json:"description"` // v0.10.1138 — seçicideki kısa açıklama
	Roles          []string `json:"roles"`       // v0.10.1138 — boş = herkes
	Deep           bool     `json:"deep"`        // v0.10.1150 — "Derin düşün" profili (tek)
	// v0.10.1238 — ek HTTP başlıkları; tam liste (gönderilmeyen = silinir).
	Headers map[string]string `json:"headers"`
	// v0.10.1451 — admin beyanı (yalnız openai-uyumlu + özel host; ValidateProfile).
	LocalOnly bool `json:"localOnly"`
	// v0.10.1456 (C1) — nil = MEVCUT değer korunur (UI'sı sürüm 6'da; bugünkü
	// form alanı göndermez ve kaydetme anahtarı sessizce kapatmamalı).
	StrictToolArgs *bool `json:"strictToolArgs"`
}

// aiProfileFromInput — SAF: gövdeden profil (kırpma + normalizasyon;
// doğrulama ValidateProfile'da).
func aiProfileFromInput(id string, in aiProfileInput) copilot.ModelProfile {
	return copilot.ModelProfile{
		ID: id, Label: strings.TrimSpace(in.Label), Provider: strings.TrimSpace(in.Provider), BaseURL: strings.TrimSpace(in.BaseURL),
		APIKey: in.APIKey, Model: strings.TrimSpace(in.Model), SkipTLS: in.SkipTLS,
		MaxTokens: in.MaxTokens, Temperature: in.Temperature, TimeoutS: in.TimeoutS,
		Thinking:    strings.TrimSpace(in.Thinking),
		Toolset:     strings.TrimSpace(in.Toolset), // v0.10.1302
		Description: strings.TrimSpace(in.Description),
		Roles:       normalizeProfileRoles(in.Roles),
		Deep:        in.Deep,
		Headers:     normalizeProfileHeaders(in.Headers),
		LocalOnly:   in.LocalOnly, // v0.10.1451
	}
}

// resolveStrictToolArgs — v0.10.1456 (C1), SAF: gövdede yoksa saklanan değer.
func resolveStrictToolArgs(in *bool, id string, existing []copilot.ModelProfile) bool {
	if in != nil {
		return *in
	}
	for _, p := range existing {
		if p.ID == id {
			return p.StrictToolArgs
		}
	}
	return false
}

func (s *Server) putAIProfile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var in aiProfileInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	p := aiProfileFromInput(id, in)
	existing, _, _ := s.copilot.ProfilesSnapshot()
	p.StrictToolArgs = resolveStrictToolArgs(in.StrictToolArgs, id, existing)
	p.ThinkingSwitch = resolveThinkingSwitch(in.ThinkingSwitch, id, existing) // v0.10.2458
	if err := copilot.ValidateProfile(p); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.copilot.UpsertProfile(r.Context(), s.store, p); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.publishConfigReload(r.Context(), "ai")
	s.audit(r, "settings.ai.profile.upsert", "settings", id, aiProfileUpsertAudit(p, in.APIKey != ""))
	writeJSON(w, s.aiProfilesPayload())
}

// normalizeProfileRoles — v0.10.1138, SAF: kırp + küçük harf + boşları at;
// sıra korunur (geçersiz/tekrarı ValidateProfile reddeder). Üç rolün üçü de
// seçiliyse liste boşalır — "herkes" tek biçimde saklanır.
func normalizeProfileRoles(in []string) []string {
	var out []string
	set := map[string]bool{}
	for _, r := range in {
		if r = strings.ToLower(strings.TrimSpace(r)); r != "" {
			out = append(out, r)
			set[r] = true
		}
	}
	if len(out) == 3 && set[auth.RoleAdmin] && set[auth.RoleEditor] && set[auth.RoleViewer] {
		return nil
	}
	return out
}

// normalizeProfileHeaders — v0.10.1238, SAF: ad ve değer kırpılır, adı boş
// satır atılır; boş sonuç nil (blob'da alan hiç yazılmaz).
func normalizeProfileHeaders(in map[string]string) map[string]string {
	var out map[string]string
	for k, v := range in {
		if k = strings.TrimSpace(k); k == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

// profileHeadersAudit — v0.10.1238, SAF: audit satırına yalnız başlık ADLARI
// (sıralı); değerler geçit jetonu olabilir. Başlıksız satır bayt bayt eski.
func profileHeadersAudit(h map[string]string) string {
	if len(h) == 0 {
		return ""
	}
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	return " headers=" + strings.Join(names, ",")
}

// aiProfileUpsertAudit — SAF: profil upsert audit ayrıntısı. v0.10.1302 —
// açık araç seti " toolset=simple|full" eki (otomatikte satır bayt bayt eski).
func aiProfileUpsertAudit(p copilot.ModelProfile, hasKey bool) string {
	return fmt.Sprintf("provider=%s model=%s baseUrl=%s hasKey=%v roles=%s", p.Provider, p.Model, p.BaseURL, hasKey, profileRolesAudit(p.Roles)) +
		profileDeepAudit(p.Deep) + profileToolsetAudit(p.Toolset) + profileHeadersAudit(p.Headers) + profileLocalOnlyAudit(p.LocalOnly) + profileStrictAudit(p.StrictToolArgs) +
		profileThinkingSwitchAudit(p.ThinkingSwitch)
}

// profileThinkingSwitchAudit — v0.10.2458, SAF: yalnız işaretliyse (işaretsiz satır bayt bayt eski).
func profileThinkingSwitchAudit(on bool) string {
	if !on {
		return ""
	}
	return " thinkingSwitch=true"
}

// resolveThinkingSwitch — v0.10.2458, SAF: gövdede yoksa saklanan değer.
func resolveThinkingSwitch(in *bool, id string, existing []copilot.ModelProfile) bool {
	if in != nil {
		return *in
	}
	for _, p := range existing {
		if p.ID == id {
			return p.ThinkingSwitch
		}
	}
	return false
}

// profileStrictAudit — v0.10.1456 (C1), SAF: yalnız açıksa; kapalı satır bayt bayt eski.
func profileStrictAudit(strict bool) string {
	if !strict {
		return ""
	}
	return " strictToolArgs=true"
}

// profileLocalOnlyAudit — v0.10.1451, SAF: yerel model beyanı audit satırına
// (yalnız işaretliyse; işaretsiz satır bayt bayt eski).
func profileLocalOnlyAudit(local bool) string {
	if !local {
		return ""
	}
	return " localOnly=true"
}

// profileToolsetAudit — v0.10.1302, SAF: açık araç seti ayarı; otomatik "".
func profileToolsetAudit(toolset string) string {
	if toolset == "" {
		return ""
	}
	return " toolset=" + toolset
}

// profileDeepAudit — v0.10.1150, SAF: derin profil işareti audit satırına
// (yalnız işaretliyse; işaretsiz satır bayt bayt eski).
func profileDeepAudit(deep bool) string {
	if !deep {
		return ""
	}
	return " deep=true"
}

// profileRolesAudit — audit satırı için: boş liste "all".
func profileRolesAudit(roles []string) string {
	if len(roles) == 0 {
		return "all"
	}
	return strings.Join(roles, ",")
}

func (s *Server) deleteAIProfile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := s.copilot.DeleteProfile(r.Context(), s.store, id); err != nil {
		profileErr(w, err)
		return
	}
	s.publishConfigReload(r.Context(), "ai")
	s.audit(r, "settings.ai.profile.delete", "settings", id, "")
	writeJSON(w, s.aiProfilesPayload())
}

func (s *Server) defaultAIProfile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := s.copilot.SetDefaultProfile(r.Context(), s.store, id); err != nil {
		profileErr(w, err)
		return
	}
	s.publishConfigReload(r.Context(), "ai")
	s.audit(r, "settings.ai.profile.default", "settings", id, "")
	writeJSON(w, s.aiProfilesPayload())
}

func (s *Server) putAISurfaceMap(w http.ResponseWriter, r *http.Request) {
	var groups map[string]string
	if err := json.NewDecoder(r.Body).Decode(&groups); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	// Kısmi gövde: verilmeyen grup MEVCUT değerini korur (#7).
	_, _, cur := s.copilot.ProfilesSnapshot()
	merged := copilot.GroupsFromSurfaceMap(cur)
	for k, v := range groups {
		merged[k] = v
	}
	m, err := copilot.SurfaceMapFromGroups(merged)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.copilot.SetSurfaceProfiles(r.Context(), s.store, m); err != nil {
		profileErr(w, err)
		return
	}
	s.publishConfigReload(r.Context(), "ai")
	b, _ := json.Marshal(groups)
	s.audit(r, "settings.ai.surface_map", "settings", "ai", string(b))
	writeJSON(w, s.aiProfilesPayload())
}

// testAIProfile — profil endpoint'ine kısa bir "ping": süre, model, hata.
// ProbeProfile: ana anahtar kapalıyken/varsayılan anahtarsızken de dener
// (#1); ai_calls satırı surface=settings-probe (ctx meta korunur, #2).
// copilotExplain sarmalayıcısı bilinçli KULLANILMIYOR: yüzeyi yoldan
// "other"a çevirip ana anahtara takılıyordu; atıf (kimlik + yüzey) burada.
func (s *Server) testAIProfile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	c := auth.FromContext(r.Context())
	meta := copilot.CallMeta{Surface: "settings-probe"}
	if c != nil {
		meta.UserID, meta.UserEmail = c.UserID, c.Email
	}
	timeout := s.copilot.ProfileTimeout(id)
	if timeout > 20*time.Second {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(copilot.WithMeta(r.Context(), meta), timeout)
	defer cancel()
	t0 := time.Now()
	out, err := s.copilot.ProbeProfile(ctx, id, "Sen bir bağlantı testisin. Yalnız 'pong' yaz.", "ping")
	if errors.Is(err, copilot.ErrProfileNotFound) {
		profileErr(w, err)
		return
	}
	res := map[string]any{"ok": err == nil, "ms": time.Since(t0).Milliseconds(), "profile": id}
	if err != nil {
		res["error"] = err.Error()
	} else {
		if r := []rune(strings.TrimSpace(out)); len(r) > 80 {
			out = string(r[:80]) + "…"
		}
		res["sample"] = strings.TrimSpace(out)
	}
	s.audit(r, "settings.ai.profile.test", "settings", id, fmt.Sprintf("ok=%v", err == nil))
	writeJSON(w, res)
}
