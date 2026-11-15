package ml

// change_point_record.go — v0.10.2235, anomali kartı sözleşmesi H10
// (docs/ml/anomaly-card-audit.md §9–§10): kırılma noktası dedektörleri
// (trace_change_point — route p95, change_point — servis log sayısı / hata
// logu sayısı; change_point.go CUSUM çekirdeği) olay kaydını doldurur.
//
// Seri: dedektörün KOŞTUĞU ml_series parçası (hedef kova, max(count)) —
// yeniden okunmaz: tik anında CUSUM'un gördüğü bitişik kuyruk (kırılmadan
// 30 dk önce → son kova) ve her kovanın referans bandı (aynı takvim sınıfı
// günlerin aynı dilimi: Q50 ± k·σ, σ = cpSigma; CUSUM'un katkı eşiği) kanıta
// dondurulur (cpT0/cpV/cpLo/cpHi). Kayıt bunu 1 dk ızgaraya basamak olarak
// açar (kova değeri kova boyunca); onset, tepe, tespit değeri, bitiş YALNIZ bu
// seriden ve kova başına banttan. started_at (hesaplanmış kırılma anı)
// değişmez; onset_at seriden türetilir. Hacim: log parçalarında aynı seri
// (kova sayısı / dakika), trace p95'te yok (gerekçeli). Kırılma ±15 dk
// içindeki rollout bağımsız kanıt. gates[] boş, sınıf service_anomaly.
// Maliyet: ek sorgu yok (kanıt ≤ ~80 kova × 3 sayı).

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

const (
	cpBandMethod        = "class_days_slot_q50±k·sigma_cusum"
	cpTraceVolumeReason = "kırılma serisi route p95'i; bu seride çağrı sayısı yok"
)

func cpRound(x float64) float64 { return math.Round(x*1e4) / 1e4 }

// cpFreeze — SAF: CUSUM'un gördüğü bitişik kuyruğu (kırılmadan lagRecordPad
// önceye kadar) ve kova başına bandı kanıta yazar. Kuyruk yoksa yazmaz.
func cpFreeze(evm map[string]any, pts []chstore.MLPoint, ref map[int]chstore.MLProfileStat, slotOf func(int64) int, step, at int64) {
	lead := at - int64(lagRecordPad.Seconds())
	start := len(pts)
	for j := len(pts) - 1; j >= 0; j-- {
		if _, ok := ref[slotOf(pts[j].T)]; !ok || pts[j].T < lead || (j < len(pts)-1 && pts[j+1].T-pts[j].T != step) {
			break
		}
		start = j
	}
	if start >= len(pts) {
		return
	}
	var v, lo, hi []float64
	for _, p := range pts[start:] {
		st := ref[slotOf(p.T)]
		sg := cpSigma(st)
		v, lo, hi = append(v, cpRound(p.V)), append(lo, cpRound(st.Q50-cpK*sg)), append(hi, cpRound(st.Q50+cpK*sg))
	}
	evm["cpT0"], evm["cpV"], evm["cpLo"], evm["cpHi"] = pts[start].T, v, lo, hi
}

// CPRecordInput — BuildChangePointRecord girdisi (saf); seri hedef kova adımında.
type CPRecordInput struct {
	Detector, Signal, Service, Route, Part string
	SeriesService                          string // ml_series servis adı ('' = Service; log: ES adı)
	BucketSec                              int
	T0                                     int64 // ilk kova (unix s)
	V, Lo, Hi                              []float64
	Direction                              string
	DetectedAt                             time.Time
	Closed                                 bool
	Confirmers                             []string
	Rollouts                               int
}

func sqlLit(s string) string { return "'" + strings.ReplaceAll(s, "'", "\\'") + "'" }

