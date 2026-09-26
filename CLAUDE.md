# Coremetry

OpenTelemetry-native APM. Single Go binary + ClickHouse + Redis +
optional external Elasticsearch/Tempo. 1000s of services, 1B+
spans/day, one container. Rubric when in doubt: **"how would
Datadog / Dynatrace / Honeycomb engineer this?"**

## Hard constraints — non-negotiable

| Constraint | Enforced where |
|---|---|
| **Single-binary release** — one image/tag; `COREMETRY_MODE=all\|ingest\|api\|worker` for roles | `main.go`, `Dockerfile` |
| **Picker = server-side search** — never eager `<Combobox options={…}>` | `ServicePicker` / `OperationPicker` / `MetricNamePicker` |
| **Table > 100 rows** — virtualize, server-paginate, or `content-visibility:auto` | every list page |
| **Record-list tables sortable + resizable** — shared primitive, never hand-rolled | `useDataTable` + `DataTable.tsx`; template `SlowQueries.tsx` |
| **Four table kinds only** (table standard, 2026-09-26): record list = `DataTable`; ≤10 fixed unsorted rows or an editable/picker list = static `<table>` with a reason; attributes = `KeyValue`; legends / chat markdown exempt. Rows look clickable only when they are; colour only on the deviating value | `docs/DECISIONS.md` "Tablo standardı"; `tableUnityRatchet` |
| **Cache key hashes ALL inputs** — sorted + FNV; length-only digests cross-poison (v0.5.187) | `internal/api/cache.go` |
| **`timeRangeToNs(range)` only inside `useEffect`/`useMemo`** — bare in JSX = infinite refetch (v0.5.184) | every page using `range` |
| **CH query on `spans`/`metric_points`** — `LIMIT` + `SETTINGS max_execution_time` + time-bounded WHERE | `internal/chstore/*.go` |
| **Admin write = audit entry** — `s.audit(r, "kind.action", "resource", id, details)` | `internal/api/*.go` |
| **No PII redaction features** — operator preference: full fidelity | memory `feedback-no-redaction.md` |

## Performance budgets

- `/api/*` p99 < 200ms warm, < 1s cold; hot endpoints (`/api/services`, `/api/problems`, `/api/health`) < 50ms warm
- `/api/spans/heatmap` < 3s ≤6h window (auto-sample beyond); `/api/logs/patterns` < 2s at billion docs
- TTFI < 1.5s fresh tab; polling ≥ 10s (except `/api/health` 5s); every poller pauses on `document.hidden`

## Tech stack

**Backend:** Go 1.22+, ClickHouse 24+, Redis 7+. `main.go` wires
`chstore.Store`, `logstore.Store` (CH or ES via
`COREMETRY_LOGS_BACKEND`), `tempo.Service`, `cache.Cache`,
`auth.Service`, `notify.Notifier`, `copilot.Service`.
**Frontend:** React + Vite, no Tailwind (CSS vars in `globals.css`),
React Query, react-router v6. No state library — URL is source of
truth for shareable views.
**Versioning:** git tags `v0.10.X`; 1.0 is deferred by operator decision (cut procedure + smoke checklist when it comes: [docs/RELEASE-1.0.md](docs/RELEASE-1.0.md)). Runtime resolution:
ldflag > `/app/VERSION` > `"dev"` = **build** kimliği; `COREMETRY_VERSION`
env yalnız GÖSTERİLENİ değiştirir. `/api/version` ikisini de döndürür +
`overridden` bayrağı, böylece bayat bir env imaj kimliğinin yerine
geçemez (olay: docs/INCIDENTS.md, v0.5.394).

## Architectural invariants

1. **OTel is the source of truth** — OTLP/gRPC + HTTP only; attributes kept verbatim.
2. **ClickHouse is the warm store** — ES is optional *read* backend for logs only.
3. **MV-first reads** — every aggregate reads its pre-aggregate (e.g. `service_summary_5m`, `operation_summary_5m`, `trace_summary_5m`, `topology_edges_5m`; span MVs: `canonicalMVs()` in `internal/chstore/store.go`, topology aggregates: `internal/topology/`, rollup families: `migrations/`; which one to use: `/clickhouse-schema`). Raw `spans` for an aggregate = bug.
4. **`ReplacingMergeTree(version)` for state**, reads use `FINAL`. Whole-row replace: carry ALL fields forward.
5. **One CH table for saved state** — `saved_views(page='<kind>', …)`; no new schema per surface.
6. **Settings live in `system_settings`** — JSON blob per key; `LoadPersisted` at boot + `SavePersisted` on admin PUT (template: `internal/tempo/client.go`).
7. **Roles admin / editor / viewer** — `auth.RequireRole` / `RequireAnyRole`; viewer SEES state read-only, never blank.

