package ml

// log_red.go — ML log dilim 1: servis log hacmi (iki yönlü) + hata oranı
// (yukarı) + servis sustu. Tik başına TEK ES agregasyonu (logstore.MLLogAggregator),
// seri CH `ml_series`'e yazılır, taban CH'den aynı takvim sınıfı günler ×
// saat penceresi (anomaly.CalendarDayClass — mevcut takvim altyapısı), medyan+MAD
// Go'da (saf judge). Kurallar: ES ingest gecikme payı (son kova geç değerlenir),
// eksik veri (taban örneği azsa sessiz), dwell, günlük tavan, tatil/yarım günde
// düşüş ve susma aday olmaz (hata oranı hariç). Backfill: 3 hafta, saatlik
// kova, 1 istek/sn, arka planda. Hepsi shadow; Problem üretmez.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	detLogVolume     = "log_volume"
	detErrorRate     = "error_rate"
	detServiceSilent = "service_silent"
	backfillDays     = 21
	partTotal        = "_total" // ml_series part: tüm seviyeler
	partError        = "_error" // ml_series part: hata seviyeleri toplamı
)

var influencerCandidates = []string{"channel", "channel_code", "channelCode", "kanal", "kanal_kodu", "transaction.channel", "attributes.channel"}

// LogRED — bileşik dedektör.
type LogRED struct {
	mu       sync.Mutex
	backfill *backfillRunner
	infField string
	infAt    time.Time
	daily    map[string]int // detector → bugün açılan olay
	dailyDay string
}

func NewLogRED() *LogRED { return &LogRED{daily: map[string]int{}} }

func (d *LogRED) Name() string { return "log_red" }

func (d *LogRED) Enabled(cfg chstore.MLConfig) bool {
	return cfg.DetectorOn(detLogVolume) || cfg.DetectorOn(detErrorRate) || cfg.DetectorOn(detServiceSilent)
}

// ── saf çekirdek ─────────────────────────────────────────────

func medianMAD(xs []float64) (med, mad float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	med = cp[len(cp)/2]
	if len(cp)%2 == 0 {
		med = (cp[len(cp)/2-1] + cp[len(cp)/2]) / 2
	}
	dev := make([]float64, len(cp))
	for i, v := range cp {
		dev[i] = math.Abs(v - med)
	}
	sort.Float64s(dev)
	mad = dev[len(dev)/2]
	if len(dev)%2 == 0 {
		mad = (dev[len(dev)/2-1] + dev[len(dev)/2]) / 2
	}
	return med, mad
}

// Judgement — bir seri için karar.
type Judgement struct {
	Fire      bool
	Direction string  // up | down
	Z         float64 // son kovanın robust z'si
	Median    float64
	MAD       float64 // ham MAD (replay bandı effMAD ile)
	Observed  float64 // son kovanın değeri
	Silent    bool    // tüm dwell kovaları 0 ve taban anlamlı
	Reason    string  // sessizlik gerekçesi (fire=false)
}

// judge — SAF. cur: son dwell kova (oran, hedef kovaya normalize), base: taban
// örnekleri. upOnly: yalnız yukarı. minRate: taban bu kadar altında ise sessiz.
func judge(cur, base []float64, cfg chstore.MLConfig, upOnly bool) Judgement {
	med, mad := medianMAD(base)
	return judgeMM(cur, med, mad, len(base), cfg, upOnly)
}

// effMAD — SAF: ölçekli MAD tabanı; düz seyreden seride MAD≈0 tuzağı
// (anomaly.go effectiveMAD ile aynı fikir). Replay bandı da bunu kullanır.
// Mutlak taban: sayım/ms serisinde 1; ORAN serisinde (medyan < 1: err_ratio,
// slow) 0.005 — sabit 1 tabanı oran serisini hiç tetiklemiyordu (v0.10.1769).
func effMAD(med, mad float64) float64 {
	floor := 1.0
	if med < 1 {
		floor = 0.005
	}
	return math.Max(mad*1.4826, math.Max(0.1*med, floor))
}

