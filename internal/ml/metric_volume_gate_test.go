package ml

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMetricVolumeGate(t *testing.T) {
	row := func(svc string, v float64) chstore.SpanMetricSeries {
		return chstore.SpanMetricSeries{GroupKey: []string{"service_name=" + svc}, Points: []chstore.SpanMetricPoint{{Value: v}}}
	}
	vol := volumeBySvc([]chstore.SpanMetricSeries{row("svc-a", 4), row("svc-b", 30), row("svc-c", 500)}, "service_name")
	cases := map[string]bool{"svc-a": true, "svc-b": false, "svc-c": false, "svc-missing": true}
	for svc, want := range cases {
		if got := belowMinVolume(vol, svc); got != want {
			t.Errorf("%s: below=%v want %v (vol=%v)", svc, got, want, vol[svc])
		}
	}
	if belowMinVolume(nil, "svc-a") {
		t.Fatal("nil volume (ungated / query failed) must pass")
	}
}

func TestMetricExprsCarryVolume(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.Enabled = true
	cfg.Detectors = map[string]bool{"metric_http_client": true, "metric_jvm": true}
	gated := map[string]bool{}
	for _, pe := range metricExprs(cfg, "service_name", 300) {
		gated[pe.id] = pe.vol != ""
		if pe.id == "client_p95" && pe.vol != "sum by (service_name) (increase(http_client_request_duration_seconds_count[300s]))" {
			t.Fatalf("client_p95 vol = %q", pe.vol)
		}
	}
	if !gated["client_p95"] || !gated["client_5xx"] || gated["heap_used"] {
		t.Fatalf("gating = %v", gated)
	}
}
