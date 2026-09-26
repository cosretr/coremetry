# Rollout detection v2: Phase 0 audit (baseline + Argo CD hub integration)

Date: 2026-09-26. Status: **draft for operator approval. No code has been written and nothing will be until this is approved.**
Scope: the current rollout-detection baseline, plus the planned Argo CD hub integration (read-only).

**Conventions used in this document**
- Repo facts cite `file:line`.
- Facts from public documentation or upstream source are marked **external (docs)** or **external (source)** with a URL.
- All hostnames, cluster suffixes, instance names and domains are synthetic: `<hub-thanos>`, `<team>-<env>`, `<suffix-a>`/`<suffix-b>`, `cluster-a`, `<apps-domain>`.

> **No earlier "Rollout detection v2" audit or Argo addendum exists in the repository.**
> - `docs/rollout-detection-v2/` did not exist before this file.
> - A grep for "rollout detection v2", "rollout-detection-v2" and "argo addendum" finds nothing. A grep for `argocd|argoproj` over `internal/`, `frontend/src/`, `migrations/`, `cmd/`, `charts/`, `docs/` and `main.go` also finds nothing. The word "Argo" appears only in a Sidebar comment (`frontend/src/components/Sidebar.tsx:96,100`).
> - The only prior design document is the v1 layer audit, `docs/audits/rollouts-audit.md` (approved 2026-08-30, shipped as the 0012 layer).
>
> This file therefore also records the current rollout-detection baseline (§2) that v2 builds on.

---

## 1. Summary and status

### Verification status

| Kind of fact | Status |
|---|---|
| Repo facts (§2, §6-§9) | Verified by reading the code only. Nothing was run. A sample of load-bearing line references was re-grepped during synthesis, and corrections to the area reports are folded in (see the notes below). |
| Argo CD, Prometheus, KSM and OpenShift facts (§3, §5, §6) | Taken from public docs and upstream source at release tags. The `argocd_app_info` / `argocd_app_sync_total` label sets on `release-3.0` and the Prometheus `honor_labels` rule were re-fetched for this document. |
| Anything about the live estate | **Unknown. No live query was run.** No connection was made to ClickHouse, `<hub-thanos>`, any target Thanos, any Argo CD instance or any cluster. |

The live-estate unknowns include:
- the real series count and whether it is deduplicated;
- `namespace` vs `exported_namespace`;
- the Argo CD version per instance;
- how well the naming convention holds;
- how much DeploymentConfig is in use;
- whether KSM `*_created` series are present.

Section §4 is the query pack the operator runs to answer these. Several design choices in §7-§8 are conditional on those answers.

**Line-reference baseline.** References are against the working tree: HEAD `2a085276` (v0.10.945) plus uncommitted in-flight changes from other work, which are not part of this audit.
- `main.go` lines after 1363 sit 3 lines lower than at HEAD.
- `internal/api/api.go` lines after 8593 sit 47 lines higher.
- No other cited line moved.

**Corrections applied to the area reports**
- The `api.go` size ratchet (`.claude/baselines/api_go_lines`, enforced by `internal/api/api_go_size_test.go:22,40`) is **11662 at HEAD**. The uncommitted working tree lowers both `api.go` and the baseline to **11615**. The rule is the same either way: `api.go` must not grow.
- **In-flight, uncommitted PromQL console work (v0.10.947)** already adds pieces relevant to §8:
  - a per-user limiter with an injected clock and Retry-After handling (`internal/api/promql_console_guard.go:219-335`);
  - console settings with `timeoutS`, `maxSeries` (≤ 2000) and `partialResponse=false` (`internal/api/promql_console_settings.go:59-69`);
  - a `promql_console` reload topic (`internal/api/cache.go:538`).

  Its reader (spec C5, `docs/promql-console/`) needs the same worker-grade features as the Argo reader. P1 should share one implementation.
- The reconciler reads previous rows at `internal/rollout/reconciler.go:331`, not :317.
- `NamespaceFilter` is **not** injected by `doQueryWith` (`internal/thanos/client.go:537-587`). Only the cluster label is injected. Every caller must add the namespace matcher itself, and the comment at `client.go:82-85` says otherwise. The v1 audit already flagged this at `docs/audits/rollouts-audit.md:244`.

### Key findings

1. **Rollout rows are span-driven only.**
   - A `workload_rollouts` row is created only from span activity in the MV `workload_revision_activity_1m` (`internal/rollout/reconcile.go:767`, `DetectedBy:"spans"`).
   - KSM data only enriches existing rows (`internal/rollout/ksm.go:114-115`). `detected_by='ksm'` is documented (`internal/chstore/rollout_schema.go:71`) but never written.
   - An uninstrumented workload deployed by Argo has **no row to classify**.
2. **DeploymentConfig is invisible to the rollout layer.**
   - The MV filters out DeploymentConfig pods because `workload=''` (`rollout_schema.go:124-126,141`).
   - The KSM leg is ReplicaSet/Deployment only (`ksm.go:44`).
   - The entity layer excludes DeploymentConfig by design (`internal/entity/normalize.go:15-17`).
3. **Silent 1000-series truncation on every Thanos read.**
   - `maxSeriesParsed = 1000` (`internal/thanos/client.go:523,584-586`); the body is capped at 8 MiB (`:572`).
   - The KSM guard `ksmMaxSeries = 5000` (`ksm.go:53,62-63`) can never fire, so the KSM leg is silently partial on clusters with more than 1000 Deployment-owned ReplicaSets.
   - `argocd_app_info` (~40k series, about 20-30 MB of JSON) **cannot** go through any existing reader.
4. **No Argo, `source`/trigger, actor, git revision, API-server URL, hub or pair concept exists anywhere.**
   - `ClusterConfig` (`client.go:43-89`) has no `api_server_url`, `argo_suffix` or `pair_group`.
5. **Metrics alone cannot classify a sync.**
   - External (source): `argocd_app_sync_total` has no initiator label, and no Argo metric carries a revision or SHA.
   - `argo_auto` vs `argo_manual` and revision-level pair consistency both need the Argo API (`status.operationState.operation.initiatedBy`, `status.sync.revision`, `status.history`).
6. **The `namespace` label on Argo metrics is ambiguous.**
   - With the default `honorLabels: false`, `namespace` is the **scrape target (Argo instance) namespace** and `exported_namespace` is the **Application CR namespace**.
   - With `honorLabels: true` it is the reverse, and `exported_namespace` is absent (§3.2).
   - The operator's hub query Q1.2 decides which case applies.
7. **`kube_replicaset_created` is likely absent on OpenShift platform KSM.**
   - External (source): the CMO denylist is `^kube_.+_created$`.
   - The rollout layer's best time anchor, `ksm_started_at` (`ksm.go:47`, `rollout_schema.go:67`), may therefore always be zero on OpenShift. Q9.3 verifies this.
8. **Existing house decisions constrain the design** (details in §10.3):
   - the `promapi` "new callers use it, do not refactor `internal/thanos`" decision (`internal/promapi/promapi.go:21-26`, `docs/audits/rollouts-audit.md:252`);
   - the full-row-replace writer contract on `workload_rollouts` (`rollout_schema.go:27-33`);
   - the Problem stale sweep (`internal/evaluator/evaluator.go:1117`);
   - the `api.go` ratchet;
   - the "Kaynak" column that already means `detectedBy` (`frontend/src/pages/Rollouts.tsx:64,211`).

---

## 2. Current rollout detection baseline

### 2.1 Data model

**`workload_rollouts`**
- Location: `internal/chstore/rollout_schema.go:52-78`. The prod copy is `migrations/0012_rollout_layer.sql:113-139` (`ReplicatedReplacingMergeTree('/clickhouse/tables/state/workload_rollouts', …)`).

| Aspect | Fact |
|---|---|
| Engine / key | `ReplacingMergeTree(version)`, `ORDER BY (cluster_id, namespace, workload, revision, started_at)` (`:76-77`), TTL 180 days (`:46,78`), no PARTITION |
| Identity | 5-part key: `(cluster_id, namespace, workload, revision, started_at)`. API: `chstore.RolloutID`. Frontend: `encodeRolloutParam` / `rolloutKey` (`frontend/src/lib/rolloutRow.ts:6,153`) |
| `cluster_id` | Remote Cluster `EffectiveID()`, not the span value; the name is resolved at read time |
| `revision` | ReplicaSet name (Deployment) or image tag (StatefulSet/DaemonSet) (`rollout_schema.go:133`) |
| `started_at` | Frozen and deterministic: the start of the first 5-minute bucket in which the revision entered the active set (`:22-24`) |
| `status` | `in_progress`, `completed`, `rolled_back`, `superseded` or `stalled` (`internal/rollout/reconcile.go:112-118`) |
| Evidence and time columns | `first_span_at`, `traffic_confirmed_at`, `ksm_started_at` (= `kube_replicaset_created`, `:67`), `pods_ready_at`, `ksm_not_ready_since`, `completed_at`, `detected_by` (`spans`, `ksm` or `spans+ksm`, `:71`), `span_count`, `note`, `updated_at` |
| **Does not exist** | Any `source`/`trigger`, `actor`, `argo_app`, git SHA, sync revision, `match_method` or `confidence` column |

- "Change kind" (deployment, config, initial or unknown) is computed only in the frontend (`frontend/src/lib/rolloutRow.ts:65-73`).
- **Writer contract** (`rollout_schema.go:27-33`): RMT replaces the whole row, so any writer must carry the other leg's fields from the existing row. Otherwise they reset to 1970.
  - The reconciler reads previous rows with FINAL at `reconciler.go:331` and upserts whole rows at `:395`.
  - **A separate Argo writer into this table would race the reconciler.**

**Other tables**
- **`rollout_reconcile_runs`** (`rollout_schema.go:84-97`): `ORDER BY (started_at, host)`, TTL 30 days. Status is ok, partial, failed or skipped. It is **not** per cluster.
- **`workload_revision_activity_1m`** (MV, `rollout_schema.go:113-144`)
  - `AggregatingMergeTree`, `PARTITION BY toDate(bucket)`, `ORDER BY (cluster, k8s_namespace, workload, revision, service_name, bucket)`.
  - `workload = multiIf(k8s_deployment, k8s_statefulset, k8s_daemonset)`; `WHERE workload != '' AND revision != ''` (`:141`).
  - `cluster` is the **span** value.
  - It is created only when all 8 required columns exist (`internal/chstore/store.go:4498`, MV append `:4580`).
- **`events`** (`store.go:3314-3325`)
  - Columns: `id`, `kind` (deploy, config, incident, maintenance or custom), `label`, `time`, `service`, `link`, `owner`, `created_at`, `version`. `ORDER BY (time, id)`.
  - **No cluster, namespace, workload or revision columns.**
  - The only producer is `POST /api/operator-events` (the Azure DevOps pipeline step, `docs/DEPLOY-EVENTS.md`), with a random id (`internal/chstore/event.go:47,139-141`).

### 2.2 Workload kinds

| Kind | Span path (row creation) | KSM path | Entity layer |
|---|---|---|---|
| Deployment | yes (revision = ReplicaSet name) | yes (`ksm.go:44-47`) | yes |
| StatefulSet / DaemonSet | yes (revision = image tag; fixed or `latest` tags are invisible, `rollout_schema.go:130-132`) | **no** | yes |
| **DeploymentConfig** | **no** (no DC or RC attribute in spans, so `workload=''`) | **no** | only as a ReplicationController (unknown owner kind passes through, `normalize.go:120-123`); DC is excluded (`:15-17`) |

- The only DeploymentConfig awareness in the codebase is the pod-churn helper `podTemplateHash` (`internal/chstore/deploys.go:697-703`). Details in §6.

### 2.3 Detection pipeline, cadence and leader

