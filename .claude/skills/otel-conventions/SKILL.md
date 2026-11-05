---
name: otel-conventions
description: Coremetry OTel rules + OTLP→CH converter checklist (ex /otlp-converter). WHEN: before changing internal/otlp/ (convert.go, semconv.go, receivers) or any OTel-mapped field. WHEN NOT: SDK/collector setup, CH schema (/clickhouse-schema).
---

# /otel-conventions — Coremetry OTel conventions

Coremetry is OTel-native: the source of truth for traces, logs,
metrics, and profiles is the OTLP wire format. Every internal
table column maps back to an OTel attribute or resource
attribute. When ingest, query, or UX gets a convention wrong
(propagator mismatch, missing service.name, percentile
nomenclature drift), the entire downstream story breaks.

This skill captures the OTel rules Coremetry agrees with and the
small set it deviates from (always documented with a reason).

**Read this before any change that touches OTel-shaped data.**

## When to use

- Adding or modifying `internal/otlp/*.go` (ingest path)
- Adding a new receiver / converter (`internal/otlp/convert.go`)
- Touching span / log / metric field names in `internal/chstore/`
- Writing a frontend picker / chart query that reads OTel attrs
- Debugging "my Python service doesn't show up"
- Debugging "spans don't chain across services"
- Adding a new alert rule shape that reads span attributes
- Reviewing operator-reported "wrong metric name" / "wrong unit"
- Adding new MCP tool / prompt that exposes OTel data

## Steps

### 1. W3C Trace Context — the ONLY propagator

Coremetry's `OTEL_PROPAGATORS` policy across the bundled demos
and recommended SDK configs:

```
OTEL_PROPAGATORS=tracecontext,baggage
```

**Why explicit-only:**

- W3C Trace Context (`traceparent` / `tracestate` headers) is
  the default in OTel SDKs ≥ 1.0 — but mixing in B3 / Jaeger
  across services breaks span chaining silently (one service
  emits B3, the next reads only tracecontext → trace splits
  into orphans).
- Baggage carries user-defined context across services (request
  id, deploy env). NEVER strip the propagator if any service
  uses baggage.

**Anti-patterns:**

- B3 (`b3` or `b3multi`) — common in legacy Spring Cloud
  Sleuth / Brave installs. If a bank's stack uses these, the
  fix is on the SDK side (switch to tracecontext); don't add
  B3 receivers to Coremetry.
- Jaeger (`jaeger`) — Uber's pre-OTel format. Same story:
  migrate the SDK, don't accept the format in ingest.

If an operator reports orphan spans / broken trace chains,
**first check `OTEL_PROPAGATORS` on every service in the chain**.
One mismatch breaks everything downstream.

### 2. The "critical 5" resource attributes

Every ingested signal MUST carry these on the Resource (set
once per process, NOT per span / log / point):

| Attribute | Source | Coremetry usage |
|---|---|---|
| `service.name` | SDK config (`OTEL_SERVICE_NAME`) | Primary partitioning key on every table |
| `deployment.environment` (or `resource.deployment.environment`) | SDK config | Picker filter; problem priority hint |
| `service.version` | SDK config | Deploy markers; before/after-release diff |
| `host.name` (or `resource.host.name`) | OTel host detector | Infra correlation; CPU / mem pivots |
| `service.instance.id` | OTel resource detector | Per-replica drill-down |

**When operator reports "service doesn't show up":**

1. Is `service.name` set? (Often the SDK didn't pick up
   `OTEL_SERVICE_NAME` — defaults to `unknown_service:<binary>`.)
2. Is the OTLP endpoint pointing at Coremetry's gRPC or HTTP?
   (Default gRPC 4317, HTTP 4318. Coremetry exposes both.)
3. Is there a recent span / log / point in the relevant table?
   `SELECT count() FROM spans WHERE service_name=? AND time>=now()-INTERVAL 5 MINUTE`.

Coremetry's coalesce chain for k8s/openshift cluster (resource
attrs vary by SDK):

```
coalesce(k8s.cluster.name, openshift.cluster.name, cluster, '')
```

When adding a new attribute that has multiple SDK-emitted names,
use the same coalesce pattern at read time — don't enforce one
name at ingest.

### 3. Semantic conventions Coremetry honors

Coremetry's internal column names map to OTel semconv. **Don't
rename them** — picker/chart queries depend on the stability.

