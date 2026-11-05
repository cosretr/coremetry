package ml

// baseline_obs.go — v0.10.1814, öğrenme döngüsü Faz 1 dilim 1.3 (audit
// docs/anomaly/learning-loop-audit.md §4.1–4.2): günlük baseline gözlem
// çekirdeği (SAF). Bir yerel günün ml_series kovaları → ml_baseline_obs
// satırları. Karar çekirdeği bu sürümde bu satırları OKUMAZ (dilim 1.6).
//
// Sözleşme:
//   - Satır = (sinyal, servis, part, yerel saat): o saatin kovalarından
//     q10/q50/q90/q99/vmax, çağrı toplamı, kova sayısı.
//   - Tane: saatte birden çok tane varsa (canlı 5 dk + saatlik backfill)
//     yalnız EN İNCE tane kullanılır; saatten kaba kova (kod geçmişinin
//     86400 sn'lik satırı) yok sayılır.
//   - Değer: sayım parçaları (log; trace "|count" istek/sn DEĞİL) hedef
//     kovaya normalize edilir (count·hedef/bucket_sec, MLBaseline ile aynı);
//     oran/gecikme/gauge parçaları olduğu gibi.
//   - Çağrı: log sayım parçası Σ count; trace "|count" ve "_calls" Σ
//     istek/sn × bucket_sec; diğerleri 0.
//   - "_calls" (trace): servisin route "|count" serilerinin kova başına
//     toplamı — servis düzeyi trafik serisi (profil sınıflandırıcısı, §4.4).
//     Bağımlılık (dep|) serileri toplama girmez.
//   - Sınıf: tek işlev baselineObsClass (mlDayClass: takvim + yarım gün
//     13:00 kuralı); hafta içi/cumartesi/pazar = normal (gün slotta).
//   - Hijyen: etiketli gerçek anomali kovaları (quality.go Labels.Exclude,
//     MLExcluded ile aynı kapsayıcı sınır) dışlanır. Saatin TÜM kovaları
//     dışlanırsa satır buckets=0 ile yine yazılır: aynı günün yeniden koşusu
//     eski satırı ezer (RMT), bant buckets=0 haftayı saymaz.
//   - Kuantil: Hyndman–Fan tip 7 (doğrusal ara değer, numpy varsayılanı):
//     h = (n−1)·p, x[⌊h⌋] + (h−⌊h⌋)·(x[⌊h⌋+1] − x[⌊h⌋]). ≤ 12 örnekte de
//     aynı kural; tek örnek = o değer. Deterministik: girdi sırası çıktıyı
//     değiştirmez (BaselineDayObs sıralar; akış okuyucusu CH'de ORDER BY).

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// baselineCallsPart — trace servis düzeyi trafik serisinin part adı.
const baselineCallsPart = "_calls"

// baselineInput — bir günün hesap girdisi.
type baselineInput struct {
	Loc       *time.Location
	Cal       chstore.AnomalyCalendarConfig
	TargetSec int                   // canlı kova (cfg.BucketSec)
	Exclude   map[string][][2]int64 // servis → gerçek anomali pencereleri (nil = hijyen yok)
	CfgHash   string
}

// quantileHF7 — SAF: sıralı OLMAYABİLEN örneklerin p kuantili (tip 7).
func quantileHF7(xs []float64, p float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if p <= 0 {
		return s[0]
	}
	if p >= 1 {
		return s[n-1]
	}
	h := float64(n-1) * p
	lo := int(math.Floor(h))
	if lo >= n-1 {
		return s[n-1]
	}
	return s[lo] + (h-float64(lo))*(s[lo+1]-s[lo])
}

// baselineObsClass — SAF: saatin gözlem sınıfı (t yerel). Tek karar noktası:
// mlDayClass (takvim + yarım gün 13:00). İkinci takvim YOK.
func baselineObsClass(t time.Time, cal chstore.AnomalyCalendarConfig) string {
	switch mlDayClass(t, cal) {
	case "holiday":
		return "holiday"
	case "half_day":
		return "half_day"
	case "month_end":
		return "month_end"
	}
	return chstore.MLBaselineClassNormal
}

// baselineCfgHash — SAF: gözlemi etkileyen ayarların özeti (kova, hijyen,
// kuantil yöntemi). Tespit eşikleri gözlemi değiştirmez → hash'e girmez.
func baselineCfgHash(cfg chstore.MLConfig) string {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "obs-v1|bucket=%d|hygieneOff=%v|q=hf7", cfg.BucketSec, cfg.Quality.HygieneOff)
	return fmt.Sprintf("%016x", h.Sum64())
}

// baselineCountPart — SAF: part bir sayım serisi mi (normalize + çağrı).
// rate=true: değer istek/sn (çağrı = v × bucket_sec), false: ham sayım.
func baselineCountPart(signal, part string) (count, rate bool) {
	switch signal {
	case chstore.MLSignalLog:
		return true, false
	case chstore.MLSignalTrace:
		if part == baselineCallsPart {
			return true, true
		}
		if _, m := splitTracePart(part); m == "count" {
			return true, true
		}
	}
	return false, false
}

