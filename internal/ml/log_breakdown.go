package ml

// log_breakdown.go — ML log dilim 3 (v0.10.1756): nadir hata kodu + aykırı
// pod. Tik başına TEK ES isteği (logstore.MLLogBreakdowner: servis × {hata →
// kod, pod → toplam + hata}) dwell penceresi üzerinde.
//   - rare_error_code: hata satırındaki kod, serviste son 14 günde HİÇ
//     görülmemiş (ml_series `code|<kod>`), servisin kod geçmişi ≥ 3 gün eski
//     (yeni servisin her kodu nadir sayılmasın) ve sayım ≥ hacim tabanı.
//     İlk tikte bir kez 14 günlük günlük-kova geçmişi (1 istek).
//   - pod_outlier: aynı servisin ≥3 pod'u arasında hata ORANI medyan+MAD
//     dışında (z ≥ kritik z), pod hatası ≥5 ve oran ≥ 2× medyan. Kesitsel
//     karar; süreklilik recorder'ın koşu takibinde.
// Alanlar saatte bir keşfedilir (kod adayları; pod: log Filter.Pod listesi).
// Hata ve pod sinyali yukarı yönlü: takvimde bastırılmaz.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	detRareErrorCode = "rare_error_code"
	detPodOutlier    = "pod_outlier"
	codePartPrefix   = "code|"
	codeLookback     = 14 * 24 * time.Hour
	codeKnownAge     = 3 * 24 * time.Hour
	podMinPods       = 3
	podMinErrors     = 5
)

var codeFieldCandidates = []string{"error.code", "error_code", "errorCode", "err.code", "attributes.error.code", "code", "status_code", "return_code"}

// LogBreakdown — dedektör.
type LogBreakdown struct {
	mu                  sync.Mutex
	codeField, podField string
	fieldsAt            time.Time
	backfilled          bool
	daily               map[string]int
	day                 string
}

func NewLogBreakdown() *LogBreakdown { return &LogBreakdown{daily: map[string]int{}} }

func (d *LogBreakdown) Name() string { return "log_breakdown" }

func (d *LogBreakdown) Enabled(cfg chstore.MLConfig) bool {
	return cfg.DetectorOn(detRareErrorCode) || cfg.DetectorOn(detPodOutlier)
}

func (d *LogBreakdown) dayCap(det string, cfg chstore.MLConfig, now time.Time) bool {
	day := now.UTC().Format("2006-01-02")
	if day != d.day {
		d.day, d.daily = day, map[string]int{}
	}
	if d.daily[det] >= cfg.DailyEventCap {
		return false
	}
	d.daily[det]++
	return true
}

// podJudgement — aykırı pod kararı.
type podJudgement struct {
	Pod           string
	Ratio, Median float64
	Z             float64
	Errors, Total int64
	Pods          int
}