## Ship checklist — every new operator-facing surface

1. Handler hits the right MV; 2. `s.serveCached` with hash-all-inputs
key; 3. auth gate + 4. audit entry if it writes; 5. settings via
`system_settings` if configurable; 6. type in `lib/types.ts`;
7. client method in `lib/api.ts`; 8. loading/error/empty states
(`<Spinner/>`, `<Empty/>`); 9. fe gate: `npx tsc --noEmit` +
`npx eslint src` + `TZ=UTC npx vitest run`; 10. `go build ./...` +
`go vet ./...` gate; 11. bug-fix release = regression test
(pure-function, table-driven, header cites the vX.Y.Z; canonical:
`internal/api/cache_key_test.go`) + `go test ./...`.

AI explain affordances route through `s.copilotExplain(r, ...)`,
never `s.copilot.Explain` direct (/ai attribution).

## Code patterns

- **Cache key for a set:** `fmt.Sprintf("…:%x", fnvDigest(sortedSlice(set)))` — never `n=%d len(set)`.
- **CH bounds:** every spans/metric_points query has time-bounded WHERE + `LIMIT` + `SETTINGS max_execution_time`.
- **Tables:** `useDataTable` (`storageKey`, COLS with `sortValue`/`numeric`) + `<DataTableColgroup/>` + `<DataTableHead/>`; server-paged tables adopt resize half only. Rows > 100: `contentVisibility: 'auto'`.
- **Buttons/fields — one design language:** `ui/` atoms only (`<Button variant size>` in `components/ui/Button.tsx`, `IconButton`, `SegmentedControl`, `TabStrip`, …), `Field.tsx` for labelled inputs, `.badge .b-ok/.b-err`. Raw `<button>` outside `ui/` is blocked (`buttonUnityRatchet` + ESLint `ui/no-raw-button`).
- **Secrets in Settings:** never echo back; "stored" indicator; empty input preserves stored value.
- **Logstore plurality:** detectors go through `logstore.Store.CountPatterns(...)` batched (ES `_msearch` / CH tokenbf) — never call ES directly from a detector.
- **Triage:** P1 = now (critical + ayarlanabilir eşik katı (varsayılan 2×) / tamamen kayıp / ayarlanabilir açık-saat (varsayılan 4h) / exception fırtınası (≥N servis aynı pencerede yeni grup; vida: `exception_triage`); vida: `problem_priority` blobu), P2 = today, P3 = convenient. Exception tarafında kazanılmış P1 zamanla düşmez (operatör direktifi): patlama ya da hacim eşiğini (`p1MinOccurrences`) aşmış her grup resolve/ignore edilene dek P1 kalır; `regressed` dalı P2 (operatör onaylı). Deploy bilgisi görünür ama önceliğe karışmaz — `fresh deploy` tetikleyicisi yok, geri ekleme. Reason string ships with every Problem. Ayrıntı ve tarihçe: `/aiops`.

## Workflow

- `kuyruk` = show prioritised queue, end with "Hangisi?". `devam` = continue current item.
- Operator-reported bugs = NEW priority, ship as `v0.10.X+1` immediately, never batch.

**Release — every functional change:**
```
edit → (fe) npx tsc --noEmit && npx eslint src && TZ=UTC npx vitest run
→ go build ./... && go vet ./... → go test ./... → make audit
→ git add <files> → commit (heredoc) → tag v0.10.X
→ git push && git push --tags → deploy (background, ONE at a time)
```
`make audit`: 🔴 critical blocks the tag; 🟡 warnings reviewed, ship
if known false positive.

**Commit format:** `v0.10.X — title (≤70)` + body (what/why/root
cause, 72 cols, operator bugs start "Operator-reported: …") +
`Co-Authored-By: Claude <noreply@anthropic.com>`.

