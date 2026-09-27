package api

// admin_state_path_rebuild_test.go — v0.10.965 route/kapı/audit/önbellek
// pinleri (admin_replica_repair_test.go deseni). Operatör kararı 2026-09-27:
// on state tablosu birleşik yolda DROP + CREATE; kart dosyası
// (admin_replica_consistency.go) salt okuma kalır.

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestStatePathRebuildAdminRoutes(t *testing.T) {
	b, err := os.ReadFile("admin_state_path_rebuild.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`registerRoutesExtra("state-path-rebuild"`,
		`"POST /api/admin/clickhouse/replica-consistency/state-paths/plan"`,
		`"POST /api/admin/clickhouse/replica-consistency/state-paths/apply"`,
		"confirm:true zorunlu", "ack zorunlu",
		"context.WithoutCancel(r.Context()), 12*time.Minute",
		"context.WithTimeout(r.Context(), 90*time.Second)",
		`s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")`,
		"http.StatusConflict", "chstore.StatePathRebuildAllowed(n)",
		"izin listesinde değil", `"ön kontrol geçmedi — "`,
		`"stage": "refused"`, `"stage": "apply"`, `"stage": "error"`,
		"writeErr(w, err)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	// v0.10.971 — kural 3 kalktı: kısmi seçim onayı (partialOK) ve kilit yok;
	// audit eski yolda kalanları (bilgi) taşır.
	if !strings.Contains(src, `"stillLegacy": res.StillLegacy`) {
		t.Error("apply audit'i stillLegacy taşımalı")
	}
	for _, gone := range []string{"PartialOK", "partialOK", "lockOpen", "LockOpen"} {
		if strings.Contains(src, gone) {
			t.Errorf("%q hâlâ var — kural 3 ile kilit/kısmi onay kalktı", gone)
		}
	}
	if n := strings.Count(src, "auth.RequireRole(auth.RoleAdmin"); n != 2 {
		t.Errorf("iki uç da admin kapılı olmalı (%d)", n)
	}
	// Ret, uygulama ve hata: üç audit sahası — başka yok.
	if n := strings.Count(src, `s.audit(r, "clickhouse.state_path_rebuild"`); n != 3 {
		t.Errorf("audit literali %d (3: refused / apply / error)", n)
	}
	if n := strings.Count(src, `s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")`); n != 1 {
		t.Errorf("apply kartın önbelleğini bir kez düşürmeli (%d)", n)
	}
	// Plan salt okuma: audit yok, DDL yok, önbellek düşürmez.
	at := strings.Index(src, "func (s *Server) postStatePathPlan(")
	end := strings.Index(src[at:], "\n}\n")
	plan := src[at : at+end]
	for _, bad := range []string{"s.audit(", "ApplyStatePathRebuild", "cacheInvalidate", "Exec("} {
		if strings.Contains(plan, bad) {
			t.Errorf("plan işleyicisi %q içeriyor — salt okuma olmalı", bad)
		}
	}
	if !strings.Contains(plan, "s.store.PlanStatePathRebuild(ctx, tables)") {
		t.Error("plan işleyicisi PlanStatePathRebuild'i çağırmalı")
	}
	// Kart dosyası hâlâ salt okuma (pini admin_replica_consistency_test.go'da).
	cb, err := os.ReadFile("admin_replica_consistency.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cb), `mux.HandleFunc("POST `) {
		t.Error("admin_replica_consistency.go salt okuma kalmalı; POST uçları admin_state_path_rebuild.go'da")
	}
}

// TestStatePathEvalFreshMatchesEvalStale — R8'in "çalışan koşu" penceresi
// ürünün terk eşiğiyle AYNI: 15 dk sessiz "running" satırı zaten terk
// edilmiş sayılır; sihirbaz onu çalışıyor saymamalı (ve tersi).
func TestStatePathEvalFreshMatchesEvalStale(t *testing.T) {
	if chstore.StatePathEvalFresh != evalRunStaleAfter {
		t.Errorf("chstore.StatePathEvalFresh = %v, evalRunStaleAfter = %v", chstore.StatePathEvalFresh, evalRunStaleAfter)
	}
}

func TestStatePathAuditTarget(t *testing.T) {
	if got := statePathAuditTarget([]string{"rollout_events", "ai_eval_runs", "ingest_ledger"}); got != "state_paths:ai_eval_runs,ingest_ledger,rollout_events" {
		t.Errorf("hedef = %q", got)
	}
	if got := statePathAuditTarget(nil); got != "state_paths:*" {
		t.Errorf("varsayılan seçim hedefi = %q", got)
	}
	if got := statePathAuditJSON(map[string]any{"stage": "error", "error": `a "b" c`}); got != `{"error":"a \"b\" c","stage":"error"}` {
		t.Errorf("audit JSON = %s", got)
	}
}
