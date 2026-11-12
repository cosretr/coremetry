package ml

// metric_thanos.go — v0.10.2019: ML metrik ailelerinin Thanos okuması ve
// aile başına kaynak tespiti (kural: thanos_source.go başı). YALNIZ gölge:
// ml_events; Problem / bildirim / anomaly_events YOK. Eski heap bandı
// dedektörüne (anomaly/heap_source.go) dokunulmaz.
//
// TİK: (1) saatte bir tespit — açık ailelerin VM varlığı (ailenin kendi
// ifadeleri, anlık) + her Thanos cluster'ında varyant setleri başına tek
// `count(...)` yoklaması; servis kataloğu (aktif servisler + metadata
// deployment'ları) aynı anda yenilenir. (2) kaynağı Thanos olan ailelerin
// ifadeleri cluster × tik en çok thanosQueriesPerClusterTick sorgu,
// cluster'lar en çok thanosParallel eşzamanlı, cluster başına
// thanosClusterDeadline; bir cluster'ın hatası ötekileri etkilemez. Ağır
// hata (zaman aşımı / 422) cluster×aile backoff'una girer (VM'den AYRI
// sayaç, aynı 5 dk × 2ⁿ ≤ 1 sa kuralı). query_range / backfill YOK: taban
// ml_series'te zamanla birikir, dolana dek dedektör sessizdir.

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// MetricSources — aile → kaynak (süreç içi; MetricML ve MetricThanos ortak).
type MetricSources struct {
	mu         sync.Mutex
	src        map[string]chstore.MLMetricSource
	variant    map[string]map[string]int // aile → cluster adı → varyant seti
	index      podIndex
	lastDetect time.Time
	tick       *chstore.MLThanosTick
}

func NewMetricSources() *MetricSources {
	return &MetricSources{src: map[string]chstore.MLMetricSource{}, variant: map[string]map[string]int{}}
}

// Sources — kopya (nil alıcı = boş: VM yolu bugünkü gibi).
func (m *MetricSources) Sources() map[string]chstore.MLMetricSource {
	out := map[string]chstore.MLMetricSource{}
	if m == nil {
		return out
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.src {
		out[k] = v
	}
	return out
}

// MetricThanos — dedektör (adı metric_ önekli: tik ölçümünde metrik türü).
type MetricThanos struct {
	q       ThanosQuerier
	ms      *MetricSources
	backoff *vmBackoff
	mu      sync.Mutex
	daily   map[string]int
	day     string
}

func NewMetricThanos(q ThanosQuerier, ms *MetricSources) *MetricThanos {
	return &MetricThanos{q: q, ms: ms, backoff: newVMBackoff(), daily: map[string]int{}}
}

func (d *MetricThanos) Name() string { return "metric_thanos" }

func (d *MetricThanos) Enabled(cfg chstore.MLConfig) bool { return (&MetricML{}).Enabled(cfg) }

func (d *MetricThanos) dayCap(det string, cfg chstore.MLConfig, now time.Time) bool {
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

func (d *MetricThanos) refundCap(det string) {
	d.mu.Lock()
	refundDaily(d.daily, det)
	d.mu.Unlock()
}

// tickStatus — recorder tik blobuna (MetricSources + Thanos ölçümü).
func (d *MetricThanos) tickStatus() (map[string]chstore.MLMetricSource, *chstore.MLThanosTick) {
	d.ms.mu.Lock()
	t := d.ms.tick
	d.ms.mu.Unlock()
	return d.ms.Sources(), t
}

// thanosMeter — tikin Thanos sayaçları (eşzamanlı cluster'lar).
type thanosMeter struct {
	mu sync.Mutex
	t  chstore.MLThanosTick
}

func (m *thanosMeter) observe(dur time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t.Queries++
	ms := dur.Milliseconds()
	m.t.Ms += ms
	m.t.MaxMs = max(m.t.MaxMs, ms)
	if err != nil {
		m.t.Errors++
	}
}

func (m *thanosMeter) add(budgetCut, unmatched int64, failed string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t.BudgetCut += budgetCut
	m.t.Unmatched += unmatched
	if failed != "" {
		m.t.Failed = append(m.t.Failed, failed)
	}
}

func (d *MetricThanos) instant(ctx context.Context, m *thanosMeter, c ThanosCluster, expr string, at time.Time) ([]ThanosSample, error) {
	t0 := time.Now()
	rows, err := d.q.Instant(ctx, c.ID, expr, at)
	m.observe(time.Since(t0), err)
	return rows, err
}

// forClusters — cluster başına iş, en çok thanosParallel eşzamanlı, her biri kendi son tarihiyle.
func forClusters(ctx context.Context, cls []ThanosCluster, fn func(context.Context, ThanosCluster)) {
	sem := make(chan struct{}, thanosParallel)
	var wg sync.WaitGroup
	for _, c := range cls {
		wg.Add(1)
		go func(c ThanosCluster) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, thanosClusterDeadline)
			defer cancel()
			fn(cctx, c)
		}(c)
	}
	wg.Wait()
}

