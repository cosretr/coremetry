# Coremetry

OpenTelemetry-native APM. Single Go binary + ClickHouse + Redis + optional external Elasticsearch/Tempo. 1000s of services, 1B+ spans/day, one container. Rubric when in doubt: **"how would Datadog / Dynatrace / Honeycomb engineer this?"**

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
- TTFI < 1.5s fresh tab; polling ≥ 10s (except `/api/health` 5s); every poller pauses on `document.hidden`. Global refresh clock (`lib/autoRefresh.ts`) + ONE SSE bus (`/api/events`) rules → `/frontend-design-system`.

## Tech stack
- **Backend:** Go 1.22+, ClickHouse 24+, Redis 7+. `main.go` wires `chstore.Store`, `logstore.Store` (CH or ES via `COREMETRY_LOGS_BACKEND`), `tempo.Service`, `cache.Cache`, `auth.Service`, `notify.Notifier`, `copilot.Service`.
- **Frontend:** React + Vite, Tailwind v4 + shadcn (`components/shadcn`, generated; land in Phase 1 R4/R5) + `components/coremetry` composites; `components/ui` FROZEN (ratchet only down); tokens in `styles/globals.css` + `styles/tokens.css`. React Query, react-router v6. No state library — URL is source of truth for shareable views. Charts: `uPlot` only — never Chart.js or heavy chart libs. Global shadcn migration supersedes Option B; CoSRE v2 builds on it (DECISIONS 2026-10-30, `docs/frontend/shadcn-phase1-plan.md`).
- **Versioning:** git tags `v0.10.X`; 1.0 deferred by operator ([docs/RELEASE-1.0.md](docs/RELEASE-1.0.md)). Build vs displayed version (ldflag / `COREMETRY_VERSION`) → `/release`.

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

## Code patterns
- **Ship checklist** (11 items, every new operator-facing surface; `s.copilotExplain` rule) → `/release`.
- Frontend patterns (buttons/fields, URL state, themes, ES-cost UI, shapes) → `/frontend-design-system`; Logstore plurality + Triage (P1/P2/P3) → `/aiops`.
- **Secrets in Settings:** never echo back; "stored" indicator; empty input preserves stored value.

## Workflow

- `kuyruk` = show prioritised queue, end with "Hangisi?". `devam` = continue current item.
- Operator-reported bugs = NEW priority, ship as `v0.10.X+1` immediately, never batch.
- ≥1000 satırlık dosya (liste `docs/claude/large-files.md`) bütün okunmaz: sembolü grep'le, ±80 satır oku; DECISIONS/INCIDENTS'ta başlık grep'i; testdata golden dosyaları asla.

**Release — every functional change:**
```
edit → scripts/gate.sh all   (fe: tsc → eslint → vitest → bundle:check;
   go: build → vet → test; output = failures + summary only) → make audit
→ git add <files> → commit (heredoc) → tag v0.10.X
→ git push && git push --tags → deploy (background, ONE at a time)
```
`make audit`: 🔴 critical blocks the tag; 🟡 reviewed, ship if known false positive.

**Commit format:** `v0.10.X — title (≤70)` + body (what/why/root cause, 72 cols, operator bugs start "Operator-reported: …") + `Co-Authored-By: Claude <noreply@anthropic.com>`.

**Skills (`.claude/skills/`) — run BEFORE the matching change:**
- `/release`, `/bugfix`, `/spec` (3+ files), `/kuyruk`, `/tdd` (new backend feature / pure helper), `/review-changes` (pre-commit diff review; before `/release` on non-trivial work), `/where-is` ("X nerede").
- `/clickhouse-schema` (any CH table/query/MV) · `/api-route` (add/move any `/api/*` route) · `/mcp-tools` (MCP server) · `/otel-conventions` (OTel-shaped data, `internal/otlp/` converter; golden-test obligation) · `/helm-chart-coremetry` (`charts/coremetry/`).
- `/aiops` (detector, rule-id, priority/escalation, incident attach, hypothesis score, exception ladder, any loop writing problems/anomaly_events/root_cause_hypotheses) · `/copilot-surface` (new AI ✨ Explain).
- `/frontend-design-system` (any UI part: component/table/filter/drawer/theme/polling — find the existing primitive first; `ui/` barrel is incomplete) · `/frontend-dashboard-panel` (new panel type).
- `/perf-triage` (ONE slow surface → measured root cause) · `/scale-audit` (quarterly whole-repo sweep).

## Model kademeleri
- **Sonnet 5.5 (basit):** dosya okuma/keşif, grep, test çalıştırma ve özetleme, mekanik düzenleme, migration-log girişi, küçük bug fix. → `repo-explorer` ve `implementer` ajanları.
- **Opus 5.5 (orta, varsayılan ana oturum):** onaylı audit'e göre dilim uygulama, çok dosyalı değişiklik, test yazma, refactor.
- **Fable 5.1 (ileri):** Faz 0 audit'leri, mimari kararlar, ClickHouse şema/rollup tasarımı, Davis parity / ML algoritmaları, CoSRE investigation loop tasarımı, zor performans sorunları. → `architect` ajanı.
- Ana oturum işi kademesine göre ajana devreder; Fable'a yalnız iş gerçekten "ileri" kademedeyse gidilir.

## Pitfall rules — full stories in [docs/INCIDENTS.md](docs/INCIDENTS.md)
CH (value+unit/toDate, combined-MV drop, TDigest state, unqualified tables, toDateTime64 args) → `/clickhouse-schema` · ES + Drain → `/aiops` · table-layout clip → `/frontend-design-system` · `maxUnavailable: 0` → `/helm-chart-coremetry`.

## Anti-patterns (don't)
- Bypass `s.serveCached` on hot reads; create new schema for user-saved state; fetch full catalogues for pickers.
- Add backwards-compat shims when removing a feature; propose data redaction; fix typing with `as any`.
- Write `// TODO` without a release-tag context — ship it or queue it via `kuyruk`.
- Add a metric dimension without bounding cardinality; `LowCardinality(String)` only for measured low-distinct columns — never IDs or free text (`/clickhouse-schema` C2–C3).

**What goes WHERE** (domain → path table, demo realism, self-observability) → `/where-is`. Decision log: [docs/DECISIONS.md](docs/DECISIONS.md).
