package api

import (
	"context"
	"testing"
	"time"
)

// v0.10.1704 — v0.10.1689'un ikinci yarısı (prod: kapsamsız VM etiket keşfi
// /api/v1/labels match[]={__name__=~…} filonun 24 sa'ini tarıyor → "context
// deadline exceeded"). 1689 yalnız readServiceHTTPRED'i düzeltmişti; aynı
// kapsamsız okuma metric_chart (copilot_metric_chart.go) ve JVM yoklamasında
// (resource_jvm.go) kalmıştı. Beklenen: servis biliniyorsa keşif servise
// kapsanır; yalnız gerçekten filo geneli hedefler kapsamsız okur.

func TestResJVMLabelService(t *testing.T) {
	cases := []struct {
		kind, target, svc, want string
	}{
		{resKindService, "orders-api", "orders-api", "orders-api"},
		{resKindService, "orders-api", "", "orders-api"},
		{"", "payments-core", "", "payments-core"},
		{resKindPod, "orders-api-6d4f8b9c7d-x2k9q", "orders-api", "orders-api"},
		{resKindPod, "orders-api-6d4f8b9c7d-x2k9q", "", ""},
		{resKindWorkload, "orders-api", "orders-api", ""},
	}
	for _, c := range cases {
		if got := resJVMLabelService(riRoute(c.kind, c.target, c.svc, resFamJVM)); got != c.want {
			t.Errorf("%s/%s/%s: %q, beklenen %q", c.kind, c.target, c.svc, got, c.want)
		}
	}
}

func TestMetricChartLabelReadIsServiceScoped(t *testing.T) {
	to := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	labels := map[string][]string{
		"jvm.memory.used":         {"service_name", "k8s_pod_name", "jvm_memory_type"},
		"kafka_consumergroup_lag": {"consumergroup", "topic"},
	}
	// Servisli şablon → kapsamlı okuma, kapsamsız yok.
	rd := &fakeChartReader{labels: labels}
	route := &guidedRoute{Intent: guidedMetricChart, Service: "orders-api", ChartTemplate: mcJVMHeap}
	if _, _, err := (&Server{}).metricChartRun(context.Background(), (&mcEvents{}).emit(), route, rd, true, from, to, 3600, time.UTC); err != nil {
		t.Fatal(err)
	}
	if rd.unscopedCalls != 0 || len(rd.scopedLabelCalls) == 0 || rd.scopedLabelCalls[0] != "service_name=orders-api" {
		t.Errorf("servisli grafik kapsamlı okumalı: unscoped=%d scoped=%v", rd.unscopedCalls, rd.scopedLabelCalls)
	}
	// Kafka lag: servisli varyant kapsamlı, Fleet varyantı (servis etiketi yok)
	// filo geneli — kapsamsız okuma yalnız onun için.
	rd = &fakeChartReader{labels: labels}
	route = &guidedRoute{Intent: guidedMetricChart, Service: "orders-api", ChartTemplate: mcKafkaLag}
	_, _, _ = (&Server{}).metricChartRun(context.Background(), (&mcEvents{}).emit(), route, rd, true, from, to, 3600, time.UTC)
	if rd.unscopedCalls != 1 || len(rd.scopedLabelCalls) != 1 {
		t.Errorf("Fleet varyantı filo geneli okumalı: unscoped=%d scoped=%v", rd.unscopedCalls, rd.scopedLabelCalls)
	}
}

func TestResourceJVMLabelReadIsServiceScoped(t *testing.T) {
	vm := &fakeJVMReader{fakeChartReader: &fakeChartReader{labels: riSemconvLabels}, answer: riSemconvAnswer}
	route := riRoute(resKindService, "orders-api", "orders-api", resFamJVM)
	if _, _, err := (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), route, resourceDeps{VM: vm, IsVM: true}, riTo, 3600, time.UTC); err != nil {
		t.Fatal(err)
	}
	if vm.unscopedCalls != 0 || len(vm.scopedLabelCalls) == 0 {
		t.Errorf("JVM yoklaması servise kapsanmalı: unscoped=%d scoped=%v", vm.unscopedCalls, vm.scopedLabelCalls)
	}
	for _, c := range vm.scopedLabelCalls {
		if c != "service_name=orders-api" {
			t.Errorf("kapsam: %s", c)
		}
	}
	if len(vm.queries()) != 7 {
		t.Errorf("JVM zincirleri yine koşmalı: %d sorgu", len(vm.queries()))
	}
}
