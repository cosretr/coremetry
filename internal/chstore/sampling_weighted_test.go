package chstore

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestWeighSQLModeOffIdentical — Phase 2 contract: mode off / shadow, or no
// coverage marker, leaves every reader's SQL byte-for-byte unchanged.
func TestWeighSQLModeOffIdentical(t *testing.T) {
	sql := `SELECT countMerge(span_count_state) AS spans, countIfMerge(error_count_state),
	  sumMerge(duration_sum_state), quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state)
	  FROM service_summary_5m WHERE time_bucket >= ?`
	m := map[string]time.Time{"service_summary_5m": time.Unix(1_700_000_000, 0)}
	from := time.Unix(1_800_000_000, 0)
	for _, mode := range []string{SamplingModeOff, SamplingModeShadow, ""} {
		if got := weighSQLWith(weightedCoverage(mode, m, from, "service_summary_5m"), sql); got != sql {
			t.Fatalf("mode %q rewrote SQL:\n%s", mode, got)
		}
	}
	// on, but marker missing for one of the MVs → unchanged
	if got := weighSQLWith(weightedCoverage(SamplingModeOn, m, from, "service_summary_5m", "operation_summary_5m"), sql); got != sql {
		t.Fatalf("missing marker rewrote SQL:\n%s", got)
	}
}

func TestWeighSQLFullAndMixed(t *testing.T) {
	marker := time.Date(2026, 11, 21, 10, 2, 10, 0, time.UTC)
	m := map[string]time.Time{"service_summary_5m": marker}
	// marker + 1 min slack, rounded up to 5 min → 10:05
	want := time.Date(2026, 11, 21, 10, 5, 0, 0, time.UTC)
	c := weightedCoverage(SamplingModeOn, m, time.Time{}, "service_summary_5m")
	if !c.since.Equal(want) || c.full {
		t.Fatalf("since=%v full=%v", c.since, c.full)
	}
	sql := `countMerge(span_count_state) countIfMerge(error_count_state) sumMerge(duration_sum_state) quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state)`
	got := weighSQLWith(c, sql)
	ts := fmt.Sprintf("toDateTime(%d)", want.Unix())
	for _, frag := range []string{
		"toUInt64(round(countMergeIf(span_count_state, time_bucket < " + ts + ") + sumMergeIf(w_span_count_state, time_bucket >= " + ts + ")))",
		"sumIfMergeIf(w_error_count_state",
		"toInt64(round(sumMergeIf(duration_sum_state",
		"if(min(time_bucket) >= " + ts + ", quantilesTDigestWeightedMerge(0.5, 0.95, 0.99)(w_duration_q_state), quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state))",
	} {
		if !strings.Contains(got, frag) {
			t.Fatalf("mixed rewrite lacks %q:\n%s", frag, got)
		}
	}
	full := weighSQLWith(weightedCoverage(SamplingModeOn, m, want, "service_summary_5m"), sql)
	if full != "toUInt64(round(sumMerge(w_span_count_state))) toUInt64(round(sumIfMerge(w_error_count_state))) toInt64(round(sumMerge(w_duration_sum_state))) quantilesTDigestWeightedMerge(0.5, 0.95, 0.99)(w_duration_q_state)" {
		t.Fatalf("full rewrite:\n%s", full)
	}
}

func TestWeightMVDDLInjects(t *testing.T) {
	all := append(canonicalMVs(), businessCodeMVDDL())
	seen := 0
	for _, name := range weightedMVNames() {
		ddl := mvDDLByName(all, name)
		if ddl == "" {
			continue // conditional MVs are appended in migrate
		}
		seen++
		w := weightMVDDL(ddl)
		for _, c := range weightedMVs[name] {
			if !strings.Contains(w, c.Expr+" AS "+c.Name) {
				t.Fatalf("%s: %s not injected:\n%s", name, c.Name, w)
			}
		}
		if strings.HasPrefix(name, "spanmetrics_") && !strings.Contains(w, "w_calls_state AggregateFunction(sum, Float64) CODEC(ZSTD(3))") {
			t.Fatalf("%s: explicit column list not extended:\n%s", name, w)
		}
		if weightMVDDL(w) != w {
			t.Fatalf("%s: injection not idempotent", name)
		}
		if body := mvSelectBody(w); !strings.HasPrefix(body, "SELECT") || !strings.Contains(body, weightedMVs[name][0].Name) {
			t.Fatalf("%s: select body extraction:\n%s", name, body)
		}
	}
	if seen < 12 {
		t.Fatalf("only %d weighted MVs found in the catalogue", seen)
	}
	// The unweighted catalogue stays unweighted (repair paths on installs
	// whose spans lacks sample_weight).
	for _, ddl := range canonicalMVs() {
		if strings.Contains(ddl, "sample_weight") {
			t.Fatalf("canonicalMVs must stay unweighted: %s", mvNameFromDDL(ddl))
		}
	}
}