**Pipeline** (`reconciler.go:300-400`)
1. MV activity over the lookback window (row cap 500k, `chstore/rollouts.go:37`).
2. First-seen horizon (`:132`).
3. Previous rows from the last 30 days, FINAL, hard cap 300k (`:169`).
4. Span cluster value → registry ID. Unmapped values are dropped and the run is marked partial.
5. KSM fetch per cluster.
6. The pure `Reconcile` function (`reconcile.go:198`).
7. Leadership re-checked before writing (`reconciler.go:391`).
8. One whole-row upsert (`:395`).

**KSM leg** (`ksm.go:42-49`)
- Instant queries on `kube_replicaset_owner{owner_kind="Deployment"}`, `_spec_replicas`, `_status_ready_replicas` and `_created`.
- **No namespace matcher.** The entity layer's `nsMatcher` is used by `internal/entity/samples.go:21-30`, but not here.
- The cluster label is injected (`client.go:541-546`).
- Wiring: `main.go:2218-2240` via `InstantSamples`, so the result is capped at 1000 series.

**State machine**
- Entry needs `Hysteresis` consecutive active buckets (at least 10 spans per bucket).
- `completed` needs about 30 minutes of old-revision silence.
- `stalled` comes only from KSM.
- A row appears roughly 10-15 minutes after new-revision traffic starts.

**Defaults** (`internal/rollout/settings.go:57`)
- interval 60s, bucket 5m, threshold 10, hysteresis 2, exit hysteresis 6, lookback 6h, stalled 10m.
- **Disabled by default.**

**Leader election**
- Worker mode only (`if mode.worker {`, `main.go:1078`).
- Own lock `"rollout-reconciler"` with `LeaderTTL(time.Minute)` = 3-minute lease (`main.go:1181`; clamp rule at `internal/cache/leader.go:84-96`).
- `SetOnAcquire(Tick)` (`main.go:1183`); an `inFlight` compare-and-swap (`reconciler.go:234`) stops overlapping ticks.
- Without Redis the lock is "always leader". With Redis unreachable, every pod is leader (`lockDegraded`, `main.go:591-592`).

**Settings and SSE**
- Settings live in `system_settings["rollouts"]`, refreshed through the shared `cfgRefresh` every 30s (`main.go:520,1076`).
- API pods run an SSE tail every 15s, as an invalidation only.

### 2.4 Service ↔ workload linkage today

1. **MV `service_name` dimension** (authoritative, per cluster, namespace and workload)
   - `RolloutServices` / `RolloutServicesBatch` (`internal/chstore/rollout_services.go:22-112`).
   - `RolloutRefsForService` (`internal/chstore/rollout_problem_telemetry.go:54`, max 50 rows).
2. **Entity graph:** svc ← runs ← pod → parent → wl (`internal/entity/identity.go:13-60`, `normalize.go:98-124`, `derived_workload.go:23-35`).
   - Workload IDs look like `wl:<cid>/<ns>/<Kind>/<name>`.
   - Exposed as `chain` in `GET /api/services/{name}/pods` (`internal/api/entities.go:47`).
3. **`service_metadata.namespace` / `.deployment`:** one value per service, not per cluster (`store.go:3787-3799`).
4. **`list_deployments` / `/api/changes`:** use the service name as the workload name (`internal/mcptools/list_deployments.go:77,114-116`).

**Does not exist:** a Service→Argo Application mapping, and any Service→Workload table carrying `match_method` or `confidence`.

### 2.5 Rollouts page and API

**API**
- `GET /api/rollouts?from&to&cluster&namespace&workload&status&kind&limit` (`internal/api/rollouts.go:7,36`).
- Uses FINAL, `started_at DESC`, cache TTL 15s. Equality ANDs are built in `rolloutWhere` (`chstore/rollouts.go:317`).
- `TestRolloutKeysCarryEveryInput` (`internal/api/rollouts_test.go:54`) pins that the cache key carries every input.
- With the flag off, every route returns 404 `{disabled:true}` (`rollouts.go:52-59`).

**Page URL state** (`frontend/src/pages/Rollouts.tsx`)
- Params: `?tab`, `?status`, `?cluster`, `?namespace`, `?range`, `?rollout`.
- The Cluster select comes from `useEntityClusters`, so it is coupled to the entity-layer flag (`:97`).
- The API accepts `workload` and `kind`, but **the UI does not expose them**.

**Columns and name collisions**
- The page already has a **"Kaynak" (= source) column that shows `detectedBy`** (`Rollouts.tsx:64,211`).
- `ChangeRow.Source` (inferred | event | rollout, `list_deployments.go:41`) and `RecentDeployEntry.Source` (`deploys.go:46-50`) also exist.
- **A new "source" filter would collide with all three.**

**Service page**
- "Runtime & rollouts" (`frontend/src/pages/Service.tsx:643-655`) shows **pod-churn** (`GET /api/services/{name}/rollouts`, `internal/api/api.go:762`). It does not show `workload_rollouts`.
- Only `Rollouts.tsx` and `RolloutDrawer` consume the rollout layer.

### 2.6 Problem correlation

**Flow** (`internal/anomaly/rollout_causes.go:150-200`)
- Window: [onset − 120 min, onset + 5 min] (`:38-39`).
- MV refs for the service → EffectiveID → `RolloutsForWorkloads` → `rollout.Rank`.

**Scoring** (`internal/rollout/score.go:8-18,63-66`)
- 0-30 min → 0.90; 30-120 min → 0.50.
- `stalled` or `rolled_back` ×1.10; `superseded` ×0.70.
- +0.05 for `spans+ksm`; +0.05 for a pod match. Cap 0.98.

**Evidence shape**
- `chstore.RolloutEvidence` (`internal/chstore/rootcause_hypothesis.go:70-86`) has `DetectedBy` (`:80`) but no trigger or source field.
- It is filled in `rollout_causes.go:128-133`.

**Plug-in points for v2:** a trigger-based multiplier in `Score`, and a `Trigger` field on `RolloutEvidence`.

---

## 3. Argo CD metrics model

### 3.1 Label sets

External (source):
- https://raw.githubusercontent.com/argoproj/argo-cd/release-3.0/controller/metrics/metrics.go (re-fetched for this document)
- Other tags: https://github.com/argoproj/argo-cd/tree/master/controller/metrics
- Docs: https://argo-cd.readthedocs.io/en/stable/operator-manual/metrics/

**`argocd_app_info`** is a gauge with value 1. The labels below are as emitted by the controller, before scraping.

| Label | Meaning | Version notes |
|---|---|---|
| `namespace` | **Application CR namespace** (not the destination) | all |
| `name`, `project` | Application name and AppProject | all |
| `repo` | normalised URL of the first source (`sources[0]` for multi-source apps) | all |
| `dest_server` | destination API URL | v2.x: raw `spec.destination.server` (`""` if the app uses `destination.name`). v3.0+: resolved through `argo.GetDestinationCluster` → cluster `.Server`, `""` if lookup fails |
| `dest_namespace` | `spec.destination.namespace` (may be `""`) | all |
| `sync_status` | Synced, OutOfSync or Unknown | all |
| `health_status` | Healthy, Progressing, Degraded, Suspended, Missing or Unknown | all |
| `operation` | `"delete"` if a deletion timestamp is set, `"sync"` while `app.operation.sync` is set (in flight), otherwise `""`. Transient | all |
| `autosync_enabled` | `true` or `false` | from v2.9. From v3.1 it respects `automated.enabled` |
| `hydrator_status`, `phase` | `phase` = last `operationState.phase` (it persists after the operation ends) | master / v3.6.0-rc1 only; not in any GA release as of 2026-09-26 |

**`argocd_app_sync_total`** (counter)
- Labels: `namespace`, `name`, `project`, `dest_server`, `phase`, plus `dry_run` from v3.1.
- It is incremented only when an operation **completes** (Succeeded, Failed or Error).
- It has **no initiator or automated label** and no `dest_namespace`.
- A series appears only on the first completed sync after the controller starts.
- It resets on controller restart and on `--metrics-cache-expiration`.

**Other useful metrics**
- `argocd_cluster_info{server, k8s_version, name}`: `name` exists from v3.0 only. It is the low-cardinality source for `dest_server` → cluster suggestions and for instance discovery.
- `argocd_info{version}`: server metrics from v2.13.
- `argocd_app_labels` (needs `--metrics-application-labels`) and `argocd_app_condition` (needs `--metrics-application-conditions`) are opt-in and separate.
- `argocd_app_reconcile` has no app name, so it is useless per app.

**Does not exist in metrics:** revision or SHA, sync time, initiator, and a managed-resource list.

### 3.2 `namespace` vs `exported_namespace`: exact conditions

**The rule.** External (docs), https://prometheus.io/docs/prometheus/latest/configuration/configuration/:
- With `honor_labels: false` (the default), a label in the scraped data that conflicts with a server-side target label is **renamed to `exported_<label>`**, and the target label is attached.
- With `true`, the scraped value is kept and the target label is ignored.
- The rename applies on a name conflict whatever the values are. This is inferred from the docs wording "label conflicts" and the scrape implementation.

**Who sets `namespace` as a target label.** Prometheus Operator always sets it from `__meta_kubernetes_namespace` on ServiceMonitor targets. External (source): prometheus-operator `pkg/prometheus/promcfg.go`, per area report, not re-fetched.

| Scrape path of the Argo controller metrics | `honorLabels` | `namespace` after scrape | `exported_namespace` after scrape |
|---|---|---|---|
| ServiceMonitor, platform or default Prometheus (argocd-operator / OpenShift GitOps SMs do not set `honorLabels`) | false | **scrape target namespace = Argo instance namespace** (where `<argocd>-metrics` lives) | **Application CR namespace**; present even when equal |
| OpenShift user-workload monitoring (CMO forces `overrideHonorLabels: true` and `enforcedNamespaceLabel: namespace`) | forced false | ServiceMonitor namespace (= instance namespace) | Application CR namespace |
| Any scrape with `honorLabels: true` | true | **Application CR namespace** | absent |
| Federation or re-scrape of already-renamed series | false twice | federating target's namespace | may become `exported_exported_namespace` (inferred, unverified) |

- Without apps-in-any-namespace, the Application CR namespace equals the instance namespace. So in the first two rows the two labels carry the **same value**.
- Instances in `openshift-*` namespaces are likely scraped by platform monitoring; others may be scraped by user-workload monitoring. This depends on the GitOps operator version and was not verified on the hub.
- The repo already uses `exported_namespace` for HAProxy series (`internal/thanos/promql.go:474-494`).

**Derivation rules for Coremetry**
- `app_ns = exported_namespace if non-empty else namespace`.
- `instance_ns = namespace` **only when `exported_namespace` is present**.
- When it is absent (`honorLabels: true`) and an instance uses apps-in-any-namespace, the instance cannot be derived from `namespace`. Use `job`/`service` (expected `<argocd-name>-metrics`) mapped through settings.
- App key = `(instance, app_ns, name)`. **`name` is never a key on its own.**
- Q1.2 decides which row applies.

### 3.3 `dest_server` / `dest_namespace` semantics

- **`dest_server` is the string as registered in the Argo cluster Secret.** Port, trailing slash and case vary, so Coremetry must normalise it (§7.1).
- `https://kubernetes.default.svc` means **the hub itself**, and is valid only for the hub's own instances. External (docs): https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/
- `dest_server=""` means `destination.name` on v2.x, or a failed lookup on v3.x. Q2.2 counts these.
- **Many Applications share `(dest_server, dest_namespace)`**, one per component, so a namespace-level join returns N candidates (§7.4).
- `argocd_app_sync_total` has `dest_server` but no `dest_namespace`. Join it to `argocd_app_info` on `(namespace, exported_namespace, name)`.

