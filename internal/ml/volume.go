package ml

// volume.go — v0.10.2242, anomali kartı Dilim B (docs/ml/anomaly-card-audit.md
// §10 B, §12 K5/K6/K15): ML telemetri hacmi dedektörü `svc_telemetry_volume`.
// Evaluator'ın `svc_volume_spike` kuralının (24 sa / önceki 24 sa) yerine
// geçer; o kural emekli (iki yazıcı aynı anda yaşamaz, kimlik uzayı ayrık).
//
// ÖLÇÜ: istek başına span = service_summary_5m span / spanmetrics_1m giriş
// span'i (server + consumer), 5 dk kova (chstore.MLServiceVolume). İki sayı da
// MV; tail sampling trace düzeyinde olduğundan oran örneklemeden bağımsız.
// İstek hızı artıp oran sabitse olay YOK (trafik artışı; throughput/RED
// dedektörlerinin işi); oran değiştiyse "telemetri hacmi değişimi". Giriş
// span'i olmayan serviste oran tanımsız → ham span sayısı (5 dk) ölçülür ve
// olay her zaman telemetry_volume (audit B riski).
//
// TABAN: saatlik oran ve saatlik span sayısı ml_series'e yazılır (signal
// "volume", part "ratio" | "spans", bucket_sec 3600); okuma aynı takvim
// sınıfı günler × yerel saat ±1 (K6: 4 haftada ≤ 12 örnek). Küçük örnek
// kuralı (K6): üst sınır max(medyan + z·1,4826·MAD, en büyük × 1,5, en büyük +
// mutlak fark), alt sınır simetrik (≥ 0). Eşikler kod sabiti (K2).
//
// KARAR: son volDwell kovanın HER BİRİ aynı yönde bant dışı VE hacim tabanı
// (saatlik span ≥ volMinSpansHour; güncel ya da taban medyanı) sağlanıyorsa
// olay. Aşağı yönde kovada span 0 ise olay yok (tam susma service_silent /
// throughput dedektörlerinin işi). Başlangıç: tik penceresinde bant dışı
// koşunun ilk kovası (kayıtta onset seriden yeniden türer); bitiş: seri banda
// dönünce koşu kapanır (episodeCloseMisses). Rejim bastırması (6 sa) bu
// dedektöre uygulanmaz; tavan 7 gün (K15, quality.go).
//
// Ayrı dedektör anahtarı YOK (yeni ayar yok): ML ana anahtarı açıkken koşar.
// Lider-only (Recorder.tick). Tik başına CH: 1 MV okuması + 1 ml_series
// yazımı + 1 taban okuması (ml_series). VM sorgusu yok (olay kaydının yan
// serileri volume_record.go'da, tavanlı).

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	detTelemetryVolume = chstore.MLDetSvcTelemetryVolume
	volSignal          = "volume" // ml_series.signal (trace okuyucularından ayrık)
	volPartRatio       = "ratio"
	volPartSpans       = "spans"
	volBucketSec       = 300 // service_summary_5m çözünürlüğü
	volDwell           = 3   // ardışık kova (15 dk)
	volMinSamples      = 4   // K6: dilim başına az örnek; altında sessiz
	volMinSpansHour    = 6000
	volMargin          = 1.5 // küçük örnek kuralı: en büyük × pay
	volAbsRatio        = 1.0 // istek başına span, mutlak fark
	volAbsSpans5m      = 500 // 5 dk span, mutlak fark
	volMaxDuration     = 7 * 24 * time.Hour
	volBandMethod      = "class_days_hour±1_max(median+z·mad,max×1.5,max+abs)"
)

// Volume — dedektör (günlük olay tavanı + VM etiket önbelleği süreç içi).
type Volume struct {
	mu     sync.Mutex
	daily  map[string]int
	day    string
	labels map[string]traceFamilyLabels // RED ailesi → etiketler (yan seri; saatlik)
}

func NewVolume() *Volume {
	return &Volume{daily: map[string]int{}, labels: map[string]traceFamilyLabels{}}
}

func (d *Volume) Name() string { return detTelemetryVolume }

// Enabled — ayrı anahtar yok: ML ana anahtarı.
func (d *Volume) Enabled(cfg chstore.MLConfig) bool { return cfg.Enabled }

func (d *Volume) dayCap(cfg chstore.MLConfig, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	day := now.Format("2006-01-02")
	if d.day != day {
		d.day, d.daily = day, map[string]int{}
	}
	if d.daily[detTelemetryVolume] >= cfg.DailyEventCap {
		return false
	}
	d.daily[detTelemetryVolume]++
	return true
}

func (d *Volume) refundCap(det string) {
	d.mu.Lock()
	refundDaily(d.daily, det)
	d.mu.Unlock()
}

// ── saf parçalar ─────────────────────────────────────────────

