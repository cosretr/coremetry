package ml

// engine_compare.go — v0.10.2309: Davis özneleri (anomaly_events
// kind='subject_red') ile ML olaylarının (ml_events) salt-okunur yan yana
// karşılaştırması. SAF: okumalar çağıranda (api/anomaly_engine_compare.go).
//
// Eşleme anahtarı (servis, aile, kaynak); aralıklar [started_at, last_seen]
// ±engineCompareTolerance içinde kesişirse eşleşir (overlaps, svc_compare.go).
//
//	aile          özne tarafı                     ML dedektörleri
//	latency       endpoint p99_ms / p95_ms        op_latency, svc_breadth, svc_response_time
//	errors        endpoint error_rate             op_error_rate, svc_error_rate, trace_error_count
//	throughput    endpoint request_rate (down)    throughput_drop
//	consumer_lag  queue consumer_lag              metric_kafka (code metric_kafka|consumer_lag…)
//	örneklenmiş   queue p99/error_rate/request    dependency_latency / dependency_error_rate,
//	              (sampled_trace, biased)         code messaging|<destination>; hedefe göre
//
// Özne satırının kimliği zamansızdır: özne × metrik TEK satır, aradan sonra
// yeniden ateşleme started_at'i ezer; yalnız episode_count ve
// first_started_at kalır. Bu yüzden özne tarafında yalnız SON bölüm eşlenir
// ve önceki bölümü ezilmiş olabilecek ML olayı "belirsiz" sayılır, "yalnız
// ML" değil. Oylar / kararlar hiçbir yerde etiket değildir (operatör kuralı).

