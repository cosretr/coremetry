# CoSRE v2 — user-scoped toolset for fast error finding: Phase 0 audit (2026-10-30)

Status: **read-only audit — no code changed, no commit.** Snapshot `main` @ v0.10.1661
(worktree). Every claim carries `file:line` from this snapshot. Examples are neutral
(`orders-api`, `KNL123`, `BKRB001`); no customer names.

Brief (operator, summarised): "is there an error in the mobile loan application since
14:00?", "what broke in MY services today?", "channel KNL123 is slow, why?" → the assistant
walks metric → trace → log in any order, inside the user's OWN services, Grafana-Assistant
style (query visible per step, clickable results, one-click `page_url`). Constraints:
build on `POST /api/cosre/v2/chat` (v1 untouched, `api.go` frozen); self-hosted
OpenAI-compatible model; data ONLY via tools, NO generic `query_clickhouse` / `query_vm` /
`query_es`; metrics VM, traces + rollups CH (existing rollup families only), logs ES
(trace_id federation); every tool call = AI SDK tool part, structured results = data parts
added to the shared golden `.sse` fixtures (Go + FE); budgeted loop with a "why I stopped"
part; tool text ≤ 2 KB, the rest via `page_url`; `user_scope` on by default, config-off.

## 1. v2 adapter today

Route `POST /api/cosre/v2/chat` = v1 body (`copilotChatWith`) + a different sink
(`internal/api/cosre_v2_chat.go:28,51,59`); flag `requireCosreV2` → 404 when off (`:38`).
Translator: `internal/cosre/uistream/translator.go`; FE closed set `frontend/src/lib/cosre/protocol.ts:23-43`.

| v1 event | Part(s) emitted | translator.go | In fixtures (`testdata/cosre/*.sse`) |
|---|---|---|---|
| `step{tool}` | `tool-input-start` + `tool-input-available` (`dynamic:true`, id `s<i>`) | :118-119 | v1, error, approval |
| `step{label}` | `data-step{id:s<i>}` | :122 | v1, closed, approval |
| step budget | `data-budget{id:"budget"}` (+ `stopReason` since v0.10.1652) | :129; `chat_loop_steps.go:55-66` | v1, approval |
| `step-result` | `tool-output-available{output}` (never `tool-output-error`) | :154 | v1, error |
| `step-result{reason:approval_required}` | `data-approval{id:a<i>}` and NO output part | :140-150 | approval |
| `delta` / `delta{reset}` | `text-start/delta/end`; `data-reset{transient:true}` | :159-193 | v1, reset, reset2 |
| `answer` | `message-metadata`, `source-url/-document`, `data-links`, `data-followups`, `data-evidence-summary` | :211-248 | v1 |
| `block{type}` (13 types) | `data-block-<type>{id:bN}` | :265; `part.go:21` | v1 (link, chart) |
| `code-lock` / `error` / Close | `data-code-lock`; `data-error{class}`+`error`; `finish{error}` | :61-66, :89-96 | v1, error, closed |
| `done` | `finish-step` + `finish{stop\|error\|tool-calls}` | :68-73 | all |
| unknown kind | `data-unknown` | :274 | — (pinned by `TestKnownKindsAreTranslatedNotWrapped` `translator_test.go:316`) |

**No "structured tool result" data part exists yet.** Tool output reaches the FE only as
`tool-output-available.output` (= v1 step-result: `ok`, preview ≤ 4096 B
`chat_step_preview.go:30`) plus `data-block-*` evidence blocks built server-side from
`mcptools.ToolResultBlocks` (`evidence_blocks.go:309`). The FE folds parts back into v1
`ChatTurn` and draws the v1 `ChatBubble` (`lib/cosre/toChatTurn.ts:1-14`,
`components/cosre/conversation/MessageList.tsx:20`).

| Loop mechanism | Where | Values |
|---|---|---|
| Round / call caps | `copilot_chat.go:62,67`; deep ×1.5 `chat_deep.go` | 5 rounds, 6 calls (8/12 deep) |
| Exchange deadline | `chat_deadline.go:55-65` | clamp(per-call×3, 180 s, 900 s) |
| Per-tool time | `internal/mcp/toolerr.go:220`; CoSRE envelope `cosre_query.go:66` | 20 s; 10 s |
| Tool result → model | `chat_tool_budget.go:41,58` | ≤ 6000 runes (brief wants ≤ 2 KB) |
| Stop conditions (S1) | `chat_stop.go:18-25,53,125` | `round_cap`, `call_cap`, `cancelled`, `token_budget`, `tool_called:<n>`; `defaultStop` |
| Stop reason on wire (S2) | `chatBudget.StopReason` `chat_loop_steps.go:66`; emit `copilot_chat.go:1446` | only on the cap turn (`Exhausted:true`) — a normal "answered" turn carries no reason |
| prepareStep (S3) | `chat_prepare_step.go:54-57,104,126,147` | `prepareChain(capTurnPlan, degenRetryPlan)`; `narrowSpecs` may only narrow |
| Approval (S4) | `chat_approval.go:33-36`; `executor.go:50,88,175`; settings `ai_tool_approval_settings.go:9` (`ai.toolApproval`, default off) | `stopReason=approval_pending`; no tool is gated today |
| Toolset per model | `copilot/profiles.go:578`; `ai/modelcaps/toolset.go:72` | ≤ 12B params → `simple` (hides `query_metric`, `search_logs`, `search_traces`, `list_metric_labels` `tools.go:617`) |

