package thanos

// health_queries_test.go — servis sağlık kontrolü dilim 2 (hazır replika +
// cluster sağlığı) ve dilim 3 (JBoss datasource) PromQL kurucuları. SAF:
// seçici değerleri kaçışlanır, ad listeleri tam eşleşmeli (^…$) ve tavanlı.

import (
	"strings"
	"testing"
)

func TestWorkloadNamesFromPods(t *testing.T) {
	cases := []struct {
		pods []string
		max  int
		want []string
	}{
		{[]string{"orders-api-7c9d8f6b5d-x2kqz", "orders-api-7c9d8f6b5d-8bm4n"}, 5, []string{"orders-api"}},
		{[]string{"orders-db-0", "orders-db-1", "orders-api-7c9d8f6b5d-x2kqz"}, 5, []string{"orders-api", "orders-db"}},
		{[]string{"a-0", "b-0", "c-0"}, 2, []string{"a", "b"}},
		{nil, 5, nil},
	}
	for _, c := range cases {
		got := WorkloadNamesFromPods(c.pods, c.max)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Fatalf("%v → %v, want %v", c.pods, got, c.want)
		}
	}
}

func TestWorkloadReplicasQuery(t *testing.T) {
	q := WorkloadReplicasQuery("shop|pay", []string{"shop"}, []string{"orders-api", `we"ird.name`})
	for _, want := range []string{
		`kube_deployment_spec_replicas|kube_deployment_status_replicas_ready`,
		`kube_statefulset_replicas|kube_statefulset_status_replicas_ready`,
		`deployment=~"^(orders-api|we\"ird\\.name)$"`,
		`statefulset=~"^(orders-api|we\"ird\\.name)$"`,
		`namespace=~"^(shop)$"`,
		`namespace=~"shop|pay"`,
		`max by (__name__, namespace, deployment, statefulset)`,
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("sorgu %q içermeli:\n%s", want, q)
		}
	}
}

func TestNodeReadyAndAlertQueries(t *testing.T) {
	if q := NodeReadyQuery(); q != `count by (status) (kube_node_status_condition{condition="Ready"} == 1)` {
		t.Fatalf("node sorgusu: %s", q)
	}
	q := FiringAlertsQuery()
	for _, want := range []string{`ALERTS{alertstate="firing"`, `severity=~"critical|warning"`, `alertname!~"Watchdog|InfoInhibitor"`, `count by (alertname, severity, namespace)`, "topk("} {
		if !strings.Contains(q, want) {
			t.Fatalf("alert sorgusu %q içermeli: %s", want, q)
		}
	}
}
