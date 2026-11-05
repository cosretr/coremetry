package ml

// dependency.go — ML trace T2 (v0.10.1758): bağımlılık gecikmesi. Operatör
// kararı (2026-10-31): kaynak VictoriaMetrics OTel CLIENT histogramları —
// db_summary_1m / topology_edges_5m örneklenmiş span'lerden türediği için
// (Collector tail sampling) KULLANILMAZ. Aileler: HTTP client, DB client,
// messaging client; servis × hedef (server.address / db.namespace /
// messaging.destination) etiketleri saatte bir keşfedilir. Aile başına 2
// gruplu sorgu (count, p95), adım = kova. Seri ml_series signal=trace part
// `dep|<aile>|<hedef>|count|p95`; taban CH takvim sınıfı × ±1 saat, aynı
// judge (yukarı). Trafik tabanı (minBaselineRate) altındaki hedef sessiz.
// Ailenin metriği VM'de yoksa o aile atlanır; hiçbiri yoksa sessiz + lastError.

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	detDependencyLatency = "dependency_latency"
	depPartPrefix        = "dep|"
	depSeriesCap         = 5000
)

// depFamily — bir client histogram ailesi (SAF tanım).
type depFamily struct {
	ID      string
	Metric  string // VM taban adı (_count / _bucket eklenir)
	Targets []string
	Scale   float64 // ms'ye çarpan
}

var depFamilies = []depFamily{
	{ID: "http", Metric: "http_client_request_duration_seconds", Targets: []string{"server_address", "net_peer_name", "peer_service", "http_host"}, Scale: 1000},
	{ID: "db", Metric: "db_client_operation_duration_seconds", Targets: []string{"db_namespace", "db_name", "server_address", "db_system"}, Scale: 1000},
	{ID: "messaging", Metric: "messaging_client_operation_duration_seconds", Targets: []string{"messaging_destination_name", "server_address"}, Scale: 1000},
}

// depPart — SAF.
func depPart(fam, target, measure string) string {
	return depPartPrefix + fam + "|" + target + "|" + measure
}

// depQueries — SAF: (count, p95) PromQL.
func depQueries(f depFamily, svc, target string, winSec int) (count, p95 string) {
	by := svc + ", " + target
	w := fmt.Sprintf("%ds", winSec)
	count = fmt.Sprintf("sum by (%s) (rate(%s_count[%s]))", by, f.Metric, w)
	p95 = fmt.Sprintf("histogram_quantile(0.95, sum by (%s, le) (rate(%s_bucket[%s])))", by, f.Metric, w)
	return
}

type depLabels struct {
	svc, target string
	at          time.Time
	ok          bool
}

// Dependency — dedektör.
type Dependency struct {
	mu     sync.Mutex
	labels map[string]depLabels
	capper
}

func NewDependency() *Dependency {
	return &Dependency{labels: map[string]depLabels{}, capper: capper{daily: map[string]int{}}}
}

func (d *Dependency) Name() string { return "dependency" }

func (d *Dependency) Enabled(cfg chstore.MLConfig) bool { return cfg.DetectorOn(detDependencyLatency) }

func (d *Dependency) famLabels(ctx context.Context, deps Deps, f depFamily, from, to time.Time) depLabels {
	d.mu.Lock()
	l, ok := d.labels[f.ID]
	d.mu.Unlock()
	if ok && time.Since(l.at) < time.Hour {
		return l
	}
	l = depLabels{at: time.Now()}
	if names, _, err := deps.VM.MetricLabelNamesIn(ctx, f.Metric+"_count", from, to); err == nil {
		lm := deps.VM.MetricLabelMap()
		l.svc = pickLabel(names, append([]string{lm.Service}, svcLabelCandidates...))
		l.target = pickLabel(names, f.Targets)
		l.ok = l.svc != "" && l.target != ""
	}
	d.mu.Lock()
	d.labels[f.ID] = l
	d.mu.Unlock()
	return l
}

