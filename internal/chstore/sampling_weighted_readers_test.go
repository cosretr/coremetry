package chstore

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.2442 — sampling-weight Phase 2 reader coverage: every reader this
// slice touches keeps byte-identical SQL (and binds) with mode off / shadow,
// and reads represented spans with mode on.

// readerSQLs — the hand-written reader texts this slice weights.
func readerSQLs(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{
		"services_raw":          servicesRawSQL("WHERE time >= ?", " HAVING span_count >= ?", "span_count DESC", " LIMIT 50 OFFSET 0"),
		"business_code_svcs":    businessCodeServicesSQL,
		"business_code_seen":    businessCodeSeenSQL,
		"slowdown_services":     serviceSlowdownServicesSQL(""),
		"operation_routes":      bareVerbRouteOpsSQL("spanmetrics_1m"),
		"slo_latency_mv":        sloLatencyMVSQL("spanmetrics_1m", true, true, 15),
		"alert_sample_count_mv": alertMetricSeriesSQL(alertSampleCountPlan(false), AlertSeriesSpec{StepSec: 60, FrameSec: 300}),
	}
	for _, m := range []string{"error_rate", "p95_ms", "avg_ms", "http_5xx_rate", "db_p99_ms", "rpc_error_rate", "mq_publish_p95_ms", "http_count"} {
		for _, raw := range []bool{false, true} {
			p, err := alertMetricSeriesPlan(m, raw, "spanmetrics_1m")
			if err != nil {
				t.Fatal(err)
			}
			k := "series_" + m
			if raw {
				k += "_raw"
			}
			out[k] = alertMetricSeriesSQL(p, AlertSeriesSpec{StepSec: 60, FrameSec: 300})
		}
		p, err := measureAllServicesPlan(m, "spanmetrics_1m")
		if err != nil {
			t.Fatal(err)
		}
		out["measure_all_"+m] = p.sql
	}
	return out
}

func TestWeighReadersModeOffIdentical(t *testing.T) {
	m := map[string]time.Time{}
	for _, mv := range weightedMVNames() {
		m[mv] = time.Unix(1_700_000_000, 0)
	}
	from := time.Unix(1_800_000_000, 0)
	for name, sql := range readerSQLs(t) {
		if weighRawSQLWith(false, sql) != sql {
			t.Errorf("%s: raw rewrite with mode off changed SQL", name)
		}
		for _, mode := range []string{SamplingModeOff, SamplingModeShadow, ""} {
			c := weightedCoverage(mode, m, from, "service_summary_5m")
			if weighSQLWith(c, sql) != sql {
				t.Errorf("%s: mode %q rewrote SQL", name, mode)
			}
			args := make([]any, countPlaceholders(sql))
			for i := range args {
				args[i] = i
			}
			gotSQL, gotArgs := weighSQLArgsWith(c, sql, args)
			if gotSQL != sql || !reflect.DeepEqual(gotArgs, args) {
				t.Errorf("%s: mode %q changed SQL/binds", name, mode)
			}
		}
	}
}

// Raw-span alert reads (the v0.10.2441 gap): with mode on the transport
// families read Σ sample_weight, like error_rate on spanmetrics_1m.
func TestWeighRawAlertFamilies(t *testing.T) {
	for _, m := range []string{"http_5xx_rate", "http_4xx_rate", "http_error_rate", "db_error_rate", "rpc_error_rate"} {
		p, err := measureAllServicesPlan(m, "spanmetrics_1m")
		if err != nil {
			t.Fatal(err)
		}
		if p.source != "spans" {
			t.Fatalf("%s: expected raw plan, got %s", m, p.source)
		}
		w := weighRawSQLWith(true, p.sql)
		_, num, _ := TransportFilter(m)
		if !strings.Contains(w, "sumIf("+rawWeight+", "+num+")") || !strings.Contains(w, "sum("+rawWeight+")") {
			t.Fatalf("%s: not weighted:\n%s", m, w)
		}
		if strings.Contains(w, "count()") || strings.Contains(w, "countIf(") {
			t.Fatalf("%s: unweighted count left:\n%s", m, w)
		}
		if countPlaceholders(w) != countPlaceholders(p.sql) {
			t.Fatalf("%s: bind count changed", m)
		}
	}
	p, _ := measureAllServicesPlan("db_p99_ms", "spanmetrics_1m")
	if w := weighRawSQLWith(true, p.sql); !strings.Contains(w, "quantileTDigestWeighted(0.99)(duration, "+rawWeightUInt+") / 1e6") {
		t.Fatalf("raw quantile not weighted:\n%s", w)
	}
	p, _ = measureAllServicesPlan("http_avg_ms", "spanmetrics_1m")
	if w := weighRawSQLWith(true, p.sql); !strings.Contains(w, "(sum(duration * "+rawWeight+") / sum("+rawWeight+")) / 1e6") {
		t.Fatalf("raw avg not weighted:\n%s", w)
	}
}

