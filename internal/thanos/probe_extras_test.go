package thanos

// v0.10.2182 (Clusters S13) — probe'lu ekler: taint / yaş / disk (node),
// unschedulable (pod), restart işaretleri. SÖZLEŞME: seri yoksa alan boş,
// hata yok; disk `/` ve `/sysroot`un dolu olanı.

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProbeExtraQueries(t *testing.T) {
	if q := nodeDiskUsedQuery(); !strings.Contains(q, `mountpoint=~"/|/sysroot"`) || !strings.HasPrefix(q, "max by (instance)") {
		t.Fatalf("disk: %s", q)
	}
	if q := podRestartChangesQuery(`n"s`, "p-1", 60); q != `sum(changes(kube_pod_container_status_restarts_total{namespace="n\"s",pod="p-1"}[60s]))` {
		t.Fatalf("restart: %s", q)
	}
	if q := podUnschedulableQuery("", ""); q != `max by (namespace, pod) (kube_pod_status_unschedulable{pod!=""} == 1)` {
		t.Fatalf("unsched: %s", q)
	}
}

func TestNodeTaintsPure(t *testing.T) {
	got := nodeTaints([]promSeries{
		{Metric: map[string]string{"node": "w1", "key": "dedicated", "value": "infra", "effect": "NoSchedule"}},
		{Metric: map[string]string{"node": "w1", "key": "node.kubernetes.io/unschedulable", "effect": "NoSchedule"}},
		{Metric: map[string]string{"node": "w1", "key": "dedicated", "value": "infra", "effect": "NoSchedule"}},
		{Metric: map[string]string{"node": "", "key": "x"}},
	})
	want := map[string][]string{"w1": {"dedicated=infra:NoSchedule", "node.kubernetes.io/unschedulable:NoSchedule"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("taints: %+v", got)
	}
}

func TestNodeMetricsProbeFields(t *testing.T) {
	m := nodeCapFixture()
	m["node_filesystem_avail_bytes"] = vec(nodeSample("10.0.1.5:9100", "0.83"))
	m["kube_node_spec_taint"] = vec(`{"metric":{"node":"worker-1","key":"dedicated","value":"infra","effect":"NoSchedule"},"value":[1784271068,"1"]}`)
	m["kube_node_created"] = vec(`{"metric":{"node":"worker-1"},"value":[1784271068,"1780000000"]}`)
	r := oneNodeRow(t, m)
	if r.DiskUsedPct != 83 || !reflect.DeepEqual(r.Taints, []string{"dedicated=infra:NoSchedule"}) || r.CreatedUnix != 1780000000 {
		t.Fatalf("probe alanları: disk=%v taints=%v created=%v", r.DiskUsedPct, r.Taints, r.CreatedUnix)
	}
	bare := oneNodeRow(t, nodeCapFixture())
	if bare.DiskUsedPct != 0 || bare.Taints != nil || bare.CreatedUnix != 0 {
		t.Fatalf("seri yokken boş olmalı: %+v", bare)
	}
}

func TestPodMetricsUnschedulable(t *testing.T) {
	srv := fakeQuerier(t, "", map[string]string{
		"container_cpu_usage_seconds_total":  vec(sample("orders", "api-1", "0.5"), sample("orders", "api-2", "0.2")),
		"container_memory_working_set_bytes": vec(sample("orders", "api-1", "100"), sample("orders", "api-2", "50")),
		"kube_pod_status_unschedulable":      vec(sample("orders", "api-2", "1")),
	})
	defer srv.Close()
	rows, _, err := New().PodMetrics(context.Background(), ClusterConfig{Name: "c", URL: srv.URL, Enabled: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Pod] = r.Unschedulable
	}
	if got["api-1"] || !got["api-2"] {
		t.Fatalf("unschedulable: %+v", got)
	}
}

func TestPodRestartMarkers(t *testing.T) {
	srv := fakeQuerier(t, "", map[string]string{
		"changes(kube_pod_container_status_restarts_total": `{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{},"values":[[1784270000,"0"],[1784270060,"1"],[1784270120,"0"],[1784270180,"2"]]}]}}`,
	})
	defer srv.Close()
	to := time.Unix(1784270200, 0)
	got, err := New().PodRestartMarkers(context.Background(), ClusterConfig{Name: "c", URL: srv.URL, Enabled: true}, "orders", "api-1", to.Add(-time.Hour), to)
	if err != nil || !reflect.DeepEqual(got, []int64{1784270060, 1784270180}) {
		t.Fatalf("markers: %v err=%v", got, err)
	}
}