func TestWeightedApdexMatchesMV(t *testing.T) {
	ddl := mvDDLByName(canonicalMVs(), "service_summary_5m")
	for _, c := range []string{"duration <= 200000000", "duration > 200000000 AND duration <= 800000000"} {
		if !strings.Contains(ddl, "countIfState("+c+")") {
			t.Fatalf("apdex threshold drift: %s", c)
		}
	}
}

func TestWeightedShapeMismatch(t *testing.T) {
	have := map[string]string{"a": "LowCardinality(String)", "s": "AggregateFunction(count)"}
	if err := weightedShapeMismatch(map[string]string{"a": "String", "s": "AggregateFunction(count)"}, have); err != nil {
		t.Fatal(err)
	}
	if err := weightedShapeMismatch(map[string]string{"s": "AggregateFunction(quantiles(0.5), Int64)"}, have); err == nil {
		t.Fatal("state type mismatch must block MODIFY QUERY")
	}
	if err := weightedShapeMismatch(map[string]string{"w": "AggregateFunction(sum, Float64)"}, have); err == nil {
		t.Fatal("missing column must block MODIFY QUERY")
	}
}

func TestSamplingOnSince(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	prev := IngestSampling{Mode: SamplingModeOn, OnSince: now.Add(-time.Hour)}
	if got := samplingOnSince(prev, "on", now); !got.Equal(prev.OnSince) {
		t.Fatal("staying on must keep OnSince")
	}
	if got := samplingOnSince(IngestSampling{Mode: "shadow"}, "on", now); !got.Equal(now) {
		t.Fatal("shadow→on stamps now")
	}
	if got := samplingOnSince(prev, "off", now); !got.IsZero() {
		t.Fatal("off clears")
	}
}

func TestWeighSQLIfCombinators(t *testing.T) {
	since := time.Unix(1_800_000_000, 0)
	mixed := weightCov{since: since}
	full := weightCov{since: since, full: true}
	sql := `countMergeIf(calls_state, period = 0), countMerge(error_state), quantilesTDigestMergeIf(0.5, 0.9, 0.95, 0.99)(duration_q_state, period = f(1, 2))`
	gotFull := weighSQLWith(full, sql)
	want := `toUInt64(round(sumMergeIf(w_calls_state, period = 0))), toUInt64(round(sumIfMerge(w_error_state))), quantilesTDigestWeightedMergeIf(0.5, 0.9, 0.95, 0.99)(w_duration_q_state, period = f(1, 2))`
	if gotFull != want {
		t.Fatalf("full:\n%s\nwant\n%s", gotFull, want)
	}
	gotMixed := weighSQLWith(mixed, sql)
	for _, frag := range []string{
		"countMergeIf(calls_state, (period = 0) AND time_bucket < toDateTime(1800000000)) + sumMergeIf(w_calls_state, (period = 0) AND time_bucket >= toDateTime(1800000000))",
		"countMergeIf(error_state, time_bucket < toDateTime(1800000000)) + sumIfMergeIf(w_error_state, time_bucket >= toDateTime(1800000000))",
		"if(minIf(time_bucket, period = f(1, 2)) >= toDateTime(1800000000), quantilesTDigestWeightedMergeIf(",
	} {
		if !strings.Contains(gotMixed, frag) {
			t.Fatalf("mixed lacks %q:\n%s", frag, gotMixed)
		}
	}
	// A `?` inside the condition is never duplicated in a mixed window.
	bind := `countMergeIf(span_count_state, time_bucket >= ?) AS c`
	if weighSQLWith(mixed, bind) != bind {
		t.Fatal("mixed rewrite must not duplicate a ? placeholder")
	}
	if got := weighSQLWith(full, bind); got != `toUInt64(round(sumMergeIf(w_span_count_state, time_bucket >= ?))) AS c` {
		t.Fatalf("full rewrite with ?: %s", got)
	}
	// -MergeState: mixed window untouched (one outer merge cannot combine
	// both state types); full window weighted (v0.10.2442, TestWeighMergeState).
	st := `quantilesTDigestMergeState(0.5, 0.95, 0.99)(duration_q_state) AS q`
	if weighSQLWith(mixed, st) != st {
		t.Fatal("MergeState must not be rewritten in a mixed window")
	}
	if got := weighSQLWith(full, st); got != `quantilesTDigestWeightedMergeState(0.5, 0.95, 0.99)(w_duration_q_state) AS q` {
		t.Fatalf("full MergeState: %s", got)
	}
}

func TestWeightedMigrationWantedOnlyWhenActive(t *testing.T) {
	for mode, want := range map[string]bool{"": false, "off": false, "shadow": true, "on": true} {
		if got := weightedMigrationWanted(NormalizeIngestSampling(IngestSampling{Mode: mode})); got != want {
			t.Errorf("mode %q: got %v want %v", mode, got, want)
		}
	}
}