### 3.4 Cardinality and safe query rules (~40k series)

**Where the series count comes from**
- One series per (app × exporting controller shard pod × Prometheus replica, if not deduplicated).
- Every sync, health or operation change ends one series and starts another (churn).
- With controller sharding the `pod` label moves between replicas, so aggregate away `pod` and `instance`.
- **Is ~40k the app count or the series count?** Open question. Q0.1 vs Q0.4.

**Size**
- About 0.5-0.8 KB of JSON per raw series, so 20-30 MB for 40k.
- A `group by` of the needed labels is about 12 MB.
- **Both exceed the 8 MiB body cap and the 1000-series cap.** This is an estimate, not a measurement.
- **Aggregation alone cannot fix it:** per-app state is inherently one row per app.

**Safe rules (humans in Grafana and the future worker alike)**
1. **Instant queries only** for inventory. A range query multiplies the cost by the number of steps.
2. Run `count(...)` before listing anything. List only when the count is ≤ 50, or under `topk(50, …)`.
3. Collapse duplicates first with `group by (namespace, exported_namespace, name, …)`.
4. Never use the label browser on `name`, `repo` or `dest_server`. Always pass `start`/`end` to label and name APIs.
5. `[24h]` range vectors only on `argocd_app_sync_total`, and after a `[1h]` trial.
6. **Worker:**
   - shard by instance namespace, and additionally by `dest_server` if a shard exceeds the cap (sized from Q1.6);
   - fetch only the non-steady set every tick;
   - run a full sharded inventory every 10-15 minutes;
   - send `partial_response=false` or skip the diff on any warning;
   - use POST form encoding for long regex matchers.

---

## 4. Operator query pack (Phase 0 facts to collect)

**How to run**
- Grafana Explore against `<hub-thanos>` for §4.1-§4.8, and against each target Thanos for §4.9. §4.10 is an optional Argo API check.
- Query type **Instant**, Format **Table**, time = now. Use backtick raw strings for regexes.
- If the hub Remote Cluster entry has `ThanosLabelName` set, run the hub queries **without** the matcher first, then with `{<L>="<V>"}`. If the two counts differ, label injection would hide Argo series from Coremetry (`client.go:541-546`).

**Tokenise before pasting** (this keeps real names out of this repo and chat; it is not a product feature)
- Paste counts, label **names**, distributions and partial-response warning text freely.
- Tokenise label **values** consistently and keep the mapping private:
  - `dest_server` → `https://api.cluster-a.<domain>:6443`
  - instance namespace → `<team>-<env>`
  - suffix → `<suffix-a>`
  - project → `<project-1>`
- Keep these literally: `https://kubernetes.default.svc`, status values, `true`/`false`, and port numbers.

**Settings-derived placeholders**
- `<env-list>` is the env alternation, for example `dev|test|prod`.
- `<suffix-list>` is the union of the clusters' planned `argo_suffix` values, for example `<suffix-a>|<suffix-b>`.
- `RE` = `` `[^-]+-[^-]+-.+-(<env-list>)-(<suffix-list>)` `` (PromQL `=~` is fully anchored, see the repo note at `internal/thanos/promql.go:54`).

### 4.1 Q0: preflight (dedup, source cluster, identity collapse)

```promql
# Q0.1 total series. Expect a scalar ≈ 40000. Paste: number.
count(argocd_app_info)
# Q0.2 HA dedup. Expect 1 row with no prometheus_replica. Paste: row count, whether prometheus_replica appears (if it does: enable dedup and rerun Q0.1).
count by (prometheus, prometheus_replica) (argocd_app_info)
# Q0.3 which external cluster labels exist on Argo series. Expect 1 row (the hub). Paste: which label NAMES have values, row count.
count by (cluster, cluster_id, cluster_name, k8s_cluster, openshift_cluster, prometheus, tenant, tenant_id) (argocd_app_info)
# Q0.4 unique apps. Expect = Q0.1. If lower, duplicate series per app (shards/replicas) -> every later query must use the group-by-first form. Paste: number.
count(group by (namespace, exported_namespace, name) (argocd_app_info))
# Q0.5 which clusters this querier serves (same candidate list as internal/thanos/cluster_detect.go:36,61). Paste: row count; whether the Q0.3 label matches.
count by (cluster, cluster_id, cluster_name, k8s_cluster, openshift_cluster, prometheus, tenant, tenant_id) (kube_node_info)
```

### 4.2 Q1: Argo instances and the namespace collision

```promql
# Q1.1 instances (namespace + metrics job). Expect one row per instance; job ≈ <instance>-metrics (unverified). Paste: rows, apps per row (tokenised).
count by (namespace, job) (argocd_app_info)
# Q1.2 THE COLLISION CASE. Paste: which case + number of rows where values differ.
#   (A) exported_namespace present and == namespace  -> standard; instance = namespace, app key (namespace, name)
#   (B) exported_namespace absent                     -> honorLabels true; namespace = app ns; instance from job
#   (C) exported_namespace present and != namespace   -> apps-in-any-namespace; app ns = exported_namespace
count by (namespace, exported_namespace, job) (argocd_app_info)
# Q1.3 collisions (empty = none). Paste: row counts.
count by (exported_namespace) (group by (namespace, exported_namespace) (argocd_app_info)) > 1
count by (job) (group by (namespace, job) (argocd_app_info)) > 1
# Q1.4 controller shards per instance. Paste: pods per instance.
count by (namespace, pod) (argocd_app_info)
# Q1.5 instances incl. zero-app ones (auto-discovery source). Paste: row count vs Q1.1.
count by (namespace, job) (argocd_cluster_info)
# Q1.6 worker shard sizing against caps. Paste: the three numbers.
max(count by (namespace) (argocd_app_info))
max(count by (namespace, dest_server) (argocd_app_info))
count(count by (namespace, dest_server) (argocd_app_info) > 1000)
```

- **Q1.7 (optional, api_url discovery)** applies only if Q0.5 shows the hub's own platform metrics.
- First check the label names with `/api/v1/labels?match[]=openshift_route_info&start=…&end=…`.
- Then run `count by (namespace) (group by (namespace, route) (openshift_route_info{namespace=~"<argo-ns-regex>"}))`.
- **Never paste hosts.**

### 4.3 Q2: target clusters (`dest_server`, `dest_namespace`)

