package anomaly

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestWeightSeriesAndRates(t *testing.T) {
	m := chstore.SamplingShadowModel{ErrWeight: 1, OkWeight: 20}
	if weightSeries(m, nil) != nil {
		t.Fatal("nil stays nil")
	}
	got := weightSeries(m, []float64{0, 100})
	if got[0] != 0 || got[1] != 100 {
		t.Fatalf("series %v", got)
	}
	r := weightRates(m, []float64{2, 2}, []float64{0, 100})
	if r[0] != 40 || r[1] != 2 {
		t.Fatalf("rates %v", r)
	}
	if mis := weightRates(m, []float64{1}, []float64{0, 0}); len(mis) != 1 || mis[0] != 1 {
		t.Fatalf("misaligned %v", mis)
	}
}

// A stored error-rate spike 0.5 % → 6 % opens today; weighted it is
// ~0.3 %, under the 1 % AbsFloor — the shadow says it would not open.
func TestShadowAnomalyErrorRateAbsFloor(t *testing.T) {
	sens := chstore.DefaultAnomalySensitivity()
	m := chstore.SamplingShadowModel{ErrWeight: 1, OkWeight: 20}
	n := 60
	buckets := make([]float64, n)
	rates := make([]float64, n)
	for i := range buckets {
		buckets[i] = 0.5 + 0.05*float64(i%3)
		rates[i] = 50
	}
	for i := n - sens.DwellBuckets; i < n; i++ {
		buckets[i] = 6
	}
	today := evaluateAnomaly("error_rate", buckets, nil, rates, 24, false, batchLatSeries{}, sens)
	if today.Action != "open" {
		t.Fatalf("fixture must open today, got %q", today.Action)
	}
	o, ok := shadowAnomalyErrorRate(m, true, "error_rate", "svc-a", false, buckets, nil, rates, 24, false, batchLatSeries{}, sens, today)
	if !ok || !o.TodayFires || o.WeightedFires {
		t.Fatalf("obs %+v ok=%v", o, ok)
	}
	if _, ok := shadowAnomalyErrorRate(m, true, "p99_ms", "svc-a", false, buckets, nil, rates, 24, false, batchLatSeries{}, sens, today); ok {
		t.Fatal("only error_rate")
	}
	if _, ok := shadowAnomalyErrorRate(m, true, "error_rate", "svc-a", true, buckets, nil, rates, 24, false, batchLatSeries{}, sens, today); ok {
		t.Fatal("VM series are unsampled")
	}
}