// podOutliers — SAF: servisin pod'ları → aykırılar. Toplamı hacim tabanının
// altındaki pod oran hesabına girmez (gürültü).
func podOutliers(rows []logstore.MLPodRow, cfg chstore.MLConfig) []podJudgement {
	minTotal := int64(math.Max(cfg.MinBaselineRate, 1))
	var use []logstore.MLPodRow
	for _, r := range rows {
		if r.Total >= minTotal && r.Pod != "" {
			use = append(use, r)
		}
	}
	if len(use) < podMinPods {
		return nil
	}
	ratios := make([]float64, len(use))
	for i, r := range use {
		ratios[i] = float64(r.Errors) / float64(r.Total)
	}
	med, mad := medianMAD(ratios)
	eff := math.Max(mad*1.4826, 0.02)
	var out []podJudgement
	for i, r := range use {
		z := (ratios[i] - med) / eff
		if z >= cfg.CriticalZ && r.Errors >= podMinErrors && ratios[i] >= 2*med {
			out = append(out, podJudgement{Pod: r.Pod, Ratio: ratios[i], Median: med, Z: z, Errors: r.Errors, Total: r.Total, Pods: len(use)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Z > out[j].Z })
	return out
}

// rareCode — SAF: kod nadir mi? first: anahtar → ilk görülme (14 g, pencere öncesi).
func rareCode(service, code string, count int64, first map[string]time.Time, svcOldest map[string]time.Time, now time.Time, cfg chstore.MLConfig) bool {
	if code == "" || float64(count) < math.Max(cfg.MinBaselineRate, 1) {
		return false
	}
	if _, seen := first[chstore.MLSeriesKey(service, codePartPrefix+code)]; seen {
		return false
	}
	o, ok := svcOldest[service]
	return ok && now.Sub(o) >= codeKnownAge
}

func (d *LogBreakdown) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	agg, ok := logstore.Unwrap(deps.Logs).(interface {
		logstore.MLLogBreakdowner
		logstore.MLLogAggregator
	})
	if !ok {
		return nil, fmt.Errorf("log deposu ES değil: kod/pod dedektörleri sessiz")
	}
	if deps.ESLeft != nil && deps.ESLeft() < 1 {
		return nil, fmt.Errorf("ES tik bütçesi bitti: kod/pod tiki atlandı (maxEsQueriesPerTick ≥ 4 gerekir)")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Since(d.fieldsAt) > time.Hour {
		d.codeField = agg.MLResolveField(ctx, codeFieldCandidates)
		d.podField = agg.MLResolveField(ctx, logstore.MLPodFieldCandidates())
		d.fieldsAt = time.Now()
	}
	wantCode := cfg.DetectorOn(detRareErrorCode) && d.codeField != ""
	wantPod := cfg.DetectorOn(detPodOutlier) && d.podField != ""
	if !wantCode && !wantPod {
		return nil, fmt.Errorf("kod/pod alanı ES'te bulunamadı (kod=%q pod=%q)", d.codeField, d.podField)
	}
	bucket := time.Duration(cfg.BucketSec) * time.Second
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	from, to := last.Add(-time.Duration(cfg.DwellBuckets-1)*bucket), last.Add(bucket)
	req := logstore.MLBreakdownRequest{From: from, To: to, MaxServices: cfg.MaxServices, ErrorLevels: cfg.ErrorLevels}
	if wantCode {
		req.CodeField = d.codeField
	}
	if wantPod {
		req.PodField = d.podField
	}
	t0 := time.Now()
	res, err := agg.MLLogBreakdown(ctx, req)
	deps.ObserveES(time.Since(t0))
	if err != nil {
		return nil, err
	}
	var evs []chstore.MLEvent
	if wantPod {
		bySvc := map[string][]logstore.MLPodRow{}
		for _, r := range res.Pods {
			bySvc[r.Service] = append(bySvc[r.Service], r)
		}
		for svc, rows := range bySvc {
			for _, p := range podOutliers(rows, cfg) {
				if !d.dayCap(detPodOutlier, cfg, now) {
					break
				}
				evs = append(evs, d.podEvent(svc, p, last, to, cfg))
			}
		}
	}
	if wantCode {
		cevs, cerr := d.codes(ctx, deps, cfg, agg, res.Codes, from, last, to, now)
		if cerr != nil {
			return evs, cerr
		}
		evs = append(evs, cevs...)
	}
	return evs, nil
}

func (d *LogBreakdown) codes(ctx context.Context, deps Deps, cfg chstore.MLConfig, agg logstore.MLLogBreakdowner, rows []logstore.MLCodeRow, from, last, to, now time.Time) ([]chstore.MLEvent, error) {
	// geçmiş: pencereden önceki 14 g (ilk görülme) — kural bu yazımdan ÖNCE okunur
	first, err := deps.Store.MLSeriesFirstSeen(ctx, chstore.MLSignalLog, codePartPrefix, now.Add(-codeLookback), from)
	if err != nil {
		return nil, err
	}
	if len(first) == 0 && !d.backfilled && (deps.ESLeft == nil || deps.ESLeft() >= 1) {
		d.backfill(ctx, deps, cfg, agg, from)
		if first, err = deps.Store.MLSeriesFirstSeen(ctx, chstore.MLSignalLog, codePartPrefix, now.Add(-codeLookback), from); err != nil {
			return nil, err
		}
	}
	svcOldest := map[string]time.Time{}
	for k, t := range first {
		svc, _, _ := strings.Cut(k, "\x00")
		if o, ok := svcOldest[svc]; !ok || t.Before(o) {
			svcOldest[svc] = t
		}
	}
	var evs []chstore.MLEvent
	var srows []chstore.MLSeriesRow
	for _, r := range rows {
		srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalLog, Service: r.Service, Part: codePartPrefix + r.Code, Bucket: last, BucketSec: uint32(cfg.BucketSec), Count: float64(r.Count)})
		if rareCode(r.Service, r.Code, r.Count, first, svcOldest, now, cfg) && d.dayCap(detRareErrorCode, cfg, now) {
			ev := mergeEvidence("", map[string]any{"field": d.codeField, "lookbackDays": 14, "count": r.Count, "dwell": cfg.DwellBuckets, "bucketSec": cfg.BucketSec})
			evs = append(evs, chstore.MLEvent{ID: chstore.MLEventID(detRareErrorCode, r.Service, codePartPrefix+r.Code), Detector: detRareErrorCode,
				Signal: chstore.MLSignalLog, Stage: chstore.MLStageShadow, Service: r.Service, Level: "error", Code: r.Code,
				StartedAt: last.UnixNano(), LastSeen: to.UnixNano(), WindowSec: uint32(cfg.BucketSec), Baseline: 0, Observed: float64(r.Count),
				Score: cfg.CriticalZ, Direction: "up", Evidence: ev})
		}
	}
	return evs, deps.Store.InsertMLSeries(ctx, srows)
}