```promql
# Q2.1 apps per target. Expect tens of rows. Paste tokenised (cluster-a…).
count by (dest_server) (argocd_app_info)
# Q2.2 counts only. Paste: three numbers.
count(group by (dest_server) (argocd_app_info))
count(argocd_app_info{dest_server=""})                                   # destination.name (v2.x) or failed lookup (v3.x)
count(argocd_app_info{dest_server="https://kubernetes.default.svc"})     # apps on the hub itself
# Q2.3 URL shape (drives normalisation). Paste: port distribution + count.
count by (port) (label_replace(group by (dest_server) (argocd_app_info), "port", "$1", "dest_server", `https?://[^/]+:([0-9]+)/?`))
count(group by (dest_server) (argocd_app_info{dest_server=~`https://api\.[^./]+\.[^/]+:6443/?`}))
# Q2.4 cluster token inside the URL (feeds api_server_url matching). Paste tokenised.
count by (api_cluster) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "api_cluster", "$1", "dest_server", `https://api\.([^./]+)\..*`))
# Q2.5 instances per target (distribution).
count_values("instances_per_target", count by (dest_server) (group by (namespace, dest_server) (argocd_app_info)))
# Q2.6 how weak a namespace-only mapping is. Paste: counts + distribution.
count(group by (dest_server, dest_namespace) (argocd_app_info))
count_values("apps_per_target_ns", count by (dest_server, dest_namespace) (group by (namespace, exported_namespace, name, dest_server, dest_namespace) (argocd_app_info)))
count(argocd_app_info{dest_namespace=""})
# Q2.7 Argo cluster registry vs use. Paste: two numbers.
count(group by (namespace, server) (argocd_cluster_info))
count(group by (server) (argocd_cluster_info) unless on (server) label_replace(group by (dest_server) (argocd_app_info), "server", "$1", "dest_server", "(.*)"))
```

### 4.4 Q3: duplicates and active-active pairs

```promql
# Q3.2 same name on the same server (true duplicates). Paste: count only.
count(count by (name, dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info)) > 1)
# Q3.3 distribution
count_values("same_name_same_server", count by (name, dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info)))
# Q3.4 same name twice within one instance (apps-in-any-namespace)
count(count by (namespace, name) (group by (namespace, exported_namespace, name) (argocd_app_info)) > 1)
# Q3.5 same name in two instances
count(count by (name) (group by (namespace, name) (argocd_app_info)) > 1)
# Q3.6a clusters per base name (base = name minus -<suffix>). Expect mostly 1, pairs at 2.
count_values("clusters_per_base", count by (base) (group by (base, dest_server) (label_replace(argocd_app_info{name=~`.+-(<env-list>)-(<suffix-list>)`}, "base", "$1", "name", `(.+)-(<suffix-list>)`))))
# Q3.6b bases present on both sides of one pair
count(group by (base) (label_replace(argocd_app_info{name=~`.+-<suffix-a>`}, "base", "$1", "name", `(.+)-<suffix-a>`)) and on (base) group by (base) (label_replace(argocd_app_info{name=~`.+-<suffix-b>`}, "base", "$1", "name", `(.+)-<suffix-b>`)))
# Q3.6c status mismatch inside a pair right now (revision mismatch is NOT checkable from metrics)
count(count by (base) (group by (base, sync_status, health_status) (label_replace(argocd_app_info{name=~`.+-(<suffix-a>|<suffix-b>)`}, "base", "$1", "name", `(.+)-(<suffix-a>|<suffix-b>)`))) > 1)
```

### 4.5 Q4 / Q8: status and autosync share

```promql
# Q4.1 at most 3x6 rows. Paste verbatim.
count by (sync_status, health_status) (group by (namespace, exported_namespace, name, sync_status, health_status) (argocd_app_info))
# Q4.2 per instance
count by (namespace, sync_status) (argocd_app_info)
# Q4.3 in-flight operations; the phase line returns data only on master / 3.6-rc builds
count by (operation) (argocd_app_info)
count by (phase) (argocd_app_info{phase!=""})
# Q8 autosync share. One row with no label -> Argo <= 2.8.
count by (autosync_enabled) (group by (namespace, exported_namespace, name, autosync_enabled) (argocd_app_info))
count by (namespace, autosync_enabled) (argocd_app_info)
```

### 4.6 Q5: label sets and Argo version inference (cheap metadata first)

- **Q5.1 metric names:** `GET <hub-thanos>/api/v1/label/__name__/values?match[]={__name__=~"argocd_.+"}&start=<now-600>&end=<now>`. Always send `start` and `end`.
- **Q5.2 label names:** `GET /api/v1/labels?match[]=argocd_app_info&start=…&end=…`, and the same for `argocd_app_sync_total`, `argocd_cluster_info` and `argocd_app_labels`. **Paste the label names verbatim.**
- **Q5.3 PromQL fallback:** `count by (__name__) ({__name__=~"argocd_app_(info|sync_total|labels|condition)|argocd_cluster_info"})`. Avoid `argocd_app_.*`, which touches `argocd_app_k8s_request_total` and the reconcile buckets.
- **Q5.4 presence probes** (an empty result means the label is absent):
  - `count(argocd_app_info{autosync_enabled!=""})`
  - `count(argocd_app_info{exported_namespace!=""})`
  - `count(argocd_app_info{phase!=""})`
  - `count(argocd_app_sync_total{dry_run!=""})`
  - `count(argocd_app_sync_total{exported_namespace!=""})`
  - `count(argocd_app_labels)`
  - `count by (namespace, version) (argocd_info)` (v2.13+)
- **Version inference:**
  - no `autosync_enabled` → Argo 2.8 or older;
  - `dry_run` present → 3.1 or newer;
  - `phase` present → 3.6 prerelease or master.
- **Q5.5 distinct counts without values:** `count(group by (<L>) (argocd_app_info))` for `<L>` in `name, project, repo, dest_server, dest_namespace, namespace, job, pod`.
- **Q5.6 one full label set:** `topk(1, argocd_app_info)`. **Paste the label names only.**

### 4.7 Q6: naming-convention validation (settings-driven lists)

The convention is `<prefix>-<team>-<component>-<env>-<clusterSuffix>`. It is observed, not guaranteed. `<env-list>` comes from the planned Argo settings and `<suffix-list>` from the union of cluster `argo_suffix` values (§7).

```promql
# Q6.1 overall match ratio (target: report it, no threshold assumed)
count(group by (namespace, exported_namespace, name) (argocd_app_info{name=~`RE`})) / count(group by (namespace, exported_namespace, name) (argocd_app_info))
# Q6.2 per instance; the "or … * 0" branch keeps zero-match instances visible (an empty group vanishes instead of reading 0)
(count by (namespace) (argocd_app_info{name=~`RE`}) or count by (namespace) (argocd_app_info) * 0) / count by (namespace) (argocd_app_info)
# Q6.3 near-miss buckets (counts only)
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~`RE`, name=~`.+-(<env-list>)-[^-]+`}))   # known env, unknown suffix
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~`RE`, name=~`.+-(<suffix-list>)`}))    # unknown env, known suffix
count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~`RE`, name=~`[^-]+(-[^-]+){0,3}`}))     # four tokens or fewer
# Q6.4 last-token values missing from the lists (bounded). Paste tokenised.
topk(20, count by (sfx) (label_replace(group by (namespace, exported_namespace, name) (argocd_app_info{name!~`RE`}), "sfx", "$1", "name", `.*-([^-]+)`)))
# Q6.5 auto-derive argo_suffix per cluster: dominant last token per dest_server and its share
topk by (dest_server) (1, count by (dest_server, sfx) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "sfx", "$1", "name", `.*-([^-]+)`)))
max by (dest_server) (count by (dest_server, sfx) (label_replace(group by (namespace, exported_namespace, name, dest_server) (argocd_app_info), "sfx", "$1", "name", `.*-([^-]+)`))) / count by (dest_server) (group by (namespace, exported_namespace, name, dest_server) (argocd_app_info))
#   suffix seen on more than one server (breaks suffix -> cluster uniqueness)
count by (sfx) (group by (sfx, dest_server) (label_replace(argocd_app_info{name=~`.+-(<suffix-list>)`}, "sfx", "$1", "name", `.+-(<suffix-list>)`))) > 1
# Q6.6 env purity per instance (> 1 row = an instance serves several envs)
count by (namespace) (group by (namespace, env) (label_replace(argocd_app_info{name=~`RE`}, "env", "$1", "name", `.+-(<env-list>)-[^-]+`))) > 1
```

**Q6.7 listing non-matching names safely (operator-only; do not paste the names)**
1. Count: `count(group by (namespace, exported_namespace, name) (argocd_app_info{name!~`RE`}))`.
2. Aggregate: `count by (namespace, project) (group by (namespace, project, name) (argocd_app_info{name!~`RE`}))`. Check the row count first.
3. Drill into one project: `topk(50, group by (namespace, project, name, dest_server) (argocd_app_info{name!~`RE`, project="<project-1>"}))`.

**Paste back:**
- the Q6.1, Q6.2 and Q6.3 numbers;
- the Q6.4 tokens, tokenised;
- `count(<Q6.5 share> < 0.95)`;
- whether team names ever contain hyphens. If they do, the team/component split can only ever be "estimated".

### 4.8 Q7 / Q10: sync activity and churn (sizes the "state changes only" worker)

```promql
# Q7.0 inventory
count(argocd_app_sync_total)
count by (phase) (argocd_app_sync_total)
# Q7.1 run with [1h] first. Undercounts: a series born by its first sync counts 0 in increase().
sum by (phase) (increase(argocd_app_sync_total[24h]))
# Q7.2 complement: identities new in 24h (each = at least one completed sync)
count by (phase) (group by (namespace, exported_namespace, name, phase) (argocd_app_sync_total) unless on (namespace, exported_namespace, name, phase) group by (namespace, exported_namespace, name, phase) (argocd_app_sync_total offset 24h))
# Q7.3 cross-checks
count(resets(argocd_app_sync_total[24h]) > 0)
# Q7.5 syncs split by autosync (key input for argo_auto vs argo_manual priors)
sum by (autosync_enabled) (sum by (namespace, exported_namespace, name) (increase(argocd_app_sync_total[24h])) * on (namespace, exported_namespace, name) group_left (autosync_enabled) group by (namespace, exported_namespace, name, autosync_enabled) (argocd_app_info))
# Q10 state transitions per hour / per 2 min (lower bound; flaps missed). Paste at 2–3 times of day.
count(group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info) unless on (namespace, exported_namespace, name, sync_status, health_status, operation) group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info offset 1h))
count(group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info) unless on (namespace, exported_namespace, name, sync_status, health_status, operation) group by (namespace, exported_namespace, name, sync_status, health_status, operation) (argocd_app_info offset 2m))
```

**Cost of Q7**
- `[24h]` over 20k series at a 60s scrape reads about 29M samples, or about 58M at 30s. That can hit the querier's max-samples or timeout limits (not verified).
- Split with `{namespace="<argo-ns-1>"}` or use 4×`[6h]` if needed.

### 4.9 Q9: target clusters (each target Thanos, with `{<L>="<V>"}`)

```promql
# Q9.1 DeploymentConfig presence (openshift-state-metrics)
count(openshift_deploymentconfig_spec_replicas{<L>="<V>"})
count(openshift_deploymentconfig_spec_replicas{<L>="<V>"} > 0)
count(count by (namespace) (openshift_deploymentconfig_spec_replicas{<L>="<V>"}))
# Q9.2 DC via KSM (fallback; kube_replicationcontroller_owner is EXPERIMENTAL and absent in the CMO minimal profile)
count by (owner_kind) (kube_pod_owner{<L>="<V>"})
count by (owner_kind) (kube_replicationcontroller_owner{<L>="<V>"})
# Q9.3 KSM coverage for the exact series the rollout layer reads (internal/rollout/ksm.go:44-47)
count(kube_replicaset_owner{<L>="<V>",owner_kind="Deployment"})   # > 1000 => KSM join silently truncated TODAY
count(kube_replicaset_spec_replicas{<L>="<V>"})
count(kube_replicaset_status_ready_replicas{<L>="<V>"})
count(kube_replicaset_created{<L>="<V>"})                          # expected ABSENT on CMO KSM (_created denylist)
count(kube_deployment_metadata_generation{<L>="<V>"})
count(kube_deployment_status_observed_generation{<L>="<V>"})
# Q9.4 resource-exact via KSM allowlist (only if the KSM on targets exposes Argo tracking)
count(kube_deployment_annotations{<L>="<V>"})
count(kube_deployment_labels{<L>="<V>", label_app_kubernetes_io_instance!=""})
```

- **Q9.5 (profile hint):** use `GET /api/v1/label/__name__/values?match[]={__name__=~"kube_(replicaset|deployment|replicationcontroller)_.+"}&start=…&end=…`.
- If `kube_deployment_spec_replicas` is present while `kube_replicaset_spec_replicas` and `kube_deployment_status_condition` are absent, the target probably uses the CMO minimal collection profile.

### 4.10 Q11: Argo API reachability (operator-run, optional, read-only)

Use the read-only token from §5. **Never pass `refresh`.**

1. `curl -sS -H "Authorization: Bearer $TOKEN" https://<instance-server-route>/api/version`
   - Paste: the version only.
2. `GET /api/v1/applications/<one-app>?appNamespace=<app-ns>&project=<project-1>`
   - Paste the **presence** (yes/no) of these key paths only, not their values:
     - `status.operationState.operation.initiatedBy.automated`
     - `.username`
     - `status.history[].initiatedBy`
     - `status.sync.revision`
     - `status.resources[].kind`, with counts per kind
3. State whether `username` values look like local accounts or SSO emails. Do not paste them.

### 4.11 Paste-back template

| Item | Value |
|---|---|
| Q0.1 / Q0.4 / dedup (Q0.2) | |
| Q0.3 cluster label names on Argo series; matches hub `<L>`? | |
| Q1.2 collision case (A/B/C), differing rows | |
| Instances (Q1.1/Q1.5), max apps per instance (Q1.6) | |
| `dest_server` count, `""` count, in-cluster count, port forms | |
| Apps per (dest_server, dest_namespace) distribution | |
| Q3 duplicates / pairs / pair mismatches | |
| Argo version(s) (Q5.4/Q11) | |
| Q6 ratio overall/per instance, near-miss counts, suffix purity | |
| Q7 syncs/24h by phase, by autosync; Q10 transitions/h and /2m | |
| Q9 per target: DC counts, `kube_replicaset_owner` count, `_created` present? | |
| Querier limits: timeout, max samples; `argocd-metrics` scrape interval | |

---

## 5. Argo CD API (read-only)

External sources for this section:
- swagger: https://github.com/argoproj/argo-cd/blob/master/assets/swagger.json
- API docs: https://argo-cd.readthedocs.io/en/stable/developer-guide/api-docs/
- server source: https://github.com/argoproj/argo-cd/blob/master/server/application/application.go

These were not re-fetched here unless stated.

### 5.1 Endpoint pattern and endpoints

- **Per instance:** `https://<instance-server-route>[/<rootpath>]/api/v1/...`.
  - On OpenShift GitOps the server Route is `<argocd-name>-server`, and `ArgoCD.status.host` holds the host.
  - The default route host pattern `<route>-<ns>.<apps-domain>` is unverified.
- Each instance has its own signing key, accounts, RBAC and possibly its own version, so Coremetry needs **N credentials**.

| Need | Endpoint | Notes |
|---|---|---|
| App state, operation, history, managed resources | `GET /api/v1/applications/{name}?appNamespace=<ns>&project=<p>` | Direct kube-apiserver GET on the hub. `appNamespace` is needed only when the app is outside the instance's control-plane namespace (apps-in-any-namespace). With `project` set, a missing app returns 404 rather than 403 |
| Bulk inventory (low frequency) | `GET /api/v1/applications?fields=items.metadata.name,items.metadata.namespace,items.spec,items.status.sync.status,items.status.resources,items.status.operationState.phase,...` | **No pagination.** The `fields` projection is undocumented (`pkg/apiclient/application/forwarder_overwrite.go`); its allow-list differs by version. `status.history` and `operationState.operation.initiatedBy` are **not** projectable |
| Resource tree (DC → RC → Pod, ReplicaSet `Rev:N`) | `GET /api/v1/applications/{name}/resource-tree` | Read from the Redis cache. **Caveat:** on a cache miss the server internally calls Get with `Refresh=normal`, an implicit annotation write (master, external (source)). Use on demand only; see Q-16 |
| Diff payloads | `GET .../managed-resources` | Heavy (full live/target JSON). **Avoid** |
| Version, clusters | `GET /api/version`, `GET /api/v1/clusters` | `clusters get` RBAC is optional |
| Watch | `GET /api/v1/stream/applications` (SSE, same filters and `fields`) | An option for later; not in the first phases |

**Never send `refresh=normal|hard`** on Get or List. It patches the refresh annotation on the Application, needs only `get` RBAC, and would violate "no write to Argo CD".

