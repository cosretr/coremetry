package chstore

import "testing"

func TestMLMetricVolumeExpr(t *testing.T) {
	defs := DefaultMLMetricFamilies()["metric_http_client"]
	want := "sum by ({svc}) (increase(http_client_request_duration_seconds_count[{win}]))"
	for _, d := range defs {
		if got := MLMetricVolumeExpr("metric_http_client", d); got != want {
			t.Fatalf("%s: default volume = %q", d.ID, got)
		}
		// Eski blob: alan yok, ifade varsayılanla aynı → varsayılan hacim.
		old := d
		old.VolumeExpr = ""
		if got := MLMetricVolumeExpr("metric_http_client", old); got != want {
			t.Fatalf("%s: legacy blob volume = %q", d.ID, got)
		}
	}
	cases := []struct {
		name string
		e    MLMetricExpr
		want string
	}{
		{"explicit wins", MLMetricExpr{ID: "x", Expr: "anything", VolumeExpr: " sum(foo) "}, "sum(foo)"},
		{"derived quantile with selector", MLMetricExpr{ID: "p99", Expr: `histogram_quantile(0.99, sum by ({svc}, le) (rate(rpc_client_duration_seconds_bucket{rpc_system="grpc"}[{win}])))`},
			`sum by ({svc}) (increase(rpc_client_duration_seconds_count{rpc_system="grpc"}[{win}]))`},
		{"edited default ratio not derived", MLMetricExpr{ID: "client_5xx", Expr: `sum by ({svc}) (rate(a_count{code="500"}[{win}])) / sum by ({svc}) (rate(a_count[{win}]))`}, ""},
		{"gauge ungated", MLMetricExpr{ID: "heap_used", Expr: `avg by ({svc}) (jvm_memory_used_bytes)`}, ""},
		{"two bucket metrics", MLMetricExpr{ID: "q", Expr: `histogram_quantile(0.9, sum by ({svc}, le) (rate(a_bucket[{win}]) + rate(b_bucket[{win}])))`}, ""},
		{"le selector", MLMetricExpr{ID: "q", Expr: `histogram_quantile(0.9, sum by ({svc}, le) (rate(a_bucket{le="1"}[{win}])))`}, ""},
		{"no svc placeholder", MLMetricExpr{ID: "q", Expr: `histogram_quantile(0.9, sum by (le) (rate(a_bucket[{win}])))`}, ""},
	}
	for _, c := range cases {
		if got := MLMetricVolumeExpr("metric_http_client", c.e); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	if MLMetricMinVolume != 30 {
		t.Fatalf("MLMetricMinVolume = %d, want 30 (StdMinCalls5m)", MLMetricMinVolume)
	}
}