// BuildChangePointRecord — SAF. ok=false: seri yok / adım 1 dk'nın katı değil.
func BuildChangePointRecord(in CPRecordInput) (EventRecord, bool) {
	k := in.BucketSec / recordSnapshotStepSec
	if k < 1 || in.BucketSec%recordSnapshotStepSec != 0 || len(in.V) == 0 || len(in.Lo) != len(in.V) || len(in.Hi) != len(in.V) {
		return EventRecord{}, false
	}
	if over := len(in.V)*k - recordMaxPoints; over > 0 { // baştan kırp
		cut := (over + k - 1) / k
		in.T0 += int64(cut * in.BucketSec)
		in.V, in.Lo, in.Hi = in.V[cut:], in.Lo[cut:], in.Hi[cut:]
	}
	n := len(in.V) * k
	v, lo, hi := make([]float64, n), make([]float64, n), make([]float64, n)
	for i := range v {
		v[i], lo[i], hi[i] = in.V[i/k], in.Lo[i/k], in.Hi[i/k]
	}
	dir := in.Direction
	if dir != "down" {
		dir = "up"
	}
	out := func(i int) bool { return v[i] > hi[i] }
	if dir == "down" {
		out = func(i int) bool { return v[i] < lo[i] }
	}
	det := min(max(int((in.DetectedAt.Unix()-in.T0)/recordSnapshotStepSec), 0), n-1)
	onset := det
	for onset >= 0 && !out(onset) {
		onset--
	}
	if onset < 0 {
		onset = det
	} else {
		for onset > 0 && out(onset-1) {
			onset--
		}
	}
	end := n - 1
	if in.Closed {
		for j := det + 1; j < n; j++ {
			if !out(j) {
				end = j
				break
			}
		}
	}
	peak := v[onset]
	for _, x := range v[onset : end+1] {
		if (dir == "down" && x < peak) || (dir != "down" && x > peak) {
			peak = x
		}
	}
	at := func(i int) int64 { return (in.T0 + int64(i)*recordSnapshotStepSec) * 1e9 }
	trace := in.Signal == chstore.MLSignalTrace
	seriesSvc := in.SeriesService
	if seriesSvc == "" {
		seriesSvc = in.Service
	}
	agg, unit := "count", "count"
	if trace {
		agg, unit = "p95", "ms"
	}
	exp := (lo[det] + hi[det]) / 2
	rec := EventRecord{
		V:       RecordVersion,
		Subject: RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Operation: in.Route, Resolved: true},
		Series: RecordSeries{Source: "ch:ml_series", Aggregation: agg, Unit: unit, StepSec: uint32(in.BucketSec),
			Query: fmt.Sprintf("SELECT toUnixTimestamp(bucket) AS t, max(count) AS v FROM ml_series WHERE signal = %s AND service = %s AND part = %s AND bucket_sec = %d GROUP BY t ORDER BY t",
				sqlLit(in.Signal), sqlLit(seriesSvc), sqlLit(in.Part), in.BucketSec)},
		Baseline:  RecordBaseline{Expected: exp, Lo: lo[det], Hi: hi[det], Method: cpBandMethod, StepSec: uint32(in.BucketSec)},
		Direction: dir, OnsetAt: at(onset), DetectedAt: at(det),
		PeakValue: peak, ValueAtDetection: v[det], ExpectedValue: exp,
		Class:    ClassServiceAnomaly,
		Snapshot: RecordSnapshot{T0: in.T0, StepSec: recordSnapshotStepSec, Aggregation: agg, V: v, Lo: lo, Hi: hi},
	}
	if in.Closed {
		rec.EndedAt = at(end)
	}
	head, label, unitWord := "p95", "Endpoint p95", " ms"
	if !trace {
		head, label, unitWord = "Kova başına log sayısı", "Servis log sayısı", " log"
		if in.Part == partError {
			head, label = "Kova başına hata logu sayısı", "Servis hata logu sayısı"
		}
		nn := make([]float64, n)
		vol := 0.0
		for i, x := range v {
			nn[i] = math.Max(x, 0) / float64(k)
			if i >= onset && i <= end {
				vol += nn[i]
			}
		}
		vol = math.Round(vol)
		rec.Volume, rec.Snapshot.N = &vol, nn
	} else {
		rec.VolumeReason = cpTraceVolumeReason
	}
	rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "series", Source: "ch:ml_series",
		Summary: label + " kırılma serisi, CUSUM referansı aynı takvim sınıfı günlerin aynı dilimi", Independent: false})
	if in.Rollouts > 0 {
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "rollout", Source: "deploys",
			Summary: fmt.Sprintf("Kırılma anının ±15 dk içinde servisin %d rollout'u (eşleme, nedensellik iddiası değil)", in.Rollouts), Independent: true})
	}
	for _, id := range in.Confirmers {
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "confirm", Source: "ml",
			Summary: "Aynı serviste başka dedektörün olayı: " + id, Independent: true})
	}
	f := func(x float64) string { return trInt(x) + unitWord }
	bound, word, ext := rec.Baseline.Hi, "üst", "tepe"
	if dir == "down" {
		bound, word, ext = rec.Baseline.Lo, "alt", "dip"
	}
	rec.Title = fmt.Sprintf("%s tespit anında %s, normal %s sınır %s", head, f(rec.ValueAtDetection), word, f(bound))
	if in.Closed {
		rec.Title += " · " + ext + " " + f(peak)
	}
	return rec, true
}

func anyFloats(x any) []float64 {
	xs, _ := x.([]any)
	out := make([]float64, 0, len(xs))
	for _, e := range xs {
		f, ok := e.(float64)
		if !ok {
			return nil
		}
		out = append(out, f)
	}
	return out
}

// attachChangePointRecords — recorder: change_point / trace_change_point
// olaylarına kayıt (yazım öncesi, applyRecordContract doğrular). Sorgu yok.
func attachChangePointRecords(evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, now time.Time) {
	for i := range evs {
		ev := evs[i]
		if (ev.Detector != detChangePoint && ev.Detector != detTraceChangePoint) || ev.Stage == chstore.MLStageChronic {
			continue
		}
		m := evidenceMap(ev.Evidence)
		t0, _ := m["cpT0"].(float64)
		part, _ := m["part"].(string)
		bs, _ := m["bucketSec"].(float64)
		if t0 <= 0 || part == "" || bs <= 0 {
			continue // tik kanıtı yok (eski olay): kayıt yazılmaz
		}
		rollouts, _ := m["rollouts"].([]any)
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		route := ""
		if ev.Detector == detTraceChangePoint {
			route = ev.Code
		}
		rec, ok := BuildChangePointRecord(CPRecordInput{Detector: ev.Detector, Signal: ev.Signal, Service: ev.Service, Route: route, Part: part,
			BucketSec: int(bs), T0: int64(t0), V: anyFloats(m["cpV"]), Lo: anyFloats(m["cpLo"]), Hi: anyFloats(m["cpHi"]),
			Direction: ev.Direction, DetectedAt: det, Closed: closed, Confirmers: conf, Rollouts: len(rollouts)})
		if !ok {
			continue
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		ev.Record, ev.Class, ev.OnsetAt, ev.PublishState = string(raw), rec.Class, rec.OnsetAt, PublishPublished
		evs[i] = ev
	}
}
