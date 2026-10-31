package ml

// trace_red.go — ML trace T1: servis × endpoint p95 (op_latency, yukarı),
// hata oranı (op_error_rate, yukarı), trafik düşüşü (throughput_drop, aşağı;
// tatilde aday değil). KAYNAK: VictoriaMetrics RED serileri (Collector'da
// tail sampling var → örneklenmiş span sayımından oran/gecikme HESAPLANMAZ;
// rollup yedeği YOK, VM yoksa dedektör sessiz ve bunu self-obs'a yazar).
// Aile pinleri anomaly.REDFamilyPins (HTTP / gRPC); etiket adları metrikten
// keşfedilir (saatte bir). Tik başına aile başına 3 gruplu sorgu (count,
// err, p95), topk yok. Seri ml_series'e (signal=trace), taban CH'den takvim
// sınıfı günler × ±1 saat, medyan+MAD (log ile aynı judge). Trafik tabanı
// (minBaselineRate) altındaki endpoint değerlendirilmez.
//
// Ertelenen (T1 notu): op_error_rate kanıtına ±5 dk exception ID + Oracle
// bağlamı referansı; BubbleUp özeti (mevcut rootcause_bubbleup) — dilim T1b.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	detOpLatency      = "op_latency"
	detOpErrorRate    = "op_error_rate"
	detThroughputDrop = "throughput_drop"
	traceRouteCap     = 5000 // seri tavanı (5.000 servis×endpoint tahmini, audit §5)
)

var (
	svcLabelCandidates   = []string{"service_name", "service", "job", "app"}
	routeLabelCandidates = []string{"http_route", "rpc_method", "route", "url_path"}
)

type traceFamilyLabels struct {
	svc, route, status string
	at                 time.Time
	ok                 bool
}

type TraceRED struct {
	mu     sync.Mutex
	labels map[string]traceFamilyLabels // family id → keşfedilen etiketler
	daily  map[string]int
	day    string
}

func NewTraceRED() *TraceRED {
	return &TraceRED{labels: map[string]traceFamilyLabels{}, daily: map[string]int{}}
}

func (d *TraceRED) Name() string { return "trace_red" }

func (d *TraceRED) Enabled(cfg chstore.MLConfig) bool {
	return cfg.DetectorOn(detOpLatency) || cfg.DetectorOn(detOpErrorRate) || cfg.DetectorOn(detThroughputDrop)
}

// ── saf parçalar ─────────────────────────────────────────────

// pickLabel — SAF: aday sırasına göre ilk mevcut etiket.
func pickLabel(have []string, candidates []string) string {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, c := range candidates {
		if c != "" && set[c] {
			return c
		}
	}
	return ""
}

// traceQueries — SAF: aile için (count, err, p95) PromQL. win = kova.
func traceQueries(pin anomaly.REDFamilyPin, svc, route, status string, winSec int) (count, errq, p95 string) {
	by := fmt.Sprintf("%s, %s", svc, route)
	w := fmt.Sprintf("%ds", winSec)
	count = fmt.Sprintf("sum by (%s) (rate(%s_count[%s]))", by, pin.Names, w)
	errq = fmt.Sprintf("sum by (%s) (rate(%s_count{%s=~\"%s\"}[%s]))", by, pin.Names, status, pin.ServerErr, w)
	p95 = fmt.Sprintf("histogram_quantile(0.95, sum by (%s, le) (rate(%s_bucket[%s])))", by, pin.Names, w)
	return
}

// groupKeyLabels — SAF: vmetrics GroupKey ("k=\"v\"" sıralı) → map.
func groupKeyLabels(gk []string) map[string]string {
	out := make(map[string]string, len(gk))
	for _, kv := range gk {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if u, err := strconv.Unquote(v); err == nil {
			v = u
		}
		out[k] = v
	}
	return out
}

// tracePart — ml_series part: endpoint + ölçü.
func tracePart(route, measure string) string { return route + "|" + measure }

func splitTracePart(part string) (route, measure string) {
	i := strings.LastIndex(part, "|")
	if i < 0 {
		return part, ""
	}
	return part[:i], part[i+1:]
}

// ── tik ──────────────────────────────────────────────────────

