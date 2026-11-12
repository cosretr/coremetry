package ml

// trace_errors.go — v0.10.2018 (T-err, anomali tekilleştirme ek dilimi):
// ML `trace_error_count` dedektörü, YALNIZ gölge (ml_events, signal=trace).
//
// Operatör (2026-11-12): collector hatalı trace'leri örneklemez, hata span
// SAYISI ClickHouse'ta tamdır; oran ve gecikme CH'den hesaplanamaz (payda
// örneklenmiş) ve VM'de kalır (trace_red). Bu dedektör MUTLAK hata sayısını
// ölçer: servis × operasyon (giriş span'i: server + consumer) başına, kova
// başına hata span sayısı, kendi tabanına göre.
//
// KAYNAK (MV-first): spanmetrics_1m (chstore.MLTraceErrors) — ham spans
// okunmaz; ≤ 3 örnek trace kimliği MV'nin error_exemplar_state'inden.
// TABAN: ml_series (signal=trace, part="<operasyon>|errcnt"), aynı takvim
// sınıfı günler × ±1 saat (trace_red ile aynı baselineDates). Dedektör hata
// olmayan kovaya satır yazmaz; bu yüzden taban, dedektörün o pencerede
// ÖLÇTÜĞÜ kova sayısına (tüm errcnt serilerinde görülen farklı kova) sıfırla
// doldurulur. Ölçülmemiş kova sıfır SAYILMAZ: yeni açılan dedektör taban
// birikene dek (MinBaselineSamples) sessizdir.
//
// KARAR (tek cümle): son DwellBuckets kovanın HER BİRİNDE hata sayısı
// "normalin üst sınırı"nı (max(medyan + CriticalZ·ölçek, tabanın p95'i))
// aşar VE son kova en az MinAffectedRequests hata ise olay. Ölçek =
// max(1.4826·MAD, 0.1·medyan, 1) — sayım serisinde taban 1 hata.
//
// Tik başına CH: 1 MV okuması + 1 ml_series yazımı + 1 taban okuması.
// Exception akışına (fırtına, P1, yeni hata) dokunmaz; Problem açmaz.

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	detTraceErrorCount = "trace_error_count"
	traceErrMeasure    = "errcnt"
	traceErrSuffix     = "|" + traceErrMeasure
	traceErrSampleCap  = 3
)

// TraceErrors — dedektör (günlük olay tavanı süreç içi).
type TraceErrors struct {
	mu    sync.Mutex
	daily map[string]int
	day   string
}

func NewTraceErrors() *TraceErrors { return &TraceErrors{daily: map[string]int{}} }

func (d *TraceErrors) Name() string { return "trace_errors" }

func (d *TraceErrors) Enabled(cfg chstore.MLConfig) bool { return cfg.DetectorOn(detTraceErrorCount) }

func (d *TraceErrors) dayCap(cfg chstore.MLConfig, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	day := now.Format("2006-01-02")
	if d.day != day {
		d.day, d.daily = day, map[string]int{}
	}
	if d.daily[detTraceErrorCount] >= cfg.DailyEventCap {
		return false
	}
	d.daily[detTraceErrorCount]++
	return true
}

func (d *TraceErrors) refundCap(det string) {
	d.mu.Lock()
	refundDaily(d.daily, det)
	d.mu.Unlock()
}

// ── saf parçalar ─────────────────────────────────────────────

// padErrBaseline — SAF: taban noktaları, dedektörün ölçtüğü kova sayısına
// sıfırla doldurulur (hata olmayan kova satır üretmez).
func padErrBaseline(pts []chstore.MLPoint, measured int) []float64 {
	out := make([]float64, 0, max(len(pts), measured))
	for _, p := range pts {
		out = append(out, p.V)
	}
	for len(out) < measured {
		out = append(out, 0)
	}
	return out
}

// measuredErrBuckets — SAF: taban penceresinde herhangi bir errcnt serisinin
// yazıldığı farklı kova sayısı (dedektörün koştuğu kovaların alt sınırı).
func measuredErrBuckets(base map[string][]chstore.MLPoint) int {
	seen := map[int64]bool{}
	for _, pts := range base {
		for _, p := range pts {
			seen[p.T] = true
		}
	}
	return len(seen)
}

type errJudgement struct {
	Fire                           bool
	Observed, NormalMax, Median, Z float64
	Reason                         string
}

// judgeErrCount — SAF: mutlak hata sayısı kararı (yalnız yukarı).
func judgeErrCount(cur, base []float64, cfg chstore.MLConfig) errJudgement {
	j := errJudgement{}
	if len(cur) < cfg.DwellBuckets {
		j.Reason = "dwell: yeterli kova yok"
		return j
	}
	if len(base) < cfg.MinBaselineSamples {
		j.Reason = "eksik veri: taban yetersiz"
		return j
	}
	med, mad := medianMAD(base)
	scale := math.Max(mad*1.4826, math.Max(0.1*med, 1))
	j.Median, j.Observed = med, cur[len(cur)-1]
	j.NormalMax = math.Max(med+cfg.CriticalZ*scale, quantileHF7(base, 0.95))
	j.Z = (j.Observed - med) / scale
	for _, v := range cur[len(cur)-cfg.DwellBuckets:] {
		if v <= j.NormalMax {
			j.Reason = "dwell: kova eşiği geçmedi"
			return j
		}
	}
	if j.Observed < float64(cfg.MinAffectedRequests) {
		j.Reason = "mutlak hata sayısı tabanın altında"
		return j
	}
	j.Fire = true
	return j
}

