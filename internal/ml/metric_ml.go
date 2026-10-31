package ml

// metric_ml.go — ML metrik M1: aileler (JVM, connection pool, Kafka, HTTP
// client, k8s) = dedektörler; her ailenin MetricsQL serileri Settings'ten
// (cfg.MetricFamilies, varsayılan + düzenlenebilir). Kaynak VictoriaMetrics;
// `{svc}` servis etiketi (LabelMap.Service, yoksa service_name), `{win}` kova.
// Counter reset: ifadeler rate/increase ile; scrape boşluğu: nokta yok = örnek
// yok (eksik veri kuralı judge'da); pod etiketi ifadede servis düzeyine
// toplanır (pod_outlier hariç — T3). Seri ml_series (signal=metric, part
// `family|exprID`), taban CH takvim sınıfı × ±1 saat, log/trace ile aynı judge.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const metricSeriesCap = 20000

type MetricML struct {
	mu    sync.Mutex
	daily map[string]int
	day   string
}

func NewMetricML() *MetricML { return &MetricML{daily: map[string]int{}} }

func (d *MetricML) Name() string { return "metric_ml" }

func (d *MetricML) Enabled(cfg chstore.MLConfig) bool {
	for _, f := range chstore.MLMetricDetectorNames {
		if cfg.DetectorOn(f) {
			return true
		}
	}
	return false
}

// renderExpr — SAF: {svc} / {win} yer tutucuları.
func renderExpr(expr, svcLabel string, winSec int) string {
	return strings.NewReplacer("{svc}", svcLabel, "{win}", fmt.Sprintf("%ds", winSec)).Replace(expr)
}

// metricPart — ml_series part.
func metricPart(family, exprID string) string { return family + "|" + exprID }

func (d *MetricML) dayCap(det string, cfg chstore.MLConfig, now time.Time) bool {
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

func (d *MetricML) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	if deps.VM == nil || !deps.VM.Configured() {
		return nil, fmt.Errorf("VictoriaMetrics yapılandırılmamış: metrik ML sessiz")
	}
	svcLabel := deps.VM.MetricLabelMap().Service
	if svcLabel == "" {
		svcLabel = "service_name"
	}
	bucket := time.Duration(cfg.BucketSec) * time.Second
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	from := last.Add(-time.Duration(cfg.DwellBuckets+1) * bucket)
	to := last.Add(bucket)
	series := map[string]map[int64]float64{}
	dirOf := map[string]string{} // part → direction
	for _, fam := range chstore.MLMetricDetectorNames {
		if !cfg.DetectorOn(fam) {
			continue
		}
		for _, e := range cfg.MetricFamilies[fam] {
			t0 := time.Now()
			rows, err := deps.VM.QueryPromQLRange(ctx, renderExpr(e.Expr, svcLabel, cfg.BucketSec), from, to, cfg.BucketSec, cfg.DwellBuckets+3)
			deps.ObserveES(time.Since(t0))
			if err != nil {
				return nil, fmt.Errorf("vm %s/%s: %w", fam, e.ID, err)
			}
			part := metricPart(fam, e.ID)
			dirOf[part] = e.Direction
			for _, r := range rows {
				svc := groupKeyLabels(r.GroupKey)[svcLabel]
				if svc == "" {
					continue
				}
				k := chstore.MLSeriesKey(svc, part)
				if series[k] == nil {
					if len(series) >= metricSeriesCap {
						break
					}
					series[k] = map[int64]float64{}
				}
				for _, p := range r.Points {
					if !math.IsNaN(p.Value) && !math.IsInf(p.Value, 0) {
						series[k][p.Time/1e9] = p.Value
					}
				}
			}
		}
	}
	var srows []chstore.MLSeriesRow
	for k, byB := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		for b, v := range byB {
			srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalMetric, Service: svc, Part: part, Bucket: time.Unix(b, 0), BucketSec: uint32(cfg.BucketSec), Count: v})
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
	t0 := time.Now()
	base, err := deps.Store.MLBaseline(ctx, chstore.MLBaselineRequest{Signal: chstore.MLSignalMetric, Dates: dates, HourFrom: max(h-1, 0), HourTo: min(h+1, 23), TargetSec: cfg.BucketSec, TZ: loc.String(), Exclude: deps.Labels.Exclude(cfg)})
	global.lastCHMs.Store(time.Since(t0).Milliseconds())
	if err != nil {
		return nil, err
	}
	dwellStart := last.Add(-time.Duration(cfg.DwellBuckets-1) * bucket).Unix()
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
		if !d.dayCap(fam, cfg, now) {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"source": "vm", "expr": part, "dayClass": class, "mad": j.MAD, "dwell": cfg.DwellBuckets, "bucketSec": cfg.BucketSec})
		evs = append(evs, chstore.MLEvent{
			ID: chstore.MLEventID(fam, svc, part), Detector: fam, Signal: chstore.MLSignalMetric, Stage: chstore.MLStageShadow,
			Service: svc, Code: part, StartedAt: last.UnixNano(), LastSeen: last.Add(bucket).UnixNano(), WindowSec: uint32(cfg.BucketSec),
			Baseline: j.Median, Observed: j.Observed, Score: math.Abs(j.Z), Direction: j.Direction, Evidence: string(raw),
		})
	}
	return evs, nil
}