func (d *TraceRED) dayCap(det string, cfg chstore.MLConfig, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	day := now.Format("2006-01-02")
	if d.day != day {
		d.day, d.daily = day, map[string]int{}
	}
	if d.daily[det] >= cfg.DailyEventCap {
		return false
	}
	d.daily[det]++
	return true
}

func (d *TraceRED) familyLabels(ctx context.Context, vm anomaly.REDVM, pin anomaly.REDFamilyPin, from, to time.Time) traceFamilyLabels {
	d.mu.Lock()
	fl, ok := d.labels[pin.ID]
	d.mu.Unlock()
	if ok && time.Since(fl.at) < time.Hour {
		return fl
	}
	names, _, err := vm.MetricLabelNamesIn(ctx, pin.Names+"_count", from, to)
	fl = traceFamilyLabels{at: time.Now()}
	if err == nil {
		lm := vm.MetricLabelMap()
		fl.svc = pickLabel(names, append([]string{lm.Service}, svcLabelCandidates...))
		fl.route = pickLabel(names, routeLabelCandidates)
		fl.status = pickLabel(names, pin.StatusLabels)
		fl.ok = fl.svc != "" && fl.route != "" && fl.status != ""
	}
	d.mu.Lock()
	d.labels[pin.ID] = fl
	d.mu.Unlock()
	return fl
}

