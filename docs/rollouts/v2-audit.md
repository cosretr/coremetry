# Rollouts v2: Phase 0 audit (KSM + Argo CD hub + Azure DevOps, Service page Argo card)

**Date:** 2026-09-26
**Status:** Phase 0 deliverable, awaiting operator approval. **No code, config, chart or schema was changed. Work stops here until the decisions in §12 are approved.**
**Scope:** brief items 1–8 (current code, Thanos/workers, KSM series, hub Argo metrics, naming convention, Argo CD API, Azure DevOps, span attributes), the proposed ClickHouse schema, one consolidated operator query pack, and the phase plan.

**Conventions**
- Repo facts cite `path:line` as read at 2026-09-26 22:18 against HEAD `fcd6f5db` (v0.10.946), plus a working tree that other sessions keep editing. HEAD is now `8d250c51` (v0.10.947), a frontend-only diff.
  - **In-flight working-tree files:** the PromQL console reader `internal/thanos/console.go` and `console_test.go`; `internal/api/promql_console_{guard,settings,handlers,routes,history}.go`; a rewritten `internal/thanos/cluster_matcher.go`/`_test.go`; edits to `main.go`, `internal/api/api.go` and `internal/api/cache.go`. `main.go` lines below are working-tree lines.
  - **Known drift at review:** `store.go` moved (`service_version_5m` is now `internal/chstore/store.go:1490-1510`); `cluster_matcher.go` was rewritten (`withClusterMatcher` is at `:448` and `stripComments` at `:236`; the old tokenizer range `:97-201` no longer applies); the `.claude/baselines/api_go_lines` baseline reads 11609.
  - Line numbers drift, so grep the symbol first.
- Upstream behaviour (kube-state-metrics, OpenShift CMO, Argo CD, Prometheus/Thanos, Azure DevOps, OpenTelemetry) is marked **external (docs)** with a URL. Unless stated, these were not re-fetched for this document; they come from the area reports and from `docs/rollouts/argo-annex.md`.
- **S1–S5** are the five area reports prepared for this audit (session scratchpad, not committed): S1 KSM series, S2 Azure DevOps, S3 span attributes, S4 current rollout/report code, S5 schema. Claims marked "per S<n>" come from them and were spot-checked, not all re-verified.
- All names are synthetic: `<hub-thanos>`, instance namespace `<team>-<env>`, metrics job `<team>-<env>-metrics`, suffixes `<suffix-a>`/`<suffix-b>`, target clusters `cluster-a`/`cluster-b`, service `checkout-api`, `<apps-domain>`, `<ado-host>`/`<collection>`/`<project>`/`<repo>`.

**Relationship to other documents**

| Document | Role |
|---|---|
| `docs/rollouts/argo-annex.md` (same date, uncommitted; "the Argo annex") | Detailed Argo annex: full label tables, API field lists, Argo query derivations. **This document supersedes it for approval**; §10.8 lists where this document decides differently. |
| `docs/audits/rollouts-audit.md` (v0.10.189, Turkish, approved 2026-08-30) | v1 design. §2.5 says what changed since. |
| `docs/promql-console/audit.md` §2 | Thanos client capability matrix for the PromQL console. That reader has since landed in the working tree (`internal/thanos/console.go`); §3.4 records how v2 reuses it without changing it. |

---

## 1. Summary

### 1.1 What is verified, and what needs the operator

| Kind of fact | Status |
|---|---|
| Repo facts (§2–§10) | **Verified by reading code** at the baseline above. Load-bearing references from the five area reports and the Argo annex were re-grepped for this document; corrections are folded in. |
| Upstream behaviour (§4–§9) | From public docs/source, marked external (docs). Version-dependent facts are called out. |
| Anything about the live estate | **Unknown. No live query was run.** No connection was made to ClickHouse, `<hub-thanos>`, either target Thanos, any Argo CD instance, Azure DevOps or any cluster; no browser, no dev server. §11 is the query pack the operator runs. |

**Facts only the live estate can answer** (all in §11):
1. Which KSM runs on `cluster-a`/`cluster-b` (CMO default vs minimal profile vs an extra KSM), its version, and therefore which of the brief's series exist (`_created`, RS generation, `status_condition.reason`).
2. Series counts per cluster against today's silent 1000-series cap; whether querier dedup and partial response are on.
3. On the hub: the `namespace`/`exported_namespace` case, Argo CD version per instance, series vs app count, `dest_server` spellings, duplicates, autosync share, sync churn.
4. Naming-convention match rate with the planned env and suffix lists.
5. DeploymentConfig usage and Argo Rollouts presence.
6. Azure DevOps flavour, PAT scopes, whether Argo `repo` URLs point at app repos or GitOps repos.
7. Span attribute coverage per cluster, and the exact form of the derived cluster value (`prod-<cluster-name>` verbatim or prefix-stripped).

### 1.2 Headline findings

1. **v2 inverts the shipped v1 layer.** Today a rollout row is born only from span activity (`internal/rollout/reconcile.go:767`, `DetectedBy: "spans"`); KSM only enriches it (`internal/rollout/ksm.go:113-151`) and `detected_by='ksm'` is never written. v2 makes KSM the event source and spans a tagging/impact input. The v1 reconciler's **scaffolding** (leader lock, inFlight CAS, runs table, cluster registry) is reusable; its **decision core** is replaced (§2).
2. **"Deployment Report" is already gone from the UI.** `/deployment-report` redirects to `/rollouts` since v0.10.201 (`frontend/src/App.tsx:112-125,196`), which is already a live feed (SSE tail, `internal/api/rollouts.go:278`). The rename to "Deployment/Rollouts" is a label change plus a new backing store. The orphaned `GET /api/deployment-report` is still registered (`internal/api/api.go:895`, `internal/api/deployment_report.go:21-22`).
3. **OpenShift's CMO KSM very likely drops the series v1 and the brief lean on.** CMO passes `--metric-denylist` entries including `^kube_.+_created$`, `^kube_replicaset_metadata_generation$`, `^kube_replicaset_status_observed_generation$` and `^kube_.+_annotations$` (external (docs): https://raw.githubusercontent.com/openshift/cluster-monitoring-operator/master/assets/kube-state-metrics/deployment.yaml). v1's `ksm_started_at` (`kube_replicaset_created`, `ksm.go:47`) is then always zero, and ROLLBACK cannot use RS creation time. v2 must detect START/ROLLBACK from **Deployment generation + the RS set diff held in durable detector state** (§4.3, §10.3.2).
4. **A generation bump is not a rollout.** `metadata.generation` also moves on scale (including HPA via `/scale`) and on annotation-only edits (external (docs): https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/apps/deployment/strategy.go). START must be confirmed by a pod-template change: a new RS, a reactivated RS, or a new StatefulSet update revision, decided only after `observed_generation` catches up (§4.4).
5. **Every committed Thanos read path is silently capped at 1000 series and blind to partial responses** (`internal/thanos/client.go:520-523,584-586`; no `warnings` field, `client.go:501`; no `partial_response` parameter). The v1 KSM leg can be truncated today: the `rs_owner`/`rs_spec` selectors return every retained RS (`ksm.go:44-45`), up to 11 per Deployment, so the cap can be hit at ~90 Deployments in the worst case (estimate; §11 K6 measures it). Its own 5000 guard can never fire (`ksm.go:53,62-64`). The PromQL console reader now in the working tree fixes the transport but is capped at 2000 series (`internal/thanos/console.go:99`), so `argocd_app_info` (~40k series) cannot pass through any existing reader. **A worker entry over the console transport is the first commit** (§3.4).
6. **Metrics alone cannot say who synced or which revision.** No Argo metric carries an initiator or SHA; `argo_auto`/`argo_manual` and the Git revision need the Argo API per instance (§5, §7). Because most apps have `autosync_enabled="false"`, a sync on such an app can only have been started by a user or API client, so a metrics-only mode still yields a useful **estimated** `argo_manual` (§7.5).
7. **Nothing Argo exists in the codebase.** The only mentions are two Sidebar comments (`frontend/src/components/Sidebar.tsx:96,100`). `ClusterConfig` has no `api_server_url`, `argo_suffix` or `pair_group` (`internal/thanos/client.go:43-89`).
8. **Azure DevOps: a read-only client exists, but none of the lookups v2 needs.** It serves one collection with a plaintext PAT (`internal/devops/client.go:86-89`) and a hard-coded "Code: Read" hint (`client.go:681`). There is no commit-by-SHA, PR, build or release call, no rate limiting, no `enrichment_status`, and no `get_change_history`. `search_knowledge` does not use this client (§8).
9. **Spans: `service.version` has no column, and impact is not cluster-scoped.** The deploy chain already prefers `container.image.tag` over `service.version` (`internal/chstore/deploys.go:87-104`). Before/after RED in the drawer ignores the cluster (`internal/api/rollout_detail.go:162,166`), while a cluster-scoped reader exists (`internal/chstore/summary.go:570`). The span `cluster` value is now derived from `deployment.environment.name`; that is a join risk (§9.3).
10. **Schema: new tables, not an extension of `workload_rollouts`.** Its key `(cluster_id, namespace, workload, revision, started_at)` is span-derived (`internal/chstore/rollout_schema.go:77`) and ClickHouse cannot re-key a table. The four named tables are proposed, plus four the analysis shows are needed: detector state, classification, commit enrichment, worker runs (§10).

### 1.3 Top risks (ranked)

| # | Risk | Consequence if ignored | Mitigation (where) |
|---|---|---|---|
| 1 | Silent 1000-series truncation, 8 MiB body cap, invisible Thanos partial responses | Missed rollouts on large clusters; Argo apps "disappearing"; wrong deletions | `WorkerQuery` over the console transport with explicit caps, `Total`/`Truncated`, warnings, `partial_response=false`; skip the diff on any partial (§3.4, P1.1) |
| 2 | CMO denylist (`_created`, RS generation, annotations) | No RS age, no incarnation timestamp, no Argo tracking annotation via KSM | Durable detector state (§10.3.2); §11 K1 confirms per cluster |
| 3 | Generation bump ≠ rollout | False events on every HPA scale | Template-change gate after `observed_generation` (§4.4); §11 K5 measures the noise ratio |
| 4 | App ↔ workload mapping ambiguity (many apps per `(dest_server, dest_namespace)`, naming not guaranteed) | Wrong "who/what" attribution | Match method + confidence on every edge; only exact/estimated classify; weak ⇒ `unknown` (§6, §10.3.5) |
| 5 | Three identity spaces (span cluster value derived from env, KSM external label, Argo `dest_server`) linked only via the Remote Cluster registry | Cross-cluster mis-joins, especially for active-active pairs | Always key on `EffectiveID()`; new `apiServerUrls`; registry-owned span values (§3.2, §9.3) |
| 6 | Split-brain (`lockDegraded` ⇒ every pod is leader, `main.go:591`) | Duplicate writes; N× load on Argo CD and Azure DevOps | Source-derived keys (`generation`, `op_started_at`, `(repo_url_norm, sha)`) are deterministic. Tick-derived ones (`incarnation_at`, `changed_at`) allow at most one duplicate under split-brain. Skip the API and enrichment workers while degraded (§3.3, §10.3.1) |
| 7 | Secrets delivery | Tokens cannot be mounted: the chart has no `extraEnv`/`envFrom`/volumes for Coremetry pods; the DevOps PAT is plaintext | Chart commit P1.6; secretref for Argo tokens (and the DevOps PAT, decision 20) |
| 8 | Argo CD write paths hidden in read APIs (`refresh=` parameter; resource-tree cache miss) | Violates "read-only everywhere" | Client guard: never send `refresh`; no resource-tree calls (§7.1) |
| 9 | DeploymentConfig invisible to every current path | DC workloads get no events | Explicit "not covered" state, or an optional DC slice (§4.7, decision 12) |
| 10 | Hub cluster-label injection and the `namespace` case | Zero Argo series, or wrong instance attribution | §11 H0.3/H1.2 before P3; explicit injection switch for the hub reader (§5.6) |

### 1.4 What approval unlocks

Approval of §12 unlocks **P1 only**: foundations with every flag off (reader, settings, chart, schema). P2 (KSM detector) additionally needs the §11 K and D answers for both target clusters. P3 (Argo) needs the §11 H and N answers.

---

## 2. Current rollout/deployment code: keep, adapt or replace (brief item 1)

### 2.1 Where the brief's premises differ from the repo

| Brief premise | Repo reality | Effect on the plan |
|---|---|---|
| "Deployment Report becomes Deployment/Rollouts (live event feed)" | Retired in the frontend since v0.10.201 (`frontend/src/App.tsx:196`): the redirect converts `?since=` into `?range=custom:…` and drops `ownerTeam`/`sreTeam`/`owner`/`sre` (`App.tsx:115-125`); `/rollouts` has no team filter. `/deploys` is also retired (`App.tsx:176-177`). The sidebar entry `/rollouts` (`frontend/src/components/Sidebar.tsx:82`, `nav.rollouts` at `frontend/src/lib/i18n.ts:41,196`) is already a live feed | Label change plus a new store. Keep `/rollouts` and every URL parameter |
| Rollout detection is "span/ReplicaSet detection" | Rows are created from the span MV `workload_revision_activity_1m` only; KSM enriches (`reconcile.go:767`; `ksm.go:113-151`) | KSM-first is a new detector, not a tweak |
| The Deployment Report detects deploys | It never did: it lists open Problems after a user-entered `since` plus before/after RED (`deployment_report.go:127-252`, per the S4 area report) | Its reusable helpers already serve the drawer |

### 2.2 Inventory and verdicts

| Component | What it does today | Verdict | Why |
|---|---|---|---|
| `GET /api/deployment-report` (`internal/api/api.go:895`, `internal/api/deployment_report.go:21-22`) | Problem-centric report. No frontend caller; only `docs/DEPLOYMENT-REPORT-TESTING.md` and a seed script | **Retire the route** (P6). Move the helpers `rollout_detail.go` reuses (`redComparisonWindow`, `redStatsFor`, `ServiceReportSection`) into the rollouts files | The v1 audit planned "one more version" (`docs/audits/rollouts-audit.md:193`); that is overdue |
| `/api/rollouts`, `/api/rollout`, `/api/rollout/detail`, `/api/rollouts/stats`, `/api/rollouts/runs`, `/api/settings/rollouts` (`internal/api/rollouts.go:35-47`) | `workload_rollouts FINAL` reads, flag gate returning 404 `{disabled:true}` (`rollouts.go:52-61`), per-pod SSE tail (`rollouts.go:278`) | **Keep routes, response shape and SSE kind; switch the backing store** to `rollout_events` behind a setting (P2.3). The API maps v2 statuses to the v1 vocabulary (`progressing`→`in_progress`, `succeeded`→`completed`, `stuck`→`stalled`; `rolled_back`/`superseded` unchanged; `frontend/src/pages/Rollouts.tsx:38`), so `?status=` links and stats stay valid | Keeps deep links, the tail pattern and `TestRolloutKeysCarryEveryInput` |
| `internal/api/rollout_detail.go` (drawer) | Rollout → services via the MV → RED/problems/anomalies; RED is not cluster-scoped (`:162,166`) | **Keep, adapt**: cluster-scoped RED via `GetServicesEnvAggFiltered` (`internal/chstore/summary.go:570`), anchored at the KSM start | Spans = impact only |
| `internal/api/rollout_problems.go` | Problem-count badge per row via the MV service mapping | **Keep** | Service ↔ workload mapping stays span-derived |
| `GET /api/services/{name}/deploys` (`api.go:756`) | Span version first-seen ∪ `events(kind='deploy')` | **Adapt**: source becomes `rollout_events` joined to the service; the span version becomes a label or a non-K8s fallback | Many consumers (ServiceCharts, Incident, annotations) |
| `GET /api/services/{name}/deploy-history` (`api.go:759`) | Span deploys + impact; no frontend caller (per S4) | **Retire** (P6) | Dead |
| `GET /api/services/{name}/rollouts` (`api.go:762`) | **Pod-churn detector on raw spans** (per S4: `GetServiceRollouts`, `analyzeRollouts` in `deploys.go`); history of operator-reported false positives | **Replace** with a service-scoped read of `rollout_events` (new route in its own file); retire the old one in P6 | Event detection from spans is exactly what v2 forbids |
| `ComputeDeployImpact` (`internal/chstore/deploys.go:259`) | Before/after RED, service-only | **Keep, adapt**: add a cluster filter and read the MV family (CLAUDE.md invariant 3) | Spans = impact |
| `effectiveVersionExpr` (`deploys.go:87-104`), `service_version_5m` (`internal/chstore/store.go:1490-1510`) | Running version from span attributes, image tag first | **Keep as version tagging** | Brief: spans tag versions |
| Span-inferred deploy funnels (`recentDeploysInferred`, `deploysInWindowInferred`, `serviceDeploysInferred`, per S4) | Deploy **events** from span version first-seen | **Replace as event sources**; keep as a fallback for non-K8s (JBoss) services only if decided (decision 13) | Event detection from spans |
| `fetchDeploysByService` (`internal/chstore/problem_telemetry.go:66`) | Raw-span deploys feeding `Problem.RecentDeploy`, which is shown as the ProblemDetail DeployBox and in chat/MCP context. It has not affected priority since v0.9.612 (`internal/chstore/problem.go:486-501`) | **Replace the source** (P2.5); display-only, no priority gate | Open 🔴 from the v1 audit (`rollouts-audit.md:189`) |
| `internal/rollout/reconciler.go` | Leader-gated tick, inFlight CAS (`:150,234`), previous rows FINAL (`:331`), one upsert (`:395`), runs table | **Keep the scaffolding**, as the pattern for the new detector | Proven failover behaviour |
| `internal/rollout/reconcile.go` | Span "observed entry" state machine with hysteresis | **Replace** with a pure KSM generation/revision detector | Wrong source |
| `internal/rollout/ksm.go` | RS-only KSM leg: `owner`, `spec`, `ready`, `created` (`:42-49`) | **Adapt**: reuse the `KSMSource`/`JoinKSM` seam shape, not the queries | Denylist, truncation, no nsMatcher (§4) |
| `internal/rollout/score.go` | Time bands 0–30 min → 0.90, 30–120 min → 0.50; status multipliers; +0.05 KSM bonus (`:3-18`) | **Keep the bands; adapt** the status mapping and drop the KSM bonus (every v2 event is KSM) | §2.3 |
| `workload_rollouts`, `workload_revision_activity_1m`, `rollout_reconcile_runs` (`rollout_schema.go:52-97`) | v1 state, MV, run log | Table: **read-only until TTL**, then retire. MV: **keep** (the only service ↔ workload mapping). Runs table: v1 only | §10.1 |
| `frontend/src/pages/Rollouts.tsx` | Feed, stats tab, drawer; "Kaynak" column = `detectedBy` (`:64,211`) | **Keep, adapt** columns (Argo app, revision, who, trigger, commit/PR) | §12.2 naming conflict |
| `DeployHistoryPanel` (Service → Details, `frontend/src/pages/Service.tsx:643-655`), `useServiceRollouts7d` (`frontend/src/lib/queries/services.ts:113`) | Pod-churn data under "Runtime & rollouts" | **Replace the data source** (P2.3) | |
| Other pod-churn consumers of `/services/{name}/rollouts` / `GetServiceRollouts`: the version chip in `frontend/src/pages/service/DetailsPropsStrip.tsx:39`, annotations (`internal/api/annotation_routes.go:78`), copilot service charts (`internal/api/copilot_service_charts.go:99`), and the ServiceCharts markers (per S4) | Pod-churn rollouts from raw spans | **Adapt** to the service-scoped `rollout_events` read | Must move before P6 retires the old route |
| MCP `list_deploys` (`internal/mcptools/discovery.go:418`) and `get_deploy_diff` (`internal/mcptools/correlate_deploy.go:109`) | Span-inferred deploys; before/after RED | **Adapt** the source to `rollout_events`; tool names and argument schemas are kept (§2.4) | P2.4 |
| MCP `get_correlated_changes` (`correlate_deploy.go:29`) | Ranks services by RED change; not deploy-based | **Keep** | |
| Root-cause fuser: `RolloutCandidate` (`internal/correlator/hypothesis.go:173`) and the Tier-1 deploy fold | A span deploy and a v1 rollout row fold into one candidate when versions match | **Adapt** to one KSM-sourced candidate | P2.4 |
| `frontend/src/components/RootCausePanel.tsx:367-373` | Hard-codes `service.version=` in the deploy headline | **Adapt** to `version_tag` | P2.4 |
| Service page Argo card | Does not exist | **New** (P3.5) | |

