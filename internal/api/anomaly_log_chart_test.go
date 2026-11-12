package api

// v0.10.2035 — ML log olayının canlı histogramı: pencere/kova sunucuda
// sabitlenir (≤ 3 sa → 1 dk; uzun pencerede kova büyür, ≤ 180 kova; ≤ 7 gün)
// ve önbellek anahtarı her girdiyi taşır (v0.5.187 çapraz zehirlenme sınıfı).

import (
	"testing"
	"time"
)

func TestSnapAnomalyLogChartWindow(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		from, to   time.Time
		wantBucket int
		wantFrom   time.Time
		wantTo     time.Time
		wantOK     bool
	}{
		{"45 dk → 1 dk kova, uçlar hizalı", now.Add(-45*time.Minute - 20*time.Second), now.Add(-10 * time.Second), 60,
			now.Add(-46 * time.Minute), now, true},
		{"tam 3 sa → hâlâ 1 dk", now.Add(-3 * time.Hour), now, 60, now.Add(-3 * time.Hour), now, true},
		{"3 sa 1 dk → 2 dk kova", now.Add(-3*time.Hour - time.Minute), now, 120, now.Add(-3*time.Hour - 2*time.Minute), now, true},
		{"12 sa → 5 dk kova", now.Add(-12 * time.Hour), now, 300, now.Add(-12 * time.Hour), now, true},
		{"2 gün → 30 dk kova", now.Add(-48 * time.Hour), now, 1800, now.Add(-48 * time.Hour), now, true},
		{"30 gün → 7 güne kırpılır, 1 sa kova", now.Add(-30 * 24 * time.Hour), now, 3600, now.Add(-7 * 24 * time.Hour), now, true},
		{"to gelecekte → now", now.Add(-time.Hour), now.Add(time.Hour), 60, now.Add(-time.Hour), now, true},
		{"from boş", time.Time{}, now, 0, time.Time{}, time.Time{}, false},
		{"ters pencere", now, now.Add(-time.Hour), 0, time.Time{}, time.Time{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, to, b, ok := snapAnomalyLogChartWindow(c.from, c.to, now)
			if ok != c.wantOK || b != c.wantBucket {
				t.Fatalf("ok=%v b=%d, istenen ok=%v b=%d", ok, b, c.wantOK, c.wantBucket)
			}
			if ok && (!f.Equal(c.wantFrom) || !to.Equal(c.wantTo)) {
				t.Fatalf("pencere %v–%v, istenen %v–%v", f, to, c.wantFrom, c.wantTo)
			}
			if ok && to.Sub(f)/(time.Duration(b)*time.Second) > anomalyLogChartMaxBuckets {
				t.Fatalf("kova sayısı tavanı aşıldı")
			}
		})
	}
}

func TestAnomalyLogChartKeyCarriesAllInputs(t *testing.T) {
	a := time.Unix(1_800_000_000, 0)
	b := a.Add(time.Hour)
	base := anomalyLogChartKey("orders-batch", `"payment failed"`, 0, a, b, 60)
	diff := []string{
		anomalyLogChartKey("orders-consumer", `"payment failed"`, 0, a, b, 60),
		anomalyLogChartKey("orders-batch", `"payment retried"`, 0, a, b, 60),
		anomalyLogChartKey("orders-batch", `"payment failed"`, 17, a, b, 60),
		anomalyLogChartKey("orders-batch", `"payment failed"`, 0, a.Add(time.Minute), b, 60),
		anomalyLogChartKey("orders-batch", `"payment failed"`, 0, a, b.Add(time.Minute), 60),
		anomalyLogChartKey("orders-batch", `"payment failed"`, 0, a, b, 120),
		// Ayırıcı: "a|b"+"c" ≠ "a"+"b|c".
		anomalyLogChartKey("orders-batch\"payment", ` failed"`, 0, a, b, 60),
	}
	for i, k := range diff {
		if k == base {
			t.Fatalf("girdi %d anahtarı değiştirmedi: %s", i, k)
		}
	}
}
