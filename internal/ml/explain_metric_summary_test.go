package ml

// v0.10.2032 — metrik özeti ölçülen değeri ve normalini birimiyle söyler
// (operatör: "Consumer lag normalde hiç görülmez, 12 dakikadır" değeri
// söylemiyordu). Fikstür nötr.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMetricSummary(t *testing.T) {
	st := time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)
	base := CardInput{Detector: "metric_kafka", Signal: chstore.MLSignalMetric, Service: "orders-consumer", Part: "metric_kafka|consumer_lag",
		MetricLabel: "Consumer lag", StartedAt: st, LastSeen: st.Add(12 * time.Minute), BucketSec: 300, Direction: "up", Now: st.Add(12 * time.Minute)}
	cases := []struct {
		name string
		mod  func(*CardInput)
		want string
	}{
		{"sıfır taban, sürüyor", func(in *CardInput) { in.Observed, in.Baseline = 162, 0 },
			"orders-consumer: Consumer lag 162'ye çıktı; normalde hiç görülmez. 16:00'dan beri sürüyor (12 dakika)."},
		{"tabanlı, bitti", func(in *CardInput) {
			in.Observed, in.Baseline, in.Stage, in.Now = 1340, 120, chstore.MLStageClosed, st.Add(3*time.Hour)
		}, "orders-consumer: Consumer lag 1.340'a çıktı; normalde yaklaşık 120. 16:00'da başladı, 16:12'de bitti (12 dakika sürdü)."},
		{"birimli düşüş", func(in *CardInput) {
			in.MetricLabel, in.MetricUnit, in.Part, in.Detector = "Bellek (working set)", "bytes", "metric_k8s|memory", "metric_k8s"
			in.Observed, in.Baseline, in.Direction = 512e6, 2e9, "down"
		}, "orders-consumer: Bellek (working set) 488,3 MB seviyesine düştü; normalde yaklaşık 1.907,3 MB. 16:00'dan beri sürüyor (12 dakika)."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			c.mod(&in)
			if got := ExplainSummary(in); got != c.want {
				t.Fatalf("özet:\n got %q\nwant %q", got, c.want)
			}
		})
	}
}