// backfill — ilk kez: 14 günlük kod geçmişi, günlük kova (1 istek).
func (d *LogBreakdown) backfill(ctx context.Context, deps Deps, cfg chstore.MLConfig, agg logstore.MLLogBreakdowner, before time.Time) {
	t0 := time.Now()
	res, err := agg.MLLogBreakdown(ctx, logstore.MLBreakdownRequest{From: before.Add(-codeLookback), To: before, MaxServices: cfg.MaxServices,
		ErrorLevels: cfg.ErrorLevels, CodeField: d.codeField, DailyCodes: true})
	deps.ObserveES(time.Since(t0))
	if err != nil {
		return
	}
	var srows []chstore.MLSeriesRow
	for _, r := range res.Codes {
		srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalLog, Service: r.Service, Part: codePartPrefix + r.Code, Bucket: time.Unix(r.Day, 0), BucketSec: 86400, Count: float64(r.Count)})
	}
	if deps.Store.InsertMLSeries(ctx, srows) == nil {
		d.backfilled = true
	}
}

func (d *LogBreakdown) podEvent(svc string, p podJudgement, last, to time.Time, cfg chstore.MLConfig) chstore.MLEvent {
	ev := mergeEvidence("", map[string]any{"field": d.podField, "pods": p.Pods, "errors": p.Errors, "total": p.Total,
		"dwell": cfg.DwellBuckets, "bucketSec": cfg.BucketSec})
	return chstore.MLEvent{ID: chstore.MLEventID(detPodOutlier, svc, "pod|"+p.Pod), Detector: detPodOutlier, Signal: chstore.MLSignalLog,
		Stage: chstore.MLStageShadow, Service: svc, Level: "error", Pod: p.Pod, StartedAt: last.UnixNano(), LastSeen: to.UnixNano(),
		WindowSec: uint32(cfg.BucketSec), Baseline: p.Median, Observed: p.Ratio, Score: p.Z, Direction: "up", Evidence: ev}
}
