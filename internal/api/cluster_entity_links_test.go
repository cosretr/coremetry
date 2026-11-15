package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// v0.10.2192 — entity kenarları yalnız boşluğu doldurur; Thanos değeri ezilmez.
func TestFillPodsFromEdges(t *testing.T) {
	rows := []thanos.PodRow{
		{Pod: "bsa-digital-dashboard-prod-7d9f8c6b5-abcde"},
		{Pod: "kept", Node: "ksm-node", Service: "ksm-svc"},
		{Pod: "no-edge"},
	}
	edges := map[string]entity.PodEdges{
		"bsa-digital-dashboard-prod-7d9f8c6b5-abcde": {Service: "bsa-digital-dashboard-prod", Node: "ocpma-worker-3"},
		"kept": {Service: "span-svc", Node: "span-node"},
	}
	fillPodsFromEdges(rows, edges)
	if rows[0].Service != "bsa-digital-dashboard-prod" || rows[0].Node != "ocpma-worker-3" {
		t.Errorf("gap not filled: %+v", rows[0])
	}
	if rows[1].Service != "ksm-svc" || rows[1].Node != "ksm-node" {
		t.Errorf("thanos value overwritten: %+v", rows[1])
	}
	if rows[2].Service != "" || rows[2].Node != "" {
		t.Errorf("unknown must stay empty: %+v", rows[2])
	}
}
