package chstore

// sampling_weighted_mv.go — v0.10.2435: Phase 2 of the sampling-weight
// design (docs/architecture/sampling-weight.md). Span MVs that count or sum
// gain WEIGHTED state columns next to the old unweighted ones:
//
//	countState()                 → w_*: sumState(toFloat64(sample_weight))
//	countIfState(c)              → w_*: sumIfState(toFloat64(sample_weight), c)
//	sumState(duration)           → w_duration_sum_state: sumState(duration × w)
//	quantilesTDigestState(L)(d)  → w_duration_q_state:
//	                               quantilesTDigestWeightedState(L)(d, toUInt64(round(w)))
//
// The old states stay (sample size n, and old pods keep reading them during
// a rolling deploy). Fresh installs get the columns in CREATE (weightMVs is
// applied to the canonical catalogue); running installs are migrated IN
// PLACE by applyWeightedMVsOnce — inner-table ADD COLUMN + MV MODIFY QUERY,
// no history rewrite, no MATERIALIZE — and a coverage marker per MV
// (system_settings `sampling.weightedSince`) records when the MV started
// writing the weighted states. Readers (sampling_weighted_read.go) use the
// weighted states only for buckets after that marker and only while
// ingest.sampling.mode = on.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Apdex thresholds — MUST equal canonicalMVs' apdexT / apdex4T (pinned by
// TestWeightedApdexMatchesMV).
const (
	wApdexT  = 200 * 1_000_000
	wApdex4T = 800 * 1_000_000
)

// weightedCol — one weighted state column: name, AggregateFunction type and
// the SELECT expression that fills it.
type weightedCol struct {
	Name, Type, Expr string
}

const wExpr = "toFloat64(sample_weight)"

func wCountCol(name string) weightedCol {
	return weightedCol{name, "AggregateFunction(sum, Float64)", "sumState(" + wExpr + ")"}
}

func wCountIfCol(name, cond string) weightedCol {
	return weightedCol{name, "AggregateFunction(sumIf, Float64, UInt8)", "sumIfState(" + wExpr + ", " + cond + ")"}
}

func wDurSumCol() weightedCol {
	return weightedCol{"w_duration_sum_state", "AggregateFunction(sum, Float64)", "sumState(duration * " + wExpr + ")"}
}

func wQuantCol(levels string) weightedCol {
	return weightedCol{"w_duration_q_state",
		"AggregateFunction(quantilesTDigestWeighted(" + levels + "), Int64, UInt64)",
		"quantilesTDigestWeightedState(" + levels + ")(duration, toUInt64(round(sample_weight)))"}
}

const (
	wLevels3 = "0.5, 0.95, 0.99"
	wLevels4 = "0.5, 0.9, 0.95, 0.99"
)

func wRED(levels string) []weightedCol {
	return []weightedCol{
		wCountCol("w_span_count_state"),
		wCountIfCol("w_error_count_state", "status_code = 'error'"),
		wDurSumCol(),
		wQuantCol(levels),
	}
}

func wREDApdex() []weightedCol {
	return append(wRED(wLevels3),
		wCountIfCol("w_apdex_satisfied_state", fmt.Sprintf("duration <= %d", wApdexT)),
		wCountIfCol("w_apdex_tolerating_state", fmt.Sprintf("duration > %d AND duration <= %d", wApdexT, wApdex4T)),
	)
}

func wSpanmetrics() []weightedCol {
	return []weightedCol{
		wCountCol("w_calls_state"),
		wCountIfCol("w_error_state", "status_code = 'error'"),
		wDurSumCol(),
		wQuantCol(wLevels4),
	}
}

