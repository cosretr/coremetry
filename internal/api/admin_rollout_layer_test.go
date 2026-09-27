package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.197 kaynak-pini (inceleme S3/S4): apply ucu ön kontrolü HER istekte
// koşar ve Supported/probe hatası/0011 kapısını sunucuda doğrular — arayüz
// kapıları doğrudan POST'la atlanamaz.
func TestRolloutLayerApplyGatesServerSide(t *testing.T) {
	b, err := os.ReadFile("admin_rollout_layer.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *Server) postRolloutLayerApply(")
	if i < 0 {
		t.Fatal("postRolloutLayerApply bulunamadı")
	}
	body := src[i:]
	for _, need := range []string{"!pre.Supported", "len(pre.ProbeErrors) > 0", "pre.MVGate && pre.Layer0011", "http.StatusConflict"} {
		if !strings.Contains(body, need) {
			t.Errorf("apply kapısı %q taşımıyor", need)
		}
	}
	if strings.Index(body, "s.store.RolloutLayerPreflight(") > strings.Index(body, "s.store.RolloutLayerApply(") {
		t.Fatal("ön kontrol DDL'den ÖNCE koşmalı")
	}
}

// rolloutLayerHandlerBody — bir handler'ın gövdesi (sonraki `func`'a kadar).
func rolloutLayerHandlerBody(t *testing.T, src, fn string) string {
	t.Helper()
	i := strings.Index(src, "func (s *Server) "+fn+"(")
	if i < 0 {
		t.Fatalf("%s bulunamadı", fn)
	}
	body := src[i+1:]
	if j := strings.Index(body, "\nfunc "); j >= 0 {
		body = body[:j]
	}
	return body
}