| OTel attribute | Coremetry column | Notes |
|---|---|---|
| `http.method` | `http_method` (LowCardinality) | server + client kind |
| `http.route` | `http_route` (LowCardinality) | parameterised path |
| `http.status_code` | `http_status` (UInt16) | numeric, not string |
| `db.system` | `db_system` (LowCardinality) | postgresql / mysql / mongodb |
| `db.statement` | `db_statement` (String, ZSTD) | full text |
| `rpc.system` | `rpc_system` | grpc / connect / dubbo |
| `rpc.method` | `rpc_method` (LowCardinality) | |
| `messaging.system` | `msg_system` (LowCardinality) | kafka / rabbit / sqs |
| `peer.service` | `peer_service` (LowCardinality) | for topology edges |
| `gen_ai.operation.name` | (not stored yet) | LLM observability — see step 8 |

**When OTel adds a new semconv attribute** (e.g.,
`http.request.body.size`, `process.runtime.name`), and operators
ask for filtering on it:

1. Check whether it already appears in `attr_keys/attr_values`
   (Array(LowCardinality(String))) — it might be there as
   key/value pair already.
2. If filter cardinality is high (>1k distinct values per
   service), add a typed column. Otherwise, leave it in
   attr_keys + use the FilterBuilder's "any attribute" search.

**Don't promote every new semconv attribute to a column.**
Schema churn at billion-row scale is expensive (column adds on
high-volume tables need the two-boot distributed contract —
/clickhouse-schema §3, §9).

### 4. OTLP gRPC vs HTTP — both, no preference

Coremetry accepts both:

- **OTLP/gRPC** on 4317 (`internal/otlp/StartGRPC`) — primary
  for SDK fleet ingestion. Lower overhead per message.
