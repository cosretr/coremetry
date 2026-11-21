package chstore

// spanmetrics_metric_mv.go — v0.10.2438: the three metric_points-derived
// spanmetrics MVs (spanmetrics_calls_5m / _hist_5m / _duration_5m) recognise
// the OTel collector spanmetrics CONNECTOR names next to the legacy
// spanmetrics PROCESSOR names, with the semantics of the VM read path
// (api/red_family.go family "spanmetrics", v0.10.2435):
//
//   - entry spans only: span.kind (OTLP) / span_kind (Prometheus-receiver
//     relabel) ∈ {SPAN_KIND_SERVER, SPAN_KIND_CONSUMER}; a series WITHOUT a
//     kind dimension is kept (legacy emitters that dropped the dimension);
//   - error = status.code / status_code = STATUS_CODE_ERROR;
//   - durations stored in MILLISECONDS: unit 's' (connector `histogram.unit:
//     s`, Prometheus name …_seconds) is ×1000, anything else is taken as ms
//     (the connector's default unit). Bounds, sum and max are all scaled.
//
// DOUBLE COUNT: a service whose spans pass through both an old processor
// and a new connector emits both name families. The MVs therefore carry a
// `source_family` key column ('connector' | 'legacy'; '' = rows written
// before v0.10.2438, legacy names only). A reader MUST pick ONE family per
// (service_name, time_bucket) — connector when present, else legacy/'' —
// never sum across families. (No reader exists today: /clickhouse-schema
// §11.2.)
//
// Running installs are migrated IN PLACE (applySpanmetricsMetricMVsOnce):
// inner-table ADD COLUMN source_family + MODIFY ORDER BY (append; metadata
// only), ingest gate, MV MODIFY QUERY, Distributed wrapper ADD COLUMN — the
// sampling-weight phase-2 mechanism (sampling_weighted_mv.go). History is
// kept; a failure leaves the old query and logs; the next boot retries.

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// Metric names (OTLP name as stored in metric_points.metric). The connector
// names are SHARED with the VM read path (api/red_family.go,
// anomaly/red_vm_query.go) — change them here only.
const (
	// SpanMetricsConnectorDuration — connector duration histogram, OTLP name.
	SpanMetricsConnectorDuration = "traces.span.metrics.duration"
	// SpanMetricsConnectorDurationProm — the same histogram, Prometheus name
	// in seconds (VM; also the OTLP name after a Prometheus receiver).
	SpanMetricsConnectorDurationProm = "traces_span_metrics_duration_seconds"
	// SpanMetricsErrorStatus — error value of the status.code dimension.
	SpanMetricsErrorStatus = "STATUS_CODE_ERROR"
)

// SpanMetricsEntryKinds — span kinds counted as service entry.
var SpanMetricsEntryKinds = []string{"SPAN_KIND_SERVER", "SPAN_KIND_CONSUMER"}

// Name lists per family. Histogram names only: metric_points never holds a
// histogram split into _bucket/_sum/_count rows (OTLP ingest; a Prometheus
// receiver reassembles the histogram under its base name).
var (
	spanmetricsConnectorCalls = []string{
		"traces.span.metrics.calls",
		"traces_span_metrics_calls_total",
		"traces_span_metrics_calls",
	}
	spanmetricsConnectorDuration = []string{
		SpanMetricsConnectorDuration,
		SpanMetricsConnectorDurationProm,
		"traces_span_metrics_duration_milliseconds",
		"traces_span_metrics_duration",
	}
	spanmetricsLegacyCalls = []string{
		"traces.spanmetrics.calls.total",
		"traces_spanmetrics_calls_total",
		"spanmetrics.calls",
		"spanmetrics_calls_total",
		"calls",
	}
	spanmetricsLegacyDuration = []string{
		"traces.spanmetrics.duration",
		"traces.spanmetrics.duration.seconds.sum",
		"traces_spanmetrics_duration",
		"spanmetrics.duration",
		"duration",
	}
)

const (
	spanmetricsFamilyConnector = "connector"
	spanmetricsFamilyLegacy    = "legacy"
	spanmetricsFamilyCol       = "source_family"
)

// spanmetricsMetricFamily — SAF: which family a metric name belongs to
// ("" = not a spanmetrics name). calls=true selects the counter lists.
func spanmetricsMetricFamily(metric string, calls bool) string {
	conn, legacy := spanmetricsConnectorDuration, spanmetricsLegacyDuration
	if calls {
		conn, legacy = spanmetricsConnectorCalls, spanmetricsLegacyCalls
	}
	for _, n := range conn {
		if n == metric {
			return spanmetricsFamilyConnector
		}
	}
	for _, n := range legacy {
		if n == metric {
			return spanmetricsFamilyLegacy
		}
	}
	return ""
}

func sqlStrList(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "'" + strings.ReplaceAll(n, "'", "''") + "'"
	}
	return strings.Join(q, ", ")
}

