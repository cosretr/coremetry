package api

// cosre_conversation_settings.go — v0.10.2324 (conversation-audit §7 karar 5):
// CoSRE konuşma kalitesi blobunun operatör yüzeyi.
//
//   GET /api/ai/cosre-conversation → {state, style, verification, bannedPhrases?, interaction, updatedBy, updatedAt}  her rol (viewer GÖRÜR)
//   PUT /api/ai/cosre-conversation {state?, style?, verification?, bannedPhrases?, interaction?}                       admin · audit settings.cosre_conversation.update
//
// Depo system_settings `cosre_conversation` (chstore/cosre_conversation_settings.go;
// boot + 30 s cfgRefresh). Bağlı olanlar: `state` (A dilimi, chat_state.go) ve
// v0.10.2327'den beri `verification` off | shadow (E1, answer_verify_turn.go);
// v0.10.2339'dan beri `enforce` de (E2, answer_seam.go — varsayılan yine shadow;
// operatör bir haftalık gölge verisinden sonra açar, karar 7). ai_general_questions.go ile aynı duruş:
// requireCopilot'suz kurulum ayarı, GET atomic okuma.
//
// v0.10.2331 — `style` artık bağlı (B dilimi, chat_answer_class.go; varsayılan kapalı).
// v0.10.2338 — `bannedPhrases` (D, answer_seam.go): varsayılan kalıp listesini
// genişleten ≤40 ifade, her biri ≤80 bayt; tam liste yazılır (boş liste = ek yok).
// v0.10.2353 — `interaction` off | on (I5 onaylı aksiyon kartı, cosre_actions.go; varsayılan off).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("cosre-conversation", (*Server).registerCosreConversationRoutes)
}

func (s *Server) registerCosreConversationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/cosre-conversation", s.getCosreConversation)
	mux.Handle("PUT /api/ai/cosre-conversation", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putCosreConversation)))
}

// cosreConversationStore — depo dikişi (test sahte depo verir).
type cosreConversationStore interface {
	SaveCosreConversation(ctx context.Context, c chstore.CosreConversation) (chstore.CosreConversation, error)
}

var cosreConversationStoreOf = func(s *Server) cosreConversationStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

func (s *Server) getCosreConversation(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, chstore.CurrentCosreConversation())
}

// cosreConversationPut — PUT gövdesi; nil alan = saklı değer korunur.
type cosreConversationPut struct {
	State         *bool     `json:"state"`
	Style         *bool     `json:"style"`
	Verification  *string   `json:"verification"`
	BannedPhrases *[]string `json:"bannedPhrases"` // v0.10.2338 — D
	Interaction   *string   `json:"interaction"`   // v0.10.2353 — I5
}

// mergeCosreConversationPut — SAF: istek + mevcut → yeni blob; ayrılmış
// alanlar bu sürümde yalnız kapalı değerlerini alabilir.
func mergeCosreConversationPut(cur chstore.CosreConversation, in cosreConversationPut) (chstore.CosreConversation, error) {
	if in.State == nil && in.Style == nil && in.Verification == nil && in.BannedPhrases == nil && in.Interaction == nil {
		return cur, fmt.Errorf("state, style, verification, bannedPhrases ya da interaction alanı zorunlu")
	}
	out := cur
	if in.State != nil {
		out.State = *in.State
	}
	if in.Style != nil {
		out.Style = *in.Style // v0.10.2331 — B dilimi bağlı (chat_answer_class.go); varsayılan kapalı
	}
	if in.Verification != nil {
		if !chstore.ValidCosreVerification(*in.Verification) {
			return cur, fmt.Errorf("verification: off | shadow | enforce")
		}
		out.Verification = *in.Verification
	}
	if in.BannedPhrases != nil {
		bp, err := chstore.NormalizeCosreBannedPhrases(*in.BannedPhrases)
		if err != nil {
			return cur, fmt.Errorf("bannedPhrases: %v", err)
		}
		out.BannedPhrases = bp
	}
	if in.Interaction != nil {
		if !chstore.ValidCosreInteraction(*in.Interaction) {
			return cur, fmt.Errorf("interaction: off | on")
		}
		out.Interaction = *in.Interaction
	}
	return out, nil
}

func (s *Server) putCosreConversation(w http.ResponseWriter, r *http.Request) {
	st := cosreConversationStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	var in cosreConversationPut
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10) // v0.10.2338 — 40 × 80 B ifade + JSON payı
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	prev := chstore.CurrentCosreConversation()
	next, err := mergeCosreConversationPut(prev, in)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	next.UpdatedAt = time.Now().UTC()
	if c := auth.FromContext(r.Context()); c != nil {
		next.UpdatedBy = c.Email
		if next.UpdatedBy == "" {
			next.UpdatedBy = c.UserID
		}
	}
	saved, err := st.SaveCosreConversation(r.Context(), next)
	if err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetCosreConversation(saved) // sahte depo testleri için de yayınla
	s.audit(r, "settings.cosre_conversation.update", "settings", chstore.CosreConversationKey,
		fmt.Sprintf(`{"before":{"state":%v,"style":%v,"verification":%q,"bannedPhrases":%d,"interaction":%q},"after":{"state":%v,"style":%v,"verification":%q,"bannedPhrases":%d,"interaction":%q}}`,
			prev.State, prev.Style, prev.Verification, len(prev.BannedPhrases), prev.Interaction,
			saved.State, saved.Style, saved.Verification, len(saved.BannedPhrases), saved.Interaction))
	writeJSON(w, saved)
}
