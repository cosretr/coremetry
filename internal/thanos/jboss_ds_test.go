package thanos

// jboss_ds_test.go — v0.10.2043 (servis sağlık kontrolü dilim 3, JBoss
// datasource havuzu). Metrik adları prod'da DOĞRULANMADI: kurucular hem
// jboss_ hem wildfly_ önekini, pool / jdbc ailesini ve MicroProfile _total
// son ekini kabul eder; keşiften gelen ad enjeksiyon kalkanından geçer.

import (
	"strings"
	"testing"
	"time"
)

func TestJBossDSDiscoveryQuery(t *testing.T) {
	q := JBossDSDiscoveryQuery("apps-.*", []string{"shop"}, "(orders-api)-.*")
	for _, want := range []string{
		`count by (__name__) (`,
		`__name__=~"(jboss|wildfly)_datasources_(pool|jdbc)_.+"`,
		`pod=~"(orders-api)-.*"`,
		`namespace=~"^(shop)$"`,
		`namespace=~"apps-.*"`,
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("%q yok:\n%s", want, q)
		}
	}
}

func TestJBossDSValuesQuery(t *testing.T) {
	names := []string{"jboss_datasources_pool_in_use_count", "wildfly_datasources_pool_available_count"}
	q := JBossDSValuesQuery(names, "", []string{"shop"}, "(orders-api)-.*", 0)
	if !strings.Contains(q, `sum by (__name__, pod, data_source, xa_data_source)`) ||
		!strings.Contains(q, `__name__=~"^(jboss_datasources_pool_in_use_count|wildfly_datasources_pool_available_count)$"`) ||
		strings.Contains(q, "offset") {
		t.Fatalf("değer sorgusu: %s", q)
	}
	if q2 := JBossDSValuesQuery(names, "", []string{"shop"}, "(orders-api)-.*", 15*time.Minute); !strings.Contains(q2, "} offset 900s)") {
		t.Fatalf("offset yok: %s", q2)
	}
}

func TestJBossDSRole(t *testing.T) {
	cases := map[string]string{
		"jboss_datasources_pool_in_use_count":             JBossDSInUse,
		"wildfly_datasources_pool_in_use_count":           JBossDSInUse,
		"jboss_datasources_pool_active_count":             JBossDSActive,
		"jboss_datasources_pool_available_count":          JBossDSAvailable,
		"jboss_datasources_pool_wait_count":               JBossDSWait,
		"wildfly_datasources_pool_wait_count_total":       JBossDSWait,
		"jboss_datasources_pool_max_wait_count":           "",
		"jboss_datasources_pool_timed_out":                JBossDSTimedOut,
		"jboss_datasources_pool_timed_out_total":          JBossDSTimedOut,
		"jboss_datasources_pool_blocking_failure_count":   JBossDSBlockingFailure,
		"jboss_datasources_pool_max_used_count":           "",
		"jboss_datasources_jdbc_prepared_statement_cache": "",
		"jvm_memory_bytes_used":                           "",
	}
	for name, want := range cases {
		if got := JBossDSRole(name); got != want {
			t.Fatalf("%s → %q, want %q", name, got, want)
		}
	}
}

func TestValidJBossDSMetric(t *testing.T) {
	for _, ok := range []string{"jboss_datasources_pool_in_use_count", "wildfly_datasources_pool_timed_out_total"} {
		if !ValidJBossDSMetric(ok) {
			t.Fatalf("%s geçerli olmalı", ok)
		}
	}
	for _, bad := range []string{"jvm_x", `jboss_datasources_pool_x"}`, "jboss_datasources_pool_", "JBOSS_datasources_pool_x"} {
		if ValidJBossDSMetric(bad) {
			t.Fatalf("%s geçersiz olmalı", bad)
		}
	}
}
