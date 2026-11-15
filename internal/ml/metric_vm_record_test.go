package ml

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2220 (H4) — VM metrik olay kaydı: sözleşme gidiş-dönüşü,
// doğrulayıcıdan geçiş (açık + kapalı, yukarı + aşağı), kalan örnek, ifade.

func metricTestInput(closed bool, dir string, vals func(min int) float64) MetricRecordInput {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var pts []chstore.SpanMetricPoint
	for m := 0; m <= 60; m++ {
		pts = append(pts, chstore.SpanMetricPoint{Time: from.Add(time.Duration(m) * time.Minute).UnixNano(), Value: vals(m)})
	}
	e := chstore.MLMetricExpr{ID: "threads", Label: "Thread sayısı", Expr: "avg by ({svc}) (jvm_thread_count)", Direction: dir}
	return MetricRecordInput{Service: "orders-api", Family: "metric_jvm", Expr: e,
		Query: `avg by (service_name) (jvm_thread_count{service_name="orders-api"})`, Direction: dir, BucketSec: 300,
		CriticalZ: 3, Median: 100, MAD: 10, DetectedAt: from.Add(45 * time.Minute), Closed: closed,
		Points: pts, From: from, To: from.Add(time.Hour), Confirmers: []string{"op_latency:abc"}}
}

func TestMetricRecordContractRoundTrip(t *testing.T) {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, dir := range []string{"up", "down"} {
		for _, closed := range []bool{false, true} {
			hit, peakV := 400.0, 900.0
			if dir == "down" {
				hit, peakV = 20, 5
			}
			rec, ok := BuildMetricRecord(metricTestInput(closed, dir, func(m int) float64 {
				switch {
				case m == 50:
					return peakV
				case m >= 40 && m < 55:
					return hit
				}
				return 100
			}))
			if !ok {
				t.Fatal("kayıt kurulmalı")
			}
			if vs := ValidateRecord(rec); len(vs) != 0 {
				t.Fatalf("%s closed=%v doğrulayıcıdan geçmeli: %+v", dir, closed, vs)
			}
			if rec.OnsetAt != from.Add(40*time.Minute).UnixNano() || rec.PeakValue != peakV || rec.ValueAtDetection != hit || rec.Direction != dir {
				t.Fatalf("%s seriden türetme: onset=%d peak=%v det=%v", dir, rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
			}
			if closed != (rec.EndedAt == from.Add(55*time.Minute).UnixNano()) || !strings.HasPrefix(rec.Title, "Thread sayısı tespit anında") {
				t.Fatalf("%s closed=%v ended=%d title=%q", dir, closed, rec.EndedAt, rec.Title)
			}
			if rec.Volume != nil || rec.VolumeReason == "" || rec.Series.Aggregation != "avg" || rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 {
				t.Fatalf("hacim / toplama / sınıf / kapı: %+v", rec)
			}
			if len(rec.Evidence) != 2 || rec.Evidence[0].Independent || !rec.Evidence[1].Independent {
				t.Fatalf("kanıt bağımsızlığı: %+v", rec.Evidence)
			}
			raw, _ := json.Marshal(rec)
			var back EventRecord
			if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
				t.Fatalf("gidiş-dönüş: %v", err)
			}
		}
	}
}

// Kalan örnek: 1 dk seri bandı hiç aşmıyor → publish_state=invalid.
func TestMetricRecordInvalidWhenSeriesNeverLeavesBand(t *testing.T) {
	rec, ok := BuildMetricRecord(metricTestInput(false, "up", func(int) float64 { return 100 }))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	raw, _ := json.Marshal(rec)
	if ev := applyRecordContract(chstore.MLEvent{ID: "x", Detector: "metric_jvm", Record: string(raw), PublishState: PublishPublished}); ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}

// İfade: tik ifadesi + servis eşleyicisi; Thanos olayı ve consumer lag (H2) dışarıda.
func TestMetricRecordExpr(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	ev := chstore.MLEvent{Detector: "metric_jvm", Service: "orders-api", Code: metricPart("metric_jvm", "gc_time")}
	_, q, ok := metricRecordExpr(cfg, ev, "service_name")
	if !ok || !strings.Contains(q, `service_name="orders-api"`) || !strings.Contains(q, "[300s]") {
		t.Fatalf("%v %q", ok, q)
	}
	ev.Code += "@c1"
	if _, _, ok := metricRecordExpr(cfg, ev, "service_name"); ok {
		t.Fatal("Thanos olayı H4 dışı")
	}
	lag := chstore.MLEvent{Detector: "metric_kafka", Service: "s", Code: metricPart("metric_kafka", kafkaLagExprID)}
	if _, _, ok := metricRecordExpr(cfg, lag, "service_name"); ok {
		t.Fatal("consumer lag H2'nin")
	}
	if titleSafe("HTTP client 5xx oranı") || !titleSafe("HTTP client p95") {
		t.Fatal("başlık etiketi sayı kuralı")
	}
}
