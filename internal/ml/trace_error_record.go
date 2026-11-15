package ml

// trace_error_record.go — v0.10.2223, anomali kartı sözleşmesi H7
// (docs/ml/anomaly-card-audit.md §9–§10): operasyon hata sayısı dedektörü
// (trace_error_count, trace_errors.go) olay kaydını doldurur.
//
// Seri: dedektörün ölçtüğü şey — spanmetrics_1m'de servis × operasyonun
// giriş span'i (server + consumer) hata sayısı, kova (BucketSec) toplamı.
// Snapshot aynı MV'nin dakika okumasından (chstore.MLTraceErrorMinutes)
// 1 dk adımlı KAYAN kova toplamı (her nokta: o dakikayla biten BucketSec
// penceresi; hatasız dakika = 0, MV eksiksiz). Tepe, tespit anı değeri,
// onset ve bitiş YALNIZ bu seriden. Bant alarmı veren bant: üst sınır
// normalMax = max(medyan + CriticalZ·ölçek, tabanın p95'i), alt 0
// (judgeErrCount yalnız yukarı); beklenen = medyan. Hacim: spanmetrics_1m
// çağrı sayısı örneklenmiş (başarılı trace'ler) → null + neden. Tespit
// mantığı, eşikler, started_at değişmez; gates[] boş, sınıf service_anomaly.
// Maliyet: açık olay başına tik başına 1 CH okuması (MV, ORDER BY öneki),
// en çok terrRecordMaxPerTick.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

const (
	terrRecordMaxPerTick = 20
	terrBandMethod       = "class_days_hour±1_max(median+z·scale,p95)"
	terrVolumeReason     = "spanmetrics_1m çağrı sayısı örneklenmiş (başarılı trace'ler); oran/hacim CH'den hesaplanamaz"
)

// terrSeriesQuery — SAF: seri tanımının çözülmüş metni (tik sorgusunun süzgeci).
func terrSeriesQuery(svc, op string, bucketSec int) string {
	return fmt.Sprintf("spanmetrics_1m: sum(countMerge(error_state)) WHERE service_name = %q AND name = %q AND status_code = 'error' AND %s; %ds pencere",
		svc, op, chstore.EntrySpanKindsWhere, bucketSec)
}

// terrGrid — SAF: dakika hata sayıları → [from, to] üzerinde 1 dk adımlı
// kayan pencere (winSec) toplamı. pts from − winSec'ten başlayabilir.
// 720 nokta tavanı aşılırsa adım 60 sn'nin katına büyür (noktada değer aynı
// kayan toplam).
func terrGrid(pts []chstore.SpanMetricPoint, from, to time.Time, winSec int) (t0, step int64, v []float64) {
	t0 = from.Unix() / 60 * 60
	end := to.Unix() / 60 * 60
	if end < t0 {
		return 0, 0, nil
	}
	w := int64(max(winSec/60, 1))
	start := t0 - (w-1)*60
	n := int((end-start)/60) + 1
	per := make([]float64, n)
	for _, p := range pts {
		i := (p.Time/1e9/60*60 - start) / 60
		if i >= 0 && i < int64(n) && p.Value > 0 {
			per[i] += p.Value
		}
	}
	roll := make([]float64, n)
	sum := 0.0
	for i := range per {
		sum += per[i]
		if int64(i) >= w {
			sum -= per[int64(i)-w]
		}
		roll[i] = sum
	}
	roll = roll[w-1:] // roll[0] = t0
	k := int64(1)
	for int64(len(roll)) > recordMaxPoints*k {
		k++
	}
	for i := int64(0); i < int64(len(roll)); i += k {
		v = append(v, roll[i])
	}
	return t0, 60 * k, v
}

// TerrRecordInput — BuildTraceErrorRecord girdisi (saf).
type TerrRecordInput struct {
	Service, Operation string
	BucketSec          int
	Median, NormalMax  float64
	DetectedAt         time.Time
	Closed             bool
	Minutes            []chstore.SpanMetricPoint // dakika başına hata
	From, To           time.Time
	Confirmers         []string
}

