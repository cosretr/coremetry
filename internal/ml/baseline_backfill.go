package ml

// baseline_backfill.go — v0.10.1815, öğrenme döngüsü Faz 1 dilim 1.4 (audit
// docs/anomaly/learning-loop-audit.md §4.2 son paragraf): ml_baseline_obs
// için 56 güne kadar geçmiş doldurma.
//
//   - ASLA otomatik değil (prod ES/VM maliyeti): yalnız admin
//     `POST /api/ml/baseline/backfill` isteği (`ml_baseline_control.backfill`)
//     başlatır; önce `?estimate=1` kuru koşu backend başına sorgu sayısını
//     ve süreyi söyler (PlanBaselineBackfill, SAF).
//   - Kaynak = canlı dedektörün kaynağı (karar S1, "dedektörle aynı kaynak"):
//     log → ES (canlı LogRED gibi MLLogAggregate; saat başına bir istek, kova
//     cfg.BucketSec, düşerse yarılar — backfill.go'nun saat mantığı), trace →
//     VM query_range (TraceRED'in dört sorgusu, gün başına; ağır hata → iki
//     yarım gün), metrik → VM query_range (açık ailelerin ifadeleri). Canlı
//     dedektörlerin CH'den okuduğu tek şey TABAN (ml_series) — o da bu
//     kaynaklardan yazılmış; ayrı bir CH-kaynaklı seri yok. Kapsam dışı:
//     şablon (tpl|), nadir kod (code|), bağımlılık (dep|), pod serileri —
//     planda "atlandı" olarak raporlanır.
//   - ml_series'e YAZILMAZ: 28 g TTL'li ve canlı tabanı besler; geçmiş kova
//     yazmak dedektör tabanını değiştirirdi. Yalnız ml_baseline_obs.
//   - Hesap 1814 çekirdeğiyle AYNI yol: kaynak günün (log'da saatin) kova
//     serisini MLSeriesRow olarak üretir → BaselineDayObs. Saat grupları
//     birbirinden bağımsız olduğu için log saat saat beslenir (bellek küçük).
//   - Hız sınırı: mevcut cfg.BackfillIntervalMs (her dış sorgu arası) +
//     cfg.BackfillTimeoutMs; VM ailesi mevcut vmBackoff'a tabi (canlıyla ortak).
//   - Sürdürülebilir: ilerleme (sinyal, gün) başına `ml_baseline_backfill`
//     blobunda; yeniden başlayınca biten günler atlanır. Lider-yalnız; ML ana
//     anahtarı / baseline anahtarı kapanınca, liderlik gidince ya da iptal
//     (DELETE) gelince gün sınırında (log'da saat sınırında) temiz durur.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	BaselineBackfillMaxDays = 56
	// backfillTraceQueriesPerFamily — etiket keşfi + count/err/p95/slow.
	backfillTraceQueriesPerFamily = 5
	backfillDefaultLatencyMs      = 1000
)

// BaselineBackfillSignals — doldurulabilen sinyaller.
var BaselineBackfillSignals = []string{chstore.MLSignalLog, chstore.MLSignalTrace, chstore.MLSignalMetric}

// BaselineBackfillPlanSignal — sinyal başına plan.
type BaselineBackfillPlanSignal struct {
	Days    int    `json:"days"`
	ES      int    `json:"es"`
	VM      int    `json:"vm"`
	Skipped string `json:"skipped,omitempty"` // dolu = bu sinyal koşmaz (neden)
}

// BaselineBackfillPlan — kuru koşu (?estimate=1).
type BaselineBackfillPlan struct {
	Days         int                                   `json:"days"`
	Signals      []string                              `json:"signals"`
	ES           int                                   `json:"es"`
	VM           int                                   `json:"vm"`
	CH           int                                   `json:"ch"` // okuma (etiket seti); yazımlar ayrı
	IntervalMs   int                                   `json:"intervalMs"`
	TimeoutMs    int                                   `json:"timeoutMs"`
	EstimatedSec int                                   `json:"estimatedSec"`
	PerSignal    map[string]BaselineBackfillPlanSignal `json:"perSignal"`
	NotCovered   []string                              `json:"notCovered"`
}

