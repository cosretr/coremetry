package chstore

// replica_repair_test.go — v0.10.820 Replika onarımı: saf planlayıcı
// yardımcıları + kaynak pinleri (node-yerel bağlantı, zaman tavanı,
// ON CLUSTER'sız adımlar, SYNC LIGHTWEIGHT + receive_timeout).

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

const testPeerDDL = "CREATE TABLE coremetry.alert_rules\n(\n    `id` String,\n    `version` UInt64\n)\nENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/{shard}/alert_rules', '{replica}', version)\nORDER BY id\nSETTINGS index_granularity = 8192"

func TestExpandMacros(t *testing.T) {
	m := map[string]string{"shard": "02", "replica": "ch-04"}
	if got, err := expandMacros("/clickhouse/tables/{shard}/alert_rules", m); err != nil || got != "/clickhouse/tables/02/alert_rules" {
		t.Fatalf("got %q err %v", got, err)
	}
	if got, err := expandMacros("{shard}-{replica}", m); err != nil || got != "02-ch-04" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := expandMacros("/clickhouse/tables/{uuid}/x", m); err == nil || !strings.Contains(err.Error(), "{uuid}") {
		t.Fatalf("çözülemeyen makro hata vermeli: %v", err)
	}
	if got, err := expandMacros("/plain/path", nil); err != nil || got != "/plain/path" {
		t.Fatalf("makrosuz yol aynen: %q %v", got, err)
	}
}

func TestReplicatedEngineArgs(t *testing.T) {
	fam, path, rep, ok := replicatedEngineArgs(testPeerDDL)
	if !ok || fam != "ReplicatedReplacingMergeTree" || path != "/clickhouse/tables/{shard}/alert_rules" || rep != "{replica}" {
		t.Fatalf("got %q %q %q ok=%v", fam, path, rep, ok)
	}
	if _, _, _, ok := replicatedEngineArgs("CREATE TABLE db.t (x UInt8) ENGINE = ReplicatedMergeTree ORDER BY x"); ok {
		t.Error("argümansız Replicated (default_replica_path) ok olmamalı")
	}
	if _, _, _, ok := replicatedEngineArgs("CREATE TABLE db.t (x UInt8) ENGINE = MergeTree ORDER BY x"); ok {
		t.Error("düz MergeTree ok olmamalı")
	}
}

func TestDDLTableName(t *testing.T) {
	for in, want := range map[string]string{
		testPeerDDL:                       "alert_rules",
		"CREATE TABLE `db`.`t_x` (\n":     "t_x",
		"CREATE TABLE t (":                "t",
		"  CREATE TABLE db.t UUID 'x' (":  "t",
		"CREATE MATERIALIZED VIEW db.v (": "",
	} {
		if got := ddlTableName(in); got != want {
			t.Errorf("%q → %q, want %q", in[:20], got, want)
		}
	}
}

func TestRewriteReplicaDDL(t *testing.T) {
	out, err := rewriteReplicaDDL(testPeerDDL, "coremetry", "alert_rules", "alert_rules_fix", "/clickhouse/tables/02/alert_rules")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "CREATE TABLE `coremetry`.`alert_rules_fix`\n(") {
		t.Errorf("başlık: %q", out[:60])
	}
	if !strings.Contains(out, "ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/02/alert_rules', '{replica}', version)") {
		t.Errorf("motor argümanları: yol LİTERAL, replika makro, version korunur: %q", out)
	}
	if strings.Contains(out, "{shard}") || strings.Contains(out, "ON CLUSTER") {
		t.Errorf("{shard} / ON CLUSTER kalmamalı: %q", out)
	}
	// Eksik-tablo kipinde aynı ad; backtick'li başlık + ON CLUSTER sökülür.
	bt := "CREATE TABLE `coremetry`.`alert_rules` ON CLUSTER c1\n(\n    `id` String\n)\nENGINE = ReplicatedMergeTree('/p/{shard}/t', '{replica}')\nORDER BY id"
	out, err = rewriteReplicaDDL(bt, "coremetry", "alert_rules", "alert_rules", "/p/01/t")
	if err != nil || !strings.HasPrefix(out, "CREATE TABLE `coremetry`.`alert_rules`\n(") || strings.Contains(out, "ON CLUSTER") || !strings.Contains(out, "ReplicatedMergeTree('/p/01/t', '{replica}')") {
		t.Errorf("backtick/ON CLUSTER: %q err %v", out, err)
	}
	// Yanlış tablo → hata (eşten yanlış nesne gelmiş olmalı).
	if _, err := rewriteReplicaDDL(testPeerDDL, "coremetry", "other", "other_fix", "/x"); err == nil {
		t.Error("tablo adı uyuşmazlığı hata vermeli")
	}
	// Argümansız motor → hata.
	if _, err := rewriteReplicaDDL("CREATE TABLE db.t (x UInt8) ENGINE = ReplicatedMergeTree ORDER BY x", "db", "t", "t", "/x"); err == nil {
		t.Error("argümansız motor hata vermeli")
	}
}

