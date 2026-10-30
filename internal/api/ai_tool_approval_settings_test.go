package api

// ai_tool_approval_settings_test.go — agent-core S4: `ai.toolApproval`
// yüzeyi — viewer GÖRÜR, yalnız admin yazar (audit before/after), gönderilmeyen
// alan korunur, varsayılan KAPALI.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

type toolApprovalFakeStore struct {
	mu    sync.Mutex
	saved chstore.AIToolApproval
	saves int
}

func (f *toolApprovalFakeStore) SaveAIToolApproval(_ context.Context, a chstore.AIToolApproval) (chstore.AIToolApproval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	f.saved = chstore.NormalizeAIToolApproval(a)
	return f.saved, nil
}

func TestAIToolApprovalRolesAuditRoundTrip(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(16), stats: newCacheStats(), auditQ: make(chan chstore.AuditEntry, 16)}
	st := &toolApprovalFakeStore{}
	prevStore := aiToolApprovalStoreOf
	aiToolApprovalStoreOf = func(*Server) aiToolApprovalStore { return st }
	prevCur := chstore.CurrentAIToolApproval()
	t.Cleanup(func() { aiToolApprovalStoreOf = prevStore; chstore.SetAIToolApproval(prevCur) })
	chstore.SetAIToolApproval(chstore.DefaultAIToolApproval())
	mux := http.NewServeMux()
	s.registerAIToolApprovalRoutes(mux)

	w := rulesDo(mux, "GET", "/api/ai/tool-approval", "", auth.RoleViewer, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":false`) || !strings.Contains(w.Body.String(), `"timeoutS":300`) {
		t.Fatalf("viewer GET %d: %s", w.Code, w.Body)
	}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if w := rulesDo(mux, "PUT", "/api/ai/tool-approval", `{"enabled":true}`, role, ""); w.Code != http.StatusForbidden {
			t.Fatalf("%s PUT %d", role, w.Code)
		}
	}
	if st.saves != 0 || len(s.auditQ) != 0 {
		t.Fatal("reddedilen istek yazmamalı")
	}
	w = rulesDo(mux, "PUT", "/api/ai/tool-approval", `{"enabled":true,"tools":["add_problem_note"," add_problem_note "],"timeoutS":10}`, auth.RoleAdmin, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin PUT %d: %s", w.Code, w.Body)
	}
	if !st.saved.Enabled || len(st.saved.Tools) != 1 || st.saved.TimeoutS != 30 || st.saved.ApproverMinRole != "editor" || st.saved.UpdatedBy != "admin@example.test" {
		t.Fatalf("saklanan = %+v", st.saved)
	}
	a := nextAudit(t, s)
	if a.Action != "settings.ai_tool_approval.update" || a.TargetID != chstore.AIToolApprovalKey ||
		!strings.Contains(a.Details, `"before":{"enabled":false`) || !strings.Contains(a.Details, `"after":{"enabled":true`) {
		t.Fatalf("audit = %+v", a)
	}
	// Gönderilmeyen alan korunur.
	if w := rulesDo(mux, "PUT", "/api/ai/tool-approval", `{"approverMinRole":"admin"}`, auth.RoleAdmin, ""); w.Code != http.StatusOK ||
		!st.saved.Enabled || st.saved.TimeoutS != 30 || st.saved.ApproverMinRole != "admin" || len(st.saved.Tools) != 1 {
		t.Fatalf("alan koruma: %d %+v", w.Code, st.saved)
	}
	if w := rulesDo(mux, "PUT", "/api/ai/tool-approval", `{bozuk`, auth.RoleAdmin, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bozuk JSON → %d", w.Code)
	}
	if st.saves != 2 {
		t.Fatalf("saves=%d", st.saves)
	}
}
