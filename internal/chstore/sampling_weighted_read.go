package chstore

// sampling_weighted_read.go — v0.10.2435: the ONE coverage-aware reader
// helper of sampling-weight Phase 2 (docs/architecture/sampling-weight.md).
//
// A reader builds its MV query exactly as before and passes it through
// s.weighSQL(sql, from, mvs…). The helper rewrites the canonical merge
// literals to their weighted twins:
//
//	countMerge(span_count_state)  → represented spans (Σ sample_weight)
//	countIfMerge(error_count_state), apdex, calls/error (spanmetrics)
//	sumMerge(duration_sum_state)  → Σ duration × weight (avg = Σwd / Σw)
//	quantilesTDigestMerge(L)(duration_q_state) → weighted TDigest
//
// Coverage: weighted states exist only for buckets ≥ S, the first full
// bucket after the MV's coverage marker (sampling_weighted_mv.go). Count /
// sum literals become `oldMergeIf(…, time_bucket < S) + wMergeIf(…,
// time_bucket >= S)` (each bucket reads exactly one side); quantiles cannot
// be mixed, so a group that reaches before S keeps the unweighted digest
// (`if(min(time_bucket) >= S, weighted, unweighted)`). A window starting at
// or after S reads the weighted states directly.
//
// OFF = UNCHANGED: with ingest.sampling.mode ≠ on, or no marker, the SQL is
// returned byte-for-byte (TestWeighSQLModeOffIdentical) — every stored
// weight is 1 then anyway. Count/sum literals keep their result types
// (UInt64 / Int64) so Go scan targets do not change.

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// weightCov — the read decision for one query.
type weightCov struct {
	since time.Time // first weighted bucket; zero = unweighted
	full  bool      // the whole window is ≥ since
}

func (c weightCov) on() bool { return !c.since.IsZero() }

// weightedCoverage — SAF core: mode, markers, MVs, window start → decision.
// Every MV must carry a marker (a query joining two MVs uses the later one).
// The marker gets one minute of slack (spans timestamped slightly ahead of
// the MV switch) and is rounded UP to the coarsest bucket among mvs.
func weightedCoverage(mode string, markers map[string]time.Time, from time.Time, mvs ...string) weightCov {
	if mode != SamplingModeOn || len(mvs) == 0 {
		return weightCov{}
	}
	var latest time.Time
	var bucket time.Duration
	for _, mv := range mvs {
		m, ok := markers[mv]
		if !ok || m.IsZero() {
			return weightCov{}
		}
		if m.After(latest) {
			latest = m
		}
		if b := weightedMVBucket(mv); b > bucket {
			bucket = b
		}
	}
	since := ceilTime(latest.Add(time.Minute), bucket)
	return weightCov{since: since, full: !from.IsZero() && !from.Before(since)}
}

func ceilTime(t time.Time, d time.Duration) time.Time {
	f := t.Truncate(d)
	if f.Equal(t) {
		return f
	}
	return f.Add(d)
}

// WeightedSince — first sampling-corrected bucket for the given MVs (zero =
// not corrected). Used by baselines (reset at the switch) and the UI marker.
func WeightedSince(mvs ...string) time.Time {
	return weightedCoverage(CurrentIngestSampling().Mode, WeightedSinceMarkers(), time.Time{}, mvs...).since
}

// EffectiveWeightedSince — the operator-facing switch point: the later of
// the MV coverage and the moment mode went on (before OnSince every stored
// weight is 1, so values there are unweighted in substance).
func EffectiveWeightedSince(mvs ...string) time.Time {
	since := WeightedSince(mvs...)
	if since.IsZero() {
		return since
	}
	if on := CurrentIngestSampling().OnSince; on.After(since) {
		return ceilTime(on.Add(time.Minute), 5*time.Minute)
	}
	return since
}

// BaselineFloor — the error-rate / volume baseline reset at the switch
// (design §6): a baseline or history window that would reach before the
// effective switch point starts AT it. Pre-switch history is inflated
// (failing traces all kept) and ~1/w low in volume; mixing it in would make
// every service look "recovered" and a 20× traffic jump. With nothing to
// reset (mode off / no marker / window already after) t is returned as is.
func BaselineFloor(t time.Time, mvs ...string) time.Time {
	if since := EffectiveWeightedSince(mvs...); since.After(t) {
		return since
	}
	return t
}

// weighSQLPeriods — comparison reads (now vs yesterday / last week): both
// periods must sit on the same side of the effective switch; a pair that
// straddles it reads stored (unweighted) values on both sides, otherwise
// the comparison would show the correction itself as a change.
func (s *Store) weighSQLPeriods(sql string, a, b time.Time, mvs ...string) string {
	if since := EffectiveWeightedSince(mvs...); !since.IsZero() && a.Before(since) != b.Before(since) {
		return sql
	}
	if b.Before(a) {
		a = b
	}
	return s.weighSQL(sql, a, mvs...)
}

