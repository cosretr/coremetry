package api

// mcp_service_pods_healthcheck_test.go — v0.10.2042 (servis sağlık kontrolü
// dilim 2). Pod adaptörünün ek okumaları SAHTE Thanos'a karşı (httptest;
// canlı çağrı yok): replika / node Ready / ALERTS ayrıştırması, sorgu sayısı
// (cluster başına sabit) ve bir sorgu düşünce yalnız o alanın "okunamadı"
// kalması.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/thanos"
)

type hcFakeThanos struct {
	mu      sync.Mutex
	queries []string
	fail    string // bu alt dizeyi taşıyan sorgu 500 döner
}

func (f *hcFakeThanos) handler(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	q := r.Form.Get("query")
	f.mu.Lock()
	f.queries = append(f.queries, q)
	fail := f.fail
	f.mu.Unlock()
	if fail != "" && strings.Contains(q, fail) {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	type res struct {
		Metric map[string]string `json:"metric"`
		Value  []any             `json:"value"`
	}
	now := float64(time.Now().Unix())
	pod := map[string]string{"namespace": "shop", "pod": "orders-api-7c9d8f6b5d-x2kqz"}
	var out []res
	switch {
	case strings.Contains(q, "kube_deployment_spec_replicas"):
		out = []res{
			{map[string]string{"__name__": "kube_deployment_spec_replicas", "namespace": "shop", "deployment": "orders-api"}, []any{now, "3"}},
			{map[string]string{"__name__": "kube_deployment_status_replicas_ready", "namespace": "shop", "deployment": "orders-api"}, []any{now, "2"}},
		}
	case strings.Contains(q, "kube_node_status_condition"):
		out = []res{{map[string]string{"status": "true"}, []any{now, "5"}}, {map[string]string{"status": "false"}, []any{now, "1"}}}
	case strings.Contains(q, "ALERTS"):
		out = []res{{map[string]string{"alertname": "KubePodCrashLooping", "severity": "critical", "namespace": "shop"}, []any{now, "1"}}}
	case strings.Contains(q, "container_cpu_usage_seconds_total"), strings.Contains(q, "container_memory_working_set_bytes"):
		out = []res{{pod, []any{now, "0.5"}}}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": out}})
}

func newHCFakeServer(t *testing.T, f *hcFakeThanos) *Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	svc := thanos.New()
	svc.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{{Name: "cluster-a", URL: srv.URL, Enabled: true}}})
	return &Server{thanos: svc}
}

func TestMCPServicePodsHealthcheckExtras(t *testing.T) {
	f := &hcFakeThanos{}
	s := newHCFakeServer(t, f)
	to := time.Now()
	opt := mcptools.ServicePodsOpts{WaitingReasons: true, Replicas: true, ClusterHealth: true}
	res, err := s.mcpServicePods(context.Background(), "orders-api", nil, to.Add(-15*time.Minute), to, opt)
	if err != nil || len(res.Clusters) != 1 {
		t.Fatalf("err=%v clusters=%d", err, len(res.Clusters))
	}
	cl := res.Clusters[0]
	if len(cl.Rows) != 1 {
		t.Fatalf("pod satırı: %+v", cl.Rows)
	}
	if !cl.WorkloadsOK || len(cl.Workloads) != 1 || cl.Workloads[0].Desired != 3 || cl.Workloads[0].Ready != 2 || cl.Workloads[0].Kind != "Deployment" {
		t.Fatalf("replika: %+v (err %q)", cl.Workloads, cl.WorkloadsErr)
	}
	if !cl.NodeReadyOK || cl.NodesReady != 5 || cl.NodesNotReady != 1 {
		t.Fatalf("node: ok=%v %d/%d", cl.NodeReadyOK, cl.NodesReady, cl.NodesNotReady)
	}
	if !cl.AlertsOK || len(cl.Alerts) != 1 || cl.Alerts[0].Name != "KubePodCrashLooping" {
		t.Fatalf("alert: %+v", cl.Alerts)
	}
	// Cluster başına sorgu sayısı: PodMetrics 11 + restart artışı 1 + bekleme
	// sebebi 1 + replika 1 + node 1 + alert 1 = 16 (sabit, pod sayısından bağımsız).
	if n := len(f.queries); n != 16 {
		t.Fatalf("cluster başına 16 sorgu beklenir, got %d:\n%s", n, strings.Join(f.queries, "\n"))
	}
	// Seçenekler kapalıyken (tarama) ek sorgu YOK: 13.
	f.queries = nil
	if _, err := s.mcpServicePods(context.Background(), "orders-api", nil, to.Add(-15*time.Minute), to, mcptools.ServicePodsOpts{WaitingReasons: true}); err != nil {
		t.Fatal(err)
	}
	if n := len(f.queries); n != 13 {
		t.Fatalf("tarama yolu değişmemeli (13 sorgu), got %d", n)
	}
}

func TestMCPServicePodsHealthcheckPartialFailure(t *testing.T) {
	f := &hcFakeThanos{fail: "ALERTS"}
	s := newHCFakeServer(t, f)
	to := time.Now()
	res, err := s.mcpServicePods(context.Background(), "orders-api", nil, to.Add(-15*time.Minute), to,
		mcptools.ServicePodsOpts{WaitingReasons: true, Replicas: true, ClusterHealth: true})
	if err != nil {
		t.Fatal(err)
	}
	cl := res.Clusters[0]
	if cl.AlertsOK || !cl.NodeReadyOK || !cl.WorkloadsOK {
		t.Fatalf("yalnız alert okunamadı olmalı: alerts=%v node=%v wl=%v", cl.AlertsOK, cl.NodeReadyOK, cl.WorkloadsOK)
	}
}