// attrEither — value of the first present attribute key (” when none).
func attrEither(dotted, underscored string) string {
	return fmt.Sprintf("if(has(attr_keys, '%[1]s'), attr_values[indexOf(attr_keys, '%[1]s')], attr_values[indexOf(attr_keys, '%[2]s')])",
		dotted, underscored)
}

func spanmetricsEntryCond() string {
	return fmt.Sprintf("(%[1]s = '' OR %[1]s IN (%[2]s))", attrEither("span.kind", "span_kind"), sqlStrList(SpanMetricsEntryKinds))
}

func spanmetricsFamilyExpr(conn []string) string {
	return fmt.Sprintf("toLowCardinality(if(metric IN (%s), '%s', '%s'))", sqlStrList(conn), spanmetricsFamilyConnector, spanmetricsFamilyLegacy)
}

// spanmetricsMsFactor — seconds → ms; everything else is already ms.
const spanmetricsMsFactor = "if(unit IN ('s', 'seconds') OR endsWith(metric, '_seconds'), 1000.0, 1.0)"

// spanmetricsMVHead — everything after `CREATE MATERIALIZED VIEW IF NOT
// EXISTS <name>`: store.go keeps that header as a literal (the AST catalogue
// guards in mv_positional_test.go read the name from it).
const spanmetricsMVHead = `
		 ENGINE = AggregatingMergeTree
		 PARTITION BY toDate(time_bucket)
		 ORDER BY (service_name, time_bucket, source_family)
		 TTL toDate(time_bucket) + INTERVAL 30 DAY
		 SETTINGS index_granularity = 8192
		 AS SELECT
		   service_name,
		   toStartOfInterval(time, INTERVAL 5 MINUTE) AS time_bucket,
		   %s AS source_family,
`

func spanmetricsWhere(conn, legacy []string) string {
	return fmt.Sprintf(`		 FROM metric_points
		 WHERE metric IN (%s)
		   AND %s
`, sqlStrList(append(append([]string{}, conn...), legacy...)), spanmetricsEntryCond())
}

// spanmetricsCallsMVDDL — calls counter: per-(service, 5 min, family) sum
// and error sum. (value is summed as emitted; a cumulative counter needs a
// delta-aware reader.)
func spanmetricsCallsMVDDL() string {
	return "CREATE MATERIALIZED VIEW IF NOT EXISTS spanmetrics_calls_5m" + spanmetricsCallsMVBody()
}

func spanmetricsCallsMVBody() string {
	return fmt.Sprintf(spanmetricsMVHead, spanmetricsFamilyExpr(spanmetricsConnectorCalls)) +
		fmt.Sprintf(`		   sumState(value)                            AS calls_state,
		   sumIfState(value, %s = '%s') AS errors_state
`, attrEither("status.code", "status_code"), SpanMetricsErrorStatus) +
		spanmetricsWhere(spanmetricsConnectorCalls, spanmetricsLegacyCalls) +
		"\t\t GROUP BY service_name, time_bucket, source_family"
}

// spanmetricsHistMVDDL — bucket layout (bounds in ms) + element-wise counts.
func spanmetricsHistMVDDL() string {
	return "CREATE MATERIALIZED VIEW IF NOT EXISTS spanmetrics_hist_5m" + spanmetricsHistMVBody()
}

func spanmetricsHistMVBody() string {
	return fmt.Sprintf(spanmetricsMVHead, spanmetricsFamilyExpr(spanmetricsConnectorDuration)) +
		fmt.Sprintf(`		   anyState(arrayMap(b -> b * %s, bucket_bounds)) AS bounds_state,
		   sumMapState(
		     arrayMap(i -> toUInt32(i), range(0, toUInt32(length(bucket_counts)))),
		     bucket_counts
		   )                                          AS counts_state
`, spanmetricsMsFactor) +
		spanmetricsWhere(spanmetricsConnectorDuration, spanmetricsLegacyDuration) +
		"\t\t   AND length(bucket_counts) > 0\n" +
		"\t\t GROUP BY service_name, time_bucket, source_family"
}

// spanmetricsDurationMVDDL — sum (ms) + count + max (ms); avg = sum/count.
func spanmetricsDurationMVDDL() string {
	return "CREATE MATERIALIZED VIEW IF NOT EXISTS spanmetrics_duration_5m" + spanmetricsDurationMVBody()
}

func spanmetricsDurationMVBody() string {
	return fmt.Sprintf(spanmetricsMVHead, spanmetricsFamilyExpr(spanmetricsConnectorDuration)) +
		fmt.Sprintf(`		   sumState(sum_value * %[1]s) AS sum_state,
		   sumState(count)      AS count_state,
		   maxState(max_value * %[1]s) AS max_state
`, spanmetricsMsFactor) +
		spanmetricsWhere(spanmetricsConnectorDuration, spanmetricsLegacyDuration) +
		"\t\t GROUP BY service_name, time_bucket, source_family"
}

var spanmetricsMetricMVs = []string{"spanmetrics_calls_5m", "spanmetrics_duration_5m", "spanmetrics_hist_5m"}

// ── in-place migration ──────────────────────────────────────────────

