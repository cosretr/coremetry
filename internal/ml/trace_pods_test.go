package ml

// v0.10.1759 — T3: pod p95 aykırısı (≥3 pod, ≥1.5× medyan), VM pod
// sorguları (span türevi yok), replay parçaları.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestLatencyOutliers(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	pods := []podLatency{{"a", 100}, {"b", 105}, {"c", 98}, {"d", 400}}
	got := latencyOutliers(pods, cfg)
	if len(got) != 1 || got[0].Pod != "d" || got[0].Median != 102.5 {
		t.Fatalf("got %+v", got)
	}
	if latencyOutliers(pods[:2], cfg) != nil {
		t.Fatal("<3 pod → karar yok")
	}
	flat := []podLatency{{"a", 100}, {"b", 100}, {"c", 100}, {"d", 140}}
	if len(latencyOutliers(flat, cfg)) != 0 {
		t.Fatal("1.5× medyan altı aykırı değil")
	}
}

func TestTracePodQueriesAndParts(t *testing.T) {
	pin := anomaly.REDFamilyPins()[0]
	tq, eq, pq := tracePodQueries(pin, "service_name", "k8s_pod_name", "http_response_status_code", 600)
	for _, q := range []string{tq, eq, pq} {
		if !strings.Contains(q, "by (service_name, k8s_pod_name") || strings.Contains(q, "span") {
			t.Fatalf("q=%s", q)
		}
	}
	if !strings.Contains(eq, `http_response_status_code=~"5.."`) {
		t.Fatalf("err=%s", eq)
	}
	if p := eventPart(chstore.MLEvent{Signal: "trace", Detector: detTraceChangePoint, Code: "/pay"}); p != "/pay|p95" {
		t.Fatalf("cp part=%s", p)
	}
	if p := eventPart(chstore.MLEvent{Signal: "trace", Detector: detTracePodOutlier, Code: "p95"}); p != "" {
		t.Fatalf("pod part=%s", p)
	}
}
