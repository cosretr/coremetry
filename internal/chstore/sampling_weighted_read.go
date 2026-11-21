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

// weighSQLArgs — v0.10.2442: weighSQL for a query whose merge CONDITIONS
// carry `?` binds (service_slowdown). In a mixed window the rewrite repeats
// such a condition (old side + weighted side, three times for a quantile);
// the bind values are repeated in the same order, so the caller's binds
// stay aligned. Off / shadow / no marker → sql and args unchanged.
func (s *Store) weighSQLArgs(sql string, args []any, from time.Time, mvs ...string) (string, []any) {
	return weighSQLArgsWith(weightedCoverage(CurrentIngestSampling().Mode, WeightedSinceMarkers(), from, mvs...), sql, args)
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
// -MergeState forms (an outer query merges the produced state) are
// rewritten by weighMergeState, full windows only (v0.10.2442).
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
//
// A call whose condition carries a `?` bind placeholder in a MIXED window:
// the mixed form repeats the condition. Without bind tracking (track =
// false, weighSQL) such a call is left as is and reads unweighted states
// there. With tracking (weighSQLArgs, v0.10.2442) it is rewritten and the
// condition's bind values are repeated once per copy, in text order; the
// returned args are nil when track is false.
func rewriteCalls(sql string, re *regexp.Regexp, mixed bool, args []any, track bool, build func(g []string, cond string) string) (string, []any) {
	var out strings.Builder
	var outArgs []any
	ai := 0
	take := func(n, copies int) {
		if !track {
			return
		}
		for c := 0; c < copies; c++ {
			outArgs = append(outArgs, args[ai:ai+n]...)
		}
		ai += n
	}
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
		head := sql[pos : pos+loc[0]]
		out.WriteString(head)
		take(countPlaceholders(head), 1)
		call := sql[pos+loc[0] : end]
		k := countPlaceholders(cond)
		if mixed && k > 0 && !track {
			out.WriteString(call)
		} else {
			built := build(g, cond)
			out.WriteString(built)
			copies := 1
			if k > 0 {
				copies = countPlaceholders(built) / k
			}
			take(countPlaceholders(call), copies)
		}
		pos = end
	}
	out.WriteString(sql[pos:])
	if track {
		outArgs = append(outArgs, args[ai:]...)
	}
	return out.String(), outArgs
}

// countPlaceholders — `?` binds outside single-quoted string literals.
func countPlaceholders(s string) int {
	n := 0
	inStr := false
	for i := 0; i < len(s); i++ {
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
		case c == '?':
			n++
		}
	}
	return n
}

func andCond(cond, extra string) string {
	if cond == "" {
		return extra
	}
	return "(" + cond + ") AND " + extra
}

// weighSQLWith — SAF rewrite.
func weighSQLWith(c weightCov, sql string) string {
	out, _ := weighSQLCore(c, sql, nil, false)
	return out
}

// weighSQLArgsWith — SAF rewrite with bind tracking (weighSQLArgs). A
// placeholder count that does not match len(args) means SQL this helper
// does not understand: conditions with `?` then stay unweighted in a mixed
// window, exactly like weighSQL.
func weighSQLArgsWith(c weightCov, sql string, args []any) (string, []any) {
	if !c.on() {
		return sql, args
	}
	if countPlaceholders(sql) != len(args) {
		return weighSQLWith(c, sql), args
	}
	return weighSQLCore(c, sql, args, true)
}