**cosre_signals** (`internal/chstore/store.go:2996`, `kind LowCardinality(String)`, append-only,
180 d TTL): client kinds `copy | link_click | evidence_click | problem_note | followup_click`
(v0.10.1291) + `evidence_relevant | evidence_irrelevant | note` (v0.10.1597/1659)
(`frontend/src/lib/types.ts:8248-8249`); system kinds `prefix_applied | prefix_corrected`
(`chstore/cosre_quality.go:363-367`). Nightly score → `cosre_answer_quality`.

**Model.** Repo pins no model name. Provider `openai` = any OpenAI-compatible
`/v1/chat/completions` (`copilot/copilot.go:11,49`); model comes from the Settings
profile (`profiles.go:50`), exposed only via `ActiveModel()` (`copilot.go:695`). The brief's
`gemma-4-31b-it` is an operator runtime setting, not code; 31B → `ToolsetFull` by the
≤ 12B rule. Evalset harness runs against any OpenAI-compatible base URL
(`internal/copilot/evalset/README.md:10-14`).

## 2. Existing CoSRE tool catalogue — keep / rewrite / drop

One registry feeds chat and the MCP server: `mcptools.ToolList` (`tools.go:373`), 73 chat tools
(golden `internal/mcptools/testdata/toolset_catalog_golden.json` `conditional/chat`) + external-only
`bubble_up` (`tools.go:582`) = 74. Verdicts below are for the **v2 default toolset** (a
`prepareStep` subset, `chat_prepare_step.go:126` can only narrow); the registry itself stays.
Family → pivot-page map is in `docs/cosre/chat-ui-audit.md` §4; the older sweep
(scratchpad `cosre-audit-tools.md`, 2026-10-29) was re-verified: line numbers still hold.

| Family | Tools (file:line) | Input shape | Source | Output | Verdict |
|---|---|---|---|---|---|
| Entity | `resolve_entity` resolve_entity.go:305; `resolve_business_code` business_code.go:266; `list_services` tools.go:852 | free text / code; `name_contains`, `env`, `range_s` | catalogue, `business_code_summary_5m`, `service_summary_5m` | summary rows | **keep** (scope wrap §3) |
| Ownership | `list_teams` team_ownership.go:531; `get_team_services` :597 | `team` (required; "no 'my team' over MCP" :605) | `service_metadata` + `TeamAliases` | RED rows | **wrap** → `list_my_services` (§9) |
| Metrics | `query_metrics` query_metrics.go:224 (`family` enum :245, "NO PromQL" :228); `compare_periods` compare_periods.go:629; `get_service_health` tools.go:1121; `get_operation_health` operation_health.go:109 | enums + service/op/env/window | MetricRouter VM/Thanos/CH (`cosre_query.go:70`); `spanmetrics_1m`, `service_version_5m` | envelope series/rows | **keep** |
| Metrics (free-form) | `query_metric` metrics_tools.go:566 (`name` + `labels` + `aggregation` + `group_by`); `list_metric_names` / `list_metric_labels`; `cluster_metric` signal_tools.go:216 | arbitrary metric name + label filters | VM / Thanos | series ≤ 20×120 | **drop from v2 set** — a PromQL builder in disguise (brief: no generic VM tool); already `simple`-hidden `tools.go:617` |
| Traces | `query_traces` query_traces.go:103 (service/operation/status/min_duration/sort/window); `get_trace` trace_tools.go:637 (≤ 200 spans, `critical_path` :170); `trace_stats`; `find_trace_by_*`; `get_exemplar_traces`; `get_linked_traces` | closed enums | `trace_summary_5m`, `trace_service_index_5m`, raw `spans` bounded | trace list / spans + analysis | **keep**; `get_trace` → wrap as `explain_trace` (§5) |
| Traces (wide) | `search_traces` search_traces.go:44 — `filters[]{key,op∈eq,ne,in,contains,prefix,regex,exists}` on ANY attribute key (:34) | open attribute filter | raw `spans` (gated `search_filters.go:125`) | traces + `deep_link` | **rewrite** → `find_traces` with closed keys (service/op/channel/function/status/min_ms) — regex on arbitrary key is a generic CH filter |
| Logs | `query_logs` query_logs.go:90 (`text` = "plain words, not a query language" :20; `service` required unless `trace_id`); `get_logs_for_trace` pivots.go:93; `get_log_histogram` analysis.go:805; `log_patterns` signal_tools.go:110 | words + entity + window | `logstore.Store` (ES or CH) | rows ≤ 50 | **keep** |
| Logs (passthrough) | `search_logs` logs_tools.go:417 — `query` = "Elasticsearch: Lucene query_string" (:440); `explain_log_spike` log_spike.go:1235 — `query` "in the /logs search language" (:1257) | ES DSL string | ES | rows / composite | **flag — generic ES tool.** `search_logs` drop from v2 set (already `simple`-hidden); `explain_log_spike` keep only with `query` removed from the v2 schema |
| Problems | `list_problems` tools.go:1219 (status/service/env/severity/priority); `get_problem` problem_tools.go:111; `get_problem_root_cause` tools.go:984; `list_anomalies` :1324; `get_correlation_evidence`; `similar_*`; `list_exception_groups`; `get_exception_samples` | ids + enums | `problems`, `root_cause_hypotheses`, `anomaly_events` | rows + hypotheses | **keep** → `get_problems` (§7) |
| Changes | `list_deployments` list_deployments.go:164; `list_deploys` discovery.go:433; `get_correlated_changes` correlate_deploy.go:27; `get_deploy_diff` :107 | service/cluster/ns/range | `service_version_5m`, deploy events, `workload_rollouts`, Argo mapping | rows | **keep** → `get_deployments` (§7) |
| Topology | `get_topology` analysis.go:355; `get_blast_radius` :548 | service | `topology_edges_5m`, `service_callers_5m` | edges | **keep** |
| K8s / infra | `list_namespaces` / `list_workloads` / `list_pods` entity_catalog.go:138/296/514; `get_pod_health`; `get_db_health`; `get_messaging_health`; `list_environments` / `list_clusters` / `list_operations` / `list_slo_status` | names | entity tables, `metric_points`, `db_summary_5m` | rows | **keep, secondary** (not in the default subset; reachable when the model names a pod/db) |
| Composites | `service_health_sweep` service_sweep.go:1928; `pivot_from_trace` pivot_trace.go:1797 | service / trace_id | many | ≤ 6 hop chips + blocks | **keep, secondary** — hops hide steps; brief wants one visible query per step |
| Knowledge / code | `search_knowledge` knowledge_tools.go:330; `get_runbook` :222; `search_wiki` / `read_wiki_page`; `search_code` / `read_source_code`; `product_guide` | text | runbooks (+ inert RAG), ADO | hits | `search_knowledge` **keep** (§8); code/wiki stay on their own tiers, not in the v2 default subset |
| Meta | `build_link` build_link.go:241; `get_capabilities`; `render_chart` tools.go:1555; `set/get/clear_context` context_tools.go:39-91 | — | — | href / spec | `build_link` **keep** (single `page_url` contract :73-82); `render_chart` **drop** (chart becomes a data part); `*_context` **drop** (v2 carries page context in the request) |