**Skills (`.claude/skills/`):** `/release`, `/bugfix`, `/spec` (3+
file changes), `/kuyruk`, `/scale-audit` (quarterly),
`/clickhouse-schema` (BEFORE any CH table/query/MV change),
`/helm-chart-coremetry` (BEFORE `charts/coremetry/` changes),
`/otel-conventions` (BEFORE OTel-shaped data changes), `/api-route`
(BEFORE adding/moving any `/api/*` route — new endpoints get their own
file, never api.go), `/mcp-tools`
(BEFORE MCP server changes), `/frontend-dashboard-panel` (BEFORE new
dashboard panel types), `/frontend-conventions` (BEFORE any frontend
change adding a component/table/filter/drawer/theme/polling loop),
`/tdd` (test-first loop for new backend features + pure helpers),
`/perf-triage` (ONE "şu sayfa yavaş" complaint → measured root cause;
whole-repo sweeps stay with `/scale-audit`),
`/frontend-design-system` (BEFORE writing any UI part — search for the
existing primitive first; the `ui/` barrel is incomplete),
`/otlp-converter` (BEFORE touching `internal/otlp/` — field map +
what silently drops + golden-test obligation),
`/aiops` (BEFORE adding/changing a detector, rule-id prefix, priority or
escalation rule, incident attach, hypothesis score, exception ladder step
or any loop writing problems/anomaly_events/root_cause_hypotheses),
`/copilot-surface` (BEFORE adding a new AI ✨ Explain affordance),
`/review-changes` (pre-commit review of the working-tree diff; before
`/release` on a non-trivial change), `/where-is` ("X nerede" — fuzzy code
location, returns file:line pointers).

## Frontend UI conventions

Full rules in `/frontend-conventions`. The one-liners:

- One design language: `ui/` atoms only (`Button`, `IconButton`,
  `Field`, `Badge`, `TabStrip`, …); raw `<button>` outside `ui/` is
  blocked (buttonUnityRatchet + ESLint `ui/no-raw-button`).
- Record lists = `useDataTable` (sort+resize, persisted widths);
  server-paged tables resize-only; >100 rows `content-visibility`.
  Other table kinds: the four-kinds row under Hard constraints.
- Pickers server-debounced; never validate a pick against a sampled
  subset (v0.8.265).
- URL = source of truth for every selection (drawer/focus/filters/
  tab/range) — write with replace:true; sig-guard URL→state imports
  (v0.8.253); one-way-read is a recurring bug class (256/265/267).
- Themes are token-level CSS vars only (dark/light/redhat v0.8.268);
  chrome variants via scoped token remap; charts re-resolve on
  data-theme; uPlot only.
- ES-cost UI discipline: fetch on expand/open only, no list
  prefetch, staleTime ≥ server TTL, cache-key params snapped to
  bounded rungs (v0.8.270).

## Pitfall rules — full incident stories in [docs/INCIDENTS.md](docs/INCIDENTS.md)

- `timeRangeToNs` bare in JSX → infinite refetch; memo it.
- Cache key from `len(set)` → cross-poisoning; stable digest.
- `table-layout:fixed` + `nowrap` + small width silently clips; use min/max-width + ellipsis + title.
- ES: no `case_insensitive` on `query_string` (8.x rejects); `_msearch` over per-pattern `_search`; `significant_text` needs `background_filter` + `sampler`.
- Drain templating is sample-based (1000/5min), never full-scan.
- Value+unit templates (Nh/Nd, ms/s): test EVERY unit; never `toDate()` around sub-day math (`retention_test.go`).
- Combined-MV drop: inner table first with `max_table_size_to_drop=0` (`dropCombinedMV`).
- MV state columns use `quantilesTDigestState`, never reservoir `quantilesState`; MV type changes cause a rolling-deploy read-error window — roll fast or dual-column.
- SQL references telemetry tables UNQUALIFIED (no `coremetry.` prefix).
- `toDateTime64(?)` bind args must be tz-less (no trailing `Z`) — `chDateTime64Arg`.
- Coremetry Deployment keeps `maxUnavailable: 0` or the OTel collector wedges on rollout ("zero addresses"); pre-fix: restart collector.

## What goes WHERE

