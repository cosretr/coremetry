# Sampling weight — design (Phase 1 shipped, Phase 2 proposal)

Status: Phase 1 implemented in v0.10.2430 (ingest column, settings, admin
card, operator guide `docs/operator/tail-sampling-weights.md`). Phase 2
approved 2026-11-21 and implemented in v0.10.2435 — see "Phase 2 as built"
at the end.

## Problem

Traces are sampled upstream of Coremetry (example production shape: every
failing trace + 5 % of the rest, OTel Collector `tail_sampling`; ~70 B stored
spans / 30 d on 2 shards × 2 replicas, ~4 TB). Every span MV counts stored
spans, so span-derived throughput is ~20× low for non-failing traffic and
the failure rate is inflated (true 1 % of traces failing → ~17 % of stored
traces). Metric-sourced RED (VictoriaMetrics HTTP metrics, spanmetrics
computed before sampling) is correct.

## Phase 1 (shipped)

- `spans.sample_weight Float32 DEFAULT 1 CODEC(ZSTD(3))` — adjusted count
  per stored span. Float32 because tracestate thresholds give non-integer
  counts (p = 0.3 → 3.33); 24-bit mantissa is exact for every integer weight
  ≤ 16 M (we clamp to 1e6). DEFAULT 1 = old parts / pods without the column
  read "unweighted". Near-constant column ⇒ ZSTD cost is a few bits per row
  (to be measured on prod with `system.columns.data_compressed_bytes`).
- Explicit Go-written ingest column → the op_group discipline
  (`internal/chstore/store.go` migrate, `repo.go` InsertSpans): ALTER skipped
  on an external Distributed `spans` without `cluster_name` (wrapper-only
  DEFAULT column would code-16 every INSERT, v0.8.186), metadata probe
  `hasSampleWeightCol` (atomic) decides whether the INSERT binds the column,
  re-probed after deferred cluster DDL (`ddl_defer.go`) — two-boot contract
  without a restart. Fresh installs get it in CREATE TABLE.
- Converter (`internal/otlp/sampling_weight.go`): tracestate `ot=th` →
  weight attributes → operator rules (attr on span/scope/resource, status,
  trace error in request, tail_sampling hash bucket) → default. Settings
  `ingest.sampling` (`internal/chstore/ingest_sampling_settings.go`), mode
  off | shadow | on, default off. Golden test
  `convert_sampling_golden_test.go`.
- Observability: pod-local (source, weight) tally + on-demand 5-minute
  stored-weight distribution (`/api/admin/ingest-sampling[/stored]`,
  Settings → Pipeline card).

No reader uses the weight yet.

## Phase 2 — weighted reads

### What changes, per aggregate

| Unweighted state | Weighted replacement | Note |
|---|---|---|
| `countState()` | `sumState(toFloat64(sample_weight))` | represented spans |
| `countIfState(status_code = 'error')` | `sumIfState(toFloat64(sample_weight), status_code = 'error')` | |
| `countIfState(duration <= T)` (apdex) | `sumIfState(toFloat64(sample_weight), duration <= T)` | both apdex bands |
| `sumState(duration)` | `sumState(duration * toFloat64(sample_weight))` | avg = Σwd / Σw |
| `quantilesTDigestState(q…)(duration)` | `quantilesTDigestWeightedState(q…)(duration, toUInt64(round(sample_weight)))` | CH weighted quantiles take an integer weight; fractional th-weights round (≤ 0.5/w relative error per span). Same TDigest state size. |
| `uniqState(trace_id)` (trace counts) | `sumIfState(toFloat64(sample_weight), parent_id = '')` | root spans carry the trace's weight |
| `minState/maxState/argMax*/anyState` | unchanged | exemplars, first/last seen |

The stored `countState` stays: it is the sample size `n`, needed for
confidence (a 5-minute bucket with 3 stored non-failing spans × 20 is ±58 %
1-σ) and for the "stored vs represented" label.

