package anomaly

// subjects_scan.go — v0.10.1791 (Davis F1.3): bir tikin ÖZNE serilerini
// toplar (endpoint: VM, db/queue: CH MV). Özneler kapalıyken (varsayılan)
// HİÇBİR okuma yapılmaz ve tik bayt bayt eskisidir — scanSubjects ilk
// satırda döner (TestScanSubjectsOffIsNoop).
//
// Bu sürüm serileri KURAR ve ölçer; karar + olay yazımı F1.4'te (v0.10.1792).

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// SubjectKindStats — bir özne türünün son tik + süreç toplamı.
type SubjectKindStats struct {
	Kind string `json:"kind"`
	// Source — v0.10.1802: "" = türün asıl kaynağı; "vm_kafka_lag" = queue'nun
	// VM consumer lag kaynağı (ayrı satır, ayrı sayaç).
	Source   string `json:"source,omitempty"`
	Enabled  bool   `json:"enabled"`
	Series   int    `json:"series"`   // son tik: seçilen özne sayısı (-1 = bilinmiyor)
	Subjects int    `json:"subjects"` // son tik: serisi kurulan özne sayısı
	Queries  int    `json:"queries"`  // son tik: bu türün sorgu sayısı
	Skipped  bool   `json:"skipped"`
	Reason   string `json:"reason,omitempty"`
	// SkipCount — süreç başından beri atlanan tik sayısı, gerekçeye göre
	// (self-obs sayaç: "seri tavanı" / "VM bütçesi" / "kaynak yok").
	SkipCount map[string]int64 `json:"skipCount,omitempty"`
	LastError string           `json:"lastError,omitempty"`
}

// SubjectStats — /api/settings/anomaly-red-source/status `subjects` bloğu.
type SubjectStats struct {
	LastUnix         int64              `json:"lastUnix"`
	SeasonalTimezone string             `json:"seasonalTimezone"`
	Kinds            []SubjectKindStats `json:"kinds"`
}

var (
	subjectObs      atomic.Pointer[SubjectStats]
	subjectSkipMu   sync.Mutex
	subjectSkipTots = map[string]map[string]int64{}
)

// SubjectObservability — son tikin ölçümü (lider olmayan pod'da boş = doğru cevap).
func SubjectObservability() SubjectStats {
	if p := subjectObs.Load(); p != nil {
		return *p
	}
	return SubjectStats{Kinds: []SubjectKindStats{}}
}