| Domain | Path |
|---|---|
| Demo generators (Go synthetic / JBoss) | `cmd/demo/`, `jboss-demo/` |
| OTLP ingest | `internal/otlp/` |
| CH writes (spans/metrics/logs) | `internal/chstore/` |
| ES log read backend | `internal/logstore/` |
| Tempo trace fallback | `internal/tempo/` |
| HTTP API + cache wrapper | `internal/api/` |
| Alert evaluator / anomaly / templater | `internal/evaluator/`, `internal/anomaly/`, `internal/templater/` |
| Notifications | `internal/notify/` |
| Auth (local + LDAP + OIDC) | `internal/auth/`, `internal/ldap/` |
| Copilot (ALL system prompts: `internal/copilot/prompts.go`) | `internal/copilot/` |
| Topology correlator | `internal/topology/` |
| CH schema (app-owned: `store.go` `migrate()`; operator-owned: `migrations/*.sql`) · single-node→cluster data copy | `internal/chstore/`, `migrations/` · `internal/chmigrate/` |
| Frontend pages / components / types+client / routes / sidebar | `frontend/src/pages/`, `components/`, `lib/{types,api}.ts`, `App.tsx`, `components/Sidebar.tsx` |

`lib/types.ts` is the single source of truth for shared data shapes —
never re-declare in components. PascalCase types, camelCase props,
`?:` for `omitempty` fields.

## Demo realism

The demo workloads (Go + JBoss) share one load model (diurnal curve,
incidents, log-normal latency, correlated errors, real histogram
buckets, saturation metrics) — details in
[docs/DEMO-REALISM.md](docs/DEMO-REALISM.md). **Rule:** any new demo
scenario/metric reads from the load model (`L` / `DemoLoad`), never
its own fixed probability or uniform latency.

## Self-observability

`/admin/stats` reads `GetSystemStats(ctx)`; AI usage lands in
`ai_calls` → `/ai`. New ingest path or expensive endpoint = register
a counter.

## Anti-patterns (don't)

- Don't bypass `s.serveCached` on hot reads.
- Don't create new schema for user-saved state (use `saved_views`).
- Don't fetch full catalogues for pickers.
- Don't poll faster than 10s (except `/api/health`) or ignore `document.hidden`.
- Don't add backwards-compat shims when removing a feature.
- Don't propose data redaction features.
- Don't fix typing with `as any`.
- Don't write `// TODO` without a release-tag context — ship it or queue it via `kuyruk`.
- Don't add a metric dimension without bounding its cardinality; `LowCardinality(String)` only for measured low-distinct columns — never IDs or free text (`/clickhouse-schema` C2–C3).

Decision log (architectural calls): [docs/DECISIONS.md](docs/DECISIONS.md).

# Coremetry Development Rules

## Backend (Go)
- Veri yazma işlemlerinde ClickHouse Async Insert (`async_insert=1`) mekanizmasını bozma.
- Her yeni API endpoint'i kendi `internal/api/<domain>.go` dosyasında `registerXxxRoutes(mux)` metoduyla yazılır ve `route_registry.go` defterine `init()` içinden `registerRoutesExtra` ile kaydolur; AI/copilot uçları `ai_routes.go` `registerAIRoutes` içine, `requireCopilot` ile sarılı. `api.go` hiç büyümez — `TestApiGoDoesNotGrow` (`go test`) ve `scripts/guard-api-go-size.sh` (CI); taban `.claude/baselines/api_go_lines` yalnız aşağı iner (`/api-route`).
- Arka plan işçilerinde lider kilidini (Leader Lock) korumak için mutlaka Redis mutex yapısını kullan.

## Frontend (TypeScript/React)
- Grafik çizimleri için kesinlikle Chart.js veya ağır kütüphaneler ekleme, sadece `uPlot` kullan.
- Canlı güncellemeler tek SSE veriyolundan gelir: `/api/events` bağlantısını uygulama kabuğundaki `useEventStream` (lider sekme + BroadcastChannel) tutar; yeni olay türü `lib/queries/eventInvalidations.ts` haritasına eklenir. Bileşen `/api/events` için kendi `EventSource`'unu açmaz — her pencere bir bağlantı, HTTP/1.1'in ~6 bağlantılık bütçesini tüketir. Olay taşımayan veri ≥10s polling ile gelir.