### MVs that count spans (source `spans`), to change

`canonicalMVs()` in `internal/chstore/store.go`:

- `service_summary_5m`, `operation_summary_5m`, `operation_group_summary_5m`,
  `service_env_summary_5m` — count, error count, duration sum, TDigest, apdex.
- `spanmetrics_1s`, `spanmetrics_10s`, `spanmetrics_1m` (tiers) — count,
  error count, duration sum, TDigest (exemplar argMax unchanged).
- `db_summary_5m`, `db_summary_1m`, `db_caller_summary_5m`,
  `db_statement_summary_5m`, `db_statement_summary_1m` — count, error, sum,
  TDigest (max/argMax unchanged).
- `messaging_summary_5m`, `messaging_caller_summary_5m` — count, error, sum,
  TDigest.
- `trace_summary_5m` (+ `trace_summary_1d` uniq), `trace_service_index_5m`,
  `service_version_5m` — span/error counts per trace / service.
- `business_code_summary_5m` (`business_code.go`) — count, error count.

Operator migrations (`migrations/`): `rollup_spans_narrow_{10s,1m,5m,1h}`
(0001), `rollup_spans_wide_{1m,5m,1h}` (0002) — plain `count()/countIf/sum`
columns (SummingMergeTree-style) → add `w_span_count Float64`,
`w_error_count`, `w_duration_sum`. `entity_seen_*` (0011) and
`workload_revision_activity_1m` (0012) record presence / activity, not RED —
leave unweighted.

Not span MVs, unaffected: `spanmetrics_calls_5m`, `spanmetrics_hist_5m`,
`spanmetrics_duration_5m` (from `metric_points`, i.e. spanmetrics connector
output — pre-sampling when the connector runs before `tail_sampling`),
`metric_catalog`, `span_links_reverse_mv`, `service_seen`, `trace_id_ts`,
`service_meta_combo_5m`, `db_receiver_instance_5m` (identity/presence).

Go workers that count raw spans: `internal/topology/aggregator.go`
(`topology_edges_5m` call/error counts) → `sum(sample_weight)`.

### Readers

71 non-test files merge these states (`countMerge`, `countIfMerge`,
`sumMerge`, `quantilesTDigestMerge`) — notably `summary.go`,
`spanmetric.go`, `endpoints*.go`, `route_red.go`, `route_prefix_red.go`,
`red_series.go`, `compare_baseline.go`, `compare_periods.go`,
`baseline.go`, `evaluator*.go`, `anomaly*.go`, `slo*.go`,
`service_slowdown.go`, `top_errors.go`, `fleet_errors.go`, `db_*.go`,
`messaging_*.go`, `dependencies.go`, `ml_volume.go`, `volume_record.go`.
Each gets one helper instead of a literal: `spanCountExpr(cov)` →
`countMerge(span_count_state)` or `sumMerge(w_span_count_state)` by
coverage (below). Raw-span verification reads (`RawSpanCounts`,
`StoredSpanBuckets`, trace health, ingest ledger) stay **unweighted by
design** — they measure what was stored.

### Live migration on a 70 B-row cluster

1. **New state columns alongside the old ones, no history rewrite.** For
   combined MVs: `ALTER TABLE <inner> ADD COLUMN w_* AggregateFunction(…)`
   ON CLUSTER, then `ALTER TABLE <mv> MODIFY QUERY` with old + new states
   (precedent: migration 0007 repaired an MV in place with MODIFY QUERY).
   Old parts get empty states (sum = 0) — never a rewrite, never a
   `MATERIALIZE COLUMN` (RAM-bound CH, 4 TB).
2. **Coverage marker.** When MODIFY QUERY lands on all replicas, write
   `system_settings` `sampling.weightedSince = {mv: <ts>}`. A reader uses
   weighted states only for buckets ≥ the first full bucket after the
   marker; earlier buckets read the old count (a bucket that straddles the
   switch holds a partial weighted sum and must not be used). The
   `sourceCovers` / rollup-coverage pattern already in the codebase.
