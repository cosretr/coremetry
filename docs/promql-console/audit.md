# PromQL Explore console (Thanos): Phase 0 audit

**Status:** Phase 0 deliverable. Read-only audit, awaiting operator approval. No code has been written.
**Date:** 2026-09-26.
**Scope:** v1 is the editor, table, graph and guardrails over the existing Thanos integration, per remote cluster. CoSRE/NL features, dashboards, alert rules and write APIs are out of scope.

**Conventions**
- Citations are `path:line` relative to the repo root, taken at audit time (HEAD plus the uncommitted working tree). Line numbers drift, so grep the symbol first.
- Anything marked *(assumption)* was not verified against a live system. Nothing here touched ClickHouse, Thanos, a browser or a dev server.
- Configured endpoints are referred to generically. No host names appear in this document.

---

## 1. Summary

1. **What exists.**
   - A per-cluster Thanos client (`internal/thanos`), configured in Settings → Remote clusters (`system_settings["thanos_clusters"]`). It supports bearer auth and optional cluster-label injection.
   - 16 curated viewer routes with fixed queries, `/api/clusters/*`.
   - A PromQL console over **ClickHouse or VictoriaMetrics, not Thanos**, at `/metrics?editor=1`.
   - Three-role RBAC, the `audit_log` table, `CorePanelMulti`, `useDataTable`, and `TimeRangePicker` with `useUrlRange`.
2. **What does not exist.**
   - No raw-PromQL path to Thanos, and no label or series APIs.
   - No decoding of warnings or scalar/string results, and no decoding of the JSON error body.
   - No timeout above 15s.
   - No per-user concurrency or rate limiter, and no server-side query history.
   - No code editor anywhere in the frontend.
3. **What to build.**
   - A console client inside `internal/thanos`.
   - `/api/promql/*` in its own route file. It registers through `init()` and `registerRoutesExtra`, so **`api.go` grows by 0 lines**.
   - A `promql_console` settings key.
   - A CodeMirror 6 editor in its own lazy chunk.
   - The `/explore/promql` page, built on the existing atoms plus `CorePanelMulti`.
4. **Decisions needed** (§10):
   - Minimum role: I recommend **editor+**.
   - Audit: I recommend **reusing `audit_log`** in v1.
   - Rate limits: **per-pod** in v1.
   - Reconciling 500 series with the 8 MiB body cap and the chart budget.
   - Hardening cluster-label injection: a `#` comment currently bypasses it.
   - Accepting that "shadcn" and "TimeSeriesPanel" in the request resolve to the existing atoms and `CorePanelMulti`, per `docs/DECISIONS.md`.

---

## 2. Thanos client: capabilities vs requirements

