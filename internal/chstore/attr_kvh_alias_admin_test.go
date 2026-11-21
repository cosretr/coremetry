package chstore

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/migrations"
)

// attr_kvh_alias_admin_test.go — v0.10.2410: 0017 sihirbazı. Adım SQL'i
// (küme + tek düğüm) gömülü 0017 ile birebir; idempotans (IF [NOT] EXISTS,
// MODIFY yok); probe ayrıştırma (host sayımı, adım hükmü, pod kipi,
// MATERIALIZE komutları); gizleme / engel hükmü.

func attrKVHNorm(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestAttrKVHStepSQLMatchesMigration(t *testing.T) {
	raw, err := migrations.FS.ReadFile(attrKVHMigrationFile)
	if err != nil {
		t.Fatal(err)
	}
	file := map[string]bool{}
	for _, s := range SplitSQLStatements(AdaptRollupDDL(string(raw), "c1")) {
		file[attrKVHNorm(s)] = true
	}
	n := 0
	for _, step := range []string{AttrKVHStepColumns, AttrKVHStepIndexes, AttrKVHStepLegacy} {
		for _, q := range attrKVHStepSQL(step, "c1", "") {
			if !strings.Contains(q, " ON CLUSTER c1 ") {
				t.Errorf("%s: ON CLUSTER yok: %s", step, q)
			}
			if file[attrKVHNorm(q)] {
				n++
			} else if !strings.Contains(q, "_keys TYPE") { // anahtar indeksleri 0014'ten
				t.Errorf("%s: 0017'de olmayan ifade: %s", step, q)
			}
		}
	}
	if n != len(file) {
		t.Errorf("0017'nin %d ifadesinden %d'i sihirbazda", len(file), n)
	}
	cols := attrKVHStepSQL(AttrKVHStepColumns, "c1", "")
	if len(cols) != 4 || !strings.HasPrefix(cols[0], "ALTER TABLE spans_local ") || !strings.HasPrefix(cols[2], "ALTER TABLE spans ON") {
		t.Errorf("kolonlar: local → sarmalayıcı: %v", cols)
	}
	leg := attrKVHStepSQL(AttrKVHStepLegacy, "c1", "")
	if len(leg) != 6 || !strings.Contains(leg[0], "DROP INDEX IF EXISTS idx_attr_kvh") || !strings.Contains(leg[2], "DROP COLUMN IF EXISTS attr_kvh, ADD COLUMN attr_kvh") {
		t.Errorf("eski: önce indeks, sonra tek ALTER DROP+ADD: %v", leg)
	}
	if got := attrKVHStepSQL(AttrKVHStepSwitch, "c1", ""); len(got) != 0 {
		t.Errorf("switch SQL taşımaz: %v", got)
	}
	mat := attrKVHStepSQL(AttrKVHStepMaterialize, "c1", "20261120")
	if len(mat) != 2 || mat[0] != "ALTER TABLE spans_local ON CLUSTER c1 MATERIALIZE INDEX idx_attr_kvh2 IN PARTITION ID '20261120'" {
		t.Errorf("materialize: %v", mat)
	}
}

func TestAttrKVHStepSQLSingleNodeAndIdempotent(t *testing.T) {
	for _, step := range []string{AttrKVHStepColumns, AttrKVHStepIndexes, AttrKVHStepLegacy, AttrKVHStepMaterialize} {
		for _, cluster := range []string{"", "c1"} {
			for _, q := range attrKVHStepSQL(step, cluster, "") {
				if cluster == "" && (strings.Contains(q, "ON CLUSTER") || strings.Contains(q, "spans_local")) {
					t.Errorf("tek düğüm: %s", q)
				}
				if strings.Contains(q, "MODIFY COLUMN") {
					t.Errorf("MODIFY … ALIAS yasak (Code 47): %s", q)
				}
				if step != AttrKVHStepMaterialize && !strings.Contains(q, "IF NOT EXISTS") && !strings.Contains(q, "IF EXISTS") {
					t.Errorf("tekrar koşmak güvenli olmalı: %s", q)
				}
			}
		}
	}
	if n := len(attrKVHStepSQL(AttrKVHStepColumns, "", "")); n != 2 {
		t.Errorf("tek düğüm kolon ifadesi 2 olmalı, %d", n)
	}
	if n := len(attrKVHStepObjects(AttrKVHStepLegacy, "c1")); n != 6 {
		t.Errorf("eski adım nesneleri 2 indeks + 4 kolon, %d", n)
	}
}

func TestAttrKVHProbeParsing(t *testing.T) {
	if attrKVHHave("present", 3, 4) != 3 || attrKVHHave("alias", 4, 4) != 4 || attrKVHHave("absent", 1, 4) != 3 || attrKVHHave("absent", 5, 4) != 0 {
		t.Error("attrKVHHave")
	}
	st := func(states ...string) []AttrKVHObjectStatus {
		var o []AttrKVHObjectStatus
		for _, s := range states {
			o = append(o, AttrKVHObjectStatus{State: s})
		}
		return o
	}
	for _, tc := range []struct {
		in   []AttrKVHObjectStatus
		want string
	}{
		{st("ok", "ok"), "done"}, {st("missing", "missing"), "pending"}, {st("ok", "partial"), "partial"},
		{st("ok", "missing"), "partial"}, {st("ok", "unknown"), "unknown"}, {nil, "unknown"},
	} {
		if got := attrKVHStepState(tc.in); got != tc.want {
			t.Errorf("%v → %s; want %s", tc.in, got, tc.want)
		}
	}
	if s, _ := attrKVHSwitchState(nil); s != "unknown" {
		t.Error("pod yok → unknown")
	}
	if s, _ := attrKVHSwitchState([]AttrKVHPod{{Mode: "v2"}, {Mode: "v2"}}); s != "done" {
		t.Error("hepsi v2 → done")
	}
	if s, d := attrKVHSwitchState([]AttrKVHPod{{Hostname: "a", Mode: "v2"}, {Hostname: "b"}}); s != "partial" || !strings.Contains(d, "b=bildirmiyor") {
		t.Errorf("eski sürüm pod → partial: %s %s", s, d)
	}
	all, ids := attrKVHMaterializedPartitions([]string{
		"MATERIALIZE INDEX idx_attr_kvh2 IN PARTITION ID '20261120'",
		"MATERIALIZE INDEX idx_res_kvh2 IN PARTITION ID '20261120'",
		"MATERIALIZE INDEX idx_attr_kvh2 IN PARTITION ID '20261121'",
		"DROP COLUMN attr_kvh",
	})
	if all || !ids["20261120"] || ids["20261121"] {
		t.Errorf("partition: all=%v ids=%v", all, ids)
	}
	if all, _ := attrKVHMaterializedPartitions([]string{"MATERIALIZE INDEX idx_attr_kvh2", "MATERIALIZE INDEX idx_res_kvh2"}); !all {
		t.Error("tüm tablo materialize → all")
	}
}

func TestAttrKVHPendingAndBlocker(t *testing.T) {
	done := func(ids ...string) []AttrKVHStep {
		var o []AttrKVHStep
		for _, id := range ids {
			o = append(o, AttrKVHStep{ID: id, State: "done"})
		}
		return o
	}
	full := AttrKVHAliasStatus{Cluster: "c1", SpansLocal: true, Steps: append(done(attrKVHRequiredSteps...), AttrKVHStep{ID: AttrKVHStepMaterialize, State: "optional"})}
	if attrKVHPending(full) {
		t.Error("dört adım tamam → gizli (MATERIALIZE opsiyonel)")
	}
	part := full
	part.Steps = done(AttrKVHStepColumns, AttrKVHStepIndexes, AttrKVHStepSwitch)
	if !attrKVHPending(part) {
		t.Error("eski adım eksik → bekliyor")
	}
	if attrKVHPending(AttrKVHAliasStatus{BootManaged: true}) {
		t.Error("boot yönetiyor → gizli")
	}
	if b := attrKVHBlocker(AttrKVHAliasStatus{BootManaged: true}); !strings.Contains(b, "uygulama yönetimli") {
		t.Error(b)
	}
	if b := attrKVHBlocker(AttrKVHAliasStatus{SpansLocal: true}); !strings.Contains(b, "küme adı") {
		t.Error(b)
	}
	blk := full
	blk.Checks = []AttrKVHCheck{{Name: "ClickHouse sürümü", OK: false}, {Name: "host erişimi", Blocking: true, OK: false, Detail: "2/3"}}
	if b := attrKVHBlocker(blk); !strings.HasPrefix(b, "host erişimi") {
		t.Errorf("engelleyici olmayan kontrol koşuyu durdurmaz, engelleyen durdurur: %q", b)
	}
	if b := attrKVHBlocker(full); b != "" {
		t.Error(b)
	}
}
