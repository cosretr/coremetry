package ml

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2223 (H7) — trace_error_count olay kaydı: gidiş-dönüş, doğrulayıcıdan
// geçiş (açık + kapalı), kalan örnek, kayan kova ızgarası.

func terrTestInput(closed bool, perMin func(min int) float64) TerrRecordInput {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var pts []chstore.SpanMetricPoint
	for m := -5; m <= 60; m++ {
		if v := perMin(m); v > 0 { // hatasız dakika satır üretmez
			pts = append(pts, chstore.SpanMetricPoint{Time: from.Add(time.Duration(m) * time.Minute).UnixNano(), Value: v})
		}
	}
	return TerrRecordInput{Service: "svc-a", Operation: "GET /a", BucketSec: 300, Median: 2, NormalMax: 10,
		DetectedAt: from.Add(45 * time.Minute), Closed: closed, Minutes: pts, From: from, To: from.Add(time.Hour),
		Confirmers: []string{"op_error_rate:abc"}}
}

func TestTraceErrorRecordContractRoundTrip(t *testing.T) {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, closed := range []bool{false, true} {
		// dakikada 1 hata (5 dk toplamı 5 < 10); 40–49 arası dakikada 6 (toplam 30), 47'de 20.
		rec, ok := BuildTraceErrorRecord(terrTestInput(closed, func(m int) float64 {
			switch {
			case m == 47:
				return 20
			case m >= 40 && m < 50:
				return 6
			}
			return 1
		}))
		if !ok {
			t.Fatal("kayıt kurulmalı")
		}
		if vs := ValidateRecord(rec); len(vs) != 0 {
			t.Fatalf("closed=%v doğrulayıcıdan geçmeli: %+v", closed, vs)
		}
		// 40. dk: 4×1 + 6 = 10 (sınırda, içeride); 41. dk: 3 + 12 = 15 → onset.
		// 45. dk: 6+6+20+... pencere 41–45 = 6+6+6+6+6 = 30; tepe 47–49 pencereleri = 44.
		if rec.OnsetAt != from.Add(41*time.Minute).UnixNano() || rec.ValueAtDetection != 30 || rec.PeakValue != 44 {
			t.Fatalf("seriden türetme: onset=%d det=%v tepe=%v", rec.OnsetAt, rec.ValueAtDetection, rec.PeakValue)
		}
		if closed != (rec.EndedAt != 0) || !strings.HasPrefix(rec.Title, "Hata span sayısı tespit anında 30, normal üst sınır 10") {
			t.Fatalf("closed=%v ended=%d title=%q", closed, rec.EndedAt, rec.Title)
		}
		if rec.Volume != nil || rec.VolumeReason == "" || rec.Series.Source != "ch:spanmetrics_1m" || rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 {
			t.Fatalf("hacim / kaynak / sınıf / kapı: %+v", rec)
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

// Kalan örnek: kayan toplam üst sınırı hiç aşmıyor → publish_state=invalid.
func TestTraceErrorRecordInvalidWhenSeriesNeverLeavesBand(t *testing.T) {
	rec, ok := BuildTraceErrorRecord(terrTestInput(false, func(int) float64 { return 1 }))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	raw, _ := json.Marshal(rec)
	if ev := applyRecordContractShadow(chstore.MLEvent{ID: "x", Detector: detTraceErrorCount, Record: string(raw), PublishState: PublishPublished}); ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}

// Izgara: hatasız dakika 0, nokta = o dakikayla biten kova toplamı, 720 tavanı.
func TestTraceErrorGrid(t *testing.T) {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	pts := []chstore.SpanMetricPoint{{Time: from.Add(-time.Minute).UnixNano(), Value: 3}, {Time: from.Add(2 * time.Minute).UnixNano(), Value: 4}}
	t0, step, v := terrGrid(pts, from, from.Add(10*time.Minute), 300)
	if t0 != from.Unix() || step != 60 || len(v) != 11 || v[0] != 3 || v[2] != 7 || v[3] != 7 || v[4] != 4 || v[7] != 0 {
		t.Fatalf("%d %d %v", t0, step, v)
	}
	if _, step, v := terrGrid(nil, from, from.Add(24*time.Hour), 300); step != 180 || len(v) > 720 {
		t.Fatalf("tavan: %d %d", step, len(v))
	}
	if q := terrSeriesQuery("svc-a", "GET /a", 300); !strings.Contains(q, `service_name = "svc-a"`) || !strings.Contains(q, "300s") {
		t.Fatal(q)
	}
}