// v0.10.960 — 0015 (Rollouts v2 state tabloları) uçları: aynı kart, aynı
// dosya, api.go büyümez. 0012'nin apply/rollback'i ve audit türleri
// DEĞİŞMEDİ; 0015 kendi türleriyle (rollout_layer.apply_0015 /
// rollout_layer.rollback_0015) ve kendi hedef kimliğiyle (0015_rollouts_v2)
// denetlenir. Apply ön kontrolü sunucuda HER istekte koşar (0012 S3/S4
// emsali); rollback confirm:true ister (sekiz tablo verisiyle düşer,
// argocd_sync_events tarihçesi geri gelmez).
func TestRolloutV2LayerAdminRoutes(t *testing.T) {
	b, err := os.ReadFile("admin_rollout_layer.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`"GET /api/admin/rollout-layer/preflight-0015"`,
		`"POST /api/admin/rollout-layer/apply-0015"`,
		`"POST /api/admin/rollout-layer/rollback-0015"`,
		`s.audit(r, "rollout_layer.apply_0015", "clickhouse", "0015_rollouts_v2"`,
		`s.audit(r, "rollout_layer.rollback_0015", "clickhouse", "0015_rollouts_v2"`,
		"v0.10.960 —",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	// 4 (0012) + 3 (0015) uç — hepsi admin kapılı, kapı KAYIT satırında.
	if n := strings.Count(src, "auth.RequireRole(auth.RoleAdmin"); n != 7 {
		t.Errorf("yedi uç da admin kapılı olmalı, %d", n)
	}
	// Audit: apply_0015 reddi + sonucu (2), rollback_0015 sonucu (1);
	// 0012'nin türleri aynı sayıda kaldı (2 + 1).
	for lit, want := range map[string]int{
		`s.audit(r, "rollout_layer.apply_0015",`:    2,
		`s.audit(r, "rollout_layer.rollback_0015",`: 1,
		`s.audit(r, "rollout_layer.apply",`:         2,
		`s.audit(r, "rollout_layer.rollback",`:      1,
	} {
		if n := strings.Count(src, lit); n != want {
			t.Errorf("%s sayısı %d, beklenen %d", lit, n, want)
		}
	}

	apply := rolloutLayerHandlerBody(t, src, "postRolloutV2LayerApply")
	for _, need := range []string{"!pre.Supported", "len(pre.ProbeErrors) > 0", "http.StatusConflict", "REDDEDİLDİ", "context.WithoutCancel"} {
		if !strings.Contains(apply, need) {
			t.Errorf("apply-0015 kapısı %q taşımıyor", need)
		}
	}
	pre := strings.Index(apply, "s.store.RolloutV2LayerPreflight(pctx, cluster)")
	run := strings.Index(apply, "s.store.RolloutV2LayerApply(")
	if pre < 0 || run < 0 || pre > run {
		t.Fatal("apply-0015: ön kontrol İSTENEN küme için DDL'den ÖNCE koşmalı")
	}
	if strings.Contains(apply, "RolloutLayerApply(") || strings.Contains(apply, "RolloutLayerPreflight(") {
		t.Error("apply-0015 0012'nin yolunu çağırmamalı")
	}

	rb := rolloutLayerHandlerBody(t, src, "postRolloutV2LayerRollback")
	gate := strings.Index(rb, "!in.Confirm")
	drop := strings.Index(rb, "s.store.RolloutV2LayerRollback(")
	if gate < 0 || drop < 0 || gate > drop {
		t.Fatal("rollback-0015: confirm kapısı DDL'den ÖNCE olmalı")
	}
	if strings.Contains(rb, "RolloutLayerRollback(") {
		t.Error("rollback-0015 0012'nin MV geri almasını çağırmamalı (0012 semantiği değişmez)")
	}
	// 0012 rollback'i hâlâ yalnız MV (chstore pinler) ve kendi ucunda.
	if !strings.Contains(rolloutLayerHandlerBody(t, src, "postRolloutLayerRollback"), "s.store.RolloutLayerRollback(ctx, cluster)") {
		t.Error("0012 rollback ucu değişmemeli")
	}

	if api := readAPISourceNoComments(t, "api.go"); strings.Contains(api, "0015") || strings.Contains(api, "RolloutV2Layer") {
		t.Error("api.go büyümemeli — 0015 uçları registerRolloutLayerAdminRoutes içinde")
	}
}

// Girdi kapıları store'a DOKUNMADAN döner (store nil: dokunsa panik olurdu).
func TestRolloutV2LayerHandlersRejectBadInputBeforeStore(t *testing.T) {
	s := &Server{}
	cases := []struct {
		label string
		h     http.HandlerFunc
		body  string
		want  string
	}{
		{"apply bozuk JSON", s.postRolloutV2LayerApply, `{`, "geçersiz JSON"},
		{"apply küme yok", s.postRolloutV2LayerApply, `{"cluster":"  "}`, "cluster required"},
		{"rollback bozuk JSON", s.postRolloutV2LayerRollback, `{`, "geçersiz JSON"},
		{"rollback küme yok", s.postRolloutV2LayerRollback, `{"confirm":true}`, "cluster required"},
		{"rollback confirm yok", s.postRolloutV2LayerRollback, `{"cluster":"uptrace_all"}`, "confirm:true zorunlu"},
		{"rollback confirm false", s.postRolloutV2LayerRollback, `{"cluster":"uptrace_all","confirm":false}`, "confirm:true zorunlu"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		c.h(rec, httptest.NewRequest(http.MethodPost, "/api/admin/rollout-layer/x", strings.NewReader(c.body)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("[%s] = %d %q, beklenen 400 …%q…", c.label, rec.Code, rec.Body.String(), c.want)
		}
	}
}

// v0.10.975 — preflight-0015 cevabı "kurulu" hükmünü taşır: installed
// (bool, her zaman) + missing ([] — null değil). Uç struct'ı OLDUĞU GİBİ
// yazar (writeJSON(w, res)), telin şekli chstore etiketleridir — burada
// uçla aynı yazıcıdan geçirilip doğrulanır. Apply-0015'in sunucu kapısı
// DEĞİŞMEDİ: Installed'a bakmaz (kurulu kümeye zorla basılan 0015 IF NOT
// EXISTS ile no-op; kapı hâlâ !pre.Supported / probe hatası). Kurulu
// kümede Supported true kaldığı için zorla apply 409 almaz.
func TestRolloutV2LayerPreflightInstalledShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, chstore.RolloutV2LayerPreflightResult{
		Clusters: []string{"uptrace_all"}, Cluster: "uptrace_all", SpansLocal: true,
		Conflicts: []string{}, Missing: []string{}, Supported: true, Installed: true,
		Detail: "Kurulu: sekiz tablo her host'ta birleşik yolda — uygulama gerekmiyor",
	})
	var m map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"installed": "true", "supported": "true", "missing": "[]", "conflicts": "[]"} {
		if string(m[k]) != want {
			t.Errorf("%s = %s, beklenen %s", k, m[k], want)
		}
	}
	// Kısmi kurulum: eksik listesi telde dizi olarak.
	rec = httptest.NewRecorder()
	writeJSON(rec, chstore.RolloutV2LayerPreflightResult{Supported: true, Missing: []string{"host-4: sekizi de yok"}})
	m = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if string(m["installed"]) != "false" || string(m["missing"]) != `["host-4: sekizi de yok"]` {
		t.Errorf("kısmi: installed=%s missing=%s", m["installed"], m["missing"])
	}

	b, err := os.ReadFile("admin_rollout_layer.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(rolloutLayerHandlerBody(t, src, "getRolloutV2LayerPreflight"), "writeJSON(w, res)") {
		t.Error("preflight-0015 struct'ı olduğu gibi yazmalı (yeni alanlar tele ulaşsın)")
	}
	apply := rolloutLayerHandlerBody(t, src, "postRolloutV2LayerApply")
	if strings.Contains(apply, "Installed") || strings.Contains(apply, "Missing") {
		t.Error("apply-0015 kapısı Installed/Missing'e bakmamalı — kurulu kümeye zorla apply güvenli no-op, kapı değişmedi")
	}
	if !strings.Contains(apply, "if err != nil || len(pre.ProbeErrors) > 0 || !pre.Supported {") {
		t.Error("apply-0015 kapısı değişmemeli (!pre.Supported / probe hatası → 409)")
	}
}
