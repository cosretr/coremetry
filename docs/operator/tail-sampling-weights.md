# Upstream trace sampling → `spans.sample_weight`

When an OpenTelemetry Collector samples traces **before** they reach
Coremetry, every span-derived number Coremetry shows is computed on the
sample: throughput is too low by the sampling factor and failure rate is
inflated (a "keep every failing trace + 5 % of the rest" pipeline turns a
true 1 % failure rate into roughly 17 %). Metric-sourced RED (Prometheus /
VictoriaMetrics HTTP metrics, spanmetrics computed *before* sampling) is not
affected.

Since v0.10.2430 the OTLP converter can stamp each span with
`spans.sample_weight` — the number of original spans the stored span stands
for (OTel calls it the *adjusted count*). Phase 1 only **stores** it; weighted
reads are Phase 2 (`docs/architecture/sampling-weight.md`). Nothing on screen
changes yet.

Settings → Pipeline → **Upstream sampling weight** (admin; writes are audited
as `settings.ingest_sampling.update`; system_settings key `ingest.sampling`).

## 1. Where the weight comes from (priority order)

| # | Source | Notes |
|---|---|---|
| 1 | W3C `tracestate` `ot=th:<hex>` | OTel probability sampling threshold. Adjusted count = 2^56 / (2^56 − T), T = `th` right-padded to 14 hex digits. `th:0` → 1. Malformed `th` is ignored. Toggle "Honour tracestate". |
| 2 | Attribute `sampling.weight`, `SampleRate`, `sampleRate` | Span, then scope, then resource. Int, double or numeric string. Toggle "Honour weight attributes". |
| 3 | Operator rules | First match wins. Conditions (all set ones must hold): `attr` (+ `value`) looked up on span → **scope** → resource; `status: "error"` (the span itself); `traceError: true` (a span of the same trace in the same export request is ERROR); `hashPercent` (+ `hashSalt`, `hashBucket: in|out`) — the trace id is inside/outside the tail_sampling probabilistic bucket. No condition = catch-all. |
| 4 | `defaultWeight` | 1 unless set. |

Every weight is clamped to [1, 1 000 000].

Modes: **off** (default — weight 1, no per-span work), **shadow** (derive
and count, store 1), **on** (store the derived weight). Always run shadow
first and read the card: "Derived on this pod" shows the share of spans per
(source/rule, weight) since the pod started — on a split install open it on
an ingest pod or use **Count stored**, which reads the stored weights of the
last 5 minutes across the fleet (needs mode on). The *implied factor* is
represented spans ÷ stored spans; for "errors + 5 %" it should be close to
20 × (non-failing share of stored spans) + (failing share).

## 2. The typical pipeline: every failing trace + N % of the rest

```yaml
processors:
  tail_sampling:
    decision_wait: 10s
    num_traces: 100000
    policies:
      - name: drop-noisy-service          # a `drop` policy wins over everything
        type: drop
        drop:
          drop_sub_policy:
            - name: noisy
              type: string_attribute
              string_attribute: {key: service.name, values: [example-batch]}
      - name: status-errors               # must come BEFORE the probabilistic policy
        type: status_code
        status_code: {status_codes: [ERROR]}
      - name: probabilistic-5
        type: probabilistic
        probabilistic: {sampling_percentage: 5}
```

A trace is kept if **any** policy samples it (OR). So a failing trace is
kept with probability 1 (weight **1**) and a non-failing trace with
probability 0.05 (weight **20**). The problem is telling the two apart on a
single span: most spans of a failing trace are themselves OK.

### Option A (exact) — record the deciding policy

Verified against opentelemetry-collector-contrib **v0.146.0**
(`processor/tailsamplingprocessor/internal/telemetry/featureflag.go`,
`processor.go`):

- Feature gate **`processor.tailsamplingprocessor.recordpolicy`** (stage
  alpha → off by default). When on, every span of a sampled trace gets
  `tailsampling.policy = <policy name>` (plus `tailsampling.composite_policy`
  for composite policies and `tailsampling.cached_decision = true` for spans
  decided from the decision cache — those still carry the policy name).
- The attribute is set on the **scope** attributes
  (`SetAttrOnScopeSpans`), not on span or resource attributes. Coremetry
  rules look at span → scope → resource, so this works; the scope
  attributes themselves are not stored in ClickHouse.