func TestAttachStatements(t *testing.T) {
	st, err := attachStatements("db", "t", []ReplicaRepairPartition{{ID: "20260919"}, {ID: "202609"}, {ID: "all"}, {ID: "3e5c1f2a"}})
	if err != nil || len(st) != 4 {
		t.Fatalf("%v %v", st, err)
	}
	if st[0] != "ALTER TABLE `db`.`t_fix` ATTACH PARTITION ID '20260919' FROM `db`.`t`" {
		t.Errorf("biçim: %q", st[0])
	}
	if _, err := attachStatements("db", "t", []ReplicaRepairPartition{{ID: "x' OR 1"}}); err == nil {
		t.Error("kimlik allowlist'i enjeksiyonu reddetmeli")
	}
}

func TestCleanupGate(t *testing.T) {
	cases := []struct {
		t, f string
		ok   bool
	}{
		{"ReplicatedReplacingMergeTree", "ReplacingMergeTree", true}, // EXCHANGE olmuş
		{"ReplacingMergeTree", "ReplicatedReplacingMergeTree", true}, // EXCHANGE olmamış: geri alma
		{"ReplicatedMergeTree", "", false},                           // _fix yok
		{"ReplicatedMergeTree", "ReplicatedMergeTree", false},        // belirsiz
		{"MergeTree", "MergeTree", false},                            // belirsiz
		{"", "ReplicatedMergeTree", false},                           // canlı yok
	}
	for _, c := range cases {
		note, err := cleanupGate(c.t, c.f)
		if (err == nil) != c.ok {
			t.Errorf("(%q,%q): ok=%v note=%q err=%v", c.t, c.f, err == nil, note, err)
		}
	}
}

func TestColumnsDiff(t *testing.T) {
	a := []chColumn{{"id", "String"}, {"version", "UInt64"}}
	if d := columnsDiff(a, a); len(d) != 0 {
		t.Errorf("aynı liste fark vermemeli: %v", d)
	}
	if d := columnsDiff(a, []chColumn{{"id", "String"}}); len(d) != 1 || !strings.Contains(d[0], "hedefte yok: version") {
		t.Errorf("eksik kolon: %v", d)
	}
	if d := columnsDiff(a, []chColumn{{"id", "String"}, {"version", "UInt32"}}); len(d) != 1 || !strings.Contains(d[0], "tip farklı") {
		t.Errorf("tip farkı: %v", d)
	}
	if d := columnsDiff(a, []chColumn{{"version", "UInt64"}, {"id", "String"}}); len(d) != 1 || d[0] != "kolon sırası farklı" {
		t.Errorf("sıra farkı: %v", d)
	}
}

func TestTableKeysAndIndexDiff(t *testing.T) {
	a := chTableKeys{PartitionKey: "toDate(time)", SortingKey: "service_name, time", PrimaryKey: "service_name, time", StoragePolicy: "default"}
	if d := tableKeysDiff(a, a); len(d) != 0 {
		t.Errorf("aynı anahtarlar fark vermemeli: %v", d)
	}
	b := a
	b.SortingKey = "service_name, time, trace_id"
	if d := tableKeysDiff(a, b); len(d) != 1 || !strings.Contains(d[0], "ORDER BY") {
		t.Errorf("ORDER BY farkı: %v", d)
	}
	c := a
	c.PartitionKey, c.StoragePolicy = "toYYYYMM(time)", "hot"
	if d := tableKeysDiff(a, c); len(d) != 2 {
		t.Errorf("iki fark: %v", d)
	}
	if d := skipIndexDiff([]string{"i1|bloom_filter|x"}, []string{"i1|bloom_filter|x"}); len(d) != 0 {
		t.Errorf("aynı indeksler: %v", d)
	}
	if d := skipIndexDiff([]string{"i1|bloom_filter|x"}, nil); len(d) != 1 || !strings.Contains(d[0], "hedefte yok") {
		t.Errorf("eksik indeks: %v", d)
	}
}