### 2.3 Problem correlation touchpoints

| Touchpoint | Today | v2 |
|---|---|---|
| `internal/anomaly/rollout_causes.go` (window `[onset−120m, onset+5m]`, `:32-38`) → `RolloutsForWorkloads` → `rollout.Rank` | Reads `workload_rollouts` | **Keep the flow, swap the table** to `rollout_events` (P2.4) |
| `chstore.RolloutEvidence` (`internal/chstore/rootcause_hypothesis.go:70`) | Has `DetectedBy`, no trigger | Add `Trigger`, `ArgoApp`, `Revision`, `Initiator`, plus `WorkloadKind`, `Generation`, `IncarnationAtNs` for v2 links (§2.4) (additive JSON; persisted evidence stays readable) |
| `rollout.Score` status multipliers | `stalled`/`rolled_back` ×1.10, `superseded` ×0.70 | Map `stuck` → ×1.10, `change_type=rollback` → ×1.10; optional trigger multiplier (for example `out_of_band` ×1.10) is decision 17 |
| "What changed" evidence from span-inferred deploys (fusion, behaviour scan, exception context, copilot guided/shift, per S4) | Spans ∪ events | Adapt to `rollout_events` + Argo syncs in P2.4 |
| `list_deployments` / `/api/changes` | Inferred + event + rollout merge | KSM/Argo first; fix the `rollouts_layer` defect (§2.6) |
| `Problem.RecentDeploy` → DeployBox (display only) | Spans only (`problem_telemetry.go:66`) | P2.5 |

### 2.4 Contracts that must stay backward compatible

- **URLs:** `/rollouts` with `?tab`, `?status`, `?cluster`, `?namespace`, `?range`, and `?rollout=` (5-part codec, per the Argo annex §2.1). The `/deployment-report` and `/deploys` redirects, and the Service `#deploys` anchor.
- **APIs:** all `/api/rollout*`, `/api/settings/rollouts`, `/api/admin/rollout-layer/*`, `/api/changes`, `/api/services/{name}/deploys|rollouts`, `/api/operator-events` (the pipeline ingress).
- **Persisted data:** `RolloutEvidence` JSON inside root-cause hypotheses, the cause subject `rollout:<c>/<ns>/<wl>@<rev>`, and `workload_rollouts` history until TTL. **Old `?rollout=` links resolve against `workload_rollouts` until it expires**; new links use the `rollout_events` key (decision 14).
  - Today's decoder rejects anything that is not 5 parts (`frontend/src/lib/rolloutRow.ts:158-170`). v2 links are 6-part: `cluster|namespace|kind|workload|incarnationAtMs|generation`. The decoder dispatches on part count (5 → `workload_rollouts`, 6 → `rollout_events`).
  - `RolloutEvidence` gains additive `workloadKind`, `generation` and `incarnationAtNs`, so `rolloutEvidenceHref` (`rolloutRow.ts:178`) can build v2 links.
- **Other:** SSE kind `rollout`; settings key `system_settings["rollouts"]`; table layout keys; MCP tool names and argument schemas.

### 2.5 What changed since `docs/audits/rollouts-audit.md` (v0.10.189)

| v1 audit item | State at v0.10.946 | Implication for v2 |
|---|---|---|
| Span detector primary; KSM "Faz 5" as second evidence | Shipped v0.10.193–v0.10.215 (`rollouts-audit.md:471-488`): promoted columns, `workload_rollouts`, MV, reconciler, API + SSE tail, `/rollouts`, drawer, STS/DS image-tag revisions, KSM slice 1, evidence gate | **Hierarchy inverted.** Reuse scaffolding, replace the decision core |
| §7: the KSM PromQL probe must be run before Faz 5 | **Never run.** The gate moved into code; prod RS metrics are "BELGELENMİŞ DEĞİL" (not documented) (`rollouts-audit.md:496-503`) | §11 K is that probe, now a **hard prerequisite for P2** |
| §6: new KSM reads go through `promapi` with a per-call `maxSeriesParsed` and a `truncated` flag (`rollouts-audit.md:252,255`) | Not done: the KSM leg uses `thanos.InstantSamples` (`main.go:2220-2242`), so the silent 1000 cap applies. `promapi` only gained `Total`/`Truncated`/`IsPartial` in v0.10.944 (`internal/promapi/meta.go:3-18`) | Reader decision §3.4: `thanos.WorkerQuery` over the console transport, not `promapi` |
| §6.1: callers add `nsMatcher`; export `NamespaceMatcher` (`rollouts-audit.md:251`) | Not done for the KSM leg (`ksm.go:42-49`) | Fixed in the v2 detector (P2.2) |
| §13.3 `cluster_id = EffectiveID()`; §13.4 deterministic `started_at` | Shipped (`rollout_schema.go:26-28,39-41`) | Carried into v2 |
| §4: `/api/deployment-report` lives "one more version" | Still registered (`api.go:895`) | Retire in P6 |
| §4 🔴: problem deploy enrichment is spans-only (`rollouts-audit.md:189`) | Still open (`problem_telemetry.go:66`). The 🔴 was raised about P1 impact, but `RecentDeploy` has been display-only since v0.9.612 (`problem.go:486-501`) | P2.5 is a display-source switch, not a priority change |
| §4 item 5: `rolloutDeployEntries` with event > rollout > span priority (`rollouts-audit.md:196`) | Never shipped (grep over `internal/` and `frontend/src` finds nothing) | P2.4 delivers it with KSM rows |
| §10: `docs/operator/rollouts-collector.md` | Never written (`docs/operator/` holds only `pending-actions.md`) | P5.3 |
| §1.5: K8s Coverage `rs`/`img` counters, split `clus` | Shipped (`internal/chstore/k8s_coverage.go:131-146`) | Add `service.version`, `container.image.tag`, `deployment.environment.name` (P5.3) |
| STS/DS `controller-revision-hash` out of scope (`rollouts-audit.md:469`) | STS/DS revision = image tag (v0.10.211); STS/DS KSM readiness still open (`rollouts-audit.md:488`) | v2 uses `kube_statefulset_status_update_revision` (§4.6) |
| Environment assumption: collector sets a static `k8s.cluster.name` (`rollouts-audit.md:401-404`) | **New fact:** the cluster value is derived from `deployment.environment.name` (`prod-<cluster-name>`) | Join risk §9.3 |
| Thanos plaintext token | `TokenRef` (`env:`/`file:`) shipped v0.10.272 (`internal/thanos/client.go:78-81`) | Reuse `internal/secretref` for Argo tokens |
| New endpoints: one line in `api.go` | `registerRoutesExtra` (`internal/api/route_registry.go:30`); ratchet baseline 11662 at HEAD; the working tree only ratchets down (11609 at review) (`.claude/baselines/api_go_lines`) | Every v2 route in its own file; **`api.go` +0 lines** |
| Operator note "maybe from Thanos or Argo later" (`Sidebar.tsx:96-100`) | Still no Argo code | v2 delivers it |
| v1 layer in prod | **Live:** upgraded to v0.10.212 on 2026-08-31, with the 0012 wizard and Enable run (`rollouts-audit.md:493-494`) | P2.3 and P6 act on a live prod layer; `0015` must be applied via the wizard before `rollouts.source=v2` |
| Evidence gate (v0.10.213/215, `rollouts-audit.md:525-557`) | Bootstrap produced fake "completed" rows for weeks-old RSs; weak evidence must not veto strong evidence | Basis for "first run = baselines only" (§10.3.2) and decision 10 |

### 2.6 Defects found along the way (not fixed here)

1. **`list_deployments` reports a healthy rollouts layer as degraded.** It emits `"ok"` or `"unavailable"` (`internal/mcptools/list_deployments.go:117-122,140`), but the renderer tests for `"on"` (`:200,230`). The guided RCA text then says rollouts were not listed. Fix in P2.4.
2. **The `NamespaceFilter` comment is wrong.** `internal/thanos/client.go:82-85` says the filter is "injected into every query"; `doQueryWith` injects only the cluster label (`client.go:538-546`).
3. **The v1 KSM leg is truncated and cannot tell "no series" from success** (§4.10); only `ksm_ms` is recorded (`rollout_schema.go:92`).
4. **The drawer's RED and `ComputeDeployImpact` are not cluster-scoped** (`rollout_detail.go:162,166`).
5. The flag-off message says "Settings → Rollouts → Enable" (`internal/api/rollouts.go:59`), but no Rollouts tab exists in Settings (per S4). The same text also appears at `rollouts.go:128` and `frontend/src/pages/Rollouts.tsx:164`.
6. **Stale comment:** `internal/chstore/problem_telemetry.go:442-448` still describes a "critical + deploy → P1" branch that `problem.go:486-501` removed in v0.9.612. Fix it in P2.5.

---

## 3. Thanos client, Remote Cluster config, workers and leader election (brief item 2)

### 3.1 Thanos client today

| Capability | `internal/thanos` (`doQuery`) | `internal/promapi` | `internal/thanos/console.go` (working tree) | Still needed by v2 workers |
|---|---|---|---|---|
| Transport | GET only, URL-encoded (`client.go:556-557`) | GET only (`internal/promapi/promapi.go:231`) | **POST form** ✓ (`consoleCall`, `console.go:492`; label values stay GET) | Nothing: reuse |
| Timeout | Shared 15 s clients (`client.go:470,487`) | Shared 15 s clients (`promapi.go:169,186`) | Dedicated client per call, 30 s default, 120 s ceiling (`console.go:96-97`); shared clients untouched | 30–45 s via `WorkerLimits` |
| Series cap | 1000, **silent** (`client.go:520-523,584-586`) | 1000 fixed, but `Total`/`Truncated` reported (`internal/promapi/meta.go:29-64`) | **Streaming cap with a total count** ✓, but ceiling 2000 (`console.go:99`, enforced in `normalized()` `:121-133`) | ≤ 50 000 through `WorkerLimits`, without raising the console ceiling |
| Body cap | 8 MiB `LimitReader` → a later decode error (`client.go:572`) | Same (`promapi.go:83,244`) | Explicit `response_too_large` ✓ (`cappedReader`, `console.go:748`); ceiling 128 MiB (`:101`) | ≤ 64 MiB through `WorkerLimits` |
| Result types | Vector/matrix (`client.go:501-518`) | Vector/matrix (`promapi.go:58-72`) | All four ✓ (`console.go:986`) | Nothing |
| Warnings / partial | Dropped | VictoriaMetrics `isPartial` only (`promapi.go:63-66`) | `warnings`/`infos` ✓; `partial_response` sent explicitly (`console.go:398,409`); `dedup` deliberately not sent (`:48-53`) | `dedup=true` per call; warnings ⇒ `Partial` |
| Error body | `HTTP n: <first 200 bytes>` (`client.go:573-576`) | Typed `HTTPError`, 401/403 → `sourcestate.ErrUnauthorized` (`meta.go:116-135`) | JSON `{errorType,error}` decoded ✓ | Nothing |
| Cluster matcher injection | Yes, `query` only (`client.go:538-546`; `withClusterMatcher`, `internal/thanos/cluster_matcher.go:448`) | No | On `query` **and** `match[]` ✓ via exported `ClusterConfig.EffectiveQuery`/`EffectiveMatchers` (`cluster_matcher.go:470,481`); `#` comments handled (`stripComments`, `:236`) | Only `NamespaceMatcher` still needs exporting (unexported `nsMatcher`, `internal/thanos/promql.go:42`) |
| Auth | Bearer; `TokenRef` resolved in `Configure` (`client.go:347-383`), unexported `effectiveTokenFor` (`:386`); an unresolved ref sends **no** header (`:562-564`) | Caller passes `Token` (`promapi.go:198-206`) | Same `effectiveTokenFor`; an unresolved ref still sends no header (`console.go:516`) | **Fail-closed** on an unresolved `TokenRef` in the worker entry |
| Label/series APIs | None (per the PromQL console audit §2) | `QueryStrings` (`promapi.go:264`) | ✓ `ConsoleLabels`, `ConsoleLabelValues`, `ConsoleSeries` (`console.go:293,314,338`) | Nothing (label-name probes can use them) |

### 3.2 Remote Cluster config

- One blob `system_settings["thanos_clusters"]`, list of `ClusterConfig` (`client.go:43-89`): `ID` (immutable, `"c-"+fnv(Name)`, `internal/thanos/cluster_identity.go:47`), `Name`, `URL`, `ThanosLabelName/Value` (shared-querier model; empty = per-cluster URL), `SpanClusterValue(s)`, `AuthType`, `Token`, `TokenRef`, `NamespaceFilter`, `InsecureSkipVerify`, `Enabled`. Refreshed every 30 s via `cfgRefresh` (`main.go:520,1033`).
- `ClusterByID` returns **enabled entries only** (`cluster_identity.go:159`).
- **Missing for v2:** `api_server_url`, `argo_suffix`, `pair_group`; nothing marks the Argo hub.
- **Proposal (P1.3):** additive `omitempty` fields `apiServerUrls []string` (a list: `dest_server` spellings vary, and single values have grown into lists before, `SpanClusterValue` → `SpanClusterValues`, `client.go:56-64`), `argoSuffix string`, `pairGroup string`. **Sites that must change together**, or a Save or a mixed-version pod wipes the fields: `ClusterSnapshot` and `Snapshot()` (`client.go:99,396`); `ReconcileClusterSettings` carry-forward (`cluster_identity.go:193`); `checkClusterUniqueness` (normalised URL and suffix unique; `:256`); `putThanosSettings` validation and audit details (`internal/api/thanos_handlers.go:753,835`); the other blob writers `detect` and `assign-span-cluster` (`internal/api/thanos_identity.go:39-42`); `ClustersTab.tsx` (`EditRow :26`, `fromSnapshot :50`, `EMPTY_ROW :67`) and `ThanosClusterSnapshot` (`frontend/src/lib/types.ts:1974`). Viewer surfaces get `pairGroup`/`argoSuffix` through an additive field on `GET /api/clusters/sources` (`api.go:633`, `thanos_handlers.go:727`), because the settings GET is admin-only (`api.go:1188`).
- **Normalisation** (pure helper): lower-case scheme and host, strip a trailing `/`, and apply an explicit default port rule; `https://kubernetes.default.svc` is valid only for the hub's own instances. **As built (v0.10.956):** it is rejected in `apiServerUrls` in every spelling — with two hubs it can only be resolved through the instance's `hubClusterId`.

### 3.3 Workers and leader election

| Mechanism | Fact | v2 use |
|---|---|---|
| Lock | `cache.LeaderHolder`, one key per worker ("different pods can lead different workers", `internal/cache/leader.go:48-52`) | New keys: `rollout-detector`, `argocd-metrics`, `argocd-api`, `ado-enrichment` |
| Lease | `LeaderTTL(interval) = clamp(3×interval, 30s, 10m)` (`leader.go:87-96`); rollout uses `LeaderTTL(time.Minute)` = 3 min (`main.go:1181`) | Same for all v2 workers |
| First tick | `SetOnAcquire(Tick)` + `inFlight` CAS (`main.go:1183`; `reconciler.go:150,234`) | Same |
| Write safety | Leadership re-checked before the upsert (`reconciler.go:389-394`) | Same; plus deterministic keys |
| Wiring | Inside `if mode.worker {` (`main.go:1078`), cloned from the rollout block (`main.go:1174-1185`) | One block per worker |
| Degraded lock | Redis configured but unreachable ⇒ always-leader Noop on **every** pod (`main.go:586-597`) | Idempotent tables tolerate it; **the API and enrichment workers skip while degraded** (external load would multiply by pod count) |
| Visibility to api pods | Worker memory is invisible; the precedents persist status (`rollout_reconcile_runs`; `thanos_label_checks`, `internal/thanos/cluster_detect.go:169`) | `rollout_worker_runs` table (§10.3.8) |
| Parallelism | Entity syncer `ParallelClusters` default 4 (`internal/entity/settings.go:33,61`) | Same bound for per-cluster KSM fetch |
| Problem stale sweep | Resolves open problems not touched within 3 × evaluator interval (`internal/evaluator/evaluator.go:1117`), except poller-owned subjects (`internal/chstore/problem.go:1513`) | Only matters if v2 emits Problems (decision 18) |

### 3.4 Reader decision (recorded by the integrator; approval item 1)

**Context.** `internal/promapi/promapi.go:21-26` states the house rule: `promapi` is the shared Prometheus-API core, new callers use it, and `internal/thanos` `doQuery` is not refactored. Rollouts v2 (KSM detector, Argo metrics worker) needs POST, warnings, all result types, error-body decoding, and an explicit series cap with a total count.

**The PromQL console reader has since landed in the working tree** and already provides that transport (§3.1 column 4):
- `internal/thanos/console.go` (1128 lines). Its header records the placement in `internal/thanos` as following operator-approved decisions 4, 5, 6, 11 and 14 (2026-09-26), because `effectiveTokenFor` is unexported (`docs/promql-console/audit.md:59,452`).
- `console_test.go` (fake querier, `newFakeConsoleThanos`, `console_test.go:41`), and `internal/api/promql_console_{handlers,routes,history}.go`.
- A rewritten `cluster_matcher.go` with `#` handling (`stripComments`, `:236`) and exported `ClusterConfig.EffectiveQuery` (`:470`) and `EffectiveMatchers` (`:481`) for `match[]`.

An earlier draft of this section proposed extending `promapi` and re-pointing the console at it before it shipped. The working tree contradicts that plan, and the placement is operator-approved, so the draft is withdrawn.

**Decision (revised after the console reader landed in the working tree):**
1. **Do not re-platform the console.** Its placement in `internal/thanos` is operator-approved (`console.go:3-5`).
2. **Workers reuse the console transport through one new exported entry:** `(*Service).WorkerQuery(ctx, clusterID, expr string, lim WorkerLimits) (*ConsoleResult, error)`.
   - It has its own normalisation: ≤ 50 000 series, ≤ 64 MiB body, 30–45 s timeout, and `dedup` and `partial_response` set per call.
   - It **fails closed** when a `TokenRef` does not resolve. The console itself still sends no header in that case (`console.go:516`).
   - The `ConsoleLimits` ceilings (`console.go:99-101`) are unchanged.
3. **`doQuery` is not touched**, so this respects the letter of `promapi.go:21-26`. Record in `docs/DECISIONS.md` that `internal/thanos` now holds a second Prometheus-API decoder.
4. **Cluster matcher:** reuse `EffectiveQuery`/`EffectiveMatchers` (`cluster_matcher.go:470,481`; `#` handled at `:236`). Only `NamespaceMatcher` (today the unexported `nsMatcher`, `internal/thanos/promql.go:42`) still needs exporting.
5. **`promapi` stays as it is** for VictoriaMetrics and CoSRE.

**Consequences.**
- P1.1 adds `WorkerQuery` + `WorkerLimits`; P1.2 only exports `NamespaceMatcher`. Neither changes the console.
- The v1 KSM leg may optionally move to `WorkerQuery` (decision 3).
- Memory: every body passes through worker memory, so the `WorkerLimits` caps are the bound.

---

## 4. KSM series on the target clusters (brief item 3)

### 4.1 What Coremetry queries from KSM today

