package anomaly

// anomaly_explain.go — "servis X için şu pencerede neden anomali yok?"
// (operatör, prod 2026-10-24: mobil BFF gateway'inde saatlerce süren 5xx
// artışı için Problem açılmadı; hangi kapının susturduğu koddan tek başına
// belirlenemiyordu — prod verisi + prod ayarı gerekiyordu).
//
// REPLAY: metrik dedektörünün kararını TEK servis × TEK metrik için geçmiş bir
// pencerede 5 dk'lık adımlarla yeniden koşar — aynı kaynak seçimi
// (vmREDView.seriesFor + pickREDSource), aynı kova kümeleri (vmConsecutiveBuckets
// / vmSeasonalBucketsCal = span WHERE'lerinin Go ikizi), aynı karar
// (evaluateAnomaly: ani sapma + sürekli teknik hata yükselişi). Her adım
// kaynağı, tabanı, z'yi ve AÇMAYI DURDURAN İLK KAPIYI yazar; açık/kapalı durumu
// adımlar boyunca simüle edilir (tek Problem, tazeleme, kapanış).
//
// Okumalar (yalnız istek anında, admin ucu): span MV'si için servis-filtreli
// TEK sorgu (service_summary_5m ORDER BY öneki; LIMIT + max_execution_time);
// VM için lider belleğine DOKUNMAYAN taze bir kova belleği ve servis
// eşleştiricili küçük sorgular (≤ explainMaxVMQueries). Lider olmayan podda da
// çalışır (durum ucunun aksine).
//
// Bilinen farklar (cevapta `notes`): takvim özel gününün 90 günlük mevsimsel
// okuması yerine yedek sınıf; batch p99 yük kapısının mevsimsel hacmi yok
// (yalnız p99_ms + batch serviste fark eder).
//
// v0.10.1521 — davranış motoru da AYNI adımlarda replay edilir
// (anomaly_explain_behavior.go): adım başına davranış hükmü (aday / yok),
// kaynağı ve durduran kapı. Ek okumalar: span MV'sine servis-filtreli tek
// 28 günlük sorgu + VM'e ≤ explainMaxVMBehaviorQueries kova sorgusu.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Tavanlar.
const (
	explainMaxWindow     = 12 * time.Hour
	explainDefaultWindow = 6 * time.Hour
	explainMaxVMQueries  = 60
	// explainMaxVMBehaviorQueries — v0.10.1521 davranış replay'inin 28 günlük
	// kova planının AYRI tavanı.
	explainMaxVMBehaviorQueries = 48
	explainVMRunLen             = 144 // tek servis: 144 nokta/seri
	explainBudget               = 45 * time.Second
	// explainTickOffset — adımın "şimdi"si kova sınırından bu kadar sonra:
	// span okuması bir önceki kovayı, VM (2 dk gecikme) aynı kovayı tamamlanmış
	// görür — liderin tipik tik konumu.
	explainTickOffset = 150 * time.Second
)

// ExplainRequest — tek servis × tek metrik × pencere.
type ExplainRequest struct {
	Service  string
	Metric   string // "" → error_rate
	From, To time.Time
}

// ExplainStep — bir replay adımı.
type ExplainStep struct {
	At           int64   `json:"at"` // adımın "şimdi"si (unix sn)
	Source       string  `json:"source"`
	SourceReason string  `json:"sourceReason,omitempty"`
	Current      float64 `json:"current"`
	Rate         float64 `json:"rate"` // en yeni kovanın istek/sn'si
	Median       float64 `json:"median"`
	MAD          float64 `json:"mad"`
	Z            float64 `json:"z"`
	Baseline     string  `json:"baseline,omitempty"`
	BaselineN    int     `json:"baselineN"`
	Action       string  `json:"action"`
	OpenedBy     string  `json:"openedBy,omitempty"`
	ZBlocker     string  `json:"zBlocker,omitempty"`
	RampBlocker  string  `json:"rampBlocker,omitempty"`
	RampBreach   int     `json:"rampBreaching"`
	RampThresh   float64 `json:"rampThreshold"`
	RampEPS      float64 `json:"rampErrorsPerSec"`
	Open         bool    `json:"open"` // adım sonrası simüle açık durum
	// v0.10.1521 — davranış motorunun AYNI adımdaki hükmü (anomaly_explain_behavior.go).
	Behavior *ExplainBehaviorStep `json:"behavior,omitempty"`
}

