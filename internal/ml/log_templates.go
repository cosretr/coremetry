package ml

// log_templates.go — ML log dilim 2 (v0.10.1755): şablon spike + nadir
// şablon. Operatör kararı C: sayım ES'te gerçek (Drain kimliği tikten tike
// kararsız, sayımı örnekten — v0.10.1030). Aile = servis × gövdedeki sabit
// ifade (şablonun <*> içermeyen en uzun ardışık belirteç dizisi); ifade
// anahtarın kendisi (ml_series part `tpl|<ifade>`), böylece restart sonrası
// izlenen aileler seriden geri yüklenir. Tik başına ES: 1 sayım isteği
// (≤100 aile) + yeni aile varsa 1 backfill isteği (BaselineDays, saatlik
// kova); ikisi de tik ES bütçesinden düşer. template_spike: taban medyanı
// ≥1 iken yukarı sapma (judge). rare_template: taban örneği yeterli ve
// hepsi 0 iken dwell kovalarının hepsi ≥ hacim tabanı.

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
	"github.com/cilcenk/coremetry/internal/templater"
)

const (
	detTemplateSpike  = "template_spike"
	detRareTemplate   = "rare_template"
	tplPartPrefix     = "tpl|"
	tplPhraseMaxRunes = 120
	tplNewPerTick     = 10
	tplRefreshEvery   = time.Hour
)