| Series | Selector / labels read | Where |
|---|---|---|
| `kube_replicaset_owner` | `{replicaset!="",owner_kind="Deployment"}`; `namespace`, `replicaset`, `owner_name` | `internal/rollout/ksm.go:44` |
| `kube_replicaset_spec_replicas`, `_status_ready_replicas`, `_created` | `{replicaset!=""}` | `ksm.go:45-47` |
| `kube_pod_info`, `kube_pod_owner`, `kube_replicaset_owner`, `kube_job_owner`, `kube_pod_container_info` (`image` only), `kube_node_info` | Entity layer, **with** `nsMatcher` | `internal/entity/samples.go:18-30` |
| `kube_deployment_spec_replicas`, `_status_replicas_ready`, `_status_condition{condition="Available"}` | `max by (deployment)`, per namespace | `internal/thanos/promql.go:407-429` |

**Not queried anywhere:**
- `kube_deployment_metadata_generation`, `_status_observed_generation`, `_status_replicas_updated`, `_status_replicas_available`, `_status_replicas`;
- any `kube_statefulset_*`, `kube_daemonset_*` or `kube_replicationcontroller_*` series; any `openshift_deploymentconfig_*` series; `kube_pod_labels`;
- `rollout_*`.

(grep over `internal/`, `cmd/`, `main.go`, `frontend/src`; S1 area report.)

### 4.2 The brief's series: labels and presence expectations

