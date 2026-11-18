package api

// notify_policy.go — v0.10.1523 (Problem standardı + triage + bildirim politikası,
// dilim R1; operatör onayı 2026-10-24, docs/DECISIONS.md).
//
//	GET /api/admin/notify-policy                 admin — kayıtlı notify_policy blobu
//	PUT /api/admin/notify-policy                 admin — doğrula + kaydet + yayınla; audit notify.policy
//	GET /api/admin/notify-digest                 admin — bekleyen P3 özet kalemleri (v0.10.1533, R5)
//
// Replay ucu (GET /api/admin/notify-policy/replay) kendi dosyasında:
// notify_policy_replay.go.
//
// api.go BÜYÜMEZ (TestApiGoDoesNotGrow): rotalar bu dosyada, kayıt init()'te
// registerRoutesExtra ile.
//
// Neden admin-only: politika tüm bildirimlerin kapısı. v0.10.1533 (R5) — ayar
// ekranı Settings → Bildirim politikası (kip, P3 özeti, sessiz saat, tabanlar,
// şablon + replay özeti); enforce'a geçiş yine yöneticinin kararı (D8).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("notify-policy", (*Server).registerNotifyPolicyRoutes) }

func (s *Server) registerNotifyPolicyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/notify-policy", auth.RequireRole(auth.RoleAdmin, s.getNotifyPolicy))
	mux.HandleFunc("PUT /api/admin/notify-policy", auth.RequireRole(auth.RoleAdmin, s.putNotifyPolicy))
	mux.HandleFunc("GET /api/admin/notify-digest", auth.RequireRole(auth.RoleAdmin, s.getNotifyDigest))
}

// getNotifyDigest — bekleyen P3 özet kalemleri (önbelleksiz: kuyruk küçük,
// ekran açıkken okunur; okuma LIMIT'li).
func (s *Server) getNotifyDigest(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListDigestPending(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	last, _ := s.store.ReadDigestLast(r.Context())
	writeJSON(w, map[string]any{"items": items, "lastSlot": last})
}

// LoadNotifyPolicy — boot hidrasyonu (main.go); okuma hatasında son iyi değer.
func (s *Server) LoadNotifyPolicy(ctx context.Context) {
	if s.store == nil {
		return
	}
	chstore.LoadNotifyPolicyWith(func() (chstore.NotifyPolicy, error) {
		return s.store.ReadNotifyPolicy(ctx)
	})
}

func (s *Server) getNotifyPolicy(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.ReadNotifyPolicy(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, p)
}

// decodeNotifyPolicyPut — SAF: gövde KAYITLI değerin üstüne (eksik alan
// kayıtlı hâlinde kalır).
func decodeNotifyPolicyPut(stored chstore.NotifyPolicy, body []byte) (chstore.NotifyPolicy, error) {
	c := stored
	c.DigestTimes = append([]string{}, stored.DigestTimes...)
	if stored.QuietHours != nil {
		q := *stored.QuietHours
		c.QuietHours = &q
	}
	if err := json.Unmarshal(body, &c); err != nil {
		return chstore.NotifyPolicy{}, err
	}
	return c, nil
}

func (s *Server) putNotifyPolicy(w http.ResponseWriter, r *http.Request) {
	stored, err := s.store.ReadNotifyPolicy(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	c, err := decodeNotifyPolicyPut(stored, raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if err := chstore.ValidateNotifyPolicy(c); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cl := auth.FromContext(r.Context()); cl != nil {
		c.UpdatedBy = cl.Email
		if c.UpdatedBy == "" {
			c.UpdatedBy = cl.UserID
		}
	}
	c.UpdatedAt = time.Now().UnixNano()
	if err := s.store.SaveNotifyPolicy(r.Context(), c); err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetNotifyPolicy(c)
	saved := chstore.CurrentNotifyPolicy()
	s.audit(r, "notify.policy", "notify_policy", chstore.NotifyPolicyKey,
		fmt.Sprintf(`{"mode":%q,"p3":%q,"digestTimes":%q,"noiseDedupeHours":%g,"noiseMuteHours":%g,"template":%q}`,
			saved.Mode, saved.P3, strings.Join(saved.DigestTimes, ","), saved.NoiseDedupeHours, saved.NoiseMuteHours, saved.Template))
	log.Printf("[settings] notify_policy: mode=%s p3=%s", saved.Mode, saved.P3)
	writeJSON(w, saved)
}
