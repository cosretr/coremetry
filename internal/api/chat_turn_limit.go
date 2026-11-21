package api

// chat_turn_limit.go — CoSRE kişi başı sohbet turu sınırı (paylaşılan yerel
// LLM'i korur; operatör onayı 2026-11-21).
//
//   GET /api/ai/turn-limits  → ayar (admin)
//   PUT /api/ai/turn-limits {perHour?, perDay?, disabled?, exemptAdmins?}
//                             → admin · audit settings.ai_turn_limits.update
//
// Sayılan: etkileşimli sohbet turu (v1 /api/copilot/chat + v2). Sayılmayan:
// değerlendirme koşuları (chatEvalMode), onay devamı (aynı tur), arka plan
// SRE koşuları (kendi bütçeleri: sre_agent.maxLLMCallsPerHour).
//
// Sayaç Redis'te, filo geneli (rate_shared.go kalıbı, sabit pencere, tek
// INCR): coremetry:rate:v1:cosre-turn-h:<kullanıcı özeti>:<saat başı> ve
// coremetry:rate:v1:cosre-turn-d:<kullanıcı özeti>:<gün başı (UTC)>.
// Saat aşılırsa gün sayacına dokunulmaz.
//
// R0 (bozulmuş Redis): sayaç yok / devre açık / hata → ENGELLEME YOK, tur
// geçer (öz-gözlem result=degraded). Pod-yerel yedek sayaç tutulmaz: sınır
// koruma amaçlı; Redis arızasında operatörü kilitlemek daha kötüdür.
//
// Sınırda: model çağrısı YOK; sohbet açık bir cevap verir (hangi sınır, ne
// zaman sıfırlanır) ve tur biter. Öz-gözlem cosre.chat.turn_limit.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/selfobs"
)

func (s *Server) registerAITurnLimitRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/turn-limits", auth.RequireRole(auth.RoleAdmin, s.getAITurnLimits))
	mux.HandleFunc("PUT /api/ai/turn-limits", auth.RequireRole(auth.RoleAdmin, s.putAITurnLimits))
}

// turnLimitVerdict — karar.
type turnLimitVerdict struct {
	Limited bool
	Window  string // hour | day
	Limit   int
	Wait    time.Duration
}

// turnCountFn — sayaç dikişi (sharedRateAllow imzası): (izin, bekleme, ok).
type turnCountFn func(ctx context.Context, scope, id string, limit int, window time.Duration, now time.Time) (bool, time.Duration, bool)

// decideTurnLimit — kullanıcının turunu sayar ve karar verir. Boş kullanıcı,
// kapalı ayar ya da muaf admin → sayılmaz. Sayaç yok → geçer (R0).
func decideTurnLimit(ctx context.Context, count turnCountFn, cfg chstore.AITurnLimits, c *auth.Claims, now time.Time) turnLimitVerdict {
	if cfg.Disabled || c == nil {
		return turnLimitVerdict{}
	}
	user := firstNonEmpty(c.UserID, c.Email)
	if user == "" || (cfg.ExemptAdmins && c.Role == auth.RoleAdmin) {
		return turnLimitVerdict{}
	}
	for _, w := range []struct {
		scope, name string
		limit       int
		win         time.Duration
	}{
		{"cosre-turn-h", "hour", cfg.PerHour, time.Hour},
		{"cosre-turn-d", "day", cfg.PerDay, 24 * time.Hour},
	} {
		ok, wait, shared := count(ctx, w.scope, user, w.limit, w.win, now)
		if !shared {
			selfobs.CountCoSRETurnLimit("degraded")
			return turnLimitVerdict{}
		}
		if !ok {
			selfobs.CountCoSRETurnLimit("limited_" + w.name)
			return turnLimitVerdict{Limited: true, Window: w.name, Limit: w.limit, Wait: wait}
		}
	}
	return turnLimitVerdict{}
}