**Fields that matter** (`types.go`, external (source))
- `status.operationState.{phase, startedAt, finishedAt, operation.initiatedBy.{username, automated}, operation.sync.revision, syncResult.revision(s)}`.
  - The controller sets `automated=true` for auto-sync and self-heal.
  - API and UI syncs set `username`: a local account name, or the SSO `email` claim. Coremetry stores it verbatim; see §8.5.
- `status.history[]`: `{id, revision(s), deployedAt, deployStartedAt, initiatedBy}`.
  - `initiatedBy` is present from v2.11.
  - An entry is appended only for a **successful, non-dry-run, full** sync. The default `revisionHistoryLimit` is 10.
- `status.sync.{status, revision(s)}` and `status.health`.
- `status.resources[]`: `{group, kind, namespace, name, status, health}`. This is the source for resource-exact mapping (§7.4).
  - From v3.0, per-resource health is not persisted by default (`resourceHealthSource: appTree`). Names are still present, and names are all the mapping needs.

### 5.2 Auth model: one local read-only account and token per instance

- **Account:** add `accounts.coremetry-ro: apiKey` in `argocd-cm`. On OpenShift GitOps the operator reconciles `argocd-cm`, so use `ArgoCD.spec.extraConfig` or `spec.localUsers`.
  - With `localUsers`: `tokenLifetime` defaults to `"0h"` (infinite) and `autoRenewToken` to true. The token is stored in Secret `<user>-local-user`, key `apiToken`, next to `expAt`. This is from GitOps 1.18 docs, per area report.
- **Token:** `argocd account generate-token --account coremetry-ro --expires-in <d> --id coremetry-<date>`, issued by an admin.
  - It is an HS256 JWT signed with that instance's `server.secretkey`.
  - **Default expiry is none.**
  - It becomes invalid if the account is disabled, the `apiKey` capability is removed, the token id is revoked, or `exp` passes.
- **RBAC** (`argocd-rbac-cm` `policy.csv`, or `ArgoCD.spec.rbac.policy`):

```csv
p, role:coremetry-ro, applications, get, */*, allow
p, role:coremetry-ro, projects, get, *, allow        # optional
p, role:coremetry-ro, clusters, get, *, allow        # optional (dest_server <-> Argo cluster name)
g, coremetry-ro, role:coremetry-ro
```

- `applications, get` covers Get, List, resource-tree, managed-resources and events. It does not cover logs.
- The glob is not `/`-aware, so `*/*` also matches `<proj>/<ns>/<app>`.
- The built-in `role:readonly` already grants `applications get */*`, and `policy.default` may already grant read to any authenticated user. This is an open question.
- **Coremetry side:** Coremetry stores only a reference (`env:` / `file:`, `internal/secretref/secretref.go:29-60`). **There is no plaintext field.** See §7.2.

### 5.3 Rate and poll guidance

- External (docs): argocd-server has **no general API rate limiter**. The only throttles are on login and webhooks, so throttling must be done by the client.

**Proposed client-side limits (for approval)**
- A token bucket per instance, 1 request per second with burst 5, and at most 2 concurrent requests.
- A global cap across instances.
- Backoff on 429 or 5xx. A circuit breaker per instance.

**What to call**
- **Change-driven GETs only:** call `GET application` when the metrics worker sees a sync event, an OutOfSync or health transition, or a new app, plus a slow round-robin refresh.
  - At 1 request per second, a full per-app sweep of 20k apps takes about 5.5 hours, so bulk refresh must use List with `fields`, per instance, every 15 minutes at most. The body size is unmeasured.

**Caching and safety**
- Cache GET results keyed by `(instance, app_ns, app, operationState.startedAt)`, and skip unchanged apps.
- **Skip the API worker while `lockDegraded`:** every pod is leader then (`main.go:591-596`), and Argo would see N× the load.

---

## 6. DeploymentConfig coverage

| Source | What it gives for DeploymentConfig | Notes |
|---|---|---|
| **KSM** | No DeploymentConfig metrics. DC rollouts appear as ReplicationControllers `<dc>-<N>`: `kube_replicationcontroller_{spec_replicas,status_ready_replicas,status_available_replicas,created,metadata_generation}`, `kube_replicationcontroller_owner{owner_kind="DeploymentConfig",owner_name}` (**EXPERIMENTAL**), `kube_pod_owner{owner_kind="ReplicationController"}` | External (docs): https://github.com/kubernetes/kube-state-metrics/blob/main/docs/metrics/workload/replicationcontroller-metrics.md. On OpenShift, CMO KSM denylists `^kube_.+_created$`, and the minimal profile drops all `kube_replicationcontroller_*` (external (source): https://raw.githubusercontent.com/openshift/cluster-monitoring-operator/master/assets/kube-state-metrics/deployment.yaml) |
| **openshift-state-metrics** | `openshift_deploymentconfig_{created, spec_replicas, spec_paused, status_replicas, status_replicas_available, status_replicas_unavailable, status_replicas_updated, status_observed_generation, metadata_generation, labels}`, labels `namespace, deploymentconfig` | External (docs): https://github.com/openshift/openshift-state-metrics/blob/master/docs/deploymentconfig-metrics.md. **No `latestVersion` or revision series.** Deployed by CMO, `honorLabels: true`, 2-minute scrape (per area report) |
| **Spans** | The k8sattributes processor has no RC or DC attribute, so DC pods get `workload=''` | External (docs): https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/processor/k8sattributesprocessor/README.md. `openshift.deployment.name` feeds only the `service_metadata` deriver fallback |
| **Argo CD** | Treats DC like any resource: built-in health check; resource-tree follows DC → RC → Pod | External (source): `resource_customizations/apps.openshift.io/DeploymentConfig/health.lua` |
| **Coremetry today** | Reads **none** of the above. The MV drops DC pods (`rollout_schema.go:141`), KSM is Deployment-only (`ksm.go:44`), the entity layer stops at RC (`normalize.go:15-17,120-123`), and only the pod-churn heuristic sees `<name>-<deployNo>-<rand>` (`deploys.go:697-703`) | — |

- DeploymentConfig is deprecated since OCP 4.14 (security and critical fixes only).

**Gap:** DC workloads get no rollout row, no KSM evidence and no classification. An Argo sync of a DC app can only be shown on the Argo card, not in Rollouts.

**Recommendation** (decide after Q9.1/Q9.2 counts)
1. If the DC share is small, show DC workloads honestly as "not covered" (trigger `unknown`, reason `dc-not-covered`) and do not build a DC path in v2.
2. If it is material, add a separate slice. That slice needs an operator migration touching the ingest-path MV, which carries 0005/0012-class risk. It would:
   - derive `workload` from the pod name for RC-owned pods (`<dc>-<N>-<rand5>` → `workload=<dc>`, `revision=<dc>-<N>`), which needs the MV to read `k8s_pod` or an RC attribute;
   - add a KSM leg on `kube_replicationcontroller_*`, falling back to `openshift_deploymentconfig_*` for readiness.
3. **Do not** create rollout rows from KSM or Argo alone without an explicit decision. That breaks the span-driven invariant (Q-3).

---

## 7. Settings and mapping model fit

### 7.1 Remote Cluster fields (`api_server_url`, `argo_suffix`, `pair_group`)

**Where they go:** additive `omitempty` fields on `thanos.ClusterConfig` (`internal/thanos/client.go:43-89`), inside the existing blob `system_settings["thanos_clusters"]`.
- No blob migration is needed: old blobs decode to zero values.
- JSON keys are camelCase, following the existing style:
  - `apiServerUrls []string`: **a list**. The `SpanClusterValue` → `SpanClusterValues` history (v0.10.139, `client.go:60-65`) shows single values grow into lists, and `dest_server` spellings vary.
  - `argoSuffix string`.
  - `pairGroup string`.

**Sites that must change together.** If any is missed, the next Save or a mixed-version pod silently wipes the new fields.
1. `ClusterSnapshot` (`client.go:99-121`) and the hand-written copy in `Snapshot()` (`:396-417`).
2. `ReconcileClusterSettings` (`internal/thanos/cluster_identity.go:193-253`): carry stored values forward when the incoming ones are omitted (the `ThanosLabelSource` precedent), with an explicit clear sentinel. Decision Q-7.
3. `checkClusterUniqueness` (`:256-275`): a normalised API URL and an `argoSuffix` may each belong to only one cluster. A `pairGroup` with a single member is a warning, not an error.
4. `putThanosSettings` (`internal/api/thanos_handlers.go:753-837`): trim and validate the fields, and extend the audit details at `:826-835`.
5. The other writers of the same blob: `POST /api/settings/thanos/detect` and `.../assign-span-cluster` (`internal/api/thanos_identity.go:35-43,85-160,248-338`).
6. UI: `frontend/src/pages/settings/ClustersTab.tsx` (`EditRow :26-48`, `fromSnapshot :50-65`, `EMPTY_ROW :67-72`, save mapping `:153-165`) and `lib/types.ts:1974-2019`.

**Normalisation (pure helper next to `cluster_identity.go`)**
- Lower-case the scheme and host, strip a trailing `/`, and apply an explicit default port rule, for example `:6443`. Decision Q-8.
- `https://kubernetes.default.svc` is an alias valid only for the hub's own instances.

**Auto-suggest, never auto-write**
- Suggest values from `argocd_cluster_info{server,name}` (v3.0+ for `name`), matched against `Name`, `ThanosLabelValue` or `SpanClusterKeys`.
- Suggest `argoSuffix` from Q6.5 (dominant last token per `dest_server`).

**Viewer surfaces** (environment matrix, pair warning)
- These cannot read the admin-only settings GET (`api.go:1188-1189`).
- Add an additive `entries:[{id,name,pairGroup,argoSuffix}]` field to the viewer route `GET /api/clusters/sources` (`thanos_handlers.go:727-737`), which has 4 consumers. The alternative is a new route in its own file.

### 7.2 Argo settings blob, hub marker and instances

**Nothing exists today** to mark a cluster as the Argo hub.

**Recommended:** a new blob `system_settings["argocd"]`:

```text
{ enabled, hubClusterId,               # → ClusterConfig.EffectiveID() (immutable, cluster_identity.go:47)
  envList: ["dev","test","prod"],       # drives name parsing + Q6 (synthetic example)
  instances: [ { id, name: "<team>-<env>", hubNamespace, metricsJob, apiUrl,
                 tokenRef: "env:…|file:…", insecureSkipVerify, enabled,
                 discovered: true|false, appsAnyNamespace: bool } ],
  apiWorker: { rps, burst, maxConcurrent }, classification: { windows… } }
```

**Hub resolution**
- Resolve the hub each tick with `ClusterByID`, which returns enabled entries only (`cluster_identity.go:159-171`). A disabled hub gives an explicit "hub unavailable" state.
- **Label injection caveat:** if the hub entry has `ThanosLabelName` set, every Argo query gets the matcher injected (`client.go:541-546`). The label was detected from `kube_node_info` (`internal/thanos/cluster_detect.go:10-18,61`). If Argo metrics carry different external labels, the injected matcher returns 0 series.
  - Mitigations: probe at hub selection (Q0.3 vs Q0.5), or give the Argo reader an explicit "inject: off / on" setting.
- Enabling the hub as a Remote Cluster also puts it into `/clusters`, entity sync and rollout reconciliation. There is no "metrics-only" entry kind. Decision Q-1.

**Instance auto-discovery**
- Use `count by (namespace, job) (argocd_app_info)` ∪ `count by (namespace, job) (argocd_cluster_info)` (Q1.1/Q1.5), applying the §3.2 rules for which label is the instance namespace.
- Discovered instances are stored as `discovered:true` suggestions.
- The metrics worker can use discovered instances immediately. The API worker uses an instance only after an admin sets `apiUrl` and `tokenRef`.

