package ml

// trace_pods.go — ML trace T3 (v0.10.1759).
//   - trace_pod_outlier: operatör kararı (2026-10-31) kaynak VM server RED
//     aileleri (T1 pinleri) + pod etiketi (k8s_pod_name / pod …, saatte bir
//     keşif); etiket yoksa aile atlanır, hiçbirinde yoksa sessiz + lastError.
//     spanmetrics_1m pod kolonu örneklenmiş span'den türer, KULLANILMAZ.
//     Dwell penceresi üzerinde anlık sorgu: servis × pod istek / hata artışı
//     ve p95; hata oranında log ile aynı podOutliers kuralı, p95'te pod'lar
//     arası medyan+MAD (≥3 pod, ≥1.5× medyan).
//   - trace_change_point: change_point.go çekirdeği, trace ml_series'in
//     endpoint p95 serileri (`|p95`).

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	detTracePodOutlier  = "trace_pod_outlier"
	detTraceChangePoint = "trace_change_point"
)

var podLabelCandidates = []string{"k8s_pod_name", "pod", "k8s_pod", "pod_name", "kubernetes_pod_name"}

// podLatency — bir pod'un p95'i.
type podLatency struct {
	Pod string
	P95 float64
}

// latencyOutliers — SAF: pod'lar arası p95 aykırıları (ms).
func latencyOutliers(pods []podLatency, cfg chstore.MLConfig) []podJudgement {
	if len(pods) < podMinPods {
		return nil
	}
	xs := make([]float64, len(pods))
	for i, p := range pods {
		xs[i] = p.P95
	}
	med, mad := medianMAD(xs)
	eff := effMAD(med, mad)
	var out []podJudgement
	for _, p := range pods {
		z := (p.P95 - med) / eff
		if z >= cfg.CriticalZ && p.P95 >= 1.5*med {
			out = append(out, podJudgement{Pod: p.Pod, Ratio: p.P95, Median: med, Z: z, Pods: len(pods)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Z > out[j].Z })
	return out
}

// tracePodQueries — SAF: (toplam, hata, p95) anlık PromQL, pencere w.
func tracePodQueries(pin anomaly.REDFamilyPin, svc, pod, status string, winSec int) (total, errq, p95 string) {
	by := svc + ", " + pod
	w := fmt.Sprintf("%ds", winSec)
	total = fmt.Sprintf("sum by (%s) (increase(%s_count[%s]))", by, pin.Names, w)
	errq = fmt.Sprintf("sum by (%s) (increase(%s_count{%s=~\"%s\"}[%s]))", by, pin.Names, status, pin.ServerErr, w)
	p95 = fmt.Sprintf("histogram_quantile(0.95, sum by (%s, le) (rate(%s_bucket[%s])))", by, pin.Names, w)
	return
}

type podFamLabels struct {
	svc, pod, status string
	at               time.Time
	ok               bool
}

// TracePods — trace_pod_outlier + trace_change_point.
type TracePods struct {
	mu     sync.Mutex
	labels map[string]podFamLabels
	capper
}

func NewTracePods() *TracePods {
	return &TracePods{labels: map[string]podFamLabels{}, capper: capper{daily: map[string]int{}}}
}

func (d *TracePods) Name() string { return "trace_pods" }

func (d *TracePods) Enabled(cfg chstore.MLConfig) bool {
	return cfg.DetectorOn(detTracePodOutlier) || cfg.DetectorOn(detTraceChangePoint)
}

func (d *TracePods) famLabels(ctx context.Context, deps Deps, pin anomaly.REDFamilyPin, from, to time.Time) podFamLabels {
	d.mu.Lock()
	l, ok := d.labels[pin.ID]
	d.mu.Unlock()
	if ok && time.Since(l.at) < time.Hour {
		return l
	}
	l = podFamLabels{at: time.Now()}
	if names, _, err := deps.VM.MetricLabelNamesIn(ctx, pin.Names+"_count", from, to); err == nil {
		// v0.10.2055 — seçim detay grafiğiyle ortak (PickTracePodLabels).
		tl, ok := PickTracePodLabels(names, deps.VM.MetricLabelMap().Service, pin)
		l.svc, l.pod, l.status, l.ok = tl.Svc, tl.Pod, tl.Status, ok
	}
	d.mu.Lock()
	d.labels[pin.ID] = l
	d.mu.Unlock()
	return l
}

func (d *TracePods) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	var evs []chstore.MLEvent
	var firstErr error
	if cfg.DetectorOn(detTracePodOutlier) {
		pe, err := d.pods(ctx, deps, cfg, now)
		evs, firstErr = append(evs, pe...), err
	}
	if cfg.DetectorOn(detTraceChangePoint) {
		ce, err := runChangePoint(ctx, deps, cfg, now, cpTarget{signal: chstore.MLSignalTrace, suffix: "|p95", detector: detTraceChangePoint,
			fields: func(part string) (string, string) {
				route, _ := splitTracePart(part)
				return "", route
			}}, func() bool { return d.take(detTraceChangePoint, cfg, now) })
		evs = append(evs, ce...)
		if firstErr == nil {
			firstErr = err
		}
	}
	return evs, firstErr
}

func (d *TracePods) pods(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	if deps.VM == nil || !deps.VM.Configured() {
		return nil, fmt.Errorf("VictoriaMetrics yapılandırılmamış: trace pod ML sessiz (tail sampling nedeniyle span rollup'ı kullanılmaz)")
	}
	bucket := time.Duration(cfg.BucketSec) * time.Second
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	to := last.Add(bucket)
	win := cfg.DwellBuckets * cfg.BucketSec
	fams := 0
	var evs []chstore.MLEvent
	for _, pin := range anomaly.REDFamilyPins() {
		l := d.famLabels(ctx, deps, pin, to.Add(-time.Hour), to)
		if !l.ok {
			continue
		}
		fams++
		tq, eq, pq := tracePodQueries(pin, l.svc, l.pod, l.status, win)
		scale := 1.0
		if pin.Unit == "s" {
			scale = 1000
		}
		vals := [3]map[[2]string]float64{{}, {}, {}}
		var sampleTs int64 // VM'in döndürdüğü örnek damgası (bayat seri teşhisi)
		for i, q := range []string{tq, eq, pq} {
			t0 := time.Now()
			rows, err := deps.VM.QueryPromQLInstant(ctx, q, to)
			deps.ObserveVM(time.Since(t0))
			if err != nil {
				return evs, fmt.Errorf("vm pod %s: %w", pin.ID, err)
			}
			for _, r := range rows {
				lb := groupKeyLabels(r.GroupKey)
				k := [2]string{lb[l.svc], lb[l.pod]}
				if k[0] == "" || k[1] == "" || len(r.Points) == 0 || math.IsNaN(r.Points[0].Value) || math.IsInf(r.Points[0].Value, 0) {
					continue
				}
				vals[i][k] = r.Points[0].Value
				sampleTs = max(sampleTs, r.Points[0].Time/1e9)
			}
		}
		rowsBy := map[string][]logstore.MLPodRow{}
		latBy := map[string][]podLatency{}
		for k, total := range vals[0] {
			rowsBy[k[0]] = append(rowsBy[k[0]], logstore.MLPodRow{Service: k[0], Pod: k[1], Total: int64(math.Round(total)), Errors: int64(math.Round(vals[1][k]))})
			if p, ok := vals[2][k]; ok && total >= math.Max(cfg.MinBaselineRate, 1) {
				latBy[k[0]] = append(latBy[k[0]], podLatency{Pod: k[1], P95: p * scale})
			}
		}
		emit := func(svc, measure string, p podJudgement) {
			if !d.take(detTracePodOutlier, cfg, now) {
				return
			}
			evs = append(evs, chstore.MLEvent{ID: chstore.MLEventID(detTracePodOutlier, svc, "pod|"+p.Pod+"|"+measure), Detector: detTracePodOutlier,
				Signal: chstore.MLSignalTrace, Stage: chstore.MLStageShadow, Service: svc, Pod: p.Pod, Code: measure,
				StartedAt: last.UnixNano(), LastSeen: to.UnixNano(), WindowSec: uint32(cfg.BucketSec), Baseline: p.Median, Observed: p.Ratio,
				Score: p.Z, Direction: "up", Evidence: mergeEvidence("", map[string]any{"source": "vm", "family": pin.ID, "measure": measure,
					"pods": p.Pods, "pod": p.Pod, "errors": p.Errors, "total": p.Total, "windowSec": win,
					"queryAt": to.Unix(), "sampleTs": sampleTs, "tickAt": now.Unix()})})
		}
		for svc, rows := range rowsBy {
			for _, p := range podOutliers(rows, cfg) {
				emit(svc, "err_ratio", p)
			}
		}
		for svc, pods := range latBy {
			for _, p := range latencyOutliers(pods, cfg) {
				emit(svc, "p95", p)
			}
		}
	}
	if fams == 0 {
		return nil, fmt.Errorf("VM RED ailelerinde pod etiketi yok (%v): trace pod ML sessiz", podLabelCandidates)
	}
	return evs, nil
}