| Requirement | Today | Evidence | Gap and plan |
|---|---|---|---|
| Instant query | **Partial.** Package-internal `doQuery` exists. The exported `InstantSamples` drops the timestamp, and a `ParseFloat` error becomes 0. | `internal/thanos/client.go:525-537`; `internal/thanos/cluster_identity.go:309-322` | New exported console method. |
| Range query | **Partial.** Only reachable through typed trend methods built on fixed builders. A generic exported range query **does not exist**. | `client.go:1464,1513,1656,1793` | New. |
| Labels, label values, series, metadata | **Do not exist** in `internal/thanos`. They exist only in `internal/vmetrics`. `/api/v1/series` and `/api/v1/metadata` are called nowhere. | `internal/vmetrics/client.go:497,643` | New, with `match[]` injection. |
| Result types | Vector and matrix only. `Result []promSeries`, so a scalar or string result (`1+1`, `time()`) fails to decode. | `client.go:501-518`; same in `internal/promapi/promapi.go:58-72` | New decoder for all four `resultType`s. |
| HTTP method | GET only; the query goes in the URL. | `client.go:556-557` | Add POST form, because queries can be up to 8 KiB. |
| Timeout | A hard 15s on both shared clients. Handler deadlines are 6–10s. The `timeout=` parameter is never sent. | `client.go:470,486-491`; `internal/api/thanos_handlers.go:139,202` | Add a dedicated console client whose timeout comes from settings (30s), plus a context deadline and the `timeout=` parameter. **Do not** raise the shared 15s client: it protects `serveCached` single-flight slots (comment at `client.go:465-469`). |
| Body cap | An 8 MiB `LimitReader` cuts silently, which then shows up as `decode:` errors. | `client.go:572,577-580` | Return an explicit `response_too_large` error. Console cap comes from settings. |
| Series cap | 1000, applied silently after the full unmarshal. No truncated flag. | `client.go:520-523,584-586` | Cap at 500 and return `truncated` and `totalSeries`. Precedent: `DecodeSeriesMeta` in `internal/promapi/meta.go:30-64`. |
| Warnings and infos | **Dropped.** No envelope has these fields, and there are zero `"warnings"` hits in Go code under `internal/`. | `client.go:501-509`; `promapi.go:58-67` (only VictoriaMetrics' `isPartial`, `:66`) | Decode them and pass them to the client. |
| Error decoding | Any HTTP status ≥300 becomes `HTTP n: <first 200 bytes>`, and `errorType`/`error` are never decoded. Prometheus returns `bad_data` as HTTP 400, so the `status!="success"` branch is effectively dead for query errors. | `client.go:573-576,581-583` | Decode the JSON error body and map `errorType` to an HTTP status. Extract the `line:col` position *(assumption: Thanos forwards Prometheus parser messages of the form `1:14: parse error: …`; verify in Phase 3)*. |
| `partial_response`, `dedup` | Not sent anywhere, so the querier's defaults apply. | grep: empty | Decision needed (Q7). |
| `max_source_resolution` | Always `auto` on `query_range` unless the caller sets it. | `client.go:547-555` | Keep for auto step. Manual step is Q7. |
| Auth | `none` and `bearer` only. `TokenRef` can be `env:` or `file:`. A token ref that fails to resolve sends the request **without** an Authorization header. There is no basic auth, mTLS, custom CA or custom header support. | `thanos_handlers.go:794-800`; `client.go:71-81,347-383,562-564`; `internal/secretref/secretref.go:24-39` | Reuse unchanged. `effectiveTokenFor` is unexported (`client.go:386`), so the console client must live **inside** `internal/thanos`. |
| Proxy | The insecure (skip-verify) transport leaves `Proxy` unset, so `HTTP(S)_PROXY` is ignored for skip-verify clusters. | `client.go:486-491` | Note only. Out of scope. |
| Cluster to endpoint | `ClusterByRef` looks up by ID, then by Name, enabled clusters only. The endpoint is `c.URL + path`. The ID is `"c-"` + fnv(Name). | `cluster_identity.go:36-52,173-180`; `client.go:433-446,556` | Reuse. |
| Existing window and step policy | `/clusters` trends clamp the window silently at 30d (`thanosMaxWindow`) and step along the `stepForWindow` ladder. | `thanos_handlers.go:78,90-95`; `client.go:1944-1996` | The console's 7d cap is **stricter** and is rejected explicitly (§6). |
| Test harness | `fakeQuerier` over `httptest`, with no live calls. | `internal/thanos/client_test.go:18-36` | Reuse. |

### Matcher injection semantics

This matters for correctness, and it has a security angle.

**How it works**
- When a cluster has `ThanosLabelName` set (single shared querier mode), `doQueryWith` rewrites **only the `query` parameter**, so every selector carries `<label>="<value>"`. The value falls back to the cluster Name (`client.go:538-546`; `cluster_identity.go:56-64`; tokenizer at `internal/thanos/cluster_matcher.go:97-201`).
- An empty label name means no rewrite: the one-URL-per-cluster model.

**Effect on user PromQL**
- The injected matcher is ANDed into every selector.
- A user matcher on the same label gives an empty result, not an escape, because both matchers must hold.
- `absent()` and cross-cluster comparisons change meaning.

**`match[]` is never injected.** In shared-querier mode, label, value and series autocomplete would return every cluster's data.

**Bypass: `#` comments are not handled**
- The tokenizer has no `'#'` case. The in-house lexer does have one (`internal/promql/lex.go:86`).
- An apostrophe inside a comment (for example `# don't`) enters `skipString` (`cluster_matcher.go:63-78,109-112`), which copies everything up to the next quote, or to the end of the query, **uninjected**.
- The file itself states that subqueries and `@` are out of its scope (`cluster_matcher.go:28-31`).
- The tests do not cover comments, subqueries, `@` or quoted UTF-8 names (`cluster_matcher_test.go:24-44`).

**Security relevance *(assumption)*:** every console user can already pick every enabled cluster. The bypass therefore matters only if the shared querier also serves data for clusters or tenants that are **not** configured in Coremetry.

**NamespaceFilter:**
- It is **not** applied to arbitrary queries. Only the fixed builders call `nsMatcher` (`internal/thanos/promql.go:42-47`), even though the field comment says it applies to every query (`client.go:82-86`).
- It is a cardinality shield, not an authorization boundary.

**Plan (Phase 1, commit C1)**
- Neutralise comments before injection: strip them with a lexer pass, or reject `#` outside strings.
- Add golden tests for comments, subquery, `@`, `offset`, UTF-8 quoted names, `absent()` and binary operators.
- Inject the same matcher into `match[]`, and synthesise `match[]={<label>="<value>"}` when the caller sends none.
- Return `effectiveQuery` so the UI shows exactly what ran.

---

## 3. Existing PromQL passthroughs and explorers

| Surface | Backend | Gate | Evidence | Fit for the Thanos console |
|---|---|---|---|---|
| `GET /api/metrics/promql` → `queryPromQL` | ClickHouse (in-house parser subset) or VictoriaMetrics (raw passthrough) | viewer, not audited | `internal/api/api.go:819`; `internal/api/promql.go:16,24-26,50-125`; `internal/api/metricsource.go:107-108,421,564` | No. It sits on the `metricSource` seam (17 methods, `metricsource.go:155-294`), which has no Thanos source and no cluster axis. |
| `/metrics?editor=1` → `MetricQueryEditor`, PromQL tab | same as above | viewer can query; `canEdit` only gates "Add to dashboard" | `frontend/src/pages/Metrics.tsx:39,74-75`; `frontend/src/components/viz/MetricQueryEditor.tsx:559-563,745` | No. It is a textarea with a hand-rolled autocomplete (`frontend/src/lib/promqlToken.ts`), and the query is not written to the URL. |
| Dashboard `promql` panel | same | — | `frontend/src/components/dashboard/PanelRenderer.tsx:393-402` | No. Dashboards are out of scope. |
| `/explore` | span and catalogue metrics | viewer | `frontend/src/App.tsx:170`; `frontend/src/pages/Explore.tsx:67-77` | No. Its history is a 4-slot localStorage ring (`frontend/src/pages/explore/useQueryHistory.ts:1-27`), which violates the "no localStorage" requirement. |
| `/api/clusters/*` (16 routes) | Thanos, fixed builders | viewer | `api.go:618-633` | Not a passthrough. Reuse only the cluster list (`/api/clusters/sources`). |
| `/clusters` PromQL list | display only | — | `frontend/src/pages/clusters/PromQLList.tsx:1-20` | No. |

**Recommendation: a new page (`/explore/promql`) and a new route family (`/api/promql/*`) over `internal/thanos`. Do not extend the existing surfaces.**
- Adding Thanos as a third `metricSource` would mean implementing 17 methods that don't apply, and it still wouldn't give a per-cluster axis.
- The ClickHouse/VictoriaMetrics console has different semantics: a parser subset versus raw PromQL, and no clusters.
- **Reuse:**
  - `maxPromQLQueryLen = 8192` (`promql.go:16`)
  - the error-classification pattern (`promql.go:120-125`)
  - `promapi`'s decoders as a copy-the-pattern source. Its header says Thanos was deliberately not refactored onto it (`promapi.go:21-26`).
  - `CorePanelMulti`
  - `AdminSql`'s layout as a structural twin (§7)
- `/explore/promql` does not collide with the exact `/explore` route (`App.tsx:170`).
- **Name collisions to avoid:** `internal/api/promql.go` and `internal/thanos/promql.go` already exist, so the new files use a `promql_console*` / `console*` prefix (§9). Neither `registerPromQLRoutes` nor `/api/promql` exists yet (grep: empty).

---

## 4. AuthN/AuthZ

### Role model

**Roles**
- There are exactly three roles: `admin`, `editor` and `viewer` (`internal/auth/auth.go:23-31`). `IsValidRole` accepts only these (`auth.go:41-43`).
- A permission or capability model **does not exist**. LDAP groups map to one of the three roles (`internal/ldap/ldap.go:116,125`).

**Live role resolution**
- The role is resolved live from the store and cached for 10s (`auth.go:353-367`; `internal/auth/live_authz.go:43`).
- If the store is unreachable, it **fails open** to the token's role (`live_authz.go:73-100`).

**API tokens and HTTP status codes**
- API tokens (`cmk_`) resolve to `UserID="token:<id>"` plus the role stored on the token (`internal/auth/api_tokens.go:40-57,97-101`), so they would pass the same gate.
- 401 means no session and 403 means the wrong role (`auth.go:500-528`).
- The frontend special-cases only 401 (`frontend/src/lib/api.ts:345-349`). A 403 or 429 arrives as a generic `Error("HTTP n: body")`.

### Gating recipe

**Server**
- The gate goes on the registration line: `auth.RequireAnyRole(editorRoles, h)` (`auth.go:533-548`; `editorRoles` at `api.go:570`) or `auth.RequireRole(auth.RoleAdmin, h)` (`auth.go:515-528`).
- The route must stay under `/api/`. Anything outside it is treated as public, and `s.audit` silently writes nothing (`auth.go:318-322`; api-route SKILL).
- Registration: `func init() { registerRoutesExtra("promql", (*Server).registerPromQLRoutes) }` (`internal/api/route_registry.go:30`). `TestApiGoDoesNotGrow` stays green (baseline 11662 in `.claude/baselines/api_go_lines` = the current `wc -l`).

**Client**
- A route-level guard **does not exist** (`App.tsx:153-230`). Each page gates itself: see `frontend/src/pages/Settings.tsx:99-110`, where the lock is an `<Empty>` inside Topbar and PageShell, never a blank page (CLAUDE.md invariant 7).
- The nav supports only `adminOnly` (`frontend/src/components/Sidebar.tsx:26-31,394-398`; `frontend/src/components/CommandPalette.tsx:47-49,367-371`). An editor-level flag and a `useRole`/`useCanEdit` hook **do not exist**.

### Custom roles

- A custom role is `{name, pages[]}` and only **narrows** what a viewer sees (`internal/auth/custom_roles.go:13-21`).
- It is enforced **only in the browser** (`frontend/src/components/AppShell.tsx:126-140`; `Sidebar.tsx:394-398`). This was accepted by the operator (`docs/DECISIONS.md:530-540`).
- It cannot restrict an API on the server, and it cannot be assigned to editor or admin users (`internal/api/custom_roles.go:145-148`).
- **Prefix leak:** `isPathAllowed` matches `p + '/'` (`AppShell.tsx:31-35`), so a custom role that grants `/explore` also opens `/explore/promql` in the browser.

### Recommended gate: editor+

| Option | Pros | Cons |
|---|---|---|
| viewer (bare route) | Matches the existing Thanos and ClickHouse/VictoriaMetrics PromQL reads (`api.go:618-633,819`). | Every viewer can send unbounded PromQL to a shared production store. Custom roles **cannot** restrict it on the server. Any metric in the store is exposed, not just the curated views. |
| **editor+ (recommended)** | Least privilege for a load-generating, whole-store read. Uses the existing trusted-operator tier and the existing `RequireAnyRole(editorRoles)`. Custom roles become irrelevant (they apply to viewers only). | Needs a new nav/palette field (e.g. `minRole?: 'editor'`) and a page lock. It is deliberately stricter than `/api/metrics/promql`. |
| admin | Zero new nav code (`adminOnly`). | Too narrow: SREs are typically editors. It would push people toward admin accounts. |

**Consequences of editor+**
- Gate every `/api/promql/*` route, including labels, series and history, with `RequireAnyRole(editorRoles, …)`.
- Settings `GET` stays viewer-readable and `PUT` is admin-only.
- Viewers see a lock `<Empty>`. The `/explore` prefix leak is harmless, because the page locks and the API returns 403.
- Do **not** add `/explore/promql` to the viewer page catalogue `internal/api/pages.go:28-66`, which by design excludes admin-only surfaces (`pages.go:13-15`).

---

## 5. Audit log

### Existing mechanism

**Call and table**
- The call is `s.audit(r, action, kind, targetID, details)` (`internal/api/anomaly_extra.go:275-311`).
- The table is `audit_log`: MergeTree, monthly partitions, `ORDER BY (time, id)`, fixed `TTL 365 DAY`, with a free-form `details String` column (`internal/chstore/store.go:2346-2360`).
- The admin UI and CSV export exist (`anomaly_extra.go:202-230`). The action filter list is `KNOWN_ACTIONS` (`frontend/src/pages/AdminAudit.tsx:42-76`).

**Best-effort delivery**
- Rows go onto a 1024-slot channel (`api.go:459`). A full channel **drops** the row and increments `auditDropCount` (`anomaly_extra.go:303-310`).
- **Nothing reads that counter.** Grep finds only the field and the increment, even though the comment says it shows on `/admin/stats` (`api.go:302-310`).
- The drainer flushes every 64 rows or 200ms, which is about 320 rows/s per pod. A failed flush is **logged and discarded** (`api.go:468-486`).
- If there are no claims, it returns silently (`anomaly_extra.go:277-279`).

**IP and precedents**
- The logged IP is the client's first `X-Forwarded-For` value (`anomaly_extra.go:313-327`).
- Per-query precedents:
  - `sql.query`, success only, details built with `%q` (`internal/api/sql_playground.go:136-141,196`).
  - The Oracle console, which logs success and failure (`internal/api/oracle_console.go:66,71`).
  - `mcp.tool.call` (`internal/api/mcp_observe.go:12-22`).
- `/api/metrics/promql` is explicitly not audited (`promql.go:24-26`).

### Recommendation: v1 reuses `audit_log`

**Why this is enough in v1**
- It is the existing compliance surface: 365-day retention, admin UI, CSV export.
- It needs no new schema, in line with CLAUDE.md "don't create new schema".
- Volume is trivial *(assumption: 50 active users × 200 runs/day ≈ 10k rows/day ≈ 3.7M rows/year)*. That is far below the drainer's capacity and negligible for ClickHouse.

**What to write**
- One row per `query` and `query_range` call: `s.audit(r, "promql.query", "thanos_cluster", clusterID, details)`.
- `details` is built with `json.Marshal`, not `%q`: `{cluster, mode, query, start, end, step, durationMs, series, truncated, status, errorType}`.
- **Audit every outcome:** `ok`, `error`, and `rejected` (range, rate or concurrency guardrail).
- Do **not** audit labels, values, series or history. That is autocomplete noise, in line with the `mcp_observe.go:13-14` stance on reads.
- Add `promql.query` to `KNOWN_ACTIONS`.

**Known gaps to accept, or escalate**
- (a) Delivery is best-effort (drops and silent flush loss). This is **platform-wide** and applies equally to admin mutations, so the fix belongs in `s.audit`, not in the console.
- (b) There are no typed columns, so reports need `JSONExtract`.
- (c) Query rows crowd the default 24h / 200-row admin view. Mitigate with the action filter.

**Option B (only if the bank requires typed columns or separate retention)**
- A dedicated append-only table modelled on `ai_calls` (`store.go:2718-2740`: typed user, `duration_ms`, `status`, monthly partitions, 90-day TTL).
- This needs an operator decision, because the `clickhouse-schema` decision tree has no branch for it.
- It also needs updates to `purge_coverage_test.go` and `state_replication_test.go:32-41`.

---

## 6. Guardrails

| Guardrail | Exists today? | Plan | Default |
|---|---|---|---|
| Query timeout | No. 15s shared client (`client.go:470`). | Console client timeout, plus a context deadline, plus the `timeout=` parameter sent to Thanos. Timeout errors come back as `errorType: timeout` (HTTP 504). `http.Server` has no Write timeout (`api.go:1398`), and the frontend request timeout is 60s (`api.ts:88`), so a 30s handler is safe. | 30s |
| Max range | Partial. `/clusters` **silently clamps** at 30d (`thanos_handlers.go:78,90-95`). | The console **rejects** anything over the limit with a structured `guardrail` error (400) that includes the limit. The UI offers "use the last 7d". This makes the Phase 3 30d check deterministic. | 7d |
| Step: auto default and floor | Partial. A server ladder exists (`client.go:1944-1996`), and client rungs exist (`frontend/src/lib/chartStep.ts:16-58`). | `effectiveStep = max(requested or auto, minStep, ceil(range / maxPointsPerSeries))`. A raised step is **not** an error: return `meta.step` and `stepRaised: true` plus a warning. With a 15s floor, the 11k ceiling takes over above about 45.8h; at 7d the floor is 55s, rounded to the 60s rung. The auto default targets chart width (`stepForWidth`), not 11k points. | minStep 15s, maxPoints 11,000 |
| Series cap | Partial. 1000, silent (`client.go:523,584-586`). | Stream-decode, keep the first N series, count the rest, and return `truncated` and `totalSeries`. | 500 |
| Response size | Partial. 8 MiB silent cut (`client.go:572`). | A console-specific cap with an explicit `response_too_large` error. See the budget conflict below. | Q4 |
| Query length | Yes, for ClickHouse/VictoriaMetrics (`promql.go:16`). | Reuse the constant: 400 over 8192. | 8192 |
| Per-user concurrency | **Does not exist** anywhere. The existing semaphores bound fan-out inside a single request only (`api_slo.go:62`, `messaging_metric.go:306`). | A non-blocking try-acquire per `Claims.UserID`; when full, return 429 `rate_limited` with `Retry-After`. | 2 |
| Per-user rate limit | No reusable limiter. The MCP gate is a per-pod fixed window tied to MCP error text (`internal/api/mcp_gate.go:35-36,123-171`). The only 429 in `internal/api` is the public status-subscribe limit (`api.go:7880`). `x/time/rate` is not used. | A per-user fixed window or token bucket, as a pure helper with an injected clock. Autocomplete endpoints get their own, higher budget, and cache hits don't count. | e.g. 30/min query, 240/min metadata |
| Autocomplete bounds | — | Always time-bounded: explicit `start`/`end`, otherwise the last 1h, clamped to the maximum range. Enforce `limit` server-side (truncate) whether or not Thanos honours it (Q12). Cache 60s. | limit 1000 |

**Budget conflict (Q4)**
- 500 series × 11,000 points × about 30 bytes of JSON per point is about 165 MB. The 8 MiB cap, the browser and uPlot cannot absorb that.
- **Recommendation:** auto step targets chart width. At about 1,000 points per series, 500 series come to roughly 15 MB. The 11k figure is only the manual-step ceiling. The console body cap is a setting (for example 32 MiB) with an explicit error that suggests a larger step or aggregation.
- This is **not** an exact budget. The series count is unknown until the response arrives.

**Caching**
- **Do not** use `serveCached` for `query` or `query_range`:
  - The cache is shared across users, so hits would bypass the per-user limits and audit.
  - A stale hit re-runs the query in the background with no user context (`internal/api/cache.go:262-268,352`).
  - Every result would be stored in Redis.
- **Do** cache labels, values and series for 60s. The key hashes **all** inputs with a sorted FNV digest: cluster ID, the **effective label name and value**, URL, sorted `match[]`, bucketed start/end, and limit.
- The existing `clusterCfgDigest` leaves out the injected label (`thanos_handlers.go:31-37`). Do not reuse it as-is.

**Where configuration lives**
- A new `system_settings` key, `promql_console` (one JSON blob, CLAUDE.md invariant 6). Fields and bounds: `timeoutS` (5–120), `maxRangeH` (1–720), `minStepS` (1–3600), `maxPointsPerSeries` (≤11000), `maxSeries` (10–2000), `maxBodyMiB`, `perUserConcurrency` (1–10), `perUserPerMin`, `metaPerUserPerMin`, `partialResponse`. Zero values fall back to protected defaults. Precedent: VictoriaMetrics `RateWindowFloorS` (`internal/vmetrics/client.go:86-108`).
- **API:** `GET /api/settings/promql-console` is viewer-readable, so the console can show its limits. `PUT` is admin-only, validates, persists, swaps the value live, and audits `settings.promql_console.update`. Precedent: `internal/api/trace_root_def_settings.go:36-41,99`.
- **Cross-pod reload:**
  - Call `publishConfigReload(ctx, "promql_console")` (`cache.go:475-480`).
  - Add a matching case in `reloadConfigOnSignal` (`cache.go:487`; thanos case at `:532-537`). `TestEveryConfigReloadTopicHasAListener` enforces this (`internal/api/config_reload_test.go:26`).
  - Add the topic to the config-import republish list (`internal/api/config_iox.go:229-231`).
- **Boot:** load at startup, then run a 30s ticker, like `trace_root_def` (`main.go:1362-1363`).
- **State location:** `Server` fields are declared in `api.go`, so the limiter and settings state live in a **package-level value in the new file**. Precedents: `var traceBackfill` (`internal/api/admin_trace_backfill.go:66`) and `var evalJobs` (`internal/api/ai_evalset_runs.go:134`).
- **Why not the `thanos_clusters` blob:** its GET is admin-only because it carries tokens (`api.go:1188-1189`), and its PUT replaces the whole blob.
- **UI:** a sub-panel in Settings → Remote clusters (`frontend/src/pages/settings/ClustersTab.tsx`). Precedent: `AiBudgetPanel` inside `AiTab`.

**Multi-replica caveat**
- In-memory limits are **per pod**. Effective limits multiply by the number of api replicas.
- The chart default `sessionAffinity: ClientIP` (`charts/coremetry/values.yaml:160`) makes them roughly per user *(assumption: if the bank's router or ingress terminates connections, the Service sees the router's IP, so many users pin to one pod and share that pod's budget)*.
- Cluster-wide limits would need `INCR`/`EXPIRE` added to `cache.Cache` (the interface at `internal/cache/cache.go:21-80` has neither), with a per-pod fallback when Redis is absent. Recommended for v2 only.

---

## 7. Frontend

**Versions**
- React 18.3.1, react-router 6.30, React Query 5, Vite 8.1.4 (rolldown), TypeScript ~5.9, uPlot 1.6.32 (`frontend/package.json:20-65`).

**Routing and nav recipe**
1. Add a lazy import and `<Route path="/explore/promql">` in `App.tsx` (the pattern at `:16-91,153-230`).
2. Add its own `NavItem`, because `isActive` is exact-match (`Sidebar.tsx:633-640`), with a new `minRole?: 'editor'` field. Mirror it in `CommandPalette` PAGES.
3. Add the page-level lock `<Empty>` for viewers.
4. Optional: `frontend/src/lib/perf/routePrefetch.ts:34`, i18n keys (`frontend/src/lib/i18n.ts:53,206`).
5. Add a small shared `hasMinRole(user, role)` helper, since none exists.

**Design system (conflicts with the request)**
- `docs/DECISIONS.md:665-684`: *"2026-09-25 — Buton bütünlüğü: shadcn/Tailwind DEĞİL, mevcut atomlar (Seçenek B, v0.10.919)"*, i.e. "Button consistency: not shadcn/Tailwind, the existing atoms". It says *"Tailwind + shadcn/ui eklenmez"* (Tailwind and shadcn/ui are not added), and it was operator-approved.
- This is reinforced by `DECISIONS.md:462-464` and CLAUDE.md ("no Tailwind").
- **So the console uses the existing atoms:**
  - `Button` (variant is required; `frontend/src/components/ui/Button.tsx:28-55`)
  - `SegmentedControl` for instant/range
  - `TabStrip` for Table/Graph, stored in `?tab=`
  - `SelectField` and `SearchField`
  - `IconButton`
  - `Spinner` and `Empty`
- Raw `<button>` is an ESLint error (`frontend/eslint.config.js:13-24,95-100`).
- A Banner/Callout atom **does not exist**. For the truncation and warnings banner, follow `frontend/src/pages/explore/RowsCappedNote.tsx:30-50` (`role="status"`, `--warn`), or promote it to `ui/` if it is reused.

**Graph**
- The request says "existing uPlot TimeSeriesPanel", but that is the **legacy** preset path (`frontend/src/components/viz/TimeSeriesPanel.tsx:23-45`). Only three JSX consumers remain.
- The decision is **one engine, `CorePanel`** (`DECISIONS.md:479-487`), and the skill says a new chart uses `CorePanelMulti` (`.claude/skills/frontend-design-system/SKILL.md:246`).
- Use `CorePanelMulti` through the lazy `frontend/src/components/chart/corePanelEntry.tsx:35-157`, the way `frontend/src/pages/explore/QueryPanel.tsx:30-50` does.
- **Mapping:** matrix → `SpanMetricSeries {groupKey: [labelSetString], points: [{time: ns, value}], fullKey}` (`frontend/src/lib/types.ts:3558-3567`).
- **Legend and series toggle:** the built-in `PanelLegend` handles both: click isolates, Ctrl/Cmd-click toggles, and the keyboard works (`frontend/src/components/chart/PanelLegend.tsx:20-80`). Prometheus label sets are unique, so keying `hiddenNames` by name is safe.
- **Limits:**
  - Colours repeat after 10 series (`frontend/src/lib/chart/seriesRole.ts:23-29`).
  - The tooltip shows at most 8 rows (`frontend/src/lib/chart/tooltipModel.ts:69-79`).
  - The legend is not virtualised.
  - Explore caps charts at 50 series (`frontend/src/pages/explore/model.ts:138`).
- **Recommendation (Q5):**
  - Draw at most 50 series, with an honest "+N not drawn" note.
  - Put the full label-set list in an external virtualised legend table, following the GroupTable precedent (`frontend/src/pages/explore/GroupTable.tsx:248-342` driving `hiddenNames`/`focusedLabel`).
  - **Never** use `MultiLineChart`/`foldTopN`. Its "others" line is a **sum** (`frontend/src/lib/chart/foldTopN.ts:41,56-100`), which is wrong for gauges.

**Table (instant results)**
- Use `useDataTable` with dynamic columns: one per label key (the union across the result), plus value and timestamp. Values use `numeric: true`; label keys and values use `mono: true`.
- Precedent: `frontend/src/pages/AdminSql.tsx:463-507`.
- Use `VirtualTable` for more than 100 rows (`frontend/src/components/ui/DataTable/VirtualTable.tsx:25-70`), with `DataTableState` inside the table.
- Scalar and string results render as a single row.
- This matches the table standard (`DECISIONS.md:727-747`).

**Time range, cluster and step**
- **Range:** `TimeRangePicker` through `<Topbar range onRangeChange>`, with `useUrlRange` (`frontend/src/components/TimeRangePicker.tsx:36-39`; `frontend/src/lib/useUrlRange.ts:137-199`; `frontend/src/lib/urlState.ts:9-65`). Its 11 presets include 30d, which the Phase 3 check needs. Instant mode evaluates at the range end.
- **Cluster:** the Thanos list is `api.clusterSources()` → `/api/clusters/sources`, names only (`api.ts:1889`; `thanos_handlers.go:727-737`). Precedent: `frontend/src/pages/Clusters.tsx:140-150`.
  - **Don't** use ContextBar's Cluster control. It lists span-derived `k8s.cluster.name` values, not Thanos clusters (`frontend/src/lib/queries/services.ts:158-163`).
  - A name in `?cluster=` is exactly as stable as the ID, because the ID is a hash of the name (`cluster_identity.go:36-52`).
  - `ClusterByRef` accepts either, so no new cluster endpoint is needed.
- **Step:** `<select>` with "auto" plus the `STEP_OPTIONS` rungs (`frontend/src/pages/explore/presets.ts:151-160`). The server's `effectiveStep` is shown next to it.

**URL state (shareable links)**
- `?cluster=&mode=instant|range&q=&range=&step=&tab=`, written with `setSearchParams(prev => …, {replace: true})`. Preserve foreign parameters.
- Guard URL → state imports with a signature (`urlSig`, the pattern at `frontend/src/pages/Logs.tsx:398-425`), per `.claude/skills/frontend-conventions/SKILL.md:59-74`.
- `ShareButton` copies `location.href` (`frontend/src/components/ShareButton.tsx:23-40`).
- The API request itself uses a POST body, so long queries never hit URL limits server-side. The page URL still carries `q` (up to 8 KiB; see Q14).

**Query history (server-side, no localStorage)**
- `/api/preferences/{key}` **cannot** hold it: it accepts only a ColumnModel of at most 16 KiB (`internal/api/preferences_routes.go:49,77-89`).
- **Recommendation:** one `saved_views` blob per user, `page='promql-history'`, id `promql-history:<uid>`. The precedent is `internal/api/ai_conversations.go:15-67`: an operator-approved "saved_views blob, no new table" with server trimming and a 64 KiB cap, following CLAUDE.md invariant 5.
- The **server appends** inside the query handler for every executed run. Guardrail rejections are excluded.
- Keep the last 50, deduplicate consecutive identical entries (`q + cluster + mode`), and cap the blob at about 128 KiB by evicting the oldest. Worst case, 50 × 8 KiB is about 400 KiB.
- Read-modify-write on `ReplacingMergeTree` is last-writer-wins. That is acceptable, given per-user concurrency of 2.
- Endpoints: `GET` and `DELETE /api/promql/history`. API-token callers (`UserID="token:<id>"`) get no history.

**Errors and loading**
- Add a small `promqlErrorOf(err)` next to `apiErrorDetail` (`api.ts:390-402`) that parses `{errorType, error, position}`.
- Feed parse positions into CodeMirror `setDiagnostics`.
- Give 429 and 504 friendly messages.
- Run on demand only; no polling. Call `timeRangeToNs` only inside `useMemo`.

**Types**
- No Prometheus result type exists in `lib/types.ts` (grep: only the Thanos settings types at `:1957-2059`). Add them there.

---

## 8. `@prometheus-io/codemirror-promql` compatibility

**Verdict: compatible.** There are three blockers, and each has a fix.

**Registry facts (npm registry, 2026-09-26)**
- Latest is **0.315.0**. The version tracks Prometheus v3.15.
- License: Apache-2.0.
- **No React dependency.** Dependencies are `@prometheus-io/lezer-promql` (exactly 0.315.0) and `lru-cache ^11.5.2` (BlueOak-1.0.0, node ≥20; the builder image is node:22, `Dockerfile:9`).
- Peer dependencies: `@codemirror/state ^6.1.1`, `view ^6.4.0`, `language ^6.3.0`, `autocomplete ^6.4.0`, `lint ^6.0.0`, `@lezer/common ^1.0.1`. All are satisfied by the installed versions.
- Packaging: CJS `main` plus ESM `module`; no `exports` field.
- It works with React 18.3.1, Vite 8 / rolldown 1.1.5, TypeScript 5.9 and `moduleResolution: bundler`. Use `import type` for `PrometheusClient` because of `isolatedModules`.

**Blocker 1: duplicate CodeMirror instances**
- CodeMirror is in the lockfile **only** as a transitive dependency of `@grafana/ui` 13.1.2, which pins `@codemirror/state 6.6.0` and `view 6.41.0` exactly (`frontend/package-lock.json:1430-1434`).
- There are already nested copies: state 6.7.1 under `commands` and under `lint`, and view 6.43.8 under `lint` (`package-lock.json:472,530,539` vs the hoisted `:562,583`).
- A test bundle against the real `node_modules` produced **3× state and 2× view** (690 KB minified). CodeMirror throws "multiple instances of @codemirror/state" in that situation.
- **Fix:**
  - Add direct, **exact** pins for state ≥6.7, view ≥6.43, language, autocomplete, lint, commands, and @lezer common/lr/highlight. Root then wins hoisting, and Grafana's pins nest under `@grafana/ui/node_modules`, which is never bundled (Grafana's CodeMirror is tree-shaken today).
  - Optionally add `overrides` too (`package.json:61-66`).
  - Add a ratchet vitest that asserts a single state/view instance outside `@grafana/ui`.
  - Risk: `Dockerfile:12` runs `npm ci || npm install`, and the fallback can re-resolve versions silently.

**Blocker 2: chunking**
- Without a new group, CodeMirror (about **140 KB gzip** for core, lint and the PromQL layer; about 99 KB for core alone) lands in the catch-all `vendor` group (`frontend/vite.config.ts:81`). 31 chunks import that group.
- **Fix:**
  - Add a dedicated group `test: /node_modules\/(@codemirror|@lezer|@prometheus-io|style-mod|crelt|w3c-keyname|@marijn)\//` with priority ≥ 90. This follows the "higher-priority group swallows unclaimed deps" lesson (`vite.config.ts:72-77`).
  - Load it through a lazy `frontend/src/features/promql/editorEntry.tsx`, mirroring `corePanelEntry`.
  - Verify with `npm run build:analyze` and check that `index.html` does not preload the chunk.

**Blocker 3: the built-in HTTP client**
- It fetches the **full** `__name__` catalogue and `/metadata`, which violates "picker = server-side search" (CLAUDE.md hard constraints).
- It has no per-request cluster parameter.
- An unregistered `/api/promql/metadata` would answer HTTP 200 with `index.html` (api-route SKILL, "silent failures").
- **Fix:** a custom `PrometheusClient` over `lib/api.ts`. This keeps `API_BASE`, cookies, 401 handling, timeouts and `CanceledError`.

**Custom client mapping** (upstream v0.315.0: the completer never calls `series()` or `flags()`; `lookbackInterval` is undefined by default, so no time bounds are sent otherwise):

| Library method | Console endpoint |
|---|---|
| `metricNames(prefix)` | `GET /api/promql/label/__name__/values?cluster=&match[]={__name__=~"<escaped>.*"}&start=&end=&limit=` (server-debounced, 60s client cache as in `MetricQueryEditor.tsx:77-86`) |
| `labelNames(metric)` | `GET /api/promql/labels?cluster=&match[]=<metric>&start=&end=` |
| `labelValues(label, metric, matchers)` | `GET /api/promql/label/{name}/values?cluster=&match[]=…&start=&end=&limit=` |
| `metricMetadata()` | Return `{}` in v1. There is no `/metadata` route. |
| `series(metric)` | `GET /api/promql/series` (used only by the metric browser) |
| `flags()` | Return `{}` |

**Wrapper (about 100 lines, in-repo)**
- **Not** `@uiw/react-codemirror`:
  - Importing it would be a phantom dependency (`.claude/skills/frontend-design-system/SKILL.md:217-229`). The skill's K1/K2 rules also forbid using `@grafana/ui`'s CodeEditor.
  - It pulls in the `codemirror` meta package, `theme-one-dark` and `@babel/runtime`.
  - Its themes use hard-coded colours.
- **Lifecycle:** create the `EditorView` once per ref and destroy it on cleanup (StrictMode double-mounts). Sync the value in from the URL only when it differs. Use `Compartment` for readonly and placeholder.
- **Cluster switch:** `PromQLExtension.setComplete()`, after calling `destroy()` on the old strategy.
- **Keybinding:** `Prec.highest(keymap.of([{key: 'Mod-Enter', run}]))`. The default keymap binds `Mod-Enter` to `insertBlankLine`.
- **Global shortcuts** already skip contentEditable targets (`frontend/src/lib/keyboard.ts:99-103`).
- **Theme:** use `var(--…)` tokens, so it re-resolves on `data-theme` without extra code.
- **Linting:** skip `lintKeymap` and the lint panel, which creates raw DOM buttons at runtime. Show the underline and hover only. Linting is fully offline.

**Air-gapped, CSP and licensing**
- There is no runtime network access beyond our own client (the dist is embedded in the binary, `main.go:60`).
- CodeMirror injects runtime `<style>` tags. A bank-side `style-src 'self'` would need `'unsafe-inline'`, or a nonce via `EditorView.cspNonce`. Coremetry sets no CSP today (Q13).
- The bank's internal npm mirror must carry `@prometheus-io/*`, `@codemirror/*`, `@lezer/*` and `lru-cache@11`. There is no `.npmrc` in the repo.
- Licenses are Apache-2.0, MIT and BlueOak-1.0.0, all permissive. There is no NOTICE or SBOM mechanism today (Q15).
- **Grammar drift:** 0.315.0 lints Prometheus 3.15 syntax. The Thanos PromQL engine version is unknown and is not pinned anywhere in the repo (Q11).

**Tests**
- Test the pure client-adapter seam (URLs, `cluster`, prefix escaping, time bounds).
- Mounting the editor in vitest would need `server.deps.inline` for `@codemirror`/`@prometheus-io`, because of a CJS/ESM dual-instance hazard *(inferred, not run)*.

---

## 9. Proposed Phase 1/2 file plan

### API surface (all under `/api/`, all `RequireAnyRole(editorRoles)` unless noted)

| Route | Notes |
|---|---|
| `GET`/`POST /api/promql/query` | `query`, `time`, `cluster`. POST is `application/x-www-form-urlencoded`. |
| `GET`/`POST /api/promql/query_range` | `query`, `start`, `end`, `step`, `cluster`. This is the first underscore static segment in the repo; the api-route SKILL line 58 says kebab-case. It is kept for Prometheus API parity, as requested, with the reason stated in the file header. |
| `GET /api/promql/labels`, `/label/{name}/values`, `/series` | `match[]`, `start`, `end`, `limit`, `cluster`. Cached 60s. Not audited. |
| `GET`/`DELETE /api/promql/history` | Per-user `saved_views` blob. |
| `GET`/`PUT /api/settings/promql-console` | GET is viewer-readable. PUT is admin-only and audited. |

**Parameters and response shape**
- Parameters follow the Prometheus HTTP API: `time`/`start`/`end` as RFC3339 or unix seconds, and `step` as a duration or seconds. This is a deliberate deviation from the house `from`/`to` nanoseconds (`api.go:3464-3475`), which also falls back **silently** on bad input. The console returns 400 instead.
- Responses use the Prometheus envelope `{status, data, warnings, infos}` plus `meta {clusterId, effectiveQuery, step, stepRaised, totalSeries, truncated, durationMs}`.
- Errors are `{status: "error", errorType, error, position?}`, mapped as follows:
  - `bad_data` → 400
  - `guardrail` → 400
  - `rate_limited` → 429 with `Retry-After`
  - `timeout` → 504
  - canceled → 499
  - upstream unreachable → 502
  - `execution` → 422
- **Never** echo the raw Go error. The `writeErr` default branch returns `err.Error()` (`api.go:11450-11453`), and for transport errors that includes the configured endpoint URL (`thanos_handlers.go:141-143`).

### New files

| File | Purpose |
|---|---|
| `internal/thanos/console.go` | Exported `ConsoleQuery`, `ConsoleQueryRange`, `ConsoleLabels`, `ConsoleLabelValues`, `ConsoleSeries`. Dedicated client with a settings-driven timeout. POST form. Full envelope with warnings, infos and all result types. Error-body decoding. Streaming series cap with a total count. Explicit body cap. Injection into `query` and `match[]`. |
| `internal/thanos/console_test.go` | Table-driven tests with `fakeQuerier`: result types, warnings, 4xx/5xx `errorType`, truncation edges (499/500/501), oversize body, injection into `match[]`, timeout. |
| `internal/api/promql_console_routes.go` | `init()` → `registerRoutesExtra("promql", …)` and `registerPromQLRoutes`. Header comment with rationale. |
| `internal/api/promql_console_handlers.go` | Handlers, parameter parsing, audit, history append, error mapping. |
| `internal/api/promql_console_guard.go` | Pure helpers: `effectiveStep`, range check, per-user concurrency plus rate limiter (package-level state, injected clock), metadata cache key (sorted FNV). |
| `internal/api/promql_console_settings.go` | `promql_console` blob: load, refresh, GET/PUT, validation, reload topic. |
| `internal/api/promql_console_*_test.go` | Guardrail edges (7d ±1s; 11k points boundary; min-step floor), limiter (2 concurrent + 1 → 429; window reset), error mapping, cache-key distinctness and permutation invariance (the `cache_key_test.go` pattern), and handler tests against an `httptest` fake Thanos. **No live calls.** |
| `frontend/src/pages/PromQLConsole.tsx` and `frontend/src/pages/promql/*` | Page shell, URL codec (with vitest), metric browser, history panel, results (table and graph), banners. |
| `frontend/src/features/promql/editorEntry.tsx`, `PromQLEditor.tsx`, `promqlClient.ts` (with vitest) | Lazy CodeMirror wrapper and custom `PrometheusClient`. |
| `frontend/src/lib/queries/promql.ts` | React Query hooks (registered in `cancellation.test.ts`). |
| `docs/promql-console/verify.md` | Phase 3. |

### Touched files (`api.go` untouched)

- **Backend:**
  - `internal/thanos/cluster_matcher.go` and `_test.go` (comment-safe injection)
  - `internal/api/cache.go` (one `case` in `reloadConfigOnSignal`)
  - `internal/api/config_iox.go` (reload topic list)
  - `main.go` (two lines: load and refresh settings)
- **Frontend:**
  - `package.json` and `package-lock.json` (exact pins)
  - `vite.config.ts` (chunk group)
  - `App.tsx`, `Sidebar.tsx` (`minRole`), `CommandPalette.tsx`
  - `lib/types.ts`, `lib/api.ts`, `lib/queries/keys.ts`, `lib/queries/index.ts`, `lib/queries/cancellation.test.ts`
  - `lib/i18n.ts`, `lib/perf/routePrefetch.ts`
  - `pages/AdminAudit.tsx` (`KNOWN_ACTIONS`)
  - `pages/settings/ClustersTab.tsx` (guardrails panel)

### Commit sequence (small and reviewable; each is gated by `go build`/`vet`/`test`, plus tsc/eslint/vitest for frontend commits)

1. **C1** thanos: comment-safe cluster matcher, plus golden tests.
2. **C2** thanos: console client and decoders, plus fake-Querier tests.
3. **C3** api: `promql_console` settings key, GET/PUT, reload topic, plus tests.
4. **C4** api: pure guardrail helpers and limiter, plus table-driven tests.
5. **C5** api: `/api/promql/*` routes (query, query_range, metadata), audit and error mapping, plus handler tests.
6. **C6** api: server-side history, plus tests.
7. **C7** fe: dependency pins, chunk group, single-instance ratchet test.
8. **C8** fe: types, client, hooks, custom `PrometheusClient`, plus tests.
9. **C9** fe: editor wrapper (lazy).
10. **C10** fe: page, route, nav `minRole`, lock state, URL codec.
11. **C11** fe: results (table, graph, legend), banners.
12. **C12** fe: metric browser and history panel.
13. **C13** fe: Settings guardrails panel, audit `KNOWN_ACTIONS`.
14. **C14** docs: `verify.md` (Phase 3).

---

## 10. Risks and open questions for the operator

### Conflicts between the request and existing decisions or code

1. **shadcn DataTable / Button.** Not adopted (`DECISIONS.md:665-684`). The console uses the `ui/` atoms and `useDataTable`. *Please confirm.*
2. **"Existing uPlot TimeSeriesPanel".** It is legacy; the single engine is `CorePanelMulti` (`DECISIONS.md:479-487`). *Please confirm.*
3. **Per-user concurrency and rate limits are per pod** in v1. They multiply by the api replica count, and the ClientIP affinity may be router-skewed. Is per-pod acceptable, or is Redis-backed (adding INCR to `cache.Cache`) required for v1?
4. **500 series vs 8 MiB vs chart budget.** Accept: auto step targets chart width, the body cap is a setting (32 MiB suggested) with an explicit error, and the graph draws 50 series with a "+N not drawn" note plus a virtualised legend table?
5. **Legend shape.** Is a single `name{k="v",…}` string in the built-in legend enough for v1, or do you want label-key columns (the GroupTable pattern) from the start?

### Security and compliance

6. **Minimum role.** I recommend **editor+**. Should it be viewer, to match the existing PromQL and Thanos reads, or admin?
7. **Thanos request parameters.** Send `partial_response=false` explicitly (fail rather than silently return partial data; my recommendation for a bank), or leave the querier default and surface warnings? Leave `dedup` at its default? For a **manual** step, keep `max_source_resolution=auto`, or force raw (`0s`)?
8. **Audit.** Is best-effort `audit_log` (platform-wide drop and loss semantics; 365-day TTL) acceptable, or does the bank need guaranteed delivery or typed columns? That would mean a dedicated table (option B) and/or a platform-wide `s.audit` hardening. Also: surfacing the unread `auditDropCount`?
9. **Injection hardening.** Strip comments, or reject `#`? Is a proper PromQL parser dependency acceptable later? The tokenizer's own note says a new dependency needs justification and approval (`cluster_matcher.go:28-31`). Does any shared querier serve clusters or tenants **not** configured in Coremetry? That decides whether the `#` bypass is a data-exposure issue or only a correctness issue.
10. **Per-cluster RBAC.** Every console user shares each cluster's single service token. Is per-user or per-cluster authorization required (it does not exist today)? Should `NamespaceFilter` apply to console queries? Today it does not, and it is not an authorization boundary.

### Environment facts needed

11. **Thanos version per target cluster.** This decides whether the linter's grammar (Prometheus 3.15) matches the engine, and whether to pin the library to the matching version.
12. **Thanos label and series APIs.** Does the target Thanos honour `match[]` and `limit` on `/labels`, `/label/<n>/values` and `/series`? The server enforces `limit` itself regardless, but the upstream cost differs.
13. **CSP.** Does the bank's ingress or router apply a CSP (`style-src`)? That decides between a nonce and `'unsafe-inline'` for CodeMirror's runtime styles.
14. **URL length.** Is carrying `q` (up to 8 KiB) in the shareable page URL acceptable behind the bank's proxies, or should long queries be shared through a short server-side id (it would reuse the history blob)?
15. **Third-party notices.** Does legal require a NOTICE or SBOM for the new Apache-2.0 and BlueOak-1.0.0 dependencies?

### Design confirmations

16. **Maximum range: reject or clamp?** I recommend **reject** with a structured `guardrail` error (explicit, and the Phase 3 30d check is deterministic). The existing `/clusters` pages clamp silently to 30d. Also confirm the console's 7d maximum is intentionally stricter than that 30d ceiling.
17. **History.** Server-side append for every executed run, 50 entries, a ~128 KiB blob cap, API-token callers excluded: OK? Include failed runs (my recommendation: yes; exclude guardrail rejections)?
18. **Route location.** Keep `/explore/promql`? With the editor+ gate, the `/explore` custom-role prefix leak is harmless. With a viewer gate, the page would also need a `pages.go` entry and a decision on the prefix rule.
19. **Route naming.** Accept `query_range` (underscore) for Prometheus parity against the kebab-case convention?

### Other risks

- The live role check fails open to the token's role when the store is unreachable (`live_authz.go:73-100`).
- The `npm ci || npm install` fallback in the Dockerfile can reintroduce duplicate CodeMirror copies.
- Every result body passes through the api pod's memory. The body cap is the only bound, so a per-pod memory budget should be considered before raising the cap above 32 MiB.