func TestDiskHeadroomOK(t *testing.T) {
	if !diskHeadroomOK(0, 100, 100) {
		t.Error("okunamayan boş alan (0) karar vermez")
	}
	if !diskHeadroomOK(230, 100, 100) || diskHeadroomOK(219, 100, 100) {
		t.Error("eş + yerel + %10 payı")
	}
}

// Engelli plan bile JSON'da steps:[] taşır (FE plan.steps.length okur; null Modal'ı düşürürdü).
func TestReplicaRepairPlanJSONNeverNullSteps(t *testing.T) {
	plan := &ReplicaRepairPlan{Table: "t", Partitions: []ReplicaRepairPartition{}, Steps: []string{}, Checks: []string{}, Blocked: []string{"x"}}
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"steps":[]`, `"partitions":[]`, `"checks":[]`, `"fixExists":false`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s yok: %s", want, b)
		}
	}
	if strings.Contains(string(b), "targetAddr") {
		t.Error("iç adres alanları JSON'a çıkmamalı")
	}
}

// Kaynak pinleri: hedef ve eş node-yerel bağlantıda, hostName() yoklaması,
// DB motoru okunur, her adım zaman tavanlı, SYNC LIGHTWEIGHT + receive_timeout,
// koşulan hiçbir ifade ON CLUSTER taşımaz, system.* okumaları tavanlı.
func TestReplicaRepairSourcePins(t *testing.T) {
	b, err := os.ReadFile("replica_repair.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"SELECT hostName()", "system.databases", "context.WithTimeout", "SYSTEM SYNC REPLICA", "LIGHTWEIGHT",
		`"receive_timeout"`, "zkChildren(", "purgeGuard", "s.ReplicaConsistency(ctx)", "ATTACH PARTITION ID", "EXCHANGE TABLES",
		// inceleme 2026-09-19: tipler SQL'de sabit; anahtar/indeks/disk kapıları; Steps hiç nil değil.
		"toUInt32(total_replicas), toUInt32(active_replicas), toUInt8(is_readonly)",
		"partition_key, sorting_key, primary_key, storage_policy", "system.data_skipping_indices", "system.disks",
		"Steps: []string{}", "res.VerifyError = verr.Error()",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	if strings.Count(src, "s.shardConn(") < 3 {
		t.Error("hedef + eş + yoklama shardConn ile bağlanmalı")
	}
	if strings.Contains(src, "telemetryReadConn") || strings.Contains(src, "s.read.") {
		t.Error("DDL/system okumaları okuma havuzuna gitmemeli")
	}
	// Go string literallerinde ON CLUSTER yok (yorumlar serbest).
	for _, m := range regexp.MustCompile(`"[^"\n]*ON CLUSTER[^"\n]*"`).FindAllString(src, -1) {
		t.Errorf("koşulan ifadede ON CLUSTER: %s", m)
	}
	if n, m := strings.Count(src, "FROM system."), strings.Count(src, "max_execution_time"); m < n {
		t.Errorf("system.* okumaları tavanlı olmalı: %d okuma, %d tavan", n, m)
	}
	// Uygula plan engelliyken koşmaz; temizlik motorları DROP'tan önce okur.
	apply := src[strings.Index(src, "func (s *Store) ApplyReplicaRepair"):]
	if strings.Index(apply, "len(plan.Blocked) > 0") > strings.Index(apply, "conn.Exec(") {
		t.Error("Blocked kontrolü Exec'ten önce olmalı")
	}
	clean := src[strings.Index(src, "func (s *Store) CleanupReplicaRepair"):]
	if strings.Index(clean, "cleanupGate(") > strings.Index(clean, "conn.Exec(") {
		t.Error("cleanupGate DROP'tan önce olmalı")
	}
}
