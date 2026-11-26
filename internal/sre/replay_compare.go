package sre

// replay_compare.go — CoSRE ölçüm: iki replay raporunun (taban ↔ aday)
// karşılaştırması. eval_compare.go emsali ama ön koşulsuz: replay raporu
// dondurulmuş küme değil, etiket penceresinden okunur — Problem'ler
// problemId ile eşlenir, yalnız bir tarafta olanlar ayrı sayılır (hükme
// girmez). SAF: iki rapor gövdesi → fark; depo / LLM / oy okumaz.
// Saklanmaz — rapor satırlarından her okumada yeniden kurulur.
//
// Problem hükmü (notlu koşuların ortalaması, ItemStats):
//   improved / regressed — |aday − taban| ≥ 1 puan
//   same                 — fark < 1
//   ungraded             — iki tarafta da var, en az birinde notlu koşu yok
//   only_base / only_cand — yalnız bir raporda (atlanan Problem hiç girmez)

import (
	"math"
	"sort"
)

// Problem hükümleri.
const (
	VerdictImproved  = "improved"
	VerdictRegressed = "regressed"
	VerdictSame      = "same"
	VerdictUngraded  = "ungraded"
	VerdictOnlyBase  = "only_base"
	VerdictOnlyCand  = "only_cand"
)

// replaySameEps — "aynı" sayılan en büyük ortalama farkı (puan).
const replaySameEps = 1.0

// ReplayCompareSide — bir tarafın künyesi + özeti (Problem satırları yok).
type ReplayCompareSide struct {
	ReportID   string        `json:"reportId"`
	State      string        `json:"state"`
	Build      string        `json:"build,omitempty"`
	GradeBuild string        `json:"gradeBuild,omitempty"`
	CreatedAt  int64         `json:"createdAt"`
	Days       int           `json:"days"`
	Repeats    int           `json:"repeats"`
	Variant    ReplayVariant `json:"variant"`
	Judge      string        `json:"judge,omitempty"`
	Summary    ReplaySummary `json:"summary"`
}

// ReplayCompareItem — Problem başına fark.
type ReplayCompareItem struct {
	ProblemID  string   `json:"problemId"`
	Service    string   `json:"service"`
	Cause      string   `json:"cause,omitempty"`
	BaseMean   *float64 `json:"baseMean"`
	CandMean   *float64 `json:"candMean"`
	Delta      *float64 `json:"delta"`
	BaseGrades []string `json:"baseGrades"`
	CandGrades []string `json:"candGrades"`
	Verdict    string   `json:"verdict"`
}

// ReplayCompare — karşılaştırmanın gövdesi (FE: SREReplayCompare).
type ReplayCompare struct {
	Base ReplayCompareSide `json:"base"`
	Cand ReplayCompareSide `json:"cand"`
	// Ölçü farkları (aday − taban; taraflardan biri null ise null).
	ScoreDelta        *float64            `json:"scoreDelta"`
	ReachDelta        *float64            `json:"reachDelta"`
	DistDelta         map[string]int      `json:"distDelta"`
	MedToolCallsDelta *float64            `json:"medToolCallsDelta"`
	MedRoundsDelta    *float64            `json:"medRoundsDelta"`
	MedWastedDelta    *float64            `json:"medWastedDelta"`
	CapHitsDelta      int                 `json:"capHitsDelta"`
	Improved          int                 `json:"improved"`
	Regressed         int                 `json:"regressed"`
	Same              int                 `json:"same"`
	Ungraded          int                 `json:"ungraded"`
	OnlyBase          int                 `json:"onlyBase"`
	OnlyCand          int                 `json:"onlyCand"`
	Items             []ReplayCompareItem `json:"items"`
}

func compareSide(rep ReplayReport) ReplayCompareSide {
	return ReplayCompareSide{ReportID: rep.ReportID, State: rep.State, Build: rep.Build, GradeBuild: rep.GradeBuild,
		CreatedAt: rep.CreatedAt, Days: rep.Days, Repeats: rep.Repeats, Variant: rep.Variant, Judge: rep.Judge,
		Summary: SummarizeReplay(rep)}
}

// deltaOf — SAF: b − a (biri nil → nil), 4 basamak (oranlar 0..1).
func deltaOf(a, b *float64) *float64 {
	if a == nil || b == nil {
		return nil
	}
	v := math.Round((*b-*a)*10000) / 10000
	return &v
}

