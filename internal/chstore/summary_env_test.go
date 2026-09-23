package chstore

import (
	"strings"
	"testing"
)

// v0.10.882 (paritesi #8 dilim 2) — /api/services cluster/env kapsamı MV'den:
// kapsam boşsa kardeş service_summary_5m ve yüklemsiz (eski sorgu bayt-bayt),
// doluysa service_env_summary_5m + cluster/deploy_env yüklemleri (zaman'dan
// sonra, ad'dan önce); sayım aynı kapıdan geçer.
func TestServicesAggScopeSelectsEnvMV(t *testing.T) {
	if servicesAggSource("", "") != "service_summary_5m" || servicesAggSource("c1", "") != "service_env_summary_5m" || servicesAggSource("", "prod") != "service_env_summary_5m" {
		t.Fatal("kaynak seçimi")
	}
	cl, args := envScopeClause("", "")
	if cl != "" || len(args) != 0 {
		t.Fatalf("boş kapsam yüklem üretti: %q %v", cl, args)
	}
	cl, args = envScopeClause("c1", "prod")
	if cl != " AND cluster = ? AND deploy_env = ?" || len(args) != 2 || args[0] != "c1" || args[1] != "prod" {
		t.Fatalf("dolu kapsam: %q %v", cl, args)
	}
	q := servicesAggSQL("service_env_summary_5m", cl, " AND positionCaseInsensitive(service_name, ?) > 0", "", "spans DESC NULLS LAST", " LIMIT 50 OFFSET 0")
	if !strings.Contains(q, "FROM service_env_summary_5m") || !strings.Contains(q, "time_bucket < ? AND cluster = ? AND deploy_env = ? AND positionCaseInsensitive") {
		t.Fatalf("kapsamlı sorgu: %s", q)
	}
	if strings.Contains(q, "FROM spans") || !strings.Contains(q, "max_execution_time = 25") {
		t.Fatalf("ham tablo / tavan: %s", q)
	}
	c := countServicesAggSQL("service_env_summary_5m", " AND cluster = ?", "")
	if !strings.Contains(c, "uniqExact(service_name)") || !strings.Contains(c, "FROM service_env_summary_5m") || !strings.Contains(c, "AND cluster = ?") {
		t.Fatalf("sayım: %s", c)
	}
	t.Logf("ENVSQL:%s", strings.ReplaceAll(servicesAggSQL("service_env_summary_5m", " AND deploy_env = ?", "", "", "spans DESC NULLS LAST", " LIMIT 50 OFFSET 0"), "\n", " "))
}
