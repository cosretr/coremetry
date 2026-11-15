package ml

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2234 (H9) — dependency_* olay kaydı: gidiş-dönüş, doğrulayıcıdan
// geçiş (açık + kapalı), kalan örnek, ifade.

func depTestRecordIn(det string, closed bool, vals func(min int) float64) REDRecordInput {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var pts, calls []chstore.SpanMetricPoint
	for m := 0; m <= 60; m++ {
		at := from.Add(time.Duration(m) * time.Minute).UnixNano()
		pts = append(pts, chstore.SpanMetricPoint{Time: at, Value: vals(m)})
		calls = append(calls, chstore.SpanMetricPoint{Time: at, Value: 120})
	}
	s, fam, _ := depSeriesQuery(det, "db|orders-db", "service_name", "db_namespace", "svc-a", 300)
	base, mad := 40.0, 2.0
	if det == detDependencyErrorRate {
		base, mad = 0.01, 0.002
	}
	ev := chstore.MLEvent{Detector: det, Service: "svc-a", Code: "db|orders-db", Baseline: base}
	return depRecordIn(ev, s, fam, mad, chstore.MLConfig{BucketSec: 300, CriticalZ: 3}, from.Add(45*time.Minute), closed,
		pts, calls, from, from.Add(time.Hour), []string{"op_latency:abc"})
}

func TestDependencyRecordContractRoundTrip(t *testing.T) {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, det := range []string{detDependencyLatency, detDependencyErrorRate} {
		normal, high, peak := 40.0, 120.0, 300.0
		if det == detDependencyErrorRate {
			normal, high, peak = 1, 8, 20 // yüzde
		}
		for _, closed := range []bool{false, true} {
			rec, ok := BuildDependencyRecord(depTestRecordIn(det, closed, func(m int) float64 {
				switch {
				case m == 50:
					return peak
				case m >= 40 && m < 55:
					return high
				}
				return normal
			}))
			if !ok {
				t.Fatal("kayıt kurulmalı")
			}
			if vs := ValidateRecord(rec); len(vs) != 0 {
				t.Fatalf("%s closed=%v doğrulayıcıdan geçmeli: %+v", det, closed, vs)
			}
			if rec.OnsetAt != from.Add(40*time.Minute).UnixNano() || rec.PeakValue != peak || rec.ValueAtDetection != high || rec.Direction != "up" {
				t.Fatalf("seriden türetme: onset=%d tepe=%v det=%v", rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
			}
			if closed != (rec.EndedAt == from.Add(55*time.Minute).UnixNano()) {
				t.Fatalf("closed=%v ended=%d", closed, rec.EndedAt)
			}
			if rec.Volume == nil || *rec.Volume <= 0 || int(*rec.Volume)%120 != 0 || rec.Series.StepSec != 300 || rec.Baseline.StepSec != 300 {
				t.Fatalf("hacim/adım: %v %+v", rec.Volume, rec.Series)
			}
			if rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 || len(rec.Evidence) != 2 || rec.Evidence[0].Independent ||
				!rec.Evidence[1].Independent || !strings.Contains(rec.Evidence[0].Summary, "bağımlılık") {
				t.Fatalf("sınıf / kapı / kanıt: %+v", rec)
			}
			raw, _ := json.Marshal(rec)
			var back EventRecord
			if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
				t.Fatalf("gidiş-dönüş: %v", err)
			}
		}
	}
}

// Kalan örnek: seri üst sınırı hiç aşmıyor → publish_state=invalid.
func TestDependencyRecordInvalidWhenSeriesNeverLeavesBand(t *testing.T) {
	rec, ok := BuildDependencyRecord(depTestRecordIn(detDependencyLatency, false, func(int) float64 { return 40 }))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	raw, _ := json.Marshal(rec)
	if ev := applyRecordContract(chstore.MLEvent{ID: "x", Detector: detDependencyLatency, Record: string(raw), PublishState: PublishPublished}); ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}

func TestDependencySeriesQuery(t *testing.T) {
	s, fam, ok := depSeriesQuery(detDependencyLatency, "db|orders-db", "service_name", "db_namespace", "svc-a", 300)
	if !ok || fam != "db" || !strings.Contains(s.Query, `db_namespace="orders-db"`) || !strings.Contains(s.Query, `service_name="svc-a"`) ||
		!strings.Contains(s.Query, "[300s]") || !strings.HasSuffix(s.Query, "* 1000") || s.Unit != "ms" {
		t.Fatalf("%v %q", ok, s.Query)
	}
	e, _, ok := depSeriesQuery(detDependencyErrorRate, "db|orders-db", "service_name", "db_namespace", "svc-a", 300)
	if !ok || !strings.Contains(e.Query, `error_type=~".+"`) || e.Scale != 100 {
		t.Fatalf("%v %q", ok, e.Query)
	}
	if _, _, ok := depSeriesQuery(detDependencyErrorRate, "http|api-b", "service_name", "server_address", "svc-a", 300); ok {
		t.Fatal("http ailesinde hata oranı yok")
	}
	if _, _, ok := depSeriesQuery(detDependencyLatency, "db|orders-db", "", "db_namespace", "svc-a", 300); ok {
		t.Fatal("tik kanıtı (etiket) yoksa ifade yok")
	}
}
