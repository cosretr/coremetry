package api

// admin_mv_target_repair_test.go — v0.10.835 route/kapı/audit pinleri.
//
// Rota KENDİ dosyasında (admin_mv_rebuild_test.go ile aynı disiplin):
// komşu dosyalar TAM SAYIDA RequireRole ve audit literali sayıyor, dördüncü
// rotayı oraya yazmak o kapıları sessizce gevşetirdi. Tek rota → tek
// RequireRole, başarı VE hata audit'e düşer, hata 409, confirm kapısı DDL
// çağrısından ÖNCE.

import (
	"os"
	"strings"
	"testing"
)

func TestMVTargetRepairAdminRoute(t *testing.T) {
	b, err := os.ReadFile("admin_mv_target_repair.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`registerRoutesExtra("mv-target-repair"`,
		`"POST /api/admin/clickhouse/mv-target/repair"`,
		`s.audit(r, "clickhouse.mv_target_repair"`,
		"http.StatusConflict",
		// Kararı STORE verir: uygunluk kapısı (fail-closed) ve şekil ayrımı
		// tek gövdede — handler'da ikinci bir kopya doğarsa ikisi ayrışır.
		"s.store.RepairMVTargetOnHost(r.Context(), in.Host, in.View, in.Peer, in.DropEmpty)",
		// Onarım `.inner_id.…` nesnesini doğurur/düşürür; Replika tutarlılığı
		// kartı o satırları 30 sn önbellekliyor.
		`s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	if n := strings.Count(src, "auth.RequireRole(auth.RoleAdmin"); n != 1 {
		t.Errorf("tek admin kapısı bekleniyordu, %d", n)
	}
	if n := strings.Count(src, `s.audit(r, "clickhouse.mv_target_repair"`); n != 2 {
		t.Errorf("başarı VE hata audit'e düşmeli, %d audit", n)
	}
	if n := strings.Count(src, `s.cacheInvalidate(r.Context(), "admin:ch:replica-consistency")`); n != 2 {
		t.Errorf("iki yolda da önbellek düşmeli (YARIM bir onarım da nesneyi değiştirmiş olabilir), %d", n)
	}
	if !strings.Contains(src, "if !in.Confirm {") {
		t.Error("confirm:true kapısı yok — bu uç DDL koşar")
	}
	if i, j := strings.Index(src, "if !in.Confirm {"), strings.Index(src, "s.store.RepairMVTargetOnHost("); i < 0 || j < 0 || i > j {
		t.Error("confirm kapısı DDL çağrısından ÖNCE olmalı")
	}
	// Audit DETAYI dalı VE koşan ifadeleri söyler: "tarihçe neden sıfırlandı"
	// ile "YARIM onarım nerede durdu" sorularının tek kalıcı kaydı budur.
	// Adımları yalnız SAYMAK (v0.10.835 öncesi) ikisini de cevapsız
	// bırakıyordu.
	for _, want := range []string{
		`"peer": peer`, `"dropEmpty": dropEmpty`, `"steps": steps`,
		"mvTargetAuditDetail(false, in.Peer, in.DropEmpty, err.Error(), steps)",
		"mvTargetAuditDetail(true, in.Peer, in.DropEmpty, \"\", steps)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("audit detayı %q taşımalı — dal + KOŞAN adımlar kaydedilmeli", want)
		}
	}
	// YARIM bir onarımın adımları CEVABA da girer: writeJSONError yalnız
	// {"error"} yazar, o yüzden hata gövdesi elle kurulur ama `error` alanı
	// AYNI yerde kalır (istemci onu okur).
	if !strings.Contains(src, `map[string]any{"error": err.Error(), "steps": steps}`) {
		t.Error("hata cevabı koşan adımları TAŞIMALI — ekranda görünmeyen bir yarım onarım operatörü kör bırakır")
	}
	if i, j := strings.Index(src, "http.StatusConflict"), strings.Index(src, `"steps": steps}`); i < 0 || j < 0 || i > j {
		t.Error("409 gövdesi steps ile BİRLİKTE yazılmalı")
	}
	// Komşu dosyalar kendi pinlerini korur: dördüncü rota oraya SIZMADI.
	for file, want := range map[string]int{"admin_dangling_mv.go": 2, "admin_mv_rebuild.go": 1, "admin_mv_leftover.go": 2} {
		d, rerr := os.ReadFile(file)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if n := strings.Count(string(d), "auth.RequireRole(auth.RoleAdmin"); n != want {
			t.Errorf("%s kapı sayısını korumalı: %d, beklenen %d", file, n, want)
		}
	}
}
