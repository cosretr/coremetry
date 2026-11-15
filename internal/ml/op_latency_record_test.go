package ml

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2219 (H3) — op_latency olay kaydı: sözleşme gidiş-dönüşü,
// doğrulayıcıdan geçiş (açık + kapalı), kalan örnek, seri ifadesi.

var latTestPin = anomaly.REDFamilyPin{ID: "http_server", Names: "http_server_request_duration_seconds", Unit: "s"}

func latTestInput(closed bool, measure string, vals func(min int) float64) LatRecordInput {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var pts, calls []chstore.SpanMetricPoint
	for m := 0; m <= 60; m++ {
		at := from.Add(time.Duration(m) * time.Minute).UnixNano()
		pts = append(pts, chstore.SpanMetricPoint{Time: at, Value: vals(m)})
		calls = append(calls, chstore.SpanMetricPoint{Time: at, Value: 100})
	}
	s, _ := latSeriesQuery(latTestPin, "service_name", "http_route", "orders-api", "/v1/orders", measure, 300, 1)
	med, mad := 100.0, 10.0
	if measure == "slow_ratio" {
		med, mad = 0.02, 0.005
	}
	return LatRecordInput{Service: "orders-api", Route: "/v1/orders", Family: latTestPin.ID, Series: s, BucketSec: 300,
		CriticalZ: 3, Median: med, MAD: mad, DetectedAt: from.Add(45 * time.Minute), Closed: closed,
		Points: pts, Calls: calls, From: from, To: from.Add(time.Hour), Confirmers: []string{"metric_jvm:abc"}}
}

func latSpike(m int) float64 {
	switch {
	case m >= 40 && m < 55 && m != 50:
		return 400
	case m == 50:
		return 900
	}
	return 100
}

func TestLatencyRecordContractRoundTrip(t *testing.T) {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, closed := range []bool{false, true} {
		rec, ok := BuildLatencyRecord(latTestInput(closed, "p95", latSpike))
		if !ok {
			t.Fatal("kayıt kurulmalı")
		}
		if vs := ValidateRecord(rec); len(vs) != 0 {
			t.Fatalf("closed=%v doğrulayıcıdan geçmeli: %+v", closed, vs)
		}
		if rec.OnsetAt != from.Add(40*time.Minute).UnixNano() || rec.PeakValue != 900 || rec.ValueAtDetection != 400 || rec.ExpectedValue != 100 {
			t.Fatalf("seriden türetme: onset=%d peak=%v det=%v", rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
		}
		wantVol := 2100.0 // açık: onset 40 → son nokta 60 = 21 dk × 100
		if closed {
			wantVol = 1600 // kapalı: 40–55 = 16 dk × 100
		}
		if rec.Volume == nil || *rec.Volume != wantVol || len(rec.Snapshot.N) != len(rec.Snapshot.V) {
			t.Fatalf("hacim: %v", rec.Volume)
		}
		if closed != (rec.EndedAt == from.Add(55*time.Minute).UnixNano()) || closed != strings.Contains(rec.Title, "tepe 900 ms") {
			t.Fatalf("closed=%v ended=%d title=%q", closed, rec.EndedAt, rec.Title)
		}
		if rec.Subject.Operation != "/v1/orders" || rec.Series.Unit != "ms" || rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 {
			t.Fatalf("konu / birim / sınıf / kapı: %+v", rec)
		}
		if len(rec.Evidence) != 2 || rec.Evidence[0].Independent || !rec.Evidence[1].Independent {
			t.Fatalf("kanıt bağımsızlığı: %+v", rec.Evidence)
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		var back EventRecord
		if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
			t.Fatalf("gidiş-dönüş: %v", err)
		}
	}
}

// Yavaş istek oranı: dedektörün 0..1 oranı ve bandı yüzdeye çevrilir; başlık
// sayıları alanlarla aynı.
func TestLatencyRecordSlowRatio(t *testing.T) {
	rec, ok := BuildLatencyRecord(latTestInput(true, "slow_ratio", func(m int) float64 {
		if m >= 40 && m < 55 {
			return 12.5
		}
		return 2
	}))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	if vs := ValidateRecord(rec); len(vs) != 0 {
		t.Fatalf("doğrulayıcıdan geçmeli: %+v", vs)
	}
	if rec.Series.Unit != "percent" || rec.ExpectedValue != 2 || !strings.Contains(rec.Title, "%12,5") {
		t.Fatalf("yüzde: %+v %q", rec.Baseline, rec.Title)
	}
}

// Kalan örnek: 1 dk seri bandı hiç aşmıyor → onset seriden türemez →
// publish_state=invalid.
func TestLatencyRecordInvalidWhenSeriesNeverLeavesBand(t *testing.T) {
	rec, ok := BuildLatencyRecord(latTestInput(false, "p95", func(int) float64 { return 100 }))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	raw, _ := json.Marshal(rec)
	ev := applyRecordContract(chstore.MLEvent{ID: "x", Detector: detOpLatency, Record: string(raw), PublishState: PublishPublished})
	if ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}

func TestLatSeriesQuery(t *testing.T) {
	s, ok := latSeriesQuery(latTestPin, "service_name", "http_route", "a", "/b", "p95", 300, 1)
	if !ok || s.Query != `histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket{http_route="/b",service_name="a"}[300s]))) * 1000` ||
		s.VolumeQuery != `sum(increase(http_server_request_duration_seconds_count{http_route="/b",service_name="a"}[60s]))` {
		t.Fatalf("%+v", s)
	}
	if _, ok := latSeriesQuery(latTestPin, "", "http_route", "a", "/b", "p95", 300, 1); ok {
		t.Fatal("etiket yoksa seri yok")
	}
}
