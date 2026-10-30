# Coremetry

OpenTelemetry-native APM. Single Go binary + ClickHouse + Redis +
optional external Elasticsearch/Tempo. 1000s of services, 1B+
spans/day, one container. Rubric when in doubt: **"how would
Datadog / Dynatrace / Honeycomb engineer this?"**

## Hard constraints — non-negotiable

| Constraint | Enforced where |
|---|---|
| **Single-binary release** — one image/tag; `COREMETRY_MODE=all\|ingest\|api\|worker\|agent\|sre` for roles (`all` ⊃ sre drain; worker only enqueues) | `main.go` `parseRunModeValue`, `Dockerfile` |
| **Picker = server-side search** — never eager `<Combobox options={…}>`; never validate a pick against a sampled subset (v0.8.265) | `ServicePicker` / `OperationPicker` / `MetricNamePicker` |
| **Table > 100 rows** — virtualize, server-paginate, or `content-visibility:auto` | every list page |
| **Four table kinds only** (2026-09-26): record list = `DataTable` + `useDataTable` (sortable + resizable, `storageKey`, COLS with `sortValue`/`numeric`, `<DataTableColgroup/>` + `<DataTableHead/>`; server-paged = resize only; template `SlowQueries.tsx`); ≤10 fixed unsorted rows or editable/picker list = static `<table>` with a reason; attributes = `KeyValue`; legends / chat markdown exempt. Rows look clickable only when they are; colour only on the deviating value | `docs/DECISIONS.md` "Tablo standardı"; `tableUnityRatchet` |
| **Cache key hashes ALL inputs** — `fmt.Sprintf("…:%x", fnvDigest(sortedSlice(set)))`, never `len(set)` (cross-poisoning, v0.5.187) | `internal/api/cache.go` |
| **`timeRangeToNs(range)` only inside `useEffect`/`useMemo`** — bare in JSX = infinite refetch (v0.5.184) | every page using `range` |
| **CH query on `spans`/`metric_points`** — `LIMIT` + `SETTINGS max_execution_time` + time-bounded WHERE | `internal/chstore/*.go` |
| **Admin write = audit entry** — `s.audit(r, "kind.action", "resource", id, details)` | `internal/api/*.go` |
| **No PII redaction features** — operator preference: full fidelity | memory `feedback-no-redaction.md` |

## Performance budgets

- `/api/*` p99 < 200ms warm, < 1s cold; hot endpoints (`/api/services`, `/api/problems`, `/api/health`) < 50ms warm
- `/api/spans/heatmap` < 3s ≤6h window (auto-sample beyond); `/api/logs/patterns` < 2s at billion docs
- TTFI < 1.5s fresh tab; polling ≥ 10s (except `/api/health` 5s); every poller pauses on `document.hidden`. Range-keyed data refreshes through the ONE global clock (`lib/autoRefresh.ts` + Topbar `RefreshPicker`, default 60s, closed route allowlist): never add `refetchInterval` to a `from/to`-keyed query (re-requests a frozen window); `since`-keyed queries opt in with `meta: LIVE_META`. New polling loops extend `lib/pollingBudget.pin.test.ts` (v0.10.1430).
- Live updates: ONE SSE bus — `/api/events` held by the shell's `useEventStream` (leader tab + BroadcastChannel); new event kind → `lib/queries/eventInvalidations.ts`. Components never open their own `EventSource` (HTTP/1.1 ~6-connection budget). Event-less data polls ≥10s.

## Tech stack

**Backend:** Go 1.22+, ClickHouse 24+, Redis 7+. `main.go` wires
`chstore.Store`, `logstore.Store` (CH or ES via
`COREMETRY_LOGS_BACKEND`), `tempo.Service`, `cache.Cache`,
`auth.Service`, `notify.Notifier`, `copilot.Service`.
**Frontend:** React + Vite, Tailwind v4 + shadcn (`components/shadcn`,
generated; land in Phase 1 R4/R5) + `components/coremetry` composites; `components/ui` FROZEN
(ratchet only down); tokens in `styles/globals.css` + `styles/tokens.css`.
React Query, react-router v6. No state library — URL is source of
truth for shareable views. Charts: `uPlot` only — never Chart.js or
heavy chart libs. Global shadcn migration supersedes Option B; CoSRE v2
builds on it (DECISIONS 2026-10-30, `docs/frontend/shadcn-phase1-plan.md`).
**Versioning:** git tags `v0.10.X`; 1.0 deferred by operator ([docs/RELEASE-1.0.md](docs/RELEASE-1.0.md)).
Runtime: ldflag > `/app/VERSION` > `"dev"` = **build** kimliği; `COREMETRY_VERSION`
env yalnız GÖSTERİLENİ değiştirir; `/api/version` ikisini + `overridden`
döndürür (docs/INCIDENTS.md, v0.5.394).

## Architectural invariants