**Generic-query check.** No tool accepts raw PromQL / SQL / ES JSON (`query_metrics.go:228`,
`signal_tools.go:217`; `logstore.RawSearch*` is not exposed as a tool). Two tools accept a
query-language *string* (`search_logs.query`, `explain_log_spike.query`) and two accept open
key spaces (`query_metric.name/labels`, `search_traces.filters[].regex`). These four are the
ones the brief forbids for the model; the remaining 69 are parametric.

**Role / identity.** Tools carry no user: `TestMCPToolsNoWriteTool` forces `MinRole==""`
(`no_write_tool_test.go:30`); identity stays in `internal/api` (`copilot_guided.go:3147-3148`).
A user-scope filter therefore has to be applied by the chat loop (`applyScreenContext`
`chat_tool_scope.go:143` is the existing argument-rewrite seam), not inside `mcptools`.

## 3. "My services" scope — what exists

| Link in the chain | Exists? | Where | Note |
|---|---|---|---|
| Directory user → `users.team` | yes | `internal/ldap/ldap.go:431` `teamFor` (`TeamAttribute` department / ou / `dn-ou` + `TeamRegex`); column `store.go:4005`; struct `chstore/user.go:28` | team comes from the user's OWN attribute, **not** from group membership |
| AD group sync (matching-rule-in-chain) | yes, separate | `ldap.go:118-147` `GroupSyncConfig`, filter `:228`; snapshot table `ldap_groups(group_uid, dn, cn, users[])` `chstore/ldap_groups.go:28`; consumer `api/ldap_groupsync.go` | **no group → team mapping anywhere** (grep empty); groups are not used for ownership today |
| LDAP team name ↔ telemetry team name | yes | `system_settings["team_aliases"]` `chstore/settings.go:629-636`; `teamAliasesCtx` `api/team_aliases.go:52`; folding `NormTeamName` | Turkish-İ safe compare |
| Team → services | yes | `service_metadata.owner_team / sre_team (+ *_team_auto)` `store.go:2312-2320`; `mcptools.TeamServiceNames` `team_ownership.go:226` (owner **OR** sre) | Inbox uses owner **AND** sre (`problems_filter.go:115`) — two semantics already coexist |
| User → services (v1 guided) | yes | `copilot_guided.go:3149` `servicesForUserTeam` → `guidedMyTeamBundle` `:3171-3215` (`GetUserByID`, honest empty answer, cap `maxTeamServices`) | identity half lives in `internal/api`; `mcptools` has none (`team_ownership.go:605`) |
| Problems for a service set | yes | `ProblemFilter.Services []string` (`copilot_guided.go:3244`) | `list_problems` tool exposes only singular `service` (`tools.go:1227`) |
| Logs for a team | yes | `logstore.Filter.Team/TeamValues` (`logstore.go:~500`), `TeamRecentLogs(TeamLogsQuery{Values})` `team_logs.go:74` | ES/CH both |
| Argo app → workload | yes | `argocd_app_mapping(cluster_id, namespace, workload, app_name, match_method, confidence)` `rollout_v2_schema.go:235` | maps workload ↔ Argo app, not team; ownership still comes from `service_metadata` |
| Default user scope on tool calls | **no** | `applyScreenContext` `chat_tool_scope.go:143` rewrites `service/env/trace/window` only (no team) | the gap the brief names |

