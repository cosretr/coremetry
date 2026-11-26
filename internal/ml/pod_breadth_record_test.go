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

// v0.10.2237 (H12) — trace_pod_outlier ve svc_breadth olay kaydı:
// gidiş-dönüş, doğrulayıcıdan geçiş (açık + kapalı), kalan örnek, ifade.

func podTestRows(t0 int64, n int, val func(pod string, i int) float64) []chstore.SpanMetricSeries {
	var out []chstore.SpanMetricSeries
	for _, pod := range []string{"pod-a", "pod-b", "pod-c", "pod-d"} {
		r := chstore.SpanMetricSeries{GroupKey: []string{"k8s_pod_name=" + pod}}
		for i := 0; i < n; i++ {
			r.Points = append(r.Points, chstore.SpanMetricPoint{Time: (t0 + int64(i)*60) * 1e9, Value: val(pod, i)})
		}
		out = append(out, r)
	}
	return out
}

func podTestRecord(t *testing.T, measure string, closed bool, bad bool) (EventRecord, bool) {
	t.Helper()
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC).Unix()
	n := 60
	total := podGrid(podTestRows(t0, n, func(string, int) float64 { return 1000 }), "k8s_pod_name", t0, n)
	meas := podGrid(podTestRows(t0, n, func(pod string, i int) float64 {
		hot := !bad && pod == "pod-a" && i >= 30 && i < 50
		if measure == "err_ratio" {
			if hot {
				return 300
			}
			return 10
		}
		if hot {
			return 2000
		}
		return 200
	}), "k8s_pod_name", t0, n)
	v, exp, hi, ok := podCrossSection(total, meas, "pod-a", measure, n, 3, 10)
	if !ok {
		t.Fatal("kesit kurulmalı")
	}
	calls := make([]float64, n)
	for i := range calls {
		calls[i] = 200
	}
	return BuildPodOutlierRecord(PodRecordInput{Service: "svc-a", Pod: "pod-a", Measure: measure, Family: "http_new", Query: "q",
		WinSec: 900, Pods: 4, T0: t0, V: v, Exp: exp, Hi: hi, Calls: calls, DetectedAt: time.Unix(t0+40*60, 0), Closed: closed,
		Confirmers: []string{"op_latency:abc"}})
}

func TestPodOutlierRecordContractRoundTrip(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC).Unix()
	for _, measure := range []string{"err_ratio", "p95"} {
		for _, closed := range []bool{false, true} {
			rec, ok := podTestRecord(t, measure, closed, false)
			if !ok {
				t.Fatal("kayıt kurulmalı")
			}
			if vs := ValidateRecord(rec); len(vs) != 0 {
				t.Fatalf("%s closed=%v doğrulayıcıdan geçmeli: %+v", measure, closed, vs)
			}
			wantPeak, wantExp := 30.0, 1.0 // yüzde
			if measure == "p95" {
				wantPeak, wantExp = 2000, 200
			}
			if rec.OnsetAt != (t0+30*60)*1e9 || rec.PeakValue != wantPeak || rec.ExpectedValue != wantExp || rec.Subject.Pod != "pod-a" ||
				rec.Baseline.Method != podBandMethod {
				t.Fatalf("%s seriden türetme: onset=%d tepe=%v beklenen=%v", measure, rec.OnsetAt, rec.PeakValue, rec.ExpectedValue)
			}
			if closed != (rec.EndedAt == (t0+50*60)*1e9) || rec.Volume == nil || *rec.Volume <= 0 || rec.Class != ClassServiceAnomaly ||
				len(rec.Gates) != 0 || rec.Evidence[0].Independent || !rec.Evidence[1].Independent {
				t.Fatalf("%s bitiş / hacim / kanıt: %+v", measure, rec)
			}
			raw, _ := json.Marshal(rec)
			var back EventRecord
			if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
				t.Fatalf("gidiş-dönüş: %v", err)
			}
		}
	}
}

// Kalan örnek: pod kesitin içinde kalıyor → publish_state=invalid.
func TestPodOutlierRecordInvalidWhenPodInBand(t *testing.T) {
	rec, ok := podTestRecord(t, "p95", false, true)
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	raw, _ := json.Marshal(rec)
	if ev := applyRecordContractShadow(chstore.MLEvent{ID: "x", Detector: detTracePodOutlier, Record: string(raw), PublishState: PublishPublished}); ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}