// judgeMM — SAF: judge'ın medyan/MAD'ı önceden hesaplanmış hali (backtest
// aynı saat sınıfı için bir kez hesaplar). n = taban örnek sayısı.
func judgeMM(cur []float64, med, mad float64, n int, cfg chstore.MLConfig, upOnly bool) Judgement {
	j := Judgement{}
	if len(cur) < cfg.DwellBuckets {
		j.Reason = "dwell: yeterli kova yok"
		return j
	}
	if n < cfg.MinBaselineSamples {
		j.Reason = fmt.Sprintf("eksik veri: taban %d < %d", n, cfg.MinBaselineSamples)
		return j
	}
	j.Median = med
	j.Observed = cur[len(cur)-1]
	if med < cfg.MinBaselineRate {
		j.Reason = "taban hacmi eşiğin altında"
		return j
	}
	j.MAD = mad
	eff := effMAD(med, mad)
	allZero := true
	dir := ""
	for _, v := range cur {
		if v != 0 {
			allZero = false
		}
		z := (v - med) / eff
		if z >= cfg.CriticalZ {
			if dir == "down" {
				j.Reason = "dwell kovaları yön değiştirdi"
				return j
			}
			dir = "up"
		} else if z <= -cfg.CriticalZ && !upOnly {
			if dir == "up" {
				j.Reason = "dwell kovaları yön değiştirdi"
				return j
			}
			dir = "down"
		} else {
			j.Reason = "dwell: kova eşiği geçmedi"
			return j
		}
	}
	j.Z = (j.Observed - med) / eff
	j.Direction = dir
	j.Silent = allZero
	j.Fire = true
	return j
}

// baselineDates — SAF: son `days` gün içinde `at` ile aynı takvim sınıfındaki
// günler; sınıf eşleşmesi 3'ün altındaysa (nadir sınıf, ör. tatil) tüm günler.
func baselineDates(at time.Time, days int, cal chstore.AnomalyCalendarConfig, loc *time.Location) (dates []string, class string) {
	class = mlDayClass(at.In(loc), cal)
	var all []string
	for i := 1; i <= days; i++ {
		d := at.In(loc).AddDate(0, 0, -i)
		all = append(all, d.Format("2006-01-02"))
		if mlDayClass(d, cal) == class {
			dates = append(dates, d.Format("2006-01-02"))
		}
	}
	if len(dates) < 3 {
		return all, class
	}
	return dates, class
}

// halfDayCutoffHour — yarım gün saat desteği (v0.10.1762, operatör 2026-10-31:
// "13:00 sonrası tatil sınıfı"): yarım günün sabahı sıradan iş günüdür.
const halfDayCutoffHour = 13

// mlDayClass — SAF: takvim sınıfı, yarım gün saatle (yerel saat t). Yalnız ML;
// yerleşik dedektörlerin gün düzeyi sınıfı değişmez.
func mlDayClass(t time.Time, cal chstore.AnomalyCalendarConfig) string {
	c := anomaly.CalendarDayClass(t, cal)
	if c == "half_day" && t.Hour() < halfDayCutoffHour {
		switch t.Weekday() {
		case time.Saturday:
			return "saturday"
		case time.Sunday:
			return "sunday"
		}
		return "weekday"
	}
	return c
}

// isOffDay — tatil/yarım gün/pazar sınıfı: düşüş ve susma aday olmaz.
func isOffDay(class string) bool {
	switch class {
	case "holiday", "half_day", "sunday":
		return true
	}
	return false
}

// liveWindow — SAF: canlı tik ES penceresi = son tam kova + bir öncekisi
// (v0.10.1765; 4 kova yerine 2: ES maliyeti, dwell geçmişi CH'den).
func liveWindow(last time.Time, bucket time.Duration) (from, to time.Time) {
	return last.Add(-bucket), last.Add(bucket)
}

// lastCompleteBucket — SAF: ingest gecikme payını geçmiş son tam kova başlangıcı.
func lastCompleteBucket(now time.Time, bucketSec, lagSec int) time.Time {
	t := now.Add(-time.Duration(lagSec) * time.Second).Truncate(time.Duration(bucketSec) * time.Second)
	return t.Add(-time.Duration(bucketSec) * time.Second)
}

func isErrorLevel(level string, errLevels []string) bool {
	l := strings.ToLower(level)
	for _, e := range errLevels {
		if l == e {
			return true
		}
	}
	return false
}

