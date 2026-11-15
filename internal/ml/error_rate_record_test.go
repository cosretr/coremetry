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

// v0.10.2221 (H5) — hata oranı olay kaydı: gidiş-dönüş, doğrulayıcıdan
// geçiş (açık + kapalı, servis + endpoint), kalan örnek, ifade.

func errTestInput(det string, closed bool, vals func(min int) float64) REDRecordInput {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var pts, calls []chstore.SpanMetricPoint
	for m := 0; m <= 60; m++ {
		at := from.Add(time.Duration(m) * time.Minute).UnixNano()
		pts = append(pts, chstore.SpanMetricPoint{Time: at, Value: vals(m)})
		calls = append(calls, chstore.SpanMetricPoint{Time: at, Value: 100})
	}
	route := "GET /a"
	if det == detSvcErrorRate {
		route = ""
	}
	ev := chstore.MLEvent{Detector: det, Baseline: 0.004}
	exp, lo, hi, method := errRecordBand(ev, 0.001, 3)
	return REDRecordInput{Detector: det, Service: "svc-a", Route: route, Family: "http_new",
		Series:    latSeries{Query: "100 * sum(rate(x[300s])) / sum(rate(y[300s]))", Unit: "percent", Aggregation: "err_ratio", Scale: 100},
		Direction: "up", BucketSec: 300, Expected: exp, Lo: lo, Hi: hi, Method: method,
		DetectedAt: from.Add(45 * time.Minute), Closed: closed, Points: pts, Calls: calls,
		From: from, To: from.Add(time.Hour), Confirmers: []string{"op_latency:abc"}, Head: "Hata oranı", Label: "hata oranı", Fmt: pctFmt}
}

func TestErrorRateRecordContractRoundTrip(t *testing.T) {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, det := range []string{detSvcErrorRate, detOpErrorRate} {
		for _, closed := range []bool{false, true} {
			rec, ok := BuildREDRecord(errTestInput(det, closed, func(m int) float64 {
				switch {
				case m == 50:
					return 9.5
				case m >= 40 && m < 55:
					return 4.2
				}
				return 0.4
			}))
			if !ok {
				t.Fatal("kayıt kurulmalı")
			}
			if vs := ValidateRecord(rec); len(vs) != 0 {
				t.Fatalf("%s closed=%v doğrulayıcıdan geçmeli: %+v", det, closed, vs)
			}
			if rec.OnsetAt != from.Add(40*time.Minute).UnixNano() || rec.PeakValue != 9.5 || rec.ValueAtDetection != 4.2 {
				t.Fatalf("%s seriden türetme: onset=%d peak=%v det=%v", det, rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
			}
			if closed != (rec.EndedAt == from.Add(55*time.Minute).UnixNano()) || !strings.HasPrefix(rec.Title, "Hata oranı tespit anında %4,2") {
				t.Fatalf("%s closed=%v ended=%d title=%q", det, closed, rec.EndedAt, rec.Title)
			}
			if rec.Volume == nil || *rec.Volume == 0 || rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 {
				t.Fatalf("hacim / sınıf / kapı: %+v", rec)
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
	// servis bandı = dedektörün kuralı: üst sınır max(2 × medyan, %1), alt 0.
	if _, lo, hi, _ := errRecordBand(chstore.MLEvent{Detector: detSvcErrorRate, Baseline: 0.02}, 0, 3); lo != 0 || hi != 4 {
		t.Fatalf("servis bandı %v..%v", lo, hi)
	}
}

// Kalan örnek: 1 dk seri bandı hiç aşmıyor → publish_state=invalid.
func TestErrorRateRecordInvalidWhenSeriesNeverLeavesBand(t *testing.T) {
	rec, ok := BuildREDRecord(errTestInput(detOpErrorRate, false, func(int) float64 { return 0.4 }))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	raw, _ := json.Marshal(rec)
	if ev := applyRecordContract(chstore.MLEvent{ID: "x", Detector: detOpErrorRate, Record: string(raw), PublishState: PublishPublished}); ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}

// İfade: err/count oranı, servis (ve route) eşleyicisi, durum regex'i; aile seçimi emit'ten.
func TestErrorRateSeriesQuery(t *testing.T) {
	pin := anomaly.REDFamilyPins()[0]
	s, ok := errSeriesQuery(pin, "service_name", "http_route", "status", "svc-a", "GET /a", 300)
	if !ok || !strings.Contains(s.Query, `service_name="svc-a"`) || !strings.Contains(s.Query, `http_route="GET /a"`) ||
		!strings.Contains(s.Query, `status=~`) || !strings.Contains(s.Query, "[300s]") || !strings.Contains(s.VolumeQuery, "[60s]") {
		t.Fatalf("%v %+v", ok, s)
	}
	s, ok = errSeriesQuery(pin, "service_name", "", "status", "svc-a", "", 300)
	if !ok || strings.Contains(s.Query, "http_route") {
		t.Fatalf("servis düzeyi: %v %q", ok, s.Query)
	}
	if _, ok := errSeriesQuery(pin, "service_name", "", "", "svc-a", "", 300); ok {
		t.Fatal("durum etiketi yoksa ifade yok")
	}
	a := newSvcAgg()
	a.add(1, "svc-a", "count", 600, 5)
	a.add(0, "svc-a", "count", 600, 5)
	if fam := a.emit(map[string]map[int64]float64{}); fam["svc-a"] != 0 {
		t.Fatalf("ilk aile seçilmeli: %v", fam)
	}
}
