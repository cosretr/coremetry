package ml

// v0.10.2036 — detay grafiğinin tek sorgusu: dedektörün kendi ifadesi,
// servis kapsamlı (VM: servis etiketi; Thanos: servisin pod regex'i; bağımlılık:
// hedefin p95'i). Fikstür nötr.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestBuildSourceQuery(t *testing.T) {
	cfg := chstore.MLConfig{MetricFamilies: chstore.DefaultMLMetricFamilies()}
	cases := []struct {
		name, det, code      string
		ok                   bool
		cluster, unit, by    string
		contains, notContain []string
	}{
		{name: "VM JVM heap: servis matcher'ı, tek seri", det: "metric_jvm", code: "metric_jvm|heap_used", ok: true, unit: "bytes",
			contains: []string{`avg by (service_name) (jvm_memory_used_bytes{service_name="orders-batch",jvm_memory_type="heap"})`}},
		{name: "VM GC: rate penceresi 120s", det: "metric_jvm", code: "metric_jvm|gc_time", ok: true, unit: "s/s",
			contains: []string{`jvm_gc_duration_seconds_sum{service_name="orders-batch"}[120s]`}},
		{name: "Thanos k8s bellek: pod kırılımı + pod regex'i", det: "metric_k8s", code: "metric_k8s|memory@prod-east", ok: true,
			cluster: "prod-east", unit: "bytes", by: "pod",
			contains: []string{`sum by (pod) (container_memory_working_set_bytes{pod=~"(orders-batch)-.*",container!="",container!="POD"})`}},
		{name: "bağımlılık db: hedef adaylarıyla or, ms", det: "dependency_latency", code: "db|orders", ok: true, unit: "ms",
			contains: []string{`db_client_operation_duration_seconds_bucket{db_namespace="orders",service_name="orders-batch"}`, " or ", "* 1000"}},
		{name: "aile/dedektör uyuşmaz", det: "metric_jvm", code: "metric_k8s|cpu", ok: false},
		{name: "bilinmeyen ifade", det: "metric_jvm", code: "metric_jvm|nope", ok: false},
		{name: "bilinmeyen bağımlılık ailesi", det: "dependency_latency", code: "grpc|x", ok: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, ok := BuildSourceQuery(cfg, c.det, c.code, "orders-batch", "")
			if ok != c.ok {
				t.Fatalf("ok=%v, istenen %v (%+v)", ok, c.ok, q)
			}
			if !ok {
				return
			}
			if q.Cluster != c.cluster || q.Unit != c.unit || q.By != c.by {
				t.Fatalf("cluster/unit/by = %q/%q/%q", q.Cluster, q.Unit, q.By)
			}
			for _, s := range c.contains {
				if !strings.Contains(q.Expr, s) {
					t.Fatalf("ifade %q içermiyor:\n%s", s, q.Expr)
				}
			}
		})
	}
	if _, ok := BuildSourceQuery(cfg, "metric_jvm", "metric_jvm|heap_used", " ", ""); ok {
		t.Fatal("servissiz olay ifade kurmamalı")
	}
}
