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

// v0.10.2437: Thanos yolu — pod sayıları servis başına toplanır, aynı eşik.
func TestThanosVolumeGate(t *testing.T) {
	ix := buildPodIndex([]string{"svc-a", "svc-b"}, nil)
	pod := func(p, ns string, v float64) ThanosSample {
		return ThanosSample{Labels: map[string]string{"pod": p, "namespace": ns}, Value: v}
	}
	rows := []ThanosSample{
		pod("svc-a-6b8f49b9d5-aaaaa", "ns-1", 12), pod("svc-a-6b8f49b9d5-bbbbb", "ns-1", 20), // 32 → geçer
		pod("svc-b-6b8f49b9d5-ccccc", "ns-1", 10), pod("svc-b-6b8f49b9d5-ddddd", "ns-2", 25), // ns-2 süzülür → 10
		pod("unknown-6b8f49b9d5-eeeee", "ns-1", 999),
	}
	vol := thanosVolumeBySvc(rows, ix, namespaceRegex("ns-1"))
	cases := map[string]bool{"svc-a": false, "svc-b": true, "svc-missing": true}
	for svc, want := range cases {
		if got := belowMinVolume(vol, svc); got != want {
			t.Errorf("%s: below=%v want %v (vol=%v)", svc, got, want, vol)
		}
	}
	if empty := thanosVolumeBySvc(nil, ix, nil); empty == nil || !belowMinVolume(empty, "svc-a") {
		t.Fatal("successful empty volume result must gate (no calls in window)")
	}
}

func TestThanosPlanCarriesVolume(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	gated := map[string]string{}
	for _, pe := range variantPlan(cfg, "metric_http_client", cfg.MetricFamilies["metric_http_client"], 300) {
		gated[pe.id] = pe.vol
	}
	want := "sum by (namespace, pod) (increase(http_client_request_duration_seconds_count[300s]))"
	if gated["client_p95"] != want || gated["client_5xx"] != want {
		t.Fatalf("vol = %v", gated)
	}
	for _, set := range familyVariantSets(cfg, "metric_jvm") {
		for _, pe := range variantPlan(cfg, "metric_jvm", set, 300) {
			if pe.vol != "" {
				t.Fatalf("jvm %s gated: %q", pe.id, pe.vol)
			}
		}
	}
}

// Hacim sorgusu bütçeden düşer: kapılı ifade 2 sorgu sayılır.
func TestThanosPlanBudgetCountsVolume(t *testing.T) {
	var exprs []thanosPlanned
	for i := 0; i < 8; i++ {
		exprs = append(exprs, thanosPlanned{id: string(rune('a' + i)), vol: "v"})
	}
	run, cut := budgetPlan(exprs, thanosQueriesPerClusterTick)
	if len(run) != thanosQueriesPerClusterTick/2 || cut != 8-thanosQueriesPerClusterTick/2 {
		t.Fatalf("plan %d / kesik %d", len(run), cut)
	}
	mixed := []thanosPlanned{{id: "a", vol: "v"}, {id: "b"}, {id: "c", vol: "v"}}
	if run, cut := budgetPlan(mixed, 4); len(run) != 2 || cut != 1 {
		t.Fatalf("mixed plan %d / kesik %d", len(run), cut)
	}
}