Labels are from the KSM docs (external (docs): https://github.com/kubernetes/kube-state-metrics/tree/main/docs/metrics/workload). "CMO default" means OpenShift's cluster-monitoring KSM with the default collection profile. **Every row is an expectation until §11 K1 confirms it per cluster.**

| Series | Labels | Expected on CMO default | v2 use |
|---|---|---|---|
| `kube_deployment_metadata_generation` | `namespace`, `deployment` | present | START candidate |
| `kube_deployment_status_observed_generation` | same | present | "controller has processed it" gate; SUCCEEDED |
| `kube_deployment_spec_replicas` | same | present (already read, `promql.go:412-416`) | scale detection; SUCCEEDED |
| `kube_deployment_status_replicas_updated` / `_available` / `_status_replicas` | same | present | SUCCEEDED parity (§4.5) |
| `kube_deployment_status_condition` | + `condition`, `status`, `reason` (`reason` only from KSM v2.17.0, external (docs): https://github.com/kubernetes/kube-state-metrics/blob/main/CHANGELOG.md) | present unless the minimal profile | STUCK native signal |
| `kube_deployment_spec_paused` | `namespace`, `deployment` | present | "paused" state, not stuck |
| `kube_replicaset_owner` | `namespace`, `replicaset`, `owner_kind`, `owner_name`, `owner_is_controller` | present (already read) | RS set per Deployment; new vs reactivated RS |
| `kube_replicaset_spec_replicas` / `_status_ready_replicas` | `namespace`, `replicaset` | present on default; possibly absent on the minimal profile | active RS; 0 → >0 reactivation |
| `kube_replicaset_created`, `kube_deployment_created`, `kube_statefulset_created`, `kube_pod_created` | — | **absent** (`^kube_.+_created$`) | — |
| `kube_replicaset_metadata_generation`, `_status_observed_generation` | — | **absent** (explicit denylist entries) | — |
| `kube_*_annotations` | — | **absent** (`^kube_.+_annotations$`) | Argo tracking annotation is not readable through KSM |
| `kube_deployment_labels` | `label_*` only if allowlisted; CMO's labels allowlist covers pods, nodes, namespaces, PVs, PVCs, PDBs | present **without** `label_*` | — |
| `kube_pod_labels` | `label_*` (CMO allowlist `pods=[*]`) | present | `pod_template_hash`, `controller_revision_hash`; `app_kubernetes_io_instance` and `argocd_argoproj_io_instance` (estimated `pod_label` mapping hints, §10.3.5) |
| `kube_pod_owner` | `pod`, `owner_kind`, `owner_name`, `owner_is_controller`, `uid` | present | pod → RS/RC |
| `kube_pod_container_info` | `container`, `pod`, `namespace`, `image`, `image_id`, `image_spec` (version not confirmed), `container_id`, `uid` | present | images; version tag (`image` comes from container status and can diverge from the spec; external (docs): https://github.com/kubernetes/kube-state-metrics/issues/1363) |
| `kube_statefulset_metadata_generation`, `_status_observed_generation`, `_replicas`, `_status_replicas{,_ready,_updated,_current,_available}`, `_status_current_revision{revision}`, `_status_update_revision{revision}` | `namespace`, `statefulset` (+ `revision`) | present | STS START/SUCCEEDED/ROLLBACK |
| `kube_daemonset_metadata_generation`, `_status_observed_generation`, `_status_desired_number_scheduled`, `_status_updated_number_scheduled`, `_status_number_available`, `_status_number_unavailable` | `namespace`, `daemonset` | present | DS START/SUCCEEDED (no revision metric) |
| `kube_replicationcontroller_*`, `kube_replicationcontroller_owner` (EXPERIMENTAL) | `replicationcontroller`, `owner_*` | dropped by the minimal profile; `_created` denylisted | DC slice only |
| `openshift_deploymentconfig_*` | `namespace`, `deploymentconfig` | present if openshift-state-metrics is scraped (external (docs): https://github.com/openshift/openshift-state-metrics/blob/master/docs/deploymentconfig-metrics.md) | DC slice only |

**Scrape cadence:** CMO scrapes KSM every 1 m (external (docs): https://raw.githubusercontent.com/openshift/cluster-monitoring-operator/master/assets/kube-state-metrics/service-monitor.yaml). The brief's 30 s tick therefore re-reads the same sample every other tick (decision 8).

### 4.3 The CMO denylist and what it does to START and ROLLBACK

**The full CMO flag set** (external (docs): https://raw.githubusercontent.com/openshift/cluster-monitoring-operator/master/assets/kube-state-metrics/deployment.yaml, fetched 2026-09-26 from the master branch; the OCP version on `cluster-a`/`cluster-b` may differ, and §11 K0.3/K1 confirm it):

| Flag | Value (verbatim) |
|---|---|
| `--metric-denylist` (1) | `^kube_secret_labels$,^kube_.+_annotations$,^kube_customresource_.+_annotations_info$,^kube_customresource_.+_labels_info$` |
| `--metric-denylist` (2) | `^kube_.+_created$,^kube_.+_metadata_resource_version$,^kube_replicaset_metadata_generation$,^kube_replicaset_status_observed_generation$,^kube_pod_restart_policy$,^kube_pod_init_container_status_terminated$,^kube_pod_init_container_status_running$,^kube_pod_container_status_terminated$,^kube_pod_container_status_running$,^kube_pod_completion_time$,^kube_pod_status_scheduled$` |
| `--metric-labels-allowlist` | `pods=[*],nodes=[*],namespaces=[*],persistentvolumes=[*],persistentvolumeclaims=[*],poddisruptionbudgets=[*]` |

None of the brief's Deployment/StatefulSet/DaemonSet generation or replica series is on the denylist. What it removes is RS generation, every `_created` series and every annotation series.

- **Today:** with `kube_replicaset_created` absent, `KSMRev.CreatedAt` stays zero (`ksm.go:97-103`). The KSM-only freshness path in the v1 entry gate always rejects (`reconcile.go:761-765`), and `ksm_started_at` is never stamped (`rollout_schema.go:67`). The repo's own probe script uses `kube_pod_created` (`scripts/probe/entity-discovery-prod.sh:145`), which is also denylisted.
- **START** cannot use "RS created at". It must use a Deployment `metadata_generation` increase, confirmed by a template change (§4.4).
- **ROLLBACK** ("target RS existed") cannot use RS age. It must use **membership**: the RS that becomes current was already known for this workload. `kubectl rollout undo` re-scales an existing RS, and blue/green reactivates one (`rollouts-audit.md:537-547`). "Known" needs a durable first-seen set that survives failover. That is the reason for `rollout_workload_state` (§10.3.2).
- **Incarnation:** Deployments, StatefulSets and DaemonSets expose no uid in KSM, only name and namespace. A delete/recreate restarts `generation` at 1, and the brief's key `(cluster, namespace, kind, name, generation)` then collides with older events. With `_created` denylisted, the incarnation must come from the detector's own state (decision 9).
  - **New-incarnation rule:** the observed `metadata_generation` is lower than the stored `generation`, or the workload re-appears after being absent from ≥ K consecutive complete reads (not partial, not truncated; K is a setting, `incarnationAbsentTicks`, default 3).
- **Argo tracking:** annotations are denylisted and workload labels are not allowlisted. Argo's tracking label or annotation is therefore **not readable through KSM**, except via pod labels when a chart propagates `app.kubernetes.io/instance` into the pod template. Even then it is an estimate (§6).

### 4.4 A generation bump is not a rollout

`metadata.generation` increments on any spec change, **including `spec.replicas`** (manual scale and HPA via `/scale`), and on **annotation-only** changes. Annotations are copied to ReplicaSets, so they bump the generation too (external (docs): https://github.com/kubernetes/kubernetes/blob/master/pkg/registry/apps/deployment/strategy.go).

**Proposed START rule (Deployment):**
1. The candidate is `generation` > the stored generation.
2. Wait until `observed_generation ≥ generation` (the controller has acted). A 1 m scrape adds lag; the detector re-checks for up to N ticks.
3. It is a rollout only if, among `kube_replicaset_owner{owner_kind="Deployment",owner_name=<d>}`:
   - **(a)** an RS name not in `known_revisions` appeared (`rollout`/`config`);
   - **(b)** a known RS went from spec 0 to spec > 0 (`rollback`); or
   - **(c)** a known RS name re-appeared after its series was absent (`rollback`: the RS was garbage-collected beyond `revisionHistoryLimit` and recreated under the same `pod-template-hash`).

   Case (c) is why `known_revisions` (bound 32) must outlive `revisionHistoryLimit` (default 10). Without it, a revert to a template whose RS was garbage-collected would look like a scale bump in step 4, and the ROLLBACK test in §4.11 would never run.
4. Otherwise it is a **scale or annotation-only bump**: update the state and write **no event** ("ignore pure scale" becomes "ignore scale and annotation-only").
5. A **paused** Deployment (`kube_deployment_spec_paused == 1`) creates no RS; the bump stays pending until it resumes.

**Change type** (from images): images changed ⇒ `rollout`; same images, new RS ⇒ `config` (this includes `kubectl rollout restart`, which KSM cannot tell apart from an env or ConfigMap-reference change, because KSM exposes no pod-template annotations); reactivated or recreated known RS (cases b and c) ⇒ `rollback`; first sighting of a new workload after bootstrap ⇒ `initial` (decision 10).

**Measure first:** §11 K5 gives the ratio of generation bumps to new RSs over 6 h, which is the size of the noise this gate removes.

### 4.5 SUCCEEDED and STUCK parity

- **SUCCEEDED**, the same test as `kubectl rollout status` (external (docs): https://github.com/kubernetes/kubectl/blob/master/pkg/polymorphichelpers/rollout_status.go):
  - `observed_generation ≥ generation`;
  - `updated ≥ spec`;
  - **`status_replicas ≤ updated`** (old pods are gone; this term is missing from the brief's rule);
  - `available ≥ updated`;
  - no `ProgressDeadlineExceeded`.
- **STUCK:** `kube_deployment_status_condition{condition="Progressing",status="false"} == 1` is the native signal, and it works without the `reason` label. The brief's timer is `stuckAfter` (default 10 m, a setting). The Kubernetes default `progressDeadlineSeconds` is 600 s (external (docs): https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#progress-deadline-seconds), so the two usually agree. Record `stuck_reason` (`progress_deadline` or `timeout`).
- **SUPERSEDED:** a newer START on the same `(cluster_id, namespace, kind, workload, incarnation_at)` while this event is `progressing` or `stuck` sets it to `superseded`, with `finished_at` = the newer `started_at`.
- **StatefulSet:** `ready ≥ replicas` and `update_revision == current_revision`. Partitioned or `OnDelete` rollouts cannot be seen through KSM (no strategy metric); STUCK for them is timer-only.
- **DaemonSet:** `updated_number_scheduled ≥ desired` and `number_available ≥ desired`.

### 4.6 StatefulSet and DaemonSet revisions (controller revisions)

- KSM has **no ControllerRevision collector**.
- **StatefulSet:** `kube_statefulset_status_update_revision{revision}` and `_current_revision{revision}` carry the ControllerRevision name as a label (value 1). START = generation bump + a new `update_revision`; ROLLBACK = `update_revision` equals a revision already in the known set.
- **DaemonSet:** no revision metric. The only revision source is `kube_pod_labels{label_controller_revision_hash}`, which the CMO pods allowlist makes available (§11 K2.4 checks). Without it, a DS event has an empty `new_revision` and ROLLBACK is undetectable (it shows as `rollout`).
- **v1 contrast:** the span MV uses the image tag as the STS/DS revision, which makes fixed-tag (`latest`) STS rollouts invisible (`rollouts-audit.md:514-518`). v2 removes that blind spot for StatefulSets.

### 4.7 DeploymentConfig gap

- Every current path is blind to DeploymentConfig:
  - the MV filters DC pods out (`workload=''`, per the Argo annex §2.2);
  - the KSM leg is Deployment-only (`ksm.go:44`);
  - the entity layer excludes DC by design (`internal/entity/normalize.go:13-19`);
  - only the pod-churn helper sees `<dc>-<N>-<rand>` names (`internal/chstore/deploys.go:703`).
- KSM has no DC metrics. DC rollouts appear as ReplicationControllers `<dc>-<N>`; `kube_replicationcontroller_owner` is EXPERIMENTAL and dropped by the minimal profile. `openshift_deploymentconfig_*` has generation and replica series but **no `latestVersion`**.
- DeploymentConfig is deprecated since OCP 4.14 (external (docs): https://access.redhat.com/articles/7041372).
- **Proposal:** decide after §11 D. If the share is small, DC workloads get an explicit "not covered" state (trigger `unknown`, reason `dc-not-covered`) and their Argo syncs show on the Service card only. If it is material, add a DC slice: `openshift_deploymentconfig_metadata_generation` + RC set diff (decision 12).

### 4.8 Duplicates (HA and dedup)

- Platform Prometheus runs as an HA pair, so KSM series exist twice unless the querier deduplicates on the replica label.
- `/clusters` already collapses with `max by (deployment)` (`internal/thanos/promql.go:407-416`); the rollout KSM leg reads raw series, so duplicates also count toward the 1000 cap.
- **v2:** always aggregate `max by (namespace, <kind>, …)` and send `dedup=true` explicitly. §11 K0.4 measures the duplicate ratio.

### 4.9 NamespaceFilter gap

- The field comment promises injection into every query (`client.go:82-85`), but only fixed builders and the entity layer add it (`internal/entity/samples.go:18-30`); the rollout KSM queries do not (`ksm.go:42-49`, `main.go:2220-2242`). This was flagged in 2026-08 (`rollouts-audit.md:244,251`).
- **v2:** every detector query carries `nsMatcher` through the exported `NamespaceMatcher` (§3.4).

### 4.10 Truncation and partial responses

- `maxSeriesParsed = 1000`, cut after the full unmarshal with no flag (`client.go:520-523,584-586`); the 8 MiB body cap produces a decode error (`client.go:572`).
- `ksmMaxSeries = 5000` (`ksm.go:53,62-64`) is **unreachable**. `kube_replicaset_spec_replicas{replicaset!=""}` returns every retained RS; with the default `revisionHistoryLimit` of 10 that is up to 11 series per Deployment, so about 90 Deployments can exceed 1000 in the worst case (estimate; §11 K6 measures it).
- **Partial responses are invisible:** no `warnings` field (`client.go:501`), no `partial_response` parameter. A down store looks like absent series, and the detector would read that as "RS disappeared" (external (docs): https://thanos.io/tip/components/query.md/).
- **v2 rule:** on `Truncated` or `Partial`, **skip the diff for that shard** and mark the run `partial`. Never infer absence from a partial read.

### 4.11 Resulting detector signal set (input to P2)

| Kind | START | SUCCEEDED | STUCK | ROLLBACK | Per-tick queries (per cluster, `max by`, nsMatcher) |
|---|---|---|---|---|---|
| Deployment | generation ↑ + new, reactivated or recreated RS (§4.4 step 3) | §4.5 | condition or timer | current RS ∈ known set (reactivated or recreated) | `metadata_generation`, `status_observed_generation`, `spec_replicas`, `status_replicas`, `status_replicas_updated`, `status_replicas_available`, `status_condition{condition="Progressing"}`, `spec_paused`, `kube_replicaset_owner{owner_kind="Deployment"}`, `kube_replicaset_spec_replicas > 0` |
| StatefulSet | generation ↑ + new `update_revision` | §4.5 | timer | `update_revision` ∈ known set | 5 STS series |
| DaemonSet | generation ↑ + `updated_number_scheduled < desired` | §4.5 | timer | only with `controller_revision_hash` | 4 DS series (+ pod labels) |
| Argo Rollout (if present, §5.5) | `rollout_info` phase / RS `owner_kind="Rollout"` | phase `Healthy` | phase `Degraded`/paused | — | separate slice |

**Images:** `kube_pod_container_info` joined through `kube_pod_owner` → RS, read **only for workloads with an open event** (bounded), not per tick fleet-wide.

---

## 5. Hub Argo metrics (brief item 4)

### 5.1 Series and labels

External (docs): https://argo-cd.readthedocs.io/en/stable/operator-manual/metrics/ and source https://github.com/argoproj/argo-cd/blob/release-3.0/controller/metrics/metrics.go. Full tables are in the Argo annex §3.1.

| Series | Labels (as emitted, before scrape) | Notes |
|---|---|---|
| `argocd_app_info` (gauge = 1) | `namespace` (**Application CR namespace**), `name`, `project`, `repo` (first source), `dest_server`, `dest_namespace`, `sync_status`, `health_status`, `operation`, `autosync_enabled` (from v2.9) | `dest_server` is raw `spec.destination.server` on v2.x (`""` with `destination.name`); resolved through the cluster lookup on v3.0+. `phase`/`hydrator_status` exist only in pre-release builds |
| `argocd_app_sync_total` (counter) | `namespace`, `name`, `project`, `dest_server`, `phase` (+ `dry_run` from v3.1) | Incremented on **completion** only; **no initiator, no `dest_namespace`, no revision**; the series is born on the first completed sync after a controller restart |
| `argocd_cluster_info` | `server`, `k8s_version` (+ `name` from v3.0) | Low-cardinality source for instance discovery and `dest_server` → cluster suggestions |

**No Argo metric carries** a revision/SHA, an initiator, a sync time or a managed-resource list.

### 5.2 `namespace` vs `exported_namespace` (honorLabels)

**Rule** (external (docs): https://prometheus.io/docs/prometheus/latest/configuration/configuration/): with `honor_labels: false` (the default), a scraped label that conflicts with a target label is renamed `exported_<label>`, and the target label wins.

| Scrape path | `namespace` after scrape | `exported_namespace` |
|---|---|---|
| ServiceMonitor, default `honorLabels` (GitOps operator SMs) | **Argo instance namespace** (where `<team>-<env>-metrics` lives) | Application CR namespace (present even when equal) |
| OpenShift user-workload monitoring (forces `honorLabels` off and enforces `namespace`) | instance namespace | Application CR namespace |
| Any scrape with `honorLabels: true` | Application CR namespace | **absent** |

- The environment fact (`namespace="<team>-<env>"`, `job="<team>-<env>-metrics"`) matches the first two rows.
- Without apps-in-any-namespace, the Application CR namespace equals the instance namespace, so the two labels carry the same value.
- **Derivation:** `app_ns = exported_namespace` if non-empty, else `namespace`. `instance_ns = namespace` only when `exported_namespace` exists; otherwise the instance comes from `job` through settings. **App key = `(instance, app_ns, name)`; `name` is never a key on its own.** §11 H1.2 decides the case.

### 5.3 `dest_server` and `dest_namespace`

- `dest_server` = the target cluster API URL **as registered in the Argo cluster Secret**. Port, trailing slash and case vary, so Coremetry normalises it before matching `apiServerUrls` (§3.2).
- `https://kubernetes.default.svc` means the hub itself (external (docs): https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/). `""` means `destination.name` (v2.x) or a failed lookup (v3.x).
- **Many Applications share `(dest_server, dest_namespace)`** (one per component), so a namespace-level join yields N candidates; this is the "weak" match (§6, §10.3.5).
- Paired active-active prod clusters: one Application per cluster, same base name, different suffix and `dest_server`.
- `argocd_app_sync_total` has `dest_server` but no `dest_namespace`; join it to `argocd_app_info` on `(namespace, exported_namespace, name)`.

### 5.4 Cardinality and safe-query rules (~40k series; never unfiltered)

1. Instant queries only for inventory. Range functions only on `argocd_app_sync_total`, and `[1h]` before `[24h]`.
2. `count(...)` before listing anything. List only when the count is ≤ 50, or under `topk(50, …)`.
3. Collapse duplicates first: `group by (namespace, exported_namespace, name, …)`. Controller shards and HA replicas multiply series; always aggregate away `pod` and `instance`.
4. Never use the label browser on `name`, `repo` or `dest_server`. Always pass `start`/`end` to label and name APIs.
5. **Worker:**
   - shard by instance namespace (and additionally by `dest_server` if a shard exceeds the cap; §11 H1.6 sizes it);
   - fetch only the non-steady set per tick, and run a full sharded inventory every 10–15 min;
   - use `partial_response=false`, and skip the shard's diff on any warning;
   - use POST for long matchers.
- **Size:** about 0.5–0.8 KB of JSON per raw series, so 20–30 MB for 40k. This is an estimate, not a measurement, and it exceeds every cap today (§4.10).

### 5.5 Argo Rollouts presence

- The brief asks for `group by (__name__) ({__name__=~"rollout_.*"})`. Its safe form is the name-values API or `count by (__name__)` including `analysis_run_.*`, `experiment_.*` and `argo_rollouts_controller_info`.
- Run it on the hub **and** both targets: the Rollouts controller runs where the workloads run, and it is scraped only if a ServiceMonitor exists (§11 R).
- The cross-check is `kube_replicaset_owner{owner_kind="Rollout"}`; the v1 KSM leg filters these out (`ksm.go:44`).
- Metrics (external (docs): https://argo-rollouts.readthedocs.io/en/stable/features/controller-metrics/): `rollout_info{namespace,name,strategy,traffic_router,phase}`, `rollout_info_replicas_*`, `rollout_events_total{type,reason}`, `analysis_run_info{phase}`.
- **If present:** an Argo Rollouts detector slice (`kind='Rollout'` in `rollout_events`). **If absent:** nothing is built (decision 11).

### 5.6 Hub entry and label injection

- `argocd_app_info` exists only in the hub's Thanos, so the hub must be a Remote Cluster entry (URL + token). That also puts it into `/clusters`, entity sync and rollout detection; there is no "metrics-only" entry kind (decision 5).
- If the hub entry sets `ThanosLabelName`, every query gets `<L>="<V>"` injected (`client.go:538-546`). The label was detected from `kube_node_info` (`internal/thanos/cluster_detect.go:36,60-61`). If Argo series carry different external labels (user-workload monitoring vs platform), the injected matcher returns 0 series.
- **Mitigation:** §11 H0.3 vs H0.5 before P3, and an explicit `injectClusterLabel` switch on the Argo reader.
- **Two hubs (operator, 2026-09-26).** Argo CD runs on TWO hub clusters, not one. Each hub is its own Remote Cluster entry pointing at the Thanos that holds that hub's `argocd_*` series. The `argocd` blob carries `hubs[{clusterId, injectClusterLabel}]` instead of a single `hubClusterId` (label injection is decided per hub), and every instance carries its own `hubClusterId`. Consequences: discovery (§5.1, H1.5) runs per hub; `https://kubernetes.default.svc` (§5.3) resolves to the instance's own hub, never to "the" hub; instance ids are unique across hubs; the §11 H and N query packs run once per hub; `injectClusterLabel` is evaluated per hub (H0.3 vs H0.5 on each). Open for P3: whether the two hubs manage disjoint target clusters (for example one side each of the active-active pair) — the mapping key already includes `instance_id`, so a workload managed from both hubs yields two edges, and the pair warning must say so.

---

## 6. Naming convention validation (brief item 5)

The convention `<prefix>-<team>-<component>-<env>-<clusterSuffix>` is **observed, not guaranteed**. Nothing is hard-coded: the env list and the suffixes come from settings.

**Method**
1. **Inputs from settings:** `envList` (argocd blob), and the union of the Remote Clusters' `argoSuffix` values. Each item is escaped (`regexp.QuoteMeta` in Go; escaped in PromQL).
2. **Regex:** `^(?P<prefix>[a-z0-9]+)-(?P<team>[a-z0-9]+)-(?P<component>[a-z0-9-]+?)-(?P<env>ENVS)-(?P<suffix>SUFFIXES)$`, anchored at the end so `env` and `suffix` parse robustly. PromQL `=~` is fully anchored (note at `internal/thanos/promql.go:54`).
3. **Measure** on distinct app identities (`group by (namespace, exported_namespace, name)`), never on raw series (§11 N):
   - overall %, and % per instance (zero-match instances kept visible);
   - near-miss buckets: known env with an unknown suffix; unknown env with a known suffix; four tokens or fewer;
   - the top 20 unknown last tokens (tokenised when pasted);
   - **suffix auto-derivation:** the dominant last token per `dest_server` and its share; a share below 0.95 flags an inconsistent cluster;
   - **suffix uniqueness:** a suffix seen on more than one `dest_server` breaks suffix → cluster;
   - env purity per instance; whether team names contain hyphens (if they do, the team/component split is only ever "estimated").
4. **Consistency check at runtime:** the parsed suffix must equal the `argoSuffix` of the cluster that `dest_server` resolves to. A mismatch is flagged and does not change the mapping.

**What the result drives**

| Outcome | Use |
|---|---|
| Name parses | `base_key = <prefix>-<team>-<component>-<env>`; the pair key is `(base_key, pairGroup)`; `component` is the **estimated** workload match |
| Name does not parse | Valid. The app keeps status and can map by resource (exact) or namespace (weak); no base key and no pair |
| Hyphenated teams | team/component marked "estimated" |

No acceptance threshold is assumed; the operator sets one after seeing §11 N.

**Service-name env suffixes are separate and hard-coded** (`-prod`, `-int`, `-uat`, `-prep`, per the Argo annex §7.3, `internal/chstore/job_service.go:127`). Aligning them with `envList` is decision 16.

---

## 7. Argo CD API (brief item 6)

External (docs): https://argo-cd.readthedocs.io/en/stable/developer-guide/api-docs/ and swagger https://github.com/argoproj/argo-cd/blob/master/assets/swagger.json. Field-level detail is in the Argo annex §5.

### 7.1 Endpoint pattern and calls

- **Per instance:** `https://<instance-server-route>[/<rootpath>]/api/v1/...`. On OpenShift GitOps the server Route is `<argocd-name>-server`, and `ArgoCD.status.host` holds the host (external (source): https://github.com/argoproj-labs/argocd-operator/blob/master/api/v1beta1/argocd_types.go, `ArgoCDStatus.Host`, "the hostname of the Ingress"; the operator reference page https://argocd-operator.readthedocs.io/en/latest/reference/argocd/ documents only `spec`). The default host pattern `<route>-<ns>.<apps-domain>` is **unverified**, so `apiUrl` is a per-instance setting and is **never derived**.
- Each instance has its own signing key, accounts, RBAC and possibly version, so Coremetry needs **N credentials**.

| Need | Call | Notes |
|---|---|---|
| Operation, initiator, revision, history, resources | `GET /api/v1/applications/{name}?appNamespace=<ns>&project=<p>` | `status.operationState.{phase,startedAt,finishedAt,operation.initiatedBy.{username,automated},syncResult.revision(s)}`, `status.history[]` (`initiatedBy` from v2.11; appended only for successful full syncs; default limit 10), `status.sync.revision`, `status.resources[]` |
| Bulk inventory (low frequency) | `GET /api/v1/applications?fields=…` | No pagination; the `fields` projection is undocumented and version-dependent; `history` and `initiatedBy` are not projectable |
| Version | `GET /api/version` | per instance |
| **Forbidden** | any `refresh=normal\|hard` parameter; `resource-tree` (a cache miss triggers an internal refresh, i.e. an annotation write; external (source): https://github.com/argoproj/argo-cd/blob/master/server/application/application.go, `getCachedAppState` calls `Get` with `Refresh: normal` on `ErrCacheMiss`); `managed-resources` (heavy) | "Read-only everywhere" |

### 7.2 Auth: one read-only local account and token per instance

- **Account:** `accounts.coremetry-ro: apiKey` via `ArgoCD.spec.extraConfig` or `spec.localUsers` (the GitOps operator reconciles `argocd-cm`). A token is issued by an admin (`argocd account generate-token --account coremetry-ro --expires-in <d>`); the default expiry is none. External (docs): https://argo-cd.readthedocs.io/en/stable/operator-manual/user-management/.
- **RBAC** (`policy.csv` or `ArgoCD.spec.rbac.policy`; external (docs): https://argo-cd.readthedocs.io/en/stable/operator-manual/rbac/):

```csv
p, role:coremetry-ro, applications, get, */*, allow
p, role:coremetry-ro, clusters, get, *, allow        # optional: dest_server <-> Argo cluster name
g, coremetry-ro, role:coremetry-ro
```

  `applications, get` covers Get and List. The built-in `role:readonly` or `policy.default` may already grant it; confirm per instance.
- **Coremetry side:** `tokenRef` only (`env:`/`file:` via `internal/secretref`). There is **no plaintext token field**, and an unresolved ref **fails closed** (the instance is skipped with an error). Thanos, by contrast, sends the request without a header (`client.go:562-564`). Config export dumps `system_settings` verbatim (`internal/api/config_iox.go:36`), so a plaintext field would leak.
- **Delivery gap:** the secretref header promises "Helm extraEnv + existingSecret" (`internal/secretref/secretref.go:7`), but the chart has `extraEnv` only for the go-demo (`charts/coremetry/values.yaml:543`, `templates/go-demo.yaml:51`). The chart needs `extraEnv`/`envFrom`/`extraVolumes`/`extraVolumeMounts` on the api **and** worker pods (P1.6).

### 7.3 Rate and poll guidance

- argocd-server documents only failed-login throttling (external (docs): https://argo-cd.readthedocs.io/en/stable/operator-manual/user-management/, section "Failed logins rate limiting", `ARGOCD_SESSION_FAILURE_MAX_FAIL_COUNT` and related variables). "No general API rate limiter" is **inferred from the source** (per the Argo annex §5.3) and **not verified**. Throttling is therefore client-side:
  - a token bucket per instance (1 req/s, burst 5, at most 2 concurrent) plus a global cap;
  - backoff on 429/5xx, and a circuit breaker per instance.
  - No token-bucket helper exists in committed code (the existing limiters are fixed-window counters, for example `internal/api/mcp_gate.go:15,36`; the working-tree `internal/api/promql_console_guard.go:35` explains why it avoided `x/time/rate`), and `golang.org/x/time` is not in `go.mod`. P3.3 builds a small bucket with an injected clock.
- **Change-driven calls:** `GET application` only when the metrics worker sees a sync completion, an OutOfSync or health transition, or a new app, plus a slow round-robin. A full per-app sweep at 1 req/s takes about 5.5 h **if the estate has ~20k apps** (assumption; §11 H0.4).
- Skip the whole API worker while `lockDegraded`.

### 7.4 What only the API can give

`initiatedBy.automated` / `.username` (who; manual vs auto), the synced Git revision(s), operation start and finish times, the history, and the managed-resource list (`status.resources[]`, the **exact** mapping; `syncResult.resources` is only what one operation touched). Metrics give status, autosync, destination and repo.

### 7.5 Classification (P3; windows are settings)

**Anchor:** the rollout's KSM `started_at`. It is far tighter than v1's span anchor, so the brief's ±5 m is realistic.

| Class | Rule |
|---|---|
| `argo_auto` | A mapped app (exact or estimated) has an operation with `automated=true` and `op_started_at − 5m ≤ anchor ≤ op_finished_at + 5m` |
| `argo_manual` | Same window, `automated=false` (initiator stored verbatim; see the note below) |
| `out_of_band` | Exact or estimated mapping, **no** operation in `[anchor − 30m, anchor + 5m]`; supporting evidence: the app went `OutOfSync` near the anchor |
| `unknown` | No mapping or weak only; an Argo/API gap or partial run in the window; DC or not covered; conflicting evidence |

- **Metrics-only mode** (no API credentials yet): a completed sync (`argocd_app_sync_total` increase) in the window gives
  - with `autosync_enabled="false"`: **`argo_manual` (estimated)**, because the controller never auto-syncs such an app (the label shows the current policy, not the policy at sync time);
  - with `autosync_enabled="true"`: `argo_auto` or `argo_manual`, **ambiguous** (confidence ≤ 0.5).

  Both are labelled "estimated" (decision 15).
- **Initiator identity:** SSO users appear as emails. The house rule is full fidelity (`CLAUDE.md`, "No PII redaction features"), so the initiator is stored and shown verbatim.

---

## 8. Azure DevOps (brief item 7)

### 8.1 What exists (`internal/devops`)

| Area | Fact |
|---|---|
| Connection | One collection per install (`Settings`, `internal/devops/client.go:60-123`); flavour auto/Server/TFS with API version negotiation 6.0/4.1 |
| Auth | HTTP Basic with a **plaintext PAT** in `system_settings["devops_connection"]` (`client.go:86-89`); no `secretref`; `Snapshot` exposes only `HasPAT` |
| Errors | 401/403 → "check the PAT and its scopes (Code: Read)", hard-coded (`client.go:680-681`) |
| Calls | refs, trees, items, repo list (names only), code search (POST), `commits?$top=1` for recency (`internal/devops/code.go:844`) |
| Useful pure functions | `ResolveRepo` (`internal/devops/repo_resolve.go:122`), `parsePinnedRepo` (`:265`), `ResolveVersionRef` (image/version → `tags/{version}` → SHA, `internal/devops/version_ref.go:64`) |
| **Does not exist** | commit by SHA, PR lookup, build/pipeline/release calls, rate limiting or `Retry-After` handling, `enrichment_status`, `get_change_history` (grep over `internal/` finds none) |
| Sharing | `search_knowledge` reads runbooks and RAG chunks only; Azure DevOps wiki content arrives via the RAG crawler with its own credentials. No MCP tool uses the devops client (per S2) |

### 8.2 Commit → PR → author → pipeline (external (docs))

| Step | Endpoint | Scope |
|---|---|---|
| Commit | `GET {coll}/{p}/_apis/git/repositories/{repo}/commits/{sha}?api-version=6.0` → author, committer, comment, parents (>1 = merge), `push.pushedBy`, `_links.web`. https://learn.microsoft.com/en-us/rest/api/azure/devops/git/commits/get?view=azure-devops-rest-7.1 | Code (Read) |
| PR for commit | `POST …/repositories/{repoId}/pullrequestquery` with `{"queries":[{"type":"lastMergeCommit","items":["<sha>"]},{"type":"commit","items":["<sha>"]}]}` (a read-only query despite POST). https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-request-query/get?view=azure-devops-rest-7.1 | Code (Read) |
| Fallback PR search | `GET …/pullrequests?searchCriteria.status=completed&…` + client-side match on `lastMergeCommit.commitId`. https://learn.microsoft.com/en-us/rest/api/azure/devops/git/pull-requests/get-pull-requests?view=azure-devops-rest-7.1 | Code (Read) |
| Pipeline/build | `GET {coll}/{p}/_apis/build/builds?repositoryId=…&repositoryType=TfsGit&…` + client-side `sourceVersion == sha` (no SHA filter). https://learn.microsoft.com/en-us/rest/api/azure/devops/build/builds/list?view=azure-devops-rest-7.1 | **Build (Read)** |
| Cheaper pipeline link | `GET …/commits/{sha}/statuses?latestOnly=true` (only if pipelines post statuses). https://learn.microsoft.com/en-us/rest/api/azure/devops/git/statuses/list?view=azure-devops-rest-7.1 | Code (Read) |
| Classic release (optional) | `GET …/_apis/release/releases?artifactVersionId=…`. https://learn.microsoft.com/en-us/rest/api/azure/devops/release/releases/list?view=azure-devops-rest-7.1 | Release (Read) |

**Scopes** (https://learn.microsoft.com/en-us/azure/devops/integrate/get-started/authentication/oauth?view=azure-devops): Code (Read) today. Build (Read) for pipeline links; Release (Read) is optional. The UI hint (`frontend/src/pages/settings/DevOpsTab.tsx:300`, per S2) and the error text (`client.go:681`) must become scope-aware. Services throttling: 200 TSTU per 5-minute window, 429 with `Retry-After` (https://learn.microsoft.com/en-us/azure/devops/integrate/concepts/rate-limits?view=azure-devops).

### 8.3 Repo identity from the Argo repo URL

- **Source:** `argocd_app_info{repo}` (metrics, first source; free) or `spec.source(s).repoURL` (API).
- **Primary path:** list repositories **collection-wide** (`{coll}/_apis/git/repositories`, keeping `id`, `project.name`, `remoteUrl`, `sshUrl`, `webUrl`). Normalise both sides (lower-case host, strip userinfo and `.git`, map SSH to https) and match exactly. The result is a repo GUID plus project, with no heuristics. Today's repo list is project-scoped and decodes only `name` (per S2), so it must be extended.
- **Fallback:** `parsePinnedRepo` + `MatchRepoName`. Traced, not tested: the SSH v3 form `git@<ssh-host>:v3/<org>/<proj>/<repo>` yields an **empty project** (per S2).
- **Guards:**
  - a host or collection other than the configured one ⇒ `unmapped_repo` (one collection is supported);
  - Helm chart repositories ⇒ `not_git_sha`;
  - multi-source apps need a "which source counts" rule (external (docs): https://argo-cd.readthedocs.io/en/stable/user-guide/multiple_sources/).
- **GitOps caveat:** if `repo` is a manifests repo, its SHA and PR answer "who changed the manifest" (often a pipeline identity bumping an image tag), not "which code shipped". Reaching the app commit is a second hop: image tag → build (`buildNumber` = tag) or git tag (`ResolveVersionRef`) → `sourceVersion` → commit → PR (decision 21).

### 8.4 Verification procedure (operator, read-only; §11 V)

The operator runs `curl` for the commit, the PR query, builds and statuses against one known Argo sync revision, and pastes the **presence** of fields, not their values. Inside Coremetry, today's checks are `POST /api/settings/devops/test` and `POST /api/devops/resolve-dryrun`; they verify PAT, repo, branch and tree, but not PRs or builds.

### 8.5 Enrichment design (P4)

- **Async, fail-open, leader-only** (`ado-enrichment`, skipped while `lockDegraded`). Rollout events are written immediately and never wait on Azure DevOps.
- **Keyed by `(repo_url_norm, sha)`**, not by event: one SHA promoted to N clusters costs one lookup; results are immutable. `repo_id` is resolved once per URL and stored as a column, so every status (including `unmapped_repo` and `not_git_sha`) has a key and the feed joins `normalise(rollout_classification.repo_url)` directly. Table: `ado_commit_enrichment` (§10.3.7).
- **`enrichment_status`:** `ok`, `no_pr` (direct push, itself a signal), `not_found`, `unmapped_repo`, `not_git_sha`, `auth` (stop retrying; surface on the admin page), `throttled` (honour `Retry-After`), `error` (backoff, capped attempts), `disabled`. **`pending` is derived at read time** (no row yet), so the detector never writes to this table.
- **Who, for display:**
  - manual sync ⇒ Argo `initiatedBy.username`;
  - auto sync ⇒ PR `closedBy`/`autoCompleteSetBy` → `createdBy` → commit author → `pushedBy`;
  - out-of-band ⇒ unknown (Kubernetes audit logs are not ingested; decision 22).
- **Budget:** at most 3–4 calls per new SHA; a token bucket; a per-tick deadline; counters on `/admin/stats` (the `code_stats.go` precedent).
- **Reuse by CoSRE:** `get_change_history` does not exist. Proposal (P4.3): a **read-only MCP tool over the persisted tables** (`rollout_events` + classification + sync events + enrichment), with `range_s` and `clampLimit` (mcp-tools conventions), exposed as a narrow `Deps` function, with **no live Azure DevOps call** and no PAT exposure. Align the output with `list_deployments`.
- **Secret:** move the PAT to `tokenRef` before widening its scopes (decision 20).

---

## 9. Span attributes (brief item 8)

### 9.1 Which attributes are read today, and where

| Attribute | Column | Readers (per S3; key refs re-checked) |
|---|---|---|
| `service.version` | **none** (read from `res_keys/res_values` only) | third link of `effectiveVersionExpr` (`deploys.go:93-94`); `service_version_5m` MV (`store.go:1490-1510`); problem telemetry; logstore/vmetrics label maps; frontend `runningVersion.ts` |
| `container.image.tag` (+ `k8s.container.image.tag`) | `container_image_tag` MATERIALIZED (`internal/chstore/promoted_attr.go:121`) | head of the version chain (`deploys.go:89-92`); MV revision for STS/DS |
| `k8s.deployment.name` | `k8s_deployment` (`promoted_attr.go:116`) | service_metadata deriver, K8s Coverage, metric rollup `service_key` |
| `k8s.replicaset.name` | `k8s_replicaset` (`promoted_attr.go:119`) | MV `revision`; K8s Coverage `rs` (`k8s_coverage.go:141`) |
| `k8s.cluster.name` → `openshift.cluster.name` → `cluster` (resource, then attributes) | `cluster` MATERIALIZED 6-way coalesce (`internal/chstore/repo.go:346-355`) | every cluster filter; exact match |
| `deployment.environment.name` (fallback `deployment.environment`) | `deploy_env` (`internal/otlp/convert.go:77-78`) | env filters (exact match) |

The promoted columns exist in prod only if migrations 0011/0012 were applied; boot skips the ALTERs on an external Distributed `spans` (per S3; `promoted_attr.go` repair path).

### 9.2 Source: agent vs `k8sattributes`

- **The repo cannot see the prod collector.** The chart collector is `otel/opentelemetry-collector-contrib:0.111.0` with `k8sattributes` **opt-in**, extracting six keys (ns, deployment, pod, uid, node, container); there is no `resource`/`resourcedetection`/`transform` processor (per S3; `charts/coremetry/templates/otel-collector.yaml`). Nothing the repo ships sets `k8s.cluster.name`, `container.image.*`, `k8s.replicaset.name` or `service.version`.
- The Java agent's default resource providers emit no `k8s.*` or `container.image.*`; `service.version` can come from the JAR manifest `Implementation-Version` (external (docs): https://github.com/open-telemetry/opentelemetry-java-instrumentation/blob/main/instrumentation/resources/library/README.md). This is a plausible cause of the fleet-constant `service.version` that made the image tag move first in the version chain (v0.9.66, `internal/chstore/deploys.go:78-86`).
- Prod evidence in the repo consists of single screenshots that disagree per cluster (`rollouts-audit.md:11`). **Coverage must be measured per cluster** (§11 T).

### 9.3 `k8s.cluster.name` derived from `deployment.environment.name`: join risks

1. **An application-set label becomes infrastructure identity.** A mis-templated env silently maps spans to the wrong `EffectiveID`, and exact-match filters cannot detect it.
2. **Missing or legacy env ⇒ empty cluster.** Such spans are dropped from cluster joins (the v1 reconciler counts them as unmapped).
3. **Form unknown:** `prod-<cluster-name>` verbatim or prefix-stripped? Matching is exact and case-sensitive (`MatchesSpanCluster`, `cluster_identity.go:119`). Non-prod envs need the same rule.
4. **Precedence shadowing:** `k8s.cluster.name` beats `openshift.cluster.name` (`repo.go:347-348`). When the derivation rolled out, history split into two values per cluster; `SpanClusterValues` (multi-value) is the existing mitigation.
5. **`deploy_env` now holds `prod-<cluster>`,** so an env picker for "prod" matches nothing on exact filters.
6. **Three identity spaces:** span value (derived) → registry via `SpanClusterKeys` (`cluster_identity.go:76`); KSM → registry via Thanos URL or external label; Argo `dest_server` → registry via the **new** `apiServerUrls`. **v2 keys everything on `EffectiveID()`.**
7. **RS names are not unique across clusters:** identical templates in a pair give the same `pod-template-hash`. Always join with `cluster_id`.

**Impact scoping:** before/after RED must be cluster-scoped. Read `service_env_summary_5m` through `GetServicesEnvAggFiltered` (`summary.go:570`) with the span cluster values of the event's `cluster_id`, never raw spans (CLAUDE.md invariant 3). Two preconditions:
1. A registry entry can own several span values (`SpanClusterKeys`, `cluster_identity.go:76`), but `GetServicesEnvAggFiltered` takes a single `cluster string`. P5.2 adds a multi-value variant (`cluster IN (…)` inside one MV merge, because tDigest p95/p99 cannot be merged across separate calls).
2. Check `EnvSummaryCovers` first (`internal/chstore/service_env_summary.go:33`; required by the caller contract at `summary.go:566-569`), since the MV is not backfilled. Older windows fall back to the unscoped reader with a "not cluster-scoped" note.

### 9.4 Collector options to set `service.version` from the image (documentation only; nothing is written)

| Option | What | Caveats |
|---|---|---|
| A. `k8sattributes` extracts `container.image.name`, `container.image.tag`, `k8s.replicaset.name` | Coremetry already prefers the image tag, so no rewrite is needed for its own features | Multi-container pods need `container.id`/`k8s.container.name` on the resource (external (docs): https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/processor/k8sattributesprocessor/README.md) |
| B. `transform` after `k8sattributes`: `set(attributes["service.version"], attributes["container.image.tag"])`, optionally `where attributes["service.version"] == nil` | Fixes downstream sinks that read `service.version` | An override discards the manifest version (external (docs): https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/pkg/ottl/ottlfuncs/README.md). The same tool can derive `k8s.cluster.name` from the env with `replace_pattern` |
| C. `k8sattributes` `extract.metadata: [service.version]` | Precedence: annotation → `app.kubernetes.io/version` → image tag (external (docs): https://opentelemetry.io/docs/specs/semconv/non-normative/k8s-attributes/) | Newer than the chart's 0.111.0 (PR https://github.com/open-telemetry/opentelemetry-collector-contrib/pull/39335); semconv moves to plural `container.image.tags`, which Coremetry would store as JSON text |
| D. Manifest label `app.kubernetes.io/version` + downward API into `OTEL_RESOURCE_ATTRIBUTES` | App-side | Per-chart change |
| E. `resourcedetection` `openshift` detector for `k8s.cluster.name` (infrastructure, not env) | Removes risk 9.3.1 | Needs `config.openshift.io/infrastructures` RBAC (external (docs): https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/processor/resourcedetectionprocessor/internal/openshift/documentation.md) |
| F. Coremetry ingest "enrich" pipeline | Static per-cluster rules only; cannot copy attributes (per S3) | Does not reach Thanos or Elasticsearch |

A collector restart risks the known wedge (`CLAUDE.md` pitfalls), so this is a maintenance-window item. **For v2 no collector change is required:** the version comes from KSM `kube_pod_container_info`, and spans only add a label.

---

## 10. Proposed schema

### 10.1 Extend `workload_rollouts` or add tables?

**Add tables.** Extending `workload_rollouts` does not work:
1. Its identity `(cluster_id, namespace, workload, revision, started_at)` is span-derived (`rollout_schema.go:77`), and ClickHouse cannot re-key a table (`MODIFY ORDER BY` only appends columns added in the same ALTER; external (docs): https://clickhouse.com/docs/sql-reference/statements/alter/order-by).
2. Two identity systems in one RMT never merge.
3. `workload_kind` is not in the key.
4. `started_at` means "first span bucket" to `score.go` and the stats queries.
5. It carries a two-writer read-modify-write contract (`rollout_schema.go:29-37`) that v2 must not extend.

**Reused unchanged:**
- `workload_revision_activity_1m` (service ↔ workload, version labels, impact services);
- `service_version_5m`, `service_env_summary_5m` (impact);
- the SSE kind, the tail cursor pattern and the 0012 wizard pattern.

`workload_rollouts` and `rollout_reconcile_runs` stay read-only for v1 history until TTL, then are retired through the removed-tables ledger (P6).

### 10.2 Rules applied (`.claude/skills/clickhouse-schema/SKILL.md`)

- State ⇒ `ReplacingMergeTree(version)`, `version UInt64 DEFAULT toUnixTimestamp64Nano(now64(9))`, reads with `FINAL` (§1 decision tree, SKILL.md:27-31).
- **ORDER BY = dedup key, exclusively** (O3, SKILL.md:137). **No PARTITION BY** on low-volume state, so rule P1 cannot occur (SKILL.md:92-98).
- **No Nullable**; sentinels `''`/`0`/epoch (C4). **LowCardinality only where structurally bounded or measured** (C2/C3); unmeasured names stay `String`.
- TTL `toDate(x) + INTERVAL N DAY` (P2). The engine is one of the four bases `adaptDDL` converts (E1).
- **One writer per table**, full-row writes carrying every field (E2 / invariant 4), with an **explicit client `version`** on every write (the Replicated insert-dedup lesson of `ai_eval_runs`/`settings.go`, per S5).
- **Codecs:** none on these state tables, as with the v1 state tables (`rollout_schema.go:52-78`, `migrations/0012_rollout_layer.sql:113-139`). That keeps `0015` byte-identical to `store.go` (the 0012 contract, `0012_rollout_layer.sql:11-17`). The ZSTD(3)/ZSTD(1) split (C1) concerns telemetry and rollups.
- `DateTime64(3)` everywhere, bound with tz-less `toDateTime64(?,3,'UTC')` (CLAUDE.md pitfall).
- **Writer contract (v0.10.959 review):** a DEFAULT-less column is NOT mandatory in ClickHouse — an omitted or zero TTL anchor stores 1970-01-01 and the row is deleted at the next TTL merge. Every P2/P3/P4 writer rejects a zero or pre-epoch TTL anchor (`started_at`, `last_seen_at`, `changed_at`, `op_started_at`, `last_verified_at`, `first_requested_at`, and `rollout_worker_runs.started_at` for every worker) with a pure validator pinned in its own tests. No CHECK constraint (operator decision if wanted).
- **`argocd_app_status` fold (P3):** at most one row per (app, tick); a sync completion and the tuple change it causes in the same tick fold into one `change_kind='sync'` row carrying the new tuple and `sync_phase` (`change_kind` is not in ORDER BY). The P3 writer pins this with a unit test.

### 10.3 DDL (single-node form; `adaptDDL` turns each into `ReplicatedReplacingMergeTree('<prefix>/state/<name>','{shard}-{replica}',version)` in cluster mode)

#### 10.3.1 `rollout_events`: one row per rollout, updated in place (KSM is the source of truth)

```sql
CREATE TABLE IF NOT EXISTS rollout_events (
  cluster_id           LowCardinality(String),        -- Remote Cluster EffectiveID(), never the span value
  namespace            LowCardinality(String),        -- workload_rollouts precedent
  workload_kind        LowCardinality(String),        -- Deployment | StatefulSet | DaemonSet | Rollout (reserved: DeploymentConfig)
  workload             String,                        -- C3: unmeasured -> String
  incarnation_at       DateTime64(3),                 -- see decision 9: kube_<kind>_created if present, else KSM sample time of the first complete read (carried)
  generation           UInt64,                        -- metadata.generation that STARTED the rollout
  started_at           DateTime64(3),                 -- scrape timestamp of the first sample at `generation`; written once, carried
  status               LowCardinality(String),        -- progressing | succeeded | stuck | rolled_back | superseded
  change_type          LowCardinality(String),        -- rollout | config | rollback | initial  (scale / annotation-only never written)
  observed_generation  UInt64        DEFAULT 0,
  spec_replicas        UInt32        DEFAULT 0,
  updated_replicas     UInt32        DEFAULT 0,
  available_replicas   UInt32        DEFAULT 0,
  new_revision         String        DEFAULT '',      -- RS name | STS update_revision | DS controller_revision_hash | ''
  old_revision         String        DEFAULT '',
  images               Array(String),                 -- sorted, deduped; kube_pod_container_info
  prev_images          Array(String),
  version_tag          String        DEFAULT '',      -- display version: image tag, else span effectiveVersionExpr (label only)
  stuck_reason         LowCardinality(String) DEFAULT '', -- progress_deadline | timeout | ''
  succeeded_at         DateTime64(3) DEFAULT 0,
  stuck_at             DateTime64(3) DEFAULT 0,
  finished_at          DateTime64(3) DEFAULT 0,       -- succeeded, rolled back or superseded
  note                 String        DEFAULT '',
  updated_at           DateTime64(3) DEFAULT now64(3), -- SSE tail cursor
  version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (cluster_id, namespace, workload_kind, workload, incarnation_at, generation)
TTL toDate(started_at) + INTERVAL 180 DAY
```

- **Key:** the brief's `(cluster, namespace, kind, name, generation)` plus `incarnation_at`, so that a delete/recreate cannot overwrite history (§4.3).
- **`incarnation_at`** = the KSM sample timestamp (`timestamp(kube_<kind>_metadata_generation)`) of the first complete read that shows the workload, truncated to the minute. Before minting it, the detector re-reads `rollout_workload_state FINAL` for that key. Residual risk under `lockDegraded`: at most one duplicate `initial` row per new workload, visible via `rollout_worker_runs.host`.
- **Status vocabulary:** v2 statuses map to the v1 API vocabulary on read (§2.2 `/api/rollouts` row); `superseded` follows the rule in §4.5.
- **Writer:** the `rollout-detector` leader only. Argo, Azure DevOps and impact data are **not** copied in. They live in their own single-writer tables and are joined at read time, which avoids a second writer and allows late evidence.
- `started_at` comes from the KSM sample timestamp (`timestamp(...)`), not from the tick clock, so two leaders under split-brain normally record the same value. If they first see the bump at different scrapes, `started_at` differs by at most one scrape interval, but `started_at` is not in the key, so both writes still land on one row and RMT keeps the latest version. While the START gate is still pending, the value is held in `rollout_workload_state.pending_started_at` (§10.3.2), so a failover mid-gate does not re-stamp it.
- **`rolled_back`:** when a later event is a `rollback` to an earlier revision, the detector (same writer) sets the earlier event's status. That keeps the v1 stats semantics `rolled_back/(completed+rolled_back)`.
- **Impact columns are deliberately absent.** The drawer computes impact on read from the MVs (the current behaviour, ≤ 6 h). Snapshot columns can be added later with `ADD COLUMN IF NOT EXISTS` (the safe class; two-boot contract) if P5 wants per-row badges (decision 23).

#### 10.3.2 `rollout_workload_state` (extra): durable detector memory

```sql
CREATE TABLE IF NOT EXISTS rollout_workload_state (
  cluster_id           LowCardinality(String),
  namespace            LowCardinality(String),
  workload_kind        LowCardinality(String),
  workload             String,
  incarnation_at       DateTime64(3),
  generation           UInt64,                        -- last generation processed
  observed_generation  UInt64        DEFAULT 0,
  pending_generation   UInt64        DEFAULT 0,       -- bump waiting for observed_generation / template evidence
  pending_started_at   DateTime64(3) DEFAULT 0,       -- KSM timestamp of first sample at pending_generation; becomes rollout_events.started_at when the gate passes (survives failover)
  current_revision     String        DEFAULT '',
  known_revisions      Array(String),                 -- bounded (knownRevisionsMax, default 32) revisions seen in this incarnation: ROLLBACK evidence; must outlive revisionHistoryLimit (§4.4 case c)
  images               Array(String),
  open_generation      UInt64        DEFAULT 0,       -- generation of the open rollout_events row (0 = none)
  first_seen_at        DateTime64(3),
  last_seen_at         DateTime64(3),                 -- touched at most once per day when nothing else changes
  version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (cluster_id, namespace, workload_kind, workload)
TTL toDate(last_seen_at) + INTERVAL 400 DAY
```

- **Why it is needed:**
  - `_created` is denylisted, so there is no RS age and no incarnation time (§4.3);
  - pure-scale detection needs the previous revision set;
  - failover must not re-emit or miss events (the v1 reconciler rebuilds from `RolloutRecentRows`, `reconciler.go:331`; baselines are not events, so `rollout_events` alone cannot hold them);
  - the first-ever run writes baselines here, and **no events**.
- **Writer:** `rollout-detector` only. Rows are written only on change (plus the daily touch).
- The TTL (400 d) is longer than the `rollout_events` TTL (180 d), so a recreate after state expiry cannot collide with a live event row.

#### 10.3.3 `argocd_app_status`: state changes only (change log)

```sql
CREATE TABLE IF NOT EXISTS argocd_app_status (
  instance_id          LowCardinality(String),        -- argocd settings instance id (tens)
  app_namespace        LowCardinality(String),        -- Application CR namespace (§5.2)
  app_name             String,                        -- C3: up to ~40k, unmeasured
  changed_at           DateTime64(3),                 -- tick time truncated to the tick interval: identical across two leaders only when both observe the change in the same interval. Under lockDegraded a change can land twice, at most one tick apart; readers collapse consecutive identical tuples
  change_kind          LowCardinality(String),        -- baseline | appeared | state | sync | deleted
  sync_status          LowCardinality(String),        -- Synced | OutOfSync | Unknown
  health_status        LowCardinality(String),        -- Healthy | Progressing | Degraded | Suspended | Missing | Unknown
  operation            LowCardinality(String) DEFAULT '', -- '' | sync | delete (transient label)
  sync_phase           LowCardinality(String) DEFAULT '', -- change_kind=sync: Succeeded | Failed | Error (argocd_app_sync_total)
  autosync_enabled     LowCardinality(String) DEFAULT '', -- 'true' | 'false' | '' (label absent, Argo <= 2.8)
  project              String        DEFAULT '',
  repo                 String        DEFAULT '',
  dest_server          String        DEFAULT '',      -- raw; normalised at match time
  dest_namespace       LowCardinality(String) DEFAULT '',
  cluster_id           LowCardinality(String) DEFAULT '', -- dest_server -> apiServerUrls -> EffectiveID(); '' = unmapped (counted)
  version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (instance_id, app_namespace, app_name, changed_at)
TTL toDate(changed_at) + INTERVAL 180 DAY
```

- **Writer:** `argocd-metrics` only (60 s). A row is written only when the tuple `(sync, health, operation, autosync, project, repo, dest_*)` changes, or on a sync completion.
- **Latest state** = `LIMIT 1 BY instance_id, app_namespace, app_name` over FINAL, for the mapped apps only (point prefix).
- **After failover** the worker rebuilds the previous state from this table before its first diff; otherwise it would re-emit about 40k "changes".
- An app idle longer than the TTL loses its last row and is re-baselined on the next full inventory (self-healing).
- **Why a change log rather than latest-only:** `out_of_band` uses "went OutOfSync near the anchor", and the drawer can show the Argo state at rollout time.

#### 10.3.4 `argocd_sync_events`: operations from the Argo API

```sql
CREATE TABLE IF NOT EXISTS argocd_sync_events (
  instance_id          LowCardinality(String),
  app_namespace        LowCardinality(String),
  app_name             String,
  op_started_at        DateTime64(3),                 -- operationState.startedAt (== history.deployStartedAt, to verify)
  phase                LowCardinality(String),        -- Running | Succeeded | Failed | Error | Terminating
  finished_at          DateTime64(3) DEFAULT 0,
  automated            UInt8         DEFAULT 0,       -- initiatedBy.automated
  initiator            String        DEFAULT '',      -- initiatedBy.username verbatim ('' when automated)
  sync_revision        String        DEFAULT '',      -- syncResult.revision
  revisions            Array(String),                 -- multi-source
  repo_url             String        DEFAULT '',      -- sources[0].repoURL
  target_revision      String        DEFAULT '',
  history_id           Int64         DEFAULT -1,      -- status.history[].id; -1 = not in history (ids start at 0)
  synced_workloads     Array(String),                 -- 'Kind/namespace/name' (Deployment|StatefulSet|DaemonSet|DeploymentConfig|Rollout) from syncResult.resources: evidence only
  managed_workloads    Array(String),                 -- 'Kind/namespace/name' from status.resources[] at GET time: exact-mapping input
  dest_server          String        DEFAULT '',
  dest_namespace       LowCardinality(String) DEFAULT '',
  cluster_id           LowCardinality(String) DEFAULT '',
  retry_count          UInt32        DEFAULT 0,
  message              String        DEFAULT '',
  version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (instance_id, app_namespace, app_name, op_started_at)
TTL toDate(op_started_at) + INTERVAL 180 DAY
```

- **Writer:** `argocd-api` only. A Running row and its later Succeeded row share the key, so RMT merges them.
- Metrics-observed sync completions are **not** written here (they have no `op_started_at`); they live in `argocd_app_status` as `change_kind='sync'`.
- Coremetry becomes the long-term record beyond Argo's 10-entry history.
- `managed_workloads` of the latest row is the **exact** mapping input (§10.3.5), which avoids a separate resources table. `synced_workloads` is evidence only: `syncResult.resources` lists what one operation touched and shrinks on selective syncs.

#### 10.3.5 `argocd_app_mapping`: workload ↔ Application edges

```sql
CREATE TABLE IF NOT EXISTS argocd_app_mapping (
  cluster_id           LowCardinality(String),
  namespace            LowCardinality(String),
  workload_kind        LowCardinality(String),        -- '' for a namespace-level (weak) edge
  workload             String,                        -- '' for a namespace-level (weak) edge
  instance_id          LowCardinality(String),
  app_namespace        LowCardinality(String),
  app_name             String,
  match_method         LowCardinality(String),        -- manual | resource | pod_label | name | namespace
  match_class          LowCardinality(String),        -- exact (manual, resource) | estimated (pod_label, name) | weak (namespace)
  confidence           UInt8,                         -- 0..100
  candidates           UInt16        DEFAULT 1,       -- apps sharing (cluster_id, namespace) for weak edges
  first_matched_at     DateTime64(3),                 -- carried
  last_verified_at     DateTime64(3),                 -- touched at least daily
  removed_at           DateTime64(3) DEFAULT 0,       -- valid_to sentinel (entities precedent); 0 = live
  version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (cluster_id, namespace, workload_kind, workload, instance_id, app_namespace, app_name)
TTL toDate(last_verified_at) + INTERVAL 30 DAY
```

- **Keyed on the workload, not the service.** Service ↔ workload is many-to-many and already owned by the MV (`workload_revision_activity_1m`, via `RolloutRefsForService`). The key includes the app, because many apps share a namespace.
- **Service card read:** service → `(cluster_id, namespace, workload)` from the MV → edges (including the namespace-level weak edges).
- **Classification join:** `rollout_events (cluster_id, namespace, workload_kind, workload)` → edges, exact and estimated only.
- **Writer:** a mapper step inside the `argocd-metrics` leader (every 10 min and on a settings change). Manual pins live in `system_settings["argocd"]` and are copied in as `match_method='manual'`, so the table stays purely derived and purgeable.
- **`pod_label` edges** (estimated) come from `kube_pod_labels` `label_app_kubernetes_io_instance` or `label_argocd_argoproj_io_instance` (§4.2; §11 K2.4 measures their presence).

#### 10.3.6 `rollout_classification` (extra): trigger per rollout event

```sql
CREATE TABLE IF NOT EXISTS rollout_classification (
  cluster_id           LowCardinality(String),        -- = rollout_events key ...
  namespace            LowCardinality(String),
  workload_kind        LowCardinality(String),
  workload             String,
  incarnation_at       DateTime64(3),
  generation           UInt64,                        -- ... end of key
  started_at           DateTime64(3),                 -- copied for TTL and window math
  trigger              LowCardinality(String),        -- argo_auto | argo_manual | out_of_band | unknown
  basis                LowCardinality(String),        -- api | metrics | none
  confidence           UInt8,                         -- 0..100
  reason               String        DEFAULT '',      -- human-readable "why" (ships with the row)
  instance_id          LowCardinality(String) DEFAULT '',
  app_namespace        LowCardinality(String) DEFAULT '',
  app_name             String        DEFAULT '',
  match_method         LowCardinality(String) DEFAULT '',
  op_started_at        DateTime64(3) DEFAULT 0,       -- -> argocd_sync_events
  sync_revision        String        DEFAULT '',
  initiator            String        DEFAULT '',
  repo_url             String        DEFAULT '',
  final                UInt8         DEFAULT 0,       -- 1 once the window closed and inputs are complete
  evaluated_at         DateTime64(3),
  version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (cluster_id, namespace, workload_kind, workload, incarnation_at, generation)
TTL toDate(started_at) + INTERVAL 180 DAY
```

- **Writer:** a classifier step in the `argocd-metrics` leader. It re-evaluates rows with `final=0` (for example `started_at` within 24 h), so API evidence that arrives late upgrades a metrics-only estimate.
- No row means `unknown`, which also covers Argo being disabled.
- **The Rollouts `trigger` filter** is a semi-join **before LIMIT** in `rolloutWhere` (`internal/chstore/rollouts.go:317`). Filtering after LIMIT is a known trap in this code (`internal/api/rollout_detail.go:132`). Both tables are plain replicated state tables, so no GLOBAL IN is needed.

#### 10.3.7 `ado_commit_enrichment` (extra): commit → PR → author → pipeline

```sql
CREATE TABLE IF NOT EXISTS ado_commit_enrichment (
  repo_url_norm        String,                        -- lower-case host, no userinfo/.git, ssh→https
  sha                  String,
  repo_id              String        DEFAULT '',      -- Azure DevOps repository GUID; '' for unmapped_repo/not_git_sha
  enrichment_status    LowCardinality(String),        -- ok | no_pr | not_found | unmapped_repo | not_git_sha | auth | throttled | error | disabled
  attempts             UInt16        DEFAULT 0,
  next_retry_at        DateTime64(3) DEFAULT 0,
  first_requested_at   DateTime64(3),                 -- carried; TTL anchor
  enriched_at          DateTime64(3) DEFAULT 0,
  project              String        DEFAULT '',
  repo_name            String        DEFAULT '',
  commit_author        String        DEFAULT '',
  commit_author_email  String        DEFAULT '',
  committed_at         DateTime64(3) DEFAULT 0,
  commit_message       String        DEFAULT '',
  is_merge             UInt8         DEFAULT 0,
  pushed_by            String        DEFAULT '',
  pr_id                UInt64        DEFAULT 0,
  pr_title             String        DEFAULT '',
  pr_created_by        String        DEFAULT '',
  pr_closed_by         String        DEFAULT '',
  pr_closed_at         DateTime64(3) DEFAULT 0,
  merge_strategy       LowCardinality(String) DEFAULT '',
  build_number         String        DEFAULT '',
  build_url            String        DEFAULT '',
  reason               String        DEFAULT '',
  version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (repo_url_norm, sha)
TTL toDate(first_requested_at) + INTERVAL 180 DAY
```

- **Key:** `(repo_url_norm, sha)`, not `(repo_id, sha)`: `unmapped_repo` and `not_git_sha` rows have no `repo_id`, and the feed must join from `rollout_classification.repo_url` without a lookup.
- **Writer:** `ado-enrichment` only.
- The work queue is the set of distinct `(normalise(repo_url), sync_revision)` in `argocd_sync_events` without an `ok`/terminal row (plus the optional second hop from image tags, decision 21).
- Identities are stored verbatim (house rule).

#### 10.3.8 `rollout_worker_runs` (extra): one run log for all v2 workers

```sql
CREATE TABLE IF NOT EXISTS rollout_worker_runs (
  worker               LowCardinality(String),        -- rollout-detector | argocd-metrics | argocd-api | ado-enrichment
  started_at           DateTime64(3),
  host                 LowCardinality(String) DEFAULT '', -- writing pod (split-brain separator; v1 precedent)
  finished_at          DateTime64(3),
  status               LowCardinality(String),        -- ok | partial | failed | skipped
  scopes_total         UInt16        DEFAULT 0,       -- clusters or instances
  scopes_ok            UInt16        DEFAULT 0,
  series_read          UInt32        DEFAULT 0,
  truncated            UInt8         DEFAULT 0,
  partial_response     UInt8         DEFAULT 0,
  rows_written         UInt32        DEFAULT 0,
  unmapped             UInt32        DEFAULT 0,       -- dest_server / span values without a registry match
  api_calls            UInt32        DEFAULT 0,
  api_throttled        UInt32        DEFAULT 0,
  duration_ms          UInt32        DEFAULT 0,
  error                String        DEFAULT '',
  version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (worker, started_at, host)
TTL toDate(started_at) + INTERVAL 30 DAY
```

- **Why not reuse `rollout_reconcile_runs`:**
  - its columns (`span_ms`, `ksm_ms`) cannot say "0 series" vs "truncated" vs "partial", which is the v1 blind spot (`rollouts-audit.md:501-503`);
  - squeezing a worker name into `host` would be a hack.
- Each worker writes only its own rows (`worker` in the key), which is effectively one writer per key range.

### 10.4 Writer and leader matrix

| Table | Writer (leader key) | Cadence | Reads |
|---|---|---|---|
| `rollout_events`, `rollout_workload_state` | `rollout-detector` | 30 s (decision 8) | KSM via `thanos.WorkerQuery` |
| `argocd_app_status`, `argocd_app_mapping`, `rollout_classification` | `argocd-metrics` (status diff → mapper every 10 min → classifier) | 60 s | hub Thanos; own tables; `rollout_events`; `argocd_sync_events`; MV |
| `argocd_sync_events` | `argocd-api` | change-driven, rate-limited; **skipped while `lockDegraded`** | `argocd_app_status` changes |
| `ado_commit_enrichment` | `ado-enrichment` | 60 s, budgeted; **skipped while `lockDegraded`** | `argocd_sync_events` (distinct `(normalise(repo_url), sync_revision)`) |
| `rollout_worker_runs` | every worker, own `worker` value | per tick | — |

### 10.5 Registration and migration

- **Boot:** all eight `CREATE`s go in the `tables` slice (pinned by `ddl_slice_placement_test.go`), in a new `internal/chstore/rollout_v2_schema.go`.
  - None enters `highVolumeTables`/`defaultShardPolicy`/`tablesWithoutTraceID`; they become one unified state replication group.
  - Copy the negative pin from `rollout_schema_test.go`.
- **Purge classification:** all eight go in `telemetryPurgeTables` (`internal/chstore/purge.go:24`; derived data, the `workload_rollouts` precedent at `:43`), unless decision 24 moves `argocd_sync_events` to `configPreserveTables` (with a reason line). `TestEveryCreatedTableIsClassified` (`purge_coverage_test.go:88`) enforces this. Caveat: Argo history older than Argo's own limit does not come back after a purge (decision 24).
- **Connection pool:** add the eight `FROM` names, plus the missing `FROM workload_rollouts`, to `stateTables` (`internal/chstore/conn_strategy_test.go:22`).
- **Prod (external Distributed):**
  - Ship `migrations/0015_rollouts_v2.sql` + `_rollback.sql` (the highest today is 0014), with `ON CLUSTER uptrace_all` and `ReplicatedReplacingMergeTree('/clickhouse/tables/state/<name>','{shard}-{replica}',version)` (the 0012 shape, `0012_rollout_layer.sql:137`), DDL identical to `store.go`.
  - Add it to the `FS` list (`migrations/embed.go:29`) and a wizard card (extend the rollout-layer card).
  - The wizard never runs at boot.
  - The ZooKeeper prefix is hard-coded in the migration, while boot resolves it at runtime (`internal/chstore/state_replication.go:226-232`); decision 25.

### 10.6 Read shapes (all FINAL, bounded, LIMIT, `max_execution_time`)

- **Feed:** `rollout_events FINAL WHERE started_at BETWEEN … [AND cluster_id/namespace/kind/workload/status] [AND key IN (SELECT key FROM rollout_classification FINAL WHERE trigger = ?)] ORDER BY started_at DESC LIMIT ?`, then batch-read classification, sync events and enrichment for the page (≤ 200 keys).
- **Tail:** a keyset on `(updated_at, key…)` with a watermark (the v1 cursor pattern). On an unpartitioned table this reads the whole small table under FINAL; measure first.
- **Detector previous state:** `rollout_workload_state FINAL WHERE cluster_id = ?`, keyset-paged.
- **Service Argo card:**
  1. MV refs for the service;
  2. mapping edges;
  3. latest status (`LIMIT 1 BY`);
  4. the last 5 sync events per app;
  5. the last 10 `rollout_events`.

  Coverage limits: the MV keeps 7 days (`workloadRevisionActivityDays = 7`, `rollout_schema.go:48`) and excludes DC pods (`workload=''`, filter at `rollout_schema.go:141`); `RolloutRefsForService` caps at 50 rows (`rolloutRefsForServiceMax`, `internal/chstore/rollout_problem_telemetry.go:33`). A service with no spans in 7 days, or a DC workload, shows "no workload seen in 7 d", not "no Argo app".
- **Impact (drawer):** a multi-value variant of `GetServicesEnvAggFiltered` over the MV family, cluster-scoped, gated by `EnvSummaryCovers` (§9.3); never raw spans.

### 10.7 Settings (not tables; CLAUDE.md invariant 6)

| Key | Content |
|---|---|
| `system_settings["thanos_clusters"]` (extend) | `apiServerUrls`, `argoSuffix`, `pairGroup` per cluster (§3.2) |
| `system_settings["argocd"]` (new) | `enabled`, `hubs[{clusterId, injectClusterLabel}]` (two hubs, §5.6), `envList`, `instances[{id, name, hubClusterId, hubNamespace, metricsJob, apiUrl, tokenRef, insecureSkipVerify, enabled, discovered, appsAnyNamespace}]`, `apiWorker{rps, burst, maxConcurrent}`, `classification{windowMin, outOfBandLookbackMin, metricsOnlyMode}`, `pins[]`, `reader{maxSeries, maxBodyMiB, timeoutS}`, `intervals{metricsS:60, inventoryMin:15, mapperMin:10, classifierReevalH:24}`, `mapping{nameConfidence, namespaceConfidence}` |
| `system_settings["rollouts"]` (extend) | `source` (`v1` or `v2`), `detectorIntervalS` (30), `stuckAfter` (10m), `ignoreScale` (true), `kinds[]`, `initialEvents` (bool), `observedGenWaitTicks` (§4.4 step 2), `incarnationAbsentTicks` (K, default 3; §4.3), `knownRevisionsMax` (32; §10.3.2) |
| `system_settings["devops_connection"]` (extend) | `tokenRef`, `enrichment{enabled, rps, maxAttempts, intervalS:60}`, `secondHop` (bool) |

Each key gets a reload topic handled in `internal/api/cache.go` (existing cases `:532` thanos, `:551` rollouts, `:588` devops; the console's `:538` in the working tree), a config-import republish entry, an audit action, and routes in their own files via `registerRoutesExtra`.

### 10.8 Where this document decides differently from its inputs

| Topic | Input proposal | This document | Why |
|---|---|---|---|
| Mapping key | S5: `(service_name, cluster_id, namespace)`, one app per key; annex: service-first with the app in the key | Workload-first, app in the key, no `service_name` | Many apps per namespace (environment fact); service ↔ workload is many-to-many and owned by the MV |
| Classification storage | S5: copied into `rollout_events` by the detector; annex Option B | Separate `rollout_classification` (annex Option B) | One writer per table; late API evidence; P2 does not depend on P3 |
| Incarnation | S5: `object_created_at` from `kube_<kind>_created` | `incarnation_at` from detector state, `_created` only when present | `_created` is denylisted on CMO, so S5's column would always be 0 |
| Enrichment storage | S5: columns on `argocd_sync_events`/`rollout_events`; S2: separate `(repo, sha)` table | Separate `ado_commit_enrichment` keyed `(repo_url_norm, sha)`, `repo_id` as a column | One writer; one lookup per SHA across clusters; every status has a key; the feed joins on the normalised URL |
| Run log | S5: reuse `rollout_reconcile_runs` with a host suffix; annex: `argocd_worker_runs` | One `rollout_worker_runs` with `worker` in the key | Truncated/partial/unmapped visibility |
| Status TTL | annex: 90 d | 180 d | Aligns with `rollout_events` for drawer history |
| `history_id` sentinel | S5: 0 | −1 | Argo history ids start at 0 |
| Sync events writer | annex: `argocd_app_operations` (API) | `argocd_sync_events` (brief's name), API worker only; metrics syncs in the status log | Keys differ, so rows could never merge |
| Enrichment location | read path only, never in workers (annex §8.4) | `ado-enrichment` worker (§8.5) | The brief's P4 says async; one lookup per SHA |
| Classification windows | −2m/+5m (+15m on a span anchor); `out_of_band` [−30m, +15m] (annex §8.3) | ±5m; `out_of_band` [−30m, +5m] (§7.5) | KSM anchor |
| Migration | `0015_argocd_layer.sql` (annex §7.5) | `0015_rollouts_v2.sql` (§10.5) | — |
| Phase numbering | P0–P9, P6 = API worker (annex §10.1) | P1–P6, P6 = retirement (§12.1) | This document's numbering is authoritative |
| Truncation | Q-2: optionally fix in P1, including entity `SnapshotQueries` (annex §10.1, §10.4) | Decision 3 | v2 does not read the entity layer |
| Filter label | "Tetikleyici" (annex §9.3) | Decision 4 | — |

The Service card, environment matrix and pair warning follow annex §9.1–§9.2, with `argocd_app_operations` read as `argocd_sync_events`.

---

## 11. Operator query pack (appendix: every live query)

### 11.0 How to run, safely

- **Where:**
  - §11.1–§11.3 run against **each target Thanos** (`cluster-a`, then `cluster-b`);
  - §11.3–§11.5 run against `<hub-thanos>`;
  - §11.6 needs one Argo token (optional); §11.7 needs a PAT; §11.8 runs against ClickHouse (or the Admin → K8s Coverage card).
- **Form:**
  - Grafana Explore, query type **Instant**, format **Table**, time = now. Or `curl` with form POST (as `scripts/probe/entity-discovery-prod.sh:24-31` does): `--data-urlencode query=… --data dedup=true --data partial_response=false --data timeout=30s`.
  - Report any **warning** text verbatim.
- **Cluster matcher:**
  - On a shared querier, add `<L>="<V>"` by hand to **every** selector, including inside `offset`, `unless`, `on()` and label-API `match[]` (Coremetry injects it only through `doQuery` and the working-tree PromQL console).
  - With a per-cluster URL (empty label in the registry), omit it.
  - If the registry has a NamespaceFilter, repeat K3 with `,namespace=~"<ns-filter>"`.
- **Hub:** if the hub entry has `ThanosLabelName`, run H0–H1 **without** the matcher first, then with it. A difference means label injection would hide Argo series.
- **Cost discipline:**
  - `count` before listing; list only when ≤ 50 or under `topk(50, …)`;
  - range functions `[1h]` before `[6h]`/`[24h]`;
  - never label-browse `name`, `repo` or `dest_server`; always send `start`/`end` to label APIs.
- **Tokenise before pasting.** Paste counts, label **names**, enum values, versions and warning text freely. Replace **values** consistently and keep the mapping private:
  - clusters → `cluster-a`/`cluster-b`;
  - `dest_server` → `https://api.cluster-a.<domain>:6443`;
  - instance namespace → `<team>-<env>`;
  - suffix → `<suffix-a>`;
  - project → `<project-1>`;
  - app or service names → `checkout-api`-style tokens.

  Keep `https://kubernetes.default.svc`, status values, `true`/`false` and port numbers literal.
- **Placeholders:** `<env-list>` = settings env alternation (for example `dev|test|prod`); `<suffix-list>` = `<suffix-a>|<suffix-b>`; `RE` = `` `[^-]+-[^-]+-.+-(<env-list>)-(<suffix-list>)` ``.

### 11.1 K: KSM on each target cluster (brief item 3; run on `cluster-a` and `cluster-b`)

**K0: plumbing**

| ID | Query | Expected shape | Why |
|---|---|---|---|
| K0.1 | `count(kube_node_info{<L>="<V>"})` | scalar > 0 | matcher works |
| K0.2 | `count by (job) (up{<L>="<V>",job=~".*state-metrics.*"} == 1)` | `kube-state-metrics` (+ `openshift-state-metrics`); an extra job = a second KSM | which KSM, and whether the denylist applies |
| K0.3 | `count by (version) (kube_state_metrics_build_info{<L>="<V>"})` | one version row (empty = telemetry port not scraped) | `reason` label ≥ v2.17 |
| K0.4 | `count(kube_deployment_spec_replicas{<L>="<V>"}) / count(count by (namespace, deployment) (kube_deployment_spec_replicas{<L>="<V>"}))` | exactly 1; > 1 = duplicates | dedup (§4.8) |
| K0.5 | `quantile(0.5, count_over_time(kube_deployment_metadata_generation{<L>="<V>"}[10m]))` | 10 ⇒ 60 s scrape; 20 ⇒ 30 s | tick choice (decision 8) |
| K0.6 | `time() - max(timestamp(kube_deployment_metadata_generation{<L>="<V>"}))` | < 90 s | freshness |

**K1: presence of the brief's series** (a missing row = denylisted, profile-dropped or not scraped; paste as a name → count table)

```promql
# K1.D  expected on CMO: created, annotations absent
count by (__name__) ({<L>="<V>",__name__=~"kube_deployment_(metadata_generation|status_observed_generation|spec_replicas|status_replicas|status_replicas_updated|status_replicas_available|status_replicas_ready|status_replicas_unavailable|status_condition|spec_paused|created|labels|annotations)"})
# K1.R  expected on CMO: created, metadata_generation, status_observed_generation absent
count by (__name__) ({<L>="<V>",__name__=~"kube_replicaset_(owner|created|spec_replicas|status_replicas|status_ready_replicas|metadata_generation|status_observed_generation|labels)"})
# K1.P
count by (__name__) ({<L>="<V>",__name__=~"kube_pod_(container_info|owner|labels|info|created|start_time)"})
# K1.S
count by (__name__) ({<L>="<V>",__name__=~"kube_statefulset_(metadata_generation|status_observed_generation|replicas|status_replicas|status_replicas_updated|status_replicas_ready|status_replicas_available|status_replicas_current|status_current_revision|status_update_revision|created)"})
# K1.DS
count by (__name__) ({<L>="<V>",__name__=~"kube_daemonset_(metadata_generation|status_observed_generation|status_desired_number_scheduled|status_updated_number_scheduled|status_number_available|status_number_unavailable|status_number_ready|status_current_number_scheduled|created)"})
# K1.H  (HPA share explains generation noise)
count by (scaletargetref_kind) (kube_horizontalpodautoscaler_info{<L>="<V>"})
```

Cross-check without PromQL: `GET /api/v1/label/__name__/values?match[]={<L>="<V>",__name__=~"kube_(deployment|replicaset|statefulset|daemonset|replicationcontroller)_.*"}&start=<now-600>&end=<now>`.

**K2: label sets** (label names or enum values only)

| ID | Query | Paste |
|---|---|---|
| K2.1 | `GET /api/v1/labels?match[]=<series>{<L>="<V>"}&start=<now-300>&end=<now>` for `kube_pod_container_info`, `kube_replicaset_owner`, `kube_deployment_status_condition`, `kube_statefulset_status_update_revision`, `kube_deployment_labels` | label-name lists (if `match[]` is unsupported, use K2.2/K2.3) |
| K2.2 | `count(kube_pod_container_info{<L>="<V>"})` and the same with `,image_spec!=""` / `,image_id!=""` / `,image=~".+@sha256:.+"` / `,image_spec=~".+@sha256:.+"` | 5 numbers: which label yields the tag on CRI-O |
| K2.3 | `count by (owner_kind, owner_is_controller) (kube_replicaset_owner{<L>="<V>"})`; `count by (owner_kind) (kube_pod_owner{<L>="<V>"})`; `count by (condition, status, reason) (kube_deployment_status_condition{<L>="<V>"} == 1)` | enum tables (empty `reason` ⇒ KSM < v2.17) |
| K2.4 | `count(kube_pod_labels{<L>="<V>",label_X!=""})` for X in `pod_template_hash`, `controller_revision_hash`, `rollouts_pod_template_hash`, `deploymentconfig`, `app_kubernetes_io_instance`, `argocd_argoproj_io_instance`; plus `count(kube_deployment_labels{<L>="<V>",label_app_kubernetes_io_instance!=""})` (expect 0 on CMO) | numbers (DS revision source, mapping hints) |

**K3: cardinality against today's 1000-per-query cap** (any value > 1000 ⇒ the current path is truncated today)

```promql
count(kube_replicaset_owner{<L>="<V>",owner_kind="Deployment"})
count(kube_replicaset_spec_replicas{<L>="<V>"})
count(kube_replicaset_spec_replicas{<L>="<V>"} > 0)
count(kube_deployment_metadata_generation{<L>="<V>"})
count(kube_statefulset_replicas{<L>="<V>"})
count(kube_daemonset_status_desired_number_scheduled{<L>="<V>"})
count(kube_pod_container_info{<L>="<V>"})
```

**K4: in-flight state now**

```promql
count(kube_deployment_status_condition{<L>="<V>",condition="Progressing",status="false"} == 1)                         # stuck now
count(max by (namespace, deployment) (kube_deployment_status_observed_generation{<L>="<V>"}) < on (namespace, deployment) max by (namespace, deployment) (kube_deployment_metadata_generation{<L>="<V>"}))  # controller lag
count(kube_deployment_spec_paused{<L>="<V>"} == 1)                                                                     # paused
count(kube_statefulset_status_update_revision{<L>="<V>"} unless on (namespace, statefulset, revision) kube_statefulset_status_current_revision{<L>="<V>"})  # STS mid-rollout
```

**K5: dynamics over 6 h** (numbers only; use `[1h]` if K3 > 5000). **This sizes the "generation bump ≠ rollout" noise.**

```promql
sum(changes(kube_deployment_metadata_generation{<L>="<V>"}[6h]))                        # generation bumps
count(changes(kube_deployment_spec_replicas{<L>="<V>"}[6h]) > 0)                         # deployments that scaled
count(count by (namespace, replicaset) (kube_replicaset_owner{<L>="<V>",owner_kind="Deployment"}) unless count by (namespace, replicaset) (kube_replicaset_owner{<L>="<V>",owner_kind="Deployment"} offset 6h))   # new RSs
count((kube_replicaset_spec_replicas{<L>="<V>"} > 0) and on (namespace, replicaset) (kube_replicaset_spec_replicas{<L>="<V>"} offset 6h == 0))   # reactivated RSs (rollback / undo candidates)
sum(changes(kube_statefulset_metadata_generation{<L>="<V>"}[6h]))
sum(changes(kube_daemonset_metadata_generation{<L>="<V>"}[6h]))
```

**K6: the v1 leg as it runs today** (`internal/rollout/ksm.go:44-47`; answers "is v1 truncated or blind?")

```promql
count(kube_replicaset_owner{<L>="<V>",replicaset!="",owner_kind="Deployment"})
count(kube_replicaset_spec_replicas{<L>="<V>",replicaset!=""})
count(kube_replicaset_status_ready_replicas{<L>="<V>",replicaset!=""})
count(kube_replicaset_created{<L>="<V>",replicaset!=""})
```

### 11.2 D: DeploymentConfig usage (each target)

```promql
count(openshift_deploymentconfig_spec_replicas{<L>="<V>"})
count(openshift_deploymentconfig_spec_replicas{<L>="<V>"} > 0)
count(count by (namespace) (openshift_deploymentconfig_spec_replicas{<L>="<V>"}))
count(kube_pod_owner{<L>="<V>",owner_kind="ReplicationController"})
count by (owner_kind) (kube_replicationcontroller_owner{<L>="<V>"})      # EXPERIMENTAL; empty on the minimal profile
count by (__name__) ({<L>="<V>",__name__=~"kube_replicationcontroller_.*|openshift_deploymentconfig_.*"})
sum(changes(openshift_deploymentconfig_metadata_generation{<L>="<V>"}[6h]))
```

Expected shape: small integers. Paste: numbers, and the DC share = line 2 ÷ (line 2 + `count(kube_deployment_spec_replicas{<L>="<V>"} > 0)`).

### 11.3 R: Argo Rollouts presence (each target **and** the hub)

```text
GET /api/v1/label/__name__/values?match[]={<L>="<V>",__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"}&start=<now-600>&end=<now>
```
```promql
count by (__name__) ({<L>="<V>",__name__=~"rollout_.*|analysis_run_.*|experiment_.*|argo_rollouts_controller_info"})   # the brief's group by (__name__), safe form
count(kube_replicaset_owner{<L>="<V>",owner_kind="Rollout"})
count by (strategy, traffic_router, phase) (rollout_info{<L>="<V>"})
```

Expected: empty if unused. Paste: metric names and counts.

### 11.4 H: hub Argo metrics (`<hub-thanos>`)

**H0: preflight**

```promql
# H0.1 total series (≈ 40000?)
count(argocd_app_info)
# H0.2 HA dedup: expect 1 row without prometheus_replica
count by (prometheus, prometheus_replica) (argocd_app_info)
# H0.3 cluster labels on Argo series (compare with H0.5 and the hub entry's <L>)
count by (cluster, cluster_id, cluster_name, k8s_cluster, openshift_cluster, prometheus, tenant, tenant_id) (argocd_app_info)
# H0.4 unique apps (= H0.1 unless shards/replicas duplicate)
count(group by (namespace, exported_namespace, name) (argocd_app_info))
# H0.5 same label candidates on kube_node_info (internal/thanos/cluster_detect.go:36)
count by (cluster, cluster_id, cluster_name, k8s_cluster, openshift_cluster, prometheus, tenant, tenant_id) (kube_node_info)
```

**H1: instances and the namespace case** (brief: `count by (namespace, job)`)

```promql
# H1.1 instances: one row per instance; job ≈ <team>-<env>-metrics
count by (namespace, job) (argocd_app_info)
# H1.2 THE CASE: (A) exported_namespace present and == namespace; (B) absent; (C) present and != namespace
count by (namespace, exported_namespace, job) (argocd_app_info)
# H1.3 collisions (empty = none)
count by (exported_namespace) (group by (namespace, exported_namespace) (argocd_app_info)) > 1
count by (job) (group by (namespace, job) (argocd_app_info)) > 1
# H1.4 controller shards per instance
count by (namespace) (group by (namespace, pod) (argocd_app_info))
# H1.5 instances incl. zero-app ones (discovery source)
count by (namespace, job) (argocd_cluster_info)
# H1.6 shard sizing vs caps
max(count by (namespace) (argocd_app_info))
max(count by (namespace, dest_server) (argocd_app_info))
count(count by (namespace, dest_server) (argocd_app_info) > 1000)
```

**H2: targets** (brief: `count by (dest_server)`)

```promql
count by (dest_server) (argocd_app_info)                                   # tens of rows; paste tokenised
count(group by (dest_server) (argocd_app_info))
count(argocd_app_info{dest_server=""})                                     # destination.name or failed lookup
count(argocd_app_info{dest_server="https://kubernetes.default.svc"})       # apps on the hub itself
count by (port) (label_replace(group by (dest_server) (argocd_app_info), "port", "$1", "dest_server", `https?://[^/]+:([0-9]+)/?`))
count(group by (dest_server, dest_namespace) (argocd_app_info))
count_values("apps_per_target_ns", count by (dest_server, dest_namespace) (group by (namespace, exported_namespace, name, dest_server, dest_namespace) (argocd_app_info)))
count(argocd_app_info{dest_namespace=""})
```

**H3: duplicates and pairs** (brief: `count by (name, dest_server) > 1`, in count-first form)

```promql
count(count by (name, dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info)) > 1)   # true duplicates; list only if ≤ 50
count_values("same_name_same_server", count by (name, dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info)))
count(count by (name) (group by (namespace, name) (argocd_app_info)) > 1)                                                   # same name in two instances
count_values("clusters_per_base", count by (base) (group by (base, dest_server) (label_replace(argocd_app_info{name=~`.+-(<env-list>)-(<suffix-list>)`}, "base", "$1", "name", `(.+)-(<suffix-list>)`))))
count(count by (base) (group by (base, sync_status, health_status) (label_replace(argocd_app_info{name=~`.+-(<suffix-a>|<suffix-b>)`}, "base", "$1", "name", `(.+)-(<suffix-a>|<suffix-b>)`))) > 1)   # pair status mismatch now
```

**H4: status and autosync** (brief: `count by (sync_status, health_status)`)

```promql
count by (sync_status, health_status) (group by (namespace, exported_namespace, name, sync_status, health_status) (argocd_app_info))   # ≤ 18 rows; paste verbatim
count by (operation) (argocd_app_info)
count by (autosync_enabled) (group by (namespace, exported_namespace, name, autosync_enabled) (argocd_app_info))                   # one unlabelled row ⇒ Argo ≤ 2.8
count by (namespace, autosync_enabled) (argocd_app_info)
```

**H5: label sets and versions** (brief: label sets of `argocd_app_info` and `argocd_app_sync_total`)

- `GET /api/v1/labels?match[]=argocd_app_info&start=<now-600>&end=<now>`, and the same for `argocd_app_sync_total`, `argocd_cluster_info` and `argocd_app_labels`. **Paste the label names verbatim.**
- Fallback: `topk(1, argocd_app_info)` and `topk(1, argocd_app_sync_total)`. **Paste the names only.**
- Presence probes (empty = absent):
  - `count(argocd_app_info{autosync_enabled!=""})`
  - `count(argocd_app_info{exported_namespace!=""})`
  - `count(argocd_app_sync_total{dry_run!=""})` (v3.1+)
  - `count(argocd_app_sync_total{exported_namespace!=""})`
  - `count by (namespace, version) (argocd_info)` (v2.13+)
- Distinct counts (drive the LowCardinality choices in §10): `count(group by (<L>) (argocd_app_info))` for `<L>` in `name, project, repo, dest_server, dest_namespace, namespace, job`.

**H6: sync activity and churn** (sizes the 60 s worker)

```promql
count(argocd_app_sync_total)
sum by (phase) (increase(argocd_app_sync_total[1h]))            # then [24h]; undercounts series born by their first sync
sum by (autosync_enabled) (sum by (namespace, exported_namespace, name) (increase(argocd_app_sync_total[24h])) * on (namespace, exported_namespace, name) group_left (autosync_enabled) group by (namespace, exported_namespace, name, autosync_enabled) (argocd_app_info))
count(resets(argocd_app_sync_total[24h]) > 0)
# transitions per hour and per 2 min (run at 2–3 times of day)
count(group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info) unless on (namespace, exported_namespace, name, sync_status, health_status, operation) group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info offset 1h))
count(group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info) unless on (namespace, exported_namespace, name, sync_status, health_status, operation) group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info offset 2m))
# non-steady set size (the per-tick fetch)
count(group by (namespace, exported_namespace, name) (argocd_app_info unless argocd_app_info{sync_status="Synced",health_status="Healthy",operation=""}))
```

`[24h]` over about 20k series can hit querier sample or timeout limits. Split by `{namespace="<team>-<env>"}` or use 4 × `[6h]`.

### 11.5 N: naming convention % (brief item 5; hub)

```promql
# N1 overall match ratio
count(group by (namespace, exported_namespace, name) (argocd_app_info{name=~RE})) / count(group by (namespace, exported_namespace, name) (argocd_app_info))
# N2 per instance (the "or … * 0" branch keeps zero-match instances visible)
(count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info{name=~RE})) or count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info)) * 0) / count by (namespace) (group by (namespace, exported_namespace, name) (argocd_app_info))
# N3 near misses (counts only)
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~RE, name=~`.+-(<env-list>)-[^-]+`}))   # known env, unknown suffix
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~RE, name=~`.+-(<suffix-list>)`}))       # unknown env, known suffix
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~RE, name=~`[^-]+(-[^-]+){0,3}`}))        # four tokens or fewer
# N4 unknown last tokens (paste tokenised)
topk(20, count by (sfx) (label_replace(group by (namespace, exported_namespace, name) (argocd_app_info{name!~RE}), "sfx", "$1", "name", `.*-([^-]+)`)))
# N5 suffix auto-derivation: dominant last token per dest_server and its share
topk by (dest_server) (1, count by (dest_server, sfx) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "sfx", "$1", "name", `.*-([^-]+)`)))
max by (dest_server) (count by (dest_server, sfx) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "sfx", "$1", "name", `.*-([^-]+)`))) / count by (dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info))
# N6 a suffix on more than one server (breaks suffix -> cluster)
count by (sfx) (group by (sfx, dest_server) (label_replace(argocd_app_info{name=~`.+-(<suffix-list>)`}, "sfx", "$1", "name", `.+-(<suffix-list>)`))) > 1
# N7 env purity per instance (> 1 row = an instance serves several envs)
count by (namespace) (group by (namespace, env) (label_replace(argocd_app_info{name=~RE}, "env", "$1", "name", `.+-(<env-list>)-[^-]+`))) > 1
```

**Listing non-matching names** (operator-only; **do not paste names**):
1. `count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~RE}))`;
2. then `count by (namespace, project) (…)`;
3. then `topk(50, group by (namespace, project, name, dest_server) (argocd_app_info{name!~RE, project="<project-1>"}))`.

Also state whether team names ever contain hyphens.

### 11.6 A: Argo CD API reachability (optional; read-only; one instance)

Use a read-only token, and **never pass `refresh`**.
1. `curl -sS -H "Authorization: Bearer $TOKEN" https://<instance-server-route>/api/version` → paste the version.
2. `curl -sS -H "Authorization: Bearer $TOKEN" "https://<instance-server-route>/api/v1/applications/<one-app>?appNamespace=<app-ns>&project=<project-1>"` → paste the **presence** (yes/no) only, not values, of:
   - `status.operationState.operation.initiatedBy.automated` and `.username`;
   - `status.operationState.syncResult.resources[].kind` (counts per kind);
   - `status.resources[].kind` (counts per kind; the exact-mapping input, §10.3.4);
   - `status.history[].initiatedBy`, `status.history[].deployStartedAt`;
   - `status.sync.revision`;
   - `spec.source.repoURL` vs `spec.sources` (multi-source?).