// obsPoint — seri noktası.
type obsPoint struct {
	t   int64
	sec uint32
	v   float64 // ham değer (sayım / oran / gauge)
}

// obsBuilder — akış hesaplayıcı: girdi (signal, service, part, bucket)
// sıralı gelir; seri bitince satırlar emit edilir. Bellek = tek seri +
// trace servisinin _calls toplamı (≤ 288 kova).
type obsBuilder struct {
	in   baselineInput
	emit func([]chstore.MLBaselineObs) error
	// geçerli seri
	sig, svc, part string
	pts            []obsPoint
	has            bool
	// trace servis düzeyi toplam (_calls)
	callsSvc string
	calls    map[int64]float64
	out      []chstore.MLBaselineObs
	series   int
}

func newObsBuilder(in baselineInput, emit func([]chstore.MLBaselineObs) error) *obsBuilder {
	if in.Loc == nil {
		in.Loc = time.UTC
	}
	if in.TargetSec <= 0 {
		in.TargetSec = 300
	}
	return &obsBuilder{in: in, emit: emit}
}

// add — sıralı girdiden bir satır.
func (b *obsBuilder) add(r chstore.MLSeriesRow) error {
	if b.has && (r.Signal != b.sig || r.Service != b.svc || r.Part != b.part) {
		if err := b.flushSeries(); err != nil {
			return err
		}
	}
	if b.callsSvc != "" && (r.Signal != chstore.MLSignalTrace || r.Service != b.callsSvc) {
		if err := b.flushCalls(); err != nil {
			return err
		}
	}
	if !b.has {
		b.sig, b.svc, b.part, b.has = r.Signal, r.Service, r.Part, true
	}
	b.pts = append(b.pts, obsPoint{t: r.Bucket.Unix(), sec: r.BucketSec, v: r.Count})
	if r.Signal == chstore.MLSignalTrace && !strings.HasPrefix(r.Part, depPartPrefix) && r.BucketSec == uint32(b.in.TargetSec) {
		if _, m := splitTracePart(r.Part); m == "count" {
			if b.calls == nil {
				b.calls = map[int64]float64{}
			}
			b.callsSvc = r.Service
			b.calls[r.Bucket.Unix()] += r.Count
		}
	}
	return nil
}

// finish — son seriyi ve servis toplamını boşaltır.
func (b *obsBuilder) finish() error {
	if b.has {
		if err := b.flushSeries(); err != nil {
			return err
		}
	}
	if b.callsSvc != "" {
		if err := b.flushCalls(); err != nil {
			return err
		}
	}
	return b.drain()
}

func (b *obsBuilder) drain() error {
	if len(b.out) == 0 || b.emit == nil {
		return nil
	}
	out := b.out
	b.out = nil
	return b.emit(out)
}

func (b *obsBuilder) flushSeries() error {
	b.out = append(b.out, seriesObs(b.sig, b.svc, b.part, b.pts, b.in)...)
	b.series++
	b.pts, b.has = b.pts[:0], false
	if len(b.out) >= 20000 {
		return b.drain()
	}
	return nil
}

func (b *obsBuilder) flushCalls() error {
	pts := make([]obsPoint, 0, len(b.calls))
	for t, v := range b.calls {
		pts = append(pts, obsPoint{t: t, sec: uint32(b.in.TargetSec), v: v})
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].t < pts[j].t })
	b.out = append(b.out, seriesObs(chstore.MLSignalTrace, b.callsSvc, baselineCallsPart, pts, b.in)...)
	b.series++
	b.callsSvc, b.calls = "", nil
	return nil
}

// seriesObs — SAF: bir serinin günlük noktaları → saat başına satır (slot sıralı).
func seriesObs(signal, service, part string, pts []obsPoint, in baselineInput) []chstore.MLBaselineObs {
	type hourKey struct {
		slot uint8
		week int64
	}
	type hourAcc struct {
		start time.Time
		week  time.Time
		pts   []obsPoint
	}
	groups := map[hourKey]*hourAcc{}
	for _, p := range pts {
		if p.sec == 0 || p.sec > 3600 {
			continue
		}
		t := time.Unix(p.t, 0)
		slot, week := chstore.MLBaselineSlot(t, in.Loc)
		k := hourKey{slot, week.Unix()}
		g := groups[k]
		if g == nil {
			lt := t.In(in.Loc)
			g = &hourAcc{start: time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour(), 0, 0, 0, in.Loc), week: week}
			groups[k] = g
		}
		g.pts = append(g.pts, p)
	}
	keys := make([]hourKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].week != keys[j].week {
			return keys[i].week < keys[j].week
		}
		return keys[i].slot < keys[j].slot
	})
	count, rate := baselineCountPart(signal, part)
	excl := in.Exclude[service]
	out := make([]chstore.MLBaselineObs, 0, len(keys))
	for _, k := range keys {
		g := groups[k]
		fine := uint32(math.MaxUint32)
		for _, p := range g.pts {
			if p.sec < fine {
				fine = p.sec
			}
		}
		var vals []float64
		var calls float64
		for _, p := range g.pts {
			if p.sec != fine || chstore.MLExcluded(excl, p.t) {
				continue
			}
			v := p.v
			switch {
			case count && rate:
				calls += p.v * float64(p.sec)
			case count:
				calls += p.v
				v = p.v * float64(in.TargetSec) / float64(p.sec)
			}
			vals = append(vals, v)
		}
		o := chstore.MLBaselineObs{Signal: signal, Service: service, Part: part, Slot: k.slot, WeekStart: g.week,
			Source: chstore.MLBaselineSourceSeries, Class: baselineObsClass(g.start, in.Cal), CfgHash: in.CfgHash,
			Buckets: uint8(min(len(vals), 255)), Calls: uint32(math.Min(math.Round(calls), math.MaxUint32))}
		if len(vals) > 0 {
			o.Q10, o.Q50 = float32(quantileHF7(vals, 0.10)), float32(quantileHF7(vals, 0.50))
			o.Q90, o.Q99 = float32(quantileHF7(vals, 0.90)), float32(quantileHF7(vals, 0.99))
			o.VMax = float32(quantileHF7(vals, 1))
		}
		out = append(out, o)
	}
	return out
}