func (d *MetricThanos) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	meter := &thanosMeter{}
	defer func() {
		t := meter.t
		sort.Strings(t.Failed)
		d.ms.mu.Lock()
		d.ms.tick = &t
		d.ms.mu.Unlock()
	}()
	var cls []ThanosCluster
	if d.q != nil {
		cls = d.q.Clusters()
	}
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	at := last.Add(time.Duration(cfg.BucketSec) * time.Second)
	d.ms.mu.Lock()
	due := d.ms.lastDetect.IsZero() || now.Sub(d.ms.lastDetect) >= sourceDetectEvery
	d.ms.mu.Unlock()
	if due {
		d.detect(ctx, deps, cfg, cls, meter, at, now)
	}
	src := d.ms.Sources()
	// kaynağı Thanos olan açık aileler × cluster
	d.ms.mu.Lock()
	ix := d.ms.index
	variants := map[string]map[string]int{}
	for f, m := range d.ms.variant {
		variants[f] = m
	}
	d.ms.mu.Unlock()
	type clusterPlan struct {
		c     ThanosCluster
		exprs []thanosPlanned
	}
	var plans []clusterPlan
	for _, c := range cls {
		var exprs []thanosPlanned
		for _, fam := range chstore.MLMetricDetectorNames {
			if !cfg.DetectorOn(fam) || !familyOnThanos(src, fam) {
				continue
			}
			vi, ok := variants[fam][c.Name]
			sets := familyVariantSets(cfg, fam)
			if !ok || vi >= len(sets) {
				continue
			}
			exprs = append(exprs, variantPlan(cfg, fam, sets[vi], cfg.BucketSec)...)
		}
		if len(exprs) > 0 {
			plans = append(plans, clusterPlan{c, exprs})
		}
	}
	if len(plans) == 0 {
		return nil, nil
	}
	var mu sync.Mutex
	series := map[string]map[int64]float64{}
	dirOf := map[string]string{}
	clusterOf := map[string]string{}
	forClusters(ctx, clusterRefs(plans, func(p clusterPlan) ThanosCluster { return p.c }), func(cctx context.Context, c ThanosCluster) {
		var exprs []thanosPlanned
		for _, p := range plans {
			if p.c.ID == c.ID {
				exprs = p.exprs
			}
		}
		run, cut := budgetPlan(exprs, thanosQueriesPerClusterTick)
		nsRe := namespaceRegex(c.NamespaceFilter)
		var unmatched int64
		failed := ""
		for _, pe := range run {
			key := c.Name + "|" + pe.family
			if d.backoff.skipping(key, now) {
				continue
			}
			rows, err := d.instant(cctx, meter, c, pe.expr, at)
			if err != nil {
				if heavyVMError(err) {
					d.backoff.fail(key, err, now)
				}
				if cctx.Err() != nil {
					failed = c.Name
					break
				}
				continue
			}
			d.backoff.ok(key)
			vals, un := podValuesByService(rows, ix, nsRe)
			unmatched += un
			part := thanosMetricPart(pe.family, pe.id, c.Name)
			mu.Lock()
			dirOf[part] = pe.direction
			for svc, vs := range vals {
				k := chstore.MLSeriesKey(svc, part)
				if series[k] == nil {
					if len(series) >= metricSeriesCap {
						break
					}
					series[k] = map[int64]float64{}
				}
				series[k][last.Unix()] = combine(pe.agg, vs)
				clusterOf[k] = c.Name
			}
			mu.Unlock()
		}
		meter.add(int64(cut), unmatched, failed)
	})
	return evalMetricSeries(ctx, deps, cfg, last, now, series, dirOf, func(fam string) bool { return d.dayCap(fam, cfg, now) },
		func(k string) map[string]any { return map[string]any{"source": "thanos", "cluster": clusterOf[k]} })
}