// backfillNotCovered — kaynakta günlük geçmişi alınamayan seri aileleri.
var backfillNotCovered = []string{
	"log şablon (tpl|) ve nadir kod (code|) serileri — ES'te geçmiş sayımı ayrı sorgu ister",
	"trace bağımlılık (dep|) ve pod serileri",
}

// PlanBaselineBackfill — SAF: istek → backend başına sorgu sayısı ve süre.
// Sinyal yalnız canlı dedektörü açıksa ve kaynağı varsa koşar (aksi seri
// zaten ml_series'te üretilmiyor). traceFamilies = VM RED ailesi sayısı.
func PlanBaselineBackfill(cfg chstore.MLConfig, days int, signals []string, esReady, vmReady bool, traceFamilies int, latencyMs int64) BaselineBackfillPlan {
	if latencyMs <= 0 {
		latencyMs = backfillDefaultLatencyMs
	}
	p := BaselineBackfillPlan{Days: days, Signals: signals, IntervalMs: cfg.BackfillIntervalMs, TimeoutMs: cfg.BackfillTimeoutMs,
		PerSignal: map[string]BaselineBackfillPlanSignal{}, NotCovered: backfillNotCovered}
	for _, sig := range signals {
		ps := BaselineBackfillPlanSignal{Days: days}
		switch sig {
		case chstore.MLSignalLog:
			switch {
			case !(&LogRED{}).Enabled(cfg):
				ps.Skipped = "log dedektörleri (hacim/hata/susma) kapalı"
			case !esReady:
				ps.Skipped = "log deposu Elasticsearch değil"
			default:
				ps.ES = days * 24
			}
		case chstore.MLSignalTrace:
			switch {
			case !(&TraceRED{}).Enabled(cfg):
				ps.Skipped = "trace dedektörleri (gecikme/hata/trafik) kapalı"
			case !vmReady:
				ps.Skipped = "VictoriaMetrics yapılandırılmamış"
			default:
				ps.VM = days * traceFamilies * backfillTraceQueriesPerFamily
			}
		case chstore.MLSignalMetric:
			n := len(metricExprs(cfg, "service_name", cfg.BucketSec))
			if n > 0 && cfg.DetectorOn("metric_kafka") && kafkaTopicDefault(cfg) {
				n++ // v0.10.2064: consumer lag topic geçmişi
			}
			switch {
			case n == 0:
				ps.Skipped = "açık metrik ailesi yok"
			case !vmReady:
				ps.Skipped = "VictoriaMetrics yapılandırılmamış"
			default:
				ps.VM = days * n
			}
		}
		p.PerSignal[sig] = ps
		p.ES += ps.ES
		p.VM += ps.VM
	}
	if p.ES+p.VM > 0 {
		p.CH = 1
	}
	q := int64(p.ES + p.VM)
	p.EstimatedSec = int(math.Ceil(float64(q*(int64(cfg.BackfillIntervalMs)+latencyMs)) / 1000))
	return p
}

// NormalizeBackfillSignals — SAF: bilinen, tekil, sıralı sinyaller.
func NormalizeBackfillSignals(in []string) []string {
	seen := map[string]bool{}
	for _, s := range in {
		seen[strings.ToLower(strings.TrimSpace(s))] = true
	}
	var out []string
	for _, s := range BaselineBackfillSignals {
		if seen[s] {
			out = append(out, s)
		}
	}
	return out
}

