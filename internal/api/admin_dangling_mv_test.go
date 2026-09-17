package api

// admin_dangling_mv_test.go — v0.10.762 route/kapı/audit pinleri.

import (
	"os"
	"strings"
	"testing"
)

func TestDanglingMVAdminRoutes(t *testing.T) {
	b, err := os.ReadFile("admin_dangling_mv.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`registerRoutesExtra("dangling-mv"`,
		`"GET /api/admin/clickhouse/dangling-mv"`, `"POST /api/admin/clickhouse/dangling-mv/repair"`,
		`s.audit(r, "clickhouse.dangling_mv_repair"`, "http.StatusConflict",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	if strings.Count(src, "auth.RequireRole(auth.RoleAdmin") != 2 {
		t.Error("iki uç da admin kapılı olmalı")
	}
	if strings.Count(src, `s.audit(r, "clickhouse.dangling_mv_repair"`) != 2 {
		t.Error("başarı VE hata audit'e düşmeli")
	}
}
