package ml

// v0.10.2036 — detay grafiğinin tek sorgusu: dedektörün kendi ifadesi,
// servis kapsamlı (VM: servis etiketi; Thanos: servisin pod regex'i; bağımlılık:
// hedefin p95'i). Fikstür nötr.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/anomaly"
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

// v0.10.2055 — trace_pod_outlier detay grafiği: ölçü başına pod kırılımlı
// sorgu (eski: kayıtlı 5 dakikalık görüntü). Her ölçü + birim sınanır.
func TestTracePodSourceQuery(t *testing.T) {
	pin := anomaly.REDFamilyPin{ID: "http", Names: "http_server_request_duration_seconds", Unit: "s", ServerErr: "5..",
		StatusLabels: []string{"http_response_status_code"}}
	names := []string{"service_name", "k8s_pod_name", "http_response_status_code", "le"}
	l, ok := PickTracePodLabels(names, "", pin)
	if !ok || l.Svc != "service_name" || l.Pod != "k8s_pod_name" || l.Status != "http_response_status_code" {
		t.Fatalf("etiketler %+v ok=%v", l, ok)
	}
	if _, ok := PickTracePodLabels([]string{"service_name"}, "", pin); ok {
		t.Fatal("pod etiketi yokken ok olmamalı")
	}
	cases := []struct {
		measure, unit string
		ok            bool
		contains      []string
	}{
		{"err_ratio", "percent", true, []string{`100 * sum by (k8s_pod_name)`, `http_response_status_code=~"5.."`, `service_name="orders-api"`, "[120s]"}},
		{"p95", "ms", true, []string{`histogram_quantile(0.95, sum by (k8s_pod_name, le)`, `http_server_request_duration_seconds_bucket{service_name="orders-api"}`, "* 1000"}},
		{"count", "", false, nil},
	}
	for _, c := range cases {
		q, ok := TracePodSourceQuery(pin, l, "orders-api", c.measure)
		if ok != c.ok {
			t.Fatalf("%s: ok=%v", c.measure, ok)
		}
		if !ok {
			continue
		}
		if q.Unit != c.unit || q.By != "k8s_pod_name" || q.Cluster != "" {
			t.Fatalf("%s: unit/by/cluster = %q/%q/%q", c.measure, q.Unit, q.By, q.Cluster)
		}
		for _, s := range c.contains {
			if !strings.Contains(q.Expr, s) {
				t.Fatalf("%s: %q yok:\n%s", c.measure, s, q.Expr)
			}
		}
	}
	msPin := pin
	msPin.Unit = "ms"
	if q, _ := TracePodSourceQuery(msPin, l, "orders-api", "p95"); !strings.HasSuffix(q.Expr, "* 1") {
		t.Fatalf("ms ailesinde ölçek 1 olmalı: %s", q.Expr)
	}
	if _, ok := TracePodSourceQuery(pin, l, " ", "p95"); ok {
		t.Fatal("servissiz sorgu kurulmamalı")
	}
}