3. State whether `username` values look like local accounts, SSO emails or a CI account. Do not paste them.

### 11.7 V: Azure DevOps verification (read-only; one known sync revision)

```bash
# commit (Code Read)
curl -sS -u ":$ADO_PAT" "https://<ado-host>/<collection>/<project>/_apis/git/repositories/<repo>/commits/<sha>?api-version=6.0" | jq '{has_author:(.author!=null), parents:(.parents|length), has_web:(._links.web!=null)}'
# PRs for the commit (read-only query despite POST)
curl -sS -u ":$ADO_PAT" -H 'Content-Type: application/json' -X POST \
  "https://<ado-host>/<collection>/<project>/_apis/git/repositories/<repo>/pullrequestquery?api-version=6.0" \
  -d '{"queries":[{"type":"lastMergeCommit","items":["<sha>"]},{"type":"commit","items":["<sha>"]}]}' | jq '[.results[] | to_entries[] | {n:(.value|length)}]'
# builds for the commit (needs Build Read) — expect 401/403 if the PAT lacks the scope
curl -sS -o /dev/null -w '%{http_code}\n' -u ":$ADO_PAT" "https://<ado-host>/<collection>/<project>/_apis/build/builds?repositoryId=<repoGuid>&repositoryType=TfsGit&queryOrder=finishTimeDescending&\$top=50&api-version=6.0"
# statuses (Code Read only)
curl -sS -u ":$ADO_PAT" "https://<ado-host>/<collection>/<project>/_apis/git/repositories/<repo>/commits/<sha>/statuses?latestOnly=true&api-version=6.0" | jq '.count'
```