func TestWeighRawSeriesStates(t *testing.T) {
	p, err := alertMetricSeriesPlan("p95_ms", true, "spanmetrics_1m")
	if err != nil {
		t.Fatal(err)
	}
	w := weighRawSQLWith(true, alertMetricSeriesSQL(p, AlertSeriesSpec{StepSec: 60, FrameSec: 120}))
	for _, frag := range []string{
		"quantileTDigestWeightedState(0.95)(duration, " + rawWeightUInt + ") AS q",
		"quantileTDigestWeightedMerge(0.95)(q) OVER w",
	} {
		if !strings.Contains(w, frag) {
			t.Fatalf("raw series lacks %q:\n%s", frag, w)
		}
	}
	p, _ = alertMetricSeriesPlan("avg_ms", true, "spanmetrics_1m")
	if w := weighRawSQLWith(true, alertMetricSeriesSQL(p, AlertSeriesSpec{StepSec: 60, FrameSec: 120})); !strings.Contains(w, "toInt64(round(sum(duration * "+rawWeight+"))) AS d") {
		t.Fatalf("raw duration sum not weighted:\n%s", w)
	}
}

func TestWeighRawServicesAndSLO(t *testing.T) {
	sql := servicesRawSQL("WHERE time >= ?", "", "span_count DESC", " LIMIT 50 OFFSET 0")
	w := weighRawSQLWith(true, sql)
	for _, frag := range []string{
		"toUInt64(round(sum(" + rawWeight + ")))                                  AS span_count",
		"toUInt64(round(sumIf(" + rawWeight + ", status_code = 'error')))",
		"quantileTDigestWeighted(0.99)(duration, " + rawWeightUInt + ")",
		"sumIf(" + rawWeight + ", duration > ? * 1e6 AND duration <= ? * 1e6)",
	} {
		if !strings.Contains(w, frag) {
			t.Fatalf("raw services lacks %q:\n%s", frag, w)
		}
	}
	if countPlaceholders(w) != countPlaceholders(sql) {
		t.Fatal("bind count changed")
	}
	slo := "SELECT count() AS total, countIf(duration <= ?) AS good FROM spans WHERE service_name = ?"
	if got := weighRawSQLWith(true, slo); got != "SELECT toUInt64(round(sum("+rawWeight+"))) AS total, toUInt64(round(sumIf("+rawWeight+", duration <= ?))) AS good FROM spans WHERE service_name = ?" {
		t.Fatalf("slo raw: %s", got)
	}
}

// -MergeState (endpoints / operation_routes / alert series): full window →
// weighted state AND its outer merge; mixed → both levels unweighted.
func TestWeighMergeState(t *testing.T) {
	since := time.Unix(1_800_000_000, 0)
	sql := bareVerbRouteOpsSQL("spanmetrics_1m")
	full := weighSQLWith(weightCov{since: since, full: true}, sql)
	for _, frag := range []string{
		"quantilesTDigestWeightedMergeState(0.5, 0.9, 0.95, 0.99)(w_duration_q_state) AS q_state",
		"quantilesTDigestWeightedMerge(0.5, 0.9, 0.95, 0.99)(q_state) AS q",
		"quantilesTDigestWeightedMerge(0.5, 0.9, 0.95, 0.99)(w_duration_q_state)      AS q_slot",
	} {
		if !strings.Contains(full, frag) {
			t.Fatalf("full lacks %q:\n%s", frag, full)
		}
	}
	if strings.Contains(full, "(duration_q_state)") || strings.Contains(full, "quantilesTDigestMerge(") {
		t.Fatalf("unweighted quantile left in full window:\n%s", full)
	}
	mixed := weighSQLWith(weightCov{since: since}, sql)
	if !strings.Contains(mixed, "quantilesTDigestMergeState(0.5, 0.9, 0.95, 0.99)(duration_q_state) AS q_state") ||
		!strings.Contains(mixed, "quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(q_state) AS q") {
		t.Fatalf("mixed window must keep -MergeState unweighted:\n%s", mixed)
	}
	p, _ := alertMetricSeriesPlan("p99_ms", false, "spanmetrics_1m")
	s := weighSQLWith(weightCov{since: since, full: true}, alertMetricSeriesSQL(p, AlertSeriesSpec{StepSec: 300, FrameSec: 300}))
	if !strings.Contains(s, "arrayElement(quantilesTDigestWeightedMerge(0.5,0.95,0.99)(q) OVER w, 3)") {
		t.Fatalf("alert series outer merge:\n%s", s)
	}
}

