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

// slowShareQuery — SAF (B2): isteklerin ≤ slow eşiğinde kalan payı
// (MetricsQL histogram_share; VM kaynağı). slow_ratio = 1 − pay.
func slowShareQuery(pin anomaly.REDFamilyPin, svc, route string, winSec int, slowSec float64) string {
	thr := slowSec
	if pin.Unit == "ms" {
		thr = slowSec * 1000
	}
	return fmt.Sprintf("histogram_share(%g, sum by (%s, %s, le) (rate(%s_bucket[%ds])))", thr, svc, route, pin.Names, winSec)
}

// slowAffected — SAF: kovada etkilenen istek ≈ oran × istek/sn × kova sn.
func slowAffected(ratio, ratePerSec float64, bucketSec int) float64 {
	return ratio * ratePerSec * float64(bucketSec)
}

// isChronic — SAF: son 7 günde ≥ K epizot → kronik.
func isChronic(episodes, k int) bool { return k > 0 && episodes >= k }

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

// pickTraceLabels — SAF: keşfedilen etiket adlarından servis/route/durum
// etiketi (canlı tik ve v0.10.1815 baseline geçmiş doldurma ortak).
func pickTraceLabels(names []string, svcLabel string, pin anomaly.REDFamilyPin) traceFamilyLabels {
	fl := traceFamilyLabels{at: time.Now()}
	fl.svc = pickLabel(names, append([]string{svcLabel}, svcLabelCandidates...))
	fl.route = pickLabel(names, routeLabelCandidates)
	fl.status = pickLabel(names, pin.StatusLabels)
	fl.ok = fl.svc != "" && fl.route != "" && fl.status != ""
	return fl
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
		fl = pickTraceLabels(names, vm.MetricLabelMap().Service, pin)
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
		sq := slowShareQuery(pin, fl.svc, fl.route, cfg.BucketSec, cfg.SlowRequestSec)
		for _, x := range []q{{cq, "count"}, {eq, "err"}, {pq, "p95"}, {sq, "slow"}} {
			rows, skip, err := vmInstant(ctx, deps, "trace:"+pin.ID, x.expr, to, now)
			if skip {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("vm %s/%s: %w", pin.ID, x.measure, err)
			}
			for _, r := range rows {
				lb := groupKeyLabels(r.GroupKey)
				svc, route := lb[fl.svc], lb[fl.route]
				v, ok := instantValue(r)
				if svc == "" || route == "" || !ok {
					continue
				}
				if x.measure == "p95" {
					v *= scale
				}
				if x.measure == "slow" {
					v = math.Max(0, 1-v) // pay → yavaş istek oranı
				}
				put(svc, tracePart(route, x.measure), last.Unix(), v)
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
	if err := deps.insertSeries(ctx, srows); err != nil {
		return nil, err
	}
	global.series.Add(int64(len(series)))
	// dwell geçmişi CH'den (instant sorgu yalnız son kovayı verir)
	if dwellFrom := last.Add(-time.Duration(cfg.DwellBuckets-1) * bucket); true {
		if hist, herr := deps.Store.MLSeriesRecent(ctx, chstore.MLSignalTrace, nil, "", "", dwellFrom, cfg.BucketSec); herr == nil {
			mergeHistory(series, hist, dwellFrom.Unix(), last.Unix())
		}
	}

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
	episodes := map[string]int{}
	if cfg.DetectorOn(detOpLatency) {
		if ec, eerr := deps.Store.MLEpisodeCounts(ctx, detOpLatency, now.Add(-7*24*time.Hour)); eerr == nil {
			episodes = ec
		}
	}
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
			// B2: p99/p95 yerine yavaş istek oranı + mutlak etkilenen istek eşiği;
			// kronik desen (≥K epizot / 7 g) yeni olay değil tek kayıt.
			sk := chstore.MLSeriesKey(svc, tracePart(route, "slow"))
			c2 := cfg
			c2.MinBaselineRate = 0
			j := judge(curOf(sk), base[sk], c2, true)
			affected := slowAffected(j.Observed, series[k][last.Unix()], cfg.BucketSec)
			if j.Fire && affected >= float64(cfg.MinAffectedRequests) && d.dayCap(detOpLatency, cfg, now) {
				ev := d.event(detOpLatency, svc, route, j, last, cfg, class)
				ev.Evidence = mergeEvidence(ev.Evidence, map[string]any{"measure": "slow_ratio", "slowSec": cfg.SlowRequestSec, "affected": math.Round(affected),
					"p95Ms": series[chstore.MLSeriesKey(svc, tracePart(route, "p95"))][last.Unix()], "episodes7d": episodes[chstore.MLSeriesKey(svc, route)]})
				if isChronic(episodes[chstore.MLSeriesKey(svc, route)], cfg.ChronicEpisodes) {
					ev.ID, ev.Stage = chstore.MLEventID("chronic:"+detOpLatency, svc, route), chstore.MLStageChronic
				}
				evs = append(evs, ev)
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