**Cheapest source:** nothing new. `user_scope` = `TeamServiceNames(aliases, metadata, users.team)`
computed once per exchange (two cached reads: `ReadTeamCatalogue` 30 s memo `team_ownership.go:313`,
`GetUserByID`) and injected as (a) a `services[]` argument on every scope-aware tool via the
`applyScreenContext` seam and (b) a `data-scope` part so the FE shows "N of your services".
Group-based ownership (ldap_groups → team) would be a new mapping table — not needed for Phase 1;
revisit only if `users.team` is empty for most users in prep (measure first).

## 4. Channel / function / operation / endpoint → service resolution

| Identifier | Resolver (existing) | Rollup / table read | Key order (point read?) | Cache |
|---|---|---|---|---|
| `CHANNEL_CODE` / `FUNCTION_CODE` (e.g. `KNL123`, `BKRB001`) | `resolve_business_code` `mcptools/business_code.go:266`; `bizIndex` `:218` → `Store.BusinessCodeIndex` `chstore/business_code.go:52`; `BusinessCodeServices` `:81` | `business_code_summary_5m` (AggregatingMergeTree, 30 d TTL) `business_code.go:29-43` | `ORDER BY (kind, code, service_name, time_bucket)` → **code-first point read, no service scope needed** | 5 min in-process map `bizIndexKey` `:199` (~17 K codes, ≤ 300 K names `:217`) |
| `FUNCTION_CODE` → services + facts | `Store.FunctionCodeServices` `chstore/function_code_services.go:121` | `rollup_spans_wide_1m` (≤ window) / `_5m` `:63-67`; raw `spans` fallback `:87` | wide rollup `ORDER BY (service_name, span_kind, status_code, channel_code, endpoint, function_code, ts)` `migrations/0002_rollup_wide.sql:45` → code is **not** a prefix: full-key scan, bounded `LIMIT` + `max_execution_time=5` `:81-82` | none |
| Operation (span name) / endpoint (`http.route`) | `resolve_business_code` kinds `operation` / `endpoint` `business_code.go:12,193` | `cosre_operation_catalog(operation, source, service, function_codes[], call_count, error_count)` `store.go:3047`; refreshed by `StartOperationCatalog` `api/cosre_operation_catalog.go:21` (leader lock) | RMT state, `FINAL` read | catalogue job cadence |
| Oracle operation name → codes | `resolve_oracle_operation` `oracle_operation.go:143` | `oracle_error_log` `chstore/oracle_error_log.go:93` | — | — |
| Free text → service / namespace / workload / pod | `resolve_entity` `resolve_entity.go:305` (scored candidates, cluster-aware) | entity tables + service catalogue | — | catalogue memo |
| Fuzzy service name | `list_services.name_contains` `tools.go:860` (substring → token/fuzzy fallback) | `service_summary_5m` / `service_env_summary_5m` | prefix | `serveCached`-style |

Promoted span columns `attr_channel_code` / `attr_function_code` (both spellings)
`chstore/promoted_attr.go:86-87`; `attr_function_id` only via operator-run
`migrations/0013_function_id.sql:1-14` on distributed prod. The wide rollup reads BOTH case
spellings since v0.9.626 (`0002_rollup_wide.sql:63-76`).

**Gaps.** (1) Channel code has no "facts" path (`FunctionCodeServices` is function-only);
`BusinessCodeServices(kind="channel_code")` gives span/error counts per service but no
latency — latency for a channel needs the wide rollup, where `channel_code` is 4th in the key.
(2) `resolve_business_code` validates `FUNCTION_CODE` shape `^[A-Z][A-Z0-9]{4,9}$` (`:144`);
channel codes are not shape-gated. (3) "mobile loan application" (a product phrase, not a
code) resolves only through `list_services.name_contains` fuzzy or `cosre_operation_catalog`
operation names — there is no product-name → channel/function dictionary; the model must ask
or the operator supplies aliases (operator question Q3 in §10).

## 5. Trace search + Explain Trace as tools

| Capability | Today | file:line | Caveat |
|---|---|---|---|
| Closed-filter trace search | `query_traces` (service / operation / status / min_duration_ms / sort_by / window / from_ms-to_ms / env) | `query_traces.go:103-130` | ≤ 20 rows; Oracle op name → FUNCTION_CODE rewrite |
| Open-filter trace search | `search_traces` (`filters[]` any key, 8 ops; `search` free text on span names; `namespace` / `cluster` / `min_ms`) + `deep_link` | `search_traces.go:44-115`; `TracesDeepLink` `search_filters.go:158` | filter needs a scope (service / ns / cluster or a FUNCTION_CODE / CHANNEL_CODE equality) `:132-134`; indexed keys ≤ 6 h, non-indexed ≤ 1 h, duration sort + filter ≤ 1 h, ≤ 50 rows `:36-38,136-150` |
| Attribute indexing | promoted typed columns (`AttrKeyColumn`) or `attr_kvh` / `res_kvh` bloom hash columns + 4 bloom indexes | `chstore/attr_index.go:53,139`; `filterexpr.go:177`; `filterIndexed` `search_filters.go:105` | distributed prod: `migrations/0014_attr_kvh.sql` operator-run `attr_index.go:91`; `function_id` only via `0013_function_id.sql`; `regex` / `contains` never hit the bloom path |
| Trace fetch | `get_trace`: ≤ 200 spans (errors first, then slowest), `analysis` over ALL spans, `critical_path[]`, `critical_path_steps`, `critical_path_top_external_wait` | `trace_tools.go:637-645,170-174`; `ttCriticalPathSteps` `:228` | span list already heavy for a ≤ 2 KB budget; the model needs the analysis block, not the spans |
| Explain Trace (deterministic route, not a tool) | `POST /api/copilot/explain-trace/{id}` → `buildTraceExplainInput` (root, lite spans, logs block, oracle block, `TopStep`, critical block) → model | `ai_routes.go:116`; `explain_trace_input.go:186,420-431` | tier runs outside the tool loop; result is prose + evidence span ids |
| Critical-path block (new) | `tracechain.CriticalPathOf/From` → `CriticalPath.Block()` plain-text block ≤ `BlockMaxRunes`, steps shrink to fit, closing line always kept | `tracechain/critpath.go:91,100,464-480`; `explain_trace_critpath.go:29-49` | pure, reusable as a tool output; also feeds `sre/facts.go` |
| Trace → logs | `get_logs_for_trace(trace_id, span_id, range_s, limit)` | `pivots.go:93-120` | logstore `Filter.TraceID` (§6) |