1. **OTel is the source of truth** — OTLP/gRPC + HTTP only; attributes kept verbatim.
2. **ClickHouse is the warm store** — ES is optional *read* backend for logs only. Never break CH Async Insert (`async_insert=1`) on writes.
3. **MV-first reads** — every aggregate reads its pre-aggregate (`service_summary_5m`, `operation_summary_5m`, `trace_summary_5m`, `topology_edges_5m`; span MVs `canonicalMVs()` in `internal/chstore/store.go`; topology `internal/topology/`; rollups `migrations/`; pick via `/clickhouse-schema`). Raw `spans` for an aggregate = bug.
4. **`ReplacingMergeTree(version)` for state**, reads use `FINAL`. Whole-row replace: carry ALL fields forward.
5. **One CH table for saved state** — `saved_views(page='<kind>', …)`; no new schema per surface.
6. **Settings live in `system_settings`** — JSON blob per key; `LoadPersisted` at boot + `SavePersisted` on admin PUT (template: `internal/tempo/client.go`).
7. **Roles admin / editor / viewer** — `auth.RequireRole` / `RequireAnyRole`; viewer SEES state read-only, never blank.
8. **Background workers** hold the leader lock via the Redis mutex.
9. **New API endpoint = own file** `internal/api/<domain>.go` with `registerXxxRoutes(mux)`, registered from `init()` via `registerRoutesExtra` (`route_registry.go`); AI/copilot routes in `ai_routes.go` `registerAIRoutes`, wrapped in `requireCopilot`. `api.go` never grows — `TestApiGoDoesNotGrow` + `scripts/guard-api-go-size.sh`; baseline `.claude/baselines/api_go_lines` only goes down (`/api-route`).

## Ship checklist — every new operator-facing surface

1. Handler hits the right MV; 2. `s.serveCached` with hash-all-inputs
key; 3. auth gate + 4. audit entry if it writes; 5. settings via
`system_settings` if configurable; 6. type in `lib/types.ts`;
7. client method in `lib/api.ts`; 8. loading/error/empty states
(`<Spinner/>`, `<Empty/>`); 9. fe gate: `npx tsc --noEmit` +
`npx eslint src` + `TZ=UTC npx vitest run`; 10. `go build ./...` +
`go vet ./...`; 11. bug-fix release = regression test (pure-function,
table-driven, header cites the vX.Y.Z; canonical:
`internal/api/cache_key_test.go`) + `go test ./...`.

AI explain affordances route through `s.copilotExplain(r, ...)`,
never `s.copilot.Explain` direct (/ai attribution).

## Code patterns

- **Buttons/fields — one design language:** `coremetry/*` + `shadcn/*` first; `ui/` atoms (`Button`, `IconButton`, `Field`, `Badge`, `.badge .b-*`, …) only on unmigrated pages. Raw `<button>/<input>/<select>/<textarea>` outside `shadcn/`/`ai-elements/` is blocked (ESLint `ui/no-raw-element` + `buttonUnityRatchet` / `rawElementRatchet`; existing input/select/textarea counted in `eslint-suppressions.json`, only down).
- **URL = source of truth** for every selection (drawer/focus/filters/tab/range) — write with `replace:true`; sig-guard URL→state imports (v0.8.253); one-way-read is a recurring bug class (256/265/267).
- **Themes** are token-level CSS vars only (dark/light/redhat v0.8.268); chrome variants via scoped token remap; charts re-resolve on `data-theme`.
- **ES-cost UI:** fetch on expand/open only, no list prefetch, staleTime ≥ server TTL, cache-key params snapped to bounded rungs (v0.8.270).
- **Secrets in Settings:** never echo back; "stored" indicator; empty input preserves stored value.
- **Logstore plurality:** detectors go through `logstore.Store.CountPatterns(...)` batched (ES `_msearch` / CH tokenbf) — never call ES directly from a detector.
- **Shapes:** `lib/types.ts` is the single source — never re-declare in components. PascalCase types, camelCase props, `?:` for `omitempty`.
- **Triage:** P1 = now (critical + ayarlanabilir eşik katı (varsayılan 2×) / tamamen kayıp / ayarlanabilir açık-saat (varsayılan 4h) / exception fırtınası (≥N servis aynı pencerede yeni grup; vida: `exception_triage`); vida: `problem_priority` blobu), P2 = today, P3 = convenient. Exception'da kazanılmış P1 zamanla düşmez (operatör): patlama ya da `p1MinOccurrences`'ı aşmış grup resolve/ignore edilene dek P1; `regressed` dalı P2 ama yeniden açıldıktan sonra ≥`p1MinOccurrences` oluşumda P1; inbox'ta exception dışı türler P3, istisna listesi `problem_priority.inboxKeepSourcePriority` kaynak önceliğini korur. Deploy bilgisi görünür ama önceliğe karışmaz — `fresh deploy` tetikleyicisi yok, geri ekleme. Reason string ships with every Problem. Ayrıntı: `/aiops`.