func runGrades(it ReplayItem) []string {
	out := []string{}
	for _, r := range it.Runs {
		if _, ok := GradeScore(r.Grade); ok {
			out = append(out, r.Grade)
		}
	}
	return out
}

// verdictRank — liste sırası: önce gerileyen, sonra iyileşen, …
var verdictRank = map[string]int{VerdictRegressed: 0, VerdictImproved: 1, VerdictSame: 2, VerdictUngraded: 3, VerdictOnlyBase: 4, VerdictOnlyCand: 5}

// CompareReplayReports — SAF: taban ↔ aday.
func CompareReplayReports(base, cand ReplayReport) ReplayCompare {
	c := ReplayCompare{Base: compareSide(base), Cand: compareSide(cand), DistDelta: map[string]int{}, Items: []ReplayCompareItem{}}
	bs, cs := c.Base.Summary, c.Cand.Summary
	c.ScoreDelta, c.ReachDelta = deltaOf(bs.Score, cs.Score), deltaOf(bs.ReachRate, cs.ReachRate)
	for _, g := range ReplayGrades {
		c.DistDelta[g] = cs.Dist[g] - bs.Dist[g]
	}
	c.MedToolCallsDelta = deltaOf(bs.MedToolCalls, cs.MedToolCalls)
	c.MedRoundsDelta = deltaOf(bs.MedRounds, cs.MedRounds)
	c.MedWastedDelta = deltaOf(bs.MedWasted, cs.MedWasted)
	c.CapHitsDelta = cs.CapHits - bs.CapHits

	active := func(rep ReplayReport) (map[string]ReplayItem, []string) {
		m, order := map[string]ReplayItem{}, []string{}
		for _, it := range rep.Items {
			if it.State == CaseSkipped || it.ProblemID == "" {
				continue
			}
			if _, dup := m[it.ProblemID]; !dup {
				order = append(order, it.ProblemID)
			}
			m[it.ProblemID] = it
		}
		return m, order
	}
	bm, border := active(base)
	cm, corder := active(cand)
	add := func(it ReplayCompareItem) {
		switch it.Verdict {
		case VerdictImproved:
			c.Improved++
		case VerdictRegressed:
			c.Regressed++
		case VerdictSame:
			c.Same++
		case VerdictUngraded:
			c.Ungraded++
		case VerdictOnlyBase:
			c.OnlyBase++
		case VerdictOnlyCand:
			c.OnlyCand++
		}
		c.Items = append(c.Items, it)
	}
	for _, id := range border {
		b := bm[id]
		bMean, _, _, _ := ItemStats(b)
		it := ReplayCompareItem{ProblemID: id, Service: b.Service, Cause: b.Cause, BaseMean: bMean, BaseGrades: runGrades(b), CandGrades: []string{}}
		cItem, ok := cm[id]
		if !ok {
			it.Verdict = VerdictOnlyBase
			add(it)
			continue
		}
		cMean, _, _, _ := ItemStats(cItem)
		it.CandMean, it.CandGrades = cMean, runGrades(cItem)
		if bMean == nil || cMean == nil {
			it.Verdict = VerdictUngraded
			add(it)
			continue
		}
		d := math.Round((*cMean-*bMean)*10) / 10
		it.Delta = &d
		switch {
		case d >= replaySameEps:
			it.Verdict = VerdictImproved
		case d <= -replaySameEps:
			it.Verdict = VerdictRegressed
		default:
			it.Verdict = VerdictSame
		}
		add(it)
	}
	for _, id := range corder {
		if _, ok := bm[id]; ok {
			continue
		}
		ci := cm[id]
		cMean, _, _, _ := ItemStats(ci)
		add(ReplayCompareItem{ProblemID: id, Service: ci.Service, Cause: ci.Cause, CandMean: cMean, BaseGrades: []string{},
			CandGrades: runGrades(ci), Verdict: VerdictOnlyCand})
	}
	sort.SliceStable(c.Items, func(i, j int) bool {
		a, b := c.Items[i], c.Items[j]
		if verdictRank[a.Verdict] != verdictRank[b.Verdict] {
			return verdictRank[a.Verdict] < verdictRank[b.Verdict]
		}
		if a.Delta != nil && b.Delta != nil && *a.Delta != *b.Delta {
			if a.Verdict == VerdictRegressed {
				return *a.Delta < *b.Delta
			}
			return *a.Delta > *b.Delta
		}
		return a.ProblemID < b.ProblemID
	})
	return c
}