// weighSQL — the reader entry point (see the file header).
func (s *Store) weighSQL(sql string, from time.Time, mvs ...string) string {
	return weighSQLWith(weightedCoverage(CurrentIngestSampling().Mode, WeightedSinceMarkers(), from, mvs...), sql)
}

// WeighSQL — weighSQL for readers outside chstore (evaluator, anomaly, ml).
func (s *Store) WeighSQL(sql string, from time.Time, mvs ...string) string {
	return s.weighSQL(sql, from, mvs...)
}

// weightState — an unweighted state column and its weighted twin. wFn is the
// weighted merge (sum for countState twins, sumIf for countIfState twins).
type weightState struct {
	wState, wFn, cast string
}

var weightStates = map[string]weightState{
	"span_count_state":       {"w_span_count_state", "sumMerge", "toUInt64"},
	"error_count_state":      {"w_error_count_state", "sumIfMerge", "toUInt64"},
	"apdex_satisfied_state":  {"w_apdex_satisfied_state", "sumIfMerge", "toUInt64"},
	"apdex_tolerating_state": {"w_apdex_tolerating_state", "sumIfMerge", "toUInt64"},
	"calls_state":            {"w_calls_state", "sumMerge", "toUInt64"},
	"error_state":            {"w_error_state", "sumIfMerge", "toUInt64"},
	"duration_sum_state":     {"w_duration_sum_state", "sumMerge", "toInt64"},
}

// Merge literals the rewrite recognises: count/sum merges (optionally with
// the -If combinator) over a known state, and quantilesTDigestMerge[If].
// -MergeState forms (an outer query merges the produced state) are NOT
// rewritten: the outer merge would need the weighted function too, so
// those quantiles stay unweighted (stored latency).
var (
	reCountMerge = regexp.MustCompile(`\b(countMerge|countIfMerge|sumMerge)(If)?\(\s*(span_count_state|error_count_state|apdex_satisfied_state|apdex_tolerating_state|calls_state|error_state|duration_sum_state)\s*([,)])`)
	reQuantMerge = regexp.MustCompile(`\bquantilesTDigestMerge(If)?\(([0-9.,\s]+)\)\(\s*duration_q_state\s*([,)])`)
)