- When several policies sample the same trace, the **first sampling policy
  in configuration order** is recorded (`makeDecision`: "We associate the
  first policy with the sampling decision"). Keep `status-errors` before
  `probabilistic-5`, otherwise a failing trace that also fell into the 5 %
  bucket would be labelled probabilistic and weighted 20.

Enable the gate on the collector that runs `tail_sampling`:

```yaml
# Helm (opentelemetry-collector chart)
args:
  feature-gates: processor.tailsamplingprocessor.recordpolicy
# or on the command line
#   otelcol-contrib --config=/conf/config.yaml \
#     --feature-gates=processor.tailsamplingprocessor.recordpolicy
```

Coremetry rules (preset "collector records policy"):

```json
[
  {"name": "errors-policy", "attr": "tailsampling.policy", "value": "status-errors", "weight": 1},
  {"name": "probabilistic-policy", "attr": "tailsampling.policy", "value": "probabilistic-5", "weight": 20}
]
```

Use your real policy names. Processors after `tail_sampling` that rewrite
span/resource attributes (`attributes`, `resource`) leave scope attributes
alone; a `transform` processor touching `scope.attributes` would not.

### Option B (no collector change) — reproduce the probabilistic hash

The v0.146.0 probabilistic policy (`internal/sampling/probabilistic.go`) is
deterministic: FNV-1a 64 over `hash_salt` bytes followed by the 16 trace-id
bytes, sampled iff the hash ≤ `MaxUint64 × percentage / 100` (computed with
`big.Float`), default salt `default-hash-seed`. Coremetry reproduces it
byte for byte (pinned by `TestTailSamplingHashReproduction`). Then:

- trace **outside** the 5 % bucket but stored ⇒ it was kept only because it
  failed ⇒ weight 1 — exact, for every span of the trace, OK or not;
- trace **inside** the bucket ⇒ 20, unless the trace's failure is visible in
  the same export request (`traceError`) ⇒ 1.

```json
[
  {"name": "failing-trace", "traceError": true, "weight": 1},
  {"name": "error-only", "hashPercent": 5, "hashBucket": "out", "weight": 1},
  {"name": "in-bucket", "hashPercent": 5, "weight": 20}
]
```

Add `"hashSalt": "<value>"` to both hash rules if the collector sets
`hash_salt`. Fragility — the rule is silently wrong when:

- `hash_salt` or `sampling_percentage` differ from the collector's (check
  with shadow mode: the in-bucket share of stored non-failing traces must be
  ~100 %; a mismatch shows as many non-failing spans in `error-only`);
- a future collector version changes the algorithm, or the alpha gate
  `processor.tailsamplingprocessor.usetracestate` is enabled (the policy then
  uses tracestate randomness instead of the hash when present — but also
  writes `ot=th`, which source #1 picks up first);
- an SDK / earlier collector changes the trace id after the decision
  (nothing standard does).

Residual bias: only failing traces **inside** the bucket whose error span
arrives in a different export request than the OK span (≈ 5 % of failing
traces × the split fraction q). Relative throughput over-count ≤
19 × 0.05 × e × (s − k) / s × q — for e = 1 % failing traces, s = 10 spans,
k = 1 error span, q = 1 (worst case): **≤ 0.86 %**.

### Option C (last resort) — status only

```json
[
  {"name": "failing-trace", "traceError": true, "weight": 1},
  {"name": "rest", "weight": 20}
]
```

Bias, N = 20: every OK span of a failing trace whose error is not visible
in the same request gets 20 instead of 1. Relative throughput over-count =
(N − 1) × e × (s − k) / s × q; displayed failure rate is under-stated by
the same factor (1 / (1 + bias)).

| e (true failing traces) | s / k | q = 1 (pure span status) | q = 0.2 (`traceError`, most traces in one request) |
|---|---|---|---|
| 1 % | 10 / 1 | +17.1 % throughput, failure rate 0.085 % instead of 0.1 % | +3.4 % |
| 5 % | 10 / 1 | +85.5 %, failure rate −46 % | +17.1 % |
| 1 % | 30 / 1 | +18.4 % | +3.7 % |

(q = share of OK spans of failing traces not exported together with the
trace's error span; the tail sampler releases a trace's spans together, the
`batch` processor afterwards can split them.) Without `traceError` (plain
`status: "error"` rule) q = 1.

## 3. Pipeline notes

- Put `tail_sampling` behind a trace-id–aware load-balancing exporter (the
  usual agent → `loadbalancing` (routing_key: traceID) → gateway layout) so
  every span of a trace reaches the same sampler; otherwise policies decide
  on partial traces and no weight scheme is correct.
- An OTLP exporter to Coremetry with `retry_on_failure` and `sending_queue`
  disabled drops data on any ingest backpressure — that loss is invisible to
  the weights. Enable both (bounded queue) unless loss is intended.
- The `spanmetrics` connector, when it runs **before** `tail_sampling` in
  the gateway, produces pre-sampling RED. Sending a copy of those metrics to
  Coremetry gives correct throughput/error rate for those services today
  (metric-sourced RED), independent of Phase 2.
- Samplers implementing OTel consistent-probability sampling (the contrib
  `probabilistic_sampler` processor's proportional/equalizing modes, SDK
  samplers that follow the tracestate-probability-sampling spec) write
  `ot=th`; classic ratio samplers do not (not verified per SDK version) —
  shadow mode shows a `tracestate` row when it arrives.
