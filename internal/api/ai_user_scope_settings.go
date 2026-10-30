package api

// ai_user_scope_settings.go — CoSRE v2 araç seti dilim 2 (docs/cosre/tools-audit.md
// §11 Q2): v2 sohbetinin varsayılan kullanıcı kapsamının (user_scope) GLOBAL
// anahtarı. system_settings `ai.userScope` JSON blobu {"enabled":bool};
// VARSAYILAN AÇIK (blob yok / bozuk / okuma hatası → açık).
//
//   GET /api/ai/user-scope → {enabled}   her rol (viewer durumu GÖRÜR)
//   PUT /api/ai/user-scope {enabled}     admin · audit settings.ai_user_scope.update
//
// Okuma yolu cosre_v2_flag.go'nun deseni: süreç-yerel 30 s memo (her v2 sohbet
// isteği CH'ye gitmesin), PUT yerel memo'yu anında günceller; podlar arası
// ≤30 s nihai tutarlılık. Kapalıyken v2 alışverişi kapsamsız koşar ve bunu
// data-scope{unscoped:true, reason:"disabled"} ile söyler (sessiz değil).
// Alışveriş başına kaçış (@-kapsam / /-komut) ayrı: chat_user_scope.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("ai-user-scope", (*Server).registerAIUserScopeRoutes) }

func (s *Server) registerAIUserScopeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/user-scope", s.getAIUserScope)
	mux.Handle("PUT /api/ai/user-scope", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putAIUserScope)))
}

const (
	aiUserScopeSettingKey = "ai.userScope"
	aiUserScopeMemoTTL    = 30 * time.Second
	aiUserScopeMaxBody    = 4 << 10
)

type aiUserScopeSetting struct {
	Enabled bool `json:"enabled"`
}

// parseAIUserScope — SAF: boş / bozuk / alanı olmayan blob AÇIK (varsayılan).
func parseAIUserScope(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	var f struct {
		Enabled *bool `json:"enabled"`
	}
	if json.Unmarshal(raw, &f) != nil || f.Enabled == nil {
		return true
	}
	return *f.Enabled
}

// aiUserScopeMemo — süreç-yerel TTL memo; load/now testte enjekte edilir.
type aiUserScopeMemo struct {
	mu    sync.Mutex
	val   bool
	at    time.Time
	valid bool
	load  func(ctx context.Context) (bool, error)
	now   func() time.Time
}

func (m *aiUserScopeMemo) get(ctx context.Context) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if m.valid && now.Sub(m.at) < aiUserScopeMemoTTL {
		return m.val
	}
	v, err := m.load(ctx)
	if err != nil {
		v = true // okuma hatası varsayılana (açık) düşer
	}
	m.val, m.at, m.valid = v, now, true
	return v
}

func (m *aiUserScopeMemo) set(on bool) {
	m.mu.Lock()
	m.val, m.at, m.valid = on, m.now(), true
	m.mu.Unlock()
}

var aiUserScopeMemos sync.Map // *Server → *aiUserScopeMemo (api.go büyümez)

func (s *Server) aiUserScopeMemo() *aiUserScopeMemo {
	if m, ok := aiUserScopeMemos.Load(s); ok {
		return m.(*aiUserScopeMemo)
	}
	m, _ := aiUserScopeMemos.LoadOrStore(s, &aiUserScopeMemo{load: s.loadAIUserScope, now: time.Now})
	return m.(*aiUserScopeMemo)
}

func (s *Server) loadAIUserScope(ctx context.Context) (bool, error) {
	if s.store == nil {
		return true, nil
	}
	raw, err := s.store.GetSetting(ctx, aiUserScopeSettingKey)
	if err != nil {
		return true, err
	}
	return parseAIUserScope(raw), nil
}

// aiUserScopeEnabled — v2 kullanıcı kapsamı açık mı (memo'lu; varsayılan açık).
func (s *Server) aiUserScopeEnabled(ctx context.Context) bool {
	return s.aiUserScopeMemo().get(ctx)
}

func (s *Server) getAIUserScope(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, aiUserScopeSetting{Enabled: s.aiUserScopeEnabled(r.Context())})
}

func (s *Server) putAIUserScope(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, aiUserScopeMaxBody)
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if in.Enabled == nil {
		writeJSONError(w, http.StatusBadRequest, "enabled gerekli")
		return
	}
	if s.store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	prev := s.aiUserScopeEnabled(r.Context())
	raw, _ := json.Marshal(aiUserScopeSetting{Enabled: *in.Enabled})
	if err := s.store.PutSetting(r.Context(), aiUserScopeSettingKey, raw); err != nil {
		writeErr(w, err)
		return
	}
	s.aiUserScopeMemo().set(*in.Enabled)
	details, _ := json.Marshal(map[string]any{"before": aiUserScopeSetting{Enabled: prev}, "after": aiUserScopeSetting{Enabled: *in.Enabled}})
	s.audit(r, "settings.ai_user_scope.update", "settings", aiUserScopeSettingKey, string(details))
	writeJSON(w, aiUserScopeSetting{Enabled: *in.Enabled})
}
