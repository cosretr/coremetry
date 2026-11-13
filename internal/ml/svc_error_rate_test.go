package ml

// svc_error_rate_test.go — v0.10.2077 (spec-ml-service-level dilim 1):
// servis düzeyi hata oranı kararı + toplayıcı + cümle.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func repeat(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestSvcErrorRateVerdict(t *testing.T) {
	base := repeat(0.004, 30)  // %0,4
	rate := repeat(5, 30)      // 5 istek/sn
	lowRate := repeat(0.1, 30) // 0,1 istek/sn
	cases := []struct {
		name      string
		cur       []float64
		baseRatio []float64
		baseCount []float64
		fire      bool
	}{
		{"3 kova ≥2× ve ≥%1 → açar", []float64{0.02, 0.03, 0.03}, base, rate, true},
		{"2 kova (10 dk) → açmaz", []float64{0.03, 0.03}, base, rate, false},
		{"bir kova eşik altı → açmaz", []float64{0.03, 0.005, 0.03}, base, rate, false},
		{"%1 tabanı: 2× ama %0,9 → açmaz", []float64{0.009, 0.009, 0.009}, base, rate, false},
		{"taban 0: %1 ve üstü → açar", []float64{0.012, 0.012, 0.012}, repeat(0, 30), rate, true},
		{"taban 0: %0,5 → açmaz", []float64{0.005, 0.005, 0.005}, repeat(0, 30), rate, false},
		{"taban trafiği 0,2 istek/sn altı → açmaz", []float64{0.5, 0.5, 0.5}, base, lowRate, false},
		{"taban örneği az → açmaz", []float64{0.5, 0.5, 0.5}, base[:5], rate, false},
		{"tam 2× ve %1 sınırı → açar", []float64{0.01, 0.01, 0.01}, repeat(0.005, 30), rate, true},
		{"2× altı (1,9×) → açmaz", []float64{0.019, 0.019, 0.019}, repeat(0.01, 30), rate, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := svcErrorRateVerdict(c.cur, c.baseRatio, c.baseCount, 20)
			if v.Fire != c.fire {
				t.Fatalf("fire=%v want %v (%s)", v.Fire, c.fire, v.Reason)
			}
		})
	}
}

// -batch servis MUAF DEĞİL: batch kuralı hata oranını yük saymaz.
func TestSvcErrorRateBatchNotExempt(t *testing.T) {
	ev := chstore.MLEvent{Detector: detSvcErrorRate, Signal: chstore.MLSignalTrace, Service: "orders-batch"}
	isBatch := func(s string) bool { return strings.HasSuffix(s, "-batch") }
	if !batchKeep(ev, isBatch, nil) {
		t.Fatal("svc_error_rate -batch serviste kesilmemeli")
	}
}

func TestSvcErrScore(t *testing.T) {
	if svcErrScore(1.2, 4) != 4 || svcErrScore(7, 4) != 7 {
		t.Fatal("skor en az CriticalZ, büyükse gerçek z")
	}
}

// Aileler TOPLANMAZ: kova başına servisin ilk ailesi; route boş satır dahil.
func TestSvcAggEmit(t *testing.T) {
	a := newSvcAgg()
	a.errRan(0)
	a.errRan(1)
	// HTTP (0): iki route + route'suz satır
	a.add(0, "orders-api", "count", 100, 8)
	a.add(0, "orders-api", "count", 100, 2)
	a.add(0, "orders-api", "err", 100, 0.5)
	// gRPC (1) aynı servis: HTTP varken kullanılmaz
	a.add(1, "orders-api", "count", 100, 1000)
	a.add(1, "orders-api", "err", 100, 900)
	// yalnız gRPC servis
	a.add(1, "pay-grpc", "count", 100, 4)
	// err sorgusu koşmamış aile (2): oran yazılmaz
	a.add(2, "only-stable", "count", 100, 3)
	a.add(0, "", "count", 100, 3)             // servissiz satır yok sayılır
	a.add(0, "orders-api", "p95", 100, 12345) // ölçü dışı
	s := map[string]map[int64]float64{}
	a.emit(s)
	get := func(svc, m string) (float64, bool) {
		v, ok := s[chstore.MLSeriesKey(svc, tracePart(svcRoute, m))][100]
		return v, ok
	}
	if c, _ := get("orders-api", svcMeasureCount); c != 10 {
		t.Fatalf("count=%v want 10 (HTTP, route'lar toplanır)", c)
	}
	if r, _ := get("orders-api", svcMeasureRatio); r != 0.05 {
		t.Fatalf("ratio=%v want 0.05", r)
	}
	if r, ok := get("pay-grpc", svcMeasureRatio); !ok || r != 0 {
		t.Fatalf("pay-grpc ratio=%v ok=%v want 0", r, ok)
	}
	if _, ok := get("only-stable", svcMeasureRatio); ok {
		t.Fatal("err sorgusu koşmayan ailede oran yazılmamalı")
	}
	if len(s) != 5 {
		t.Fatalf("seri sayısı %d want 5: %v", len(s), s)
	}
}

func TestSvcErrHead(t *testing.T) {
	in := CardInput{Detector: detSvcErrorRate, Signal: chstore.MLSignalTrace, Service: "orders-api", Observed: 0.032, Baseline: 0.004,
		Direction: "up", StartedAt: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC), LastSeen: time.Date(2026, 11, 13, 9, 15, 0, 0, time.UTC),
		Now: time.Date(2026, 11, 13, 9, 16, 0, 0, time.UTC), Loc: time.UTC, BucketSec: 300}
	got := ExplainSummary(in)
	if !strings.HasPrefix(got, "orders-api: servis hata oranı normalde %0,4, şimdi %3,2") {
		t.Fatalf("cümle: %s", got)
	}
	in.Baseline = 0
	if got := ExplainSummary(in); !strings.Contains(got, "normalde neredeyse hiç yok, şimdi %3,2") {
		t.Fatalf("taban 0 cümlesi: %s", got)
	}
}

func TestSvcErrorEventPart(t *testing.T) {
	ev := chstore.MLEvent{Detector: detSvcErrorRate, Signal: chstore.MLSignalTrace, Service: "orders-api"}
	if p := eventPart(ev); p != "*|svc_err_ratio" {
		t.Fatalf("part=%s", p)
	}
	if chstore.MLDetectorSignal(detSvcErrorRate) != chstore.MLSignalTrace || !chstore.MLDetectorDefaultOn(detSvcErrorRate) {
		t.Fatal("svc_error_rate trace sinyali ve varsayılan açık olmalı")
	}
}

// TraceRED tiki: servis serisi route döngüsüne girmez (route "*" olayı yok).
func TestSvcSeriesNotARoute(t *testing.T) {
	if _, m := splitTracePart(tracePart(svcRoute, svcMeasureCount)); m == "count" {
		t.Fatal("servis count serisi route 'count' ölçüsüyle çakışmamalı")
	}
}
