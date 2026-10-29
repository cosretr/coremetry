package api

// cosre_v2_flag.go — v0.10.1592 (CoSRE v2 Faz 1, R4): CoSRE v2 sohbetinin
// admin bayrağı (operatör kararı §0-6). system_settings("cosre_v2") JSON
// blobu {"enabled":bool}; ai_budget.go'nun GetSetting/PutSetting deseni.
//
//   GET /api/admin/cosre-v2 → {enabled}   (admin)
//   PUT /api/admin/cosre-v2 {enabled}     (admin + audit)
//
// Bayrak HEM v2 rotasını (R5: kapalıyken 404) HEM istemcinin lazy chunk'ını
// kapatır (istemci /api/copilot/config'teki cosreV2 alanına bakar). v1 ve v2
// aynı kalıcılığı kullanır; bayrağı çevirmek thread kaybettirmez.
//
// Okuma yolu süreç-yerel 30 s memo: her sohbet isteği ve config okuması CH'ye
// gitmesin. Podlar arası 30 s nihai tutarlılık bir özellik bayrağı için
// kabul; PUT yerel memo'yu anında günceller. Redis `config:` yayını (diğer
// podlara anında) bilinçli olarak YOK — açık soru, ayrı karar.
//
// Store yok / okuma hatası / anahtar yok → KAPALI (varsayılan kapalı).
// Memo Server alanı değil (api.go büyümez): *Server anahtarlı sync.Map.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("cosre-v2-flag", (*Server).registerCosreV2FlagRoutes) }

func (s *Server) registerCosreV2FlagRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/admin/cosre-v2", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getCosreV2Flag)))
	mux.Handle("PUT /api/admin/cosre-v2", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putCosreV2Flag)))
}

const (
	cosreV2SettingKey  = "cosre_v2"
	cosreV2MemoTTL     = 30 * time.Second
	cosreV2FlagMaxBody = 4 << 10
)

type cosreV2Flag struct {
	Enabled bool `json:"enabled"`
}

// parseCosreV2Flag — SAF: bozuk / boş blob kapalı.
func parseCosreV2Flag(raw []byte) bool {
	var f cosreV2Flag
	if len(raw) == 0 || json.Unmarshal(raw, &f) != nil {
		return false
	}
	return f.Enabled
}

func marshalCosreV2Flag(on bool) []byte {
	b, _ := json.Marshal(cosreV2Flag{Enabled: on})
	return b
}

// cosreV2Memo — süreç-yerel TTL memo; load/now testte enjekte edilir.
type cosreV2Memo struct {
	mu    sync.Mutex
	val   bool
	at    time.Time
	valid bool
	load  func(ctx context.Context) (bool, error)
	now   func() time.Time
}

func (m *cosreV2Memo) get(ctx context.Context) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if m.valid && now.Sub(m.at) < cosreV2MemoTTL {
		return m.val
	}
	v, err := m.load(ctx)
	if err != nil {
		v = false
	}
	m.val, m.at, m.valid = v, now, true
	return v
}

func (m *cosreV2Memo) set(on bool) {
	m.mu.Lock()
	m.val, m.at, m.valid = on, m.now(), true
	m.mu.Unlock()
}

var cosreV2Memos sync.Map // *Server → *cosreV2Memo

func (s *Server) cosreV2Memo() *cosreV2Memo {
	if m, ok := cosreV2Memos.Load(s); ok {
		return m.(*cosreV2Memo)
	}
	m, _ := cosreV2Memos.LoadOrStore(s, &cosreV2Memo{load: s.loadCosreV2Flag, now: time.Now})
	return m.(*cosreV2Memo)
}

func (s *Server) loadCosreV2Flag(ctx context.Context) (bool, error) {
	if s.store == nil {
		return false, nil
	}
	raw, err := s.store.GetSetting(ctx, cosreV2SettingKey)
	if err != nil {
		return false, err
	}
	return parseCosreV2Flag(raw), nil
}

// cosreV2Enabled — v2 sohbeti açık mı (memo'lu; varsayılan kapalı).
func (s *Server) cosreV2Enabled(ctx context.Context) bool {
	return s.cosreV2Memo().get(ctx)
}

func (s *Server) getCosreV2Flag(w http.ResponseWriter, r *http.Request) {
	on, err := s.loadCosreV2Flag(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, cosreV2Flag{Enabled: on})
}

func (s *Server) putCosreV2Flag(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, cosreV2FlagMaxBody)
	var in cosreV2Flag
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if s.store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	raw := marshalCosreV2Flag(in.Enabled)
	if err := s.store.PutSetting(r.Context(), cosreV2SettingKey, raw); err != nil {
		writeErr(w, err)
		return
	}
	s.cosreV2Memo().set(in.Enabled)
	s.audit(r, "settings.cosre_v2.update", "settings", cosreV2SettingKey, string(raw))
	writeJSON(w, cosreV2Flag{Enabled: in.Enabled})
}