**Secrets**
- Reuse `internal/secretref` as is: `Valid` (`secretref.go:29`), `ResolveWith` with injectable `getenv`/`readFile` (`:39-60`), and the 400 text `InvalidMessage` (`:63`).
- Resolve in `Configure`, following the thanos pattern (`client.go:347-374`), so file rotation is picked up within 30 seconds through `cfgRefresh`.
- **There is no `token` field at all.** This is stricter than thanos, oracle and DevOps; DevOps stores its PAT in plaintext (`internal/devops/client.go:89`).
- **Fail closed:** an unresolved ref skips the instance and records an error. Thanos, by contrast, sends the request with no `Authorization` header (`client.go:562-564`).
- Reference-only also matters because config export dumps `system_settings` verbatim (`internal/api/config_iox.go:35-36`).

**Deployment gap**
- The UI hint (`ClustersTab.tsx:273-278`) and the secretref header (`secretref.go:7`) both say "Helm extraEnv + existingSecret".
- **The chart has no `extraEnv`, `envFrom`, `extraVolumes` or `extraVolumeMounts` for the Coremetry pods.** The only `extraEnv` is `goDemo.extraEnv` (`charts/coremetry/values.yaml:543`, `templates/go-demo.yaml:51`).
- Chart work is required, or operators must patch the manifests. In distributed mode the Secret must be mounted on **both** the api and worker roles, or the "resolved" badge on the api pod will be wrong.

**Wiring checklist**
- `publishConfigReload(ctx,"argocd")` plus a matching case in `internal/api/cache.go:487+`. `TestEveryConfigReloadTopicHasAListener` enforces this.
- The config-import republish list (`config_iox.go:229-231`).
- `cfgRefresh.Add("argocd",…)` next to `main.go:1033`, placed before `Start` (`main.go:1256`).
- An audit action `settings.argocd.update`.
- The route goes in a new `internal/api/argocd_settings.go` via `registerRoutesExtra` (`internal/api/route_registry.go:30`). **`api.go` must not grow** (ratchet: 11662 at HEAD, 11615 in the working tree).

### 7.3 Base app key and name parsing

Built with Go RE2 from the settings lists (`regexp.QuoteMeta` on each item), lower-cased, and anchored at the end so that `env` and `suffix` are robust:

```text
^(?P<prefix>[a-z0-9]+)-(?P<team>[a-z0-9]+)-(?P<component>[a-z0-9-]+?)-(?P<env>ENV_LIST)-(?P<suffix>SUFFIX_LIST)$
base_key = <prefix>-<team>-<component>-<env>        (= name minus "-<suffix>")
pair_key = (base_key, pair_group of the cluster that dest_server maps to)
```

**Consistency and limits**
- The parsed `suffix` must equal the `argoSuffix` of the cluster that `dest_server` resolves to. A mismatch is flagged and does not change the mapping.
- A hyphenated team makes the team/component split ambiguous, so `team` and `component` are "estimated". Q6.6 measures env purity.
- A non-matching name is **valid**: the app keeps a metrics state and can map by namespace or resource, but it has no base key and no pair.
- Env naming today: service-name env suffixes are hard-coded as `-prod`, `-int`, `-uat`, `-prep` (`internal/chstore/job_service.go:127`, `StripEnvSuffix :131`). The Argo `envList` is a **separate** settings list. Whether it should be aligned is Q-9.

### 7.4 Service → Application mapping (priority, confidence, unmapped)

**Cluster join (prerequisite).** `dest_server` → normalised → `ClusterConfig.apiServerUrls` → `EffectiveID()`.
- Unmapped `dest_server` values are **counted, not dropped**, following the `entity_sync_runs.unmapped_keys/counts` precedent (`store.go:3469-3470`).
- The service side is keyed by `(cluster_id, namespace, workload)`. It comes from the MV `service_name` dimension (span cluster value → EffectiveID via `clusterIDMap`, `internal/anomaly/rollout_causes.go:44-66`), or from the entity graph (`wl:<cid>/<ns>/<Kind>/<name>`).

| Priority | `match_method` | Evidence | Proposed confidence | Available from |
|---|---|---|---|---|
| 0 | `manual` | Admin pin ("human pin beats auto", `store.go:2161-2165`; `internal/devops/repo_resolve.go:10-15`) | 1.0 | settings/API |
| 1 | `resource` (exact) | Application `status.resources[]` contains `(kind∈{Deployment,StatefulSet,DaemonSet,DeploymentConfig}, dest ns, name)` = a service workload on the same `cluster_id`; or KSM tracking annotation/label allowlisted (Q9.4) | 1.0 | API worker (Phase 6) or KSM allowlist |
| 2 | `name` (estimated) | Parsed `component` = workload name, or `StripEnvSuffix(service)`, on the same `(cluster_id, dest_namespace)` | 0.7 (0.5 if namespace differs) | metrics only |
| 3 | `namespace` (weak) | Only `(cluster_id, dest_namespace)` overlaps the service's observed `(cid, ns)`; N candidates | 0.3/N, flagged `ambiguous` if N>1 | metrics only |
| — | `none` | No candidate | — | explicit "unmapped" state, not an error |

- Classification (§8.3) uses only `manual`, `resource` and `name` by default. A namespace-only match yields `unknown`.
- Existing sources to reuse:
  - `RolloutRefsForService` (`rollout_problem_telemetry.go:54`), capped at 50 rows. Batch reads need a new bounded query.
  - `EntitySeenForService` (`internal/chstore/entity_queries.go:369`), which returns (cid, ns) pairs.

### 7.5 Storage (per `.claude/skills/clickhouse-schema/SKILL.md`)

**House rules that apply** (clickhouse-schema skill, SKILL.md):
- State tables use `ReplacingMergeTree(version)`, `version DEFAULT toUnixTimestamp64Nano(now64(9))`, and are read with FINAL.
- **`ORDER BY` is the dedup key exclusively** (O3).
- No PARTITION unless needed (P1).
- No Nullable; use sentinels (C4). No LowCardinality for high-cardinality IDs (C2).
- Every table appears in **both** `store.go` (app-managed installs) **and** a new operator migration `migrations/0015_argocd_layer.sql` plus a `_rollback`, with `ReplicatedReplacingMergeTree('/clickhouse/tables/state/<t>', …)`. This follows the 0012 contract (`migrations/0012_rollout_layer.sql:11-17,137`), and 0015 would be added to the wizard `FS` list (`migrations/embed.go:29`).
- The wizard never runs at boot.
- **One writer per table** (E2 full-row replace).
- Deterministic keys, because split-brain under `lockDegraded` writes twice.

| Table (proposal) | Writer | Key / engine | Content |
|---|---|---|---|
| `argocd_app_status` | metrics worker only | RMT(version), `ORDER BY (instance_id, app_namespace, app_name, changed_at)`, TTL 90 days | **Change log.** A row only when the tuple `(sync_status, health_status, operation, autosync_enabled, dest_server, dest_namespace, project, repo)` changes, plus `change_kind` ∈ {baseline, appeared, state, sync, deleted} and sync `phase`. `changed_at` = tick time aligned to the minute (idempotent under two leaders). Latest state = `LIMIT 1 BY` over FINAL (about 20-40k apps) |
| `argocd_app_mapping` | mapper step (inside the metrics-worker leader, every 10 minutes and on settings change) | RMT(version), `ORDER BY (service_name, cluster_id, instance_id, app_namespace, app_name)`, `valid_to` sentinel (entity precedent, `store.go:3422-3443`) | `match_method` (LC), `confidence` Float32, `base_key`, `env`, `suffix`, `component`, `pinned` UInt8, `ambiguous` UInt8 |
| `argocd_app_operations` (**extra, needed for classification**) | API worker only | RMT(version), `ORDER BY (instance_id, app_namespace, app_name, op_started_at)`, TTL 180 days | `phase`, `op_finished_at`, `automated` UInt8, `initiator` (verbatim `initiatedBy.username`, `''` if automated), `sync_revision`, a `resources` Array(Tuple(kind, ns, name)) snapshot |
| `argocd_worker_runs` | both workers | `ORDER BY (worker, started_at, host)`, TTL 30 days (`rollout_reconcile_runs` precedent) | status, instances ok/failed, series read, truncated/partial flags, unmapped counts, API calls and throttles |
| Classification (see §8.3) | classifier (single writer) | Option A or B | — |

The operator named two tables. Classification needs at least `argocd_app_operations` in addition (Q-5).

---

## 8. Workers and classification fit

### 8.1 Metrics worker (leader-elected, 60s, state changes only)

**Wiring** (inside `if mode.worker {…}`, `main.go:1078-1186`, cloned from `main.go:1174-1185`)
- Key `"argocd-metrics"`, `LeaderTTL(time.Minute)`, which gives a 3-minute lease.
- `SetOnAcquire(Tick)`; `Start(chstore.WithQueryTag(ctx,"worker:argocd-metrics"))`; `go Run`.
- Reuse from the reconciler: the `inFlight` compare-and-swap, the 30-second-step `Run`, a tick deadline, the leadership re-check before writing (`reconciler.go:389-394`), and a detached-ctx run row.

**Reader** (prerequisite; this is where a house decision constrains the design)
- A worker-grade reader must provide: a per-call series cap (~50k) and body cap (32-64 MiB), a `Truncated`/`Total` flag, Thanos warnings, `partial_response=false`, POST form encoding, its own 30-45 second client plus a Thanos `timeout=` parameter, and streaming decode.
- **House decision:** new callers use `internal/promapi`, and `internal/thanos` is not refactored (`internal/promapi/promapi.go:21-26`). The v1 audit rejected adding `RangeSamples`/`InstantSamplesN` to `thanos.Service` (`docs/audits/rollouts-audit.md:252`).
- `promapi` already has `QuerySeriesMeta` with `Total`/`Truncated` (`internal/promapi/meta.go:28-78`), but it still has fixed 1000/8 MiB caps (`promapi.go:78,83`), GET only, and `isPartial` for VictoriaMetrics only (no Thanos warnings).
- `thanos.Service` has **no exported resolved-token or request builder**: `effectiveTokenFor` is unexported (`client.go:386`), and cluster-matcher injection is unexported.
- The seam is therefore a small exported `thanos` helper (for example `RequestFor(clusterID) promapi.Request`, plus an exported `WithClusterMatcher`), with the caps and warnings added to `promapi`. Decision Q-6.

**Tick shape (proposal)**
1. For each instance shard:
   - Fetch the non-steady set: `group by (namespace, exported_namespace, name, dest_server, dest_namespace, sync_status, health_status, operation, autosync_enabled, project) (argocd_app_info unless argocd_app_info{sync_status="Synced",health_status="Healthy",operation=""})`.
   - Fetch sync events:
     - `sum by (namespace, exported_namespace, name, phase) (increase(argocd_app_sync_total[3m])) > 0`
     - **or** new sync-series identities: `group by (namespace, exported_namespace, name, phase) (argocd_app_sync_total) unless on (…) group by (…) (argocd_app_sync_total offset 3m)`. A series born by its first sync counts 0 in `increase`.
2. Diff in memory against the previous state and write only the changes.
3. **On acquire (failover):** rebuild the previous state from `argocd_app_status` (latest per app) before the first diff, following the reconciler's `RolloutRecentRows` pattern (`reconciler.go:331`). Otherwise every failover re-emits about 40k "changes".
4. **First ever run:** write `baseline` rows, not events.
5. **Full sharded inventory every 10-15 minutes.** It covers the steady set and also detects deletions and apps that returned to Synced/Healthy.
6. **Stateless OutOfSync cross-check** (after failover, or as a guard):
   - `group by (namespace, exported_namespace, name) (argocd_app_info{sync_status="OutOfSync"}) unless on (namespace, exported_namespace, name) group by (namespace, exported_namespace, name) (argocd_app_info{sync_status="OutOfSync"} offset 2m)`.
   - The offset must be ≥ tick + scrape interval. Flaps shorter than the scrape interval are invisible. Q10 sizes this.