## Workflow

- `kuyruk` = show prioritised queue, end with "Hangisi?". `devam` = continue current item.
- Operator-reported bugs = NEW priority, ship as `v0.10.X+1` immediately, never batch.

**Release — every functional change:**
```
edit → (fe) npx tsc --noEmit && npx eslint src && TZ=UTC npx vitest run
   && npm run bundle:check
→ go build ./... && go vet ./... → go test ./... → make audit
→ git add <files> → commit (heredoc) → tag v0.10.X
→ git push && git push --tags → deploy (background, ONE at a time)
```
`make audit`: 🔴 critical blocks the tag; 🟡 reviewed, ship if known false positive.

**Commit format:** `v0.10.X — title (≤70)` + body (what/why/root
cause, 72 cols, operator bugs start "Operator-reported: …") +
`Co-Authored-By: Claude <noreply@anthropic.com>`.

**Skills (`.claude/skills/`) — run BEFORE the matching change:**
- `/release`, `/bugfix`, `/spec` (3+ files), `/kuyruk`, `/tdd` (new backend feature / pure helper), `/review-changes` (pre-commit diff review; before `/release` on non-trivial work), `/where-is` ("X nerede").
- `/clickhouse-schema` (any CH table/query/MV) · `/api-route` (add/move any `/api/*` route) · `/mcp-tools` (MCP server) · `/otel-conventions` (OTel-shaped data) · `/otlp-converter` (`internal/otlp/`; golden-test obligation) · `/helm-chart-coremetry` (`charts/coremetry/`).
- `/aiops` (detector, rule-id, priority/escalation, incident attach, hypothesis score, exception ladder, any loop writing problems/anomaly_events/root_cause_hypotheses) · `/copilot-surface` (new AI ✨ Explain).
- `/frontend-conventions` (component/table/filter/drawer/theme/polling) · `/frontend-design-system` (any UI part — find the existing primitive first; `ui/` barrel is incomplete) · `/frontend-dashboard-panel` (new panel type).
- `/perf-triage` (ONE slow surface → measured root cause) · `/scale-audit` (quarterly whole-repo sweep).

## Pitfall rules — full stories in [docs/INCIDENTS.md](docs/INCIDENTS.md)

- `table-layout:fixed` + `nowrap` + small width silently clips; use min/max-width + ellipsis + title.
- ES: no `case_insensitive` on `query_string` (8.x rejects); `_msearch` over per-pattern `_search`; `significant_text` needs `background_filter` + `sampler`.
- Drain templating is sample-based (1000/5min), never full-scan.
- Value+unit templates (Nh/Nd, ms/s): test EVERY unit; never `toDate()` around sub-day math (`retention_test.go`).
- Combined-MV drop: inner table first with `max_table_size_to_drop=0` (`dropCombinedMV`).
- MV state columns use `quantilesTDigestState`, never reservoir `quantilesState`; MV type changes cause a rolling-deploy read-error window — roll fast or dual-column.
- SQL references telemetry tables UNQUALIFIED (no `coremetry.` prefix).
- `toDateTime64(?)` bind args must be tz-less (no trailing `Z`) — `chDateTime64Arg`.
- Coremetry Deployment keeps `maxUnavailable: 0` or the OTel collector wedges on rollout ("zero addresses"); pre-fix: restart collector.

## Anti-patterns (don't)

- Bypass `s.serveCached` on hot reads; create new schema for user-saved state; fetch full catalogues for pickers.
- Add backwards-compat shims when removing a feature; propose data redaction; fix typing with `as any`.
- Write `// TODO` without a release-tag context — ship it or queue it via `kuyruk`.
- Add a metric dimension without bounding cardinality; `LowCardinality(String)` only for measured low-distinct columns — never IDs or free text (`/clickhouse-schema` C2–C3).

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
| CH schema (app-owned: `store.go` `migrate()`; operator-owned: `migrations/*.sql`) · single-node→cluster copy | `internal/chstore/`, `migrations/` · `internal/chmigrate/` |
| Frontend pages / components / types+client / routes / sidebar | `frontend/src/pages/`, `components/`, `lib/{types,api}.ts`, `App.tsx`, `components/Sidebar.tsx` |

**Demo realism:** Go + JBoss demos share one load model (diurnal,
incidents, log-normal latency, correlated errors, real histogram
buckets, saturation) — [docs/DEMO-REALISM.md](docs/DEMO-REALISM.md).
New demo scenario/metric reads the load model (`L` / `DemoLoad`), never
its own fixed probability or uniform latency.

**Self-observability:** `/admin/stats` reads `GetSystemStats(ctx)`; AI
usage → `ai_calls` → `/ai`. New ingest path or expensive endpoint =
register a counter.

Decision log: [docs/DECISIONS.md](docs/DECISIONS.md).