// service_slowdown: `?` inside merge conditions — mixed window repeats the
// condition AND its bind, so binds stay aligned.
func TestWeighSQLArgsRepeatsBinds(t *testing.T) {
	cur := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	q, args := ServiceSlowdownServicesQuery(cur, DefaultServiceSlowdown(), DefaultAnomalySensitivity(), []string{"svc-a"})
	mixed := weightCov{since: cur.Add(-time.Hour)}
	gq, ga := weighSQLArgsWith(mixed, q, args)
	if countPlaceholders(gq) != len(ga) {
		t.Fatalf("placeholders %d ≠ binds %d", countPlaceholders(gq), len(ga))
	}
	if !strings.Contains(gq, "sumMergeIf(w_span_count_state, (time_bucket >= ?) AND time_bucket >= ") ||
		!strings.Contains(gq, "quantilesTDigestWeightedMergeIf(0.5, 0.95, 0.99)(w_duration_q_state, time_bucket < ?)") {
		t.Fatalf("slowdown not weighted in mixed window:\n%s", gq)
	}
	// cur_calls: cond `time_bucket >= ?` twice → bind cur twice, then
	// hour_calls (hourStart, cur) twice.
	if !ga[0].(time.Time).Equal(cur) || !ga[1].(time.Time).Equal(cur) ||
		!ga[2].(time.Time).Equal(args[1].(time.Time)) || !ga[3].(time.Time).Equal(cur) ||
		!ga[4].(time.Time).Equal(args[1].(time.Time)) || !ga[5].(time.Time).Equal(cur) {
		t.Fatalf("bind order: %v", ga[:6])
	}
	// Trailing binds (services, floors) unchanged and last.
	if !reflect.DeepEqual(ga[len(ga)-8:], args[len(args)-8:]) {
		t.Fatalf("tail binds moved: %v vs %v", ga[len(ga)-8:], args[len(args)-8:])
	}
	// Full window: no repetition.
	fq, fa := weighSQLArgsWith(weightCov{since: cur.Add(-48 * time.Hour), full: true}, q, args)
	if !reflect.DeepEqual(fa, args) || countPlaceholders(fq) != len(args) {
		t.Fatal("full window must not repeat binds")
	}
	// Placeholder/bind mismatch → no tracking, `?` conditions left as is.
	bq, ba := weighSQLArgsWith(mixed, q, args[:3])
	if len(ba) != 3 || !strings.Contains(bq, "countMergeIf(span_count_state, time_bucket >= ?)") {
		t.Fatal("mismatched binds must fall back to the untracked rewrite")
	}
}

func TestBusinessCodeWeighted(t *testing.T) {
	cols, ok := weightedMVs["business_code_summary_5m"]
	if !ok || len(cols) != 2 {
		t.Fatal("business_code_summary_5m must carry weighted count + error states")
	}
	if weightedMVBucket("business_code_summary_5m") != 5*time.Minute {
		t.Fatal("bucket")
	}
	if !strings.Contains((&Store{}).weightedCanonicalDDL("business_code_summary_5m"), "sumIfState(toFloat64(sample_weight), status_code = 'error') AS w_error_count_state") {
		t.Fatal("business code canonical weighted DDL")
	}
	w := weighSQLWith(weightCov{since: time.Unix(1_800_000_000, 0), full: true}, businessCodeServicesSQL)
	if !strings.Contains(w, "toUInt64(round(sumMerge(w_span_count_state))) AS c") || !strings.Contains(w, "toUInt64(round(sumIfMerge(w_error_count_state))) AS e") {
		t.Fatalf("business code reader:\n%s", w)
	}
}