3. **Labelling.** A window that mixes covered and uncovered buckets shows
   the switch point (chart annotation "weighted from …") and the tile says
   "stored" vs "represented". Before the marker nothing changes.
4. **Rolling deploy.** Old pods keep reading the old states (still
   written); new pods gate on the marker → no read-error window (the
   "new MV state column opens a rolling-deploy window" warning in
   `/clickhouse-schema` §3 is avoided because the old columns stay).
5. **Retire.** After the longest MV TTL (90 d) the coverage gate is always
   true; keep `countState` as sample size, drop nothing else.
6. **Thresholds.** Span-based Problem rules / detectors were tuned on
   inflated error rates; when a service's window becomes weighted, error-rate
   baselines drop sharply. Re-baseline per service at the switch (baseline
   window starts at the marker) — otherwise every service looks
   "recovered", and a 1 % true error rate that used to read 17 % can no
   longer trip a 5 % rule.

### Expected effect

- Correctness: with Option A (policy recorded) or B (hash) weights are
  Horvitz–Thompson unbiased: throughput and failure rate converge to the
  pre-sampling truth (verify against VictoriaMetrics HTTP RED for the same
  service — the comparison is the Phase 2 acceptance test). Option C keeps
  the bias quantified in the operator guide.
- Variance: non-failing traffic is a 5 % sample; low-volume operations get
  noisy buckets. Show `n` (stored) next to weighted values and widen
  baselines by 1/√n.
- Latency: weighted quantiles stop over-representing failing traces (all
  kept) relative to successful ones.
- Cost: per affected MV row +2–4 states (Float64 sums ≈ 8 B each; weighted
  TDigest ≈ same as the unweighted one when it replaces it, double while
  both are kept). MV insert CPU: one extra multiply/sum per state per span.
  Expect +10–25 % on the affected MV tables during the dual-write period,
  ≈ +5 % after dropping the duplicate TDigest. Raw `spans`: one
  near-constant Float32, expected ≪ 0.1 % of the 4 TB (measure).

### Open questions for the operator

1. Keep the unweighted TDigest during transition (doubles quantile state
   size) or switch quantiles at the marker only?
2. Which surfaces show "represented" by default vs "stored" (trace search
   counts should stay "stored")?
3. Is Option A's collector change (alpha feature gate on the gateway)
   acceptable, or do we go with Option B (hash, no collector change)?

## Phase 2 as built (v0.10.2435)

- **Schema.** `weightedMVs` (`internal/chstore/sampling_weighted_mv.go`):
  service / operation / operation_group / service_env summaries (count,
  error, duration sum, weighted TDigest, both apdex bands), spanmetrics
  1s / 10s / 1m, db_summary 5m / 1m, db_caller_summary_5m,
  db_statement_summary 5m / 1m, messaging_summary_5m,
  messaging_caller_summary_5m (count, error, sum, TDigest),
  service_version_5m (count). Old states stay. Fresh CREATE / recreate
  carries them only when `spans.sample_weight` exists (`liveMVDDL`).
- **Rollout.** Background after boot (`applyWeightedMVsOnce`): per MV
  inner `ADD COLUMN IF NOT EXISTS` (ON CLUSTER, one `.inner_id` name across
  hosts or skip) → `DESCRIBE` of the new SELECT vs the inner table (names +
  state types; mismatch = skip, protects ingest) → `MODIFY QUERY` → wrapper
  `ADD COLUMN` → columns visible on every host → marker
  `sampling.weightedSince[mv]` (an MV migrated now resets its marker).