- For `<sha>`, use `status.sync.revision` of one app from §11.6.
- **Paste:** HTTP codes, counts and yes/no only; Server vs Services; whether the Argo repo is an app repo or a GitOps/manifests repo; whether any app uses a Helm chart repository; whether Coremetry needs an egress proxy to reach Azure DevOps.

### 11.8 T: span attributes (ClickHouse; bounded; or the Admin → K8s Coverage card)

```sql
-- T1 attribute coverage per span cluster value (15-minute sample)
SELECT cluster,
       count()                                              AS sampled,
       countIf(has(res_keys, 'service.version'))             AS svc_version,
       countIf(has(res_keys, 'container.image.tag'))         AS img_tag,
       countIf(has(res_keys, 'container.id'))                AS container_id,
       countIf(has(res_keys, 'k8s.deployment.name'))         AS depl,
       countIf(has(res_keys, 'k8s.replicaset.name'))         AS rs,
       countIf(has(res_keys, 'k8s.statefulset.name'))        AS sts,
       countIf(has(res_keys, 'k8s.daemonset.name'))          AS ds,
       countIf(has(res_keys, 'deployment.environment.name')) AS env_name,
       countIf(has(res_keys, 'k8s.cluster.name'))            AS k8s_cluster,
       countIf(has(res_keys, 'openshift.cluster.name'))      AS ocp_cluster
FROM (SELECT cluster, res_keys FROM spans
      WHERE time >= now() - INTERVAL 15 MINUTE
      LIMIT 200 BY service_name
      LIMIT 200000)
GROUP BY cluster ORDER BY sampled DESC LIMIT 50
SETTINGS max_execution_time = 20;

-- T2 derived-cluster form: env value vs cluster value (paste tokenised)
SELECT deploy_env, cluster, count() AS n
FROM (SELECT deploy_env, cluster FROM spans WHERE time >= now() - INTERVAL 15 MINUTE LIMIT 200 BY service_name LIMIT 200000)
GROUP BY deploy_env, cluster ORDER BY n DESC LIMIT 50
SETTINGS max_execution_time = 20;
```

