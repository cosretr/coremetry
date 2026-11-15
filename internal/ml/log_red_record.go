package ml

// log_red_record.go — v0.10.2236, anomali kartı sözleşmesi H11
// (docs/ml/anomaly-card-audit.md §9–§10): log RED dedektörleri (log_volume,
// error_rate — hata logu sayısı, service_silent; log_red.go) olay kaydını
// doldurur.
//
// Seri: dedektörün koştuğu kova serisi — tikte ES toplamasından (logstore
// MLLogAggregate) ml_series'e yazılan servis × parça (_total / _error) kova
// sayısı. Snapshot için ES'e GİDİLMEZ: aynı seri ml_series'ten tek servis,
// zaman sınırlı CH okumasıyla (MLSeriesRange: LIMIT + max_execution_time) gelir,
// 1 dk ızgarada basamak (H10 kurucusu). Eksik kova 0 (dedektörün curOf'u da
// öyle sayar; susma serisi tam bu). Bant alarmı veren medyan ±
// criticalZ·effMAD (judgeMM). Hacim aynı seriden (olay penceresindeki log
// satırı). Tespit mantığı, eşikler, started_at değişmez; gates[] boş, sınıf
// service_anomaly. Maliyet: açık olay başına tik başına 1 CH okuması (ml_series),
// 0 ES sorgusu, tik başına en çok logRecordMaxPerTick.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const logRecordMaxPerTick = 20

// logREDGrid — SAF: [from, last] kova ızgarası; eksik kova 0, çift satır
// (FINAL yok) en büyüğü (MLSeriesRecent'in max(count)'u).
func logREDGrid(pts []chstore.MLPoint, from, last int64, step int64) []float64 {
	if step <= 0 || last < from {
		return nil
	}
	v := make([]float64, (last-from)/step+1)
	for _, p := range pts {
		if i := (p.T - from) / step; p.T >= from && i < int64(len(v)) && p.T%step == from%step && p.V > v[i] {
			v[i] = p.V
		}
	}
	return v
}

// attachLogREDRecords — recorder: log_volume / error_rate / service_silent
// olaylarına kayıt (yazım öncesi, applyRecordContract doğrular).
func attachLogREDRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget *int, now time.Time) {
	if deps.Store == nil || cfg.BucketSec <= 0 {
		return
	}
	step := int64(cfg.BucketSec)
	for i := range evs {
		ev := evs[i]
		if (ev.Detector != detLogVolume && ev.Detector != detErrorRate && ev.Detector != detServiceSilent) ||
			ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		part := partTotal
		if ev.Detector == detErrorRate {
			part = partError
		}
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		bucket := time.Duration(cfg.BucketSec) * time.Second
		from := det.Add(-time.Duration(cfg.DwellBuckets)*bucket-lagRecordPad).Unix() / step * step
		last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec).Unix()
		if closed {
			last = min(last, time.Unix(0, ev.LastSeen).Add(lagRecordPad).Unix()/step*step)
		}
		if last <= from {
			continue
		}
		svcName := logServiceOf(ev)
		*budget--
		all, err := deps.Store.MLSeriesRange(ctx, chstore.MLSignalLog, svcName, time.Unix(from, 0), time.Unix(last+step, 0), cfg.BucketSec)
		if err != nil {
			continue // önceki kayıt (varsa) aynen kalır
		}
		v := logREDGrid(all[part], from, last, step)
		m := evidenceMap(ev.Evidence)
		mad, _ := m["mad"].(float64)
		lo, hi := lagBand(ev.Baseline, mad, cfg.CriticalZ)
		los, his := make([]float64, len(v)), make([]float64, len(v))
		for j := range v {
			los[j], his[j] = lo, hi
		}
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildLogREDRecord(CPRecordInput{Detector: ev.Detector, Signal: chstore.MLSignalLog, Service: ev.Service, SeriesService: svcName,
			Part: part, BucketSec: cfg.BucketSec, T0: from, V: v, Lo: los, Hi: his, Direction: ev.Direction, DetectedAt: det,
			Closed: closed, Confirmers: conf})
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

// BuildLogREDRecord — SAF: kova serisi kurucusu (H10) + alarm bandı yöntemi
// ve log RED kanıt metni.
func BuildLogREDRecord(in CPRecordInput) (EventRecord, bool) {
	rec, ok := BuildChangePointRecord(in)
	if !ok {
		return rec, false
	}
	rec.Baseline.Method = lagBandMethod
	label := "Servis log sayısı"
	if in.Part == partError {
		label = "Servis hata logu sayısı"
	}
	rec.Evidence[0].Summary = label + ", tikteki ES sayımının ml_series kova serisi"
	return rec, true
}
