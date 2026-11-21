package chstore

// topology_weight.go — v0.10.2435, sampling-weight Phase 2 for the Go-side
// topology aggregator (docs/architecture/sampling-weight.md "Go workers").
// The writer passes of WriteTopologyBucket add w_calls / w_errors =
// Σ sample_weight of the counted spans; the service-map read uses them per
// row when present (w_calls > 0 — every weight is ≥ 1, so a written row is
// never 0) and falls back to calls / errors otherwise (rows from old pods or
// from before the column). No coverage marker: each row is complete or old.

import (
	"context"
	"regexp"
	"strings"
)

func (s *Store) probeTopoWeightCols(ctx context.Context) {
	rows, err := s.conn.Query(ctx, `SELECT w_calls, w_errors FROM topology_edges_5m LIMIT 1 SETTINGS max_execution_time = 3`)
	maybeCloseRows(rows, err)
	s.hasTopoWeightCols.Store(err == nil)
}

// topoWriteWeighted — the writers can stamp w_calls / w_errors (both
// columns exist; spans has sample_weight). Independent of the mode: with
// mode off every weight is 1 and w_calls = calls.
func (s *Store) topoWriteWeighted() bool {
	return s.hasTopoWeightCols.Load() && s.hasSampleWeightCol.Load()
}

// topoReadWeighted — the service map reads represented calls.
func (s *Store) topoReadWeighted() bool {
	return s.hasTopoWeightCols.Load() && CurrentIngestSampling().Mode == SamplingModeOn
}

func (s *Store) topoWeightSQL(sql string) string {
	if !s.topoWriteWeighted() {
		return sql
	}
	return topoWeightSQLWith(sql)
}

var (
	reTopoErrors   = regexp.MustCompile(`toUInt64\(countIf\(((?:c\.)?)status_code = 'error'\)\)(\s+)AS errors,`)
	reTopoInnerDur = regexp.MustCompile(`\bduration, status_code,`)
	reTopoInnerSC  = regexp.MustCompile(`(\n[ \t]*duration,\s*\n[ \t]*status_code)`)
)

// topoWeightSQLWith — SAF: one writer pass → the same pass also writing
// w_calls, w_errors (positionally right after errors). Pinned by
// topology_weight_test.go for all four passes.
func topoWeightSQLWith(sql string) string {
	const cols = "sum_duration_ns, p99_ms, errors,"
	if !strings.Contains(sql, cols) || !reTopoErrors.MatchString(sql) {
		return sql
	}
	sql = strings.Replace(sql, cols, cols+" w_calls, w_errors,", 1)
	sql = reTopoErrors.ReplaceAllString(sql,
		"${0}\n\t\t\ttoFloat64(sum(${1}sample_weight)) AS w_calls,\n\t\t\ttoFloat64(sumIf(${1}sample_weight, ${1}status_code = 'error')) AS w_errors,")
	// Passes that aggregate over a column-projecting subquery need the
	// weight projected too.
	sql = reTopoInnerDur.ReplaceAllString(sql, "duration, status_code, sample_weight,")
	return reTopoInnerSC.ReplaceAllString(sql, "${1}, sample_weight")
}

// topoCallsSel — SAF: the service-map aggregate's call / error totals.
func topoCallsSel(weighted bool) string {
	if !weighted {
		return "toUInt64(sum(calls)) AS total_calls,\n\t\t\t\ttoUInt64(sum(errors)) AS total_errors"
	}
	return "toUInt64(round(sum(if(w_calls > 0, w_calls, toFloat64(calls))))) AS total_calls,\n\t\t\t\t" +
		"toUInt64(round(sum(if(w_calls > 0, w_errors, toFloat64(errors))))) AS total_errors"
}
