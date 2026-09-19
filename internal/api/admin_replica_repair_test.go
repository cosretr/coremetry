package api

// admin_replica_repair_test.go — v0.10.820 route/kapı/audit/önbellek pinleri
// (admin_dangling_mv_test.go deseni). admin_replica_consistency.go'ya
// dokunulmaz (o dosya "POST yok" pinli).

import (
	"os"
	"strings"
	"testing"
)

func TestReplicaRepairAdminRoutes(t *testing.T) {
	b, err := os.ReadFile("admin_replica_repair.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`registerRoutesExtra("replica-repair"`,
		`"POST /api/admin/clickhouse/replica-consistency/repair/plan"`,
		`"POST /api/admin/clickhouse/replica-consistency/repair/apply"`,
		`"POST /api/admin/clickhouse/replica-consistency/repair/cleanup"`,
		`s.audit(r, "clickhouse.replica_repair"`, "http.StatusConflict",
		`s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")`,
		"confirm:true zorunlu",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	if strings.Count(src, "auth.RequireRole(auth.RoleAdmin") != 3 {
		t.Error("üç uç da admin kapılı olmalı")
	}
	if strings.Count(src, `s.audit(r, "clickhouse.replica_repair"`) != 4 {
		t.Error("apply ve cleanup: başarı VE hata audit'e düşmeli (4)")
	}
	if strings.Count(src, `s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")`) != 2 {
		t.Error("apply ve cleanup kartın önbelleğini düşürmeli")
	}
	// confirm kapısı: apply/cleanup needConfirm=true, plan false.
	if strings.Count(src, "decodeReplicaRepairInput(w, r, true)") != 2 || strings.Count(src, "decodeReplicaRepairInput(w, r, false)") != 1 {
		t.Error("confirm kapısı: apply+cleanup true, plan false")
	}
	// Kart dosyası hâlâ salt okuma.
	cb, err := os.ReadFile("admin_replica_consistency.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cb), `mux.HandleFunc("POST `) {
		t.Error("admin_replica_consistency.go salt okuma kalmalı; POST uçları admin_replica_repair.go'da")
	}
}