// backfillPlanDays — SAF: dünden geriye `days` yerel gün, EN YENİ önce
// (bant son 4–8 haftayı kullanır: yarıda kesilse de en değerli günler dolu).
func backfillPlanDays(at time.Time, loc *time.Location, days int) []string {
	if loc == nil {
		loc = time.UTC
	}
	lt := at.In(loc)
	y := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	out := make([]string, 0, days)
	for i := 0; i < days; i++ {
		out = append(out, y.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	return out
}

// backfillSource — bir sinyalin kaynağı: günü iş birimlerine böler (log:
// saat; trace/metrik: gün) ve birimin kova serisini üretir. Testte sahte.
type backfillSource interface {
	units(from, to time.Time) [][2]time.Time
	fetch(ctx context.Context, from, to time.Time) ([]chstore.MLSeriesRow, error)
}

// errBackfillWait — kaynak beklemede (VM backoff): gün bitmiş SAYILMAZ, koşu durur, sonra sürer.
var errBackfillWait = errors.New("kaynak beklemede")

// backfillStore — CH dikişi.
type backfillObsStore interface {
	InsertMLBaselineObs(ctx context.Context, rows []chstore.MLBaselineObs) error
	SaveMLBaselineBackfill(ctx context.Context, st chstore.MLBaselineBackfillStatus) error
}

// backfillEnv — koşunun bağımlılıkları (saf olmayan kenarlar enjekte).
type backfillEnv struct {
	store    backfillObsStore
	sources  map[string]backfillSource
	input    func(day string) baselineInput
	stop     func() (reason string, cancelled bool) // "" = devam
	sleep    func(time.Duration)
	interval time.Duration
}

func dayDone(ss *chstore.MLBaselineBackfillSignal, day string) bool {
	for _, d := range ss.Done {
		if d == day {
			return true
		}
	}
	return false
}

// runBaselineBackfill — planın (gün × sinyal) birimlerini koşar; her
// (sinyal, gün) sonunda ilerleme kaydedilir. Durursa Running=false + neden.
func runBaselineBackfill(ctx context.Context, env backfillEnv, st *chstore.MLBaselineBackfillStatus) {
	save := func() {
		st.UpdatedAt = time.Now().Unix()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := env.store.SaveMLBaselineBackfill(sctx, *st); err != nil {
			log.Printf("[ml] baseline backfill ilerlemesi yazılamadı: %v", err)
		}
	}
	halt := func(reason string, cancelled bool) {
		st.Running, st.Stopped = false, reason
		if cancelled {
			st.Cancelled = true
		}
		save()
		log.Printf("[ml] baseline backfill durdu: %s", reason)
	}
	st.Running, st.Stopped = true, ""
	save()
	for _, day := range st.PlanDays {
		for _, sig := range st.Signals {
			ss := st.PerSignal[sig]
			src := env.sources[sig]
			if ss == nil || ss.Skipped != "" || src == nil || dayDone(ss, day) {
				continue
			}
			from, to, err := baselineDayBounds(day, env.input(day).Loc)
			if err != nil {
				ss.Errors++
				ss.LastError = err.Error()
				continue
			}
			in := env.input(day)
			rows, errs := 0, 0
			for _, u := range src.units(from, to) {
				if reason, cancelled := env.stop(); reason != "" {
					halt(reason, cancelled)
					return
				}
				if ctx.Err() != nil {
					halt("kapanış", false)
					return
				}
				rs, ferr := src.fetch(ctx, u[0], u[1])
				if env.sleep != nil {
					env.sleep(env.interval)
				}
				if errors.Is(ferr, errBackfillWait) {
					halt(truncRunes(ferr.Error(), 200), false)
					return
				}
				if len(rs) > 0 { // yarı saat düşse de diğer yarı yazılır
					obs := BaselineDayObs(rs, in)
					if ierr := env.store.InsertMLBaselineObs(ctx, obs); ierr != nil {
						ferr = ierr
					} else {
						rows += len(obs)
					}
				}
				if ferr != nil {
					errs++
					ss.LastError = truncRunes(ferr.Error(), 200)
				}
			}
			ss.Done = append(ss.Done, day)
			ss.DaysDone++
			ss.Rows += rows
			ss.Errors += errs
			save()
			log.Printf("[ml] baseline backfill signal=%s day=%s rows=%d errors=%d (%d/%d)", sig, day, rows, errs, ss.DaysDone, ss.DaysTotal)
		}
	}
	st.Running, st.Complete = false, true
	save()
}

// ── kaynaklar ───────────────────────────────────────────────

func hourUnits(from, to time.Time) [][2]time.Time {
	var out [][2]time.Time
	for t := from; t.Before(to); t = t.Add(time.Hour) {
		out = append(out, [2]time.Time{t, t.Add(time.Hour)})
	}
	return out
}

func dayUnit(from, to time.Time) [][2]time.Time { return [][2]time.Time{{from, to}} }

// rowsFromSeries — SAF: anahtar → kova → değer haritası → sıralı olmayan satırlar.
func rowsFromSeries(signal string, series map[string]map[int64]float64, bucketSec int) []chstore.MLSeriesRow {
	var out []chstore.MLSeriesRow
	for k, byB := range series {
		svc, part, _ := strings.Cut(k, "\x00")
		for b, v := range byB {
			out = append(out, chstore.MLSeriesRow{Signal: signal, Service: svc, Part: part, Bucket: time.Unix(b, 0), BucketSec: uint32(bucketSec), Count: v})
		}
	}
	return out
}

// logBackfillSource — ES, saat başına bir agregasyon (canlı LogRED isteğinin
// aynısı, kova cfg.BucketSec; kardinalite alt agregasyonu KAPALI). Kısmi
// sonuç / hata → iki yarım saat (backfill.go tryHour deseni).
type logBackfillSource struct {
	agg      logstore.MLLogAggregator
	cfg      chstore.MLConfig
	sleep    func(time.Duration)
	interval time.Duration
}

func (s logBackfillSource) units(from, to time.Time) [][2]time.Time { return hourUnits(from, to) }

func (s logBackfillSource) try(ctx context.Context, from, to time.Time) ([]chstore.MLSeriesRow, error) {
	timeout := time.Duration(s.cfg.BackfillTimeoutMs) * time.Millisecond
	bctx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()
	t0 := time.Now()
	res, err := s.agg.MLLogAggregate(bctx, logstore.MLLogAggRequest{From: from, To: to, BucketSec: s.cfg.BucketSec,
		MaxServices: s.cfg.MaxServices, ErrorLevels: s.cfg.ErrorLevels, Timeout: timeout})
	global.sample(time.Since(t0).Milliseconds()) // gecikme örneği; tik ES bütçesini TÜKETMEZ
	if err != nil {
		return nil, err
	}
	if res.Partial {
		return nil, fmt.Errorf("kısmi sonuç: %s", res.PartialReason)
	}
	return rowsFromSeries(chstore.MLSignalLog, seriesFromRows(res.Rows, s.cfg.ErrorLevels), s.cfg.BucketSec), nil
}

func (s logBackfillSource) fetch(ctx context.Context, from, to time.Time) ([]chstore.MLSeriesRow, error) {
	rows, err := s.try(ctx, from, to)
	if err == nil || ctx.Err() != nil {
		return rows, err
	}
	mid := from.Add(to.Sub(from) / 2)
	var out []chstore.MLSeriesRow
	var firstErr error
	for _, h := range [][2]time.Time{{from, mid}, {mid, to}} {
		if s.sleep != nil {
			s.sleep(s.interval)
		}
		r, e := s.try(ctx, h[0], h[1])
		out = append(out, r...)
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}
	return out, firstErr
}

// vmRange — VM query_range, ağır hatada iki yarım gün; aile beklemedeyse
// (canlı ya da backfill backoff'u) errBackfillWait. Backfill'in backoff'u
// AYRI örnek: geçmiş sorgusunun 422'si canlı aileyi beklemeye almaz.
type vmRanger struct {
	vm       anomaly.REDVM
	live     *vmBackoff
	own      *vmBackoff
	step     int
	sleep    func(time.Duration)
	interval time.Duration
}

func (v vmRanger) query(ctx context.Context, family, expr string, from, to time.Time) ([]chstore.SpanMetricSeries, error) {
	now := time.Now()
	if v.live.skipping(family, now) || v.own.skipping(family, now) {
		return nil, fmt.Errorf("%w: VM ailesi %s", errBackfillWait, family)
	}
	run := func(f, t time.Time) ([]chstore.SpanMetricSeries, error) {
		t0 := time.Now()
		rs, err := v.vm.QueryPromQLRange(ctx, expr, f, t, v.step, 0)
		global.sample(time.Since(t0).Milliseconds())
		return rs, err
	}
	rs, err := run(from, to)
	if err == nil {
		v.own.ok(family)
		return rs, nil
	}
	if !heavyVMError(err) || ctx.Err() != nil {
		return nil, err
	}
	mid := from.Add(to.Sub(from) / 2)
	var out []chstore.SpanMetricSeries
	for _, h := range [][2]time.Time{{from, mid}, {mid, to}} {
		if v.sleep != nil {
			v.sleep(v.interval)
		}
		r, e := run(h[0], h[1])
		if e != nil {
			if heavyVMError(e) {
				d := v.own.fail(family, e, time.Now())
				return nil, fmt.Errorf("%w: VM ailesi %s %s (%v)", errBackfillWait, family, d.Round(time.Minute), e)
			}
			return nil, e
		}
		out = append(out, r...)
	}
	v.own.ok(family)
	return out, nil
}

// pointsOf — SAF: aralık sonucunun noktaları (sonlu değerler; Time kova başı ns).
func pointsOf(r chstore.SpanMetricSeries) []chstore.SpanMetricPoint {
	out := r.Points[:0:0]
	for _, p := range r.Points {
		if !math.IsNaN(p.Value) && !math.IsInf(p.Value, 0) {
			out = append(out, p)
		}
	}
	return out
}

// traceBackfillSource — VM, gün başına aile × (etiket keşfi + 4 sorgu);
// dönüşüm canlı TraceRED ile aynı (p95 birimi, slow = 1 − pay, err_ratio =
// err/count, "err" yazılmaz, seri tavanı traceRouteCap×3).
type traceBackfillSource struct {
	r   vmRanger
	cfg chstore.MLConfig
}

func (s traceBackfillSource) units(from, to time.Time) [][2]time.Time { return dayUnit(from, to) }

func (s traceBackfillSource) fetch(ctx context.Context, from, to time.Time) ([]chstore.MLSeriesRow, error) {
	series := map[string]map[int64]float64{}
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
	agg := newSvcAgg() // v0.10.2077: servis hata oranı tabanı (canlı tikle aynı dönüşüm)
	for fi, pin := range anomaly.REDFamilyPins() {
		family := "trace:" + pin.ID
		names, _, err := s.r.vm.MetricLabelNamesIn(ctx, pin.Names+"_count", from, to)
		if err != nil {
			return nil, fmt.Errorf("vm %s etiket keşfi: %w", pin.ID, err)
		}
		fl := pickTraceLabels(names, s.r.vm.MetricLabelMap().Service, pin)
		if !fl.ok {
			continue
		}
		cq, eq, pq := traceQueries(pin, fl.svc, fl.route, fl.status, s.cfg.BucketSec)
		sq := slowShareQuery(pin, fl.svc, fl.route, s.cfg.BucketSec, s.cfg.SlowRequestSec)
		scale := 1.0
		if pin.Unit == "s" {
			scale = 1000
		}
		for _, x := range []struct{ expr, measure string }{{cq, "count"}, {eq, "err"}, {pq, "p95"}, {sq, "slow"}} {
			if s.r.sleep != nil {
				s.r.sleep(s.r.interval)
			}
			rows, err := s.r.query(ctx, family, x.expr, from, to)
			if err != nil {
				return nil, fmt.Errorf("vm %s/%s: %w", pin.ID, x.measure, err)
			}
			if x.measure == "err" {
				agg.errRan(fi)
			}
			for _, r := range rows {
				lb := groupKeyLabels(r.GroupKey)
				svc, route := lb[fl.svc], lb[fl.route]
				if svc != "" && s.cfg.DetectorOn(detSvcErrorRate) {
					for _, p := range pointsOf(r) {
						agg.add(fi, svc, x.measure, p.Time/int64(time.Second), p.Value)
					}
				}
				if svc == "" || route == "" {
					continue
				}
				for _, p := range pointsOf(r) {
					v := p.Value
					if x.measure == "p95" {
						v *= scale
					}
					if x.measure == "slow" {
						v = math.Max(0, 1-v)
					}
					put(svc, tracePart(route, x.measure), p.Time/int64(time.Second), v)
				}
			}
		}
	}
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
	for k := range series {
		if _, m := splitTracePart(k); m == "err" {
			delete(series, k)
		}
	}
	agg.emit(series)
	return rowsFromSeries(chstore.MLSignalTrace, series, s.cfg.BucketSec), nil
}

// metricBackfillSource — VM, gün başına açık ailelerin her ifadesi.
type metricBackfillSource struct {
	r   vmRanger
	cfg chstore.MLConfig
}

func (s metricBackfillSource) units(from, to time.Time) [][2]time.Time { return dayUnit(from, to) }

func (s metricBackfillSource) fetch(ctx context.Context, from, to time.Time) ([]chstore.MLSeriesRow, error) {
	svcLabel := s.r.vm.MetricLabelMap().Service
	if svcLabel == "" {
		svcLabel = "service_name"
	}
	series := map[string]map[int64]float64{}
	for _, pe := range metricExprs(s.cfg, svcLabel, s.cfg.BucketSec) {
		if s.r.sleep != nil {
			s.r.sleep(s.r.interval)
		}
		rows, err := s.r.query(ctx, "metric:"+pe.family, pe.expr, from, to)
		if err != nil {
			return nil, fmt.Errorf("vm %s/%s: %w", pe.family, pe.id, err)
		}
		part := metricPart(pe.family, pe.id)
		// v0.10.2064 (Davis S3): canlı tik consumer lag'i topic anahtarlı yazar
		// (kafka_topic.go) → geçmişi de topic'li doldur (gün başına +1 sorgu;
		// adım başına servis başına üst-K topic). Servis düzeyi seri aynen.
		if pe.family == "metric_kafka" && pe.id == kafkaLagExprID && kafkaTopicDefault(s.cfg) {
			trows, terr := s.r.query(ctx, "metric:"+pe.family, kafkaTopicBackfillExpr(svcLabel, s.cfg.VMTargetCardinality), from, to)
			if terr != nil {
				return nil, fmt.Errorf("vm %s/%s topic: %w", pe.family, pe.id, terr)
			}
			for _, r := range trows {
				lb := groupKeyLabels(r.GroupKey)
				svc, t := lb[svcLabel], lb[kafkaTopicLabel]
				if svc == "" || t == "" {
					continue
				}
				k := chstore.MLSeriesKey(svc, kafkaTopicPart(t))
				if series[k] == nil {
					if len(series) >= metricSeriesCap {
						break
					}
					series[k] = map[int64]float64{}
				}
				for _, p := range pointsOf(r) {
					series[k][p.Time/int64(time.Second)] = p.Value
				}
			}
		}
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
			for _, p := range pointsOf(r) {
				series[k][p.Time/int64(time.Second)] = p.Value
			}
		}
	}
	return rowsFromSeries(chstore.MLSignalMetric, series, s.cfg.BucketSec), nil
}

// ── lider bağlantısı ────────────────────────────────────────

// backfillWaitRetry — kaynak beklemesi (VM backoff) sonrası yeniden deneme.
const backfillWaitRetry = 5 * time.Minute

// backfillJob — süreç-içi durum (lider pod). own: geçmiş sorgularının VM
// backoff'u (canlıdan ayrı).
type backfillJob struct {
	running atomic.Bool
	nextTry atomic.Int64
	own     *vmBackoff
}

// newBackfillStatus — SAF: istekten ilk durum (gün planı istek anına göre sabit).
func newBackfillStatus(req chstore.MLBaselineBackfillRequest, plan BaselineBackfillPlan, loc *time.Location) chstore.MLBaselineBackfillStatus {
	st := chstore.MLBaselineBackfillStatus{ID: req.ID, Days: req.Days, Signals: req.Signals, By: req.By,
		PlanDays: backfillPlanDays(time.Unix(req.At, 0), loc, req.Days), StartedAt: time.Now().Unix(),
		PerSignal: map[string]*chstore.MLBaselineBackfillSignal{}}
	for _, sig := range req.Signals {
		st.PerSignal[sig] = &chstore.MLBaselineBackfillSignal{DaysTotal: req.Days, Skipped: plan.PerSignal[sig].Skipped}
	}
	return st
}

// backfillAction — SAF: lider ne yapmalı. start=true → koş (st = sürdürülecek durum).
func backfillAction(req *chstore.MLBaselineBackfillRequest, cur chstore.MLBaselineBackfillStatus, have bool) (start, markCancelled bool) {
	if req == nil || req.ID == "" {
		return false, false
	}
	same := have && cur.ID == req.ID
	if req.Cancel {
		return false, same && !cur.Complete && !cur.Cancelled
	}
	if same && (cur.Complete || cur.Cancelled) {
		return false, false
	}
	return true, false
}

func (r *Recorder) maybeBackfill(ctx context.Context, cfg chstore.MLConfig, ctl chstore.MLBaselineControl, loc *time.Location) {
	if r.bf.running.Load() || time.Now().Unix() < r.bf.nextTry.Load() || ctl.Backfill == nil {
		return
	}
	cur, have, err := r.store.GetMLBaselineBackfill(ctx)
	if err != nil {
		return
	}
	start, markCancelled := backfillAction(ctl.Backfill, cur, have)
	if markCancelled {
		cur.Running, cur.Cancelled, cur.Stopped, cur.UpdatedAt = false, true, "iptal edildi", time.Now().Unix()
		_ = r.store.SaveMLBaselineBackfill(ctx, cur)
		return
	}
	if !start {
		return
	}
	req := *ctl.Backfill
	if !have || cur.ID != req.ID {
		_, esOK := logstore.Unwrap(r.logs).(logstore.MLLogAggregator)
		plan := PlanBaselineBackfill(cfg, req.Days, req.Signals, esOK, r.vm != nil && r.vm.Configured(), len(anomaly.REDFamilyPins()), 0)
		cur = newBackfillStatus(req, plan, loc)
	}
	r.bf.running.Store(true)
	go func(st chstore.MLBaselineBackfillStatus) {
		defer r.bf.running.Store(false)
		runBaselineBackfill(ctx, r.backfillEnv(ctx, cfg, req.ID, st.PlanDays), &st)
		if !st.Complete && !st.Cancelled {
			r.bf.nextTry.Store(time.Now().Add(backfillWaitRetry).Unix())
		}
	}(cur)
}

// backfillEnv — gerçek kaynaklar + durdurma koşulları.
func (r *Recorder) backfillEnv(ctx context.Context, cfg chstore.MLConfig, id string, days []string) backfillEnv {
	sleep := func(d time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
	}
	interval := time.Duration(cfg.BackfillIntervalMs) * time.Millisecond
	cal, loc := calendarFor(r.store.AnomalySensitivityForDetectors())
	var excl map[string][][2]int64
	if len(days) > 0 {
		if from, _, err := baselineDayBounds(days[len(days)-1], loc); err == nil {
			if labels, lerr := r.store.MLLabels(ctx, from.Add(-baselineLabelsSlack)); lerr == nil {
				excl = NewLabels(labels).Exclude(cfg)
			}
		}
	}
	in := baselineInput{Loc: loc, Cal: cal, TargetSec: cfg.BucketSec, Exclude: excl, CfgHash: baselineCfgHash(cfg)}
	srcs := map[string]backfillSource{}
	if agg, ok := logstore.Unwrap(r.logs).(logstore.MLLogAggregator); ok {
		srcs[chstore.MLSignalLog] = logBackfillSource{agg: agg, cfg: cfg, sleep: sleep, interval: interval}
	}
	if r.vm != nil && r.vm.Configured() {
		vr := vmRanger{vm: r.vm, live: r.backoff, own: r.bf.own, step: cfg.BucketSec, sleep: sleep, interval: interval}
		srcs[chstore.MLSignalTrace] = traceBackfillSource{r: vr, cfg: cfg}
		srcs[chstore.MLSignalMetric] = metricBackfillSource{r: vr, cfg: cfg}
	}
	return backfillEnv{store: r.store, sources: srcs, input: func(string) baselineInput { return in }, sleep: sleep, interval: interval,
		stop: func() (string, bool) {
			if ctx.Err() != nil {
				return "kapanış", false
			}
			if !r.leader.IsLeader() {
				return "liderlik başka pod'a geçti", false
			}
			sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if !r.store.GetMLConfig(sctx).BaselineOn() {
				return "ML ana anahtarı ya da baseline anahtarı kapandı", false
			}
			ctl, _, err := r.store.GetMLBaselineControl(sctx)
			if err != nil {
				return "", false // okunamadı: sürdür (bir sonraki birimde yeniden bakılır)
			}
			if ctl.Backfill == nil || ctl.Backfill.ID != id {
				return "istek değişti", false
			}
			if ctl.Backfill.Cancel {
				return "iptal edildi", true
			}
			return "", false
		}}
}