func TestPodSeriesQueries(t *testing.T) {
	pin := anomaly.REDFamilyPins()[0]
	q, ok := podSeriesQueries(pin, "service_name", "k8s_pod_name", "http_status_code", "svc-a", "pod-a", "err_ratio", 900)
	if !ok || !strings.Contains(q.Total, "sum by (k8s_pod_name)") || !strings.Contains(q.Series, `k8s_pod_name="pod-a"`) ||
		!strings.Contains(q.Meas, "http_status_code=~") || !strings.Contains(q.Calls, "[60s]") || !strings.Contains(q.Total, "[900s]") {
		t.Fatalf("%v %+v", ok, q)
	}
	if _, ok := podSeriesQueries(pin, "service_name", "", "", "svc-a", "pod-a", "p95", 900); ok {
		t.Fatal("pod etiketi yoksa ifade yok")
	}
}

func breadthTestHist(t0 time.Time, counts []int) *breadthHist {
	h := &breadthHist{}
	for i, c := range counts {
		ops := map[string][]BreadthOp{}
		for j := 0; j < c; j++ {
			ops["svc-a"] = append(ops["svc-a"], BreadthOp{Op: "op-" + string(rune('a'+j))})
		}
		h.observe(ops, t0.Add(time.Duration(i)*5*time.Minute))
	}
	return h
}

func TestBreadthRecordContractRoundTrip(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, closed := range []bool{false, true} {
		h := breadthTestHist(t0, []int{1, 2, 0, 1, 2, 3, 4, 5, 3, 1, 0})
		since := t0.Add(25 * time.Minute)
		ev := breadthEvent("svc-a", []BreadthOp{{Op: "op-a", P95Ms: 1500, BaseMs: 500, Ratio: 3}}, breadthRun{since: since, ticks: 2},
			t0.Add(30*time.Minute), chstore.MLConfig{BucketSec: 300})
		evs := []chstore.MLEvent{ev}
		attachBreadthRecords(h, chstore.MLConfig{BucketSec: 300}, evs, closed, func(string) time.Time { return t0.Add(30 * time.Minute) }, t0.Add(time.Hour))
		var rec EventRecord
		if err := json.Unmarshal([]byte(evs[0].Record), &rec); err != nil {
			t.Fatalf("kayıt yazılmalı: %v", err)
		}
		if vs := ValidateRecord(rec); len(vs) != 0 {
			t.Fatalf("closed=%v doğrulayıcıdan geçmeli: %+v", closed, vs)
		}
		if rec.OnsetAt != since.UnixNano() || rec.OnsetAt != evs[0].StartedAt || rec.PeakValue != 5 || rec.ValueAtDetection != 4 || rec.Baseline.Hi != 2 {
			t.Fatalf("seriden türetme: onset=%d tepe=%v det=%v", rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
		}
		if closed != (rec.EndedAt == t0.Add(45*time.Minute).UnixNano()) || rec.Volume != nil || rec.VolumeReason == "" || rec.Class != ClassServiceAnomaly ||
			len(rec.Gates) != 0 || len(rec.Evidence) != 2 || rec.Evidence[0].Independent || rec.Evidence[1].Independent {
			t.Fatalf("bitiş / hacim / kanıt (üyeler bağımsız değil): %+v", rec)
		}
		raw, _ := json.Marshal(rec)
		var back EventRecord
		if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
			t.Fatalf("gidiş-dönüş: %v", err)
		}
	}
}

// Kalan örnek: lider değişimi — geçmiş koşulun ortasından başlıyor, onset'in
// öncesi görünmüyor → publish_state=invalid (kart eski yola düşer).
func TestBreadthRecordInvalidWithoutPreOnsetHistory(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	h := breadthTestHist(t0, []int{4, 4, 5})
	ev := breadthEvent("svc-a", nil, breadthRun{since: t0, ticks: 2}, t0.Add(5*time.Minute), chstore.MLConfig{BucketSec: 300})
	evs := []chstore.MLEvent{ev}
	attachBreadthRecords(h, chstore.MLConfig{BucketSec: 300}, evs, false, func(string) time.Time { return t0.Add(5 * time.Minute) }, t0.Add(time.Hour))
	if out := applyRecordContractShadow(evs[0]); evs[0].Record == "" || out.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", out.PublishState)
	}
}
