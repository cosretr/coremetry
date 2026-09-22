package api

// admin_mv_leftover_test.go — v0.10.830 route/kapı/audit pinleri.
//
// Rotalar KENDİ dosyasında: admin_dangling_mv_test.go o dosyada TAM İKİ
// RequireRole ve TAM İKİ audit literali sayıyor; buraya yazmak o kapıyı
// sessizce gevşetirdi (admin_mv_rebuild.go ile aynı disiplin). api.go
// BÜYÜMEZ — kayıt route defterinden (registerRoutesExtra).

import (
	"os"
	"strings"
	"testing"
)

func TestMVLeftoverAdminRoutes(t *testing.T) {
	b, err := os.ReadFile("admin_mv_leftover.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`registerRoutesExtra("mv-leftover"`,
		`"POST /api/admin/clickhouse/mv-leftover/drop-view"`,
		`"POST /api/admin/clickhouse/mv-leftover/drop-inner"`,
		"s.store.DropLeftoverMV(r.Context(), in.Host, in.View)",
		"s.store.DropOrphanInner(r.Context(), in.Host, in.UUID)",
		"http.StatusConflict",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	if n := strings.Count(src, "auth.RequireRole(auth.RoleAdmin"); n != 2 {
		t.Errorf("iki uç da admin kapılı olmalı, %d", n)
	}
	// Her uçta başarı VE hata audit'e düşer: iki uç × iki dal.
	if n := strings.Count(src, `s.audit(r, "clickhouse.mv_leftover"`); n != 4 {
		t.Errorf("her uçta başarı VE hata audit'e düşmeli, %d audit", n)
	}
	// confirm kapısı ortak gövdede ve store çağrılarından ÖNCE.
	if !strings.Contains(src, "if !in.Confirm {") {
		t.Error("confirm:true kapısı yok")
	}
	gate := strings.Index(src, "if !in.Confirm {")
	for _, call := range []string{"s.store.DropLeftoverMV(", "s.store.DropOrphanInner("} {
		if i := strings.Index(src, call); gate < 0 || i < 0 || gate > i {
			t.Errorf("confirm kapısı %s çağrısından ÖNCE olmalı", call)
		}
	}
	// İstemci `.inner_id.…` ADI göndermez: uç uuid alır, adı chstore kurar.
	if strings.Contains(src, `in.Inner`) || strings.Contains(src, `"inner"`) {
		t.Error("uç istemciden iç tablo ADI almamalı — yalnız uuid")
	}

	// Bitişik dosya kendi pinlerini korur: dördüncü/beşinci rota oraya SIZMADI,
	// ama GET cevabı artık artıkları da taşır (liste HER ZAMAN dizi).
	d, err := os.ReadFile("admin_dangling_mv.go")
	if err != nil {
		t.Fatal(err)
	}
	dsrc := string(d)
	if strings.Count(dsrc, "auth.RequireRole(auth.RoleAdmin") != 2 {
		t.Error("admin_dangling_mv.go iki kapısını korumalı")
	}
	for _, want := range []string{
		// v0.10.833 — öksüz kararı kapsamanın topladığı `TO INNER UUID`
		// kümesine bakar; küme İKİNCİ bir probe açmadan zarftan taşınır.
		"s.store.MVAdminReport(r.Context())", // v0.10.848 — tek envanter zarfı
		`out["leftoverError"]`,
		`out["leftovers"] = lo`,
		"lo = []chstore.MVLeftover{}", // null DEĞİL: kart "yok"u "ölçülmedi"den ayırır
	} {
		if !strings.Contains(dsrc, want) {
			t.Errorf("GET cevabında eksik: %s", want)
		}
	}
}

// v0.10.848 — zarf tek envanterden üçünü kurar; artıklar kapsamanın hedef
// kümesini alır (833: ikinci probe YOK). Pin chstore tarafında yaşar.
func TestMVAdminReportPassesCoverageTargetsToLeftovers(t *testing.T) {
	d, err := os.ReadFile("../chstore/mv_admin_report.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d), "s.mvLeftoversFrom(ctx, snap, r.Coverage.Targets, addrs)") {
		t.Error("artıklar kapsamanın Targets kümesini almalı (ikinci probe yok)")
	}
}
