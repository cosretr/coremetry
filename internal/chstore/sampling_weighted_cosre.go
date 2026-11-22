package chstore

// sampling_weighted_cosre.go — v0.10.2457: sampling weight on the CoSRE
// lookup readers added in v0.10.2449 / 2453 / 2454 (endpoint_lookup,
// trace_where, messaging_flow, downstream_calls, dependency_path,
// get_service_health report). Same helpers as every other reader:
// weighSQL / weighSQLPeriods for MV merges, weighRawSQLWith for raw spans,
// topology w_calls / w_errors per row. Mode ≠ on → byte-identical SQL and
// binds (TestCoSREReadersModeOffIdentical). Each result carries Weighted so
// the tool labels its numbers "örnekleme düzeltilmiş" vs "sampled".

import "time"

// Topology represented calls / errors per row: a row written before the
// w_* columns (or by an old pod) has w_calls = 0 → its stored counts.
const (
	topoWCalls  = "if(w_calls > 0, w_calls, toFloat64(calls))"
	topoWErrors = "if(w_calls > 0, w_errors, toFloat64(errors))"
)

// rawWeightProj — SAF: the extra projection a column-projecting subquery
// needs so weighRawSQLWith's outer Σ sample_weight resolves; "" when off.
func rawWeightProj(w bool) string {
	if w {
		return ", sample_weight"
	}
	return ""
}

// topoPeriodsWeighted — a two-window topology read (current vs reference)
// is weighted only when the reference starts at or after the moment mode
// went on: rows before it carry weight 1 (w_calls = calls), so a straddling
// pair would show the correction itself as a 20× traffic change (the
// weighSQLPeriods rule for MVs).
func (s *Store) topoPeriodsWeighted(refFrom time.Time) bool {
	return topoPeriodsWeightedWith(s.topoReadWeighted(), CurrentIngestSampling().OnSince, refFrom)
}

// topoPeriodsWeightedWith — SAF core of topoPeriodsWeighted.
func topoPeriodsWeightedWith(readWeighted bool, onSince, refFrom time.Time) bool {
	return readWeighted && !onSince.After(refFrom)
}

// RawSpansWeighted — raw-span aggregates read represented spans (mode on
// and spans.sample_weight stored). For tool labels.
func (s *Store) RawSpansWeighted() bool { return s.rawSpansWeighted() }

// SpanMVWeighted — a read of mvs starting at from gets weighted states
// (mode on, every MV has a coverage marker). For tool labels of readers
// that weigh through weighSQL without returning a flag.
func SpanMVWeighted(from time.Time, mvs ...string) bool {
	return weightedCoverage(CurrentIngestSampling().Mode, WeightedSinceMarkers(), from, mvs...).on()
}