// ExplainReport — cevap gövdesi.
type ExplainReport struct {
	Service        string        `json:"service"`
	Metric         string        `json:"metric"`
	From           int64         `json:"from"`
	To             int64         `json:"to"`
	Tracked        bool          `json:"tracked"`
	Batch          bool          `json:"batch"`
	SpanPresent    bool          `json:"spanPresent"`
	VMConfigured   bool          `json:"vmConfigured"`
	VMMode         string        `json:"vmMode"`
	VMPresent      bool          `json:"vmPresent"`
	VMFamilies     []string      `json:"vmFamilies,omitempty"` // "id: usable|gerekçe"
	VMQueries      int           `json:"vmQueries"`
	VMErrors       []string      `json:"vmErrors,omitempty"`
	Sensitivity    string        `json:"sensitivity"`
	Steps          []ExplainStep `json:"steps"`
	FirstOpenAt    int64         `json:"firstOpenAt,omitempty"`
	Problems       int           `json:"problems"` // simüle açılış sayısı (dedup: tek açık satır)
	Verdict        string        `json:"verdict"`
	TopBlockers    []string      `json:"topBlockers,omitempty"`
	Notes          []string      `json:"notes,omitempty"`
	SpanReadError  string        `json:"spanReadError,omitempty"`
	EnvExcludesSvc bool          `json:"envExcludesService,omitempty"`
	// v0.10.1521 — davranış motoru özeti (adım ayrıntısı Steps[].behavior).
	BehaviorVerdict     string   `json:"behaviorVerdict,omitempty"`
	BehaviorFirstAt     int64    `json:"behaviorFirstAt,omitempty"`
	BehaviorSteps       int      `json:"behaviorCandidateSteps"`
	BehaviorTopBlockers []string `json:"behaviorTopBlockers,omitempty"`
	BehaviorReadError   string   `json:"behaviorReadError,omitempty"`
}

// spanRow — span MV'sinin bir kovası.
type spanRow struct{ v, rate float64 }

// spanRowsReader — servis-filtreli span okuması (test seam'i).
type spanRowsReader func(ctx context.Context, service, metric string, from, to time.Time) (map[int64]spanRow, error)

// ExplainDeps — G/Ç bağımlılıkları.
type ExplainDeps struct {
	Store *chstore.Store
	VM    REDVM // nil = VM yok
	// spanRows — testte sahte; nil → Store üzerinden.
	spanRows spanRowsReader
	// behaviorRows — v0.10.1521 davranış replay'inin span okuması; testte
	// sahte, nil → Store üzerinden.
	behaviorRows behaviorRowsReader
}

// NormalizeExplainWindow — SAF: pencereyi 5 dk ızgarasına hizalar, tavanlar.
func NormalizeExplainWindow(from, to, now time.Time) (time.Time, time.Time) {
	if to.IsZero() || to.After(now) {
		to = now
	}
	if from.IsZero() || !from.Before(to) {
		from = to.Add(-explainDefaultWindow)
	}
	if to.Sub(from) > explainMaxWindow {
		from = to.Add(-explainMaxWindow)
	}
	return from.UTC().Truncate(5 * time.Minute), to.UTC().Truncate(5 * time.Minute)
}