// closeParen — index of the ')' closing the call whose argument list
// continues at i (depth 1), or -1.
func closeParen(s string, i int) int {
	depth := 1
	inStr := false
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			if c == '\\' {
				i++
			} else if c == '\'' {
				inStr = false
			}
		case c == '\'':
			inStr = true
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// rewriteCalls — replaces every regex match (plus its optional `, cond`
// tail up to the closing paren) with build(match groups, cond).
// A call whose condition carries a `?` bind placeholder is left as is in
// a MIXED window: the mixed form repeats the condition and would shift the
// caller's bind order. Such a call reads unweighted states there.
func rewriteCalls(sql string, re *regexp.Regexp, mixed bool, build func(g []string, cond string) string) string {
	var out strings.Builder
	pos := 0
	for {
		loc := re.FindStringSubmatchIndex(sql[pos:])
		if loc == nil {
			break
		}
		g := make([]string, len(loc)/2)
		for k := range g {
			if loc[2*k] >= 0 {
				g[k] = sql[pos+loc[2*k] : pos+loc[2*k+1]]
			}
		}
		end := pos + loc[1] // just past ',' or ')'
		cond := ""
		if g[len(g)-1] == "," {
			cl := closeParen(sql, end)
			if cl < 0 {
				break // malformed — leave the rest untouched
			}
			cond = strings.TrimSpace(sql[end:cl])
			end = cl + 1
		}
		out.WriteString(sql[pos : pos+loc[0]])
		if mixed && strings.Contains(cond, "?") {
			out.WriteString(sql[pos+loc[0] : end])
		} else {
			out.WriteString(build(g, cond))
		}
		pos = end
	}
	out.WriteString(sql[pos:])
	return out.String()
}

func andCond(cond, extra string) string {
	if cond == "" {
		return extra
	}
	return "(" + cond + ") AND " + extra
}

// weighSQLWith — SAF rewrite.
func weighSQLWith(c weightCov, sql string) string {
	if !c.on() {
		return sql
	}
	ts := fmt.Sprintf("toDateTime(%d)", c.since.Unix())
	sql = rewriteCalls(sql, reCountMerge, !c.full, func(g []string, cond string) string {
		oldFn, state := g[1], g[3]
		w := weightStates[state]
		if c.full {
			if cond == "" {
				return fmt.Sprintf("%s(round(%s(%s)))", w.cast, w.wFn, w.wState)
			}
			return fmt.Sprintf("%s(round(%sIf(%s, %s)))", w.cast, w.wFn, w.wState, cond)
		}
		return fmt.Sprintf("%s(round(%sIf(%s, %s) + %sIf(%s, %s)))", w.cast,
			oldFn, state, andCond(cond, "time_bucket < "+ts),
			w.wFn, w.wState, andCond(cond, "time_bucket >= "+ts))
	})
	return rewriteCalls(sql, reQuantMerge, !c.full, func(g []string, cond string) string {
		levels := g[2]
		old := "quantilesTDigestMerge(" + levels + ")(duration_q_state)"
		wq := "quantilesTDigestWeightedMerge(" + levels + ")(w_duration_q_state)"
		first := "min(time_bucket)"
		if cond != "" {
			old = "quantilesTDigestMergeIf(" + levels + ")(duration_q_state, " + cond + ")"
			wq = "quantilesTDigestWeightedMergeIf(" + levels + ")(w_duration_q_state, " + cond + ")"
			first = "minIf(time_bucket, " + cond + ")"
		}
		if c.full {
			return wq
		}
		return fmt.Sprintf("if(%s >= %s, %s, %s)", first, ts, wq, old)
	})
}

// ── raw spans ───────────────────────────────────────────────────────

const (
	rawWeight     = "toFloat64(sample_weight)"
	rawWeightUInt = "toUInt64(round(sample_weight))"
)

// rawSpansWeighted — raw-span aggregates read represented spans: mode on
// and the column stored. Every raw row has a weight (DEFAULT 1 before), so
// unlike the MVs there is no coverage boundary.
func (s *Store) rawSpansWeighted() bool {
	return CurrentIngestSampling().Mode == SamplingModeOn && s.hasSampleWeightCol.Load()
}

// weightedRawAgg — SAF: the weighted twin of aggToSQL's count / rate /
// error / apdex / sum / avg / percentile expressions (min/max unchanged →
// ok=false, the caller keeps the unweighted text).
func weightedRawAgg(agg, field string, stepSec int) (string, bool) {
	w := rawWeight
	switch strings.ToLower(agg) {
	case "", "count":
		return "sum(" + w + ")", true
	case "rate":
		return fmt.Sprintf("sum(%s) / %d.0", w, stepSec), true
	case "per_min":
		return fmt.Sprintf("sum(%s) / %d.0 * 60.0", w, stepSec), true
	case "error_rate":
		return fmt.Sprintf("100.0 * sumIf(%[1]s, status_code = 'error') / sum(%[1]s)", w), true
	case "errors":
		return "sumIf(" + w + ", status_code = 'error')", true
	case "apdex":
		return fmt.Sprintf("(sumIf(%[2]s, %[1]s <= 200.0) + sumIf(%[2]s, %[1]s > 200.0 AND %[1]s <= 800.0) / 2.0) / sum(%[2]s)", field, w), true
	case "sum":
		return fmt.Sprintf("sumOrNull(%s * %s)", field, w), true
	case "avg":
		return fmt.Sprintf("if(sum(%[2]s) = 0, NULL, sum(%[1]s * %[2]s) / sum(%[2]s))", field, w), true
	case "p50", "p90", "p95", "p99", "p999":
		lv := map[string]string{"p50": "0.50", "p90": "0.90", "p95": "0.95", "p99": "0.99", "p999": "0.999"}[strings.ToLower(agg)]
		return fmt.Sprintf("quantileTDigestWeighted(%s)(%s, %s)", lv, field, rawWeightUInt), true
	}
	return "", false
}

// SamplingCorrection — what an API response / the UI marker needs.
type SamplingCorrection struct {
	Mode string `json:"mode"`
	// Since — first sampling-corrected bucket (zero = numbers are stored
	// counts). Per surface family; the UI compares it with its window.
	Since map[string]time.Time `json:"since"`
}

// samplingSurfaceMVs — UI surface family → the MVs its numbers come from.
var samplingSurfaceMVs = map[string][]string{
	"services":   {"service_summary_5m"},
	"operations": {"operation_summary_5m", "operation_group_summary_5m"},
	"endpoints":  {"spanmetrics_1m", "operation_summary_5m"},
	"databases":  {"db_summary_5m", "db_caller_summary_5m", "db_statement_summary_5m"},
	"messaging":  {"messaging_summary_5m", "messaging_caller_summary_5m"},
}

// CurrentSamplingCorrection — /api/sampling/correction.
func CurrentSamplingCorrection() SamplingCorrection {
	out := SamplingCorrection{Mode: CurrentIngestSampling().Mode, Since: map[string]time.Time{}}
	for k, mvs := range samplingSurfaceMVs {
		if t := EffectiveWeightedSince(mvs...); !t.IsZero() {
			out.Since[k] = t
		}
	}
	return out
}
