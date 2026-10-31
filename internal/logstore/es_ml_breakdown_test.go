package logstore

// v0.10.1756 — ML log dilim 3 gövdesi: kod kolu hata filtresi altında, pod
// kolu toplam + hata; boş alan kolu yok; backfill'de pod kolu yok.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMLBreakdownBodyAndParse(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	req := MLBreakdownRequest{From: t0, To: t0.Add(10 * time.Minute), MaxServices: 10, ErrorLevels: []string{"error"}, CodeField: "error.code", PodField: "k8s.pod.name"}
	b := string(mlBreakdownBody("@timestamp", "svc", "level", req, "10s"))
	for _, want := range []string{`"field":"error.code"`, `"field":"k8s.pod.name"`, `"perr"`, `"track_total_hits":false`} {
		if !strings.Contains(b, want) {
			t.Errorf("gövdede %s yok", want)
		}
	}
	req.PodField, req.DailyCodes = "k8s.pod.name", true
	if b := string(mlBreakdownBody("@timestamp", "svc", "level", req, "10s")); strings.Contains(b, "k8s.pod.name") || !strings.Contains(b, `"fixed_interval":"1d"`) {
		t.Errorf("backfill: pod kolu yok, günlük histogram var: %s", b)
	}
	req.CodeField, req.DailyCodes = "", false
	if b := string(mlBreakdownBody("@timestamp", "svc", "level", req, "10s")); strings.Contains(b, `"err"`) {
		t.Error("kod alanı yokken kod kolu olmamalı")
	}
	raw := json.RawMessage(`{"svc":{"buckets":[{"key":"pay","err":{"code":{"buckets":[{"key":"ORA-00001","doc_count":3}]}},"pod":{"buckets":[{"key":"pay-1","doc_count":100,"perr":{"doc_count":40}}]}}]}}`)
	got, err := parseMLBreakdown(raw)
	if err != nil || len(got.Codes) != 1 || got.Codes[0].Code != "ORA-00001" || len(got.Pods) != 1 || got.Pods[0].Errors != 40 || got.Pods[0].Total != 100 {
		t.Fatalf("got %+v err %v", got, err)
	}
}
