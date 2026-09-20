package chstore

// ddl_blind_hosts_test.go — v0.10.823: DDL-kör host boot uyarısının
// sözleşmesi.
//
// Sözleşme: (1) iki sorgu da clusterAllReplicas + bütçe +
// skip_unavailable_shards; (2) küme adı WHERE'de BAĞLANIR, sorgu
// currentDatabase() çözmez; (3) roster AYRICA system.one'dan okunur —
// GROUP BY sıfır satırlı grubu üretmez, "absent" host aksi hâlde hiç
// görünmezdi; (4) sınıflandırma v0.10.818'in saf blindHosts'unu KULLANIR,
// yeniden yazmaz; (5) boot'ta store.go New() çağırır.

import (
	"os"
	"strings"
	"testing"
)

func TestDDLBlindHostsSQLBounded(t *testing.T) {
	roster := ddlBlindRosterSQL("uptrace_all")
	for _, w := range []string{
		"clusterAllReplicas('uptrace_all', system.one)",
		"hostName()",
		"max_execution_time = 10",
		"skip_unavailable_shards = 1",
	} {
		if !strings.Contains(roster, w) {
			t.Errorf("roster sorgusu %q içermeli:\n%s", w, roster)
		}
	}
	local := ddlBlindIsLocalSQL("uptrace_all")
	for _, w := range []string{
		"clusterAllReplicas('uptrace_all', system.clusters)",
		"toUInt32(countIf(is_local))",
		"WHERE cluster = ?",
		"GROUP BY 1",
		"ORDER BY 1",
		"max_execution_time = 10",
		"skip_unavailable_shards = 1",
	} {
		if !strings.Contains(local, w) {
			t.Errorf("is_local sorgusu %q içermeli:\n%s", w, local)
		}
	}
	for _, s := range []string{roster, local} {
		if strings.Contains(s, "currentDatabase()") {
			t.Errorf("küme geneli sorgu currentDatabase() ÇÖZMEMELİ:\n%s", s)
		}
	}
	// Küme adı literal olarak clusterAllReplicas'ın İÇİNDE (bağlanamaz) ama
	// WHERE'de bağlı olmalı — iki yazımı karıştırmak filtreyi öldürürdü.
	if strings.Contains(local, "WHERE cluster = 'uptrace_all'") {
		t.Error("WHERE'deki küme adı bağlanmalı, gömülmemeli")
	}
}

func TestDDLBlindHostsSourceContract(t *testing.T) {
	b, err := os.ReadFile("ddl_blind_hosts.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, w := range []string{
		"blindHosts(hosts, isLocal)",             // v0.10.818 saf sınıflandırıcı, yeniden yazılmadı
		"s.conn.Query(pctx, ddlBlindRosterSQL(",  // ana bağlantı (hostName() kimliği)
		"s.conn.Query(pctx, ddlBlindIsLocalSQL(", // aynı
		"rrows.Err()", "lrows.Err()",             // yarım roster = uydurma uyarı
		"DDL-KÖR HOST",
		"if !s.clusterMode()",
	} {
		if !strings.Contains(src, w) {
			t.Errorf("ddl_blind_hosts.go %q içermeli", w)
		}
	}
	if strings.Contains(src, "log.Fatal") || strings.Contains(src, "panic(") {
		t.Error("teşhis logu boot'u ÖLDÜRMEMELİ")
	}
}

// Kablolama pini — LogDanglingMVs ile aynı yaşam döngüsü (store.go New()).
func TestDDLBlindHostsWiredAtBoot(t *testing.T) {
	b, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "s.LogDDLBlindHosts(") {
		t.Error("New() boot sonrası DDL-kör host uyarısını koşmalı")
	}
	if strings.Index(s, "s.LogDanglingMVs(") > strings.Index(s, "s.LogDDLBlindHosts(") {
		t.Error("uyarı sarkan-MV logunun HEMEN ARDINDAN gelmeli (aynı teşhis bloğu)")
	}
}

// blindHosts'un bu çağrı yerindeki sözleşmesi: roster'da olup sayımı
// olmayan host "absent" (GROUP BY onu hiç üretmez), sayımı 0 olan "zero".
func TestBlindHostsClassificationForBootWarning(t *testing.T) {
	zero, absent := blindHosts(
		[]string{"ch-01", "ch-02", "ch-03", "ch-04"},
		map[string]uint32{"ch-01": 1, "ch-02": 0, "ch-03": 2},
	)
	if len(zero) != 1 || zero[0] != "ch-02" {
		t.Errorf("is_local=0 host'u zero olmalı: %v", zero)
	}
	if len(absent) != 1 || absent[0] != "ch-04" {
		t.Errorf("küme adına satırı olmayan host absent olmalı: %v", absent)
	}
}