// ExplainServiceWindow — replay. Hata yalnız girdi geçersizse; okuma hataları
// rapora yazılır (yarım cevap da teşhistir).
func ExplainServiceWindow(ctx context.Context, deps ExplainDeps, req ExplainRequest) (ExplainReport, error) {
	metric := req.Metric
	if metric == "" {
		metric = errorRampMetric
	}
	if _, err := metricValueExpr(metric); err != nil {
		return ExplainReport{}, err
	}
	if strings.TrimSpace(req.Service) == "" {
		return ExplainReport{}, fmt.Errorf("service gerekli")
	}
	ctx, cancel := context.WithTimeout(ctx, explainBudget)
	defer cancel()
	sens := deps.Store.AnomalySensitivityForDetectors()
	days, minS, neighbor := seasonalParams(deps.Store.GetAnomalyPromotion(ctx))
	tracked := false
	for _, m := range deps.Store.AnomalyTracked().Enabled() {
		tracked = tracked || m == metric
	}
	reader := deps.spanRows
	if reader == nil {
		reader = storeSpanRows(deps.Store)
	}
	beh := deps.behaviorRows
	if beh == nil {
		beh = storeBehaviorRows(deps.Store)
	}
	return explainRun(ctx, deps.VM, reader, beh, req.Service, metric, req.From, req.To, sens, days, minS, neighbor, tracked), nil
}

// explainRun — çekirdek (sahte okuyucularla testli).
// beh nil → davranış replay'inde span satırı yok (VM satırları yine okunur).
func explainRun(ctx context.Context, vm REDVM, reader spanRowsReader, beh behaviorRowsReader, service, metric string, from, to time.Time,
	sens chstore.AnomalySensitivityConfig, days, minS, neighbor int, tracked bool) ExplainReport {
	rep := ExplainReport{Service: service, Metric: metric, From: from.Unix(), To: to.Unix(), Tracked: tracked,
		Batch: sens.IsBatchService(service), VMMode: sens.REDSourceMode(), Sensitivity: sensitivityLogLine(sens)}
	if !tracked {
		rep.Notes = append(rep.Notes, "metrik izlenmiyor (anomaly_tracked) — dedektör bu metriği hiç ölçmez")
	}
	if metric == "request_rate" && rep.Batch {
		rep.Notes = append(rep.Notes, "batch serviste request_rate değerlendirilmez (batchServicePatterns)")
	}
	steps := explainStepTimes(from, to)
	// ── span: tek okuma (en eski adımın mevsimsel başından son adıma) ──
	spanFrom := from.Add(-time.Duration(days)*24*time.Hour - time.Hour)
	rows, err := reader(ctx, service, metric, spanFrom, lastCompleteBucketStart(to.Add(explainTickOffset)))
	if err != nil {
		rep.SpanReadError = err.Error()
	}
	for b := range rows {
		if b >= from.Add(-24*time.Hour).Unix() {
			rep.SpanPresent = true
			break
		}
	}
	// ── VM: taze bellek, servis eşleştiricili sorgular ──
	withBeh := behaviorReplayable(metric, sens)
	vx := newExplainVM(ctx, vm, service, sens, steps, days, neighbor, withBeh)
	bx := newExplainBehavior(ctx, beh, withBeh, service, metric, steps, sens)
	rep.VMConfigured, rep.VMFamilies, rep.VMQueries, rep.VMErrors = vx.configured, vx.famLines(), vx.queries, vx.errs
	rep.EnvExcludesSvc = vx.envExcludes
	if vx.envExcludes {
		rep.Notes = append(rep.Notes, fmt.Sprintf("ortam kapsamı (redSourceEnv=%q) servisi DIŞARIDA bırakıyor: seriler ortam etiketi taşımıyor ya da başka değerde — VM bu servis için hiç okunmuyor", sens.REDSourceEnvScope()))
	}
	if !rep.SpanPresent {
		rep.Notes = append(rep.Notes, "servis span MV'sinde yok (son 24 sa) — v0.10.1518 öncesi dedektör bu servisi HİÇ değerlendirmiyordu; artık VM'de varsa VM'den değerlendirilir")
	}
	hasOpen := false
	blockers := map[string]int{}
	for _, now := range steps {
		st := ExplainStep{At: now.Unix()}
		view := vx.viewAt(now, sens, days, neighbor)
		bx.step(&st, now, view)
		s, u := view.seriesFor(service, metric, sens.DwellBuckets)
		if !rep.VMPresent && view.vmKnows(service) {
			rep.VMPresent = true
		}
		var vals, seas, rates []float64
		switch {
		case u.Hold:
			st.Source, st.SourceReason = "hold", u.Reason
		case u.VM:
			st.Source = redSourceVM
			vals, seas, rates = s.Values, s.Seasonal, s.Rates
		default:
			st.Source, st.SourceReason = redSourceSpan, shownSourceReason(u.Reason)
			vals, rates = spanConsecutiveAt(rows, now, sens.DwellBuckets)
			seas = spanSeasonalAt(rows, now, days, neighbor, sens.Calendar)
		}
		if len(rates) > 0 {
			st.Rate = rates[len(rates)-1]
		}
		var oc anomalyOutcome
		switch {
		case u.Hold:
			oc = anomalyOutcome{Action: "none"}
		case metric == "request_rate" && rep.Batch:
			oc = anomalyOutcome{Action: "skip"}
		default:
			oc = evaluateAnomaly(metric, vals, seas, rates, minS, hasOpen, batchLatSeries{Batch: rep.Batch}, sens)
		}
		st.Current, st.Median, st.MAD, st.Z = oc.Current, oc.Median, oc.MAD, oc.Z
		st.Baseline, st.BaselineN, st.Action, st.OpenedBy = oc.BaselineKind, oc.BaselineN, oc.Action, oc.OpenedBy
		st.ZBlocker = oc.ZBlocker
		if metric == errorRampMetric {
			st.RampBlocker, st.RampBreach, st.RampThresh, st.RampEPS = oc.Ramp.Blocker, oc.Ramp.Breaching, oc.Ramp.Threshold, oc.Ramp.CurEPS
		}
		switch oc.Action {
		case "open":
			if !hasOpen {
				rep.Problems++
				if rep.FirstOpenAt == 0 {
					rep.FirstOpenAt = st.At
				}
			}
			hasOpen = true
		case "resolve":
			hasOpen = false
		case "skip":
			blockers["veri yetersiz (seri / taban kısa) — "+st.Source]++
		}
		if oc.Action != "open" && oc.Action != "skip" && !hasOpen {
			b := st.ZBlocker
			if st.Source == "hold" {
				b = "hold: " + st.SourceReason
			}
			if b != "" {
				blockers[b]++
			}
		}
		st.Open = hasOpen
		rep.Steps = append(rep.Steps, st)
	}
	rep.TopBlockers = topBlockers(blockers, 4)
	rep.Verdict = explainVerdict(rep)
	bx.finish(&rep)
	rep.Notes = append(rep.Notes, "takvim özel gününde mevsimsel taban yedek sınıftan")
	return rep
}

