package api

// cosre_conversation_settings.go — v0.10.2324 (conversation-audit §7 karar 5):
// CoSRE konuşma kalitesi blobunun operatör yüzeyi.
//
//   GET /api/ai/cosre-conversation → {state, style, verification, updatedBy, updatedAt}  her rol (viewer GÖRÜR)
//   PUT /api/ai/cosre-conversation {state?, verification?}                                admin · audit settings.cosre_conversation.update
//
// Depo system_settings `cosre_conversation` (chstore/cosre_conversation_settings.go;
// boot + 30 s cfgRefresh). Bağlı olanlar: `state` (A dilimi, chat_state.go) ve
// v0.10.2327'den beri `verification` off | shadow (E1, answer_verify_turn.go);
// `style` ayrılmış ve `enforce` E2'ye kadar reddedilir: PUT onları açmaya
// çalışırsa 400 — bağlı olmayan bir anahtarı "açtım" yanılsaması olmasın. ai_general_questions.go ile aynı duruş:
// requireCopilot'suz kurulum ayarı, GET atomic okuma.

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
	State        *bool   `json:"state"`
	Style        *bool   `json:"style"`
	Verification *string `json:"verification"`
}

// mergeCosreConversationPut — SAF: istek + mevcut → yeni blob; ayrılmış
// alanlar bu sürümde yalnız kapalı değerlerini alabilir.
func mergeCosreConversationPut(cur chstore.CosreConversation, in cosreConversationPut) (chstore.CosreConversation, error) {
	if in.State == nil && in.Style == nil && in.Verification == nil {
		return cur, fmt.Errorf("state ya da verification alanı zorunlu")
	}
	out := cur
	if in.State != nil {
		out.State = *in.State
	}
	if in.Style != nil {
		if *in.Style {
			return cur, fmt.Errorf("style henüz bağlı değil (ayrılmış alan, kapalı kalır)")
		}
		out.Style = false
	}
	if in.Verification != nil {
		if !chstore.ValidCosreVerification(*in.Verification) {
			return cur, fmt.Errorf("verification: off | shadow | enforce")
		}
		if *in.Verification == chstore.CosreVerifyEnforce {
			return cur, fmt.Errorf("verification=enforce henüz bağlı değil (E2); off | shadow")
		}
		out.Verification = *in.Verification
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
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
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
		fmt.Sprintf(`{"before":{"state":%v,"style":%v,"verification":%q},"after":{"state":%v,"style":%v,"verification":%q}}`,
			prev.State, prev.Style, prev.Verification, saved.State, saved.Style, saved.Verification))
	writeJSON(w, saved)
}