// BaselineDayObs — SAF sarmalayıcı (test + determinizm): girdiyi akış
// okuyucusunun sırasına dizer (CH ORDER BY signal, service, part, bucket,
// bucket_sec) ve aynı akış hesaplayıcısından geçirir.
func BaselineDayObs(rows []chstore.MLSeriesRow, in baselineInput) []chstore.MLBaselineObs {
	rs := append([]chstore.MLSeriesRow(nil), rows...)
	sort.SliceStable(rs, func(i, j int) bool { return seriesRowLess(rs[i], rs[j]) })
	var out []chstore.MLBaselineObs
	b := newObsBuilder(in, func(o []chstore.MLBaselineObs) error { out = append(out, o...); return nil })
	for _, r := range rs {
		_ = b.add(r)
	}
	_ = b.finish()
	return out
}

// seriesRowLess — SAF: akış sırası (CH ORDER BY ile aynı) + tam eşitlikte değer.
func seriesRowLess(a, b chstore.MLSeriesRow) bool {
	switch {
	case a.Signal != b.Signal:
		return a.Signal < b.Signal
	case a.Service != b.Service:
		return a.Service < b.Service
	case a.Part != b.Part:
		return a.Part < b.Part
	case !a.Bucket.Equal(b.Bucket):
		return a.Bucket.Before(b.Bucket)
	case a.BucketSec != b.BucketSec:
		return a.BucketSec < b.BucketSec
	}
	return a.Count < b.Count
}

// baselineDayBounds — SAF: yerel günün [başlangıç, ertesi gün) UTC sınırları.
func baselineDayBounds(day string, loc *time.Location) (time.Time, time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	d, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("gün YYYY-MM-DD olmalı: %q", day)
	}
	return d.UTC(), d.AddDate(0, 0, 1).UTC(), nil
}

// baselineSeriesRetentionDays — ml_series TTL'i (store.go: 28 g).
const baselineSeriesRetentionDays = 28

// CalendarLocation — dedektörlerin takvim dilimi (calendarFor ile aynı kaynak).
func CalendarLocation(sens chstore.AnomalySensitivityConfig) *time.Location {
	_, loc := calendarFor(sens)
	return loc
}

// BaselineManualDayOK — SAF: elle koşulacak gün biçimli, bitmiş ve ml_series
// saklama süresi içinde mi.
func BaselineManualDayOK(day string, now time.Time, loc *time.Location) error {
	from, to, err := baselineDayBounds(day, loc)
	if err != nil {
		return err
	}
	if to.After(now) {
		return fmt.Errorf("%s henüz bitmedi", day)
	}
	lt := now.In(loc)
	today := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	if from.Before(today.AddDate(0, 0, -baselineSeriesRetentionDays)) {
		return fmt.Errorf("%s ml_series saklama süresinin (%d gün) dışında", day, baselineSeriesRetentionDays)
	}
	return nil
}

// baselineEarliestHour — günlük iş yerel bu saatten sonra dünü işler.
const baselineEarliestHour = 3

// baselinePendingDays — SAF: işlenecek günler, eskiden yeniye. Son uygun gün
// = 03:00 sonrası dün, öncesi evvelki gün; lastDay'den sonrası bekler, en
// yeni max gün (kesinti sonrası yetişme). lastDay biçimsizse hiç işlenmemiş.
func baselinePendingDays(now time.Time, loc *time.Location, lastDay string, max int) []string {
	if loc == nil {
		loc = time.UTC
	}
	lt := now.In(loc)
	latest := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	if lt.Hour() < baselineEarliestHour {
		latest = latest.AddDate(0, 0, -1)
	}
	var last time.Time
	if d, err := time.ParseInLocation("2006-01-02", lastDay, loc); err == nil {
		last = d
	}
	var out []string
	for i := max - 1; i >= 0; i-- {
		d := latest.AddDate(0, 0, -i)
		if !last.IsZero() && !d.After(last) {
			continue
		}
		out = append(out, d.Format("2006-01-02"))
	}
	return out
}