// explainStepTimes — SAF: [from, to] içinde 5 dk adımların "şimdi"leri.
func explainStepTimes(from, to time.Time) []time.Time {
	var out []time.Time
	for t := from; !t.After(to); t = t.Add(5 * time.Minute) {
		out = append(out, t.Add(explainTickOffset))
	}
	return out
}

// spanConsecutiveAt — SAF: fetchAllBuckets'ın tek servis ikizi (aynı pencere,
// yalnız satırı olan kovalar, kuyruk sessizliği + dwell ızgarası sıfır
// dolgu — fillDwellGrid, v0.10.1585).
func spanConsecutiveAt(rows map[int64]spanRow, now time.Time, dwell int) ([]float64, []float64) {
	var vals, rates []float64
	var times []int64
	for _, b := range vmConsecutiveBuckets(now) {
		r, ok := rows[b]
		if !ok {
			continue
		}
		vals, rates, times = append(vals, r.v), append(rates, r.rate), append(times, b)
	}
	return fillDwellGrid(vals, rates, times, lastCompleteBucketStart(now).Unix(), dwell)
}

// spanSeasonalAt — SAF: fetchAllSeasonal'ın tek servis ikizi (aynı kova
// kümesi + gün çeşitliliği budaması; takvim özel günü yedek sınıfla).
func spanSeasonalAt(rows map[int64]spanRow, now time.Time, days, neighbor int, cal chstore.AnomalyCalendarConfig) []float64 {
	set, class := vmSeasonalBucketsCal(now, days, neighbor, cal)
	var out []float64
	seen := map[int64]struct{}{}
	for _, b := range set {
		r, ok := rows[b]
		if !ok {
			continue
		}
		out = append(out, r.v)
		seen[b/86400] = struct{}{}
	}
	if len(seen) < seasonalMinDaysFor(class) {
		return nil
	}
	return out
}

