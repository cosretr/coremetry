package ml

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2235 (H10) — kırılma noktası olay kaydı: kanıta dondurma → kayıt
// gidiş-dönüşü, doğrulayıcıdan geçiş (açık + kapalı), kalan örnek.

func cpTestEvent(t *testing.T, det, signal, part string, after float64) (chstore.MLEvent, cpResult, time.Time) {
	t.Helper()
	start := time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC).Unix()
	var pts []chstore.MLPoint
	for i := 0; i < 72; i++ {
		v := 100.0
		if i >= 50 {
			v = after
		}
		pts = append(pts, chstore.MLPoint{T: start + int64(i)*300, V: v})
	}
	ref := map[int]chstore.MLProfileStat{0: {Q25: 90, Q50: 100, Q75: 110}}
	slotOf := func(int64) int { return 0 }
	r := cusum(pts, ref, slotOf, 300, 6)
	evm := map[string]any{"part": part, "bucketSec": 300}
	if r.Fire {
		cpFreeze(evm, pts, ref, slotOf, 300, r.At)
		evm["rollouts"] = []any{map[string]any{"service": "svc-a"}}
	}
	raw, _ := json.Marshal(evm)
	ev := chstore.MLEvent{ID: "e1", Detector: det, Signal: signal, Service: "svc-a", Code: "GET /a", Direction: r.Direction,
		StartedAt: r.At * 1e9, Evidence: string(raw)}
	return ev, r, time.Unix(pts[len(pts)-1].T, 0)
}

func TestChangePointRecordContractRoundTrip(t *testing.T) {
	cases := []struct{ det, signal, part string }{
		{detTraceChangePoint, chstore.MLSignalTrace, "GET /a|p95"},
		{detChangePoint, chstore.MLSignalLog, partError},
	}
	for _, c := range cases {
		for _, closed := range []bool{false, true} {
			ev, r, last := cpTestEvent(t, c.det, c.signal, c.part, 200)
			if !r.Fire || r.Direction != "up" {
				t.Fatalf("CUSUM ateşlemeli: %+v", r)
			}
			evs := []chstore.MLEvent{ev}
			attachChangePointRecords(evs, closed, func(string) time.Time { return last }, func(chstore.MLEvent) []string { return []string{"x:1"} }, last.Add(time.Hour))
			if evs[0].Record == "" || evs[0].PublishState != PublishPublished {
				t.Fatalf("%s kayıt yazılmalı", c.det)
			}
			var rec EventRecord
			if err := json.Unmarshal([]byte(evs[0].Record), &rec); err != nil {
				t.Fatal(err)
			}
			if vs := ValidateRecord(rec); len(vs) != 0 {
				t.Fatalf("%s closed=%v doğrulayıcıdan geçmeli: %+v", c.det, closed, vs)
			}
			if out := applyRecordContract(evs[0]); out.PublishState != PublishPublished {
				t.Fatalf("sözleşme: %q", out.PublishState)
			}
			if rec.OnsetAt != r.At*1e9 || rec.PeakValue != 200 || rec.ValueAtDetection != 200 || rec.ExpectedValue != 100 || rec.Snapshot.StepSec != 60 || rec.Series.StepSec != 300 {
				t.Fatalf("seriden türetme: onset=%d (kırılma %d) tepe=%v", rec.OnsetAt, r.At*1e9, rec.PeakValue)
			}
			if closed != (rec.EndedAt > 0) || rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 || len(rec.Evidence) != 3 ||
				rec.Evidence[0].Independent || !rec.Evidence[1].Independent || rec.Evidence[1].Kind != "rollout" {
				t.Fatalf("bitiş / sınıf / kanıt: %+v", rec)
			}
			if c.signal == chstore.MLSignalLog && (rec.Volume == nil || *rec.Volume != 200*22) {
				t.Fatalf("log hacmi aynı seriden: %v", rec.Volume)
			}
			if c.signal == chstore.MLSignalTrace && (rec.Volume != nil || rec.VolumeReason == "" || rec.Subject.Operation != "GET /a" || !strings.HasPrefix(rec.Title, "p95 tespit anında 200 ms")) {
				t.Fatalf("trace: %+v %q", rec.Volume, rec.Title)
			}
			back, _ := json.Marshal(rec)
			var again EventRecord
			if err := json.Unmarshal(back, &again); err != nil || !reflect.DeepEqual(again, rec) {
				t.Fatalf("gidiş-dönüş: %v", err)
			}
		}
	}
}

// Kalan örnek: seri bant içinde (kırılma yok) → doğrulayıcı onset'i bulamaz.
func TestChangePointRecordInvalidWhenSeriesInBand(t *testing.T) {
	rec, ok := BuildChangePointRecord(CPRecordInput{Detector: detChangePoint, Signal: chstore.MLSignalLog, Service: "svc-a", Part: partTotal,
		BucketSec: 300, T0: time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC).Unix(), V: []float64{100, 101, 99, 100}, Lo: []float64{85, 85, 85, 85},
		Hi: []float64{115, 115, 115, 115}, Direction: "up", DetectedAt: time.Date(2026, 11, 15, 6, 15, 0, 0, time.UTC)})
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	raw, _ := json.Marshal(rec)
	if ev := applyRecordContract(chstore.MLEvent{ID: "x", Detector: detChangePoint, Record: string(raw), PublishState: PublishPublished}); ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
	if _, ok := BuildChangePointRecord(CPRecordInput{BucketSec: 90, V: []float64{1}, Lo: []float64{0}, Hi: []float64{2}}); ok {
		t.Fatal("1 dk'nın katı olmayan kova reddedilir")
	}
}
