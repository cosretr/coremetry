package vmetrics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// v0.10.2261 (Entity S6 parça 2) — sorgu şekli: tek metrik, topk tavanı,
// eşlemenin etiketleri; pod etiketi yoksa sorgu yok.
func TestEntityPairQueries(t *testing.T) {
	if _, _, _, ok := EntityPairQueries(LabelMap{Service: "service_name"}, 15*time.Minute, 10); ok {
		t.Fatal("pod etiketi yokken sorgu üretilmemeli")
	}
	svc, podQ, hostQ, ok := EntityPairQueries(LabelMap{Pod: "k8s_pod_name", Namespace: "k8s_namespace_name"}, 15*time.Minute, 40000)
	if !ok || svc != "service_name" {
		t.Fatalf("servis konvansiyonu: %q ok=%v", svc, ok)
	}
	for _, want := range []string{"topk(40000,", "count by (service_name, k8s_pod_name, k8s_namespace_name)", `target_info{service_name!="", k8s_pod_name!=""}[900s]`} {
		if !strings.Contains(podQ, want) {
			t.Errorf("pod sorgusu %q içermiyor: %s", want, podQ)
		}
	}
	for _, want := range []string{"count by (service_name, host_name)", `k8s_pod_name=""`, `host_name!=""`} {
		if !strings.Contains(hostQ, want) {
			t.Errorf("host sorgusu %q içermiyor: %s", want, hostQ)
		}
	}
}

func TestDecodeEntityVector(t *testing.T) {
	rows, err := decodeEntityVector(json.RawMessage(`{"resultType":"vector","result":[{"metric":{"service_name":"checkout-api","k8s_pod_name":"checkout-api-0"},"value":[1,"1"]}]}`))
	if err != nil || len(rows) != 1 || rows[0]["k8s_pod_name"] != "checkout-api-0" {
		t.Fatalf("%v %v", rows, err)
	}
	if _, err := decodeEntityVector(json.RawMessage(`{"resultType":"matrix","result":[]}`)); err == nil {
		t.Fatal("matrix reddedilmeli")
	}
}