7. **Result hygiene:**
   - Handle duplicate `(namespace, name)` rows (HA without dedup, staleness) by taking the latest via `timestamp()`.
   - On any partial or warning, **skip the diff for that shard**. Never treat missing series as deletions.

### 8.2 API worker (rate-limited)

- Separate key `"argocd-api"`, so the two workers can be led by different pods (`internal/cache/leader.go:48-52`).
- **Work queue:** fed from the new rows in `argocd_app_status` (sync, state and appeared changes) plus the slow round-robin.
- **Calls:** `GET application` per changed app (§5.1), and List with `fields` for bulk inventory.
- **Limiter:** no reusable limiter exists in committed code, and `golang.org/x/time` is not in `go.mod`.
  - The uncommitted PromQL console limiter (`internal/api/promql_console_guard.go:219`) is per user, fixed window and package-private to `internal/api`.
  - Build (or extract) a small token-bucket helper with an injected clock, applied per instance plus a global cap.
- **Skip when `lockDegraded`.** Skip an instance whose `tokenRef` does not resolve (fail closed).
- Parallelism follows the entity syncer's `ParallelClusters` bound (default 4, at most 16; `internal/entity/settings.go:33,61`).
- **Visibility to api pods:** worker memory is invisible to them. Publish status through `argocd_worker_runs`, following the `rollout_reconcile_runs` and `thanos_label_checks` precedents (`internal/thanos/cluster_detect.go:164-174,208-265`).

### 8.3 Classification joined with rollout events

**Revision spaces do not match.**
- Rollouts use the ReplicaSet name or image tag. Argo uses a GitOps SHA or chart version, and no ReplicaSet → SHA mapping exists.
- The join is therefore `(cluster_id via dest_server, namespace, workload ∈ mapped app) + time proximity`.

**Anchor time**
- `ksm_started_at` when it is non-zero. It may always be zero on OpenShift (Q9.3).
- Otherwise `first_span_at`.
- `started_at` is 5-minute bucket-aligned and lags traffic by 10-15 minutes.

| Class | Rule (proposal; windows are settings with these defaults) |
|---|---|
| `argo_auto` | A mapped app (`manual`, `resource` or `name`) has an operation with `automated=true` and `op_started_at − 2m ≤ anchor ≤ op_finished_at + 5m` (span anchor: `+15m`) |
| `argo_manual` | Same window, operation with `automated=false` and a non-empty initiator |
| `out_of_band` | The workload is resource-mapped (or name-mapped) to an app, **no** operation falls within `[anchor − 30m, anchor + 15m]`, and ideally the app went `OutOfSync` within ±15m of the anchor (drift; with autosync mostly off it stays OutOfSync) |
| `unknown` | No mapping or only a namespace match; a metrics or API gap in the window (a failed, partial or skipped run); DC or uninstrumented workloads; conflicting evidence |

- **Metrics-only fallback** (no API credentials yet): a sync event in the window plus `autosync_enabled` gives only an **estimated** `argo_auto` or `argo_manual` (confidence ≤ 0.5), because autosync apps can also be synced by hand. The operator decides whether to show this (Q-11).
- `kubectl rollout restart` shows as change kind "config" (`rolloutRow.ts:72`). Telling it apart from an out-of-band edit needs extra evidence (audit log, §8.5).

**Where the class lives** (decision Q-4)
- **Option A:** columns on `workload_rollouts`, for example `trigger`, `argo_instance`, `argo_app`, `match_method`, `trigger_confidence`, with the reconciler as the only writer reading Argo tables inside its tick.
  - Touchpoints: `rollout.Rollout` (`reconcile.go:87`), `rolloutRowCols`/`scanRollout`/`RolloutUpsert` (`chstore/rollouts.go:89,112,217`), **`rolloutEqual`** (`reconcile.go:1011`; if the field is missing there, changes are never written), `MarshalJSON`, the TypeScript `WorkloadRollout` (`frontend/src/lib/types.ts:7425`), a DDL ALTER, and migration 0015.
  - **Problem:** terminal and completed rows are frozen, so API evidence that arrives after completion would never be applied.
- **Option B (recommended):** a separate `rollout_trigger` table keyed by the 5-part rollout ID, RMT(version), written by one classifier step in the Argo metrics-worker leader. It is re-evaluated for rows whose window is still open (for example the last 24 hours).
  - It avoids a second writer on `workload_rollouts` and allows late evidence.
  - Cost: the Rollouts filter must be a SQL **semi-join before LIMIT** in `rolloutWhere`. Filtering after LIMIT is the trap noted at `internal/api/rollout_detail.go:143`.

**Argo syncs with no rollout row** (uninstrumented workloads, DC, config-only changes with no new ReplicaSet) are **not** rollouts today. Show them on the Service Argo card. Whether they should create rows is Q-3.

### 8.4 Azure DevOps enrichment (reuse, read path only)

**Reuse as is**
- `ResolveRepo` (`internal/devops/repo_resolve.go:10-15,122`).
- `ResolveVersionRef` / `refCommit`: image tag → `tags/{version}` → SHA (`internal/devops/version_ref.go:26,60-121`).
- `ResolveFrameLinks` → `Revision{Version, Ref, SHA, Verified}` (`frame_links.go:121-128,201`).
- `FileURLAt` / `BrowseURL` (`client.go:788-842`).
- Caches: 10 minutes; misses 2 minutes (`code.go:88,319-320`).

**Caveat**
- The Argo `repo` label and `sync_revision` refer to the **GitOps/manifest** repo, which answers "who changed the manifest".
- "Which code shipped" is only reachable through the rollout's `image_tag` → `ResolveVersionRef`.

**Does not exist:** commit-by-SHA lookup (author, message), PR-by-commit, build/release lookup, a commit-page URL builder, and any rate limiter in `internal/devops`.

**Proposal:** enrichment happens on the read path only (card and drawer), cached, and never in the workers. The GitOps-commit link needs a new commit lookup and a PAT that can read the GitOps repo (Q-13).

**Pipeline deploy events** already exist (`POST /api/operator-events` kind=deploy, `docs/DEPLOY-EVENTS.md`, `link=$RELEASE_RELEASEWEBURL`). They can be shown next to an `argo_manual` classification when `(service, version)` matches (`internal/chstore/deploys.go:1081-1135`).

### 8.5 Optional: audit-log actor enrichment

- Coremetry has **no Kubernetes/OpenShift audit-log ingestion.**
- For `out_of_band`, the actor (who edited the Deployment) could come from API-server audit logs, if they are shipped to the log store. Argo sync actors come from `initiatedBy` or `GET …/events`.
- Out of scope for the first phases; Q-14.
- SSO `username` values are emails. The house rule is "No PII redaction features — full fidelity" (`CLAUDE.md` Hard constraints; "Don't propose data redaction features" under Anti-patterns). The initiator is therefore stored and shown verbatim. No hashing or masking is proposed.

---

## 9. Service page card, environment matrix, pair consistency, Rollouts source filter

### 9.1 Backend

- New file `internal/api/argocd_service.go`, registered through `init()` → `registerRoutesExtra` (`route_registry.go:27-36`; precedents `devops_frames.go:42-48`, `rollouts.go:35-47`).
- Route: `GET /api/services/{name}/argocd?cluster=&from&to`.
  - Viewer level, with no role gate or audit (`devops_frames.go:11-14`).
  - `serveCached` with every input in the key.
  - 404 `{disabled:true}` when the flag is off (`rollouts.go:52-59`).
  - It sits next to `/api/services/{name}/pods` (`entities.go:47`).
- Response: mapped apps (match method and confidence), latest status (`argocd_app_status`), last operation (`argocd_app_operations`), the environment matrix, pair warnings, an unmapped flag, and `source`/`asOf`.
- Pair consistency is computed **at read time** (no stored state):
  - same `base_key` across clusters of one `pairGroup`;
  - a warning when `sync_status` or `health_status` differ, when one side is missing, or when `sync_revision` differs (the last needs the API);
  - `pairGroup`/`argoSuffix` read from the extended `/api/clusters/sources` data (§7.1).

### 9.2 Frontend conventions

- **Data layer:** the type goes in `lib/types.ts`, the method in `lib/api.ts`, and one shared hook in `lib/queries/services.ts` (precedent `useServiceRollouts7d`, `:101-113`).
- **Component:** `pages/service/ArgoCard.tsx`.
  - Mounted in the Details tab with `<SectionHead id="dtl-argo" source="hub-thanos · argocd api">` plus `LazyMount`, next to `dtl-runtime` (`Service.tsx:643`).
  - Add a ToC entry in `pages/service/DetailsToc.tsx:14-27`, and extend the pin test `pages/service/detailsSections.pin.test.ts`.
  - **Do not add a tab.** The union `ServiceTab` (`Service.tsx:55`) already has 7 tabs.
- **Atoms and rules:**
  - `KeyValue`/`KeyValueRow` for attributes, monospace only for SHAs.
  - `Badge` tones.
  - The matrix is a `useDataTable` table with a unique `storageKey` and dynamic columns (precedent `ServiceClusterBreakdown.tsx:19-28,63,71,127-129`).
  - Status as dot plus text (T9); one-line cells with middle ellipsis for SHAs (T11); empty and error states inside the table (T12) (`docs/DECISIONS.md:727-747`).
  - Healthy is neutral, and colour is used only for deviation (`:686-694`).
  - Hide the card when there is no data, and name the source (`pages/service/HeapBaselineCard.tsx:11-22`).
- **Matrix shape:** rows = component (`base_key` without env), columns = env × cluster (grouped by `pairGroup`). Each cell shows sync/health, the last operation time and trigger, and the short revision.
  - Non-conventional names appear as their own rows ("name not parsed").
  - Unmapped services show an explicit "no Argo Application mapped" line, not an empty card.
- **Rollout drawer:** `RolloutDrawer.tsx:63-85` already shows "kaynak {detectedBy}". Add a trigger line there (class, app, initiator, confidence).
- **Live updates:** no card opens its own `EventSource`. If Argo changes should push, add an event type to `lib/queries/eventInvalidations.ts` on the shared `/api/events` bus (`CLAUDE.md` Frontend rules). Otherwise poll at 10 seconds or more and pause on `document.hidden`.

### 9.3 Rollouts "source" filter

- **Naming collision:** "Kaynak" already means `detectedBy` (`Rollouts.tsx:64`), and `ChangeRow.Source` and `RecentDeployEntry.Source` exist.
- **Proposal:** URL param `?trigger=` (argo_auto | argo_manual | out_of_band | unknown), labelled "Tetikleyici", plus a "Tetikleyici" column. Optionally rename the existing column to "Kanıt" (evidence). Decision Q-10.
- **Backend:** add `Trigger` to `RolloutFilter`, applied as a semi-join in `rolloutWhere` before LIMIT under Option B, and add it to the cache key, which is pinned by `TestRolloutKeysCarryEveryInput`.
- Consider exposing the existing `workload`/`kind` filters at the same time; the API already supports them.

### 9.4 Event emission path for Problem correlation

**Preferred: no new events in v2 phase 1.**
- Correlation already flows `workload_rollouts` → `rolloutCauses` → `RolloutEvidence`.
- Add `Trigger` (+ `ArgoApp`) to `RolloutEvidence` (`rootcause_hypothesis.go:70-86`).
- Optionally add a trigger multiplier in `Score` (for example `out_of_band ×1.10`). Decision Q-15.

