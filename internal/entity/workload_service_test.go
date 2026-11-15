package entity

import "testing"

// v0.10.2191 — operatör örneği: Deployment bsa-digital-dashboard-prod
// (ns digital, cluster ocpma) ↔ servis bsa-digital-dashboard-prod.
func TestResolveWorkloadService(t *testing.T) {
	const ns = "digital"
	pods := []string{"bsa-digital-dashboard-prod-7d9f8c6b5-abcde", "bsa-digital-dashboard-prod-7d9f8c6b5-fghij"}
	seen := []SeenEdge{
		{Namespace: ns, Pod: pods[0], Node: "worker-1", Service: "bsa-digital-dashboard-prod", Spans: 900},
		{Namespace: ns, Pod: pods[0], Node: "worker-1", Service: "otel-agent", Spans: 10},
		{Namespace: "", Pod: pods[1], Node: "worker-2", Service: "bsa-digital-dashboard-prod", Spans: 500}, // ns basılmamış
		{Namespace: "other", Pod: "x-1", Service: "nope", Spans: 1},
	}
	edges := IndexPodEdges(ns, seen)
	if e := edges[pods[0]]; e.Service != "bsa-digital-dashboard-prod" || e.Node != "worker-1" {
		t.Fatalf("edge0 %+v", e)
	}
	if e := edges[pods[1]]; e.Service != "bsa-digital-dashboard-prod" || e.Node != "worker-2" {
		t.Fatalf("edge1 %+v", e)
	}
	if _, ok := edges["x-1"]; ok {
		t.Fatal("other-namespace edge leaked")
	}

	links := []DeploymentLink{
		{Service: "dash-attr", Deployment: "bsa-digital-dashboard-prod", Namespace: ns},
		{Service: "payments", Deployment: "payments-api", Namespace: "bank"}, // service.namespace maskesi
		{Service: "twin-a", Deployment: "twin", Namespace: "x"},
		{Service: "twin-b", Deployment: "twin", Namespace: "y"},
	}
	known := []string{"bsa-digital-dashboard-prod", "Orders-Svc", "twin"}
	cases := []struct {
		name, wl string
		pods     []string
		links    []DeploymentLink
		want     string
	}{
		{"pod runs edges win", "bsa-digital-dashboard-prod", pods, links, "bsa-digital-dashboard-prod"},
		{"attr link when no pod edge", "bsa-digital-dashboard-prod", nil, links, "dash-attr"},
		{"exact name equality as last resort", "bsa-digital-dashboard-prod", nil, nil, "bsa-digital-dashboard-prod"},
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
