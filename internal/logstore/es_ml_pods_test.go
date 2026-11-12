package logstore

// v0.10.2045 — katalogda olmayan log servis adlarının pod'ları: TEK ek sorgu,
// size 0, servis başına en çok 3 pod. Fikstür nötr.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMLServicePodsBodyAndParse(t *testing.T) {
	from := time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)
	b := string(mlServicePodsBody("@timestamp", "service.name", "k8s.pod.name", []string{"Api", "Worker"}, from, from.Add(10*time.Minute), 3, "10s"))
	for _, want := range []string{`"size":0`, `"terms":{"service.name":["Api","Worker"]}`, `"pod":{"terms":{"field":"k8s.pod.name","size":3}}`, `"timeout":"10s"`, `"gte":"2026-11-12T16:00:00Z"`} {
		if !strings.Contains(b, want) {
			t.Fatalf("gövde %q içermiyor: %s", want, b)
		}
	}
	got, err := parseMLServicePods(json.RawMessage(`{"svc":{"buckets":[{"key":"Api","pod":{"buckets":[{"key":"orders-api-7d9f6c-x2k4p"},{"key":"orders-api-7d9f6c-q8w2z"}]}}]}}`))
	if err != nil || len(got["Api"]) != 2 || got["Api"][0] != "orders-api-7d9f6c-x2k4p" {
		t.Fatalf("%v %v", got, err)
	}
}