// volBand — SAF: alarm bandı (seri biriminde). Küçük örnek kuralı (K6).
func volBand(base []float64, z, abs float64) (med, lo, hi float64) {
	med, mad := medianMAD(base)
	s := 1.4826 * mad
	mx, mn := base[0], base[0]
	for _, x := range base {
		mx, mn = math.Max(mx, x), math.Min(mn, x)
	}
	hi = math.Max(med+z*s, math.Max(mx*volMargin, mx+abs))
	lo = math.Max(0, math.Min(med-z*s, math.Min(mn/volMargin, mn-abs)))
	return med, lo, hi
}

// volInput — judgeVolume girdisi: tik penceresinin son volDwell kovası.
type volInput struct {
	Spans, Entries      []float64 // kova başına (eksik kova 0)
	HourSpans           float64   // son 60 dk span toplamı
	BaseRatio, BaseSpan []float64 // saatlik taban örnekleri (oran | saatlik span)
	Z                   float64
}

type volJudgement struct {
	Fire                  bool
	Measure               string // ratio | spans
	Dir                   string
	Cur                   []float64 // ölçü serisi (dwell)
	Median, Lo, Hi, Score float64
	Samples               int
	Reason                string
}

// judgeVolume — SAF karar. Ölçü: oran (giriş span'i her kovada > 0 ve taban
// yeterli) yoksa ham 5 dk span sayısı.
func judgeVolume(in volInput) volJudgement {
	j := volJudgement{}
	n := len(in.Spans)
	if n < volDwell || len(in.Entries) != n {
		j.Reason = "dwell: yeterli kova yok"
		return j
	}
	entriesOK := true
	for _, e := range in.Entries {
		entriesOK = entriesOK && e > 0
	}
	var base []float64
	var abs float64
	switch {
	case entriesOK && len(in.BaseRatio) >= volMinSamples:
		j.Measure, base, abs = volPartRatio, in.BaseRatio, volAbsRatio
		j.Cur = make([]float64, n)
		for i := range in.Spans {
			j.Cur[i] = in.Spans[i] / in.Entries[i]
		}
	case len(in.BaseSpan) >= volMinSamples:
		j.Measure, abs = volPartSpans, volAbsSpans5m
		base = make([]float64, len(in.BaseSpan))
		for i, x := range in.BaseSpan {
			base[i] = x * volBucketSec / 3600 // saatlik → 5 dk
		}
		j.Cur = append([]float64(nil), in.Spans...)
	default:
		j.Reason = "eksik veri: taban yetersiz"
		return j
	}
	j.Samples = len(base)
	baseSpanMed, _ := medianMAD(in.BaseSpan)
	if math.Max(in.HourSpans, baseSpanMed) < volMinSpansHour {
		j.Reason = "hacim tabanın altında"
		return j
	}
	j.Median, j.Lo, j.Hi = volBand(base, in.Z, abs)
	up, down := true, true
	for i, v := range j.Cur {
		up = up && v > j.Hi
		down = down && v < j.Lo && in.Spans[i] > 0
	}
	last := j.Cur[n-1]
	switch {
	case up:
		j.Dir = "up"
	case down:
		j.Dir = "down"
	default:
		j.Reason = "dwell: bant içinde"
		return j
	}
	if j.Median > 0 {
		j.Score = math.Abs(last-j.Median) / j.Median
	}
	j.Fire = true
	return j
}

// volOnsetBucket — SAF: tik penceresinde son kovadan geriye bant dışı (yönünde)
// koşunun ilk kovası. vals kova sırasıyla, ts aynı uzunlukta.
func volOnsetBucket(ts []int64, vals []float64, lo, hi float64, dir string) int64 {
	if len(ts) == 0 {
		return 0
	}
	i := len(vals) - 1
	for i > 0 {
		v := vals[i-1]
		if (dir == "down" && v < lo) || (dir != "down" && v > hi) {
			i--
			continue
		}
		break
	}
	return ts[i]
}

