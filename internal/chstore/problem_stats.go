package chstore

// problem_stats.go — v0.10.774 (Dynatrace paritesi #7: MTTR / yaşam döngüsü
// serisi; spec 2026-09-16, operatör "go" 2026-09-17). Problems başlığı için
// pencerede açılan / çözülen seri, MTTR (çözülenlerden), açık olanların
// öncelik dağılımı (okuma anında computePriority — SQL'e İNMEZ,
// problem_filter_contract_test) ve açılanların kategori dağılımı.
//
// Okuma: ListProblems (FINAL; env daraltması dahil) + OverlapFrom/To, tavan
// problemStatsRowCap (started_at DESC → kırpma pencerenin ESKİ ucunu düşürür,
// Truncated bunu söyler). Hesap SAF: ProblemStatsFromRows.

import (
	"context"
	"math"
	"sort"
	"time"
)

const problemStatsRowCap = 5000

type ProblemStatsBucket struct {
	TimeS    int64 `json:"t"`
	Opened   int   `json:"opened"`
	Resolved int   `json:"resolved"`
}

// ProblemMTTR — pencerede ÇÖZÜLEN problemlerin açık kalma süreleri (sn).
type ProblemMTTR struct {
	N       int     `json:"n"`
	MedianS float64 `json:"medianS"`
	MeanS   float64 `json:"meanS"`
	P90S    float64 `json:"p90S"`
}

type ProblemStats struct {
	FromS      int64                `json:"fromS"`
	ToS        int64                `json:"toS"`
	StepS      int                  `json:"stepS"`
	Buckets    []ProblemStatsBucket `json:"buckets"`
	Opened     int                  `json:"opened"`   // pencerede açılan
	Resolved   int                  `json:"resolved"` // pencerede çözülen
	OpenNow    int                  `json:"openNow"`  // şu an açık (pencereyle kesişenlerden)
	Carried    int                  `json:"carried"`  // pencere başında zaten açıktı
	Truncated  bool                 `json:"truncated"`
	MTTR       ProblemMTTR          `json:"mttr"`
	ByPriority map[string]int       `json:"byPriority"` // açık olanlar, P1/P2/P3
	ByCategory map[string]int       `json:"byCategory"` // pencerede açılanlar
}

// problemStatsStep — SAF: pencere uzunluğuna göre kova (≤6 sa 5 dk, ≤24 sa
// 15 dk, ≤7 g 1 sa, üstü 6 sa) — en çok ~170 kova.
func problemStatsStep(window time.Duration) time.Duration {
	switch {
	case window <= 6*time.Hour:
		return 5 * time.Minute
	case window <= 24*time.Hour:
		return 15 * time.Minute
	case window <= 7*24*time.Hour:
		return time.Hour
	default:
		return 6 * time.Hour
	}
}

// ProblemStatsFromRows — SAF. rows: pencereyle kesişen problemler.
func ProblemStatsFromRows(rows []Problem, from, to time.Time, step time.Duration, nowNs int64, cfg ProblemPriorityConfig, truncated bool) ProblemStats {
	if step <= 0 {
		step = 5 * time.Minute
	}
	fromNs, toNs := from.UnixNano(), to.UnixNano()
	n := int(math.Ceil(float64(toNs-fromNs) / float64(step.Nanoseconds())))
	if n < 0 {
		n = 0
	}
	out := ProblemStats{
		FromS: from.Unix(), ToS: to.Unix(), StepS: int(step.Seconds()),
		Buckets:    make([]ProblemStatsBucket, n),
		Truncated:  truncated,
		ByPriority: map[string]int{"P1": 0, "P2": 0, "P3": 0},
		ByCategory: map[string]int{},
	}
	for i := range out.Buckets {
		out.Buckets[i].TimeS = from.Add(time.Duration(i) * step).Unix()
	}
	idx := func(ns int64) int {
		if ns < fromNs || ns >= toNs {
			return -1
		}
		i := int((ns - fromNs) / step.Nanoseconds())
		if i >= n {
			return -1
		}
		return i
	}
	var durs []float64
	for _, p := range rows {
		if i := idx(p.StartedAt); i >= 0 {
			out.Opened++
			out.Buckets[i].Opened++
			out.ByCategory[ProblemCategory(p)]++
		}
		if p.ResolvedAt != nil {
			if i := idx(*p.ResolvedAt); i >= 0 {
				out.Resolved++
				out.Buckets[i].Resolved++
				if d := *p.ResolvedAt - p.StartedAt; d >= 0 {
					durs = append(durs, float64(d)/1e9)
				}
			}
		} else {
			out.OpenNow++
			pr, _ := computePriority(p, nowNs, cfg)
			out.ByPriority[pr]++
		}
		if p.StartedAt < fromNs && (p.ResolvedAt == nil || *p.ResolvedAt >= fromNs) {
			out.Carried++
		}
	}
	out.MTTR = mttrOf(durs)
	return out
}

// mttrOf — SAF: ortanca (çift sayıda ortalama), ortalama, p90 (en yakın sıra).
func mttrOf(d []float64) ProblemMTTR {
	m := ProblemMTTR{N: len(d)}
	if len(d) == 0 {
		return m
	}
	sort.Float64s(d)
	sum := 0.0
	for _, v := range d {
		sum += v
	}
	m.MeanS = sum / float64(len(d))
	if len(d)%2 == 1 {
		m.MedianS = d[len(d)/2]
	} else {
		m.MedianS = (d[len(d)/2-1] + d[len(d)/2]) / 2
	}
	k := int(math.Ceil(0.9*float64(len(d)))) - 1
	if k < 0 {
		k = 0
	}
	m.P90S = d[k]
	return m
}

// ProblemStats — [from, to) penceresi; f'nin Env/Service daraltmaları aynen
// (liste ile aynı satırlar), Status/Limit burada belirlenir.
func (s *Store) ProblemStats(ctx context.Context, f ProblemFilter, from, to time.Time) (ProblemStats, error) {
	f.Status, f.NotStatuses = "", nil
	f.OverlapFrom, f.OverlapTo = from, to
	f.Limit = problemStatsRowCap
	rows, err := s.ListProblems(ctx, f)
	if err != nil {
		return ProblemStats{}, err
	}
	return ProblemStatsFromRows(rows, from, to, problemStatsStep(to.Sub(from)), time.Now().UnixNano(), CurrentProblemPriority(), len(rows) >= problemStatsRowCap), nil
}