- **OTLP/HTTP** on 8088 (mux'd into the API server) at
  `/v1/traces`, `/v1/logs`, `/v1/metrics`, `/v1/profiles`.
  Easier for browser-side / Lambda / serverless that can't
  do long-lived gRPC streams.

**SDK config recommendations** (in docs / java-demo /
go-demo / jboss-demo):

```
OTEL_EXPORTER_OTLP_ENDPOINT=http://coremetry:8088     # for HTTP
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
# OR
OTEL_EXPORTER_OTLP_ENDPOINT=http://coremetry:4317     # for gRPC
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
```

The Collector forwards either to the right Coremetry port.
Reading `coremetry-otel-collector:14317` vs direct varies by
deploy topology — both work.

**Don't add a third protocol.** Jaeger UDP, Zipkin HTTP,
OpenTracing wire format — all out of scope. Migrate the SDK.

### 5. Span duration + percentile naming

Internal:
- `duration` = nanoseconds (Int64, T64 + ZSTD codec)
- `duration_ms` = float64 milliseconds (computed at read time:
  `duration / 1e6`)

UI / aggregate APIs expose milliseconds. Operators think in ms;
nanoseconds in the API would force every dashboard panel to
divide manually.

**Percentile naming** (locked across MVs + alert rules + UI):

| Code | UI label | Description |
|---|---|---|
| `p50_ms` | "P50" | median latency |
| `p95_ms` | "P95" | tail latency |
| `p99_ms` | "P99" | high-tail (default for SLOs) |
| `avg_ms` | "Average" | mean — affected by outliers; rarely useful at scale |

**Don't add `p999_ms` or `p9999_ms` as new MV columns.** Beyond
p99, the MV quantile state (`quantilesTDigestState`) inflates state size sharply and
the precision drops. If a SLO needs p999, compute it on raw spans
with a bounded window.

### 6. Sampling — none in the binary

Coremetry stores 100% of the spans it receives; in-binary head/tail
sampling was removed in v0.8.73 (docs/DECISIONS.md, "Ingest / OTLP").
When an operator wants sampling, it is configured in the OTel
Collector in front of Coremetry (e.g. the `tailsampling` processor).
Don't add a sampler, keep-rule engine or sampling setting to the
ingest path. Drop / enrich rules belong to the ingest pipeline
(`internal/pipeline`, `/api/admin/pipeline-rules`).

### 7. EDOT vs raw OTel SDK — both acceptable

Coremetry doesn't push a particular SDK distribution. Operators
can use:

- **Elastic Distribution of OTel (EDOT)** — zero-code agent
  attachment (Java/Python/.NET). Good for "give me traces with
  no app change" requests.
- **Raw OTel SDK** — language-native SDKs from
  `opentelemetry.io`. Standard. Full control over exporters /
  processors / samplers.

Both speak OTLP, both work. Coremetry's docs show EDOT for the
zero-code path AND raw OTel for control. Don't force operators
into one.

**When debugging "trace from EDOT looks different from raw":**

- EDOT may set additional resource attributes (`agent.name=
  elastic-otel-javaagent`, `elastic.distro.version=...`).
  Filter them out in the picker if they create noise; don't
  reject the data.
- Both should produce the same span shape (kind, name, duration,
  attributes). If they differ, the SDK has a bug, not Coremetry.

### 8. gen_ai.* semantic conventions — LLM observability ready

OTel ships a draft semantic convention for AI/LLM
instrumentation:

| Attribute | Meaning |
|---|---|
| `gen_ai.operation.name` | "chat", "completion", "embedding" |
| `gen_ai.system` | "openai", "anthropic", "vertex_ai" |
| `gen_ai.request.model` | model id (gpt-4, claude-opus) |
| `gen_ai.usage.input_tokens` | prompt token count |
| `gen_ai.usage.output_tokens` | completion token count |

**Coremetry's current stance:** ingested as generic span attrs
(land in `attr_keys/attr_values`). The Copilot wrapper at
`internal/api/ai_observability.go::copilotExplain` writes its
OWN `ai_calls` row that captures the same info — internally
typed, not from OTel.

**When an SDK starts emitting gen_ai.* on spans:**

1. The data is already queryable via FilterBuilder ("any
   attribute" → `gen_ai.request.model = ?`).
2. If operators ask for LLM-specific dashboards, the gen_ai
   attrs are first-class — don't ingest into a separate path.
3. Cost / token aggregation is NOT in the OTel draft yet
   (Coremetry's `ai_calls` extends it). Watch the spec; align
   when it stabilises.

### 9. Coremetry-specific deviations (documented)

These are intentional differences from the OTel standard.
Document them clearly when surfaced:

1. **Span events compressed inline.** OTel spec says events
   are first-class on a span (each with timestamp + attrs).
   Coremetry stores them as a JSON-encoded `events String`
   column to avoid a separate spans_events table. Reads use
   JSONExtract*; at scale the inline storage wins on partition
   pruning + scan cost.
2. **Resource + span attributes merged in queries.** The OTel
   spec maintains them as separate scopes. Coremetry's
   FilterBuilder treats them uniformly (filter on
   `resource.k8s.pod.name` AND `http.method` in the same
   filter list). Operators want this; the spec is too pedantic
   here.
3. **Profile pprof endpoint** at `/v1/profiles` — OTel
   profiling spec is still experimental. Coremetry accepts
   raw pprof + a thin metadata wrapper. When the spec
   stabilises, Coremetry will pivot.
4. **Logs `body` is a String, not the OTel-spec'd AnyValue.**
   The spec allows structured log bodies (maps, arrays).
   Coremetry stores the stringified form + uses attr_keys /
   attr_values for the structured portion. Saves a recursive
   AnyValue serdes hot path at billion-log scale.

### 10. Diagnostic flow for "OTel data looks wrong"

When operator reports any of:
- "service doesn't show up"
- "spans don't chain across services"
- "metric name has weird suffix"
- "log timestamps look off"

Run this checklist in order:

1. **Is the SDK configured?** `OTEL_SERVICE_NAME`,
   `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL`.
2. **Is the propagator consistent?** Same `OTEL_PROPAGATORS`
   value across every service in the chain.
3. **Is the OTLP receiver healthy?** `curl http://coremetry:
   8088/v1/traces -X POST -d '{}'` should 415 (wrong
   content-type) not 404 / 500.
4. **Are spans landing in CH?** `SELECT count() FROM spans
   WHERE service_name=? AND time>=now()-INTERVAL 5 MINUTE`.
5. **Is ingest dropping them?** `/api/health` → `spans_dropped`
   counts spans refused because the ingest queue was full (item
   count or byte budget — back-pressure), not a sampling
   decision. Coremetry does not sample; if spans are missing by
   policy, check the Collector's sampling config.
6. **Is the pipeline (v0.5.263) filtering them?** Check
   /admin/pipeline rules — a "drop spans where service=X" rule
   could silently kill ingest.

## Anti-patterns

- **Adding B3 / Jaeger / Zipkin receivers.** Coremetry is
  OTLP-only by design. Migrating SDKs is the right answer.
- **Renaming a typed column to "match OTel name better".**
  Picker queries + frontend types + saved views depend on the
  current names. Don't churn.
- **Promoting every new OTel attribute to a typed column.**
  attr_keys / attr_values handles it without schema migration.
- **Adding `OTEL_TRACES_EXPORTER=otlp` env to docs.** Modern
  OTel SDKs default to OTLP — explicit setting is noise and
  becomes wrong when the SDK switches default exporters.
- **Ignoring `OTEL_PROPAGATORS=tracecontext,baggage` in new
  demos / docs.** The default depends on the SDK version; pin
  it explicitly so the demo trace always chains.
- **Storing `gen_ai.*` in a parallel side-channel table.** The
  attrs are already on the span; query through the existing
  spans table. Side-channel = drift between Copilot view and
  Trace view.
- **Forcing EDOT or raw SDK.** Banks have legacy + greenfield
  services. Coremetry must accept both.

## Hard-constraint reminders

- **OTLP is the source of truth.** Spans, logs, metrics enter
  via OTLP/gRPC + OTLP/HTTP. No proprietary ingest path.
  (CLAUDE.md invariant #1.)
- **Resource + span attributes kept verbatim.** Operators
  query them later via FilterBuilder. Don't strip "useless"
  ones at ingest.
- **Cluster filter coalesce chain** for k8s/openshift —
  `coalesce(k8s.cluster.name, openshift.cluster.name,
  cluster, '')`. Same shape for any future multi-name attr.

## Historical incidents — read before guessing

- **v0.5.471** — added `cluster` filter to logs path because
  k8s.cluster.name / openshift.cluster.name / cluster were
  resolving inconsistently across SDKs. Coalesce chain pattern
  was set here; copy it for any new multi-name attr.
- **v0.5.263** — ingest-time pipeline engine added (drop /
  enrich rules). When debugging "spans missing", check pipeline
  rules first.
- **v0.5.208** — Tempo external trace backend as a fallback.
  `/trace/{id}` resolves CH first, Tempo second. Any
  sample-to-Coremetry / 100%-to-Tempo split is done in the
  Collector, not in the binary.
- **v0.5.244** — Drain templater is sample-based on purpose;
  full-scan templating at billion-log scale is unworkable.
  Don't re-derive without reading the v0.5.244 commit msg.
- **v0.5.346** — async_insert tuning to current values for
  ingest. Don't churn.

## Don't

- **Don't add a non-OTLP ingest path.** OTLP gRPC + HTTP only.
  Use the OTel Collector for protocol bridging if needed.
- **Don't strip resource attributes at ingest.** They're the
  operator's filter affordance. attr_keys/attr_values handles
  the open-ended ones; typed columns hold the named ones.
- **Don't add an OTel attribute as a typed column without
  checking cardinality.** > 10k distinct values per service =
  attr_keys/attr_values lane.
- **Don't break propagator consistency in docs.** Every demo /
  example SDK setup MUST include `OTEL_PROPAGATORS=
  tracecontext,baggage`.
- **Don't follow the OTel spec literally when it costs
  scale.** The four documented deviations (inline span events,
  merged attribute scopes, stringified log body, pprof
  acceptance) exist for a reason. Re-deriving them in the
  spec's direction = re-introducing the cost.

## Dönüştürücü kontrol listesi (eski /otlp-converter)

> 2026-11-05: `/otlp-converter` bu skill'e katıldı. Kod yorumlarındaki
> `/otlp-converter §N` atıfları aşağıdaki numaralı bölümlere karşılık gelir.

Tek giriş noktaları (hepsi `convert.go`): `ConvertTraces` + `convertSpan`
+ `appendSpanLinks` · `ConvertLogs` · `ConvertMetrics` + `convertMetric` +
`appendExemplars`.

> **`make audit`'in OTLP ile ilgili TEK CHECK'i yok** (doğrulandı: 0
> eşleşme). Bu alanda otomatik koruma sıfır — bu liste onun yerine geçiyor.

### 1. Alan haritası

#### Span (16 proto alanı): 7 tam · 4 kısmi · **5 kayıp**

| OTLP | CH | Durum |
|---|---|---|
| `trace_id`, `span_id`, `parent_span_id`, `name`, `status` | aynı adlı kolonlar | TAM |
| `kind` | `kind` | TAM ama **daraltma: 6 girdi → 5 çıktı** (`UNSPECIFIED` + `INTERNAL` → ikisi de `internal`) |
| `start_time_unix_nano` | `time` DateTime64(9) | TAM — **`0` gelirse 1970 partition, fallback YOK** |
| `end_time_unix_nano` | yalnız `duration` | KISMİ; negatif sessizce 0'a kelepçelenir |
| `attributes` | `attr_keys`/`attr_values` + 11 tipli kolon | KISMİ (tip düzleştirme) |
| `events` | `spans.events` JSON blob | KISMİ (map daraltması) |
| `links` | `span_links` tablosu | KISMİ (6 alandan 3'ü) |
| **`trace_state`** | — | **KAYIP** (0 referans, doğrulandı) |
| **`flags`** | — | **KAYIP** — W3C `SAMPLED` biti + `PARENT_IS_REMOTE` |
| **`dropped_attributes_count`** | — | **KAYIP** |
| **`dropped_events_count`** | — | **KAYIP** |
| **`dropped_links_count`** | — | **KAYIP** |

**Hata kuralı TEK ve KATI:** `status_code = 'error'`. **HTTP 5xx tek başına
hata değildir** — `http_status >= 500` yalnız alarm değerlendiricisinin
ayrı filtresi. Bunu değiştirmek 8+ okuma yolunu ve iki MV'yi etkiler.

#### Resource / Scope

`Resource.Attributes` **tümü verbatim** → `res_keys`/`res_values`.
`service.name` (yoksa `"unknown"`), `host.name`, `deployment.environment`
(çift-adlı zincir, v0.8.379), `Scope.Name` → kendi kolonlarına.
**KAYIP:** `Scope.Version`, `Scope.Attributes`, `Resource.entity_refs`,
her iki `schema_url`, tüm `dropped_*_count`.

#### Span Event → `spans.events` JSON blob'u, ayrı tabloya GİTMİYOR

`ConvertLogs` span event'lerine hiç bakmaz; event'ler `logs` tablosuna
girmez. Attribute'lar `map[string]string`'e düşürülür → **sıra kaybolur,
yinelenen anahtar çakışır (son yazan kazanır)**.
`dropped_attributes_count` kayıp. Boş event `""` yazılır, `"[]"` değil
(bilinçli alloc tasarrufu; okuma korumalı).

**Exception ayıklaması Go'da DEĞİL, CH MATERIALIZED kolonlarında**
(`ex_match`/`ex_type`/`ex_msg`/`ex_stack`; ilk exception event'i konumdan
bağımsız `arrayFirst` ile, v0.8.563). İkinci besleme yolu: exception
event'siz ama `error.type` attr'lı hata span'leri (v0.8.494).
`cluster_name` unset dış Distributed'da ALTER atlanır → okuma ifade yoluna
düşer: davranış korunur, **yavaşlar**.

#### Span Link (6 alan): 3 taşınıyor, 3 kayıp

`trace_id`/`span_id`/`attributes` taşınıyor; **`trace_state`, `flags`,
`dropped_attributes_count` kayıp**. Link'in kendi zamanı OTLP'de yok →
`Time` = **sahip span'in başlangıç zamanı**.

> **Bu, kod tabanındaki en DÜRÜST dönüşüm hattı:** geçerlilik kapısı +
> `spanLinksIngested`/`spanLinksDroppedInvalid` sayaçları + `/admin/stats`
> + testler. Diğer bütün kayıplar sessiz. §3'teki politika bu hattı
> standart ilan ediyor.

#### Metrik: 5/5 tip işleniyor

Gauge · Sum · Histogram · ExponentialHistogram · Summary — hiçbiri komple
düşmüyor. Ama `switch d := m.Data.(type)` **`default` dalsız**
(doğrulandı) → `m.Data == nil` sessizce 0 satır, log yok.

| Alan | Durum |
|---|---|
| `name`, `description`, `unit`, `data`→`instrument` | TAM |
| `Metric.metadata` | **KAYIP** |
| **`ScopeMetrics.scope`** | **KAYIP** — `metric_points`'te `scope_name` kolonu yok; span/log'ta var → **asimetri** |
| `start_time_unix_nano` | yazılıyor, **hiç okunmuyor** |
| `value` | TAM — **int64 > 2^53 hassasiyet kaybı** |
| `min_value` | yazılıyor, **hiç okunmuyor** (`max_value` okunuyor) |
| `bucket_counts`/`bucket_bounds` | yalnız **İKİSİ de doluysa**; aksi hâlde satır avg-only'ye düşer |
| DP `flags` | **KAYIP** (her tipte) |
| `aggregation_temporality`, `is_monotonic` | TAM |

### 2. Kontrol listesi — HER değişiklikte

#### 2.1 Span links
- [ ] `appendSpanLinks` hâlâ `parentID` mi kullanıyor (all-zero → `""`),
      `hexID` değil?
- [ ] `ConvertTraces`'in **ikinci dönüş değeri** HER İKİ taşımada da
      tüketiliyor mu — HTTP **ve** gRPC? (Birini unutmak linkleri sessizce
      yok eder.)
- [ ] Geçerlilik kapısı + iki sayaç yerinde mi, `/admin/stats`'a çıkıyor mu?
- [ ] `Time` hâlâ sahip span'in başlangıç zamanı mı?
- [ ] `go test ./internal/otlp/ -run 'Link'`

#### 2.2 Exemplar
- [ ] **Dört** DP tipinde de `appendExemplars` çağrısı duruyor mu?
      Summary'de OLMAMALI (proto'da alan yok).
- [ ] `series_fingerprint` data point ile exemplar satırlarında **AYNI** mı?
      Farklıysa pivot join sessizce boş döner.
- [ ] `trace_id`/`span_id` `parentID` ile mi normalize ediliyor?
      `spans.trace_id` ile aynı kodlama olmadan join tip uyuşmazlığına düşer.
- [ ] `timeUnixNano == 0` çapası duruyor mu? Kaldırılırsa 1970 satırı.
- [ ] `filtered_attributes` nil → `map[string]string{}` dönüşümü INSERT'te
      duruyor mu? CH Map kolonu nil reddeder.
- [ ] `require_trace_context` (varsayılan **true**) ve cap kapıları hâlâ
      Ingester'da mı — convert'te DEĞİL? (Tek politika, iki taşıma.)

#### 2.3 Attribute yönlendirme
- [ ] **Çift saklama korundu mu?** Terfi kolonu çıkarımı `sp.Attributes`'tan
      **okumalı**, slice'tan eleman **çıkarmamalı**.
- [ ] Okuma üçlüsünün sırası **üç yerde de** aynı mı:
      **terfi kolonu → well-known semconv kolonu → `attr_values[indexOf(...)]`**
      (`filterexpr.go`, `repo.go`, `business_dims.go`).
- [ ] `wellKnown` ile `wellKnownResource` **ayrı** mı? Birleştirilirse
      `resource.http.method` span-attr kolonuna düşer.
- [ ] Yeni tipli kolon: `hasXCol` boot probe + koşullu INSERT +
      `cluster_name` unset'te ALTER atlama (→ `/clickhouse-schema` §9).
- [ ] `spanAppendArgs`/`metricAppendArgs` argüman sırası kolon listesiyle
      **birebir** mi? **Aynı-tipli komşu takası hiçbir testi kırmaz.**
- [ ] `attrsToArrays`'in `cap == len` özelliği bozulmadı mı? Prealloc
      büyütmek pipeline enrich'in append dalını paylaşılan array'e yazdırır.

#### 2.4 Semconv geriye uyum
- [ ] `attrFirst`/`attrIntFirst` semantiği: **ilk BOŞ-OLMAYAN değer kazanır**
      — "anahtar var" ≠ "değer var".
- [ ] Yeni yazım eklendiyse **üç harita birlikte** güncellendi mi:
      `spanAttrAliases` + `wellKnown` + `WellKnownTraceCol`?
- [ ] `go test ./internal/otlp/ -run Semconv` — sözlük üzerinden dönen test
      yeni alias'ı otomatik kapsar.
- [ ] `cmd/demo/semconv_mix.go` listesi `spanAttrAliases` ile eşleşiyor mu?
      (Bugün eşleşiyor ama **bunu çiviyen test YOK** — §7.)
- [ ] **Tip ekseni:** yeni `attrIntFirst` eşlemesi ekleniyorsa string ve
      double varyantı da test edildi mi? Bugünkü testlerin hepsi `kvInt`
      kullanıyor.
- [ ] Frontend + acache katmanı güncellendi mi (`acache.go`,
      `useAttributeKeys.ts`, `ColumnManager.tsx`, `TraceWaterfall.tsx`,
      `Trace.tsx`)? **v0.9.628 bunları atladı.**

#### 2.5 Bilinmeyen alan davranışı
- [ ] Yeni OTLP proto sürümü: yeni `AnyValue` case'i eklendi mi?
      `anyValStr` bilinmeyen case'te `""` döndürüyor — **sessiz**.
- [ ] `convertMetric`'in `switch`'i hâlâ `default` dalsız mı? Yeni metrik
      tipi 0 satır üretir, log yazmaz.
- [ ] `kindStr` yeni bir kind'ı `default → "internal"`e mi düşürüyor?
- [ ] Yeni alan §3 politikasına göre ya taşınır ya **sayaçla** düşürülür.

#### 2.6 Golden test
- [ ] Değişen dönüşüm bir golden fixture'la geliyor mu? Emsal:
      `convert_messaging_golden_test.go` + `testdata/span_messaging_kafka.json`
      (v0.10.553). Yeni sinyal ailesi = yeni fixture + aynı kalıpta test.
- [ ] Golden test **tam-struct** karşılaştırması mı (`want chstore.Span`
      literali)? Alan-alan iddia yetmez — **yeni alan eklendiğinde test
      kırılmak ZORUNDA.**
- [ ] Değişen alan fixture'da kapsanıyor mu?
- [ ] `go test ./internal/otlp/ -cover` — taban **%82.2**, düşmedi mi?

#### 2.7 Kapanış
`go build ./...` · `go test ./...` · `go vet ./...` (protobuf `copylocks`
tuzağı, v0.9.726) · `make audit` (bu alanda koruma **yok**).

### 3. Bilinmeyen alan politikası

**Bugünkü fiili durum:** anahtar düzeyinde politika **doğru** — hiçbir
attribute atılmıyor, allowlist/denylist yok; tam fidelity duruşuyla
tutarlı. **Tip ve alan düzeyinde politika YOK, her şey sessiz:**

| Kategori | Bugün | Sonuç |
|---|---|---|
| Bilinmeyen `AnyValue` tipi | `""` döner | anahtar kalır, değer boşalır — sessiz |
| Bilinmeyen metrik `Data` tipi | `switch` default'suz | metrik tamamen kaybolur — sessiz |
| Bilinmeyen span kind | `→ "internal"` | sessiz daraltma |
| Tanınan ama beklenmedik tip | `attrInt` → 0 | **sessiz + yanıltıcı** |
| Degrade exp histogram | 4 koşulda boş kova | sessiz kalite düşüşü |

> **KURAL — bilinmeyen alan sessizce düşmez.** Her bilinmeyen/desteklenmeyen
> dal üç sınıftan birine düşer ve sınıf kodda **AÇIKÇA** seçilir:
>
> **A. TAŞI** (varsayılan) — değer generic diziye/blob'a girer, tip bilgisi
> kaybolsa bile değer korunur.
> **B. DEGRADE ET + SAY** — değer korunamıyorsa satır yazılır, bir sayaç
> tıklar ve sayaç `/admin/stats`'a çıkar. *(Bugün yalnız exemplar ve span
> link bu sınıfta — örnek alınacak hat budur.)*
> **C. DÜŞÜR + SAY + LOGLA** — satır hiç yazılmıyorsa sayaç **ve**
> rate-limited log şart.
>
> Sessiz `return ""`, sessiz `default:`, sessiz `return nil,nil,false`
> yeni kodda **kabul edilmez**.

### 4. Semconv geçiş yordamı

**Desen A — merkezi alias tablosu** (`internal/otlp/semconv.go`), 4 çift:

| Kolon | Öncelik (yeni → eski) |
|---|---|
| `http_method` | `http.request.method` → `http.method` |
| `http_status` | `http.response.status_code` → `http.status_code` |
| `db_system` | `db.system.name` → `db.system` |
| `db_statement` | `db.query.text` → `db.statement` |

**Desen B — tek-kullanımlık zincirler** (tablo dışında, 3 adet):
`deploy_env`, `http_route` (üç kollu), `messaging.destination` (tipli kolon
yok, SQL coalesce'ı).

**Kapsam dışı ve gerekçeli:** `peer.service` → `server.address` **yapılmaz**
— semantik farklı (mantıksal servis adı vs host), topoloji düğüm adlarını
bozar. Aynı şekilde `url.full`, `client.address`, `db.operation.name`,
`db.namespace` (karşılık gelen kolon yok).

**Yeni geçiş eklerken dokunulacak TAM liste:**
1. `internal/otlp/semconv.go` — `spanAttrAliases` (yeni ad ÖNCE)
2. `internal/chstore/filterexpr.go` — `wellKnown`, aynı kolona
3. `internal/chstore/repo.go` — `WellKnownTraceCol`
4. `cmd/demo/semconv_mix.go` — demo iki yazımı da üretsin
5. Test: `semconv_test.go` sözlük-üzerinden dönen testler otomatik kapsar
6. Frontend: `acache.go`, `useAttributeKeys.ts`, `ColumnManager.tsx`,
   `TraceWaterfall.tsx`, `Trace.tsx`

**Harf-duyarlılık:** attribute anahtarları harf-duyarlı eşleşir. Bu sınıf
prod'u bir kez kırdı (v0.9.626: `CHANNEL_CODE` vs `channel_code`, 11 gün
boş kolon, 10 dk penceresinde küçük harfli 2.67M span / büyük harfli
sıfır). Yeni bir anahtar eklerken **prod'da hangi yazımın geldiğini ÖLÇ** —
varsayma. Teşhis anlatısı `/perf-triage` örnek vakasında.

### 5. Golden test — emsal ve genişletme

Emsal: `internal/otlp/convert_messaging_golden_test.go`
(`TestConvertSpanMessagingGolden`) — `testdata/span_messaging_kafka.json`
(protojson) → `ConvertTraces` → TAM `chstore.Span` karşılaştırması,
yalnız stdlib (`reflect`, `protojson`; go-cmp bağımlılık değil). Yeni
fixture'ı bu dosyanın kalıbıyla ekle. Neden tam struct: yeni bir alan
eklendiğinde test KIRILMAK ZORUNDA; kırılmıyorsa alanın taşındığını kimse
doğrulamamış demektir.

Fixture'ın kapsaması gereken, **bugün test edilmeyen** dallar: span links ·
exemplar (dört DP tipi) · exponential histogram degrade dalları ·
array/kvlist attribute · `start_time == 0` · her semconv çiftinin **eski**
yazımı.

### 6. Sessizce bozulanlar

| Ne | Sonuç | Yakalayan |
|---|---|---|
| `spanAppendArgs` argüman sırası takası (aynı tip) | Kolonlar yer değiştirir, veri "geçerli" görünür | **hiçbiri** |
| İkinci taşımada (gRPC) link tüketimini unutmak | Linkler sessizce yok | **hiçbiri** |
| `series_fingerprint` ayrışması | Exemplar pivot join'i boş | **hiçbiri** |
| Terfi kolonu çıkarımının slice'tan eleman çıkarması | Generic dizi eksilir, filtre sessizce boş | **hiçbiri** |
| Yeni `AnyValue` case'i | Değer boşalır | **hiçbiri** |
| Demo listesi ile alias tablosunun ayrışması | Demo yanlış şeyi doğrular | **hiçbiri** |

### 7. Açık sorular

1. **33 protokol alanı kayıp.** En ucuz düzeltme: `schema_url` +
   `dropped_*_count` saf ek kolon olarak. Dilim mi?
2. **`ScopeMetrics.scope` asimetrisi** — span/log'ta `scope_name` var,
   metrikte yok.
3. **`min_value` ve `start_time` yazılıyor ama hiç okunmuyor** — ölü kolon
   mu, gelecek özellik mi?
4. **`trace_state` + `flags`** — W3C `SAMPLED` biti kayıp olduğu için
   "bu span örneklenmiş miydi" sorusu cevaplanamıyor.
5. **Demo↔alias eşleşmesini çiviyen test yok** — tek satırlık kapı.