// volHourRows — SAF: tamamlanmış saatlerin ml_series satırları (oran yalnız
// giriş span'i > 0 iken). Saat tamam = 12 kovanın hepsi end'den önce.
func volHourRows(rows []chstore.MLVolumeRow, end int64) []chstore.MLSeriesRow {
	type acc struct{ spans, entries float64 }
	hours := map[string]map[int64]*acc{}
	for _, r := range rows {
		h := r.Bucket / 3600 * 3600
		if h+3600 > end {
			continue
		}
		if hours[r.Service] == nil {
			hours[r.Service] = map[int64]*acc{}
		}
		a := hours[r.Service][h]
		if a == nil {
			a = &acc{}
			hours[r.Service][h] = a
		}
		a.spans += float64(r.Spans)
		a.entries += float64(r.Entries)
	}
	var out []chstore.MLSeriesRow
	for svc, byH := range hours {
		for h, a := range byH {
			t := time.Unix(h, 0)
			out = append(out, chstore.MLSeriesRow{Signal: volSignal, Service: svc, Part: volPartSpans, Bucket: t, BucketSec: 3600, Count: a.spans})
			if a.entries > 0 {
				out = append(out, chstore.MLSeriesRow{Signal: volSignal, Service: svc, Part: volPartRatio, Bucket: t, BucketSec: 3600, Count: a.spans / a.entries})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		if !out[i].Bucket.Equal(out[j].Bucket) {
			return out[i].Bucket.Before(out[j].Bucket)
		}
		return out[i].Part < out[j].Part
	})
	return out
}

// volEvent — SAF: olay. Baseline = beklenen (medyan), Observed = son kova.
func volEvent(svc string, j volJudgement, onset, last time.Time, hourSpans float64, class string, cfg chstore.MLConfig) chstore.MLEvent {
	raw, _ := json.Marshal(map[string]any{"source": "service_summary_5m+spanmetrics_1m", "measure": j.Measure, "median": j.Median,
		"lo": j.Lo, "hi": j.Hi, "method": volBandMethod, "samples": j.Samples, "dayClass": class, "dwell": volDwell,
		"bucketSec": volBucketSec, "hourSpans": hourSpans, "baselineDays": cfg.BaselineDays})
	return chstore.MLEvent{
		ID: chstore.MLEventID(detTelemetryVolume, svc, ""), Detector: detTelemetryVolume, Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow,
		Service: svc, StartedAt: onset.UnixNano(), LastSeen: last.Add(volBucketSec * time.Second).UnixNano(),
		WindowSec: volBucketSec, Baseline: j.Median, Observed: j.Cur[len(j.Cur)-1], Score: j.Score, Direction: j.Dir, Evidence: string(raw),
	}
}

// ── tik ──────────────────────────────────────────────────────

func (d *Volume) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	bucket := time.Duration(volBucketSec) * time.Second
	last := lastCompleteBucket(now, volBucketSec, cfg.IngestLagSec)
	end := last.Add(bucket)
	from := last.Truncate(time.Hour).Add(-time.Hour) // önceki tam saat + bu saat
	rows, err := deps.Store.MLServiceVolume(ctx, "", from, end, volBucketSec)
	if err != nil {
		return nil, err
	}
	if err := deps.insertSeries(ctx, volHourRows(rows, end.Unix())); err != nil {
		return nil, err
	}
	bySvc := map[string]map[int64]chstore.MLVolumeRow{}
	for _, r := range rows {
		if bySvc[r.Service] == nil {
			bySvc[r.Service] = map[int64]chstore.MLVolumeRow{}
		}
		bySvc[r.Service][r.Bucket] = r
	}
	global.series.Add(int64(len(bySvc)))
	if len(bySvc) == 0 {
		return nil, nil
	}
	sens := deps.Store.AnomalySensitivityForDetectors()
	loc := OperatorLocation(sens)
	dates, class := baselineDates(last, cfg.BaselineDays, sens.Calendar, loc)
	h := last.In(loc).Hour()
	base, err := deps.Store.MLBaseline(ctx, chstore.MLBaselineRequest{Signal: volSignal, Dates: dates, HourFrom: max(h-1, 0), HourTo: min(h+1, 23),
		TargetSec: 3600, TZ: loc.String()})
	if err != nil {
		return nil, err
	}
	svcs := make([]string, 0, len(bySvc))
	for s := range bySvc {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)
	var evs []chstore.MLEvent
	for _, svc := range svcs {
		byB := bySvc[svc]
		var ts []int64
		var spans, entries []float64
		for b := from.Unix(); b <= last.Unix(); b += volBucketSec {
			r := byB[b]
			ts, spans, entries = append(ts, b), append(spans, float64(r.Spans)), append(entries, float64(r.Entries))
		}
		n := len(ts)
		hour := 0.0
		for _, x := range spans[max(n-12, 0):] {
			hour += x
		}
		j := judgeVolume(volInput{Spans: spans[n-volDwell:], Entries: entries[n-volDwell:], HourSpans: hour,
			BaseRatio: base[chstore.MLSeriesKey(svc, volPartRatio)], BaseSpan: base[chstore.MLSeriesKey(svc, volPartSpans)], Z: cfg.CriticalZ})
		if !j.Fire || !(deps.extends(detTelemetryVolume, svc, "") || d.dayCap(cfg, now)) {
			continue
		}
		vals := spans
		if j.Measure == volPartRatio {
			vals = make([]float64, n)
			for i := range spans {
				if entries[i] > 0 {
					vals[i] = spans[i] / entries[i]
				}
			}
		}
		onset := volOnsetBucket(ts, vals, j.Lo, j.Hi, j.Dir)
		evs = append(evs, volEvent(svc, j, time.Unix(onset, 0), last, hour, class, cfg))
	}
	return evs, nil
}
