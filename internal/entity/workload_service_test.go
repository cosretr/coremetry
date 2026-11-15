package entity

import "testing"

// v0.10.2191 — operatör örneği: Deployment demo-portal-dashboard-prod
// (ns portal, cluster kprod) ↔ servis demo-portal-dashboard-prod.
func TestResolveWorkloadService(t *testing.T) {
	const ns = "portal"
	pods := []string{"demo-portal-dashboard-prod-7d9f8c6b5-abcde", "demo-portal-dashboard-prod-7d9f8c6b5-fghij"}
	seen := []SeenEdge{
		{Namespace: ns, Pod: pods[0], Node: "worker-1", Service: "demo-portal-dashboard-prod", Spans: 900},
		{Namespace: ns, Pod: pods[0], Node: "worker-1", Service: "otel-agent", Spans: 10},
		{Namespace: "", Pod: pods[1], Node: "worker-2", Service: "demo-portal-dashboard-prod", Spans: 500}, // ns basılmamış
		{Namespace: "other", Pod: "x-1", Service: "nope", Spans: 1},
	}
	edges := IndexPodEdges(ns, seen)
	if e := edges[pods[0]]; e.Service != "demo-portal-dashboard-prod" || e.Node != "worker-1" {
		t.Fatalf("edge0 %+v", e)
	}
	if e := edges[pods[1]]; e.Service != "demo-portal-dashboard-prod" || e.Node != "worker-2" {
		t.Fatalf("edge1 %+v", e)
	}
	if _, ok := edges["x-1"]; ok {
		t.Fatal("other-namespace edge leaked")
	}

	links := []DeploymentLink{
		{Service: "dash-attr", Deployment: "demo-portal-dashboard-prod", Namespace: ns},
		{Service: "payments", Deployment: "payments-api", Namespace: "store"}, // service.namespace maskesi
		{Service: "twin-a", Deployment: "twin", Namespace: "x"},
		{Service: "twin-b", Deployment: "twin", Namespace: "y"},
	}
	known := []string{"demo-portal-dashboard-prod", "Orders-Svc", "twin"}
	cases := []struct {
		name, wl string
		pods     []string
		links    []DeploymentLink
		want     string
	}{
		{"pod runs edges win", "demo-portal-dashboard-prod", pods, links, "demo-portal-dashboard-prod"},
		{"attr link when no pod edge", "demo-portal-dashboard-prod", nil, links, "dash-attr"},
		{"exact name equality as last resort", "demo-portal-dashboard-prod", nil, nil, "demo-portal-dashboard-prod"},
		{"attr link single candidate, ns masked", "payments-api", nil, links, "payments"},
		{"ambiguous attr link falls to name", "twin", nil, links, "twin"},
		{"name equality case-insensitive", "orders-svc", nil, nil, "Orders-Svc"},
		{"no match", "batch-runner", nil, links, ""},
		{"empty workload", "", pods, links, ""},
	}
	for _, c := range cases {
		if got := ResolveWorkloadService(c.wl, ns, c.pods, edges, c.links, known); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