func (d *TraceRED) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	if deps.VM == nil || !deps.VM.Configured() {
		return nil, fmt.Errorf("VictoriaMetrics yapılandırılmamış: trace ML sessiz (tail sampling nedeniyle rollup kullanılmaz)")
	}
	bucket := time.Duration(cfg.BucketSec) * time.Second
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	from := last.Add(-time.Duration(cfg.DwellBuckets+1) * bucket)
	to := last.Add(bucket)
	series := map[string]map[int64]float64{} // key(service, part) → bucket → değer
	put := func(svc, part string, t int64, v float64) {
		k := chstore.MLSeriesKey(svc, part)
		if series[k] == nil {
			if len(series) >= traceRouteCap*3 {
				return
			}
			series[k] = map[int64]float64{}
		}
		series[k][t] = v
	}
	families := 0
	for _, pin := range anomaly.REDFamilyPins() {
		fl := d.familyLabels(ctx, deps.VM, pin, from, to)
		if !fl.ok {
			continue
		}
		families++
		cq, eq, pq := traceQueries(pin, fl.svc, fl.route, fl.status, cfg.BucketSec)
		scale := 1.0
		if pin.Unit == "s" {
			scale = 1000
		}
		type q struct{ expr, measure string }
		for _, x := range []q{{cq, "count"}, {eq, "err"}, {pq, "p95"}} {
			t0 := time.Now()
			rows, err := deps.VM.QueryPromQLRange(ctx, x.expr, from, to, cfg.BucketSec, cfg.DwellBuckets+3)
			deps.ObserveVM(time.Since(t0))
			if err != nil {
				return nil, fmt.Errorf("vm %s/%s: %w", pin.ID, x.measure, err)
			}
			for _, r := range rows {
				lb := groupKeyLabels(r.GroupKey)
				svc, route := lb[fl.svc], lb[fl.route]
				if svc == "" || route == "" {
					continue
				}
				for _, p := range r.Points {
					if math.IsNaN(p.Value) {
						continue
					}
					v := p.Value
					if x.measure == "p95" {
						v *= scale
					}
					put(svc, tracePart(route, x.measure), p.Time/1e9, v)
				}
			}
		}
	}
	if families == 0 {
		return nil, fmt.Errorf("VM'de RED ailesi/etiketi bulunamadı (servis, route, durum)")
	}
	// hata ORANI serisi: err/count (count>0)
	for k, byB := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		route, m := splitTracePart(part)
		if m != "err" {
			continue
		}
		cnt := series[chstore.MLSeriesKey(svc, tracePart(route, "count"))]
		for b, e := range byB {
			if c := cnt[b]; c > 0 {
				put(svc, tracePart(route, "err_ratio"), b, e/c)
			}
		}
	}
	var srows []chstore.MLSeriesRow
	for k, byB := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		if _, m := splitTracePart(part); m == "err" {
			continue
		}
		for b, v := range byB {
			srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalTrace, Service: svc, Part: part, Bucket: time.Unix(b, 0), BucketSec: uint32(cfg.BucketSec), Count: v})
		}
	}
	if err := deps.Store.InsertMLSeries(ctx, srows); err != nil {
		return nil, err
	}
	global.series.Add(int64(len(series)))

	sens := deps.Store.AnomalySensitivityForDetectors()
	loc := time.UTC
	if sens.Calendar.Timezone != "" {
		if l, err := time.LoadLocation(sens.Calendar.Timezone); err == nil {
			loc = l
		}
	}
	dates, class := baselineDates(last, cfg.BaselineDays, sens.Calendar, loc)
	h := last.In(loc).Hour()
	hf, ht := max(h-1, 0), min(h+1, 23)
	t0 := time.Now()
	base, err := deps.Store.MLBaseline(ctx, chstore.MLBaselineRequest{Signal: chstore.MLSignalTrace, Dates: dates, HourFrom: hf, HourTo: ht, TargetSec: cfg.BucketSec, TZ: loc.String(), Exclude: deps.Labels.Exclude(cfg)})
	global.lastCHMs.Store(time.Since(t0).Milliseconds())
	if err != nil {
		return nil, err
	}
	dwellStart := last.Add(-time.Duration(cfg.DwellBuckets-1) * bucket).Unix()
	// eksik veri: VM'de nokta yok (scrape boşluğu) ≠ 0 → kovası eksik seri değerlendirilmez
	curOf := func(k string) []float64 {
		cur, ok := dwellWindow(series[k], dwellStart, last.Unix(), int64(cfg.BucketSec), false)
		if !ok {
			return nil
		}
		return cur
	}
	off := isOffDay(class)
	var evs []chstore.MLEvent
	for k := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		route, m := splitTracePart(part)
		if m != "count" {
			continue
		}
		// trafik tabanı: count tabanı medyanı eşiğin altındaysa endpoint değerlendirilmez
		cntBase := base[k]
		if med, _ := medianMAD(cntBase); len(cntBase) < cfg.MinBaselineSamples || med < cfg.MinBaselineRate/float64(cfg.BucketSec)*float64(cfg.BucketSec) {
			continue
		}
		if cfg.DetectorOn(detThroughputDrop) && !off {
			if j := judge(curOf(k), cntBase, cfg, false); j.Fire && j.Direction == "down" && d.dayCap(detThroughputDrop, cfg, now) {
				evs = append(evs, d.event(detThroughputDrop, svc, route, j, last, cfg, class))
			}
		}
		if cfg.DetectorOn(detOpLatency) {
			pk := chstore.MLSeriesKey(svc, tracePart(route, "p95"))
			if j := judge(curOf(pk), base[pk], cfg, true); j.Fire && d.dayCap(detOpLatency, cfg, now) {
				evs = append(evs, d.event(detOpLatency, svc, route, j, last, cfg, class))
			}
		}
		if cfg.DetectorOn(detOpErrorRate) {
			ek := chstore.MLSeriesKey(svc, tracePart(route, "err_ratio"))
			// oran serisi: taban hacmi eşiği oran için anlamsız → geçici olarak sıfırla
			c2 := cfg
			c2.MinBaselineRate = 0
			if j := judge(curOf(ek), base[ek], c2, true); j.Fire && j.Observed >= 0.01 && d.dayCap(detOpErrorRate, cfg, now) {
				evs = append(evs, d.event(detOpErrorRate, svc, route, j, last, cfg, class))
			}
		}
	}
	return evs, nil
}

func (d *TraceRED) event(det, svc, route string, j Judgement, last time.Time, cfg chstore.MLConfig, class string) chstore.MLEvent {
	raw, _ := json.Marshal(map[string]any{"source": "vm", "route": route, "dayClass": class, "mad": j.MAD, "dwell": cfg.DwellBuckets, "bucketSec": cfg.BucketSec, "baselineDays": cfg.BaselineDays})
	return chstore.MLEvent{
		ID: chstore.MLEventID(det, svc, route), Detector: det, Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow,
		Service: svc, Code: route, StartedAt: last.UnixNano(), LastSeen: last.Add(time.Duration(cfg.BucketSec) * time.Second).UnixNano(),
		WindowSec: uint32(cfg.BucketSec), Baseline: j.Median, Observed: j.Observed, Score: math.Abs(j.Z), Direction: j.Direction, Evidence: string(raw),
	}
}
