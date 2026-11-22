package chstore

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.2452 — sampling-weight reader coverage, second pass: db_health,
// the CoSRE (mcptools) span reads, OpP99 binds and the smaller MV readers
// that never went through weighSQL. Every text below joins readerSQLs, so
// TestWeighReadersModeOffIdentical pins mode off / shadow byte-identical
// SQL and binds for it.
func readerSQLs2452(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{
		"db_health_buckets":      dbHealthBucketsSQL("", ""),
		"db_health_buckets_excl": dbHealthBucketsSQL("\n\t\t\t  AND db_system NOT IN ?", "service_name IN ?"),
		"db_health_reference":    dbHealthReferenceSQL(""),
		"op_baseline":            mustSQL(OpBaselineQuery(time.Unix(1_800_000_000, 0), time.Unix(1_800_086_400, 0), 30)),
		"ops_mv_summary":         opsMVSummarySQL("operation_summary_5m", "name", "", "optimize_skip_unused_shards = 1"),
		"ops_mv_summary_group":   opsMVSummarySQL("operation_group_summary_5m", "op_group", " AND op_group != ''", "optimize_skip_unused_shards = 1"),
		"ops_mv_spark":           opsMVSparkSQL("operation_summary_5m", "name", "", "optimize_skip_unused_shards = 1"),
		"ops_raw_summary":        opsRawSummarySQL("name", "WHERE time >= ? AND time <= ? AND service_name = ?", ""),
		"ops_raw_spark":          opsRawSparkSQL("name", "WHERE time >= ? AND time <= ? AND service_name = ?", "?,?"),
		"business_breakdown":     businessBreakdownSQL("attr_values[indexOf(attr_keys, ?)]"),
		"slow_span_corroborate":  mustSQL(SlowSpanCorroborateSQL("svc", "op", 1e9, time.Unix(1_800_000_000, 0), time.Unix(1_800_003_600, 0), 1)),
		"route_window":           routeWindowSQL(),
		"span_peers":             spanPeersMVSQL(),
		"notify_replay_traffic":  mustSQL(serviceBucketTrafficSQL([]ServiceBucket{{Service: "a", Bucket: 1_800_000_000}, {Service: "b", Bucket: 1_800_000_300}})),
		"rootcause_error_series": buildErrorRateSeriesQuery(2),
		"slow_statement_buckets": slowStatementBucketsSQL(),
		"dbstmt_summary":         dbStmtSummarySQL("WHERE stmt_hash = ?"),
		"dbstmt_summary_fine":    dbStmtSummaryFineSQL("WHERE stmt_hash = ?"),
		"dbstmt_trend":           dbStmtTrendSQLFrom(dbStmtTrendTableCoarse, "WHERE stmt_hash = ?"),
		"dbstmt_callers":         dbStmtCallersSQL("WHERE stmt_hash = ?"),
		"dbstmt_baseline":        dbStmtBaselineSQL("WHERE stmt_hash = ?"),
		"slow_queries_mv":        slowQueriesGlobalMVSQL("WHERE time_bucket >= ?"),
		"db_statements_mv":       dbStatementsMVSQL("WHERE time_bucket >= ?", true, DBStatementsSort{}),
		"statement_window":       statementWindowSQL(true, true),
		"statement_search":       statementSearchSQL(true),
		"db_detail_trend":        dbDetailTrendSQL("db_summary_5m", ""),
		"service_cluster_mv":     serviceClusterBreakdownMVSQL(),
		"service_cluster_series": serviceClusterSeriesMVSQL(2),
		"correlated_changes_mv":  correlatedChangesMVTopSQL,
		"ml_trace_errors":        fmt.Sprintf(mlTraceErrorsSQL, "''", "spanmetrics_1m"),
		"ml_trace_error_minutes": fmt.Sprintf(mlTraceErrorMinutesSQL, "spanmetrics_1m"),
	}
	for _, dwell := range []int{1, 3} {
		for _, batch := range []bool{false, true} {
			sp := pfOpLatSpec(dwell, batch)
			out[fmt.Sprintf("op_p99_pivot_%d_%v", dwell, batch)] = mustSQL(OpP99PivotQuery(sp))
			out[fmt.Sprintf("op_p99_current_%d_%v", dwell, batch)] = mustSQL(OpP99CurrentQuery(sp))
		}
	}
	return out
}

func mustSQL(q string, _ []any) string { return q }

// db_health: full window → weighted inner counts, weighted -MergeState and
// its outer merge; finalizeAggregation reads whichever state is built.
func TestWeighDBHealth(t *testing.T) {
	since := time.Unix(1_800_000_000, 0)
	full := weighSQLWith(weightCov{since: since, full: true}, dbHealthBucketsSQL("", ""))
	for _, frag := range []string{
		"toUInt64(round(sumMerge(w_span_count_state)))",
		"toUInt64(round(sumIfMerge(w_error_count_state)))",
		"quantilesTDigestWeightedMergeState(0.5, 0.95, 0.99)(w_duration_q_state) AS c_q",
		"arrayElement(quantilesTDigestWeightedMerge(0.5, 0.95, 0.99)(c_q), 3)",
	} {
		if !strings.Contains(full, frag) {
			t.Fatalf("db health buckets lacks %q:\n%s", frag, full)
		}
	}
	if countPlaceholders(full) != countPlaceholders(dbHealthBucketsSQL("", "")) {
		t.Fatal("bind count changed")
	}
	// Reference (24 h back, per-bucket groups) in a mixed window: each
	// bucket reads one side of the switch.
	mixed := weighSQLWith(weightCov{since: since}, dbHealthReferenceSQL(""))
	for _, frag := range []string{
		"countMergeIf(span_count_state, time_bucket < toDateTime(1800000000)) + sumMergeIf(w_span_count_state, time_bucket >= toDateTime(1800000000))",
		"if(min(time_bucket) >= toDateTime(1800000000), quantilesTDigestWeightedMerge(0.5, 0.95, 0.99)(w_duration_q_state), quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state))",
	} {
		if !strings.Contains(mixed, frag) {
			t.Fatalf("db health reference lacks %q:\n%s", frag, mixed)
		}
	}
}

