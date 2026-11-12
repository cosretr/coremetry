package ml

// v0.10.2044 — cümle ile grafik aynı ölçüyü söylesin (operatör: cümle 1,34
// bin derken grafik 2,8 bin gösteriyordu). Sunucu cümlesi ölçüyü ADLANDIRIR
// ("5 dakikalık ortalama" / "5 dakikalık ölçüm"); değer parçası kartta
// (Value) ki detay panel tepesiyle yeniden yazabilsin; dedektörün değeri
// teknik ayrıntıda (Measured). Fikstür nötr.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMetricValueSpan(t *testing.T) {
	cfg := chstore.MLConfig{MetricFamilies: chstore.DefaultMLMetricFamilies()}
	if MetricWindowedFor(cfg, "metric_kafka", "metric_kafka|consumer_lag") {
		t.Fatal("lag gauge: pencereli değil")
	}
	if !MetricWindowedFor(cfg, "metric_jvm", "metric_jvm|gc_time@prod-east") {
		t.Fatal("GC rate: pencereli")
	}
	st := time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)
	in := CardInput{Detector: "metric_kafka", Signal: chstore.MLSignalMetric, Service: "orders-consumer", Part: "metric_kafka|consumer_lag",
		MetricLabel: "Consumer lag", Observed: 1340, Baseline: 120, StartedAt: st, LastSeen: st.Add(10 * time.Minute), BucketSec: 300,
		Direction: "up", Now: st.Add(10 * time.Minute), Loc: time.UTC}
	c := BuildExplainCard(in)
	if c.Value == nil || c.Value.Text != "1.340'a çıktı (5 dakikalık ölçüm)" || c.Value.Measured != "1.340 (5 dakikalık ölçüm)" {
		t.Fatalf("değer parçası: %+v", c.Value)
	}
	if !strings.Contains(c.Summary, c.Value.Text) || c.Technical.Measured != c.Value.Measured {
		t.Fatalf("cümle parçayı birebir taşımalı: %q", c.Summary)
	}
	in.MetricWindowed, in.Observed, in.MetricUnit = true, 0.9, "s/s"
	if v := metricValue(in, specFor(in)); v.Text != "0,9 sn/sn seviyesine çıktı (5 dakikalık ortalama)" || v.Unit != "s/s" {
		t.Fatalf("pencereli: %+v", v)
	}
	// Bağlantılar olay türüne göre: Kafka → Messaging + servis (trace yok).
	var labels []string
	for _, l := range c.WhereToLook {
		labels = append(labels, l.Label)
	}
	if strings.Join(labels, "|") != "Messaging sayfası|Servis sayfası" {
		t.Fatalf("Kafka bağlantıları: %v", labels)
	}
	jvm := in
	jvm.Detector, jvm.Part, jvm.Code = "metric_jvm", "metric_jvm|heap_used", "metric_jvm|heap_used"
	if w := BuildExplainCard(jvm).WhereToLook; len(w) != 2 || w[0].Label != "Altyapı (pod'lar)" {
		t.Fatalf("JVM bağlantıları: %+v", w)
	}
	// Log hacmi cümlesi değer + normal söyler (oran değil).
	lv := CardInput{Detector: "log_volume", Signal: chstore.MLSignalLog, Service: "orders-api", Observed: 4400, Baseline: 200, BucketSec: 300,
		Direction: "up", StartedAt: st, LastSeen: st.Add(10 * time.Minute), Now: st.Add(10 * time.Minute), Loc: time.UTC}
	if s := ExplainSummary(lv); !strings.HasPrefix(s, "orders-api: log hacmi dakikada ~880 satıra çıktı (normalde ~40)") {
		t.Fatalf("log hacmi: %s", s)
	}
	log := BuildExplainCard(CardInput{Detector: "log_volume", Signal: chstore.MLSignalLog, Service: "orders-api", StartedAt: st, LastSeen: st, Now: st, Loc: time.UTC})
	if log.Value != nil || log.Technical.Measured != "" {
		t.Fatal("metrik dışı olayda değer parçası yok")
	}
}