// seriesFromRows — SAF: ES satırları → (service, part) → bucket → sayım; part
// _total / _error toplamları + ham seviye.
func seriesFromRows(rows []logstore.MLLogRow, errLevels []string) map[string]map[int64]float64 {
	out := map[string]map[int64]float64{}
	add := func(svc, part string, b int64, c int64) {
		k := chstore.MLSeriesKey(svc, part)
		if out[k] == nil {
			out[k] = map[int64]float64{}
		}
		out[k][b] += float64(c)
	}
	for _, r := range rows {
		add(r.Service, partTotal, r.Bucket, r.Count)
		if isErrorLevel(r.Level, errLevels) {
			add(r.Service, partError, r.Bucket, r.Count)
		}
	}
	return out
}

// ── tik ──────────────────────────────────────────────────────

func (d *LogRED) dayCap(detector string, cfg chstore.MLConfig, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	day := now.Format("2006-01-02")
	if d.dailyDay != day {
		d.dailyDay, d.daily = day, map[string]int{}
	}
	if d.daily[detector] >= cfg.DailyEventCap {
		return false
	}
	d.daily[detector]++
	return true
}

func (d *LogRED) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	agg, ok := logstore.Unwrap(deps.Logs).(logstore.MLLogAggregator)
	if !ok {
		return nil, fmt.Errorf("log deposu ES değil: log ML dedektörleri sessiz")
	}
	// influencer alanı: saatte bir keşif
	d.mu.Lock()
	if cfg.InfluencerField != "" {
		d.infField = cfg.InfluencerField
	} else if time.Since(d.infAt) > time.Hour {
		d.infField = agg.MLResolveField(ctx, influencerCandidates)
		d.infAt = time.Now()
	}
	inf := d.infField
	d.mu.Unlock()

	bucket := time.Duration(cfg.BucketSec) * time.Second
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	from, to := liveWindow(last, bucket) // yalnız son tam kova + bir öncekisi (geç log); dwell geçmişi CH'den
	t0 := time.Now()
	res, err := agg.MLLogAggregate(ctx, logstore.MLLogAggRequest{From: from, To: to, BucketSec: cfg.BucketSec,
		MaxServices: cfg.MaxServices, InfluencerField: inf, ErrorLevels: cfg.ErrorLevels})
	deps.ObserveES(time.Since(t0))
	if err != nil {
		return nil, err
	}
	if res.Partial {
		return nil, fmt.Errorf("%w: %s — seri yazılmadı, kova eksik veri sayıldı", logstore.ErrESPartial, res.PartialReason)
	}
	var docs int64
	for _, r := range res.Rows {
		docs += r.Count
	}
	global.docs.Store(docs)
	series := seriesFromRows(res.Rows, cfg.ErrorLevels)
	if cfg.DwellBuckets > 2 {
		dwellFrom := last.Add(-time.Duration(cfg.DwellBuckets-1) * bucket)
		if hist, herr := deps.Store.MLSeriesRecent(ctx, chstore.MLSignalLog, nil, "", "", dwellFrom, cfg.BucketSec); herr == nil {
			mergeHistory(series, hist, dwellFrom.Unix(), from.Unix())
		}
	}
	// seri yazımı (backfill ile aynı tablo)
	var srows []chstore.MLSeriesRow
	for k, byB := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		for b, c := range byB {
			srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalLog, Service: svc, Part: part, Bucket: time.Unix(b, 0), BucketSec: uint32(cfg.BucketSec), Count: c})
		}
	}
	if err := deps.Store.InsertMLSeries(ctx, srows); err != nil {
		return nil, err
	}
	global.series.Store(int64(len(series)))
	d.mu.Lock()
	if d.backfill == nil {
		d.backfill = newBackfillRunner(chstore.MLSignalLog, backfillDays)
	}
	bf := d.backfill
	d.mu.Unlock()
	bf.maybeStart(deps, cfg, agg, now)

	// taban: takvim sınıfı + saat penceresi (yerel dilim)
	sens := deps.Store.AnomalySensitivityForDetectors()
	loc := time.UTC
	if sens.Calendar.Timezone != "" {
		if l, err := time.LoadLocation(sens.Calendar.Timezone); err == nil {
			loc = l
		}
	}
	dates, class := baselineDates(last, cfg.BaselineDays, sens.Calendar, loc)
	h := last.In(loc).Hour()
	hf, ht := h-1, h+1
	if hf < 0 {
		hf = 0
	}
	if ht > 23 {
		ht = 23
	}
	base, err := deps.Store.MLBaseline(ctx, chstore.MLBaselineRequest{Signal: chstore.MLSignalLog, Dates: dates, HourFrom: hf, HourTo: ht, TargetSec: cfg.BucketSec, TZ: loc.String(), Exclude: deps.Labels.Exclude(cfg)})
	if err != nil {
		return nil, err
	}
	// ingest gecikmesi ölçümü: pencere son kova sonuna kadar dolu mu?
	ingestLagMs := int64(0)
	if res.LatestDocMs > 0 {
		ingestLagMs = now.UnixMilli() - res.LatestDocMs
	}
	// değerlendirme
	var evs []chstore.MLEvent
	infBySvc := map[string][]logstore.MLLogInfluencer{}
	for _, i := range res.Influencers {
		infBySvc[i.Service] = append(infBySvc[i.Service], i)
	}
	dwellStart := last.Add(-time.Duration(cfg.DwellBuckets-1) * bucket).Unix()
	curOf := func(k string) []float64 {
		var cur []float64
		for b := dwellStart; b <= last.Unix(); b += int64(cfg.BucketSec) {
			cur = append(cur, series[k][b])
		}
		return cur
	}
	// susma: taban var ama bu tikte hiç satır yok → anahtar seride yok; tabandaki anahtarları da gez
	keys := map[string]bool{}
	for k := range series {
		keys[k] = true
	}
	for k := range base {
		keys[k] = true
	}
	off := isOffDay(class)
	for k := range keys {
		svc, part, _ := strings.Cut(k, "\x00")
		switch part {
		case partTotal:
			j := judge(curOf(k), base[k], cfg, false)
			if !j.Fire {
				continue
			}
			if j.Silent && cfg.DetectorOn(detServiceSilent) && !off && d.dayCap(detServiceSilent, cfg, now) {
				evs = append(evs, d.event(detServiceSilent, svc, part, j, last, cfg, class, ingestLagMs, nil))
			} else if !j.Silent && cfg.DetectorOn(detLogVolume) && (j.Direction == "up" || !off) && d.dayCap(detLogVolume, cfg, now) {
				evs = append(evs, d.event(detLogVolume, svc, part, j, last, cfg, class, ingestLagMs, nil))
			}
		case partError:
			if !cfg.DetectorOn(detErrorRate) {
				continue
			}
			j := judge(curOf(k), base[k], cfg, true)
			if !j.Fire || !d.dayCap(detErrorRate, cfg, now) {
				continue
			}
			evs = append(evs, d.event(detErrorRate, svc, part, j, last, cfg, class, ingestLagMs, infBySvc[svc]))
		}
	}
	return evs, nil
}

func (d *LogRED) event(det, svc, part string, j Judgement, last time.Time, cfg chstore.MLConfig, class string, lagMs int64, inf []logstore.MLLogInfluencer) chstore.MLEvent {
	ev := map[string]any{"dayClass": class, "ingestLagMs": lagMs, "mad": j.MAD, "dwell": cfg.DwellBuckets, "bucketSec": cfg.BucketSec, "baselineDays": cfg.BaselineDays}
	if len(inf) > 0 {
		ev["influencers"] = inf
	}
	raw, _ := json.Marshal(ev)
	level := ""
	if part == partError {
		level = "error"
	}
	return chstore.MLEvent{
		ID: chstore.MLEventID(det, svc, part), Detector: det, Signal: chstore.MLSignalLog, Stage: chstore.MLStageShadow,
		Service: svc, Level: level, StartedAt: last.UnixNano(), LastSeen: last.Add(time.Duration(cfg.BucketSec) * time.Second).UnixNano(),
		WindowSec: uint32(cfg.BucketSec), Baseline: j.Median, Observed: j.Observed, Score: math.Abs(j.Z), Direction: j.Direction, Evidence: string(raw),
	}
}
