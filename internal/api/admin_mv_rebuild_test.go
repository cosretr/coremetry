package api

// admin_mv_rebuild_test.go — v0.10.825 route/kapı/audit pinleri.
//
// Rota KENDİ dosyasında: admin_dangling_mv_test.go o dosyada TAM İKİ
// RequireRole ve TAM İKİ audit literali sayıyor; üçüncü rotayı oraya yazmak
// o kapıyı sessizce gevşetirdi. Burada da aynı disiplin: tek rota → tek
// RequireRole, başarı VE hata audit'e düşer, hata 409.

import (
	"os"
	"strings"
	"testing"
)

func TestMVRebuildAdminRoute(t *testing.T) {
	b, err := os.ReadFile("admin_mv_rebuild.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`registerRoutesExtra("mv-rebuild"`,
		`"POST /api/admin/clickhouse/dangling-mv/rebuild"`,
		`s.audit(r, "clickhouse.mv_rebuild"`,
		"http.StatusConflict",
		"s.store.RebuildMVOnHost(r.Context(), in.Host, in.View)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	if n := strings.Count(src, "auth.RequireRole(auth.RoleAdmin"); n != 1 {
		t.Errorf("tek admin kapısı bekleniyordu, %d", n)
	}
	if n := strings.Count(src, `s.audit(r, "clickhouse.mv_rebuild"`); n != 2 {
		t.Errorf("başarı VE hata audit'e düşmeli, %d audit", n)
	}
	// confirm kapısı: DDL koşan uç onaysız POST kabul etmez ve kapı store
	// çağrısından ÖNCE durur.
	if !strings.Contains(src, "if !in.Confirm {") {
		t.Error("confirm:true kapısı yok")
	}
	if i, j := strings.Index(src, "if !in.Confirm {"), strings.Index(src, "s.store.RebuildMVOnHost("); i < 0 || j < 0 || i > j {
		t.Error("confirm kapısı DDL çağrısından ÖNCE olmalı")
	}
	// Bitişik dosya kendi pinlerini korur: kapsama alanı eklendi ama üçüncü
	// bir rota/audit oraya SIZMADI.
	d, err := os.ReadFile("admin_dangling_mv.go")
	if err != nil {
		t.Fatal(err)
	}
	dsrc := string(d)
	if strings.Count(dsrc, "auth.RequireRole(auth.RoleAdmin") != 2 {
		t.Error("admin_dangling_mv.go iki kapısını korumalı")
	}
	if !strings.Contains(dsrc, "s.store.MVCoverage(r.Context())") || !strings.Contains(dsrc, `out["coverageError"]`) {
		t.Error("GET cevabı kapsamayı ve kapsama hatasını taşımalı")
	}
}