**If events are wanted** (Argo syncs with no rollout row, pair drift on the annotation lane)
- The `events` table has **no cluster, namespace or app columns**, and `UpsertEvent` uses random ids (`event.go:47,139-141`). A leader-elected writer needs a deterministic id **and** a deterministic `time`, because `ORDER BY (time, id)`.
- Deploy correlation reads only `kind='deploy'` with `label` = **version/image tag** (`deploys.go:1081-1135`; `internal/correlator/hypothesis.go:130-142,301-330,341,571`). An Argo event must therefore use the image tag, never the Git SHA, or it duplicates inferred deploys and never matches.
- A new kind (for example `argo`) needs the hard-coded kind filter in `frontend/src/pages/Events.tsx:317-324` updated.

**If paging on pair inconsistency is wanted**
- Use a Problem with `kind=service` (it appears in the attention strip without UI work) and a deterministic RuleID such as `argo:pair:<pair_group>/<base_key>`, with lifecycle owned by the worker.
- **The stale sweep resolves any open problem not touched within 3 × 1 minute** (`evaluator.go:1117`), while the leader lease is 3 minutes. A failover would make it flap.
- It therefore needs a `PollerOwnedSubject` exemption (`internal/chstore/problem.go:1513-1530`), following the ext precedent in `docs/DECISIONS.md:594-609`.

---

## 10. Phase plan, risks, open questions

### 10.1 Proposed phases (small reviewable commits; each ships alone via /release)

| # | Slice | Contents | Depends on |
|---|---|---|---|
| P0 | This audit + operator query results | §4 paste-back; decisions Q-1…Q-16 | — |
| P1 | Worker-grade Prometheus reader | `promapi` per-call caps + `Truncated` + Thanos warnings/`partial_response` + POST + worker client; exported `thanos` request builder/matcher; table tests. Coordinate with the in-flight PromQL console reader (C5), so there is one implementation. **Optionally** fix the silent 1000-series truncation in the rollout KSM leg and entity `SnapshotQueries` (Q-2) | P0 |
| P2a | Remote Cluster fields | `apiServerUrls`/`argoSuffix`/`pairGroup` + normalise + uniqueness + carry-forward + ClustersTab + `/api/clusters/sources` entries | P0 |
| P2b | Argo settings blob | `system_settings["argocd"]`, hub + instances (tokenRef only), reload topic, audit, admin UI, discovery suggestions (read-only probe) | P1, P2a |
| P2c | Helm chart | `extraEnv`/`envFrom`/`extraVolumes`/`extraVolumeMounts` on api + worker (`/helm-chart-coremetry`) | — (before P6) |
| P3 | Schema | `argocd_app_status`, `argocd_app_mapping`, `argocd_app_operations`, `argocd_worker_runs` (+ classification table if Option B) in `store.go` + `migrations/0015_argocd_layer.sql` + rollback + wizard entry | P0 decisions |
| P4 | Metrics worker | leader `argocd-metrics`, sharded reads, diff, baseline, rebuild-on-acquire, runs table, admin diagnostics route | P1, P2b, P3 |
| P5 | Mapper | name + namespace mapping, manual pin, unmapped counts | P4 |
| P6 | API worker | leader `argocd-api`, limiter, per-instance creds, GET on change, `status.resources` → resource-exact mapping, operations table; **never `refresh`** | P2c, P4, P5 |
| P7 | Classification | classifier + `trigger` storage (A/B) + `RolloutEvidence.Trigger` + Rollouts filter/column + drawer line | P5, P6 |
| P8 | Service page | `/api/services/{name}/argocd`, Argo card, env matrix, pair warning | P5 (P6 for revisions) |
| P9 | Optional | events/Problem emission, DevOps GitOps-commit enrichment, DC coverage slice, audit-log actor | decisions |

### 10.2 Risks

1. **Size and truncation:** a 40k-series body overruns today's caps silently (1000 series) or noisily (8 MiB decode error). The worker needs P1 first.
2. **Thanos partial responses** (on by default, reported only as warnings; external (docs): https://thanos.io/tip/components/query.md/) would look like apps disappearing. Skip the diff on any warning.
3. **Label injection on the hub** may hide Argo series, and the `namespace` / `exported_namespace` case is unknown until Q1.2.
4. **Argo version drift:** `autosync_enabled`, resolved `dest_server`, `dry_run` and `cluster_info.name` all depend on version, and instances may run different versions.
5. **Naming convention not guaranteed:** mis-mapping risk. Confidence and "estimated" labelling are mandatory, and unmapped is valid.
6. **Split-brain (`lockDegraded`):** duplicate writes (keys must be deterministic) and N× Argo API load (skip the API worker).
7. **Mixed-version settings wipe** of new `ClusterConfig` fields during a rolling deploy or from a cached old bundle.
8. **No-write rule:** the `refresh` parameter and the resource-tree cache-miss path both write the refresh annotation. The client must be guarded (no `refresh`; resource-tree on demand only, or never).
9. **Initiator identity:** `initiatedBy.username` holds SSO emails for UI syncs. They are stored verbatim per the house no-redaction rule. Whether a UI sync by a human can be told apart from a CI account depends on Q-12.
10. **`ksm_started_at` may always be zero on OpenShift** (`_created` denylist). Classification then falls back to the coarser span anchor.
11. **The existing KSM/entity truncation** makes the evidence v2 joins against partial on large clusters.
12. **Secrets:** the chart cannot mount them today; config export would leak any plaintext token (hence reference-only).
13. **Problem flapping** from the 3-minute stale sweep vs the 3-minute lease, if Problems are emitted.

### 10.3 Conflicts between the plan and existing code or decisions

| Plan item | Conflicts with | Resolution needed |
|---|---|---|
| Worker-grade Thanos reader inside `internal/thanos` (area-report suggestion) | `promapi` house decision (`promapi.go:21-26`; `rollouts-audit.md:252`) | Q-6 |
| Argo worker updating rollout rows | Full-row-replace contract, reconciler as writer (`rollout_schema.go:27-33`, `reconciler.go:331,395`) | Q-4 |
| Classifying Argo deployments of uninstrumented or DC workloads | Rows are span-driven only (`reconcile.go:767`); `detected_by='ksm'` never written | Q-3 |
| Rollouts "source" filter | "Kaynak" = `detectedBy` (`Rollouts.tsx:64`), `ChangeRow.Source`, `RecentDeployEntry.Source` | Q-10 |
| Single `api_server_url` | List precedent (`SpanClusterValues`) and URL spelling variance | Q-8 |
| Settings-provided env list | Hard-coded service env suffixes (`job_service.go:127`) | Q-9 |
| Hub metrics only on the hub Thanos | Hub entry would also join `/clusters`, entity sync and rollouts; label injection | Q-1 |
| Credential refs "via existing secret-ref mechanism" | Documented Helm `extraEnv` path does not exist for the Coremetry pods | Q-16 |
| Service page Argo card next to rollouts | The Service page shows pod-churn, not `workload_rollouts` (`Service.tsx:643-655`) | Q-15 |
| Events for correlation | `events` lacks cluster/namespace; deploy events keyed on (service, image-tag version) | §9.4 |
| Worker-owned Problems | Stale sweep at 3× interval (`evaluator.go:1117`) | §9.4 |
| API route placement | `api.go` ratchet, 11662 at HEAD and 11615 in the working tree (`.claude/baselines/api_go_lines`) | new files via `registerRoutesExtra` |
| Hashing or masking SSO initiator emails (area-report option) | House rule "No PII redaction features" (`CLAUDE.md`) | Resolved: store verbatim; not proposed |

### 10.4 Open questions and decisions for the operator

**Hub and metrics**

1. **Q-1 Hub entry.**
   - Is the hub an ordinary enabled Remote Cluster (it then also shows in `/clusters`, entity sync and rollouts), or do we add a metrics-only entry kind?
   - Is `hubClusterId` in the new `argocd` blob acceptable?
2. **Q-2 KSM/entity truncation.** Should the silent 1000-series truncation (`client.go:584-586` behind `ksm.go:53`, `entity/samples.go:21-30`) be fixed in P1, before v2 classification depends on it?

**Rollout rows and classification**

3. **Q-3 Row creation.** Should Argo syncs (or KSM) be allowed to create rollout rows for uninstrumented or DC workloads? This changes the span-driven invariant. Or should they stay visible only on the Argo card?
4. **Q-4 Classification storage.** Option A (columns, reconciler writes) or Option B (separate `rollout_trigger` table, semi-join filter)? B is recommended.
5. **Q-5 Tables.** Approve `argocd_app_operations` and `argocd_worker_runs` in addition to the two named tables? Approve the change-log semantics of `argocd_app_status` and the TTLs (90d / 180d / 30d)?

**Reader, settings and naming**

6. **Q-6 Reader seam.** Extend `promapi` (caps, warnings, POST) plus a small exported `thanos` request builder, which follows the house decision? Or a new `internal/thanos` method, which conflicts with it? What memory ceiling is acceptable in the worker pod (proposal: at most 64 MiB body and at most 50k series per call, streaming decode)?
7. **Q-7 Mixed-version wipe.** Carry new cluster fields forward in `ReconcileClusterSettings` (with a clear sentinel), or accept a fast roll?
8. **Q-8 `apiServerUrls`.**
   - A list with aliases?
   - Normalisation rules (default port, trailing slash)?
   - One `argoSuffix` per cluster, or aliases?
   - Is `pairGroup` free text or a managed list?
9. **Q-9 Environment source of truth.** Which is canonical: the service-name suffix, `deploy_env`, or the `<env>` segment of Argo names? Should the Argo `envList` be aligned with `EnvSuffixes`?

**UI and correlation**

10. **Q-10 Filter naming.** `?trigger=` / "Tetikleyici"? Rename the existing "Kaynak" column?
11. **Q-11 Metrics-only mode.** Before API credentials exist, show "estimated" `argo_auto`/`argo_manual` derived from `autosync_enabled`, or only `unknown`?

**API side**

12. **Q-12 Who syncs.** Does Azure DevOps sync through the Argo API (so `username` is a CI account) or only commit to Git (so a human syncs, often via SSO)? Should `argo_manual` be split by CI account vs human, using a settings list of CI account names? The initiator is stored verbatim either way (house no-redaction rule).
13. **Q-13 DevOps scope.** Can the DevOps PAT read the GitOps repo? Are commit and PR lookups acceptable under "Code: Read" (`internal/devops/client.go:681`)?
14. **Q-14 Audit-log actor.** Are Kubernetes/OpenShift API audit logs available in the log store for `out_of_band` actor enrichment, or is it out of scope?
15. **Q-15 Service page and correlation.**
    - Should the Service page also get a `workload_rollouts` view, next to the Argo card?
    - Should `trigger` feed Problem scoring (a multiplier), `/api/changes`, or the MCP `source` tag?
    - Should pair inconsistency page (Problem) or only warn (card, optional event)?
16. **Q-16 Secrets delivery.**
    - Is chart work (P2c) in scope, or will the operator patch the manifests?
    - Must api pods also mount the Argo Secret, so the resolved badge and a test-connection action work?
    - What token lifetime and rotation apply (`localUsers` auto-renew vs `generate-token --expires-in`)?
    - Does `policy.default` already grant read?
    - Is the implicit refresh on a resource-tree cache miss acceptable, or must resource-tree be banned?

**Facts only the operator can supply (from §4)**
- Argo CD / GitOps and OCP versions per instance.
- Which Prometheus scrapes each instance, and at what interval.
- Whether dedup is on.
- The Q1.2 collision case.
- Whether any instance has apps-in-any-namespace.
- Whether `destination.name` is in use.
- Whether cluster-secret `server` strings are byte-identical to the planned `apiServerUrls`.
- Whether each side of an active-active pair has its own Remote Cluster record and span value.
- Whether target KSM is the CMO one (the `_created` denylist, minimal vs default profile).
- Whether openshift-state-metrics is scraped.
- The DeploymentConfig share.
- Hub querier limits (timeout, max samples).