// templatePhrase — SAF: şablonun <*> içermeyen en uzun ardışık belirteç
// dizisi (en çok 8 belirteç); ≥3 belirteç ve ≥12 karakter değilse "".
func templatePhrase(tpl string) string {
	toks := strings.Fields(tpl)
	best, cur := []string{}, []string{}
	for _, t := range toks {
		if strings.Contains(t, "<*>") || strings.Contains(t, "<") && strings.Contains(t, ">") {
			if len(cur) > len(best) {
				best = cur
			}
			cur = []string{}
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > len(best) {
		best = cur
	}
	if len(best) > 8 {
		best = best[:8]
	}
	p := strings.Join(best, " ")
	if len(best) < 3 || len([]rune(p)) < 12 {
		return ""
	}
	if r := []rune(p); len(r) > tplPhraseMaxRunes {
		p = string(r[:tplPhraseMaxRunes])
	}
	return p
}

type tplFamily struct {
	service, phrase, sample string
}

func (f tplFamily) part() string { return tplPartPrefix + f.phrase }

// LogTemplates — dedektör.
type LogTemplates struct {
	mu        sync.Mutex
	tracked   map[string]tplFamily // part\x00service → aile
	restored  bool
	refreshed time.Time
	pending   []tplFamily // backfill bekleyen yeni aileler
	daily     map[string]int
	day       string
}

func NewLogTemplates() *LogTemplates {
	return &LogTemplates{tracked: map[string]tplFamily{}, daily: map[string]int{}}
}

func (d *LogTemplates) Name() string { return "log_templates" }

func (d *LogTemplates) Enabled(cfg chstore.MLConfig) bool {
	return cfg.DetectorOn(detTemplateSpike) || cfg.DetectorOn(detRareTemplate)
}

func (d *LogTemplates) dayCap(det string, cfg chstore.MLConfig, now time.Time) bool {
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

// selectFamilies — SAF: aday şablonlardan yeni aileler (izlenenler önce,
// toplam tavan). Aynı servisin izlenen ailesiyle Drain kuralına göre aynı
// ailedeki şablon yeni aile açmaz.
func selectFamilies(tracked map[string]tplFamily, cands []chstore.LogTemplate, cap int) []tplFamily {
	var out []tplFamily
	seen := map[string]bool{}
	for k := range tracked {
		seen[k] = true
	}
	bySvc := map[string][]tplFamily{}
	for _, f := range tracked {
		bySvc[f.service] = append(bySvc[f.service], f)
	}
	for _, t := range cands {
		if len(tracked)+len(out) >= cap {
			break
		}
		ph := templatePhrase(t.Template)
		if ph == "" {
			continue
		}
		for _, svc := range t.Services {
			f := tplFamily{service: svc, phrase: ph, sample: t.Template}
			k := f.part() + "\x00" + svc
			if seen[k] {
				continue
			}
			same := false
			for _, o := range bySvc[svc] {
				if o.sample != "" && templater.SameTemplateFamily(o.sample, t.Template) {
					same = true
					break
				}
			}
			if same {
				continue
			}
			seen[k] = true
			bySvc[svc] = append(bySvc[svc], f)
			out = append(out, f)
			if len(tracked)+len(out) >= cap {
				break
			}
		}
	}
	return out
}

// rareJudge — SAF: taban yeterli ve tamamen 0, dwell kovalarının hepsi ≥ min.
func rareJudge(cur, base []float64, cfg chstore.MLConfig) bool {
	if len(cur) < cfg.DwellBuckets || len(base) < cfg.MinBaselineSamples {
		return false
	}
	for _, v := range base {
		if v > 0 {
			return false
		}
	}
	min := math.Max(cfg.MinBaselineRate, 1)
	for _, v := range cur {
		if v < min {
			return false
		}
	}
	return true
}

func (d *LogTemplates) refresh(ctx context.Context, deps Deps, now time.Time) {
	if !d.restored {
		if keys, err := deps.Store.MLSeriesParts(ctx, chstore.MLSignalLog, tplPartPrefix, now.Add(-7*24*time.Hour)); err == nil {
			for _, k := range keys {
				f := tplFamily{service: k.Service, phrase: strings.TrimPrefix(k.Part, tplPartPrefix)}
				d.tracked[f.part()+"\x00"+f.service] = f
			}
			d.restored = true
		}
	}
	if now.Sub(d.refreshed) < tplRefreshEvery {
		return
	}
	cands, err := deps.Store.ListLogTemplates(ctx, chstore.ListLogTemplatesFilter{SinceNs: now.Add(-6 * time.Hour).UnixNano(), SortBy: "count", Limit: 500})
	if err != nil {
		return
	}
	d.refreshed = now
	for _, f := range selectFamilies(d.tracked, cands, logstore.MLTemplateFamilyCap) {
		d.tracked[f.part()+"\x00"+f.service] = f
		d.pending = append(d.pending, f)
	}
}

func (d *LogTemplates) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	counter, ok := logstore.Unwrap(deps.Logs).(logstore.MLTemplateCounter)
	if !ok {
		return nil, fmt.Errorf("log deposu ES değil: şablon dedektörleri sessiz")
	}
	if deps.ESLeft != nil && deps.ESLeft() < 1 {
		return nil, fmt.Errorf("ES tik bütçesi bitti: şablon sayımı atlandı (maxEsQueriesPerTick ≥ 2 gerekir)")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refresh(ctx, deps, now)
	fams := make([]tplFamily, 0, len(d.tracked))
	for _, f := range d.tracked {
		fams = append(fams, f)
	}
	sort.Slice(fams, func(i, j int) bool { return fams[i].service+fams[i].phrase < fams[j].service+fams[j].phrase })
	if len(fams) == 0 {
		return nil, nil
	}
	bucket := time.Duration(cfg.BucketSec) * time.Second
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	from, to := last.Add(-time.Duration(cfg.DwellBuckets+1)*bucket), last.Add(bucket)
	req := logstore.MLTemplateCountRequest{From: from, To: to, BucketSec: cfg.BucketSec}
	for _, f := range fams {
		req.Families = append(req.Families, logstore.MLTemplateFamily{Service: f.service, Phrase: f.phrase})
	}
	t0 := time.Now()
	counts, err := counter.MLTemplateCounts(ctx, req)
	deps.ObserveES(time.Since(t0))
	if err != nil {
		return nil, err
	}
	series := map[string]map[int64]float64{}
	var srows []chstore.MLSeriesRow
	for _, c := range counts {
		if c.Index < 0 || c.Index >= len(fams) {
			continue
		}
		f := fams[c.Index]
		k := chstore.MLSeriesKey(f.service, f.part())
		if series[k] == nil {
			series[k] = map[int64]float64{}
		}
		series[k][c.Bucket] = float64(c.Count)
		srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalLog, Service: f.service, Part: f.part(), Bucket: time.Unix(c.Bucket, 0), BucketSec: uint32(cfg.BucketSec), Count: float64(c.Count)})
	}
	if err := deps.Store.InsertMLSeries(ctx, srows); err != nil {
		return nil, err
	}
	d.backfill(ctx, deps, cfg, counter, now)

	sens := deps.Store.AnomalySensitivityForDetectors()
	cal, loc := calendarFor(sens)
	dates, class := baselineDates(last, cfg.BaselineDays, cal, loc)
	h := last.In(loc).Hour()
	base, err := deps.Store.MLBaseline(ctx, chstore.MLBaselineRequest{Signal: chstore.MLSignalLog, Dates: dates, HourFrom: max(h-1, 0), HourTo: min(h+1, 23),
		TargetSec: cfg.BucketSec, TZ: loc.String(), Exclude: deps.Labels.Exclude(cfg)})
	if err != nil {
		return nil, err
	}
	dwellStart := last.Add(-time.Duration(cfg.DwellBuckets-1) * bucket).Unix()
	c2 := cfg
	c2.MinBaselineRate = 0 // şablon sayımı küçük; hacim tabanı spike için anlamsız (etki eşiği recorder'da)
	var evs []chstore.MLEvent
	for _, f := range fams {
		k := chstore.MLSeriesKey(f.service, f.part())
		cur, ok := dwellWindow(series[k], dwellStart, last.Unix(), int64(cfg.BucketSec), true)
		if !ok {
			continue
		}
		if cfg.DetectorOn(detRareTemplate) && rareJudge(cur, base[k], cfg) && d.dayCap(detRareTemplate, cfg, now) {
			evs = append(evs, tplEvent(detRareTemplate, f, Judgement{Observed: cur[len(cur)-1], Z: cfg.CriticalZ, Direction: "up"}, last, cfg, class))
			continue
		}
		if !cfg.DetectorOn(detTemplateSpike) {
			continue
		}
		j := judge(cur, base[k], c2, true)
		if j.Fire && j.Median >= 1 && d.dayCap(detTemplateSpike, cfg, now) {
			evs = append(evs, tplEvent(detTemplateSpike, f, j, last, cfg, class))
		}
	}
	return evs, nil
}

// backfill — yeni ailelerin geçmişi (BaselineDays, saatlik kova): tik başına
// tek istek, ≤10 aile; bütçe yoksa sonraki tike kalır.
func (d *LogTemplates) backfill(ctx context.Context, deps Deps, cfg chstore.MLConfig, counter logstore.MLTemplateCounter, now time.Time) {
	if len(d.pending) == 0 || (deps.ESLeft != nil && deps.ESLeft() < 1) {
		return
	}
	n := min(len(d.pending), tplNewPerTick)
	batch := d.pending[:n]
	end := now.Truncate(time.Hour)
	req := logstore.MLTemplateCountRequest{From: end.Add(-time.Duration(cfg.BaselineDays) * 24 * time.Hour), To: end, BucketSec: 3600}
	for _, f := range batch {
		req.Families = append(req.Families, logstore.MLTemplateFamily{Service: f.service, Phrase: f.phrase})
	}
	t0 := time.Now()
	counts, err := counter.MLTemplateCounts(ctx, req)
	deps.ObserveES(time.Since(t0))
	if err != nil {
		return
	}
	var srows []chstore.MLSeriesRow
	for _, c := range counts {
		if c.Index < 0 || c.Index >= len(batch) {
			continue
		}
		f := batch[c.Index]
		srows = append(srows, chstore.MLSeriesRow{Signal: chstore.MLSignalLog, Service: f.service, Part: f.part(), Bucket: time.Unix(c.Bucket, 0), BucketSec: 3600, Count: float64(c.Count)})
	}
	if deps.Store.InsertMLSeries(ctx, srows) == nil {
		d.pending = d.pending[n:]
	}
}

func tplEvent(det string, f tplFamily, j Judgement, last time.Time, cfg chstore.MLConfig, class string) chstore.MLEvent {
	ev := mergeEvidence("", map[string]any{"phrase": f.phrase, "template": truncRunes(f.sample, sampleBodyCap), "dayClass": class, "mad": j.MAD,
		"dwell": cfg.DwellBuckets, "bucketSec": cfg.BucketSec, "baselineDays": cfg.BaselineDays})
	return chstore.MLEvent{
		ID: chstore.MLEventID(det, f.service, f.part()), Detector: det, Signal: chstore.MLSignalLog, Stage: chstore.MLStageShadow,
		Service: f.service, Code: f.phrase, StartedAt: last.UnixNano(), LastSeen: last.Add(time.Duration(cfg.BucketSec) * time.Second).UnixNano(),
		WindowSec: uint32(cfg.BucketSec), Baseline: j.Median, Observed: j.Observed, Score: math.Abs(j.Z), Direction: "up", Evidence: ev,
	}
}
