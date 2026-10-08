package api

// wiki.go — v0.10.1122 ("karma"): Azure DevOps wiki bilgisi — Ayarlar uçları,
// lider-kapılı senkron döngüsü ve wiki servisinin kablosu. Registrar deseni
// (route_registry.go): api.go büyümez; servis paket düzeyinde tutulur
// (SetArgoCDSettings emsali), Server struct'ına alan eklenmez.
//
// Uçlar:
//
//	GET  /api/wiki/config — ayar + DevOps bağlı mı + durum (her oturum; sır yok)
//	PUT  /api/wiki/config — admin, audit settings.wiki.update
//	GET  /api/wiki/status — durum kartı (her oturum)
//	POST /api/wiki/sync   — admin, audit wiki.sync; LİDER pod'un döngüsü ≤15 sn
//	                        içinde koşar (istek pod'u senkronu kendisi yapmaz —
//	                        tek-lider sözleşmesi, iki pod aynı anda yazmaz)
//
// Kimlik bilgisi YOK: PAT Ayarlar → Kod entegrasyonu'ndaki devops_connection
// blobunda kalır ve hiçbir yanıtta görünmez.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/rag"
	"github.com/cilcenk/coremetry/internal/wiki"
)

var wikiSvcPtr atomic.Pointer[wiki.Service]

// SetWiki — main.go boot'ta bir kez (nil = özellik yok).
func SetWiki(w *wiki.Service) { wikiSvcPtr.Store(w) }

// wikiKB — canlı wiki servisi (nil olabilir).
func wikiKB() *wiki.Service { return wikiSvcPtr.Load() }

// NewWikiService — main.go'nun kurucusu: depo CH, bağlantı DevOps servisi,
// embedding (varsa) RAG servisi. Sağlayıcılar her çağrıda canlı durumu okur.
func NewWikiService(store *chstore.Store, dv *devops.Service, rg *rag.Service) *wiki.Service {
	return wiki.New(store,
		func() wiki.API {
			if dv == nil {
				return nil
			}
			return dv
		},
		func() wiki.Embedder {
			if rg == nil || !rg.Ready() {
				return nil
			}
			return rg.Embed
		})
}

func init() { registerRoutesExtra("wiki", (*Server).registerWikiRoutes) }

func (s *Server) registerWikiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/wiki/config", s.getWikiConfig)
	// Yazma uçları YALNIZ oturum açmış yöneticiye — admin rollü API token'ı
	// değil (oidcSessionOnly deseni, wikiSessionOnly).
	mux.HandleFunc("PUT /api/wiki/config", auth.RequireRole(auth.RoleAdmin, wikiSessionOnly(s.putWikiConfig)))
	mux.HandleFunc("GET /api/wiki/status", s.getWikiStatus)
	mux.HandleFunc("POST /api/wiki/sync", auth.RequireRole(auth.RoleAdmin, wikiSessionOnly(s.postWikiSync)))
}

// wikiSessionOnly — API token principal'ını reddeder (rol kapısının İÇİNDE).
func wikiSessionOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sourceCodeCallerAllowed(auth.FromContext(r.Context())) {
			writeJSONError(w, http.StatusForbidden, "wiki ayarı yalnız oturum açmış yönetici tarafından değiştirilebilir (API token kabul edilmez)")
			return
		}
		h(w, r)
	}
}

// wikiStatusView — durum + türetilmiş "koşuyor" bayrağı (lider başka pod'da olabilir).
type wikiStatusView struct {
	wiki.Status
	Running bool `json:"running"`
}

func wikiStatusOf(st wiki.Status) wikiStatusView {
	return wikiStatusView{Status: st, Running: st.LastStartedAt > st.LastFinishedAt}
}

func (s *Server) wikiConfigView(ctx context.Context) map[string]any {
	w := wikiKB()
	if w == nil {
		return map[string]any{"available": false}
	}
	cfg := w.Config()
	st := w.Status(ctx, false)
	return map[string]any{
		"available":        true,
		"config":           cfg,
		"mode":             cfg.EffectiveMode(),
		"modeWarning":      wikiModeWarning(cfg.EffectiveMode(), st.Search),
		"devopsConfigured": s.devops != nil && s.devops.Configured(),
		"embedding":        s.rag != nil && s.rag.Ready(),
		"status":           wikiStatusOf(st),
		"defaults": map[string]int{
			"intervalMin": wiki.DefaultIntervalMin, "minIntervalMin": wiki.MinIntervalMin,
			"maxPages": wiki.DefaultMaxPages,
		},
	}
}