// applySpanmetricsMetricMVsOnce — background, after boot (New starts it).
// Every step is idempotent; failure → old query stays, next boot retries.
func (s *Store) applySpanmetricsMetricMVsOnce() {
	for _, mv := range spanmetricsMetricMVs {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		migrated, err := s.migrateSpanmetricsMetricMV(ctx, mv)
		cancel()
		switch {
		case err != nil:
			log.Printf("[spanmetrics-mv] %s: %v — old query kept, next boot retries", mv, err)
		case migrated:
			log.Printf("[spanmetrics-mv] %s: connector names + source_family applied in place (history kept)", mv)
		}
	}
}

// spanmetricsKeyState — SAF: what the inner table's sorting key needs.
// hosts = hosts carrying the inner table; keyed = of those, how many
// already have source_family in the sorting key.
func spanmetricsKeyState(hosts, keyed uint64) (alter bool, err error) {
	switch {
	case hosts == 0:
		return false, fmt.Errorf("inner table not found")
	case keyed == hosts:
		return false, nil
	case keyed == 0:
		return true, nil
	}
	return false, fmt.Errorf("source_family in the sorting key on %d/%d hosts — partial migration, align by hand", keyed, hosts)
}

// spanmetricsInnerAlter — the inner-table DDL (ON CLUSTER injected by caller).
func spanmetricsInnerAlter(inner, on string) string {
	return "ALTER TABLE `" + inner + "`" + on +
		" ADD COLUMN IF NOT EXISTS " + spanmetricsFamilyCol + " LowCardinality(String) CODEC(ZSTD(3))," +
		" MODIFY ORDER BY (service_name, time_bucket, " + spanmetricsFamilyCol + ")"
}

func (s *Store) migrateSpanmetricsMetricMV(ctx context.Context, mv string) (bool, error) {
	storage := s.mvStorageName(mv)
	if exists, err := s.objectExistsAnyHost(ctx, storage); err != nil || !exists {
		return false, err
	}
	// The MV's column list follows its query: present on every host = the
	// MODIFY QUERY landed everywhere (fresh installs: from CREATE).
	if ok, err := s.columnPresentAllHosts(ctx, storage, spanmetricsFamilyCol); err != nil || ok {
		if ok {
			err = s.ensureSpanmetricsWrapper(ctx, mv)
		}
		return false, err
	}
	inner, err := s.mvInnerName(ctx, storage)
	if err != nil {
		return false, err
	}
	var hosts, keyed uint64
	if err := s.conn.QueryRow(ctx, "SELECT toUInt64(count()), toUInt64(countIf(position(sorting_key, '"+spanmetricsFamilyCol+"') > 0)) FROM "+
		s.clusterSystemSource("tables")+" WHERE database = currentDatabase() AND name = ? SETTINGS max_execution_time = 15", inner).Scan(&hosts, &keyed); err != nil {
		return false, err
	}
	alter, err := spanmetricsKeyState(hosts, keyed)
	if err != nil {
		return false, err
	}
	on := s.onCluster()
	if alter {
		if err := s.conn.Exec(ctx, spanmetricsInnerAlter(inner, on)); err != nil {
			return false, fmt.Errorf("inner ADD COLUMN + MODIFY ORDER BY: %w", err)
		}
	}
	sel := mvSelectBody(s.adaptDDL(canonicalMVDDL(mv))[0])
	if sel == "" {
		return false, fmt.Errorf("could not extract SELECT")
	}
	// INGEST GATE: the new SELECT's output must match the inner table, or
	// every metric_points INSERT would fail.
	if err := s.weightedQueryMatchesInner(ctx, sel, inner); err != nil {
		return false, err
	}
	if err := s.conn.Exec(ctx, "ALTER TABLE "+storage+on+" MODIFY QUERY "+sel); err != nil {
		return false, fmt.Errorf("MODIFY QUERY: %w", err)
	}
	if err := s.ensureSpanmetricsWrapper(ctx, mv); err != nil {
		return false, err
	}
	ok, err := s.columnPresentAllHosts(ctx, storage, spanmetricsFamilyCol)
	if err == nil && !ok {
		err = fmt.Errorf("MODIFY QUERY not visible on every host yet")
	}
	return ok, err
}

// ensureSpanmetricsWrapper — cluster: the Distributed wrapper carries its
// own column list.
func (s *Store) ensureSpanmetricsWrapper(ctx context.Context, mv string) error {
	if !s.clusterMode() || !highVolumeTables[mv] {
		return nil
	}
	if exists, err := s.objectExistsAnyHost(ctx, mv); err != nil || !exists {
		return err // not created yet — ensureDistributedWrappers copies the _local columns
	}
	if ok, err := s.columnPresentAllHosts(ctx, mv, spanmetricsFamilyCol); err != nil || ok {
		return err
	}
	if err := s.conn.Exec(ctx, "ALTER TABLE "+mv+s.onCluster()+" ADD COLUMN IF NOT EXISTS "+spanmetricsFamilyCol+" LowCardinality(String)"); err != nil {
		return fmt.Errorf("wrapper ADD COLUMN: %w", err)
	}
	return nil
}