// noteSubjectSkip — atlanma sayacı (süreç-içi, behaviorObs emsali).
func noteSubjectSkip(kind, reason string) map[string]int64 {
	subjectSkipMu.Lock()
	defer subjectSkipMu.Unlock()
	m := subjectSkipTots[kind]
	if m == nil {
		m = map[string]int64{}
		subjectSkipTots[kind] = m
	}
	if reason != "" {
		m[reason]++
	}
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func logSubjectErr(kind, metric, what string, err error) {
	log.Printf("[anomaly] özne %s %s %s okuması: %v — bu tik o metrik atlanır", kind, metric, what, err)
}

// scanSubjects — tikin özne serileri. vmUsed = servis yolunun bu tik harcadığı
// VM sorgusu (redView.queries); endpoint türü yalnız kalanı kullanır.
func (d *Detector) scanSubjects(ctx context.Context, now time.Time, sens chstore.AnomalySensitivityConfig,
	tracked []string, days, neighbor, vmUsed int) subjectTick {
	if !sens.Subjects.AnyEnabled() {
		return nil
	}
	loc := seasonalLocation(sens.SeasonalTimezone)
	out := subjectTick{}
	reqs := subjectKindsOrdered(sens.Subjects)
	stats := make([]SubjectKindStats, 0, len(reqs))
	vmLeft := vmMaxQueriesPerTick - vmUsed
	// v0.10.1802 — tik başı plan: endpoint payı lag'in en az payını ayırır.
	plans := planSubjectBudget(reqs, vmLeft)
	for i, r := range reqs {
		st := SubjectKindStats{Kind: r.Kind, Source: r.Source, Enabled: r.Cfg.Enabled, Series: -1}
		if !r.Cfg.Enabled {
			stats = append(stats, st)
			continue
		}
		before := countSubjects(out, r.Kind)
		switch {
		case r.Source == subjectSourceKafkaLag:
			d.collectKafkaLag(ctx, now, sens, days, neighbor, r, &vmLeft, out, &st)
		case r.Kind == chstore.AnomalySubjectEndpoint:
			d.collectEndpoints(ctx, now, sens, tracked, days, neighbor, plans[i], &vmLeft, out, &st)
		default:
			d.collectCHKind(ctx, now, sens, tracked, days, neighbor, loc, r, out, &st)
		}
		st.Subjects = countSubjects(out, r.Kind) - before
		st.SkipCount = noteSubjectSkip(r.Kind+r.Source, st.Reason)
		stats = append(stats, st)
	}
	subjectObs.Store(&SubjectStats{LastUnix: time.Now().Unix(), SeasonalTimezone: seasonalTZLiteral(loc), Kinds: stats})
	return out
}

// collectEndpoints — VM endpoint kaynağı; bütçe planlayıcısının kararıyla.
func (d *Detector) collectEndpoints(ctx context.Context, now time.Time, sens chstore.AnomalySensitivityConfig,
	tracked []string, days, neighbor int, plan subjectKindPlan, vmLeft *int, out subjectTick, st *SubjectKindStats) {
	if d.redEndpoints == nil {
		st.Skipped, st.Reason = true, subjectSkipNoSource
		return
	}
	// v0.10.1802 — plan tik başında (planSubjectBudget, lag payı ayrılmış).
	cfg := sens.Subjects.Endpoint
	if plan.Skipped {
		st.Skipped, st.Reason = true, plan.Reason
		return
	}
	src := d.redEndpoints
	src.topN, src.maxSeries = cfg.TopN, cfg.MaxSeriesPerTick
	src.queryCap = plan.VMBudget - 1 // seçim sorgusuna bir pay
	v := src.prepare(ctx, now, sens, days, neighbor)
	*vmLeft -= v.queries
	st.Queries, st.Series = v.queries, v.selected
	switch {
	case v.reason == subjectSkipSeriesCap:
		st.Skipped, st.Reason = true, subjectSkipSeriesCap
		return
	case v.reason != "":
		st.Skipped, st.Reason = true, subjectSkipNoSource
		st.LastError = v.reason
		return
	}
	if v.lastErr != "" {
		st.LastError = v.lastErr
	}
	keys := v.vmServices()
	for _, k := range keys {
		svc, route, ok := splitEndpointStoreKey(k)
		if !ok {
			continue
		}
		sk := subjectKey{Kind: chstore.AnomalySubjectEndpoint, Owner: svc, ID: route}.String()
		for _, m := range tracked {
			s, used := v.seriesFor(k, m, sens.DwellBuckets)
			if !used.VM {
				continue
			}
			out.put(m, sk, func(x *subjectSeries) {
				x.Values, x.Rates, x.Seasonal, x.SeasonalRates = s.Values, s.Rates, s.Seasonal, s.SeasonalRates
			})
		}
	}
}

// collectCHKind — db / queue: seçim + seriler.
func (d *Detector) collectCHKind(ctx context.Context, now time.Time, sens chstore.AnomalySensitivityConfig,
	tracked []string, days, neighbor int, loc *time.Location, r subjectKindReq, out subjectTick, st *SubjectKindStats) {
	var src subjectCHSource
	for _, s := range subjectCHSources {
		if s.Kind == r.Kind {
			src = s
		}
	}
	if src.Table == "" {
		st.Skipped, st.Reason = true, subjectSkipNoSource
		return
	}
	pairs, err := d.selectCHSubjects(ctx, src, now, r.Cfg.TopN, r.Cfg.MaxSeriesPerTick)
	st.Queries = 1
	if err != nil {
		st.Skipped, st.Reason, st.LastError = true, subjectSkipNoSource, err.Error()
		return
	}
	r.Series = len(pairs)
	st.Series = len(pairs)
	if p := planSubjectBudget([]subjectKindReq{r}, 0)[0]; p.Skipped {
		st.Skipped, st.Reason = true, p.Reason
		return
	}
	d.fetchCHSubjects(ctx, src, pairs, tracked, now, sens.DwellBuckets, days, neighbor, loc, out)
	st.Queries += 2 * len(tracked)
}

// countSubjects — SAF: tikte bir türün kaç farklı öznesi var.
func countSubjects(t subjectTick, kind string) int {
	seen := map[string]bool{}
	for _, byKey := range t {
		for k := range byKey {
			if sk, ok := parseSubjectKey(k); ok && sk.Kind == kind {
				seen[k] = true
			}
		}
	}
	return len(seen)
}
