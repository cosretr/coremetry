package ml

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2215 (H2) — consumer lag olay kaydı: sözleşme gidiş-dönüşü,
// doğrulayıcıdan geçiş, kalan örnek ve kartın başlığı kayıttan okuması.

func lagTestInput(closed bool, vals func(min int) float64) LagRecordInput {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var pts []chstore.SpanMetricPoint
	for m := 0; m <= 60; m++ {
		pts = append(pts, chstore.SpanMetricPoint{Time: from.Add(time.Duration(m) * time.Minute).UnixNano(), Value: vals(m)})
	}
	return LagRecordInput{Service: "orders-api", Topic: "orders-events", Query: lagSeriesQuery("service_name", "orders-api", "orders-events"),
		BucketSec: 300, CriticalZ: 3, Median: 100, MAD: 10, DetectedAt: from.Add(45 * time.Minute), Closed: closed,
		Points: pts, From: from, To: from.Add(time.Hour), Confirmers: []string{"op_latency:abc"}}
}

func lagSpike(m int) float64 {
	switch {
	case m >= 40 && m < 55 && m != 50:
		return 500
	case m == 50:
		return 900
	}
	return 100
}

func TestLagRecordContractRoundTrip(t *testing.T) {
	for _, closed := range []bool{false, true} {
		rec, ok := BuildLagRecord(lagTestInput(closed, lagSpike))
		if !ok {
			t.Fatal("kayıt kurulmalı")
		}
		if vs := ValidateRecord(rec); len(vs) != 0 {
			t.Fatalf("closed=%v doğrulayıcıdan geçmeli: %+v", closed, vs)
		}
		from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
		if rec.OnsetAt != from.Add(40*time.Minute).UnixNano() || rec.PeakValue != 900 || rec.ValueAtDetection != 500 || rec.ExpectedValue != 100 {
			t.Fatalf("seriden türetme: onset=%d peak=%v det=%v", rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
		}
		if closed != (rec.EndedAt == from.Add(55*time.Minute).UnixNano()) || closed != strings.Contains(rec.Title, "tepe 900") {
			t.Fatalf("closed=%v ended=%d title=%q", closed, rec.EndedAt, rec.Title)
		}
		if rec.Volume != nil || rec.VolumeReason == "" || rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 {
			t.Fatalf("hacim nedeni / sınıf / kapı yer tutucusu: %+v", rec)
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
			t.Fatalf("gidiş-dönüş: %v\n%+v\n%+v", err, back, rec)
		}
	}
}

// Kalan örnek: seri hiç bant dışına çıkmıyor (5 dk kova tetiklemiş ama 1 dk
// seri sapmayı göstermiyor) → onset seriden türemez → publish_state=invalid.
func TestLagRecordInvalidWhenSeriesNeverLeavesBand(t *testing.T) {
	rec, ok := BuildLagRecord(lagTestInput(false, func(int) float64 { return 100 }))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	vs := ValidateRecord(rec)
	found := false
	for _, v := range vs {
		found = found || v.Rule == RuleOnsetFromSerie
	}
	if !found {
		t.Fatalf("onset_from_series ihlali beklenir: %+v", vs)
	}
	raw, _ := json.Marshal(rec)
	ev := applyRecordContract(chstore.MLEvent{ID: "x", Detector: "metric_kafka", Record: string(raw), PublishState: PublishPublished})
	if ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}

// Kart: geçerli kayıt varsa başlık yalnız kayıttan, panel tepesi parçası
// (Value) yok → FE withPanelPeak başlığı yeniden yazmaz. Kayıt geçersizse
// ya da yoksa eski yol aynen.
func TestCardTitleFromRecord(t *testing.T) {
	rec, _ := BuildLagRecord(lagTestInput(false, lagSpike))
	raw, _ := json.Marshal(rec)
	at := time.Date(2026, 11, 15, 12, 45, 0, 0, time.UTC)
	ev := chstore.MLEvent{ID: "e1", Detector: "metric_kafka", Signal: chstore.MLSignalMetric, Service: "orders-api",
		Code: kafkaTopicPart("orders-events"), StartedAt: at.UnixNano(), LastSeen: at.Add(5 * time.Minute).UnixNano(),
		WindowSec: 300, Observed: 500, Baseline: 100, Direction: "up", Record: string(raw), PublishState: PublishPublished}
	c := BuildExplainCard(CardInputFromEvent(ev, at.Add(10*time.Minute), time.UTC))
	if c.Summary != rec.Title || c.Value != nil {
		t.Fatalf("başlık kayıttan: %q / value=%v", c.Summary, c.Value)
	}
	ev.PublishState = PublishInvalid
	if c := BuildExplainCard(CardInputFromEvent(ev, at.Add(10*time.Minute), time.UTC)); c.Summary == rec.Title || c.Value == nil {
		t.Fatal("geçersiz kayıtta eski yol")
	}
}

func TestLagSeriesQuery(t *testing.T) {
	if got := lagSeriesQuery("service_name", `a"b`, ""); got != `max by (service_name) (kafka_consumer_records_lag_max{service_name="a\"b"})` {
		t.Fatal(got)
	}
	if got := lagSeriesQuery("svc", "s", "t"); got != `max by (svc, topic) (kafka_consumer_records_lag_max{svc="s", topic="t"})` {
		t.Fatal(got)
	}
}