// weightedMVs — registry (SAF; pinned by sampling_weighted_mv_test.go).
// Left unweighted BY DESIGN: trace_summary_5m / trace_summary_1d /
// trace_service_index_5m (trace search counts what was stored; trace-level
// weighting is a later slice — see the design doc), presence/identity MVs.
// business_code_summary_5m joined in v0.10.2442 (CoSRE code → service
// calls / errors); its DDL lives outside canonicalMVs (weightedMVCatalogue).
var weightedMVs = map[string][]weightedCol{
	"service_summary_5m":          wREDApdex(),
	"operation_summary_5m":        wREDApdex(),
	"operation_group_summary_5m":  wREDApdex(),
	"service_env_summary_5m":      wREDApdex(),
	"spanmetrics_1m":              wSpanmetrics(),
	"spanmetrics_10s":             wSpanmetrics(),
	"spanmetrics_1s":              wSpanmetrics(),
	"db_summary_5m":               wRED(wLevels3),
	"db_summary_1m":               wRED(wLevels3),
	"db_caller_summary_5m":        wRED(wLevels3),
	"db_statement_summary_5m":     wRED(wLevels3),
	"db_statement_summary_1m":     wRED(wLevels3),
	"messaging_summary_5m":        wRED(wLevels3),
	"messaging_summary_1m":        wRED(wLevels3), // v0.10.2450
	"messaging_caller_summary_5m": wRED(wLevels3),
	"messaging_caller_summary_1m": wRED(wLevels3), // v0.10.2450
	"service_version_5m":          {wCountCol("w_span_count_state")},
	"business_code_summary_5m": {
		wCountCol("w_span_count_state"),
		wCountIfCol("w_error_count_state", "status_code = 'error'"),
	},
}

// weightedMVCatalogue — every CREATE the weighted registry can name: the
// canonical catalogue plus MVs migrate appends outside it.
func weightedMVCatalogue() []string {
	return append(canonicalMVs(), businessCodeMVDDL())
}

// weightedMVBucket — the MV's time_bucket width (coverage alignment).
func weightedMVBucket(mv string) time.Duration {
	switch {
	case strings.HasSuffix(mv, "_1s"):
		return time.Second
	case strings.HasSuffix(mv, "_10s"):
		return 10 * time.Second
	case strings.HasSuffix(mv, "_1m"):
		return time.Minute
	}
	return 5 * time.Minute
}