// traceErrEvent — SAF: olay. Baseline = normalin üst sınırı ("bu saatte
// normali en çok M"), Observed = son kovadaki hata sayısı.
func traceErrEvent(svc, op string, j errJudgement, last time.Time, cfg chstore.MLConfig, class string, samples []string) chstore.MLEvent {
	if samples == nil {
		samples = []string{}
	}
	raw, _ := json.Marshal(map[string]any{"source": "spanmetrics_1m", "measure": "error_count", "operation": op, "dayClass": class,
		"median": j.Median, "normalMax": j.NormalMax, "samples": samples, "dwell": cfg.DwellBuckets, "bucketSec": cfg.BucketSec, "baselineDays": cfg.BaselineDays})
	return chstore.MLEvent{
		ID: chstore.MLEventID(detTraceErrorCount, svc, op), Detector: detTraceErrorCount, Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow,
		Service: svc, Code: op, StartedAt: last.UnixNano(), LastSeen: last.Add(time.Duration(cfg.BucketSec) * time.Second).UnixNano(),
		WindowSec: uint32(cfg.BucketSec), Baseline: j.NormalMax, Observed: j.Observed, Score: math.Abs(j.Z), Direction: "up", Evidence: string(raw),
	}
}

// traceErrSamples — SAF: kanıttan örnek trace kimlikleri (replay örnekleri).
func traceErrSamples(evidence string) []string {
	var e struct {
		Samples []string `json:"samples"`
	}
	if evidence == "" || json.Unmarshal([]byte(evidence), &e) != nil {
		return nil
	}
	return e.Samples
}

// ── tik ──────────────────────────────────────────────────────

func (d *TraceErrors) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	bucket := time.Duration(cfg.BucketSec) * time.Second
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	from := last.Add(-time.Duration(cfg.DwellBuckets-1) * bucket)
	rows, err := deps.Store.MLTraceErrors(ctx, from, last.Add(bucket), cfg.BucketSec)
	if err != nil {
		return nil, err
	}
	series := map[string]map[int64]float64{}
	samples := map[string][]string{}
	srows := make([]chstore.MLSeriesRow, 0, len(rows))
	for _, r := range rows {
		if r.Service == "" || r.Operation == "" {
			continue
		}
		part := tracePart(r.Operation, traceErrMeasure)
		k := chstore.MLSeriesKey(r.Service, part)
		if series[k] == nil {
			series[k] = map[int64]float64{}
		}
		series[k][r.Bucket] = float64(r.Errors)
		// en yeni kovanın örnekleri önce
		if r.Bucket == last.Unix() || len(samples[k]) == 0 {
			samples[k] = append(append([]string{}, r.Samples...), samples[k]...)
		}
		srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalTrace, Service: r.Service, Part: part,
			Bucket: time.Unix(r.Bucket, 0), BucketSec: uint32(cfg.BucketSec), Count: float64(r.Errors)})
	}
	if err := deps.insertSeries(ctx, srows); err != nil {
		return nil, err
	}
	global.series.Add(int64(len(series)))
	if len(series) == 0 {
		return nil, nil
	}
	sens := deps.Store.AnomalySensitivityForDetectors()
	loc := OperatorLocation(sens)
	dates, class := baselineDates(last, cfg.BaselineDays, sens.Calendar, loc)
	h := last.In(loc).Hour()
	base, err := deps.Store.MLSuffixBaseline(ctx, chstore.MLBaselineRequest{Signal: chstore.MLSignalTrace, Dates: dates, HourFrom: max(h-1, 0), HourTo: min(h+1, 23),
		TargetSec: cfg.BucketSec, TZ: loc.String(), Exclude: deps.Labels.Exclude(cfg)}, traceErrSuffix)
	if err != nil {
		return nil, err
	}
	measured := measuredErrBuckets(base)
	var evs []chstore.MLEvent
	for k, byB := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		op, _ := splitTracePart(part)
		// MV eksiksiz: pencerede satırı olmayan kova = 0 hata
		cur, _ := dwellWindow(byB, from.Unix(), last.Unix(), int64(cfg.BucketSec), true)
		j := judgeErrCount(cur, padErrBaseline(base[k], measured), cfg)
		if j.Fire && d.dayCap(cfg, now) {
			s := samples[k]
			if len(s) > traceErrSampleCap {
				s = s[:traceErrSampleCap]
			}
			evs = append(evs, traceErrEvent(svc, op, j, last, cfg, class, s))
		}
	}
	return evs, nil
}