// BuildTraceErrorRecord — SAF: trace_error_count olay kaydı. ok=false: seri yok.
func BuildTraceErrorRecord(in TerrRecordInput) (EventRecord, bool) {
	t0, step, v := terrGrid(in.Minutes, in.From, in.To, in.BucketSec)
	if len(v) == 0 {
		return EventRecord{}, false
	}
	lo, hi := 0.0, in.NormalMax
	onset, det, end, peak := seriesWindow(v, t0, step, lo, hi, "up", in.DetectedAt, in.Closed)
	at := func(i int) int64 { return (t0 + int64(i)*step) * 1e9 }
	los, his := make([]float64, len(v)), make([]float64, len(v))
	for i := range v {
		los[i], his[i] = lo, hi
	}
	rec := EventRecord{
		V:       RecordVersion,
		Subject: RecordSubject{EntityID: entity.ServiceID(in.Service), Service: in.Service, Operation: in.Operation, Resolved: true},
		Series: RecordSeries{Source: "ch:spanmetrics_1m", Query: terrSeriesQuery(in.Service, in.Operation, in.BucketSec),
			Aggregation: "sum", Unit: "count", StepSec: uint32(in.BucketSec)},
		Baseline:  RecordBaseline{Expected: in.Median, Lo: lo, Hi: hi, Method: terrBandMethod, StepSec: uint32(in.BucketSec)},
		Direction: "up", OnsetAt: at(onset), DetectedAt: at(det),
		PeakValue: peak, ValueAtDetection: v[det], ExpectedValue: in.Median,
		VolumeReason: terrVolumeReason,
		Class:        ClassServiceAnomaly,
		Snapshot:     RecordSnapshot{T0: t0, StepSec: uint32(step), Aggregation: "sum", V: v, Lo: los, Hi: his},
	}
	if in.Closed {
		rec.EndedAt = at(end)
	}
	rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "series", Source: "ch",
		Summary: "Operasyon giriş span'i hata sayısı, spanmetrics_1m (hata trace'leri örneklenmez)", Independent: false})
	for _, id := range in.Confirmers {
		rec.Evidence = append(rec.Evidence, RecordEvidence{Kind: "confirm", Source: "ml",
			Summary: "Aynı serviste başka dedektörün olayı: " + id, Independent: true})
	}
	// Başlık yalnız alanlardan; operasyon adı içermez (rakamlı adlar sayı kuralını bozar).
	rec.Title = fmt.Sprintf("Hata span sayısı tespit anında %s, normal üst sınır %s", trInt(rec.ValueAtDetection), metricNum(hi))
	if in.Closed {
		rec.Title += " · tepe " + trInt(peak)
	}
	return rec, true
}

// attachTraceErrorRecords — recorder: trace_error_count olaylarına kayıt
// (yazım öncesi, applyRecordContract doğrular). attachLagRecords ile aynı akış.
func attachTraceErrorRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget *int, now time.Time) {
	if deps.Store == nil {
		return
	}
	for i := range evs {
		ev := evs[i]
		if ev.Detector != detTraceErrorCount || ev.Stage == chstore.MLStageChronic || ev.Code == "" || *budget <= 0 {
			continue
		}
		m := evidenceMap(ev.Evidence)
		med, ok1 := m["median"].(float64)
		nmax, ok2 := m["normalMax"].(float64)
		if !ok1 || !ok2 {
			continue // tik kanıtı yok (eski olay): kayıt yazılmaz
		}
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		bucket := time.Duration(cfg.BucketSec) * time.Second
		from := det.Add(-time.Duration(cfg.DwellBuckets)*bucket - lagRecordPad)
		to := now
		if closed {
			to = time.Unix(0, ev.LastSeen).Add(lagRecordPad)
			if to.After(now) {
				to = now
			}
		}
		*budget--
		qctx, cancel := context.WithTimeout(ctx, sourceCheckTimeout)
		pts, err := deps.Store.MLTraceErrorMinutes(qctx, ev.Service, ev.Code, from.Add(-bucket), to.Add(time.Minute))
		cancel()
		if err != nil {
			continue // önceki kayıt (varsa) aynen kalır
		}
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildTraceErrorRecord(TerrRecordInput{Service: ev.Service, Operation: ev.Code, BucketSec: cfg.BucketSec,
			Median: med, NormalMax: nmax, DetectedAt: det, Closed: closed, Minutes: pts, From: from, To: to, Confirmers: conf})
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