// wikiModeWarning — SAF (v0.10.1124): canlı mod Search uzantısı ister; uç
// yoksa ya da henüz doğrulanmadıysa kayıtta ve kartta uyarı.
func wikiModeWarning(mode, search string) string {
	if mode != wiki.ModeLive {
		return ""
	}
	switch search {
	case wiki.SearchUnavailable:
		return wiki.NoteSearchUnavailableLive + " — canlı mod bu sunucuda çalışmaz."
	case wiki.SearchAvailable:
		return ""
	}
	return "Azure DevOps Search henüz doğrulanmadı — canlı mod Search uzantısı ister; \"Aramayı test et\" ile doğrulayın."
}

func (s *Server) getWikiConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.wikiConfigView(r.Context()))
}

func (s *Server) putWikiConfig(w http.ResponseWriter, r *http.Request) {
	ws := wikiKB()
	if ws == nil || s.store == nil {
		http.Error(w, "wiki bilgisi bu kurulumda yok", http.StatusServiceUnavailable)
		return
	}
	var body wiki.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := ws.SavePersisted(r.Context(), s.store, body); err != nil {
		writeErr(w, err)
		return
	}
	s.publishConfigReload(r.Context(), "wiki")
	cfg := ws.Config()
	details, _ := json.Marshal(map[string]any{
		"enabled": cfg.Enabled, "projects": len(cfg.Projects), "wikis": len(cfg.Wikis),
		"intervalMin": cfg.IntervalMin, "maxPages": cfg.MaxPages, "disableLiveSearch": cfg.DisableLiveSearch,
		"mode": cfg.EffectiveMode(),
	})
	s.audit(r, "settings.wiki.update", "settings", wiki.SettingsKey, string(details))
	writeJSON(w, s.wikiConfigView(r.Context()))
}

func (s *Server) getWikiStatus(w http.ResponseWriter, r *http.Request) {
	ws := wikiKB()
	if ws == nil {
		writeJSON(w, wikiStatusView{})
		return
	}
	writeJSON(w, wikiStatusOf(ws.Status(r.Context(), true)))
}

func (s *Server) postWikiSync(w http.ResponseWriter, r *http.Request) {
	ws := wikiKB()
	if ws == nil {
		http.Error(w, "wiki bilgisi bu kurulumda yok", http.StatusServiceUnavailable)
		return
	}
	if !ws.Config().Enabled {
		http.Error(w, "wiki bilgisi kapalı — önce etkinleştirin", http.StatusConflict)
		return
	}
	if s.devops == nil || !s.devops.Configured() {
		http.Error(w, "Azure DevOps bağlantısı yapılandırılmamış (Ayarlar → Kod entegrasyonu)", http.StatusConflict)
		return
	}
	if ws.Config().EffectiveMode() == wiki.ModeLive {
		http.Error(w, "canlı modda senkron yok — mod Karma ya da Yalnız senkron olmalı", http.StatusConflict)
		return
	}
	by := ""
	if c := auth.FromContext(r.Context()); c != nil {
		by = c.Email
	}
	st := ws.RequestSync(r.Context(), by)
	s.audit(r, "wiki.sync", "wiki", "manual", "{}")
	writeJSON(w, map[string]any{"queued": true, "status": wikiStatusOf(st)})
}

// wikiLeaderKey — senkronun Redis lider kilidi.
const wikiLeaderKey = "coremetry:lock:wiki-sync"

// StartWikiSync — lider-kapılı senkron döngüsü (her rolde başlatılır; yalnız
// kilidi tutan pod koşar). Her lider tikinde ayar CH'den yeniden okunur ki
// başka pod'a düşen PUT 15 sn içinde görülsün.
func (s *Server) StartWikiSync(ctx context.Context, lock cache.Lock) {
	ws := wikiKB()
	if ws == nil || lock == nil {
		return
	}
	leader := cache.NewLeaderHolder(lock, wikiLeaderKey, cache.LeaderTTL(time.Minute))
	leader.Start(ctx)
	ws.RunLoop(ctx, leader.IsLeader, func(c context.Context) {
		if s.store != nil {
			_ = ws.LoadPersisted(c, s.store)
		}
	})
}
