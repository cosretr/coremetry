package ml

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2236 (H11) — log RED olay kaydı: ızgara, gidiş-dönüş, doğrulayıcıdan
// geçiş (açık + kapalı; hacim artışı, hata logu, susma), kalan örnek.

func logTestInput(det, dir string, closed bool, val func(i int) float64) CPRecordInput {
	t0 := time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC).Unix()
	var pts []chstore.MLPoint
	for i := 0; i < 24; i++ {
		if x := val(i); x >= 0 { // < 0: satır yok (eksik kova)
			pts = append(pts, chstore.MLPoint{T: t0 + int64(i)*300, V: x})
		}
	}
	pts = append(pts, chstore.MLPoint{T: t0, V: 1}) // çift satır: en büyüğü kalır
	v := logREDGrid(pts, t0, t0+23*300, 300)
	lo, hi := lagBand(1000, 50, 3)
	los, his := make([]float64, len(v)), make([]float64, len(v))
	for i := range v {
		los[i], his[i] = lo, hi
	}
	part := partTotal
	if det == detErrorRate {
		part = partError
	}
	return CPRecordInput{Detector: det, Signal: chstore.MLSignalLog, Service: "svc-a", SeriesService: "svc-a-logs", Part: part,
		BucketSec: 300, T0: t0, V: v, Lo: los, Hi: his, Direction: dir, DetectedAt: time.Unix(t0+20*300+90, 0), Closed: closed,
		Confirmers: []string{"op_latency:abc"}}
}

func TestLogREDRecordContractRoundTrip(t *testing.T) {
	cases := []struct {
		det, dir string
		peak     float64
		val      func(i int) float64
	}{
		{detLogVolume, "up", 5000, func(i int) float64 {
			switch {
			case i == 19:
				return 5000
			case i >= 16 && i < 22:
				return 3000
			}
			return 1000
		}},
		{detErrorRate, "up", 5000, func(i int) float64 {
			if i >= 16 && i < 22 {
				return 5000
			}
			return 1000
		}},
		{detServiceSilent, "down", 0, func(i int) float64 {
			if i >= 16 && i < 22 {
				return -1 // susma: ml_series'te satır yok
			}
			return 1000
		}},
	}
	for _, c := range cases {
		for _, closed := range []bool{false, true} {
			in := logTestInput(c.det, c.dir, closed, c.val)
			rec, ok := BuildLogREDRecord(in)
			if !ok {
				t.Fatal("kayıt kurulmalı")
			}
			if vs := ValidateRecord(rec); len(vs) != 0 {
				t.Fatalf("%s closed=%v doğrulayıcıdan geçmeli: %+v", c.det, closed, vs)
			}
			if rec.OnsetAt != (in.T0+16*300)*1e9 || rec.PeakValue != c.peak || rec.Direction != c.dir || rec.Baseline.Method != lagBandMethod {
				t.Fatalf("%s seriden türetme: onset=%d tepe=%v", c.det, rec.OnsetAt, rec.PeakValue)
			}
			if closed != (rec.EndedAt == (in.T0+22*300)*1e9) || rec.Volume == nil || rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 ||
				rec.Evidence[0].Independent || !rec.Evidence[1].Independent || !strings.Contains(rec.Series.Query, "'svc-a-logs'") {
				t.Fatalf("%s bitiş / hacim / kanıt / sorgu: %+v", c.det, rec)
			}
			raw, _ := json.Marshal(rec)
			var back EventRecord
			if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
				t.Fatalf("gidiş-dönüş: %v", err)
			}
		}
	}
}

// Kalan örnek: seri bandın içinde → publish_state=invalid.
func TestLogREDRecordInvalidWhenSeriesInBand(t *testing.T) {
	rec, ok := BuildLogREDRecord(logTestInput(detLogVolume, "up", false, func(int) float64 { return 1000 }))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	raw, _ := json.Marshal(rec)
	if ev := applyRecordContractShadow(chstore.MLEvent{ID: "x", Detector: detLogVolume, Record: string(raw), PublishState: PublishPublished}); ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}