// weightedMVNames — sorted registry keys (deterministic migration order).
func weightedMVNames() []string {
	out := make([]string, 0, len(weightedMVs))
	for k := range weightedMVs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var (
	reMVFromSpans = regexp.MustCompile(`\n[ \t]*FROM spans\b`)
	reMVColEnd    = regexp.MustCompile(`\n[ \t]*\)[ \t]*\n[ \t]*ENGINE\b`)
	reMVAsSelect  = regexp.MustCompile(`(?s)\bAS\s+(SELECT\b.*)$`)
)

// weightMVDDL — SAF: injects the weighted state expressions (and, for an
// explicit column list, the column definitions) into one canonical MV DDL.
// Unknown MV or already-weighted DDL → unchanged.
func weightMVDDL(ddl string) string {
	name := mvNameFromDDL(ddl)
	cols, ok := weightedMVs[name]
	if !ok || strings.Contains(ddl, cols[0].Name) {
		return ddl
	}
	loc := reMVFromSpans.FindStringIndex(ddl)
	if loc == nil {
		return ddl
	}
	var sel strings.Builder
	for _, c := range cols {
		sel.WriteString(",\n\t\t   " + c.Expr + " AS " + c.Name)
	}
	out := ddl[:loc[0]] + sel.String() + ddl[loc[0]:]
	// Explicit column list (spanmetrics): `( … )` before ENGINE.
	head := out
	if i := strings.Index(out, "ENGINE"); i > 0 {
		head = out[:i+len("ENGINE")]
	}
	if strings.Contains(head, "(\n") && strings.Contains(head, "AggregateFunction(") {
		if m := reMVColEnd.FindStringIndex(out); m != nil {
			var defs strings.Builder
			for _, c := range cols {
				defs.WriteString(",\n\t\t   " + c.Name + " " + c.Type + " CODEC(ZSTD(3))")
			}
			out = out[:m[0]] + defs.String() + out[m[0]:]
		}
	}
	return out
}

// weightMVs — applied to the canonical catalogue.
func weightMVs(mvs []string) []string {
	for i, q := range mvs {
		mvs[i] = weightMVDDL(q)
	}
	return mvs
}

// mvSelectBody — SAF: the `SELECT …` of a CREATE MATERIALIZED VIEW.
func mvSelectBody(ddl string) string {
	m := reMVAsSelect.FindStringSubmatch(ddl)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// ── coverage marker ─────────────────────────────────────────────────

// SamplingWeightedSinceKey — system_settings: {mv: time the MV started
// writing weighted states on every host}.
const SamplingWeightedSinceKey = "sampling.weightedSince"

var weightedSinceCur atomic.Pointer[map[string]time.Time]

// WeightedSinceMarkers — process-local copy (boot + 30 s refresh).
func WeightedSinceMarkers() map[string]time.Time {
	if p := weightedSinceCur.Load(); p != nil {
		return *p
	}
	return nil
}

func setWeightedSince(m map[string]time.Time) { weightedSinceCur.Store(&m) }

func parseWeightedSince(raw []byte) map[string]time.Time {
	m := map[string]time.Time{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

// LoadWeightedSince — boot + cfgRefresh.
func (s *Store) LoadWeightedSince(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, SamplingWeightedSinceKey)
	if err != nil {
		return err
	}
	setWeightedSince(parseWeightedSince(raw))
	return nil
}

// markWeightedSince — adds markers for MVs that do not have one yet (an
// earlier marker is the true switch point, so it is never moved) and
// RESETS the markers of MVs migrated just now (reset): such an MV was not
// writing weighted states before — e.g. it was recreated unweighted after
// an old marker — so buckets before `at` must read the old states.
func (s *Store) markWeightedSince(ctx context.Context, mvs, reset []string, at time.Time) error {
	raw, err := s.GetSetting(WithoutSettingsSnapshot(ctx), SamplingWeightedSinceKey)
	if err != nil {
		return err
	}
	m := parseWeightedSince(raw)
	changed := false
	for _, mv := range reset {
		m[mv] = at.UTC().Truncate(time.Second)
		changed = true
	}
	for _, mv := range mvs {
		if _, ok := m[mv]; !ok {
			m[mv] = at.UTC().Truncate(time.Second)
			changed = true
		}
	}
	if !changed {
		setWeightedSince(m)
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := s.PutSetting(ctx, SamplingWeightedSinceKey, b); err != nil {
		return err
	}
	setWeightedSince(m)
	return nil
}

// ── in-place migration ──────────────────────────────────────────────

// weightedMVsWhenActive — v0.10.2435 (operatör 2026-11-21): the in-place
// migration (inner ADD COLUMN + MODIFY QUERY, ~2× summary storage for new
// data) runs only once ingest.sampling is shadow or on. With the mode off a
// deploy leaves the prod schema untouched; the poll picks up a later switch
// (settings refresh is 30 s) without a restart. Runs at most once per process.
func (s *Store) weightedMVsWhenActive(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if weightedMigrationWanted(CurrentIngestSampling()) {
			s.applyWeightedMVsOnce()
			return
		}
		<-t.C
	}
}

// weightedMigrationWanted — SAF: mode shadow/on → migrate.
func weightedMigrationWanted(c IngestSampling) bool { return c.Active() }

// applyWeightedMVsOnce — background, after boot (New starts it). Every step
// is idempotent; a failure leaves the MV unweighted (readers keep the old
// states) and the next boot retries.
func (s *Store) applyWeightedMVsOnce() {
	if !s.hasSampleWeightCol.Load() {
		// MODIFY QUERY reading sample_weight on a spans table without it
		// would code-16 every INSERT (v0.8.186 class). Re-probe once: the
		// deferred spans ALTER may just have landed.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		s.reprobeSampleWeightCol(ctx)
		cancel()
		if !s.hasSampleWeightCol.Load() {
			log.Printf("[sampling-weight] spans.sample_weight absent — weighted MV states not added")
			return
		}
	}
	var done, reset []string
	for _, mv := range weightedMVNames() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		ok, migrated, err := s.migrateWeightedMV(ctx, mv)
		cancel()
		if err != nil {
			log.Printf("[sampling-weight] %s: %v — stays unweighted, next boot retries", mv, err)
			continue
		}
		switch {
		case ok && migrated:
			reset = append(reset, mv)
		case ok:
			done = append(done, mv)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.markWeightedSince(ctx, done, reset, time.Now()); err != nil {
		log.Printf("[sampling-weight] coverage marker: %v", err)
	}
}

// migrateWeightedMV — ok: the MV writes the weighted states on every host;
// migrated: it did not before this call (the coverage marker restarts).
func (s *Store) migrateWeightedMV(ctx context.Context, mv string) (ok, migrated bool, err error) {
	cols := weightedMVs[mv]
	storage := s.mvStorageName(mv)
	exists, err := s.objectExistsAnyHost(ctx, storage)
	if err != nil || !exists {
		return false, false, err // not created on this install (gated MV) — nothing to do
	}
	if ok, err := s.weightedColsAllHosts(ctx, storage, cols); err != nil || ok {
		if ok {
			err = s.ensureWeightedWrapper(ctx, mv, cols)
		}
		return ok && err == nil, false, err
	}
	ddl := s.weightedCanonicalDDL(mv)
	if ddl == "" {
		return false, false, fmt.Errorf("canonical DDL not found")
	}
	inner, err := s.mvInnerName(ctx, storage)
	if err != nil {
		return false, false, err
	}
	on := s.onCluster()
	adds := make([]string, 0, len(cols))
	for _, c := range cols {
		adds = append(adds, "ADD COLUMN IF NOT EXISTS "+c.Name+" "+c.Type+" CODEC(ZSTD(3))")
	}
	if err := s.conn.Exec(ctx, "ALTER TABLE `"+inner+"`"+on+" "+strings.Join(adds, ", ")); err != nil {
		return false, false, fmt.Errorf("inner ADD COLUMN: %w", err)
	}
	sel := mvSelectBody(s.adaptDDL(ddl)[0])
	if sel == "" {
		return false, false, fmt.Errorf("could not extract SELECT")
	}
	// INGEST GATE: MODIFY QUERY only when the new SELECT's output matches the
	// inner table (names + aggregate state types). A mismatch (an install
	// that skipped an older upgrade) would code-16/53 every span INSERT.
	if err := s.weightedQueryMatchesInner(ctx, sel, inner); err != nil {
		return false, false, err
	}
	if err := s.conn.Exec(ctx, "ALTER TABLE "+storage+on+" MODIFY QUERY "+sel); err != nil {
		return false, false, fmt.Errorf("MODIFY QUERY: %w", err)
	}
	if err := s.ensureWeightedWrapper(ctx, mv, cols); err != nil {
		return false, false, err
	}
	ok, err = s.weightedColsAllHosts(ctx, storage, cols)
	if err == nil && ok {
		log.Printf("[sampling-weight] %s: weighted states added in place (no history rewrite)", mv)
	}
	return ok, ok, err
}

// weightedCanonicalDDL — the canonical (weighted) CREATE for mv.
func (s *Store) weightedCanonicalDDL(mv string) string {
	return weightMVDDL(mvDDLByName(weightedMVCatalogue(), mv))
}

// liveMVDDL — the CREATE a repair/recreate path must run: weighted when
// spans carries sample_weight (an MV reading a missing column would code-16
// every span INSERT), unweighted otherwise.
func (s *Store) liveMVDDL(name string) string {
	ddl := canonicalMVDDL(name)
	if s.hasSampleWeightCol.Load() {
		ddl = weightMVDDL(ddl)
	}
	return ddl
}

// weightedColsAllHosts — every weighted column present on every host's
// storage object. For an MV the column list follows its query, so presence
// on the MV object = MODIFY QUERY landed (inner ADD COLUMN alone does not
// show here).
func (s *Store) weightedColsAllHosts(ctx context.Context, storage string, cols []weightedCol) (bool, error) {
	for _, c := range cols {
		ok, err := s.columnPresentAllHosts(ctx, storage, c.Name)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func (s *Store) objectExistsAnyHost(ctx context.Context, name string) (bool, error) {
	var n uint64
	err := s.conn.QueryRow(ctx, "SELECT count() FROM "+s.clusterSystemSource("tables")+
		" WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 15", name).Scan(&n)
	return n > 0, err
}

// mvInnerName — the single `.inner_id.<view uuid>` (or `.inner.<name>`)
// storage of a combined MV across the cluster. Diverged names (a host
// recreated separately) → error: ON CLUSTER needs one name.
func (s *Store) mvInnerName(ctx context.Context, storage string) (string, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT DISTINCT if(toString(uuid) = '00000000-0000-0000-0000-000000000000',
		                   concat('.inner.', name), concat('.inner_id.', toString(uuid)))
		FROM `+s.clusterSystemSource("tables")+`
		WHERE database = currentDatabase() AND name = ? AND engine = 'MaterializedView'
		SETTINGS max_execution_time = 15`, storage)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "", err
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(names) != 1 {
		return "", fmt.Errorf("%s: %d distinct inner tables across hosts (%v) — in-place weighting skipped; align with the MV repair wizard", storage, len(names), names)
	}
	return names[0], nil
}

// weightedQueryMatchesInner — ingest gate (see migrateWeightedMV).
func (s *Store) weightedQueryMatchesInner(ctx context.Context, sel, inner string) error {
	want := map[string]string{}
	rows, err := s.conn.Query(ctx, "DESCRIBE ("+sel+")")
	if err != nil {
		return fmt.Errorf("describe new query: %w", err)
	}
	for rows.Next() {
		var name, typ, dt, de, cm, codec, ttl string
		if err := rows.Scan(&name, &typ, &dt, &de, &cm, &codec, &ttl); err != nil {
			rows.Close()
			return err
		}
		want[name] = typ
	}
	rows.Close()
	have := map[string]string{}
	rows, err = s.conn.Query(ctx, `SELECT name, type FROM system.columns
		WHERE database = currentDatabase() AND table = ? SETTINGS max_execution_time = 15`, inner)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n, t string
		if err := rows.Scan(&n, &t); err != nil {
			rows.Close()
			return err
		}
		have[n] = t
	}
	rows.Close()
	return weightedShapeMismatch(want, have)
}

// weightedShapeMismatch — SAF: every output column of the new query exists
// in the inner table; AggregateFunction columns must match exactly (plain
// columns convert on insert, e.g. String → LowCardinality(String)).
func weightedShapeMismatch(want, have map[string]string) error {
	var bad []string
	for n, t := range want {
		h, ok := have[n]
		switch {
		case !ok:
			bad = append(bad, n+" missing")
		case strings.HasPrefix(t, "AggregateFunction(") && h != t:
			bad = append(bad, n+": "+h+" ≠ "+t)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("new MV query does not match the inner table (%s) — MODIFY QUERY skipped to protect ingest", strings.Join(bad, "; "))
}

// ensureWeightedWrapper — cluster: the Distributed wrapper carries its own
// column list (v0.8.162 class); add the weighted columns there too.
func (s *Store) ensureWeightedWrapper(ctx context.Context, mv string, cols []weightedCol) error {
	if !s.clusterMode() || !highVolumeTables[mv] {
		return nil
	}
	if ok, err := s.weightedColsAllHosts(ctx, mv, cols); err != nil || ok {
		return err
	}
	adds := make([]string, 0, len(cols))
	for _, c := range cols {
		adds = append(adds, "ADD COLUMN IF NOT EXISTS "+c.Name+" "+c.Type)
	}
	if err := s.conn.Exec(ctx, "ALTER TABLE "+mv+s.onCluster()+" "+strings.Join(adds, ", ")); err != nil {
		return fmt.Errorf("wrapper ADD COLUMN: %w", err)
	}
	return nil
}
