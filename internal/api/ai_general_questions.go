package api

// ai_general_questions.go — v0.10.1458 (operatör kararı 2026-10-23: "Genel
// soruları açık bırakalım, anahtar da olabilir."): konu dışı (off_topic)
// sorulara model cevabı anahtarının operatör yüzeyi.
//
//   GET /api/ai/general-questions  → {allow, updatedBy, updatedAt}   her rol (viewer GÖRÜR)
//   PUT /api/ai/general-questions  {allow}                           admin · audit settings.ai_general_questions.update
//
// Depo system_settings `ai.generalQuestions` (chstore/ai_general_questions.go;
// boot + 30 s cfgRefresh). Varsayılan allow=TRUE (blob yok) — v0.10.819'un
// deterministik sınırını tersine çeviren karar; false = v0.10.819 bayt bayt.
// Cevap yolu chat_offtopic_general.go.
//
// Neden requireCopilot'suz, ai_toolarg_settings.go ile aynı duruş: kurulum
// ayarı, AI sağlayıcısından bağımsız görünür (CoSRE kapalıyken de admin
// anahtarı önceden ayarlayabilir). GET serveCached'siz: atomic okuma, sorgu
// yok. PUT'ta "allow" ZORUNLU — boş gövde "kapattım" ya da "açtım" yanılsaması
// üretmesin (400).

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
	registerRoutesExtra("ai-general-questions", (*Server).registerAIGeneralQuestionsRoutes)
}

func (s *Server) registerAIGeneralQuestionsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/general-questions", s.getAIGeneralQuestions)
	mux.Handle("PUT /api/ai/general-questions", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putAIGeneralQuestions)))
}

// aiGeneralQuestionsStore — depo dikişi (test sahte depo verir).
type aiGeneralQuestionsStore interface {
	SaveAIGeneralQuestions(ctx context.Context, a chstore.AIGeneralQuestions) (chstore.AIGeneralQuestions, error)
}

var aiGeneralQuestionsStoreOf = func(s *Server) aiGeneralQuestionsStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

func (s *Server) getAIGeneralQuestions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, chstore.CurrentAIGeneralQuestions())
}

func (s *Server) putAIGeneralQuestions(w http.ResponseWriter, r *http.Request) {
	st := aiGeneralQuestionsStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	var in struct {
		Allow           *bool `json:"allow"`
		SourceDiscovery *bool `json:"sourceDiscovery"` // v0.10.1553
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if in.Allow == nil && in.SourceDiscovery == nil {
		writeJSONError(w, http.StatusBadRequest, "allow ya da sourceDiscovery alanı zorunlu (true | false)")
		return
	}
	prev := chstore.CurrentAIGeneralQuestions()
	// v0.10.1553 — gelmeyen alan saklı değeri korur.
	next := chstore.AIGeneralQuestions{Allow: prev.Allow, SourceDiscovery: prev.SourceDiscovery, UpdatedAt: time.Now().UTC()}
	if in.Allow != nil {
		next.Allow = *in.Allow
	}
	if in.SourceDiscovery != nil {
		v := *in.SourceDiscovery
		next.SourceDiscovery = &v
	}
	if c := auth.FromContext(r.Context()); c != nil {
		next.UpdatedBy = c.Email
		if next.UpdatedBy == "" {
			next.UpdatedBy = c.UserID
		}
	}
	saved, err := st.SaveAIGeneralQuestions(r.Context(), next)
	if err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetAIGeneralQuestions(saved) // sahte depo testleri için de yayınla
	s.audit(r, "settings.ai_general_questions.update", "settings", chstore.AIGeneralQuestionsKey,
		fmt.Sprintf(`{"before":{"allow":%v,"sourceDiscovery":%v},"after":{"allow":%v,"sourceDiscovery":%v}}`,
			prev.Allow, prev.SourceDiscoveryOn(), saved.Allow, saved.SourceDiscoveryOn()))
	writeJSON(w, saved)
}