// topBlockers — SAF: en sık n kapı ("kapı ×sayı"), sayıya göre azalan.
func topBlockers(m map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	var xs []kv
	for k, v := range m {
		xs = append(xs, kv{k, v})
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].v != xs[j].v {
			return xs[i].v > xs[j].v
		}
		return xs[i].k < xs[j].k
	})
	var out []string
	for i, x := range xs {
		if i >= n {
			break
		}
		out = append(out, x.k+" ×"+strconv.Itoa(x.v))
	}
	return out
}

// explainVerdict — SAF: tek cümlelik özet.
func explainVerdict(r ExplainReport) string {
	if r.FirstOpenAt != 0 {
		by := ""
		for _, s := range r.Steps {
			if s.At == r.FirstOpenAt {
				by = s.OpenedBy
				break
			}
		}
		return fmt.Sprintf("bu sürümün kuralları %s'te açardı (kol: %s, kaynak: %s); pencerede %d açılış",
			time.Unix(r.FirstOpenAt, 0).UTC().Format(time.RFC3339), by, stepSource(r, r.FirstOpenAt), r.Problems)
	}
	if len(r.TopBlockers) > 0 {
		return "açılmazdı — en sık kapı: " + r.TopBlockers[0]
	}
	return "açılmazdı"
}

func stepSource(r ExplainReport, at int64) string {
	for _, s := range r.Steps {
		if s.At == at {
			return s.Source
		}
	}
	return ""
}

// storeSpanRows — servis-filtreli span MV okuması (ORDER BY öneki service_name).
func storeSpanRows(st *chstore.Store) spanRowsReader {
	return func(ctx context.Context, service, metric string, from, to time.Time) (map[int64]spanRow, error) {
		vexpr, err := metricValueExpr(metric)
		if err != nil {
			return nil, err
		}
		rows, err := st.TelemetryReadConn().Query(ctx, buildExplainSpanQuery(vexpr), service, from, to)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := map[int64]spanRow{}
		for rows.Next() {
			var t uint32
			var v, rate float64
			if err := rows.Scan(&t, &v, &rate); err != nil {
				return nil, err
			}
			out[int64(t)] = spanRow{v: v, rate: rate}
		}
		return out, rows.Err()
	}
}

// buildExplainSpanQuery — SAF: buildAllBucketsQuery'nin tek servis ikizi
// (aynı ifade + aynı hacim kolonu; servis ORDER BY önekinde → budama).
// Bağlar: servis, başlangıç, üst sınır (tamamlanmış kovalar).
func buildExplainSpanQuery(vexpr string) string {
	return fmt.Sprintf(`
		SELECT toUnixTimestamp(time_bucket) AS t, %s AS v,
		       countMerge(span_count_state) / 300.0 AS rate
		FROM service_summary_5m
		WHERE service_name = ? AND time_bucket >= ? AND time_bucket < ?
		GROUP BY t
		ORDER BY t
		LIMIT 30000
		SETTINGS max_execution_time = 15`, vexpr)
}

// ── VM tarafı ──────────────────────────────────────────────────────────────

// explainVM — replay'in taze VM belleği.
type explainVM struct {
	configured  bool
	src         *vmREDSource
	queries     int
	errs        []string
	envExcludes bool
	behavior    bool // v0.10.1521: davranış kovaları okundu → görünüm readyBeh hesaplar
}

