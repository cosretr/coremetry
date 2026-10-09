package chstore

// v0.10.1140 — namespace üyeliği okumalarının SQL şekli: entity_seen_5m
// (ham spans DEĞİL), zaman sınırlı WHERE + LIMIT + max_execution_time,
// cluster koşulu opsiyonel.

import (
	"strings"
	"testing"
	"time"
)

// Kök nedenin pini: deriver servis başına TEK (en sık) namespace yazar —
// payments-uat'taki seyrek trafik katalogda HİÇ görünmez. Bu yüzden süzgeç
// üyeliği katalogdan değil entity_seen_5m'den okur (api/services_namespace.go).
func TestMarginalModesKeepsOnlyMajorityNamespace(t *testing.T) {
	_, ns, _ := marginalModes([]metaComboRow{
		{Svc: "svc-gateway", NS: "payments-prep", C: 100000},
		{Svc: "svc-gateway", NS: "payments-uat", C: 50},
	})
	if ns["svc-gateway"] != "payments-prep" {
		t.Fatalf("mod beklenen payments-prep, gelen %q", ns["svc-gateway"])
	}
}

func TestServiceNamespaceMembershipSQL(t *testing.T) {
	to := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	from := to.Add(-24 * time.Hour)
	common := []string{"FROM entity_seen_5m", "time_bucket >= toStartOfFiveMinute(?)", "time_bucket <= ?", "LIMIT ", "SETTINGS max_execution_time = 10"}

	type sqlCase struct {
		name          string
		sql           string
		args          []any
		want, notWant []string
		wantArgs      int
	}
	var cases []sqlCase
	add := func(name, sql string, args []any, want, notWant []string, n int) {
		cases = append(cases, sqlCase{name, sql, args, want, notWant, n})
	}
	s, a := servicesSeenInNamespaceSQL("payments-uat", "", from, to)
	add("seen in ns, no cluster", s, a, []string{"k8s_namespace = ?", "GROUP BY service_name", "LIMIT 5000"}, []string{"cluster"}, 3)
	s, a = servicesSeenInNamespaceSQL("payments-uat", "cl-a", from, to)
	add("seen in ns, cluster", s, a, []string{"k8s_namespace = ?", "AND cluster = ?"}, nil, 4)
	s, a = namespacesSeenSQL(from, to)
	add("namespaces seen", s, a, []string{"k8s_namespace != ''", "GROUP BY k8s_namespace", "ORDER BY k8s_namespace", "LIMIT 1000"}, nil, 2)
	s, a = serviceNamespaceCountsSQL([]string{"svc-gateway"}, from, to)
	add("ns counts", s, a, []string{"service_name IN (?)", "uniqExact(k8s_namespace)", "k8s_namespace != ''", "LIMIT 500"}, nil, 3)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, w := range append(append([]string{}, common...), tc.want...) {
				if !strings.Contains(tc.sql, w) {
					t.Errorf("%q yok:\n%s", w, tc.sql)
				}
			}
			for _, nw := range append([]string{"FROM spans", "JOIN"}, tc.notWant...) {
				if strings.Contains(tc.sql, nw) {
					t.Errorf("%q olmamalı:\n%s", nw, tc.sql)
				}
			}
			if len(tc.args) != tc.wantArgs || strings.Count(tc.sql, "?") != tc.wantArgs {
				t.Errorf("arg/yer tutucu sayısı: args=%d ?=%d want %d", len(tc.args), strings.Count(tc.sql, "?"), tc.wantArgs)
			}
		})
	}
}
