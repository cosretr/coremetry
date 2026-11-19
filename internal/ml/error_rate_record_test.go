package ml

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	route, step := "GET /a", 300
	if det == detSvcErrorRate { // v0.10.2373: yargılanan 1 dk'lık seri
		route, step = "", svcImpactStepSec
	}
	ev := chstore.MLEvent{Detector: det, Baseline: 0.004}
	exp, lo, hi, method := errRecordBand(ev, 0.001, 3)
	return REDRecordInput{Detector: det, Service: "svc-a", Route: route, Family: "http_new",
		Series:    latSeries{Query: fmt.Sprintf("100 * sum(rate(x[%ds])) / sum(rate(y[%ds]))", step, step), Unit: "percent", Aggregation: "err_ratio", Scale: 100},
		Direction: "up", BucketSec: step, Expected: exp, Lo: lo, Hi: hi, Method: method,
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
	// servis bandı = dedektörün kuralı (v0.10.2373, etki kuralı): üst sınır
	// max(%1, medyan × 1,5, medyan + 1 puan), alt 0.
	for _, c := range []struct{ base, hi float64 }{{0, 1}, {0.004, 1.4}, {0.02, 3}, {0.05, 7.5}} {
		_, lo, hi, method := errRecordBand(chstore.MLEvent{Detector: detSvcErrorRate, Baseline: c.base}, 0, 3)
		if lo != 0 || math.Abs(hi-c.hi) > 1e-9 || method != svcErrBandMethod || strings.Contains(method, "x2") {
			t.Fatalf("servis bandı taban %v: %v..%v (%s), üst sınır %v olmalı", c.base, lo, hi, method, c.hi)
		}
	}
}

// errRecVM — kayıt aralık sorgularını yakalayan sahte VM: oran sorgusuna
// dakikalık yüzde serisi (tespitten 4 dk önce %0,4 → %5), hacme 600 döner.
type errRecVM struct {
	fakeVM
	det     time.Time
	queries []string
	froms   []time.Time
}

func (f *errRecVM) QueryPromQLRange(_ context.Context, q string, from, to time.Time, _, _ int) ([]chstore.SpanMetricSeries, error) {
	f.queries, f.froms = append(f.queries, q), append(f.froms, from)
	var pts []chstore.SpanMetricPoint
	for at := from.Truncate(time.Minute); !at.After(to); at = at.Add(time.Minute) {
		v := 0.4
		if strings.HasPrefix(q, "sum(increase(") {
			v = 600
		} else if !at.Before(f.det.Add(-4 * time.Minute)) {
			v = 5
		}
		pts = append(pts, chstore.SpanMetricPoint{Time: at.UnixNano(), Value: v})
	}
	return []chstore.SpanMetricSeries{{Points: pts}}, nil
}

// v0.10.2373 (I3): svc_error_rate kaydı dedektörün yargıladığı 1 DAKİKALIK
// seridir — ifade [60s], seri adımı 60 sn, bant etki kuralının eşiği, pencere
// tespitten 5 dk + 30 dk önce başlar; 15 dakikalık sürdürme iddiası kalmaz.
func TestSvcErrorRateRecordIsOneMinuteSeries(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 8, 30, 0, time.UTC)
	det := now.Add(-30 * time.Second) // 12:08:00
	vm := &errRecVM{det: det}
	cfg := chstore.DefaultMLConfig()
	evs := []chstore.MLEvent{{ID: "ep1", Detector: detSvcErrorRate, Signal: chstore.MLSignalTrace, Service: "orders", Stage: chstore.MLStageShadow,
		Baseline: 0.004, Observed: 0.05, StartedAt: det.Add(-4 * time.Minute).UnixNano(), LastSeen: det.UnixNano(), WindowSec: 300,
		Evidence: `{"rule":"impact","redFamily":"http","svcLabel":"service_name","statusLabel":"http_response_status_code"}`}}
	budget := 5
	attachErrorRateRecords(context.Background(), Deps{VM: vm}, cfg, evs, false, func(string) time.Time { return det }, nil, &budget, now)
	if budget != 4 || len(vm.queries) != 2 {
		t.Fatalf("olay başına 2 aralık sorgusu: bütçe %d, sorgu %d", budget, len(vm.queries))
	}
	if !strings.Contains(vm.queries[0], "[60s]") || strings.Contains(vm.queries[0], "[300s]") || !strings.Contains(vm.queries[0], `service_name="orders"`) {
		t.Fatalf("ifade 1 dk pencereli olmalı: %s", vm.queries[0])
	}
	if want := det.Add(-5*time.Minute - lagRecordPad); !vm.froms[0].Equal(want) {
		t.Fatalf("pencere başı %s want %s", vm.froms[0], want)
	}
	ev := applyRecordContract(evs[0])
	if ev.PublishState != PublishPublished {
		t.Fatalf("kayıt doğrulayıcıdan geçmeli: %q", ev.PublishState)
	}
	var rec EventRecord
	if err := json.Unmarshal([]byte(ev.Record), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Series.StepSec != 60 || rec.Baseline.StepSec != 60 || rec.Snapshot.StepSec != 60 || rec.Baseline.Method != svcErrBandMethod ||
		rec.Baseline.Lo != 0 || math.Abs(rec.Baseline.Hi-1.4) > 1e-9 {
		t.Fatalf("seri/bant: %+v %+v", rec.Series, rec.Baseline)
	}
	if rec.OnsetAt != det.Add(-4*time.Minute).UnixNano() || rec.PeakValue != 5 || rec.ValueAtDetection != 5 || ev.OnsetAt != rec.OnsetAt {
		t.Fatalf("onset/tepe seriden: onset=%d peak=%v det=%v", rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
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
