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

// v0.10.2222 (H6) — throughput_drop olay kaydı: gidiş-dönüş, doğrulayıcıdan
// geçiş (açık + kapalı), kalan örnek, ifade.

func tpTestInput(closed bool, vals func(min int) float64) REDRecordInput {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var pts []chstore.SpanMetricPoint
	for m := 0; m <= 60; m++ {
		pts = append(pts, chstore.SpanMetricPoint{Time: from.Add(time.Duration(m) * time.Minute).UnixNano(), Value: vals(m)})
	}
	lo, hi := lagBand(10, 0.5, 3)
	return REDRecordInput{Detector: detThroughputDrop, Service: "svc-a", Route: "GET /a", Family: "http_new",
		Series:    latSeries{Query: `sum(rate(x_count{service_name="svc-a"}[300s]))`, Unit: "req/s", Aggregation: "rate", Scale: 1},
		Direction: "down", BucketSec: 300, Expected: 10, Lo: lo, Hi: hi, Method: lagBandMethod,
		DetectedAt: from.Add(45 * time.Minute), Closed: closed, Points: pts, CallsFromSeries: true,
		From: from, To: from.Add(time.Hour), Confirmers: []string{"op_latency:abc"}, Head: "İstek hızı", Label: "istek hızı", Fmt: rpsFmt}
}

func TestThroughputRecordContractRoundTrip(t *testing.T) {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, closed := range []bool{false, true} {
		rec, ok := BuildREDRecord(tpTestInput(closed, func(m int) float64 {
			switch {
			case m == 50:
				return 0.5
			case m >= 40 && m < 55:
				return 2
			}
			return 10
		}))
		if !ok {
			t.Fatal("kayıt kurulmalı")
		}
		if vs := ValidateRecord(rec); len(vs) != 0 {
			t.Fatalf("closed=%v doğrulayıcıdan geçmeli: %+v", closed, vs)
		}
		if rec.OnsetAt != from.Add(40*time.Minute).UnixNano() || rec.PeakValue != 0.5 || rec.ValueAtDetection != 2 || rec.Direction != "down" {
			t.Fatalf("seriden türetme: onset=%d dip=%v det=%v", rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
		}
		if closed != (rec.EndedAt == from.Add(55*time.Minute).UnixNano()) || !strings.HasPrefix(rec.Title, "İstek hızı tespit anında 2 istek/sn, normal alt sınır") {
			t.Fatalf("closed=%v ended=%d title=%q", closed, rec.EndedAt, rec.Title)
		}
		// hacim aynı seriden: istek/sn × 60 sn, olay penceresi toplamı
		if rec.Volume == nil || *rec.Volume <= 0 || len(rec.Snapshot.N) != len(rec.Snapshot.V) || rec.Snapshot.N[0] != 600 {
			t.Fatalf("hacim: %v %v", rec.Volume, rec.Snapshot.N[:1])
		}
		if rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 || len(rec.Evidence) != 2 || rec.Evidence[0].Independent || !rec.Evidence[1].Independent {
			t.Fatalf("sınıf / kapı / kanıt: %+v", rec)
		}
		raw, _ := json.Marshal(rec)
		var back EventRecord
		if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
			t.Fatalf("gidiş-dönüş: %v", err)
		}
	}
}

// Kalan örnek: seri alt sınırın altına hiç inmiyor → publish_state=invalid.
func TestThroughputRecordInvalidWhenSeriesNeverLeavesBand(t *testing.T) {
	rec, ok := BuildREDRecord(tpTestInput(false, func(int) float64 { return 10 }))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	raw, _ := json.Marshal(rec)
	if ev := applyRecordContract(chstore.MLEvent{ID: "x", Detector: detThroughputDrop, Record: string(raw), PublishState: PublishPublished}); ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}

func TestThroughputSeriesQuery(t *testing.T) {
	pin := anomaly.REDFamilyPins()[0]
	s, ok := tpSeriesQuery(pin, "service_name", "http_route", "svc-a", "GET /a", 300)
	if !ok || !strings.HasPrefix(s.Query, "sum(rate(") || !strings.Contains(s.Query, `service_name="svc-a"`) ||
		!strings.Contains(s.Query, `http_route="GET /a"`) || !strings.Contains(s.Query, "[300s]") {
		t.Fatalf("%v %q", ok, s.Query)
	}
	if _, ok := tpSeriesQuery(pin, "service_name", "", "svc-a", "GET /a", 300); ok {
		t.Fatal("route etiketi yoksa ifade yok")
	}
}