func (d *Dependency) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	if deps.VM == nil || !deps.VM.Configured() {
		return nil, fmt.Errorf("VictoriaMetrics yapılandırılmamış: bağımlılık ML sessiz (tail sampling nedeniyle rollup kullanılmaz)")
	}
	bucket := time.Duration(cfg.BucketSec) * time.Second
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	from, to := last.Add(-time.Duration(cfg.DwellBuckets+1)*bucket), last.Add(bucket)
	series := map[string]map[int64]float64{}
	put := func(svc, part string, t int64, v float64) {
		k := chstore.MLSeriesKey(svc, part)
		if series[k] == nil {
			if len(series) >= depSeriesCap*2 {
				return
			}
			series[k] = map[int64]float64{}
		}
		series[k][t] = v
	}
	fams := 0
	win := fmt.Sprintf("%ds", cfg.BucketSec)
	var waiting []string
	for _, f := range depFamilies {
		l := d.famLabels(ctx, deps, f, from, to)
		if !l.ok {
			continue
		}
		fams++
		// A: servis başına en çok K hedef — count serisi ucuz (le yok)
		cq := fmt.Sprintf("topk by (%s) (%d, sum by (%s, %s) (rate(%s_count[%s])))", l.svc, cfg.VMTargetCardinality, l.svc, l.target, f.Metric, win)
		rows, skip, err := vmInstant(ctx, deps, "dep:"+f.ID, cq, to, now)
		if skip {
			waiting = append(waiting, f.ID)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("vm dep %s/count: %w", f.ID, err)
		}
		bySvc := map[string][]string{}
		for _, r := range rows {
			lb := groupKeyLabels(r.GroupKey)
			svc, tgt := lb[l.svc], lb[l.target]
			if v, ok := instantValue(r); ok && svc != "" && tgt != "" {
				put(svc, depPart(f.ID, tgt, "count"), last.Unix(), v)
				bySvc[svc] = append(bySvc[svc], tgt)
			}
		}
		// B: p95 servis grupları halinde, yalnız seçili hedefler (ham bucket taraması sınırlı)
		for _, bt := range svcBatches(bySvc, cfg.VMServiceBatch) {
			pq := fmt.Sprintf("histogram_quantile(0.95, sum by (%s, %s, le) (rate(%s_bucket{%s=~%q, %s=~%q}[%s])))",
				l.svc, l.target, f.Metric, l.svc, bt.svcRe, l.target, bt.tgtRe, win)
			rows, skip, err := vmInstant(ctx, deps, "dep:"+f.ID, pq, to, now)
			if skip {
				waiting = append(waiting, f.ID)
				break
			}
			if err != nil {
				return nil, fmt.Errorf("vm dep %s/p95: %w", f.ID, err)
			}
			for _, r := range rows {
				lb := groupKeyLabels(r.GroupKey)
				svc, tgt := lb[l.svc], lb[l.target]
				if v, ok := instantValue(r); ok && bt.allowed[svc][tgt] {
					put(svc, depPart(f.ID, tgt, "p95"), last.Unix(), v*f.Scale)
				}
			}
		}
	}
	if fams == 0 {
		return nil, fmt.Errorf("VM'de client histogram ailesi/etiketi yok (http/db/messaging client duration)")
	}
	if len(waiting) > 0 && len(series) == 0 {
		return nil, fmt.Errorf("VM aileleri beklemede (backoff): %v", waiting)
	}
	var srows []chstore.MLSeriesRow
	for k, byB := range series {
		svc, part := splitKey(k)
		for b, v := range byB {
			srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalTrace, Service: svc, Part: part, Bucket: time.Unix(b, 0), BucketSec: uint32(cfg.BucketSec), Count: v})
		}
	}
	if err := deps.insertSeries(ctx, srows); err != nil {
		return nil, err
	}
	cal, loc := calendarFor(deps.Store.AnomalySensitivityForDetectors())
	dates, class := baselineDates(last, cfg.BaselineDays, cal, loc)
	h := last.In(loc).Hour()
	t0 := time.Now()
	base, err := deps.Store.MLBaseline(ctx, chstore.MLBaselineRequest{Signal: chstore.MLSignalTrace, Dates: dates, HourFrom: max(h-1, 0), HourTo: min(h+1, 23),
		TargetSec: cfg.BucketSec, TZ: loc.String(), Exclude: deps.Labels.Exclude(cfg)})
	global.lastCHMs.Store(time.Since(t0).Milliseconds())
	if err != nil {
		return nil, err
	}
	dwellStart := last.Add(-time.Duration(cfg.DwellBuckets-1) * bucket).Unix()
	// dwell geçmişi CH'den (instant sorgu yalnız son kovayı verir)
	if hist, herr := deps.Store.MLSeriesRecent(ctx, chstore.MLSignalTrace, nil, depPartPrefix, "", time.Unix(dwellStart, 0), cfg.BucketSec); herr == nil {
		mergeHistory(series, hist, dwellStart, last.Unix())
	}
	var evs []chstore.MLEvent
	for k := range series {
		svc, part := splitKey(k)
		if !strings.HasSuffix(part, "|count") {
			continue
		}
		cb := base[k]
		if med, _ := medianMAD(cb); len(cb) < cfg.MinBaselineSamples || med < cfg.MinBaselineRate/float64(cfg.BucketSec) {
			continue // trafik tabanı (count = istek/sn)
		}
		pk := chstore.MLSeriesKey(svc, strings.TrimSuffix(part, "|count")+"|p95")
		cur, ok := dwellWindow(series[pk], dwellStart, last.Unix(), int64(cfg.BucketSec), false)
		if !ok {
			continue
		}
		j := judge(cur, base[pk], cfg, true)
		if !j.Fire || !d.take(detDependencyLatency, cfg, now) {
			continue
		}
		target := strings.TrimSuffix(strings.TrimPrefix(part, depPartPrefix), "|count")
		evs = append(evs, chstore.MLEvent{ID: chstore.MLEventID(detDependencyLatency, svc, target), Detector: detDependencyLatency,
			Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow, Service: svc, Code: target,
			StartedAt: last.UnixNano(), LastSeen: to.UnixNano(), WindowSec: uint32(cfg.BucketSec), Baseline: j.Median, Observed: j.Observed,
			Score: math.Abs(j.Z), Direction: "up", Evidence: mergeEvidence("", map[string]any{"source": "vm", "target": target, "dayClass": class,
				"mad": j.MAD, "unit": "ms", "dwell": cfg.DwellBuckets, "bucketSec": cfg.BucketSec, "baselineDays": cfg.BaselineDays})})
	}
	return evs, nil
}