**As tools for v2.** `find_traces` = `query_traces` + the two business-code equality filters
(the only attribute filters the brief needs; both are promoted columns → bloom/typed path)
+ `deep_link` from `TracesDeepLink`. `explain_trace` = `get_trace` with the span list dropped
to the top-5 error/slow spans and `CriticalPath.Block()` + `analysis` kept (≤ 2 KB), plus
`page_url=/trace/<id>`; the full Explain Trace route stays a one-click affordance from the
card, not a loop step (operator: classic explain preferred, memory
`feedback-cosre-classic-explain-preferred`). `search_traces` with open `filters[]` is **not**
offered to the v2 model (§2) — regex over `attr_values` at 1B spans/day is the generic CH
scan the brief excludes.

## 6. ES log federation

`logstore.Store` interface (`internal/logstore/logstore.go:495`) is backend-neutral (ES or CH via
`COREMETRY_LOGS_BACKEND`); the brief's "logs ES" is satisfied by calling the interface, never ES directly.

| Need | Method | Filter fields used | Notes (file:line) |
|---|---|---|---|
| by `trace_id` (federation) | `Search(ctx, Filter)` | `TraceID`, `TraceIDs[]`, `SpanID`, `HasTrace` | `logstore.go` Filter struct; tools `get_logs_for_trace` `pivots.go:93`, `query_logs.trace_id` `query_logs.go:109` |
| by entity + time | `Search` | `Service`, `Cluster`, `Pod`, `Namespace`, `Env`, `From/To`, `SeverityMin`, `Limit`, `SinceNs`, `SoftTimeout`, `LeanSource` | env field resolution `es_env_field.go`; service filter test `elasticsearch_servicefilter_test.go` |
| by team (set of services) | `Search` with `Team` / `TeamValues[]`; `TeamRecentLogs(TeamLogsQuery{Values, From, To})` → patterns | `team_logs.go:74-84` | exactly the "my services" shape; returns message templates per service |
| free text | `Search` with `Search` (plain words) | `Filter.Search` | `query_logs.text` is "plain words, not a query language" `query_logs.go:110`; `search_logs.query` is Lucene passthrough `logs_tools.go:440` (excluded §2) |
| histogram / spike shape | `Histogram(ctx, f, bucketSec, groupBy)`; `PatternHistogram` | same Filter | `get_log_histogram` `analysis.go:805` |
| pattern counts (detectors + `explain_log_spike`) | `CountPatterns(pats, curStart, prevStarts, baseStart, now)` — ES: one `_msearch` body `elasticsearch.go:2260-2347`; CH: `clickhouse.go:459` | `PatternSpec` | batched by contract — never per-pattern `_search` (CLAUDE.md pitfall) |
| field discovery | `FieldValues`, `FieldStats`, `Indices` | — | `list_log_fields` `logs_tools.go:699` |
| raw DSL | `RawSearch*`, `EQLSearch` | — | evaluator-only; **no tool exposes them** (grep `internal/mcptools` empty) |

