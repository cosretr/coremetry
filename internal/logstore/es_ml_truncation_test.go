package logstore

// v0.10.1812 — ML log terms tavanı (MaxServices) dışında kalan servisler
// ölçülür (audit §13 D3). Önceki semptom: aşan servisler doc_count sırasıyla
// SESSİZ düşüyordu; tik "500 servis" derken filo 2000 servis olabilirdi.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMLDroppedServices(t *testing.T) {
	cases := []struct {
		name      string
		returned  int
		sumOther  int64
		distinct  int64
		counted   bool
		wantDrops int
	}{
		{"kesilme yok", 120, 0, 120, true, 0},
		{"kesilme yok, HLL fazla saydı", 120, 0, 123, true, 0},
		{"tavan dolu, 17 dışarıda", 500, 9000, 517, true, 17},
		{"kesilme var, sayım yok → en az 1", 500, 9000, 0, false, 1},
		{"kesilme var, HLL eksik saydı → en az 1", 500, 10, 499, true, 1},
	}
	for _, c := range cases {
		if got := MLDroppedServices(c.returned, c.sumOther, c.distinct, c.counted); got != c.wantDrops {
			t.Errorf("%s: got %d want %d", c.name, got, c.wantDrops)
		}
	}
}

func TestMLCardinalityPrecision(t *testing.T) {
	for in, want := range map[int]int{10: 1000, 500: 1000, 5000: 10000, 30000: 40000} {
		if got := mlCardinalityPrecision(in); got != want {
			t.Errorf("%d: got %d want %d", in, got, want)
		}
	}
}

func TestMLAggBodyCardinalityOnlyWhenAsked(t *testing.T) {
	req := MLLogAggRequest{From: time.Unix(0, 0), To: time.Unix(600, 0), BucketSec: 300, MaxServices: 500}
	if strings.Contains(string(mlLogAggBody("@timestamp", "service.name", "level", req, "10s")), "svc_n") {
		t.Fatal("backfill isteğinde kardinalite yok")
	}
	req.CountServices = true
	b := string(mlLogAggBody("@timestamp", "service.name", "level", req, "10s"))
	if !strings.Contains(b, `"svc_n":{"cardinality":{"field":"service.name","precision_threshold":1000}}`) {
		t.Fatalf("tik isteğinde kardinalite: %s", b)
	}
	br := MLBreakdownRequest{From: time.Unix(0, 0), To: time.Unix(600, 0), MaxServices: 500, ErrorLevels: []string{"error"}, CountServices: true}
	if !strings.Contains(string(mlBreakdownBody("@timestamp", "service.name", "level", br, "10s")), `"svc_n"`) {
		t.Fatal("kırılım isteğinde kardinalite")
	}
}

func TestParseMLAggDropped(t *testing.T) {
	aggs := json.RawMessage(`{"svc":{"sum_other_doc_count":4200,"buckets":[{"key":"orders-api","doc_count":10},{"key":"branch-teller","doc_count":5}]},"svc_n":{"value":19},"latest":{"value":null}}`)
	r, err := parseMLLogAgg(aggs)
	if err != nil || r.DroppedServices != 17 {
		t.Fatalf("log RED: %+v %v", r, err)
	}
	r2, err := parseMLLogAgg(json.RawMessage(`{"svc":{"sum_other_doc_count":0,"buckets":[{"key":"orders-api","doc_count":10}]},"latest":{}}`))
	if err != nil || r2.DroppedServices != 0 {
		t.Fatalf("kesilmesiz: %+v %v", r2, err)
	}
	b, err := parseMLBreakdown(json.RawMessage(`{"svc":{"sum_other_doc_count":7,"buckets":[{"key":"orders-api"}]}}`))
	if err != nil || b.DroppedServices != 1 {
		t.Fatalf("kırılım, sayımsız: %+v %v", b, err)
	}
}
