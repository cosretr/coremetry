package mcptools

// service_healthcheck_cluster_test.go — v0.10.2042 (servis sağlık kontrolü,
// dilim 2): kontrol 9 hazır replika + kontrol 10 cluster sağlığı. Cluster
// başına satır; okunamayan kaynak Ölçülemedi, asla İyi.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/thanos"
)

func TestHCReplicas(t *testing.T) {
	podRows := []thanos.PodRow{{Namespace: "shop", Pod: "orders-api-7c9d8f6b5d-x2kqz", CPUCores: 0.1, MemBytes: 1}}
	w := func(desired, ready float64) []WorkloadReplicas {
		return []WorkloadReplicas{{Namespace: "shop", Kind: "Deployment", Name: "orders-api", Desired: desired, Ready: ready, DesiredOK: true, ReadyOK: true}}
	}
	cases := []struct {
		name string
		cl   ServicePodsCluster
		want string
	}{
		{"okunamadı → ölçülemedi", ServicePodsCluster{Rows: podRows, WorkloadsErr: "boom"}, HCUnknown},
		{"iş yükü yok → ölçülemedi", ServicePodsCluster{Rows: podRows, WorkloadsOK: true, WorkloadNames: []string{"orders-api"}}, HCUnknown},
		{"3/3 → iyi", ServicePodsCluster{Rows: podRows, WorkloadsOK: true, Workloads: w(3, 3)}, HCOK},
		{"2/3 → dikkat", ServicePodsCluster{Rows: podRows, WorkloadsOK: true, Workloads: w(3, 2)}, HCWarn},
		{"0/3 → sorun", ServicePodsCluster{Rows: podRows, WorkloadsOK: true, Workloads: w(3, 0)}, HCCrit},
		{"0'a ölçeklenmiş → iyi (bilgi)", ServicePodsCluster{Rows: podRows, WorkloadsOK: true, Workloads: w(0, 0)}, HCOK},
		{"hazır serisi yok → ölçülemedi", ServicePodsCluster{Rows: podRows, WorkloadsOK: true, Workloads: []WorkloadReplicas{{Namespace: "shop", Kind: "Deployment", Name: "orders-api", Desired: 3, DesiredOK: true}}}, HCUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := hcReplicas("orders-api", "cluster-a", c.cl)
			if r.State != c.want || r.Check != HCCheckReplicas || r.Subject != "cluster-a" {
				t.Fatalf("got %q (%s), want %q", r.State, r.Seen, c.want)
			}
		})
	}
}

func TestHCClusterHealth(t *testing.T) {
	podRows := []thanos.PodRow{{Namespace: "shop", Pod: "orders-api-1", CPUCores: 0.1, MemBytes: 1}}
	alert := func(name, sev, ns string) ClusterAlert {
		return ClusterAlert{Name: name, Severity: sev, Namespace: ns, Count: 1}
	}
	cases := []struct {
		name string
		cl   ServicePodsCluster
		want string
		seen string
	}{
		{"ikisi de okunamadı → ölçülemedi", ServicePodsCluster{Rows: podRows}, HCUnknown, "okunamadı"},
		{"temiz", ServicePodsCluster{Rows: podRows, NodeReadyOK: true, NodesReady: 6, AlertsOK: true}, HCOK, "6 node hazır"},
		{"NotReady node → sorun", ServicePodsCluster{Rows: podRows, NodeReadyOK: true, NodesReady: 5, NodesNotReady: 1, AlertsOK: true}, HCCrit, "1 node hazır değil"},
		{"servis ns'inde critical → sorun", ServicePodsCluster{Rows: podRows, NodeReadyOK: true, NodesReady: 6, AlertsOK: true, Alerts: []ClusterAlert{alert("KubePodCrashLooping", "critical", "shop")}}, HCCrit, "KubePodCrashLooping"},
		{"başka ns'te critical → dikkat", ServicePodsCluster{Rows: podRows, NodeReadyOK: true, NodesReady: 6, AlertsOK: true, Alerts: []ClusterAlert{alert("KubeAPIErrorBudgetBurn", "critical", "openshift-kube-apiserver")}}, HCWarn, "KubeAPIErrorBudgetBurn"},
		{"servis ns'inde warning → dikkat", ServicePodsCluster{Rows: podRows, NodeReadyOK: true, NodesReady: 6, AlertsOK: true, Alerts: []ClusterAlert{alert("KubeHpaMaxedOut", "warning", "shop")}}, HCWarn, "KubeHpaMaxedOut"},
		{"başka ns'te warning → iyi (sayı yazılır)", ServicePodsCluster{Rows: podRows, NodeReadyOK: true, NodesReady: 6, AlertsOK: true, Alerts: []ClusterAlert{alert("X", "warning", "other")}}, HCOK, "1 warning"},
		{"alert okunamadı, node temiz → ölçülemedi", ServicePodsCluster{Rows: podRows, NodeReadyOK: true, NodesReady: 6}, HCUnknown, "alert'ler okunamadı"},
		{"alert okunamadı ama NotReady → sorun", ServicePodsCluster{Rows: podRows, NodeReadyOK: true, NodesReady: 5, NodesNotReady: 2}, HCCrit, "hazır değil"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := hcClusterHealth("orders-api", "cluster-a", c.cl)
			if r.State != c.want || !strings.Contains(r.Seen, c.seen) || r.Check != HCCheckCluster {
				t.Fatalf("got %q (%s), want %q ∋ %q", r.State, r.Seen, c.want, c.seen)
			}
		})
	}
}

// TestHCPodsWithClusterChecks — seçenekler açıkken cluster başına 5 satır,
// sıra kontrol-önce (katalog sırası 6, 7, 8, 9, 10).
func TestHCPodsWithClusterChecks(t *testing.T) {
	cl := ServicePodsCluster{ID: "a", Name: "a", RestartsOK: true, Restarts: map[string]float64{}, WaitingOK: true, Waiting: map[string]string{},
		Rows:        []thanos.PodRow{{Namespace: "shop", Pod: "orders-api-1", MemPct: 10, CPUPct: 10, CPUCores: 0.1, MemBytes: 1}},
		WorkloadsOK: true, Workloads: []WorkloadReplicas{{Namespace: "shop", Kind: "Deployment", Name: "orders-api", Desired: 1, Ready: 1, DesiredOK: true, ReadyOK: true}},
		NodeReadyOK: true, NodesReady: 3, AlertsOK: true}
	b := cl
	b.ID, b.Name = "b", "b"
	all := hcPodChecks{replicas: true, cluster: true}
	rows := hcClassifyPodsOpts("orders-api", ServicePodsResult{Thanos: true, Clusters: []ServicePodsCluster{cl, b}}, nil, false, all)
	var got []string
	for _, r := range rows {
		got = append(got, r.Check+"/"+r.Subject+"/"+r.State)
	}
	want := "pod_restarts/a/ok pod_restarts/b/ok pod_waiting/a/ok pod_waiting/b/ok pod_resources/a/ok pod_resources/b/ok replicas/a/ok replicas/b/ok cluster/a/ok cluster/b/ok"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
	if n := len(hcClassifyPodsOpts("orders-api", ServicePodsResult{}, nil, true, all)); n != 5 {
		t.Fatalf("Thanos yokken 5 satır, got %d", n)
	}
}
