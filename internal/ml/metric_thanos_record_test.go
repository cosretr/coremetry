package ml

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2224 (H8) — Thanos metrik olay kaydı: gidiş-dönüş, doğrulayıcıdan
// geçiş (açık + kapalı), kalan örnek, dondurulmuş pod ifadesi, birleşim.

func thanosTestInput(closed bool, vals func(min int) float64) MetricRecordInput {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	// iki pod; servis değeri max birleşimi
	var rows []ThanosRangeSeries
	for p := 0; p < 2; p++ {
		s := ThanosRangeSeries{Labels: map[string]string{"pod": "svc-a-" + string(rune('0'+p))}}
		for m := 0; m <= 60; m++ {
			v := vals(m)
			if p == 1 {
				v /= 2
			}
			s.Points = append(s.Points, chstore.SpanMetricPoint{Time: from.Add(time.Duration(m) * time.Minute).UnixNano(), Value: v})
		}
		rows = append(rows, s)
	}
	e := chstore.MLMetricExpr{ID: "threads", Label: "Thread sayısı", Expr: "avg by ({svc}) (jvm_thread_count)", Direction: "up"}
	return MetricRecordInput{Service: "svc-a", Family: "metric_jvm", Expr: e, Source: "thanos:cl-a", Agg: "max", Pods: 2,
		Query: `avg by (namespace, pod) (jvm_thread_count{pod=~"svc-a-0|svc-a-1",namespace=~"ns-a"})`, Direction: "up", BucketSec: 300,
		CriticalZ: 3, Median: 100, MAD: 10, DetectedAt: from.Add(45 * time.Minute), Closed: closed,
		Points: combineRange(rows, "max"), From: from, To: from.Add(time.Hour), Confirmers: []string{"op_latency:abc"}}
}

func TestThanosMetricRecordContractRoundTrip(t *testing.T) {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, closed := range []bool{false, true} {
		rec, ok := BuildMetricRecord(thanosTestInput(closed, func(m int) float64 {
			switch {
			case m == 50:
				return 900
			case m >= 40 && m < 55:
				return 400
			}
			return 100
		}))
		if !ok {
			t.Fatal("kayıt kurulmalı")
		}
		if vs := ValidateRecord(rec); len(vs) != 0 {
			t.Fatalf("closed=%v doğrulayıcıdan geçmeli: %+v", closed, vs)
		}
		if rec.OnsetAt != from.Add(40*time.Minute).UnixNano() || rec.PeakValue != 900 || rec.ValueAtDetection != 400 {
			t.Fatalf("seriden türetme (max birleşimi): onset=%d peak=%v det=%v", rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
		}
		if closed != (rec.EndedAt == from.Add(55*time.Minute).UnixNano()) || rec.Series.Source != "thanos:cl-a" || rec.Series.Aggregation != "max" ||
			rec.Snapshot.Aggregation != "max" {
			t.Fatalf("closed=%v ended=%d series=%+v", closed, rec.EndedAt, rec.Series)
		}
		if rec.Volume != nil || rec.VolumeReason == "" || rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 {
			t.Fatalf("hacim / sınıf / kapı: %+v", rec)
		}
		if len(rec.Evidence) != 3 || rec.Evidence[0].Independent || rec.Evidence[1].Kind != "pods" || rec.Evidence[1].Independent || !rec.Evidence[2].Independent {
			t.Fatalf("kanıt bağımsızlığı: %+v", rec.Evidence)
		}
		raw, _ := json.Marshal(rec)
		var back EventRecord
		if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
			t.Fatalf("gidiş-dönüş: %v", err)
		}
	}
}

// Kalan örnek: birleşik seri bandı hiç aşmıyor → publish_state=invalid.
func TestThanosMetricRecordInvalidWhenSeriesNeverLeavesBand(t *testing.T) {
	rec, ok := BuildMetricRecord(thanosTestInput(false, func(int) float64 { return 100 }))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	raw, _ := json.Marshal(rec)
	if ev := applyRecordContractShadow(chstore.MLEvent{ID: "x", Detector: "metric_jvm", Record: string(raw), PublishState: PublishPublished}); ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}

// İfade: pod düzeyi tik ifadesi + dondurulmuş pod/namespace eşleyicileri; pod adları tik süzgeciyle.
func TestThanosRecordQueryAndPods(t *testing.T) {
	q, ok := thanosRecordQuery("avg by (namespace, pod) (jvm_thread_count)", []string{"ns-a/svc-a-0", "ns-a/svc-a-1"})
	if !ok || !strings.Contains(q, `pod=~"svc-a-0|svc-a-1"`) || !strings.Contains(q, `namespace=~"ns-a"`) {
		t.Fatalf("%v %q", ok, q)
	}
	if _, ok := thanosRecordQuery("x", nil); ok {
		t.Fatal("pod kümesi yoksa ifade yok")
	}
	ix := buildPodIndex([]string{"svc-a"}, nil)
	rows := []ThanosSample{{Labels: map[string]string{"namespace": "ns-a", "pod": "svc-a-7d9f-abcde"}}, {Labels: map[string]string{"namespace": "ns-b", "pod": "svc-a-7d9f-xyz12"}},
		{Labels: map[string]string{"namespace": "ns-a", "pod": "svc-a-7d9f-abcde"}}}
	got := podNamesByService(rows, ix, namespaceRegex("ns-a"))
	if !reflect.DeepEqual(got["svc-a"], []string{"ns-a/svc-a-7d9f-abcde"}) {
		t.Fatalf("pod adları: %v", got)
	}
	m, err := decodeMatrix("matrix", json.RawMessage(`[{"metric":{"pod":"p"},"values":[[1763208000,"1.5"],[1763208060,"NaN"]]}]`))
	if err != nil || len(m) != 1 || len(m[0].Points) != 1 || m[0].Points[0].Time != 1763208000*1e9 || m[0].Points[0].Value != 1.5 {
		t.Fatalf("matris: %v %+v", err, m)
	}
}