// newExplainVM — aile keşfi (liderle AYNI refreshMeta: servis etiketi, durum
// etiketleri, ortam kapsamı), sonra kapsam servis eşleştiricisiyle daraltılır
// ve adımların gerektirdiği kovalar okunur.
func newExplainVM(ctx context.Context, vm REDVM, service string, sens chstore.AnomalySensitivityConfig, steps []time.Time, days, neighbor int, withBehavior bool) *explainVM {
	x := &explainVM{src: newVMREDSource(), behavior: withBehavior}
	if vm == nil || !vm.Configured() || sens.REDSourceMode() == chstore.AnomalyREDSourceSpan || len(steps) == 0 {
		return x
	}
	x.configured = true
	last := steps[len(steps)-1].Add(-vmIngestLag)
	lm := vm.MetricLabelMap().Normalized()
	env := sens.REDSourceEnvScope()
	for fi := range vmREDFamilies {
		x.src.refreshMeta(ctx, vm, fi, lm, env, last)
		m := &x.src.meta[fi]
		if !m.ok {
			continue
		}
		m.scope.EnvMatcher = joinMatchers(m.scope.EnvMatcher, m.scope.SvcLabel+"="+strconv.Quote(service))
		m.scopeKey += "|svc"
		x.queries += x.src.refreshStatus(ctx, vm, fi, last)
	}
	req := map[int64]bool{}
	for _, now := range steps {
		vnow := now.Add(-vmIngestLag)
		seas, _ := vmSeasonalBucketsCal(vnow, days, neighbor, sens.Calendar)
		for _, b := range append(vmConsecutiveBuckets(vnow), seas...) {
			req[b] = true
		}
	}
	plan := x.planFor(req)
	if len(plan) > explainMaxVMQueries {
		x.errs = append(x.errs, fmt.Sprintf("sorgu tavanı: %d/%d plan sorgusu koşturuldu (en yeni önce)", explainMaxVMQueries, len(plan)))
		plan = plan[:explainMaxVMQueries]
	}
	n, nerr, _, lastErr := x.src.execute(ctx, vm, plan, last)
	x.queries += n
	if nerr > 0 {
		x.errs = append(x.errs, fmt.Sprintf("%d sorgu hata verdi: %s", nerr, lastErr))
	}
	// v0.10.1521 — davranış replay'inin 28 günlük haftanın-saati kovaları:
	// AYRI plan ve AYRI tavan (dedektörün kovalarını tavandan itmesin).
	if withBehavior {
		breq := map[int64]bool{}
		for _, now := range steps {
			for _, b := range vmBehaviorBuckets(now.Add(-vmIngestLag)) {
				breq[b] = true
			}
		}
		bplan := x.planFor(breq)
		if len(bplan) > explainMaxVMBehaviorQueries {
			x.errs = append(x.errs, fmt.Sprintf("davranış sorgu tavanı: %d/%d — eksik kovalı adımlarda davranış span satırlarından", explainMaxVMBehaviorQueries, len(bplan)))
			bplan = bplan[:explainMaxVMBehaviorQueries]
		}
		n, nerr, _, lastErr := x.src.execute(ctx, vm, bplan, last)
		x.queries += n
		if nerr > 0 {
			x.errs = append(x.errs, fmt.Sprintf("davranış: %d sorgu hata verdi: %s", nerr, lastErr))
		}
	}
	// Ortam kapsamı servisi dışarıda mı bırakıyor? Kapsamlı okuma hiç istek
	// görmediyse, ortamsız tek bir sayım sorgusu (son 1 sa) sorulur.
	if env != "" && !x.anyCount() {
		x.envExcludes, x.queries = x.probeWithoutEnv(ctx, vm, service, last), x.queries+1
	}
	return x
}

// planFor — eksik kovaların sorgu planı (en yeni önce; liderin planlayıcısıyla
// aynı koşu / gruplama / uzunluk bölmesi).
func (x *explainVM) planFor(req map[int64]bool) []vmPlanned {
	required := make([]int64, 0, len(req))
	for b := range req {
		required = append(required, b)
	}
	sort.Slice(required, func(i, j int) bool { return required[i] > required[j] })
	var plan []vmPlanned
	for fi, f := range vmREDFamilies {
		m := x.src.meta[fi]
		if !m.ok {
			continue
		}
		for k := vmREDKind(0); k < vmKindN; k++ {
			if _, ok := vmREDExpr(f, k, m.scope, 0); !ok {
				continue
			}
			for _, g := range vmGroupRuns(vmRuns(x.src.store.missing(fi, k, required), explainVMRunLen), vmMaxOffsetsPerQuery) {
				build := func(offs []int64) string { q, _ := vmREDQuery(f, k, m.scope, offs); return q }
				for _, sg := range vmSplitForLen(g, build) {
					if q := build(sg.Offsets); q != "" && len(q) <= vmMaxQueryLen {
						plan = append(plan, vmPlanned{fam: fi, kind: k, group: sg, query: q})
					}
				}
			}
		}
	}
	return plan
}