import (
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// engineCompareTolerance — evaluator subjectGroupWindow (özne grubu, 15 dk)
// ve svcCompareSlack (servis düzeyi gölge karşılaştırması) ile aynı pencere.
const engineCompareTolerance = svcCompareSlack

// EngineCompareTolerance / EngineCompareShortSec — cevap başlığı için.
// Kısa = Davis özne terfi kapısının varsayılan en az süresinin (singleMinSustained,
// 600 sn) altında kalan olay.
var (
	EngineCompareTolerance = engineCompareTolerance
	EngineCompareShortSec  = int64(singleMinSustained / time.Second)
)

// EngineCompareRowCap — satır tavanı (toplam olaya göre sıralı, fazlası kesilir).
const EngineCompareRowCap = 500

// Aileler ve kaynaklar.
const (
	EngFamLatency     = "latency"
	EngFamErrors      = "errors"
	EngFamThroughput  = "throughput"
	EngFamConsumerLag = "consumer_lag"

	EngSrcMetric       = "metric"
	EngSrcSampledTrace = "sampled_trace"
)

// EngineSubjectIn — özne satırı (son bölüm).
type EngineSubjectIn struct {
	ID, Service, SubjectKind, Pattern, Direction, Promotion string
	Start, End                                              int64
	EpisodeCount                                            uint32
	FirstStart                                              int64
}

// EngineMLIn — ML olayı (epizot başına satır).
type EngineMLIn struct {
	ID, Detector, Service, Code, Direction string
	Start, End                             int64
	WouldOpen, LiveOpened                  bool
}

// EngineRefProblem — iki motordan bağımsız referans Problem (End 0 = açık).
type EngineRefProblem struct {
	Service    string
	Start, End int64
}

// EngineSubjectInOf — SAF: anomaly_events satırı → girdi.
func EngineSubjectInOf(ev chstore.AnomalyEvent) EngineSubjectIn {
	return EngineSubjectIn{ID: ev.ID, Service: ev.Service, SubjectKind: ev.SubjectKind, Pattern: ev.Pattern,
		Direction: ev.Direction, Promotion: ev.Promotion, Start: ev.StartedAt, End: ev.LastSeen,
		EpisodeCount: ev.EpisodeCount, FirstStart: ev.FirstStartedAt}
}

// EngineMLInOf — SAF: ML satırı → girdi. "Problem açardı" grup ya da servis
// gölge kararından (GroupShadowOf / SvcShadowOf), "açtı" aynı kararın Live
// bayrağından.
func EngineMLInOf(e chstore.EngineCompareMLRow) EngineMLIn {
	in := EngineMLIn{ID: e.ID, Detector: e.Detector, Service: e.Service, Code: e.Code, Direction: e.Direction,
		Start: e.StartedAt, End: e.LastSeen}
	if g, ok := GroupShadowOf(e.Evidence); ok {
		in.WouldOpen = in.WouldOpen || g.WouldOpen
		in.LiveOpened = in.LiveOpened || g.Live
	}
	if s, ok := SvcShadowOf(e.Evidence); ok {
		in.WouldOpen = in.WouldOpen || s.WouldOpen
		in.LiveOpened = in.LiveOpened || s.Live
	}
	return in
}

// EngineRefRule — SAF: kural referans Problem mi (iki motorun da açmadığı).
func EngineRefRule(rule string) bool {
	for _, p := range chstore.EngineCompareNonRefRulePrefixes {
		if strings.HasPrefix(rule, p) {
			return false
		}
	}
	return true
}

// metricFamily — SAF: özne metriği → aile ("" = eşlenmez).
func metricFamily(metric string) string {
	switch metric {
	case "p99_ms", "p95_ms":
		return EngFamLatency
	case "error_rate":
		return EngFamErrors
	case "request_rate":
		return EngFamThroughput
	case "consumer_lag":
		return EngFamConsumerLag
	}
	return ""
}

// engKey — satır anahtarı. Örneklenmiş kuyruk satırında servis yerine hedef
// (destination) — iki taraf yalnız onda buluşur.
type engKey struct{ svc, family, source string }

// queueDestination — SAF: "queue:<sistem>@<hedef>" → hedef.
func queueDestination(svc string) string {
	if _, d, ok := strings.Cut(svc, "@"); ok {
		return d
	}
	return svc
}

// subjectSide — SAF: özne satırının aile / kaynak / anahtarı ve karşılığı
// olup olmadığı. biased: subject_kind queue|db ve metrik consumer_lag
// değil (başarılı trace ~%5 örneklenir, hata %100 → hata oranı şişik).
func subjectSide(s EngineSubjectIn) (k engKey, biased, counterpart, ok bool) {
	metric := s.Pattern
	if i := strings.Index(metric, " @ "); i >= 0 {
		metric = metric[:i]
	}
	fam := metricFamily(metric)
	if fam == "" {
		return engKey{}, false, false, false
	}
	switch s.SubjectKind {
	case chstore.AnomalySubjectEndpoint:
		if fam == EngFamConsumerLag {
			return engKey{}, false, false, false
		}
		counterpart = fam != EngFamThroughput || s.Direction == chstore.AnomalyDirectionDown
		return engKey{s.Service, fam, EngSrcMetric}, false, counterpart, true
	case chstore.AnomalySubjectQueue:
		if fam == EngFamConsumerLag {
			return engKey{s.Service, fam, EngSrcMetric}, false, true, true
		}
		// Örneklenmiş: ML'de yalnız gecikme ve hata karşılığı var.
		return engKey{queueDestination(s.Service), fam, EngSrcSampledTrace}, true, fam != EngFamThroughput, true
	case chstore.AnomalySubjectDB:
		if fam == EngFamConsumerLag {
			return engKey{}, false, false, false
		}
		// DB örneklenmiş özne: ML'de bu raporun eşlediği karşılığı yok.
		return engKey{s.Service, fam, EngSrcSampledTrace}, true, false, true
	}
	return engKey{}, false, false, false
}

// mlSide — SAF: ML olayının aile / kaynak / anahtarı.
func mlSide(e EngineMLIn) (k engKey, biased, ok bool) {
	switch e.Detector {
	case "op_latency", "svc_breadth", "svc_response_time":
		return engKey{e.Service, EngFamLatency, EngSrcMetric}, false, true
	case "op_error_rate", "svc_error_rate", "trace_error_count":
		return engKey{e.Service, EngFamErrors, EngSrcMetric}, false, true
	case "throughput_drop":
		return engKey{e.Service, EngFamThroughput, EngSrcMetric}, false, true
	case "metric_kafka":
		if strings.HasPrefix(e.Code, chstore.EngineCompareKafkaLagCodePrefix) {
			return engKey{e.Service, EngFamConsumerLag, EngSrcMetric}, false, true
		}
	case "dependency_latency", "dependency_error_rate":
		dest, ok := strings.CutPrefix(e.Code, chstore.EngineCompareMessagingCodePrefix)
		if !ok {
			return engKey{}, false, false
		}
		fam := EngFamLatency
		if e.Detector == "dependency_error_rate" {
			fam = EngFamErrors
		}
		return engKey{dest, fam, EngSrcSampledTrace}, true, true
	}
	return engKey{}, false, false
}

// EngineSubjectStats — özne kolu sayaçları.
type EngineSubjectStats struct {
	Series        int     `json:"series"`
	Episodes      int     `json:"episodes"`
	EpisodesExact bool    `json:"episodesExact"`
	Matched       int     `json:"matched"`
	Only          int     `json:"only"`
	NoCounterpart int     `json:"noCounterpart"`
	WouldOpen     int     `json:"wouldOpen"`
	WithProblem   int     `json:"withProblem"`
	MedianSec     int64   `json:"medianSec"`
	Short         int     `json:"short"`
	PerDay        float64 `json:"perDay"`
}

// EngineMLStats — ML kolu sayaçları.
type EngineMLStats struct {
	Events        int     `json:"events"`
	Matched       int     `json:"matched"`
	Only          int     `json:"only"`
	Indeterminate int     `json:"indeterminate"`
	WouldOpen     int     `json:"wouldOpen"`
	LiveOpened    int     `json:"liveOpened"`
	WithProblem   int     `json:"withProblem"`
	MedianSec     int64   `json:"medianSec"`
	Short         int     `json:"short"`
	PerDay        float64 `json:"perDay"`
}

// EngineExamples — "yalnız" örnek kimlikleri (en çok iki).
type EngineExamples struct {
	OnlySubject []string `json:"onlySubject"`
	OnlyML      []string `json:"onlyMl"`
}

// EngineCompareRow — tablo satırı.
type EngineCompareRow struct {
	Service  string             `json:"service"`
	Family   string             `json:"family"`
	Source   string             `json:"source"`
	Biased   bool               `json:"biased"`
	Subject  EngineSubjectStats `json:"subject"`
	ML       EngineMLStats      `json:"ml"`
	Examples EngineExamples     `json:"examples"`
}

// EngineCompareTotals — yanlı (örneklenmiş) satırlar HARİÇ toplamlar.
type EngineCompareTotals struct {
	Subject EngineSubjectStats `json:"subject"`
	ML      EngineMLStats      `json:"ml"`
}

// EngineCompareResult — saf karşılaştırmanın çıktısı.
type EngineCompareResult struct {
	Totals        EngineCompareTotals `json:"totals"`
	Rows          []EngineCompareRow  `json:"rows"`
	RowsTruncated bool                `json:"-"`
}

type engAcc struct {
	row         EngineCompareRow
	subjDur     []int64
	mlDur       []int64
	firstSubj   string // gösterim adı (örneklenmiş satırda özne servisi)
	subjInExact bool
}

func durSec(s, e int64) int64 {
	if e < s {
		return 0
	}
	return (e - s) / int64(time.Second)
}

// medianSec — SAF: saniye medyanı (çift sayıda ortadaki ikisinin ortalaması).
func medianSec(d []int64) int64 {
	if len(d) == 0 {
		return 0
	}
	c := append([]int64(nil), d...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

func perDay(n, days int) float64 {
	if days <= 0 {
		return 0
	}
	return float64(n) / float64(days)
}

func addExample(list []string, id string) []string {
	if len(list) < 2 {
		return append(list, id)
	}
	return list
}

func withProblem(svc string, s, e int64, refs map[string][]EngineRefProblem, now time.Time) bool {
	for _, p := range refs[svc] {
		if overlaps(s, e, p.Start, p.End, now, engineCompareTolerance) {
			return true
		}
	}
	return false
}

// indeterminate — SAF: eşleşmeyen ML olayının karşılık bölümü ezilmiş
// olabilir mi: aynı anahtarlı özne satırı birden çok bölüm taşıyor, ilk
// bölümü ML bitişinden (+tolerans) önce ya da o anda, son bölümü sonra başlamış.
func indeterminate(m EngineMLIn, subs []EngineSubjectIn) bool {
	end := m.End + int64(engineCompareTolerance)
	for _, s := range subs {
		first := s.FirstStart
		if first == 0 {
			first = s.Start
		}
		if s.EpisodeCount > 1 && first <= end && s.Start > end {
			return true
		}
	}
	return false
}

// CompareEngines — SAF: iki kol → satırlar + toplamlar. days: pencere (gün,
// olay/gün için). fromNs/toNs: pencere (episodesExact ve açık uçlar için).
func CompareEngines(subjects []EngineSubjectIn, mls []EngineMLIn, refs []EngineRefProblem, days int, fromNs, toNs int64) EngineCompareResult {
	now := time.Unix(0, toNs)
	refBySvc := map[string][]EngineRefProblem{}
	for _, p := range refs {
		refBySvc[p.Service] = append(refBySvc[p.Service], p)
	}
	acc := map[engKey]*engAcc{}
	get := func(k engKey, biased bool) *engAcc {
		a := acc[k]
		if a == nil {
			a = &engAcc{row: EngineCompareRow{Service: k.svc, Family: k.family, Source: k.source, Biased: biased,
				Examples: EngineExamples{OnlySubject: []string{}, OnlyML: []string{}}}, subjInExact: true}
			acc[k] = a
		}
		return a
	}
	// Anahtar başına özne satırları: eşlemede yalnız karşılığı olanlar,
	// belirsizlik kuralında aynı anahtarın tümü.
	subjByKey := map[engKey][]EngineSubjectIn{}
	cpByKey := map[engKey][]EngineSubjectIn{}
	type subjMeta struct {
		s           EngineSubjectIn
		k           engKey
		counterpart bool
	}
	var subjs []subjMeta
	for _, s := range subjects {
		k, biased, cp, ok := subjectSide(s)
		if !ok {
			continue
		}
		get(k, biased)
		subjByKey[k] = append(subjByKey[k], s)
		if cp {
			cpByKey[k] = append(cpByKey[k], s)
		}
		subjs = append(subjs, subjMeta{s, k, cp})
	}
	type mlMeta struct {
		m EngineMLIn
		k engKey
	}
	var ms []mlMeta
	mlByKey := map[engKey][]EngineMLIn{}
	for _, m := range mls {
		k, biased, ok := mlSide(m)
		if !ok {
			continue
		}
		get(k, biased)
		mlByKey[k] = append(mlByKey[k], m)
		ms = append(ms, mlMeta{m, k})
	}

	for _, sm := range subjs {
		s, a := sm.s, acc[sm.k]
		st := &a.row.Subject
		st.Series++
		ep := int(s.EpisodeCount)
		if ep < 1 {
			ep = 1
		}
		st.Episodes += ep
		first := s.FirstStart
		if first == 0 {
			first = s.Start
		}
		if first < fromNs {
			a.subjInExact = false
		}
		if a.firstSubj == "" {
			a.firstSubj = s.Service
		}
		switch {
		case !sm.counterpart:
			st.NoCounterpart++
		default:
			matched := false
			for _, m := range mlByKey[sm.k] {
				if overlaps(s.Start, s.End, m.Start, m.End, now, engineCompareTolerance) {
					matched = true
					break
				}
			}
			if matched {
				st.Matched++
			} else {
				st.Only++
				a.row.Examples.OnlySubject = addExample(a.row.Examples.OnlySubject, s.ID)
			}
		}
		if s.Promotion == chstore.AnomalyPromotionShadow || s.Promotion == chstore.AnomalyPromotionLive {
			st.WouldOpen++
		}
		if withProblem(s.Service, s.Start, s.End, refBySvc, now) {
			st.WithProblem++
		}
		d := durSec(s.Start, s.End)
		a.subjDur = append(a.subjDur, d)
		if d < EngineCompareShortSec {
			st.Short++
		}
	}
	for _, mm := range ms {
		m, a := mm.m, acc[mm.k]
		st := &a.row.ML
		st.Events++
		matched := false
		for _, s := range cpByKey[mm.k] {
			if overlaps(s.Start, s.End, m.Start, m.End, now, engineCompareTolerance) {
				matched = true
				break
			}
		}
		switch {
		case matched:
			st.Matched++
		case indeterminate(m, subjByKey[mm.k]):
			st.Indeterminate++
		default:
			st.Only++
			a.row.Examples.OnlyML = addExample(a.row.Examples.OnlyML, m.ID)
		}
		if m.WouldOpen {
			st.WouldOpen++
		}
		if m.LiveOpened {
			st.LiveOpened++
		}
		if withProblem(m.Service, m.Start, m.End, refBySvc, now) {
			st.WithProblem++
		}
		d := durSec(m.Start, m.End)
		a.mlDur = append(a.mlDur, d)
		if d < EngineCompareShortSec {
			st.Short++
		}
	}

	var res EngineCompareResult
	var totSubjDur, totMLDur []int64
	res.Totals.Subject.EpisodesExact = true
	rows := make([]EngineCompareRow, 0, len(acc))
	for _, a := range acc {
		r := a.row
		r.Subject.EpisodesExact = a.subjInExact
		r.Subject.MedianSec, r.ML.MedianSec = medianSec(a.subjDur), medianSec(a.mlDur)
		r.Subject.PerDay, r.ML.PerDay = perDay(r.Subject.Series, days), perDay(r.ML.Events, days)
		if r.Source == EngSrcSampledTrace && a.firstSubj != "" {
			r.Service = a.firstSubj
		} else if r.Source == EngSrcSampledTrace {
			r.Service = chstore.EngineCompareMessagingCodePrefix + r.Service
		}
		if !r.Biased {
			addSubjectTotals(&res.Totals.Subject, r.Subject)
			addMLTotals(&res.Totals.ML, r.ML)
			totSubjDur = append(totSubjDur, a.subjDur...)
			totMLDur = append(totMLDur, a.mlDur...)
		}
		rows = append(rows, r)
	}
	res.Totals.Subject.MedianSec, res.Totals.ML.MedianSec = medianSec(totSubjDur), medianSec(totMLDur)
	res.Totals.Subject.PerDay, res.Totals.ML.PerDay = perDay(res.Totals.Subject.Series, days), perDay(res.Totals.ML.Events, days)
	sort.Slice(rows, func(i, j int) bool {
		ti, tj := rows[i].Subject.Series+rows[i].ML.Events, rows[j].Subject.Series+rows[j].ML.Events
		if ti != tj {
			return ti > tj
		}
		if rows[i].Service != rows[j].Service {
			return rows[i].Service < rows[j].Service
		}
		if rows[i].Family != rows[j].Family {
			return rows[i].Family < rows[j].Family
		}
		return rows[i].Source < rows[j].Source
	})
	if len(rows) > EngineCompareRowCap {
		rows, res.RowsTruncated = rows[:EngineCompareRowCap], true
	}
	res.Rows = rows
	return res
}

func addSubjectTotals(t *EngineSubjectStats, r EngineSubjectStats) {
	t.Series += r.Series
	t.Episodes += r.Episodes
	t.EpisodesExact = t.EpisodesExact && (r.EpisodesExact || r.Series == 0)
	t.Matched += r.Matched
	t.Only += r.Only
	t.NoCounterpart += r.NoCounterpart
	t.WouldOpen += r.WouldOpen
	t.WithProblem += r.WithProblem
	t.Short += r.Short
}

func addMLTotals(t *EngineMLStats, r EngineMLStats) {
	t.Events += r.Events
	t.Matched += r.Matched
	t.Only += r.Only
	t.Indeterminate += r.Indeterminate
	t.WouldOpen += r.WouldOpen
	t.LiveOpened += r.LiveOpened
	t.WithProblem += r.WithProblem
	t.Short += r.Short
}
