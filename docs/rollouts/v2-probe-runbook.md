# Rollouts v2 — §11 query pack as an admin probe (runbook)

**Version:** v0.10.979. **Source:** `docs/rollouts/v2-audit.md` §11.0–11.9 (the query catalogue),
`internal/rollout/v2probe` (pure catalogue / tokeniser / renderer), `internal/api/rollouts_v2_probe.go`
(routes). **Scope:** K, D, R, H, N (metrics via the Remote Cluster registry) and T (ClickHouse).
A (§11.6 Argo CD API) and V (§11.7 Azure DevOps) are **out of scope** — the report prints
`skipped (metrics-only)` for them (decision 2026-09-27 "argocd şimdilik metrikle").

All names below are synthetic (`cluster-a`, `hub-1`, `coremetry.example.invalid`); replace only the host.

## 0. What it does

`POST /api/admin/rollouts-v2/probe` starts an **asynchronous, read-only** run that executes the whole
§11 catalogue through the readers Coremetry already has (`thanos.WorkerQuery` with the Remote
Cluster credentials — the operator never handles a Thanos token — `ConsoleLabels`/`ConsoleLabelValues`
for label APIs, `chstore` for T). Every result is classified with the `sourcestate` vocabulary
(`ok`, `empty`, `unauthorized`, `unreachable`, `timeout`, `partial`, `truncated`, `not_configured`,
`error`), every **value** is replaced by a stable synthetic token, and one markdown report (plus the
structured results) is kept **in this pod's memory for 1 hour**. `GET /api/admin/rollouts-v2/probe`
returns it. The mapping raw → token is built inside the run goroutine and is **unrecoverable by
design**: it is never stored, logged or returned (`tokens` in the response is only its size).

What stays literal: label **names**, counts, enum values (`Synced`, `Healthy`, `true`), versions,
ports, metric names, and upstream warning text (with cluster names / URL hosts scrubbed).
What is tokenised: cluster values (`cluster-a`, `hub-1`, `cluster-c`…), `dest_server` hosts
(`https://api.cluster-a.<domain>:6443`), namespaces (`<team-1>-prod`), jobs, app names (`app-1`),
projects, repos, suffixes (`<suffix-a>`), pods, `deploy_env` (`prod-cluster-a`). Any label the
catalogue does not know is **default-denied** (`<label-N>`) and listed in the report footer.

## 1. Prerequisites

- An **admin** session or an admin `cmk_` service token (Admin › API tokens; `internal/auth/api_tokens.go`).
- Remote Clusters configured and enabled (Settings › Remote Clusters), including the two Argo hubs
  (Settings › Argo CD › `hubs[]`). Every selected cluster's `tokenRef` must resolve — an unresolved
  reference is rejected **before any request** (`400 guardrail`), the console family does not fail closed.
- Optional but recommended for the N pack: `envList` in Argo CD settings, `argoSuffix` and
  `pairGroup` on each Remote Cluster (§11.5 needs the env and suffix alternations).

## 2. Run

```bash
CMK='cmk_…'                                    # admin service token
BASE='https://coremetry.example.invalid'

# start (defaults: all enabled clusters minus the hubs as targets, hubs from Argo settings, all packs)
curl -sS -X POST -H "Authorization: Bearer $CMK" -H 'Content-Type: application/json' \
     -d '{}' "$BASE/api/admin/rollouts-v2/probe"
# → 202 {"runId":"…","status":"running","pod":"coremetry-api-0","budgetS":300,"targets":[…ids],"hubs":[…ids],"planned":331}

# poll every 10 s until status != running
watch -n 10 "curl -sS -H 'Authorization: Bearer $CMK' $BASE/api/admin/rollouts-v2/probe | head -c 300"
# 202 {"status":"running","progress":{"calls":118,"planned":331,"unit":"cluster-b","pack":"K"}}
# 200 {"status":"done" | "budget_exhausted" | "failed", …}

# save the markdown
curl -sS -H "Authorization: Bearer $CMK" "$BASE/api/admin/rollouts-v2/probe?format=md" -o rollouts-v2-probe.md
```