func joinMatchers(a, b string) string {
	if strings.TrimSpace(a) == "" {
		return b
	}
	return a + ", " + b
}

// anyCount — bellekte servisin herhangi bir sayım değeri var mı.
func (x *explainVM) anyCount() bool {
	for _, fr := range x.src.store.frames {
		for _, ff := range fr.fam {
			if ff == nil {
				continue
			}
			for _, v := range ff.vals[vmKindCount] {
				if v > 0 {
					return true
				}
			}
		}
	}
	return false
}

// probeWithoutEnv — ortam süzgeçsiz son 1 sa sayım (yalnız ilk kullanılabilir
// ya da ortam yüzünden kullanılamayan HTTP ailesi). true = servis VM'de VAR.
func (x *explainVM) probeWithoutEnv(ctx context.Context, vm REDVM, service string, at time.Time) bool {
	f := vmREDFamilies[0]
	svcLabel := x.src.meta[0].scope.SvcLabel
	if svcLabel == "" {
		svcLabel = "service_name"
	}
	sc := vmQueryScope{SvcLabel: svcLabel, EnvMatcher: svcLabel + "=" + strconv.Quote(service)}
	q := "sum by (" + svcLabel + ") (increase(" + vmSelector(f, "count", sc, "") + "[1h]))"
	qctx, cancel := context.WithTimeout(ctx, vmQueryTimeout)
	defer cancel()
	ser, err := vm.QueryPromQLInstant(qctx, q, at)
	if err != nil {
		x.errs = append(x.errs, "ortamsız sayım: "+err.Error())
		return false
	}
	for _, s := range ser {
		for _, p := range s.Points {
			if p.Value > 0 {
				return true
			}
		}
	}
	return false
}

// famLines — aile durumları ("http: usable" | "http: <gerekçe>").
func (x *explainVM) famLines() []string {
	if !x.configured {
		return nil
	}
	var out []string
	for fi, f := range vmREDFamilies {
		m := x.src.meta[fi]
		s := f.ID + ": "
		if m.ok {
			s += "usable · durum etiketleri=" + strings.Join(m.scope.StatusLabels, ",")
		} else {
			s += m.reason
		}
		out = append(out, s)
	}
	return out
}

// viewAt — adımın görünümü (liderin prepare'iyle aynı alanlar, okuma yok).
func (x *explainVM) viewAt(now time.Time, sens chstore.AnomalySensitivityConfig, days, neighbor int) *vmREDView {
	v := &vmREDView{mode: sens.REDSourceMode(), configured: x.configured, store: x.src.store}
	if !x.configured {
		v.reason = redReasonNotConfigured
		return v
	}
	vnow := now.Add(-vmIngestLag)
	v.vnow, v.upper = vnow, lastCompleteBucketStart(vnow).Unix()
	v.consec = vmConsecutiveBuckets(vnow)
	v.seasonal, v.seasonalClass = vmSeasonalBucketsCal(vnow, days, neighbor, sens.Calendar)
	if x.behavior {
		v.behavior = vmBehaviorBuckets(vnow)
	}
	v.meta = append([]vmFamMeta(nil), x.src.meta...)
	for fi, f := range vmREDFamilies {
		if !x.src.meta[fi].ok {
			continue
		}
		for k := vmREDKind(0); k < vmKindN; k++ {
			if _, ok := vmREDExpr(f, k, x.src.meta[fi].scope, 0); !ok {
				continue
			}
			v.readyConsec[fi][k] = len(x.src.store.missing(fi, k, v.consec)) == 0
			v.readyDet[fi][k] = v.readyConsec[fi][k] && len(x.src.store.missing(fi, k, v.seasonal)) == 0
			v.readyBeh[fi][k] = len(v.behavior) > 0 && len(x.src.store.missing(fi, k, v.behavior)) == 0
			if until, ok := x.src.backoff[[2]int{fi, int(k)}]; ok && !until.IsZero() {
				v.failed[fi][k] = true
			}
		}
	}
	return v
}