func weighSQLCore(c weightCov, sql string, args []any, track bool) (string, []any) {
	if !c.on() {
		return sql, args
	}
	ts := fmt.Sprintf("toDateTime(%d)", c.since.Unix())
	sql, args = rewriteCalls(sql, reCountMerge, !c.full, args, track, func(g []string, cond string) string {
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
	sql, args = rewriteCalls(sql, reQuantMerge, !c.full, args, track, func(g []string, cond string) string {
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
	if c.full {
		sql = weighMergeState(sql)
	}
	return sql, args
}

// reMergeState — `quantilesTDigestMergeState(L)(duration_q_state) AS a`:
// a two-level read (per-bucket state here, window merge outside).
var reMergeState = regexp.MustCompile(`\bquantilesTDigestMergeState\(([0-9.,\s]+)\)\(\s*duration_q_state\s*\)(\s+AS\s+)(\w+)`)

// weighMergeState — v0.10.2442: the -MergeState twin (endpoints window
// percentiles, operation_routes, alert series), FULL windows only. The
// inner state and every outer `quantilesTDigestMerge(L)(a)` of its alias
// switch together (a weighted state needs the weighted merge). A mixed
// window keeps both levels unweighted: one outer merge cannot combine the
// two state types.
func weighMergeState(sql string) string {
	var aliases []string
	sql = reMergeState.ReplaceAllStringFunc(sql, func(m string) string {
		g := reMergeState.FindStringSubmatch(m)
		aliases = append(aliases, g[3])
		return "quantilesTDigestWeightedMergeState(" + g[1] + ")(w_duration_q_state)" + g[2] + g[3]
	})
	for _, a := range aliases {
		re := regexp.MustCompile(`\bquantilesTDigestMerge\(([0-9.,\s]+)\)\(\s*` + regexp.QuoteMeta(a) + `\s*\)`)
		sql = re.ReplaceAllString(sql, "quantilesTDigestWeightedMerge(${1})("+a+")")
	}
	return sql
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

// weighRawSQL — v0.10.2442: the raw-spans twin of weighSQL for readers
// whose SQL is hand-written rather than built by aggToSQLW (alert rules,
// alert series, SLO, raw /api/services, raw endpoints). Mode ≠ on or no
// column → byte-identical SQL.
func (s *Store) weighRawSQL(sql string) string {
	return weighRawSQLWith(s.rawSpansWeighted(), sql)
}

// WeighRawSQL — weighRawSQL for readers outside chstore (evaluator).
func (s *Store) WeighRawSQL(sql string) string { return s.weighRawSQL(sql) }

var (
	reRawCount        = regexp.MustCompile(`\bcount\(\s*\)`)
	reRawCountIf      = regexp.MustCompile(`\bcountIf\(`)
	reRawSumDur       = regexp.MustCompile(`\bsum\(\s*duration\s*\)`)
	reRawAvgDur       = regexp.MustCompile(`\bavg\(\s*duration\s*\)`)
	reRawQuantile     = regexp.MustCompile(`\bquantile(?:TDigest)?\(([0-9.]+)\)\(\s*duration\s*\)`)
	reRawQState       = regexp.MustCompile(`\bquantileState\(([0-9.]+)\)\(\s*duration\s*\)(\s+AS\s+)(\w+)`)
	reRawQsState      = regexp.MustCompile(`\bquantilesTDigestState\(([0-9.,\s]+)\)\(\s*duration\s*\)(\s+AS\s+)(\w+)`)
	reRawQMergeOuter  = `\bquantileMerge\(([0-9.]+)\)\(\s*%s\s*\)`
	reRawQsMergeOuter = `\bquantilesTDigestMerge\(([0-9.,\s]+)\)\(\s*%s\s*\)`
)

// weighRawSQLWith — SAF rewrite of raw-span aggregates to represented
// spans (rawWeight = sample_weight). Counts keep their UInt64 type (Go scan
// targets unchanged) and the duration sum its Int64 type; avg becomes
// Σwd / Σw; every quantile (reservoir or TDigest, scalar or -State with its
// outer -Merge) becomes the weighted TDigest. countIf conditions are copied
// once, so `?` binds keep their order.
func weighRawSQLWith(on bool, sql string) string {
	if !on {
		return sql
	}
	wu := rawWeightUInt
	var b strings.Builder
	for {
		loc := reRawCountIf.FindStringIndex(sql)
		if loc == nil {
			break
		}
		cl := closeParen(sql, loc[1])
		if cl < 0 {
			break
		}
		b.WriteString(sql[:loc[0]])
		b.WriteString("toUInt64(round(sumIf(" + rawWeight + ", " + strings.TrimSpace(sql[loc[1]:cl]) + ")))")
		sql = sql[cl+1:]
	}
	b.WriteString(sql)
	sql = b.String()
	sql = reRawCount.ReplaceAllString(sql, "toUInt64(round(sum("+rawWeight+")))")
	sql = reRawSumDur.ReplaceAllString(sql, "toInt64(round(sum(duration * "+rawWeight+")))")
	sql = reRawAvgDur.ReplaceAllString(sql, "(sum(duration * "+rawWeight+") / sum("+rawWeight+"))")
	sql = reRawQuantile.ReplaceAllString(sql, "quantileTDigestWeighted(${1})(duration, "+wu+")")
	for _, st := range []struct {
		re        *regexp.Regexp
		fn, outer string
		outerFn   string
	}{
		{reRawQState, "quantileTDigestWeightedState", reRawQMergeOuter, "quantileTDigestWeightedMerge"},
		{reRawQsState, "quantilesTDigestWeightedState", reRawQsMergeOuter, "quantilesTDigestWeightedMerge"},
	} {
		var aliases []string
		sql = st.re.ReplaceAllStringFunc(sql, func(m string) string {
			g := st.re.FindStringSubmatch(m)
			aliases = append(aliases, g[3])
			return st.fn + "(" + g[1] + ")(duration, " + wu + ")" + g[2] + g[3]
		})
		for _, a := range aliases {
			re := regexp.MustCompile(fmt.Sprintf(st.outer, regexp.QuoteMeta(a)))
			sql = re.ReplaceAllString(sql, st.outerFn+"(${1})("+a+")")
		}
	}
	return sql
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