Body options (all optional):

```json
{
  "targets": ["c-1a2b3c4d"],            "hubs": ["c-9f8e7d6c"],
  "packs": ["K","D","R","H","N","T"],
  "envList": ["dev","test","prod"],     "suffixList": ["ocpa","ocpb"],
  "options": { "withoutMatcher": true, "dedupCheck": true, "secondSample": true,
               "hubInject": {"c-9f8e7d6c": true}, "budgetS": 300 }
}
```

- `targets` / `hubs`: Remote Cluster ids (`c-…`, from Settings › Remote Clusters). A hub may not also be a target.
- `packs`: subset of `K D R H N T`. If a run reports `budget_exhausted`, split: first `["K","D","R"]`, then `["H","N","T"]`.
- `envList` / `suffixList`: override the Argo settings env list and the union of `argoSuffix` values. The N pack is
  skipped with a note when either is empty.
- `options.withoutMatcher`: on a hub with a Thanos cluster label, run H0–H1 **without** the matcher first
  (§11.0 "Hub"). `hubInject: {id: false}` means "injection is already known to be wrong": every H call runs
  without the matcher and the separate pass is skipped. `dedupCheck` adds K0.4b / H0.2b (`dedup=false`),
  `secondSample` adds the second K0.6b sample ≥ 30 s after the first. `budgetS` 60–600 (default 300).

Guardrails (all before any upstream request): unknown/disabled id, hub = target, unknown pack, unresolved
`tokenRef`, empty plan → `400 {"errorType":"guardrail"}`; a run already in progress on this pod → `409 busy`;
Remote Cluster service not wired → `503`.

## 3. Before pasting the report

1. `tokens` in the JSON must be `> 0` (the tokeniser ran).
2. Belt and braces — grep the file for your real cluster names, API hosts and team names:
   `grep -i -E 'prod-01|api\.real|payments' rollouts-v2-probe.md` must print nothing.
3. Paste the whole markdown into the chat. It contains the §11.9 paste-back table (columns
   `cluster-a | cluster-b | hub-1 | hub-2`), one block per query (template expression, state, rows/scalar,
   warnings), the V1–V14 verdicts with evidence, the decisions-informed table and the skipped list.

Never paste: the raw JSON of another admin endpoint, Remote Cluster settings, or Grafana screenshots —
the probe report is the only artefact designed to leave the estate.

## 4. Multi-pod api

The report lives in the memory of the pod that ran it. With several `COREMETRY_MODE=api` replicas a
`GET` can land on another pod and answer `404 {"status":"none","pod":"coremetry-api-1"}`. Either pin a pod
(port-forward to one replica, or the Route's sticky cookie) or simply retry until the `pod` in the 202
answer is the one you reach. Nothing is persisted in ClickHouse or Redis on purpose.

## 5. Reading the outcome

- Unit line `early stop: unauthorized streak after K0.2` — two consecutive 401/403 (or unreachable) on a
  cluster stop that unit; the others continue. Fix the token/role and rerun with `targets: [id]`.
- `budget_exhausted` — the 5-minute default (or your `budgetS`) or the 400-call cap ran out; remaining rows
  are `skipped: run budget exhausted`. Split the packs.
- `T0 fallback: cluster column missing — 0011 not applied` — the spans table has no `cluster` column;
  T1/T2 were re-run on `res_values[indexOf(res_keys,'k8s.cluster.name')]`.
- `default-denied label names` in the footer — a label the catalogue does not classify; its values were
  hidden (`<label-N>`). Report the name so the allowlist can be extended.

Audit: exactly one `rollouts_v2.probe` row per run (ids, packs, status, calls, durations) — never a value.