// OpP99: the reads now pass binds and their real window start. A window
// after the switch reads the weighted states directly; binds unchanged.
func TestWeighOpP99Binds(t *testing.T) {
	sp := pfOpLatSpec(3, true)
	q, args := OpP99CurrentQuery(sp)
	from := opP99CurrentFrom(sp)
	if !from.Equal(sp.SlotStarts[len(sp.SlotStarts)-1]) {
		t.Fatal("current window start = oldest slot start")
	}
	gq, ga := weighSQLArgsWith(weightCov{since: from, full: true}, q, args)
	if !reflect.DeepEqual(ga, args) || countPlaceholders(gq) != len(ga) {
		t.Fatal("binds must stay aligned")
	}
	if !strings.Contains(gq, "quantilesTDigestWeightedMerge(0.5, 0.95, 0.99)(w_duration_q_state) AS q") ||
		!strings.Contains(gq, "toUInt64(round(sumMerge(w_span_count_state))) AS calls") {
		t.Fatalf("op p99 current not weighted:\n%s", gq)
	}
	bq, bargs := OpBaselineQuery(time.Unix(1_800_000_000, 0), time.Unix(1_800_086_400, 0), 30)
	if !opBaselineFrom(bargs).Equal(time.Unix(1_800_000_000, 0)) || !opBaselineFrom(nil).IsZero() {
		t.Fatal("baseline window start")
	}
	mq, ma := weighSQLArgsWith(weightCov{since: time.Unix(1_800_000_300, 0)}, bq, bargs)
	if !reflect.DeepEqual(ma, bargs) || !strings.Contains(mq, "if(min(time_bucket) >= toDateTime(1800000300)") {
		t.Fatalf("baseline mixed window:\n%s", mq)
	}
}

// Operations table (CoSRE service sweep / pivot trace, /service): MV and
// raw paths weighted; counts keep UInt64 so the scan targets hold.
func TestWeighOperationsReaders(t *testing.T) {
	w := weighSQLWith(weightCov{since: time.Unix(1_800_000_000, 0), full: true}, opsMVSummarySQL("operation_summary_5m", "name", "", "x = 1"))
	for _, frag := range []string{
		"toUInt64(round(sumMerge(w_span_count_state)))                            AS span_count",
		"toUInt64(round(sumIfMerge(w_error_count_state)))                           AS error_count",
		"toUInt64(round(sumIfMerge(w_apdex_satisfied_state)))",
		"quantilesTDigestWeightedMerge(0.5, 0.95, 0.99)(w_duration_q_state)",
	} {
		if !strings.Contains(w, frag) {
			t.Fatalf("ops MV lacks %q:\n%s", frag, w)
		}
	}
	raw := opsRawSummarySQL("name", "WHERE time >= ?", "")
	r := weighRawSQLWith(true, raw)
	if strings.Contains(r, "count()") || strings.Contains(r, "countIf(") || strings.Contains(r, "quantile(0.") ||
		countPlaceholders(r) != countPlaceholders(raw) {
		t.Fatalf("ops raw not weighted / binds moved:\n%s", r)
	}
	if s := weighRawSQLWith(true, opsRawSparkSQL("name", "WHERE time >= ?", "?")); !strings.Contains(s, "quantileTDigestWeighted(0.99)(duration, "+rawWeightUInt+")") ||
		!strings.Contains(s, "toInt64(round(sum(duration * "+rawWeight+"))) / 1e6") {
		t.Fatalf("ops raw spark:\n%s", s)
	}
	b := weighRawSQLWith(true, businessBreakdownSQL("x"))
	if !strings.Contains(b, "toUInt64(round(sumIf("+rawWeight+", status_code = 'error')))") {
		t.Fatalf("business breakdown:\n%s", b)
	}
	sq, _ := SlowSpanCorroborateSQL("svc", "op", 1e9, time.Unix(1_800_000_000, 0), time.Unix(1_800_003_600, 0), 1)
	if s := weighRawSQLWith(true, sq); !strings.Contains(s, "toUInt64(toUInt64(round(sum("+rawWeight+"))))") {
		t.Fatalf("slow span corroborate:\n%s", s)
	}
}

// The smaller MV readers: every touched text rewrites (a reader whose
// literals the helper does not recognise would silently stay stored).
func TestWeighSmallerMVReaders(t *testing.T) {
	cov := weightCov{since: time.Unix(1_800_000_000, 0), full: true}
	for name, sql := range readerSQLs2452(t) {
		if strings.HasPrefix(name, "ops_raw") || name == "business_breakdown" || name == "slow_span_corroborate" {
			continue // raw spans: TestWeighOperationsReaders
		}
		w := weighSQLWith(cov, sql)
		if w == sql {
			t.Errorf("%s: not rewritten under mode on", name)
			continue
		}
		if reCountMerge.MatchString(w) || reQuantMerge.MatchString(w) {
			t.Errorf("%s: unweighted merge left in a full window:\n%s", name, w)
		}
	}
}