- **Readers.** `weighSQL(sql, from, mvs…)` rewrites merge literals (incl.
  `-If`) to weighted twins for buckets ≥ ceil(marker + 1 min, bucket);
  mixed windows split counts per bucket and keep unweighted quantiles for
  groups reaching before the switch; a `?` inside a merge condition is
  never duplicated. Mode ≠ on → identical SQL. `-MergeState` quantiles
  stay unweighted. Raw-span chart aggregates: `aggToSQLW`.
- **Topology.** `topology_edges_5m.w_calls / w_errors`; service map reads
  them per row with a calls/errors fallback.
- **Baselines.** `BaselineFloor` (metric baseline, anomaly history), the
  seasonal baseline waits for `minDays` of weighted history, comparisons
  straddling the switch read stored values on both sides
  (`weighSQLPeriods`).
- **UI.** `/api/sampling/correction` + `SamplingCorrectionBadge` on
  Services, Service, Endpoints.
- **Measured cost (2026-11-21).** The 15 MV SELECTs, unweighted vs
  weighted, over 1 M synthetic spans (CH 26.3 `clickhouse local`, one
  thread, median of 5): 3.18 s → 3.76 s, **+18 % MV aggregation CPU**
  (+0.58 µs/span). At 27 k stored spans/s that is ≈ +0.016 core
  cluster-wide. Storage (OPTIMIZE FINAL): the 5-minute summaries ≈ ×2
  (the duplicate weighted TDigest dominates), spanmetrics +28 % on this
  high-cardinality sample. Prod check: `system.parts` bytes of the
  weighted MVs a day after the switch.
- **Left for later:** trace_summary_* / trace_service_index_5m /
  business_code_summary_5m states, rollup_* migrations (operator SQL) and
  their readers, raw-spans RED paths (env-scoped /api/services, GetServices
  raw), SLO good/total, service_slowdown (bind `?` in conditions),
  remaining ~40 lower-traffic MV readers.

## Rule thresholds (v0.10.2441)

With mode = on every reader below sees represented values; a static
threshold tuned on stored (inflated) error rates stops firing. Inventory at
v0.10.2440:

| Rule / detector | Read (weighted when on?) | Threshold | Kind | Under weighting |
|---|---|---|---|---|
| Alert rule `error_rate` (window ≥ 5 m, span path) | `MeasureAllServices` spanmetrics_1m, entry spans — **yes** | operator value (builtins 15 / 50 / 5 %, off by default) | absolute | drops ~20×: retune to `ErrorPct(T)` |
| Alert rule `mq_publish_rate` / `mq_consume_rate` (≥ 5 m) | spanmetrics_1m error_state — **yes** | operator | absolute | same as error_rate |
| Alert rule `http_5xx/4xx_rate`, `http_/db_/rpc_*_rate` | raw spans GROUP BY — **no** (aggToSQLW not used here) | operator | absolute | unchanged (still stored rate) — inconsistent with error_rate, known gap |
| Alert rule, any metric, window < 5 m | raw spans — **no** | operator | absolute | unchanged |
| Alert rule `request_rate` | service_summary_5m count — **yes** | operator | absolute | ×≈ okWeight (20): `<` drop rules stop firing |
| Alert rule `error_count` | service_summary_5m error count — **yes** | operator | absolute | ×errWeight (1 in prod shape): ≈ unchanged |
| Alert rule `avg_ms` / `p*_ms` | service_summary_5m sum / TDigest — **yes** | operator | absolute | shifts toward the success-path latency (direction per service) |
| Alert `MinSamples` gate | count — **yes** | per rule | absolute | ×≈ 20: more low-volume pairs pass the floor |
| Any alert rule served by VictoriaMetrics (`alert_source.go`) | VM, unsampled | — | — | unaffected |
| Anomaly `anomaly:*:error_rate` z / MAD | service_summary_5m — **yes**, history from `BaselineFloor` | z ≥ 6, FloorPct 10 % | relative | re-baselines at the switch (no history until dwell + minSamples) |
| — its `AbsFloor` | same | 1 % | absolute | binding: a service at true < 1 % never opens (prod 0.33 %) |
| — error ramp `MinRatioPct` / `MinErrorsPerSec` | same | 0.2 % / 1 err/s | absolute | ratio drops ~20×; errors/s unchanged |
| — `MinBaselineRate` volume gate | same | 0.2 req/s | absolute | ×≈ 20: more services eligible |
| Anomaly `p99_ms` / `request_rate` | weighted, `BaselineFloor` | z, AbsFloor 10 ms | relative | re-baselines |
| Seasonal baseline | weighted, waits `minDays` | — | relative | falls back to the recent-history baseline |
| ML impact rule (`svc_error_rate`, response time) | VictoriaMetrics only | Floor 1 %, +50 % / +1 pt | absolute + relative | unaffected |
| ML telemetry volume (`ml_volume.go`) | weighted (both MVs) | spans/request ratio | relative, scale-free | unaffected (ratio) |
| Problem standard impact floors (notify_policy D6) | value of the source Problem | 30 failed / 5 m or ≥ 1 % with ≥ 30 calls | absolute | inherits the source: error % lower, calls higher |
| DB health (`db-health:`), service_slowdown, SLO good/total | not weighted (left for later) | — | — | unchanged |