// turnLimitText — SAF: sınır cevabı (model çağrısı yok).
func turnLimitText(v turnLimitVerdict) string {
	win := "saatlik"
	if v.Window == "day" {
		win = "24 saatlik"
	}
	return fmt.Sprintf("CoSRE %s soru sınırına ulaştınız (%d tur). Paylaşılan modeli korumak için bu tur yanıtlanmadı; sınır %s sonra sıfırlanır. Acil bir durumda bir yöneticiye başvurun.",
		win, v.Limit, waitTR(v.Wait))
}

// waitTR — SAF: "45 dk" / "3 sa 10 dk" / "1 dk" (yukarı yuvarlı dakika).
func waitTR(d time.Duration) string {
	m := int((d + time.Minute - 1) / time.Minute)
	if m < 1 {
		m = 1
	}
	if m < 60 {
		return fmt.Sprintf("%d dk", m)
	}
	if m%60 == 0 {
		return fmt.Sprintf("%d sa", m/60)
	}
	return fmt.Sprintf("%d sa %d dk", m/60, m%60)
}

// chatTurnLimited — sohbet gövdesinin kapısı: sınırdaysa cevabı yayınlar ve
// true döner (çağıran "done" yazıp çıkar).
func (s *Server) chatTurnLimited(ctx context.Context, emit func(string, any), c *auth.Claims) bool {
	v := decideTurnLimit(ctx, s.sharedRateAllow, chstore.CurrentAITurnLimits(), c, time.Now())
	if !v.Limited {
		return false
	}
	emit("answer", map[string]any{"text": turnLimitText(v), "exchangeId": copilot.MetaFromContext(ctx).ExchangeID,
		"sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{},
		"turnLimit": map[string]any{"window": v.Window, "limit": v.Limit, "resetInSec": int64((v.Wait + time.Second - 1) / time.Second)}})
	return true
}

func (s *Server) getAITurnLimits(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, chstore.CurrentAITurnLimits())
}

// aiTurnLimitsPut — PUT gövdesi; nil alan = mevcut korunur.
type aiTurnLimitsPut struct {
	PerHour      *int  `json:"perHour"`
	PerDay       *int  `json:"perDay"`
	Disabled     *bool `json:"disabled"`
	ExemptAdmins *bool `json:"exemptAdmins"`
}

// mergeTurnLimitsPut — SAF.
func mergeTurnLimitsPut(cur chstore.AITurnLimits, in aiTurnLimitsPut) chstore.AITurnLimits {
	out := cur
	if in.PerHour != nil {
		out.PerHour = *in.PerHour
	}
	if in.PerDay != nil {
		out.PerDay = *in.PerDay
	}
	if in.Disabled != nil {
		out.Disabled = *in.Disabled
	}
	if in.ExemptAdmins != nil {
		out.ExemptAdmins = *in.ExemptAdmins
	}
	return chstore.NormalizeAITurnLimits(out)
}

// aiTurnLimitsStore — depo dikişi (test sahte depo verir).
type aiTurnLimitsStore interface {
	SaveAITurnLimits(ctx context.Context, b chstore.AITurnLimits) (chstore.AITurnLimits, error)
}

var aiTurnLimitsStoreOf = func(s *Server) aiTurnLimitsStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

func (s *Server) putAITurnLimits(w http.ResponseWriter, r *http.Request) {
	st := aiTurnLimitsStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	var in aiTurnLimitsPut
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	prev := chstore.CurrentAITurnLimits()
	next := mergeTurnLimitsPut(prev, in)
	if c := auth.FromContext(r.Context()); c != nil {
		next.UpdatedBy = firstNonEmpty(c.Email, c.UserID)
	}
	next.UpdatedAt = time.Now().UTC()
	saved, err := st.SaveAITurnLimits(r.Context(), next)
	if err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetAITurnLimits(saved)
	strip := func(b chstore.AITurnLimits) chstore.AITurnLimits {
		b.UpdatedBy, b.UpdatedAt = "", time.Time{}
		return b
	}
	details, _ := json.Marshal(map[string]any{"before": strip(prev), "after": strip(saved)})
	s.audit(r, "settings.ai_turn_limits.update", "settings", chstore.AITurnLimitsKey, string(details))
	writeJSON(w, saved)
}
