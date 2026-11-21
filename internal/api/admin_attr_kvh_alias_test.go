package api

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cluster"
)

// admin_attr_kvh_alias_test.go — v0.10.2410: iki uç deftere kayıtlı ve
// admin kapılı; koşu başlangıcı + reddi + bitişi audit'li; api.go büyümedi;
// 0014 sihirbazının uçları silindi.
func TestAttrKVHAliasRoutes(t *testing.T) {
	b, err := os.ReadFile("admin_attr_kvh_alias.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`registerRoutesExtra("attr-kvh-alias"`,
		`"GET /api/admin/attr-kvh-alias/status"`, `"POST /api/admin/attr-kvh-alias/run"`,
		`s.audit(r, "attr_kvh_alias.run", "clickhouse", "0017_attr_kvh_alias", "REDDEDİLDİ: "`,
		`s.audit(r, "attr_kvh_alias.run", "clickhouse", "0017_attr_kvh_alias",`,
		`s.auditAs(claims, ip, "attr_kvh_alias.run_done"`,
		"http.StatusConflict", `cluster.RegisterFact("attrIndexMode"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%q yok", want)
		}
	}
	if strings.Count(src, "auth.RequireRole(auth.RoleAdmin") != 2 {
		t.Error("iki uç da admin kapılı olmalı")
	}
	api := readAPISourceNoComments(t, "api.go")
	if strings.Contains(api, "attr-kvh-alias") || strings.Contains(api, "attr-index") {
		t.Error("api.go büyümemeli")
	}
	if _, err := os.Stat("admin_attr_index.go"); err == nil {
		t.Error("0014 sihirbazı (admin_attr_index.go) 0017 ile değişti — geri gelmemeli")
	}
}

func TestAttrKVHPodsAndAudit(t *testing.T) {
	pods := attrKVHPodsFromMembers([]cluster.Member{
		{ID: "a", Hostname: "pa", Facts: map[string]string{"attrIndexMode": "v2"}, IsThisPod: true},
		{ID: "b", Hostname: "pb"},
	})
	if len(pods) != 2 || pods[0].Mode != "v2" || !pods[0].IsThisPod || pods[1].Mode != "" {
		t.Errorf("pod'lar: %+v", pods)
	}
	d := attrKVHJobAudit(chstore.AttrKVHJob{Cluster: "c1", FailedStep: "legacy", Err: "x",
		Log: []chstore.RollupStmtResult{{OK: true}, {OK: false}}})
	for _, w := range []string{`"cluster":"c1"`, `"ok":1`, `"total":2`, `"failedStep":"legacy"`} {
		if !strings.Contains(d, w) {
			t.Errorf("%s yok: %s", w, d)
		}
	}
}