- If the `cluster` column does not exist on prod `spans` (0011 not applied), replace `cluster` with `res_values[indexOf(res_keys,'k8s.cluster.name')]`.
- Also run `GET /api/settings/thanos/span-clusters` (admin) and paste `unmapped` count only.

### 11.9 Paste-back template

| ID | cluster-a | cluster-b | hub |
|---|---|---|---|
| K0.2 KSM jobs / K0.3 version / K0.4 dup ratio / K0.5 scrape | | | — |
| K1 presence table (name → count) | | | — |
| K2.2 image label forms; K2.3 `reason` present?; K2.4 pod labels | | | — |
| K3 counts (any > 1000?) | | | — |
| K4 stuck / lag / paused / STS mid-rollout | | | — |
| K5 gen bumps : new RS : reactivated RS : scaled deployments | | | — |
| K6 v1 leg counts (`_created` present?) | | | — |
| D DC counts and share | | | — |
| R Argo Rollouts names/counts | | | |
| H0.1 / H0.4 / dedup / label names (H0.3 vs H0.5) | — | — | |
| H1.2 case (A/B/C); instances; max apps per instance and shard | — | — | |
| H2 `dest_server` count, `""`, in-cluster, ports, apps per target ns | — | — | |
| H3 duplicates / pairs / pair mismatches | — | — | |
| H4 status table; autosync share | — | — | |
| H5 label names; versions per instance | — | — | |
| H6 syncs/h and /24h by phase and autosync; transitions/h and /2m; non-steady size | — | — | |
| N1–N7 ratios, near misses, suffix share and uniqueness, hyphenated teams? | — | — | |
| A (optional) version; field presence; username kind | — | — | |
| V HTTP codes, counts, repo kind, flavour, proxy | — | — | — |
| T1/T2 coverage and cluster form (per span cluster value) | | | — |
| Querier limits (timeout, max samples); warnings seen | | | |

