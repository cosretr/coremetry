package api

// ai_tool_approval_settings.go — agent-core S4 (docs/cosre/agent-core-ideas.md §4.3):
// CoSRE araç onayının operatör yüzeyi.
//
//   GET /api/ai/tool-approval  → {enabled, tools, timeoutS, approverMinRole, externalAll, updatedBy, updatedAt}   her rol (viewer GÖRÜR)
//   PUT /api/ai/tool-approval  {enabled?, tools?, timeoutS?, approverMinRole?, externalAll?}                      admin · audit settings.ai_tool_approval.update
//
// Depo system_settings `ai.toolApproval` (chstore/ai_tool_approval_settings.go;
// boot + 30 s cfgRefresh). Varsayılan KAPALI. S4'te onay-kapılı araç yok:
// ayar açılsa da katalog değişmez (toolsForRole Approval araçlarını sunmaz);
// S5 ilk aracı (add_problem_note) bu ayarın kapısından geçirir.
// Şablon ai_toolarg_settings.go: GET atomic okuma (sorgu yok, serveCached'siz);
// PUT'ta gönderilmeyen alan MEVCUT değeri korur.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() {
	registerRoutesExtra("ai-tool-approval", (*Server).registerAIToolApprovalRoutes)
}

func (s *Server) registerAIToolApprovalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/tool-approval", s.getAIToolApproval)
	mux.Handle("PUT /api/ai/tool-approval", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putAIToolApproval)))
}

// aiToolApprovalStore — depo dikişi (test sahte depo verir).
type aiToolApprovalStore interface {
	SaveAIToolApproval(ctx context.Context, a chstore.AIToolApproval) (chstore.AIToolApproval, error)
}

var aiToolApprovalStoreOf = func(s *Server) aiToolApprovalStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

func (s *Server) getAIToolApproval(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, chstore.CurrentAIToolApproval())
}

// aiToolApprovalPut — PUT gövdesi; nil alan = mevcut korunur.
type aiToolApprovalPut struct {
	Enabled         *bool     `json:"enabled"`
	Tools           *[]string `json:"tools"`
	TimeoutS        *int      `json:"timeoutS"`
	ApproverMinRole *string   `json:"approverMinRole"`
	ExternalAll     *bool     `json:"externalAll"`
}

// mergeToolApprovalPut — SAF: istek + mevcut → normalize edilmiş blob.
func mergeToolApprovalPut(cur chstore.AIToolApproval, in aiToolApprovalPut) chstore.AIToolApproval {
	out := cur
	if in.Enabled != nil {
		out.Enabled = *in.Enabled
	}
	if in.Tools != nil {
		out.Tools = append([]string(nil), (*in.Tools)...)
	}
	if in.TimeoutS != nil {
		out.TimeoutS = *in.TimeoutS
	}
	if in.ApproverMinRole != nil {
		out.ApproverMinRole = *in.ApproverMinRole
	}
	if in.ExternalAll != nil {
		out.ExternalAll = *in.ExternalAll
	}
	return chstore.NormalizeAIToolApproval(out)
}

func (s *Server) putAIToolApproval(w http.ResponseWriter, r *http.Request) {
	st := aiToolApprovalStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	var in aiToolApprovalPut
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	prev := chstore.CurrentAIToolApproval()
	next := mergeToolApprovalPut(prev, in)
	if c := auth.FromContext(r.Context()); c != nil {
		next.UpdatedBy = c.Email
		if next.UpdatedBy == "" {
			next.UpdatedBy = c.UserID
		}
	}
	next.UpdatedAt = time.Now().UTC()
	saved, err := st.SaveAIToolApproval(r.Context(), next)
	if err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetAIToolApproval(saved) // sahte depo testleri için de yayınla
	s.audit(r, "settings.ai_tool_approval.update", "settings", chstore.AIToolApprovalKey, aiToolApprovalAuditDetails(prev, saved))
	writeJSON(w, saved)
}

// aiToolApprovalAuditDetails — SAF: önce → sonra (kim/ne zaman alanları hariç).
func aiToolApprovalAuditDetails(prev, next chstore.AIToolApproval) string {
	strip := func(a chstore.AIToolApproval) chstore.AIToolApproval {
		a.UpdatedBy, a.UpdatedAt = "", time.Time{}
		return a
	}
	b, _ := json.Marshal(map[string]any{"before": strip(prev), "after": strip(next)})
	return string(b)
}
