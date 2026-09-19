package api

// admin_replica_consistency_test.go — v0.10.791 route/kapı pinleri (salt
// okuma; eylem uçları ve audit bir sonraki dilimde gelir).

import (
	"os"
	"strings"
	"testing"
)

func TestReplicaConsistencyAdminRoutes(t *testing.T) {
	b, err := os.ReadFile("admin_replica_consistency.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`registerRoutesExtra("replica-consistency"`,
		`"GET /api/admin/clickhouse/replica-consistency"`,
		`s.serveCached(w, r, "admin:ch:replica-consistency", 30*time.Second`,
		`s.store.ReplicaConsistency(ctx)`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	if strings.Count(src, "auth.RequireRole(auth.RoleAdmin") != 1 {
		t.Error("uç admin kapılı olmalı")
	}
	// Yorum metni değil, kayıt: mux.HandleFunc("POST … (gate kendi metnini ısırmasın).
	if strings.Contains(src, `mux.HandleFunc("POST `) {
		t.Error("bu dilim salt okuma — eylem uçları (SYNC/RESTORE) audit'le birlikte ayrı dilimde")
	}
}
