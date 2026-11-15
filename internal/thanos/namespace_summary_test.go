package thanos

// v0.10.2176 (Clusters S8, plan §9.3) — namespace detayının anlık alanları,
// iş yükü türü ve pod satırının node'u.
//
// SÖZLEŞME:
//   - NamespaceSummary hata DÖNMEZ; aile yoksa alan boş (restarts24h nil).
//   - Bekleme sebebi pod başına, konteynerler arasında en kötüsü.
//   - DeploymentRow.Kind owner zincirinden: RS→Deployment "Deployment",
//     doğrudan StatefulSet / DaemonSet / Job; sezgisel eşleşmede boş.
//   - PodRow.Node kube_pod_info'dan; seri yoksa boş, satır yine gelir.

import (
	"context"
	"fmt"
	"testing"
)

func nsResSample(resource, v string) string {
	return `{"metric":{"resource":"` + resource + `"},"value":[1784271068,"` + v + `"]}`
}

func waitSample(pod, reason string) string {
	return fmt.Sprintf(`{"metric":{"namespace":"orders","pod":"%s","reason":"%s"},"value":[1784271068,"1"]}`, pod, reason)
}

func TestNamespaceSummaryQueries(t *testing.T) {
	if got := nsResourceSumQuery("requests", `ord"ers`); got != `sum by (resource) (kube_pod_container_resource_requests{resource=~"cpu|memory",namespace="ord\"ers"})` {
		t.Fatalf("requests: %s", got)
	}
	if got := nsRestarts24hQuery("orders"); got != `sum(increase(kube_pod_container_status_restarts_total{namespace="orders"}[24h]))` {
		t.Fatalf("restarts: %s", got)
	}
}

func TestNamespaceSummaryFields(t *testing.T) {
	srv := fakeQuerier(t, "", map[string]string{
		"kube_pod_container_resource_requests":              vec(nsResSample("cpu", "16"), nsResSample("memory", "4096")),
		"kube_pod_container_resource_limits":                vec(nsResSample("cpu", "32"), nsResSample("memory", "8192")),
		"increase(kube_pod_container_status_restarts_total": vec(`{"metric":{},"value":[1784271068,"6.8"]}`),
		"kube_pod_container_status_waiting_reason": vec(
			waitSample("api-1", "CrashLoopBackOff"),
			waitSample("api-1", "ImagePullBackOff"),
			waitSample("api-2", "ContainerCreating")),
	})
	defer srv.Close()
	got := New().NamespaceSummary(context.Background(), ClusterConfig{Name: "c", URL: srv.URL, Enabled: true}, "orders")
	if got.CPURequestCores != 16 || got.MemRequestBytes != 4096 || got.CPULimitCores != 32 || got.MemLimitBytes != 8192 {
		t.Fatalf("istek/limit: %+v", got)
	}
	if got.Restarts24h == nil || *got.Restarts24h != 7 {
		t.Fatalf("restarts24h: %v", got.Restarts24h)
	}
	if got.Waiting["api-1"] != "ImagePullBackOff" || got.Waiting["api-2"] != "ContainerCreating" || len(got.Waiting) != 2 {
		t.Fatalf("waiting: %+v", got.Waiting)
	}
}

func TestNamespaceSummaryMissingFamilies(t *testing.T) {
	srv := fakeQuerier(t, "", map[string]string{})
	defer srv.Close()
	got := New().NamespaceSummary(context.Background(), ClusterConfig{Name: "c", URL: srv.URL, Enabled: true}, "orders")
	if got.CPURequestCores != 0 || got.MemLimitBytes != 0 || got.Restarts24h != nil || got.Waiting != nil {
		t.Fatalf("aile yokken alanlar boş olmalı: %+v", got)
	}
}

func ownerKindSample(pod, kind, owner string) string {
	return fmt.Sprintf(`{"metric":{"pod":"%s","owner_kind":"%s","owner_name":"%s"},"value":[1784271068,"1"]}`, pod, kind, owner)
}

func TestDeploymentMetricsKind(t *testing.T) {
	srv := fakeQuerier(t, "", map[string]string{
		"rate(container_cpu_usage_seconds_total": podVec(
			podSample("api-7d9f8c6b5-x2k4j", "0.5"),
			podSample("cache-0", "0.3"),
			podSample("agent-x2k4j", "0.2"),
			podSample("lonely-7d9f8c6b5-q8r2t", "0.1")),
		"container_memory_working_set_bytes": podVec(),
		"kube_pod_owner": podVec(
			ownerKindSample("api-7d9f8c6b5-x2k4j", "ReplicaSet", "api-7d9f8c6b5"),
			ownerKindSample("cache-0", "StatefulSet", "cache"),
			ownerKindSample("agent-x2k4j", "DaemonSet", "agent")),
		"kube_replicaset_owner": podVec(rsSample("api-7d9f8c6b5", "api")),
	})
	defer srv.Close()
	rows, err := New().DeploymentMetrics(context.Background(), ClusterConfig{Name: "c", URL: srv.URL, Enabled: true}, "ns")
	if err != nil {
		t.Fatalf("DeploymentMetrics: %v", err)
	}
	kind := map[string]string{}
	for _, r := range rows {
		kind[r.Deployment] = r.Kind
	}
	want := map[string]string{"api": "Deployment", "cache": "StatefulSet", "agent": "DaemonSet", "lonely": ""}
	for w, k := range want {
		if got, ok := kind[w]; !ok || got != k {
			t.Fatalf("%s türü: %q (var=%v), want %q — %+v", w, got, ok, k, rows)
		}
	}
}

func TestPodMetricsNode(t *testing.T) {
	srv := fakeQuerier(t, "", map[string]string{
		"container_cpu_usage_seconds_total":  vec(sample("orders", "api-1", "0.5"), sample("orders", "api-2", "0.2")),
		"container_memory_working_set_bytes": vec(sample("orders", "api-1", "100"), sample("orders", "api-2", "50")),
		"kube_pod_info":                      vec(`{"metric":{"namespace":"orders","pod":"api-1","node":"worker-7"},"value":[1784271068,"1"]}`),
	})
	defer srv.Close()
	rows, _, err := New().PodMetrics(context.Background(), ClusterConfig{Name: "c", URL: srv.URL, Enabled: true}, "")
	if err != nil {
		t.Fatalf("PodMetrics: %v", err)
	}
	node := map[string]string{}
	for _, r := range rows {
		node[r.Pod] = r.Node
	}
	if node["api-1"] != "worker-7" || node["api-2"] != "" || len(node) != 2 {
		t.Fatalf("node: %+v", node)
	}
}