func clusterRefs[T any](xs []T, f func(T) ThanosCluster) []ThanosCluster {
	out := make([]ThanosCluster, 0, len(xs))
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

// namespaceRegex — cluster'ın namespace kalkanı (boş/bozuk = süzgeç yok;
// bozuk filtre ayar ekranında zaten reddedilir).
func namespaceRegex(filter string) *regexp.Regexp {
	if strings.TrimSpace(filter) == "" {
		return nil
	}
	re, err := regexp.Compile("^(?:" + filter + ")$")
	if err != nil {
		return nil
	}
	return re
}

// podValuesByService — SAF: pod örnekleri → servis başına değerler; eşlenemeyen pod sayılır.
func podValuesByService(rows []ThanosSample, ix podIndex, nsRe *regexp.Regexp) (map[string][]float64, int64) {
	out := map[string][]float64{}
	var unmatched int64
	for _, r := range rows {
		if nsRe != nil && !nsRe.MatchString(r.Labels["namespace"]) {
			continue
		}
		svc, ok := ix.serviceOf(r.Labels["pod"])
		if !ok {
			unmatched++
			continue
		}
		out[svc] = append(out[svc], r.Value)
	}
	return out, unmatched
}

// detect — saatte bir: servis kataloğu + aile başına kaynak + cluster varyantı.
func (d *MetricThanos) detect(ctx context.Context, deps Deps, cfg chstore.MLConfig, cls []ThanosCluster, meter *thanosMeter, at, now time.Time) {
	if deps.Store != nil {
		svcs, err := deps.Store.ListActiveServiceNames(ctx, 24*time.Hour)
		meta, merr := deps.Store.ListServiceMetadata(ctx)
		if err == nil {
			if merr != nil {
				meta = nil
			}
			ix := buildPodIndex(svcs, meta)
			d.ms.mu.Lock()
			d.ms.index = ix
			d.ms.mu.Unlock()
		}
	}
	prev := d.ms.Sources()
	next := map[string]chstore.MLMetricSource{}
	nextVar := map[string]map[string]int{}
	var mu sync.Mutex
	thanosChecks := map[string]map[string]familyCheck{}
	var fams []string
	for _, fam := range chstore.MLMetricDetectorNames {
		if cfg.DetectorOn(fam) {
			fams = append(fams, fam)
			thanosChecks[fam] = map[string]familyCheck{}
			nextVar[fam] = map[string]int{}
		}
	}
	forClusters(ctx, cls, func(cctx context.Context, c ThanosCluster) {
		for _, fam := range fams {
			chk := familyCheck{}
			for vi, set := range familyVariantSets(cfg, fam) {
				if len(set) == 0 {
					continue
				}
				rows, err := d.instant(cctx, meter, c, "count("+renderThanosExpr(set[0].Expr, cfg.BucketSec)+")", at)
				if err != nil {
					continue
				}
				chk.Checked = true
				if len(rows) > 0 && rows[0].Value > 0 {
					chk.Present = true
					mu.Lock()
					nextVar[fam][c.Name] = vi
					mu.Unlock()
					break
				}
			}
			mu.Lock()
			thanosChecks[fam][c.Name] = chk
			mu.Unlock()
		}
	})
	for _, fam := range fams {
		next[fam] = pickFamilySource(vmFamilyCheck(ctx, deps, cfg, fam, at), thanosChecks[fam], prev[fam], now)
	}
	d.ms.mu.Lock()
	d.ms.src, d.ms.variant, d.ms.lastDetect = next, nextVar, now
	d.ms.mu.Unlock()
}

// vmFamilyCheck — ailenin kendi ifadeleri VM'de (anlık); ilk veri döndüren
// yeter. VM yapılandırılmamışsa "ölçüldü, yok" (aile VM'de olamaz).
func vmFamilyCheck(ctx context.Context, deps Deps, cfg chstore.MLConfig, fam string, at time.Time) familyCheck {
	if deps.VM == nil || !deps.VM.Configured() {
		return familyCheck{Checked: true}
	}
	svcLabel := deps.VM.MetricLabelMap().Service
	if svcLabel == "" {
		svcLabel = "service_name"
	}
	chk := familyCheck{}
	for _, e := range cfg.MetricFamilies[fam] {
		qctx, cancel := context.WithTimeout(ctx, sourceCheckTimeout)
		t0 := time.Now()
		rows, err := deps.VM.QueryPromQLInstant(qctx, renderExpr(e.Expr, svcLabel, cfg.BucketSec), at)
		if deps.ObserveVM != nil {
			deps.ObserveVM(time.Since(t0))
		}
		cancel()
		if err != nil {
			continue
		}
		chk.Checked = true
		if len(rows) > 0 {
			chk.Present = true
			return chk
		}
	}
	return chk
}

// evalMetricSeries — VM ve Thanos metrik yollarının ortak sonu: seri yazımı,
// dwell geçmişi (CH), taban, karar, olay. evidence: anahtar → ek kanıt.
func evalMetricSeries(ctx context.Context, deps Deps, cfg chstore.MLConfig, last, now time.Time, series map[string]map[int64]float64,
	dirOf map[string]string, dayCap func(fam string) bool, evidence func(key string) map[string]any) ([]chstore.MLEvent, error) {
	bucket := time.Duration(cfg.BucketSec) * time.Second
	dwellFrom := last.Add(-time.Duration(cfg.DwellBuckets-1) * bucket)
	if hist, herr := deps.Store.MLSeriesRecent(ctx, chstore.MLSignalMetric, nil, "", "", dwellFrom, cfg.BucketSec); herr == nil {
		mergeHistory(series, hist, dwellFrom.Unix(), last.Unix())
	}
	var srows []chstore.MLSeriesRow
	for k, byB := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		for b, v := range byB {
			srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalMetric, Service: svc, Part: part, Bucket: time.Unix(b, 0), BucketSec: uint32(cfg.BucketSec), Count: v})
		}
	}
	if err := deps.insertSeries(ctx, srows); err != nil {
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
	t0 := time.Now()
	base, err := deps.Store.MLBaseline(ctx, chstore.MLBaselineRequest{Signal: chstore.MLSignalMetric, Dates: dates, HourFrom: max(h-1, 0), HourTo: min(h+1, 23), TargetSec: cfg.BucketSec, TZ: loc.String(), Exclude: deps.Labels.Exclude(cfg)})
	global.lastCHMs.Store(time.Since(t0).Milliseconds())
	if err != nil {
		return nil, err
	}
	dwellStart := dwellFrom.Unix()
	off := isOffDay(class)
	c2 := cfg
	c2.MinBaselineRate = 0 // gauge/oran serilerinde hacim tabanı anlamsız; eksik veri kuralı yeter
	var evs []chstore.MLEvent
	for k := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		fam, _, _ := strings.Cut(part, "|")
		cur, ok := dwellWindow(series[k], dwellStart, last.Unix(), int64(cfg.BucketSec), false)
		if !ok { // scrape boşluğu: eksik veri → değerlendirme yok
			continue
		}
		dir := dirOf[part]
		j := judge(cur, base[k], c2, dir == "up")
		if !j.Fire || (dir == "down" && j.Direction != "down") || (j.Direction == "down" && off) {
			continue
		}
		if !dayCap(fam) {
			continue
		}
		ev := map[string]any{"source": "vm", "expr": part, "dayClass": class, "mad": j.MAD, "dwell": cfg.DwellBuckets, "bucketSec": cfg.BucketSec}
		for ek, evv := range evidence(k) {
			ev[ek] = evv
		}
		raw, _ := json.Marshal(ev)
		evs = append(evs, chstore.MLEvent{
			ID: chstore.MLEventID(fam, svc, part), Detector: fam, Signal: chstore.MLSignalMetric, Stage: chstore.MLStageShadow,
			Service: svc, Code: part, StartedAt: last.UnixNano(), LastSeen: last.Add(bucket).UnixNano(), WindowSec: uint32(cfg.BucketSec),
			Baseline: j.Median, Observed: j.Observed, Score: math.Abs(j.Z), Direction: j.Direction, Evidence: string(raw),
		})
	}
	return evs, nil
}