ES rules that bind any new `search_logs` tool (from `docs/INCIDENTS.md` via CLAUDE.md): no
`case_insensitive` on `query_string` (8.x rejects); `significant_text` needs `background_filter`
+ `sampler`; staleTime ≥ server TTL and bounded window rungs on the FE (v0.8.270). The v2
`search_logs` therefore takes `words` + `service(s)` + `severity_min` + `trace_id` + window and
builds the ES query server-side (today's `query_logs` body) — the Lucene string stays off the model.
Result shape already exists: evidence table rows `(ts, severity, source, body ≤ evBlockBodyRunes, trace_id)`
`evidence_blocks.go:197`; `page_url` from `EvidenceHref queryLogs` / `logsHref` (`chat-ui-audit.md` §4).

## 7. Problems / deployments / rollouts access

| Surface | chstore method | HTTP route | Tool today | Scope by service set? |
|---|---|---|---|---|
| Problems list | `ListProblems(ctx, ProblemFilter{Status, NotStatuses, Service, Services[], Env, Limit…})` `problem.go:1419`, filter struct `:1081-1093` | `/api/problems*` (several files, e.g. `problem_affected.go:35`, `problem_insight.go:29`) | `list_problems` `tools.go:1219` — singular `service` only | **yes in store** (`Services[]` used by `copilot_guided.go:3244`), **no in tool** |
| Problem detail / RCA | `GetProblem` `problem.go:1989`, `GetProblemByDisplayID` `:420`; hypotheses via `get_problem_root_cause` (15 s on-demand `tools.go:942`) | `/api/problems/{id}/rootcause/core`, `/bubbleup` `rootcause_progressive.go:51-52`; `/investigation` `sre_status.go:40` | `get_problem` `problem_tools.go:111`; `get_correlation_evidence` `:238` | by id |
| Anomalies / window events | `anomaly_events` | — | `list_anomalies` `tools.go:1324`; `list_problem_window_events` `guided_parity.go:1182` | service |
| Inferred deploys (service.version) | `GetDeploysInWindow` `deploys.go:1216` (MV `service_version_5m` when covered `:903`, else inferred), `GetServiceDeploys` `:1231`, `GetRecentDeploys` `:1160` | `/api/changes` `changes_routes.go:22` (`serveCached` `:41`) | `list_deploys` `discovery.go:433`; `list_deployments` `list_deployments.go:164` (span + operator event + K8s rollout, source-tagged) | service / cluster / ns |
| Deploy ↔ metric correlation | `GetCorrelatedChangesMV(at, windowSec, baselineSec)` `correlate.go:287` (`LIMIT 500`, top-N in Go) ; `ComputeDeployImpact` `deploys.go:259` | `/rootcause` | `get_correlated_changes` `correlate_deploy.go:27`; `get_deploy_diff` `:107` | fleet-wide at a time point; filter client-side |
| K8s rollouts | `workload_rollouts` `rollout_schema.go:52`; `RolloutsForWorkloads` `rollout_problem.go:49`; `RolloutRefsForService` `rollout_problem_telemetry.go:56`; Argo `argocd_app_status / argocd_sync_events / argocd_app_mapping` `rollout_v2_schema.go:180-235` | `/api/rollouts`, `/stats`, `/workloads` `rollouts.go:39-42`, `rollout_workloads.go:36` | folded into `list_deployments` | cluster / ns / workload; service via `RolloutRefsForService` |

**For v2:** `get_problems` = `list_problems` + `services[]` (one filter field already in
`ProblemFilter`; the loop injects `user_scope`), ≤ 10 rows `{id, displayId, priority, service,
kind, openedAt, reason}` + `page_url=/problems?problem=`; `get_deployments` = `list_deployments`
with `services[]` and a `window` enum, ≤ 10 rows `{at, service/workload, from→to version, source}`
+ `page_url=/rollouts?rollout=` / `/changes`. Both read existing tables; no new rollup. Priority
semantics (P1 sticky, reason string) are carried verbatim from the Problem row — the tool must not
recompute them (`/aiops`).

## 8. `search_knowledge` (ADO Search) status

| Item | State | Evidence |
|---|---|---|
| Tool | live; lexical search over runbooks + RAG chunks only; `source ∈ all\|runbook\|document`; ≤ 10 rows, 500-char snippet, 0-1 score | `knowledge_tools.go:330-337`; `get_runbook` `:222` |
| ADO backends (wiki / code / workitem) in the tool | **not implemented** — design approved as audit only | `docs/cosre/search-knowledge-audit.md:3` "awaiting approval"; TL;DR 1-5 |
| ADO client | exists: PAT, Server/TFS detection, code search `_apis/search/codesearchresults?api-version=7.0` | `internal/devops/codesearch.go:296,305`; wiki live search optional `wiki.go:17,563` |
| RAG | **unused by operator decision** (2026-09-25); `document` source inert | `search-knowledge-audit.md:9-13`; memory `project-rag-state` |
| Chat tiers using ADO today | wiki tier (`chat_wiki*.go`), code tier (`chat_code_turn.go`, `search_code` / `read_source_code`) — separate from the tool loop | `copilot_chat.go:751-757` toolset re-resolution |

**Recommendation for the v2 set:** keep `search_knowledge` as-is (runbooks) in Phase 1; ADO
wiki/code backends follow the already-written design (`search-knowledge-audit.md` §3) as its own
slice, gated by the operator's PAT/service-account decision there. No new tool name.

## 9. Proposed tool list (mapped to the brief)

All twelve are thin wrappers over existing handlers (`mcptools` Deps closures) registered as
`mcp.Tool` values with `MinRole:""`; `user_scope` is injected by the loop (§3), so the tools stay
identity-free and MCP-safe. Every result = `{summary ≤ 2 KB, rows ≤ N, page_url, source}` and is
also emitted as `data-tool-result{id:"s<i>", data:{tool, kind, rows, page_url}}` (new part, §10).

| Tool | Exists / wrap / new | Wraps | Data source · rollup | ≤ 2 KB summary shape | `page_url` target |
|---|---|---|---|---|---|
| `resolve_entity` | **exists** | `resolve_entity` + `resolve_business_code` (one tool, `kind` auto) | catalogue; `business_code_summary_5m`; `cosre_operation_catalog` | `{candidates[≤5]{kind, name, score, services[≤5]}, ambiguous}` | `/service?name=` / `/traces?filters=[code]` |
| `list_my_services` | **wrap** | `get_team_services` with team = `users.team` (server-side), `env` | `service_metadata` + `service_summary_5m` / `service_env_summary_5m` | `{team, services[≤20]{name, rps, err%, p99, open_problems}, trimmed}` | `/service?name=` ; `/inbox?team=` |
| `metric_summary` | **wrap** | `query_metrics` (family enum; VM via MetricRouter, span RED via `spanmetrics_1m`) | VM for OTLP metrics; `spanmetrics_1m` for RED | `{family, window, points[≤24]{t, v}, now, prev, delta%, query}` — `query` = PromQL / SQL text shown, read-only | `/metrics?editor=1&tab=promql&promql=` (`evidence_link.go:276`) / `/service?tab=` |
| `compare_windows` | **wrap** | `compare_periods` (`reference` enum, `from_iso/to_iso`) | `spanmetrics_1m` tdigest merge; `service_version_5m` | `{red{cur, ref, delta}, top_operations[≤5], versions[≤3], method}` | `/service?name=&range=custom:` |
| `top_k_by_label` | **new (thin)** | `query_metrics group_by=route\|pod` + `trace_stats group_by` for channel/function | `operation_summary_5m` (route); `business_code_summary_5m` (channel / function); runtime pods | `{label, k≤10, rows{value, count, err%, p95}, total}` | `/endpoints?service=` / `/traces?filters=` |
| `find_traces` | **rewrite** | `query_traces` + CHANNEL_CODE / FUNCTION_CODE equality + `TracesDeepLink` | `trace_summary_5m`, `trace_service_index_5m`; bounded `spans` with bloom path | `{traces[≤10]{id, root_op, entry_svc, ms, spans, error}, has_more, narrowed}` | `deep_link` (`/traces?...`) |
| `explain_trace` | **wrap** | `get_trace` trimmed: `analysis` + `CriticalPath.Block()` + top-5 spans; no 200-span list | raw `spans` by trace_id (point read) | `{wall_ms, extent_ms, critical_path[≤6], top_external_wait, error_spans[≤5]}` | `/trace/<id>` (+ one-click full Explain, `ai_routes.go:116`) |
| `search_logs` | **rewrite** | `query_logs` body (words, not Lucene) + `services[]` + `trace_id` | `logstore.Store.Search` (ES), `Histogram` for shape | `{rows[≤10]{ts, sev, svc, body ≤ 160, trace_id}, total, histogram[≤12]}` | `logsHref` (`/logs?q=&service=&traceId=&range=`) |
| `get_problems` | **wrap** | `list_problems` + `services[]` | `problems` (+ hypotheses on demand) | `{rows[≤10]{id, P, service, kind, since, reason}, total}` | `/problems?problem=` |
| `get_deployments` | **wrap** | `list_deployments` + `services[]` | `service_version_5m`, deploy events, `workload_rollouts`, Argo mapping | `{rows[≤10]{at, target, from→to, source}}` | `/rollouts?rollout=` / `/changes` |
| `search_knowledge` | **exists** | as-is (runbooks); ADO later (§8) | runbooks | `{hits[≤5]{title, snippet ≤ 200, href}}` | runbook href |
| `get_topology` | **exists** | `get_topology` + `get_blast_radius` | `topology_edges_5m`, `service_callers_5m` | `{edges[≤12]{from, to, rps, err%}, callers, callees}` | `/service-map?focus=&range=` (chip lacks focus/range today — fix in wrap) |

Excluded from the v2 default subset (still in the registry): `query_metric`, `search_traces`
(open filters), `search_logs` v1 (Lucene), `explain_log_spike.query`, `render_chart`,
`*_context`, composites, k8s/db/messaging/code/wiki (§2). Subset is applied through a
`prepareStep` plan (`chat_prepare_step.go:54`), so a model that calls an excluded tool in
history still sees its definition (`narrowSpecs` keeps referenced specs `:126`).

## 10. Gaps, conflicts with existing decisions, operator questions

| # | Gap / conflict | Evidence | Resolution proposed |
|---|---|---|---|
| G1 | **No structured result part.** Brief: "structured results = data parts". Today results travel as `tool-output-available.output` (v1 step-result, preview text) + server-built `data-block-*`. | `translator.go:154`; `evidence_blocks.go:309` | add `data-tool-result{id:"s<i>"}` (one part type, `kind` enum inside) — NOT 12 part names; extend `ui-stream-v1.sse` + `.events.jsonl` + `protocol.ts DATA_PART_NAMES` + `TestKnownKindsAreTranslatedNotWrapped` |
| G2 | **"Why I stopped" part.** `StopReason` is emitted only on the cap turn (`Exhausted:true`); a normal answer, a cancelled run or a deadline carry no reason part. | `copilot_chat.go:1446`; `chat_stop.go:18-25` | emit `data-stop{reason, calls, rounds, elapsed_ms}` on EVERY exit (`stop`, `round_cap`, `call_cap`, `cancelled`, `deadline`, `approval_pending`); fixture rows for each |
| G3 | **Budget from config.** Caps are constants (5/6, deep ×1.5); deadline clamp 180-900 s; tool text 6000 runes. Brief: max calls / tokens / time from config, text ≤ 2 KB. | `copilot_chat.go:62,67`; `chat_deadline.go:55-58`; `chat_tool_budget.go:41` | `system_settings["ai.loopBudget"]` `{maxCalls, maxRounds, maxTokens, maxSeconds, toolTextBytes:2048}` read via `prepareStep` input; `stopAtTokens` already exists `chat_stop.go:113`; keep constants as ceilings (settings can only narrow — same contract as `stopAny`) |
| G4 | **v2 answers are drawn by the v1 `ChatBubble`** (2b §10, operator: "v2 must never look poorer"). Grafana-Assistant step cards (query visible, clickable rows) need a renderer the fold-back (`toChatTurn`) cannot express. | `toChatTurn.ts:1-14`; `MessageList.tsx:20`; `phase-2b-plan.md` §10 | render `data-tool-result` as a NEW card family in `components/cosre/evidence/` beside the reused bubble; mockup first (memory: mockup-first for classic surfaces); no v1 change |
| G5 | **2b B6-B7 in flight** (B1-B5 shipped 1647-1660; B6 persistence/history, B7 context parity pending). `requestContext.ts` already sends v1-shaped `context` (service / env / rangeS / explainAsk). | `git log`; `requestContext.ts:25-41` | tool work starts after B6 (persistence must round-trip the new part: `fromPersistMessages` blocks → parts) |
| G6 | **Agent-core S5 pending** (first approval-gated tool `add_problem_note`). The user-scoped toolset is read-only → no approval dependency; `approvalGatePlan` and the scope plan compose in `prepareChain`. | `agent-core-ideas.md` §5; `chat_prepare_step.go:57` | ship scope plan as its own slice; S5 unaffected |
| G7 | **Attribution.** v2 shares `Surface:"chat"` in `ai_calls`; GenAI semconv attrs exist on spans (`gen_ai.system/request.model/usage.*`). | `copilot_chat.go:302`; `chat_span.go:88-167` | `surface = "chat-v2"` via the sink, `ai.loop.stop_reason` span attr (agent-core §2.3); `/ai` filter |
| G8 | **Evalset harness.** `agent_loop.json` cases bind a service and assert tool order; nothing asserts scope or `page_url`. | `evalset/agent_loop.json`; `chat_eval_mode.go:46-58` (`Toolset`, `LoopProbe`) | add `mode:"agent"` cases: "benim servislerimde…" with a fixture `users.team`; assert every call carries `services[] ⊆ scope`; run on prep per README |
| G9 | **Two ownership semantics** (chat owner OR sre; Inbox owner AND sre). | `team_ownership.go:226`; `problems_filter.go:115` | `user_scope` uses OR (chat precedent, "my services" = both roles); document in DECISIONS |
| G10 | **Generic tools exist in the registry** and the external MCP server must keep them (`search_logs` Lucene, `query_metric`). Brief forbids them for the model only. | §2 | subset via `prepareStep`, not deletion; `toolset_catalog_golden.json` gains a `v2` key |
| G11 | **Latency for a channel code** has no MV point-read path (code is 4th in the wide-rollup key). | §4 | Phase 1: counts + error% from `business_code_summary_5m`, latency via `find_traces sort=duration`; a channel-first rollup is a `/clickhouse-schema` decision, out of scope ("no new rollup") |

**Operator questions (recommended default in bold).**

| # | Question | Recommendation |
|---|---|---|
| Q1 | `user_scope` when `users.team` is empty or the team owns no service? | **ask once ("hangi takım?") then run unscoped with a visible `data-scope{unscoped:true}` badge**; never silently widen (v0.9.353 lesson) |
| Q2 | Scope escape: can the user say "tüm servisler"? | **yes, per exchange via the existing `@` scope / `/` command** (`chat_scope.go:132`); config `ai.userScope.enabled=false` disables globally |
| Q3 | Product phrases ("mobil kredi başvurusu") → code / service: dictionary or ask? | **ask + offer `resolve_entity` candidates**; an alias table is a later slice (operator supplies names, never inferred from screenshots) |
| Q4 | Budget defaults | **maxCalls 8, maxRounds 5, maxSeconds 120, toolTextBytes 2048, maxTokens from profile**; settings only narrow |
| Q5 | One `data-tool-result` part or one part per tool? | **one part, `kind` enum** — fixtures and `protocol.ts` stay a closed small set |
| Q6 | Step card shows the query text (PromQL / SQL / ES body)? | **yes, read-only, collapsed by default** (Grafana-Assistant style; evidence envelope already carries `evidence.query`) |
| Q7 | Should v2 default subset drop `search_traces` even for `ToolsetFull` models? | **yes** — open attribute regex is the generic scan; business-code equality covers the brief |
| Q8 | Where do new tools live? | **new files under `internal/mcptools/` (`v2_*.go`), one `registerV2Subset`**; no `api.go` growth, `/mcp-tools` conventions (`range_s`, `clampLimit`) |
| Q9 | Order of slices? | **(1) `data-tool-result` + `data-stop` parts & fixtures → (2) scope plan + `list_my_services` → (3) rewrites `find_traces` / `search_logs` / `explain_trace` → (4) budget settings → (5) FE step cards (mockup first)**; each its own `v0.10.X` |

## 11. Operatör onayı (2026-10-30)

Q1–Q9 öneriler olduğu gibi onaylandı ("ok"). Dilim sırası Q9; dilim 1
(`data-tool-result` + `data-stop` + fixture) 2b B6 main'e indikten sonra başlar.