---

## 12. Phase plan, conflicts and decisions

### 12.1 Phase plan (small reviewable commits; each ships alone via `/release` as its own v0.10.X; flags off until P2.3)

| Commit | Contents | Gate |
|---|---|---|
| **P1: settings, data model, reader (no behaviour change)** | | this approval |
| P1.1 | `thanos`: `WorkerQuery` + `WorkerLimits` over the console transport (§3.4); table tests on the `console_test.go` fake querier (49 999/50 000/50 001 series, oversize body, warnings → `Partial`, unresolved `TokenRef` → error) (/tdd) | — |
| P1.2 | Export `NamespaceMatcher`; the matcher, `#` and `match[]` work is already in the working tree (`cluster_matcher.go:236,470,481`) | — |
| P1.3 | Remote Cluster `apiServerUrls`/`argoSuffix`/`pairGroup`: normalise, uniqueness, carry-forward, snapshot, audit, ClustersTab, `/api/clusters/sources` entries | decision 7 |
| P1.4 | `system_settings["argocd"]` (hub, envList, instances with `tokenRef` only, reader limits, windows, pins), reload topic, config import, audit, admin route file, read-only discovery probe | P1.2, P1.3 |
| P1.5 | `system_settings["rollouts"]` v2 knobs (`source`, interval, `stuckAfter`, kinds, `initialEvents`, `observedGenWaitTicks`, `incarnationAbsentTicks`, `knownRevisionsMax`) | — |
| P1.6 | Chart: `extraEnv`/`envFrom`/`extraVolumes`/`extraVolumeMounts` on api **and** worker (`/helm-chart-coremetry`) | — |
| P1.7 | Schema in `store.go` (8 tables), purge classification, `stateTables`, negative shard-registry pin | decisions 6, 9, 24 |
| P1.8 | `migrations/0015_rollouts_v2.sql` + rollback + `embed.go` + wizard card | P1.7 |
| **P2: KSM detector** | | §11 K/D answered |
| P2.1 | Pure detector core (generation gate, RS/revision set diff including the recreated-RS case, scale/annotation filter, SUCCEEDED parity, STUCK, ROLLBACK, SUPERSEDED, new-incarnation rule, first-run baseline); table tests | P1.7 |
| P2.2 | Fetch via `thanos.WorkerQuery` (per cluster, `max by`, nsMatcher, skip on partial/truncated), leader `rollout-detector`, state rebuild on acquire, `rollout_worker_runs` | P1.1–P1.2, P2.1 |
| P2.3 | Read path: `/api/rollouts*` read `rollout_events` when `rollouts.source=v2` (shape kept; v2 → v1 status mapping); SSE tail on the new table; columns; 6-part `?rollout=` links with a part-count decoder; service-scoped read for `DeployHistoryPanel` and the other pod-churn consumers (§2.2). Label "Deployment/Rollouts" at every touchpoint: `frontend/src/lib/i18n.ts:41` (EN) and `:196` (TR); `frontend/src/components/CommandPalette.tsx:75` label plus the alias "deployments"; the hard-coded Topbar title (`frontend/src/pages/Rollouts.tsx:127`); the empty-state text (`Rollouts.tsx:168`); `frontend/src/lib/pageContext.ts:33` | P2.2; `0015` applied via the wizard in prod (§2.5) |
| P2.4 | Correlation: `RolloutEvidence` from `rollout_events` (+ `workloadKind`, `generation`, `incarnationAtNs`) + score mapping; `list_deployments`/`/api/changes` KSM-first + the `rollouts_layer` fix; `rolloutDeployEntries` (event > rollout > span); MCP `list_deploys`/`get_deploy_diff` source; `RolloutCandidate` fold; `RootCausePanel` `version_tag` | P2.3 |
| P2.5 | `RecentDeploy` source switch to `rollout_events` (display only); fix the stale comment at `problem_telemetry.go:442-448` | P2.4, decision 19 |
| **P3: Argo CD** | | §11 H/N answered |
| P3.1 | `argocd-metrics` worker: discovery, sharded inventory, non-steady fetch, sync-series detection, change log, baseline, rebuild on acquire, runs | P1.4, P1.7 |
| P3.2 | Mapper: name and pod label (estimated), namespace (weak), manual pins, unmapped counts | P3.1 |
| P3.3 | `argocd-api` worker: token-bucket limiter with an injected clock (§7.3), per-instance `tokenRef`, change-driven GETs, `argocd_sync_events`, resource (exact) edges from `status.resources[]`; never `refresh`; skip while degraded | P1.6, P3.1 |
| P3.4 | Classifier + `rollout_classification`; `?trigger=` filter (semi-join before LIMIT) + column + drawer line + `RolloutEvidence.Trigger` | P2.3, P3.2 (P3.3 for API basis) |
| P3.5 | Service page Argo card (`internal/api/argocd_service.go`, `pages/service/ArgoCard.tsx`): mapped apps, status, last operation, env matrix, pair warning | P3.2 |
| **P4: Azure DevOps** | | §11 V answered |
| P4.1 | `devops`: `GetCommit`, `PullRequestsForCommit` (shared `doPostJSON`), builds/statuses, scope-aware errors, 429/`Retry-After`, collection-wide repo list with URLs; PAT → `tokenRef` | decision 20 |
| P4.2 | `ado-enrichment` worker + table; `enrichment_status` in the feed/drawer | P3.3, P4.1 |
| P4.3 | MCP `get_change_history` (read-only over tables; `/mcp-tools`) | P4.2 |
| **P5: spans (version + impact only)** | | §11 T answered |
| P5.1 | `version_tag` from KSM image + span label; unify the four placeholder lists | P2.2 |
| P5.2 | Cluster-scoped before/after impact from the MV family, anchored at KSM start/success: a multi-value `cluster IN (…)` variant of `GetServicesEnvAggFiltered`, gated by `EnvSummaryCovers` (§9.3) | P2.3 |
| P5.3 | `docs/operator/rollouts-collector.md` (the §9.4 options); K8s Coverage counters for `service.version`, `container.image.tag`, `deployment.environment.name` | — |
| **P6: retirement** | v1 reconciler off (a live prod layer, §2.5); `workload_rollouts` read-only until TTL then ledgered; `/api/deployment-report` and `deploy-history` removed; pod-churn `/services/{name}/rollouts` replaced | P2.3 stable ≥ 2 weeks; every pod-churn consumer moved (§2.2) |
| Optional | DC slice; Argo Rollouts slice; audit-log actor; Problem/event emission for pair drift | decisions 11, 12, 18, 22 |

Every route goes in its own file via `registerRoutesExtra`, so `api.go` gains 0 lines (ratchet `.claude/baselines/api_go_lines`). Every admin write is audited, and every read uses `serveCached` with all inputs in the key.

### 12.2 Conflicts with existing code and decisions

| Plan item | Conflicts with | Resolution proposed |
|---|---|---|
| Worker-grade reader | House rule "new callers use promapi; don't refactor thanos" (`promapi.go:21-26`); the operator-approved console reader already lives in `internal/thanos/console.go` (working tree, header `:3-5`) with its own decoder | §3.4: the console is not re-platformed; workers use a new `WorkerQuery` entry over its transport; `doQuery` untouched; `docs/DECISIONS.md` records the second decoder (decision 1) |
| KSM as event source | v1 span-driven invariant (`reconcile.go:767`); annex Q-3 | Settled by the brief: KSM is the source of truth; rows for uninstrumented workloads now appear |
| Rollouts "source" filter | "Kaynak" already = `detectedBy` (`frontend/src/pages/Rollouts.tsx:64,211`); `ChangeRow.Source`, `RecentDeployEntry.Source` | Decision 4 |
| Problems for stuck rollouts or pair drift | Stale sweep at 3 × evaluator interval (`evaluator.go:1117`) vs the 3 min lease (`leader.go:87-96`) ⇒ flapping on failover | Decision 18 (no Problems in v2 by default; if wanted, a `PollerOwnedSubject` prefix, `problem.go:1513`) |
| Secret references "via Helm extraEnv + existingSecret" | The chart has no `extraEnv`/`envFrom`/volumes for Coremetry pods (`charts/coremetry/values.yaml:543` is go-demo only) | P1.6 (decision 20 covers the DevOps PAT) |
| Single writer on `workload_rollouts` | Two-writer RMW contract (`rollout_schema.go:29-37`) | Not extended; new single-writer tables |
| Hub metrics only in the hub Thanos | Hub entry would also join `/clusters`, entity sync, rollout detection; label injection | Decision 5 |
| `api.go` growth | Ratchet (`internal/api/api_go_size_test.go`; baseline 11662 at HEAD; the working tree only ratchets down, 11609 at review) | New route files only |
| Hashing or masking initiator emails | House rule: no PII redaction (`CLAUDE.md`) | Stored verbatim; not proposed |
| Deploy events for correlation | `events` has no cluster/namespace, random ids, `ORDER BY (time, id)` (per the annex §9.4) | No Argo events in v2 by default (decision 18) |

### 12.3 Decisions and open questions (numbered; each has a recommendation)

**Reader and settings**
1. **Worker reader (§3.4).** Keep the operator-approved PromQL console reader in `internal/thanos` as it is; add one exported `WorkerQuery` + `WorkerLimits` entry over its transport (fail-closed `TokenRef`, per-call `dedup`/`partial_response`); leave `doQuery` and `promapi` untouched; record the second decoder in `docs/DECISIONS.md`. *Recommend: approve.*
2. **Worker memory ceiling.** ≤ 50k series and ≤ 64 MiB body per call, 30–45 s timeout, enforced by `WorkerLimits` normalisation (the console's streaming decode is reused; the `ConsoleLimits` ceilings of 2000 series and 128 MiB stay). *Recommend: approve; revisit after H1.6/K3.*
3. **v1 KSM leg.** Move it to `WorkerQuery` in P1 (fixing its silent truncation) or leave it until P6? *Recommend: leave it; v2 replaces it.*
4. **"Source" filter naming.** URL `?trigger=` (argo_auto | argo_manual | out_of_band | unknown). In v2 `detectedBy` is always KSM, so drop the "Kaynak" (`by`) column and label the new `trigger` column "Kaynak" (TR) / "Source" (EN) under a **new column id**, so stored widths do not misapply. The API field name stays `trigger`, avoiding `ChangeRow.Source`. *Recommend: approve.*
5. **Hub entry.** An ordinary enabled Remote Cluster (it also shows in `/clusters` and gets entity/rollout processing), or a new "metrics-only" entry kind? *Recommend: ordinary entry + `hubClusterId` in the argocd blob; revisit if the hub's own workloads are unwanted.* **Approved 2026-09-26; amended for two hubs:** `hubs[{clusterId, injectClusterLabel}]` + per-instance `hubClusterId` (§5.6). `https://kubernetes.default.svc` is never stored on a Remote Cluster; it resolves to the instance's hub.
6. **Tables.** Approve the four named tables plus `rollout_workload_state`, `rollout_classification`, `ado_commit_enrichment` and `rollout_worker_runs`, and TTLs of 180 d (events, status, sync events, classification, enrichment), 400 d (state), 30 d (mapping, runs).
7. **`apiServerUrls` as a list**, with normalisation (default port `:6443`, no trailing slash, lower-case host). Is `pairGroup` free text or a managed list? One `argoSuffix` per cluster? *Recommend: list; free text; one suffix.*
8. **Detector tick.** The brief says 30 s; CMO scrapes KSM every 1 m (§4.2). *Recommend: 30 s default (cheap; with a 1 m scrape it cuts worst-case detection latency from about 2 min to about 1.5 min), revisited with K0.5.*
9. **Incarnation in the key** (`incarnation_at`), a deviation from the brief's key, needed because `_created` is denylisted. *Recommend: approve.*
10. **`initial` events.** Emit an event when a new workload first appears after bootstrap? *Recommend: yes, `change_type='initial'`; first-ever run = baselines only.*

**Coverage**
11. **Argo Rollouts.** Build the slice only if §11 R finds it. *Recommend: conditional.*
12. **DeploymentConfig.** "Not covered" state vs a DC slice, decided on the §11 D share. *Recommend: "not covered" unless the share is material.*
13. **Non-K8s (JBoss) services.** Keep span-inferred version first-seen as a **labelled fallback** where no KSM workload exists, or drop it? *Recommend: keep it as a fallback, labelled "inferred", never mixed into KSM events.*
14. **Old `?rollout=` links** resolve against `workload_rollouts` until its TTL; new links use the 6-part v2 key and the decoder dispatches on part count (§2.4). Acceptable? *Recommend: yes.*

**Classification and mapping**
15. **Metrics-only mode.** Before API credentials exist, show "estimated" `argo_manual` for autosync-off apps and ambiguous (≤ 0.5) for autosync-on apps, or only `unknown`? *Recommend: show estimates, clearly labelled.*
16. **Env source of truth.** Service-name suffix (hard-coded `-prod/-int/-uat/-prep`), `deploy_env` (`prod-<cluster>`), or the Argo name `<env>` segment? Align `envList` with the service suffixes?
17. **Trigger in Problem scoring.** Add a multiplier (for example `out_of_band` ×1.10)? *Recommend: not in v2; show the trigger in evidence only.*
18. **Emitting Problems/events** (stuck rollouts, pair drift, Argo syncs without a rollout). *Recommend: none in v2. If wanted later, use a `PollerOwnedSubject` exemption so the stale sweep does not flap on a 3 min lease failover.*
19. **RecentDeploy source.** Switch the DeployBox and chat deploy hint to `rollout_events`, keeping span inference as the labelled non-K8s fallback (decision 13). Priority is unaffected: the "critical + fresh deploy" P1 trigger was removed in v0.9.612 (`problem.go:486-501`) and must not return (CLAUDE.md Triage). *Recommend: yes, in P2.5.*

**Azure DevOps**
20. **PAT handling and scopes.** Move the DevOps PAT to `tokenRef` before adding Build (Read)? Is Release (Read) needed? *Recommend: `tokenRef` first; Build (Read) only if pipelines do not post commit statuses (§11 V).*
21. **Second hop** (image tag → build → app commit → PR) when Argo repos are GitOps repos. *Recommend: P4 optional sub-step after §11 V.*
22. **Out-of-band actor.** Kubernetes audit logs are not ingested. *Recommend: out of scope; `out_of_band` shows no actor.*

**Schema operations**
23. **Impact storage.** Compute on read (today's drawer) or snapshot columns on `rollout_events`? *Recommend: on read in P5; add columns only if the feed needs per-row impact.*
24. **Purge class.** All eight in `telemetryPurgeTables` (derived)? Argo sync history beyond Argo's own 10-entry limit is not recoverable after a purge. *Recommend: telemetry class, except consider `argocd_sync_events` for `configPreserveTables`.*
25. **ZooKeeper path** in `0015` hard-coded to `/clickhouse/tables/state/…` (as 0012), while boot resolves it at runtime. *Recommend: keep the 0012 convention and document it.*

**Environment facts the operator supplies (from §11)**
26. The CMO KSM profile and version on each target; whether a second KSM exists.
27. Querier dedup / partial-response defaults and limits (timeout, max samples), hub and targets.
28. Argo CD / GitOps operator version per instance; whether any instance uses apps-in-any-namespace or `destination.name`.
29. Whether each side of the active-active pair has its own Remote Cluster entry and span cluster values.
30. Whether Azure DevOps syncs through the Argo API with a CI account (the initiator is then a CI identity) or only commits to Git (a human syncs, often via SSO). Should `argo_manual` be split into CI vs human with a settings list of CI accounts?

**Stop.** No P1 work starts until decisions 1–10 are approved. P2, P3, P4 and P5 each also need their §11 answers.
