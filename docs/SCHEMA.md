# ClickHouse schema conventions

These are the rules every new table / column added to Coremetry should
follow. They keep storage compact and reads fast even at high cardinality.

## 1. `LowCardinality(String)` for bounded-distinct columns

Use `LowCardinality(String)` for any column whose distinct values number
in the dozens to low thousands — even if the alphabet is theoretically
unbounded. It dictionary-encodes the column with a per-part dictionary,
so storage shrinks ~10×, predicate evaluation is cheap, and `GROUP BY`
on the column does a sort-by-id rather than a hash by string bytes.

| Column class                | Use `LowCardinality`? | Why |
|-----------------------------|---|---|
| `service_name`              | ✅ Yes | Bounded by your service catalogue (10s–100s) |
| `span_kind`, `status_code`  | ✅ Yes | Tiny enum |
| `host_name`, `deploy_env`   | ✅ Yes | Bounded by infra |
| `http_method`, `http_route` | ✅ Yes | Limited verbs / route templates |
| `db_system`, `rpc_system`   | ✅ Yes | OTel semconv enum |
| `peer_service`              | ✅ Yes | Same alphabet as `service_name` |
| `severity_text`             | ✅ Yes | trace/debug/info/warn/error/fatal |
| `attr_keys`, `res_keys`     | ✅ Yes | `Array(LowCardinality(String))` — keys, not values |
| `trace_id`, `span_id`       | ❌ No  | Effectively unique per row → dictionary explodes |
| `attr_values`, `res_values` | ❌ No  | Free-form |
| `body` (logs)               | ❌ No  | Arbitrary text |

Default for new attribute columns: start as `LowCardinality(String)`.
If you discover the cardinality is much higher than expected (e.g.
millions of distinct values), drop the wrapper — `LowCardinality` over
high-cardinality data is a slight pessimisation.

## 2. Materialized Views (`AggregatingMergeTree`) for hot read paths

Reads that scan billions of raw spans to compute aggregates (count,
quantile, error rate per time bucket) get materialised into a summary
table on insert and merged at query time via `*State()` /
`*Merge()` aggregate-function pairs.

Every MV in this codebase follows the same shape:

```sql
CREATE MATERIALIZED VIEW <name>_<bucket>
ENGINE = AggregatingMergeTree
PARTITION BY toDate(time_bucket)
ORDER BY (group_keys..., time_bucket)
TTL toDate(time_bucket) + INTERVAL <N> DAY
AS SELECT
  group_keys...,
  toStartOfInterval(time, INTERVAL <bucket>) AS time_bucket,
  countState()                                AS span_count_state,
  countIfState(condition)                     AS subset_count_state,
  sumState(metric)                            AS metric_sum_state,
  quantilesState(0.5, 0.95, 0.99)(metric)     AS metric_q_state
FROM <source_table>
GROUP BY group_keys, time_bucket
```

Read with `*Merge()`:

```sql
SELECT countMerge(span_count_state) AS spans,
       arrayElement(quantilesMerge(0.5, 0.95, 0.99)(metric_q_state), 3) / 1e6 AS p99_ms
FROM <name>_<bucket>
WHERE time_bucket >= ? AND time_bucket <= ?
GROUP BY group_keys
```

Existing MVs:

| MV                    | Source  | Bucket | Used by |
|-----------------------|---------|--------|---------|
| `service_summary_5m`  | `spans` | 5 min  | `chstore.GetServiceSummary5m` (services overview, anomaly baseline) |
| `trace_id_ts`         | `spans` | 1 h (ORDER BY trace_id first) | `chstore.traceWindowFromIDTs` — trace open: trace_id → [start, end] PK lookup before the tiered `trace_summary_5m` probe; not backfilled, gated by `sourceCovers` |

## 3. Partitioning + ORDER BY

- Time-series tables: `PARTITION BY toDate(time)` — one part per day.
  Daily partition pruning is the cheapest filter ClickHouse offers.
- ORDER BY: put the most-selective filter column FIRST, time second.
  E.g. `ORDER BY (service_name, time)` — queries scoped to a service hit
  one mark range; queries that scan all services still benefit from the
  internal granularity index.
- For MVs: `ORDER BY (group_keys, time_bucket)` mirrors the source.

## 4. Codecs

- Time columns: `DateTime64(9) CODEC(Delta, ZSTD(3))` — Delta on the
  monotonically-increasing timestamp halves bytes.
- Duration / count columns: `Int64 CODEC(T64, ZSTD(3))` — T64 is the
  optimal integer codec when values are correlated within a row range.
- Free-form strings (body, db_statement, events JSON): `CODEC(ZSTD(3))`
  — text compresses ~3×.
- Numeric float metrics: `CODEC(Gorilla, ZSTD(3))` — Facebook Gorilla
  exploits float32 mantissa similarity in adjacent samples.

## 5. TTL

Every time-series table gets an explicit TTL on the partition column
sourced from `config.retention.*_days`. MVs get their own TTL — usually
longer than the raw source so the summary survives raw-data eviction.

```sql
TTL toDate(time)        + INTERVAL %d DAY    -- raw spans / logs / metrics
TTL toDate(time_bucket) + INTERVAL 90 DAY    -- summary MVs
```

**Exception — trace MVs follow `retention.spans`.** `trace_summary_5m`
and `trace_service_index_5m` hold per-trace rows (`trace_id` is in the
key), not service aggregates: a row whose trace is gone is a dead link
in `/traces`, not history. Their TTL therefore equals the spans
retention (`retention.spans`, default 30d), with
`ttl_only_drop_parts = 1`. No reader needs them past spans retention:
the hourly retention enforcer has dropped their partitions at
spans + 1 day since v0.10.263 (`retention_mv.go`), so nothing could see
older rows anyway. The CREATE carries the 30-day default; on every boot
`ApplyPersistedRetention` runs `MODIFY TTL … SETTINGS
materialize_ttl_after_modify = 0` on the combined MV's inner table
(cluster-wide, skipped when already current), and an admin retention
change applies it immediately (`retention_trace_mv.go`). Same reasoning
as `exemplars` / `span_links` riding `retention.spans`.

## 6. ID columns

`trace_id` / `span_id` are stored as hex `String`, NOT `UUID` or
`FixedString(16)`. Hex strings pay a 2× space cost vs raw bytes but make
ad-hoc queries (`WHERE trace_id = 'abc...'`) trivial and let us share
the same column across both spans and logs without conversion. ZSTD
compression recovers most of the overhead.

`Bloom_filter` index on `trace_id` makes single-trace lookups O(log N)
instead of O(N).