### Shadow comparison

In shadow the derived weight is **not stored** (shadow writes 1), so the
weighted MV states equal the unweighted ones and a weighted re-read would
show no difference. The shadow instead re-scores the value each rule already
read with a two-class model from the operator's sampling rules
(`SamplingShadowModelFor`): failing spans weigh E (explicit status=error /
traceError rule, else the smallest configured weight), the rest O (largest).
`weighted % = p·E / (p·E + (1−p)·O)`. Scope: alert rules that become weighted
(`RuleShadowEligible`: error_rate + mq_*_rate, window ≥ 5 m, not VM) and the
anomaly error_rate decision (`evaluateAnomaly` re-run on transformed
series). Results: self-obs `sampling.rule_shadow{family, outcome}` and the
blob `sampling.ruleShadow` (per-rule counts, ≤ every 5 min, leader-local
window) → Settings → Pipeline "Rule thresholds under weighting (shadow)".

Cost per evaluator tick: zero ClickHouse reads; one multiply + compare per
eligible (rule, service) pair; the anomaly tick runs one extra pure
`evaluateAnomaly` per span-sourced service for error_rate (~µs each). One
`system_settings` write per 5 minutes while shadow records (it bumps the
settings cache generation once). Mode off / on: nothing runs.

Bias: non-failing spans of failing traces keep weight 1 upstream, the model
gives them O → the estimate sits at or below the true weighted rate, so
"stop firing" is an upper bound. Pairs below MinSamples today are not
scored (under on more pass the floor).

### Retune (no automatic change)

The transform is monotone, so threshold T on stored rates and `ErrorPct(T)`
on weighted rates fire on the same values under the model. Alert rules
carry one threshold; a second "on true rates" field would split every rule
editor, the notify text and the VM path, so no mode-aware threshold is
added. The card lists each enabled rule's retune value; the operator edits
the rules when switching to on. Prod shape (E = 1, O = 20):

| Stored threshold | Weighted equivalent |
|---|---|
| 1 % | 0.05 % |
| 2 % | 0.10 % |
| 5 % | 0.26 % |
| 10 % | 0.55 % |
| 15 % | 0.87 % |
| 25 % | 1.64 % |
| 50 % | 4.76 % |

`request_rate` thresholds: × O (upper bound). Anomaly `AbsFloor` 1 % on true
rates is equivalent to ~17 % stored; recommended `anomaly_sensitivity`
error_rate AbsFloor for mode on: 0.05 (≈ today's 1 % stored), ErrorRamp
MinRatioPct 0.01 (its lower clamp; today 0.2 % stored ≈ 0.01 % weighted).
