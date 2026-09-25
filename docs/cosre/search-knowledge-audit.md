# CoSRE `search_knowledge` via Azure DevOps Search — Phase 0 audit and proposed design

Status: **read-only audit, awaiting approval.** No code was changed for this report.
Snapshot: 2026-09-25, `main` @ v0.10.919. File references are `path:line` at that snapshot.
Host and organisation names are placeholders (`<collection-url>`, `<project>`); no real names are recorded.
Revision 2: fact-checked against the code and reviewed by a design critic. 13 factual corrections
and 29 design issues are folded in.

> **Operator decision (2026-09-25): RAG will not be used** ("hiç kullanmadım, ihtiyaç da yok").
> The local RAG subsystem (`internal/rag`, `rag_chunks`, the URL/wiki crawler, chat tier 3) is out
> of this design; §1.5 keeps the findings as context only. The local knowledge source is **runbooks**,
> and Azure DevOps (ADO) is the external source. The tool's existing `document` value stays for
> contract compatibility and is inert without RAG documents. Removing RAG entirely is a separate
> queue item and is not part of this work.

## TL;DR

1. **`search_knowledge` already exists** as a live CoSRE/MCP tool (lexical search over runbooks and
   RAG chunks, `internal/mcptools/knowledge_tools.go:317`). One registry feeds chat and MCP, so a
   second tool with that name is impossible. **Proposal: extend it.** ADO `wiki`, `code` and
   `workitem` become extra backends behind the same tool, which is also the seam for a later vector
   backend. The response changes in a few documented, small ways (§3.1).
2. **An ADO client already exists: `internal/devops`** (PAT, Server/TFS flavour detection,
   sanitising, code search at `codesearch.go:286`). **Reuse it** instead of adding
   `internal/integrations/azdo`.
3. **The target is on-prem Azure DevOps Server.** A live operator run found that api-version 7.1 is
   rejected ("latest supported is 7.0", `codesearch.go:280-285`), which points to Server 2022.
   Consequences:
   - Endpoints are collection-scoped (`<collection-url>/_apis/search/...`) with a server-side
     project filter, not `almsearch.dev.azure.com`.
   - The api-version is negotiated per endpoint family, not fixed at 7.1.
   - **Entra service principals and managed identities do not work against ADO Server.** A PAT for a
     dedicated service account is the primary auth, not a fallback.
4. **The real access boundary is the ADO service account's permissions, not the PAT.** A PAT cannot be
   limited to projects. Coremetry's project allowlist is a second, query-side filter. It is applied
   to every search and every page read, including IDs supplied by the model (§3.10).
5. **Incident analysis (RCA) has no tool loop, by an explicit operator decision**
   (`internal/api/rca_shields.go:134-138`, `internal/copilot/prompts.go:846-848`). **Proposal:** keep
   that decision. Knowledge enters RCA as a deterministic prefetch with its own citation IDs
   (`K1..Kn`). Only the server resolves them to URLs, and they are framed as data, not instructions.
6. **With default settings the chat tool loop is rarely reached.** `intentClassify` defaults to
   `on_no_loop` (`internal/copilot/copilot.go:1263-1271`). The loop runs only when the classifier
   fails, times out or its guided route declines (`copilot_intent.go:364-501`). **Proposal:** add a
   `knowledge` intent, with both lexical triggers and a classifier label.
7. **The existing budgets constrain the brief's numbers:**
   - A tool call has 20 s in total (`internal/mcp/toolerr.go:203`).
   - A result is clamped to 6000 runes of *marshalled JSON*, with an explicit truncation note
     (`internal/api/chat_tool_budget.go:19-66`).
   - The chat step preview is clipped at 4 KB (`chat_step_preview.go:30`).

   The brief's "10 s timeout + 2 retries" and "12 KB per page × N pages" do not fit. §3.3 and §3.6
   give measured, budget-aware versions.
8. **No retrieval eval exists anywhere** (no hit@k, recall or MRR code). The retrieval metrics and
   replay fixtures move *before* the tool work (Phase 2b), so ranking and expansion choices are
   measured rather than assumed.

## 1. Findings

### 1.1 CoSRE tool surface

| Question | Finding |
|---|---|
| Tool type | `mcp.Tool{Name, Description, ShortDescription, InputSchema, Handler, MinRole}`; `ToolHandler func(ctx, json.RawMessage) (any, error)` (`internal/mcp/mcp.go:248,309`) |
| Registration | An explicit ordered slice in `mcptools.ToolList(d Deps)` (`internal/mcptools/tools.go:265-393`). Dependencies are injected through `Deps` (`:182-229`), built for chat in `internal/api/mcp_deps.go:40-62` |
| Consumers | One catalogue serves the in-app chat (`copilot_chat.go:326`, role-filtered) and Coremetry's MCP server. The external MCP server is built with **reduced deps** (`main.go:1423-1427`: Store, LogStore, RuntimePods), and the construction-site guard test does not scan `main.go` (`internal/api/mcp_deps_test.go:50-62`) |
| Dispatch | `internal/ai/agent/tools/executor.go:98-148` runs, in order: repeated-call guard, `Scope.Constrain` (unrestricted), `ai.tool` span, 20 s budget, `json.Marshal` (default HTML escaping), `ToolErrorJSON` (six classes, `internal/mcp/toolerr.go:65-89`), `mcp.tool.call` audit. **This audit happens only on executor paths (loop and MCP wire). Guided routes call handlers directly and are not audited** |
| Feedback | JSON clamped to 6000 runes, with a truncation note telling the model to narrow (`chat_tool_budget.go:41-66`). The step preview in the UI is clipped at 4096 bytes and rendered as a table only when the JSON has exactly one array field (`frontend/src/components/ai/stepPreview.ts:48-65`) |
| Limits | 5 rounds per exchange. Calls per round are not enforced. **Calls run serially** (no parallel tool calls, `Executor.seen` is unsynchronised), so fan-out has to happen inside the handler |
| Small model | Surface group `intent` routes to the operator's small local model (`internal/copilot/profiles.go:538-541`). The classifier timeout is clamped to [5 s, 25 s] because a cold local model is slow (`copilot_intent.go:317-326`) |
| MCP client | `internal/mcpclient` bridges external MCP servers into the free loop only. An off-the-shelf ADO MCP server could be attached today, but it is weaker: loop-only, no citations, long descriptions sent in every turn, a dead server can stall about 15 s, and there is no server-side allowlist |

### 1.2 Azure DevOps and outbound HTTP

- **`internal/devops`:**
  - Settings blob `devops_connection` (`client.go:60-123`), whose rule is "one integration, one row".
  - HTTP Basic auth with the PAT (`:672`); non-JSON 200 responses (sign-in page) are rejected (`:686-693`).
  - Flavor versions: Server → 6.0, TFS → 4.1 (`:47-55`). Search is pinned to 7.0 (`codesearch.go:286`).
  - `SearchCode` posts collection-scoped and **never sets `filters`**, so it returns every project the PAT account can see (`codesearch.go:253-343`). Its errors are **not** passed through `sanitize` (`:321`), which is a defect.
- **Search extension:** often absent on-prem (`code.go:38-41`).
- **Not implemented anywhere:** wiki search, work-item search, wiki page content through `_apis/wiki`.
- **No Entra, service principal or managed identity code exists.** `golang.org/x/oauth2` is present, so client-credentials would add no new dependency. Zoom's token cache (`internal/notify/notify.go:1740-1936`) is a precedent, but it lacks `singleflight`.
- **Secrets:** plaintext JSON in `system_settings`. The tempo pattern (`HasToken`, optional `secretref` `env:`/`file:`, resolved off the request path, `internal/tempo/client.go:34-224`) is canonical, but devops does not support `secretref` yet.
- **Outbound HTTP:**

  | Concern | State today |
  |---|---|
  | Tracing | No transport-level (`otelhttp`) spans. LLM and tool calls are spanned by the caller (`ai.explain`, `ai.chat.turn`, `ai.tool`); `mcpclient` spans each call |
  | Retries | None. copilot has a 429 quota breaker but does not read `Retry-After` |
  | Proxy | devops, tempo and mcpclient build a bare `&http.Transport{}` and **ignore `HTTPS_PROXY`** |
  | Rate limiting | None |
  | Timeouts | Vary from 10 s to 180 s; devops uses 20 s, the same as the whole tool budget |

### 1.3 Identity

- Coremetry issues its own HS256 session JWT carrying `Claims{UserID, Email, Role}` (`internal/auth/auth.go:50-55,199-215`).
- **Upstream credentials are not kept:**
  - OIDC discards the IdP access and refresh tokens (`oidc.go:94-100`).
  - LDAP drops the password after the bind.
  - The trusted-header mode ignores `X-Forwarded-Access-Token`.
  - `cmk_` MCP tokens carry a role, not a person.
- `auth.FromContext(ctx)` works inside chat tool handlers, but no tool reads it (`internal/mcptools/team_ownership.go:13-29`).
- Background analyses run without a user, and the audit helper drops rows that have no claims (`internal/api/anomaly_extra.go:277-279`).
- **Conclusion:** service identity only for now (§3.10 covers user-mode options).

### 1.4 Incident analysis and evaluation

- **RCA verdict:** one structured call with no tools (`internal/api/rootcause.go:476-591`, `rca_verdict.go:197-255`).
  - The on-click cache is 10 min, keyed by hypothesis version.
  - **The synthesizer re-upserts hypotheses every 30 s for active anchors, and each insert stamps a new version** (`internal/anomaly/rootcause_worker.go:146,225,299`, `chstore/rootcause_hypothesis.go:156`). So during an incident the version-keyed cache rarely hits.
  - A leader-gated auto path covers deep-investigation anchors (P1 or deploy-correlated), deduplicated for 30 min (`rca_auto_verdict.go`).
- **Evidence catalog:** deterministic `E1..En` for found signals and `N1..Nn` for checked-and-not-found (`internal/rca/evidence.go:92-211`).
  - Extras are appended so existing E-IDs stay stable (`internal/rca/extras.go:40-44`).
  - Past signatures come last, as prior information (`rca_verdict.go:117-165`).
  - There is **no global rune budget** for the RCA prompt.
  - The RCA system prompt has **no `DataNotInstruction` framing**; only the chat, RAG and explain prompts carry it.
- **Output schema:** `rcaVerdictSchema` (`internal/api/copilot_schemas.go:148-202`).
  - Remediation is `{kind: mitigate|fix, action, target, risk}` with no source field.
  - All properties are required and `additionalProperties` is false.
  - `applyRCAShieldsPure` (`rca_verdict.go:305-382`) filters IDs, checks entities (the K3 free-text scan covers `remediation.action/target`) and caps confidence. **It does not validate sources.**
- **Persistence:** the persisted verdict stores **neither remediation nor evidence refs, causal chain or rejected hypotheses** (`internal/chstore/rca_verdict_store.go:40-75`, `frontend/src/lib/types.ts:1236-1263`).
- **Eval harness:**
  - `//go:build evalset`: local OpenAI-compatible model, temperature 0, 42 synthetic fixtures.
  - Deterministic rubric (`internal/ai/evalrubric`) and a run differ (`cmd/evalsetdiff`).
  - Single-shot replay only.
  - **No retrieval metrics** (gap G7 in `docs/audit/cosre-evaluation-program-2026-09-10.md:30,91`).

### 1.5 Existing knowledge subsystem (context only — RAG not used)

- **`search_knowledge` today:**
  - Args: `{query, source: all|runbook|document, limit ≤10}`.
  - Response: `{query, terms[], rows[], count, total, notes[], note}`.
  - Rows: `{source, title, snippet(500), href, id, score}`, merged across sources by raw score (`knowledge_tools.go:283-383`).
  - `source:"wiki"` is pinned as invalid (`knowledge_tools_test.go:82`).
  - The response has two top-level arrays (`terms`, `rows`), which is why the chat preview shows raw JSON today.
- **`get_runbook`:** resolves by runbook, problem, rule or service ID. It caps the description at 4000 runes and each step field at 600 runes; the step count is unbounded.
- **RAG:** `rag_chunks`, a crawler that fetches rendered HTML/text/PDF rather than the ADO wiki API, and chat tier 3 (`internal/api/rag.go:313-404`). With no documents, tier 3 runs one keyword query against an empty table per chat question, matches nothing and falls through.
- **The agent-loop prompt never mentions `search_knowledge`** (`internal/copilot/prompts.go:1628-1684`).

### 1.6 UI

- **Chat:**
  - Model-written links are never clickable.
  - The "Aç →" row opens `https?:` links with `noopener noreferrer` (`frontend/src/components/ai/ChatBubble.tsx:585-607`).
  - The RAG "Kaynak §N" chips have no scheme check (`:563-583`).
  - The typed `evidence` block (`internal/ai/agent/blocks/block.go`, `lib/chatEvidence.ts`, `components/ai/EvidenceCard.tsx`) is the pattern to reuse.
- **Explain/Insight markdown:** `^(https?:|mailto:|\/|#)` accepts plain http, mailto, root- or protocol-relative (`//host`) links and anchors, with **no host allowlist** (`frontend/src/components/Markdown.tsx:252-257`).
- **RCA panel:** evidence rows are `{id, kind:'E'|'N', text}` with no link (`RCAVerdictPanel.tsx:138-150`).
- **Settings:**
  - The DevOps tab is admin-only, audited, reloads config and shows the "kayıtlı" (stored) indicator for the PAT (`DevOpsTab.tsx`, `internal/api/devops_handlers.go:173-209`). Its PAT hint mentions only "Code (Read)".
  - `DevOpsSettingsInput` has type drift (`types.ts:1820-1833`).

### 1.7 Defects noticed in passing (not fixed here; each goes with the phase that touches the file)

| # | Defect | Where | Phase |
|---|---|---|---|
| 1 | Document rows link to `/settings?tab=rag`, which lands on `/settings/smtp` | `knowledge_tools.go:363` | 2c |
| 2 | `get_capabilities` says "search_knowledge planned" | `problem_tools.go:396-401` | 2c |
| 3 | `SearchCode` errors are not sanitised (PAT-leak class) | `codesearch.go:321` | 1 |
| 4 | Stale "nothing consumes this package" comments | `devops/client.go:9`, `main.go:1233` | 1 |
| 5 | `DevOpsSettingsInput` type drift | `types.ts:1820-1833` | 1 |
| 6 | RAG source chip has no scheme check and no `noreferrer` | `ChatBubble.tsx:571` | 3b |

## 2. Where the brief meets the codebase

| Brief | Codebase reality | Proposal |
|---|---|---|
| New tool `search_knowledge` | Name taken by a live tool | Extend it; contract changes listed explicitly (§3.1) |
| `internal/integrations/azdo` | `internal/devops` already speaks to ADO | Add wiki, work-item and page methods to `internal/devops` |
| `almsearch.dev.azure.com`, 7.1 | On-prem Server; 7.1 rejected live | Collection-scoped URLs with a project filter; api-version ladder per endpoint family |
| Entra SP / managed identity preferred | Not usable against ADO Server | PAT of a dedicated service account (optional `patRef`). Entra SP only if an ADO Services org appears |
| 10 s timeout, 2 retries | 20 s total tool budget, serial calls | Internal soft deadline 17.5 s, per-attempt deadlines, one retry only if it fits (§3.3) |
| Full content 12 KB × top N | 6000-rune clamp on marshalled JSON | Search returns 5 short rows; `get_knowledge_page` returns ≤4000 runes of body, fitted to 5600 runes marshalled (§3.6) |
| Query expansion with a small model | A cold local model needs 5–25 s; the guided path already makes one classifier call | Keywords come from the existing classifier call. A separate expansion call is a switch that stays off unless the eval shows a lift (D12) |
| Per-incident budget of 3 searches | No per-incident agent | Chat: 3 ADO searches per exchange. RCA: a separate prefetch cache and circuit breaker (§3.7) |
| CoSRE calls the tool in incident analysis | RCA is single-shot by operator decision | Deterministic prefetch with K citations (D3) |
| Search as the user | No delegated token; ADO Server rejects Entra tokens | Service mode now; on-prem user-mode options listed for later (D9) |
| `-tags eval` | The harness uses `-tags evalset` | Reuse `evalset` |

## 3. Proposed design

### 3.1 Tool contract

```
search_knowledge(
  query   string,                                                   // required
  source  "all"|"runbook"|"document"|"wiki"|"code"|"workitem",      // today: all|runbook|document
  service string?,                                                  // new: boosts rows naming the service
  project enum?,                                                    // new: exposed ONLY when the allowlist has 2–10 entries
  limit   int?                                                      // default 5, schema max stays 10
)
→ { query, termsText, rows: [ {source, title, snippet, href, id, score, rank, project?, lastUpdated?} ],
    count, total, partial, status: {<backend>: "ok|disabled|extension_missing|project_not_found|auth|
    throttled|timeout|indexing|bad_query|budget_exhausted"}, note }
```

- **Breaking for external MCP clients (goes in the release note):**
  - `terms[]` becomes `termsText` (a string).
  - `notes[]` becomes `status{}` plus `note`.

  Reason: `rows` must be the only top-level array for the chat table preview.
- **Unchanged:** `total`, `score`, limit max 10.
  - ADO rows carry `score = 1/(1+rank)`.
  - More than 8 ADO rows are clamped internally.
- **`id` is a short server handle** (`k:<8 hex>`). It maps, through the search cache, to a row a previous search actually returned (TTL 30 min). Long GUID-and-path IDs are fragile when a small model copies them.
- **`href`** is the ADO web URL, validated against the configured web origin.
- **New reader `get_knowledge_page(id)`:**
  - A wiki page returns cleaned text plus a heading table of contents.
  - A work item returns title, description or repro steps and the last 5 comments.
- **Per-backend failures are `status` values inside a successful result, never tool errors.** The tool returns an error only when every requested backend failed. That error maps onto the existing six classes:
  - auth, disabled, extension_missing, project_not_found → `backend_unavailable`, hint "do not retry".
  - timeout, throttled → `timeout`.
- **`source:"all"` includes ADO kinds only when they are enabled.** `code` rows appear only on an explicit `source:"code"` (they are often bare paths).

### 3.2 Backend interface

```go
type KnowledgeBackend interface {
    Name() string // "wiki" | "code" | "workitem" | later "vector"
    Search(ctx context.Context, q KnowledgeQuery) (KnowledgeResult, error)
}
```

- `knowledgeBackends(d Deps)` **always** builds the local `runbook` (and inert `document`) backends from `d.Store`. `Deps.Knowledge` holds only the *additional* backends. So a nil value means "local only, as today", and the external MCP server keeps its current behaviour.
- The ADO backends are adapters in `internal/api` around `s.devops`, following the `mcpClusterMetrics` pattern. mcptools never imports api or devops.
- **D10:** the external MCP server stays local-only until a per-`cmk_`-token rate limiter exists. When it is enabled, `main.go` builds Deps through the same constructor, and the guard test is extended to scan `main.go`.

### 3.3 ADO client additions (`internal/devops`)

- **Methods:**
  - `SearchWiki`, `SearchWorkItems`, `searchCodeKnowledge`. `searchCodeKnowledge` is new and sets filters and `$top`; the existing `SearchCode`/`FetchCode` path is left untouched.
  - `ListWikis(project)` (cached 1 h).
  - `GetWikiPage(project, wikiID, pagePath, version)`.
  - `GetWorkItem(id)`, with field selection.
- **Scoping on every request:**
  - Collection-scoped endpoint plus a server-side project filter equal to (requested ∩ allowlist): `filters.Project` for wiki and code, `filters["System.TeamProject"]` for work items.
  - A post-filter on `project.name` (case-insensitive) as defence in depth.
  - Allowlist entries may be `project` or `project/wikiName`.
- **Query text:** a pure, table-tested `adoQueryText(terms)`:
  - drops stopwords;
  - OR-joins at most 5 terms (ADO search ANDs space-separated terms);
  - quotes tokens that contain punctuation (`ORA-00001`, `HTTP 503`);
  - strips `<word>:` operators (`proj:`, `repo:`, `path:`, `a:`, `s:`, `t:` …) and leading wildcards;
  - adds a trailing-wildcard stem for Turkish terms of 5 or more runes (ADO does no Turkish stemming; verify on the target).
  - A shared `foldTR` (İ/I/ı/i → i) is used for terms, the service boost and dedupe.
- **api-version:** negotiated once per endpoint family and stored like `detVersion`.
  - Ladders: search `[7.0, 6.0-preview.1]`, wiki and work items `[7.0, 6.0]`.
  - A 400 saying "latest supported is <v>" jumps straight to that version.
  - With flavor=tfs the knowledge kinds report `disabled`.
- **Wiki paths:** a pure `wikiPagePathFromGitPath(gitPath, wikiType, mappedPath)`.
  - Project wikis: strip `.md`, turn `-` into a space, percent-decode.
  - Code wikis: strip `mappedPath` and pass the branch as `versionDescriptor`.
  - Web URL: the Pages API `remoteUrl` when available, otherwise `<web-base>/<project>/_wiki/wikis/<wiki>?pagePath=…`.
  - An optional `webBaseURL` setting covers installs whose browser URL differs from the API URL.
- **Error classification (status *and* body):**
  - A 404 naming a project (`TF200016`, `VS800075`, `ProjectDoesNotExist…`) → `project_not_found`, not cached.
  - Any other 404 → one collection-level probe; a 404 there → `extension_missing`.
  - A 200 with `infoCode` ≠ 0 → `indexing` (1, 2, 6, 7, 9) or `bad_query` (3, 4, 19, 20), never cached.
  - 401/403 → `auth`, with a per-kind scope hint.
  - Every error string is passed through `sanitize`.
- **Deadlines (the 20 s tool budget):**
  - The handler works to a soft deadline of 17.5 s, fuses whatever has arrived and returns `partial:true`. It never lets the executor's deadline fire.
  - One collection-scoped request per kind per query variant; there are no per-project requests.
  - At most 2 variants. The original keyword form starts at t = 0.
  - Per-attempt timeouts: search 6 s, page 5 s.
  - At most one retry, on 429/502/503/504, only when `Retry-After` (or 500 ms backoff) fits the remaining time. Otherwise the status is `throttled`.
  - Time spent waiting on the semaphore counts against the deadline.
- **Transport:** reuse the devops transport (the same TLS and proxy behaviour). A new setting `proxy: direct|env` (default `direct`, today's working behaviour) applies to **all** devops clients together, so code fetch and knowledge search always take the same network path. Internal ADO hosts often sit outside `NO_PROXY`.
- **Caching and limits (distributed-mode safe):**
  - The Redis-backed `cache.Cache`: searches 5 min, pages 15 min.
  - Key: `knowledge:v1:%x` = fnv over sorted inputs (kind, normalised query, project set, `$top`, apiVersion, sha256(PAT)[:8], base URL, collection).
  - Negative cache in Redis for 5 min, bypassed by the test button.
  - Concurrency: 4 per pod, plus a global Redis token bucket (20 req/s).
- **Observability:**
  - A child span `devops.knowledge.search`/`.page` with kind, status, hits, bytes and apiVersion. **No query text.**
  - Outcome counters on `/admin/stats`.
- **Tests:** `httptest` with recorded, **synthetic** fixtures for each on-prem shape: 404 with no extension, 404 project, 401, 429 with `Retry-After`, `infoCode` indexing, project-wiki and code-wiki paths. No live calls in CI.

### 3.4 Query keywords and expansion

- **Guided path:** the `knowledge` intent adds a `keywords: string[≤3]` slot to the **existing** classifier JSON. There is no extra LLM call.
- **Free loop:** the model's own `query`, plus the lexical `knowledgeTerms` form.
- **RCA:** a query built in code (§3.9).
- **Separate expansion call:** `Deps.ExpandQuery` stays as a seam behind `knowledgeExpand` (default **off**). It is enabled only if the Phase 2b eval shows a hit@5 lift of at least 5 points. When on, it runs in parallel with the original search, with timeout = min(classifier timeout, remaining − 9 s). **This deviates from the brief** (D12).

### 3.5 Merge and rank

- Weighted RRF (k = 60): wiki 1.0, runbook 1.0, workitem 0.6, code 0.5. Local rows enter the fusion only with a lexical score ≥ 0.4.
- Deterministic tie-break: (backend priority, lastUpdated desc, title). This keeps K IDs and cached answers stable.
- `service` boost uses `foldTR` token equality (split on `-_./ `).
- Dedupe on the normalised page path or the page id.

### 3.6 Content and size (budgeted against marshalled JSON)

- **Search:**
  - 5 rows by default. Snippets ≤220 runes, with `<highlighthit>` tags stripped.
  - No `path` field, because it duplicates href and id.
  - A pure `fitKnowledgeResult(result, 5600)` drops the lowest-ranked rows, then shortens snippets, until the *marshalled* size fits, and sets `partial` with a note.
- **Page:** body ≤4000 runes, TOC ≤600 runes, fitted to 5600 runes with the same function.
- Both tools marshal with `SetEscapeHTML(false)`, so `<`, `>` and `&` stay one rune.
- A size test pins the result against `chatToolResultMaxRunes`.
- **Wiki cleanup (pure, tested):**
  - drop `[[_TOC_]]` and `[[_TOSP_]]`;
  - turn `::: mermaid` blocks and HTML into text;
  - keep code fences;
  - make relative wiki links absolute.
- **Retrieved text is framed as data** (`DataNotInstruction`).

### 3.7 Budgets

- **Chat:** at most 3 ADO searches per exchange (`ExchangeID`), setting `knowledgeSearchesPerExchange`. After that the result is a normal one with empty rows and `status: budget_exhausted`. The repeated-call guard still applies.
- **RCA prefetch** (§3.9):
  - Its own cache (15 min), keyed by the derived query, kinds and settings digest, **not** by hypothesis version.
  - A circuit breaker opens after 3 consecutive timeouts or 5xx responses within 2 min and skips ADO for 5 min.
  - Gated by the same `AutoExplainEnabled` and `QuotaBackoff` checks as other background AI.
  - At most 30 background ADO requests per minute (Redis token bucket).

### 3.8 Chat reachability

`knowledge` is added in two places:

1. **The deterministic tier-1 router** gets lexical triggers: runbook, wiki, prosedür, "bilinen hata", "daha önce … olmuş mu", "geçmiş olay". This works with `intentClassify=off` and costs no LLM call.
2. **The classifier** gets the label, with 6 contrastive few-shot examples against `how_to` and `off_topic`. A server veto downgrades `knowledge` to `how_to` when the question names a Coremetry page or feature.

Guidance is also added to the agent-loop prompt. The intent evalset gains 15 knowledge and 15 contrast fixtures, and **the release is blocked if accuracy on existing intents drops.**

### 3.9 Incident analysis (keeps the no-tool-loop decision)

- **Prefetch:** a soft-fail gatherer in `gatherRCACatalogExtras` (`internal/api/rca_extras.go:38`), with a 5 s timeout (below BubbleUp's 8 s). It is behind a new setting `knowledgeInRCA` (default off).
- **Queries:** at most 2 narrow queries built in code.
  - Q1, service identity: service name OR metadata display name OR repo-catalog repo name.
  - Q2, error signature: error code or exception class alone.
  - Both are filtered to the owning project from the repo catalog when it is known, otherwise to the allowlist.
  - In RCA, K entries come from **wiki only** (never work items or code). At most 3 entries, each a title plus a ≤220-rune snippet and `lastUpdated`.
- **K IDs `K1..Kn`:**
  - Never accepted as `root_cause` or `causal_chain` evidence, same as `N`.
  - Never raise confidence.
  - Never widen the entity whitelist.
  - The prompt appends `DataNotInstruction` and wraps K entries in delimiters. This is a prompt version bump with a pinned test.
- **Schema:** `remediation[].sources` (an array of `enumProp(K IDs)`) is added **only when the K catalog is non-empty**. With no K entries, the schema and prompt are byte-identical to today (pinned by a test). `kind` stays `mitigate|fix`.
- **Shield rules:**
  - A remediation whose `action` references a procedure (pure `mentionsProcedure`: runbook, wiki, prosedür, adım …) but has no valid K gets the note `uncited`.
  - A remediation supported **only** by K gets `risk` of at least `medium`.
  - For items with valid sources, the K3 name scan also accepts tokens from the cited snippets (reported as `k_sourced`, not unknown).
  - The server resolves K to `{title, href}`.
- **Persistence:** one new column, `refs_json` (evidence refs, remediation and sources). It fixes the existing E-ref and remediation gaps as well. It goes through the `/clickhouse-schema` gate, the two-boot contract, and a mandatory parameter on `rcaVerdictRecordOf` with a round-trip test.
- **Panel:** K-cited remediation is labelled "wiki'den — doğrulanmamış prosedür" ("from the wiki — unverified procedure") and shows the page's last-updated time.

### 3.10 Identity and access

- **Service mode (ship):**
  - A **dedicated, non-human service account**, with Reader access only on the allowlisted projects. **This account permission is the real boundary**; PATs cannot be limited to projects.
  - PAT scopes: Code, Wiki and Work Items (Read), only for the kinds that are enabled.
  - The Coremetry allowlist is a second, query-side filter, applied to search **and** to every `get_knowledge_page` read:
    - handles must come from a real earlier search;
    - a wiki must belong to an allowlisted project;
    - a work item is fetched with `System.TeamProject` and refused as `not_found` when it is outside the allowlist.
  - `code` and `workitem` are off by default. Work-item rows carry only id, title, type, state, changed date and highlights (field selection).
  - Settings show the PAT expiry date, and the test button warns when it is less than 30 days away.
- **Audit:** once, inside the ADO adapter, so every path is covered: guided, loop, MCP wire and RCA.
  - Action `devops.knowledge.search`/`.page`.
  - Details: `{mode:"service", transport, kinds, projects, queryPreview(≤256), returnedIds, cache, outcome}`.
  - Background calls use actor `system`.
- **User mode (deferred, D9):**
  - OIDC/Entra delegation works only against ADO Services.
  - On-prem options: (a) an encrypted per-user PAT vault, where results are trimmed to each user's own ADO permissions; (b) Kerberos constrained delegation from a domain-joined service account.
  - Both need a new secret store and a security review.

### 3.11 UI

- **Chat:**
  - A typed `knowledge` block built from the validated tool result (EvidenceCard pattern). Pure guard in `frontend/src/lib/chatKnowledge.ts`; component `KnowledgeCitations.tsx`.
  - Links pass a shared `safeExternalHref(href, allowedOrigins)` check and open with `target="_blank" rel="noopener noreferrer"`.
  - Titles are shown (D7).
  - Saved chats keep text only, so citations disappear on reload. The answer text names titles in plain text.
- **RCA panel:** remediation `sources` are shown as links with the "unverified procedure" label (§3.9).
- **Settings, DevOps tab, section "Bilgi araması" (knowledge search):**
  - Enable toggle, kinds, allowlist (`project` or `project/wiki`), limits, `patRef`, `webBaseURL`, `proxy`, and `knowledgeInRCA`.
  - An admin-only, audited **test** button: `POST /api/devops/knowledge/test` → `{ok, kind, apiVersion, infoCode, hits, collection, networkPath, sampleFields}`. It bypasses the negative cache.
  - The PAT scope hint is updated. Audit details list every new non-secret field.

### 3.12 Evaluation

- **Pure metrics** (hit@k, recall@k, MRR) with table tests in the untagged suite.
- **Fixtures:** `internal/copilot/evalset/knowledge/*.json`, 15–20 synthetic cases (incident → expected page), including at least 5 Turkish inflection and İ/ı cases.
- **`TestCoSREKnowledgeEval`** behind `evalset`, in two modes:
  - **replay (default):** recorded synthetic ADO responses feed the real query-building, fusion and rank code.
  - **live (`COREMETRY_EVAL_AZDO=1`):** runs against the operator's wiki using a local fixture file that is **not committed**. Only aggregate hit@1/hit@5 goes into `docs/cosre/search-knowledge-eval.md`.
- Run artefacts plug into `cmd/evalsetdiff`.

## 4. Decisions needed

| # | Decision | Recommendation |
|---|---|---|
| D1 | Extend the existing `search_knowledge` (with the listed small breaking changes) or add a new tool name | **Extend** |
| D2 | Target and auth | **On-prem ADO Server + PAT of a dedicated service account.** Entra SP only if an ADO Services org appears |
| D3 | Incident analysis: deterministic prefetch with K citations, or open a tool loop in RCA | **Prefetch**, behind `knowledgeInRCA` (default off) |
| D4 | Chat reachability | **`knowledge` intent** (lexical triggers + classifier label), gated on the intent eval |
| D5 | Access | **Service-account permissions as the boundary**, plus a required allowlist; `code` and `workitem` off |
| D6 | Sizes | **5 rows, ≤220-rune snippets, pages ≤4000 runes**, all fitted to the marshalled budget |
| D7 | Citation titles | **Show** them for ADO citations (an exception to the v0.9.515 "hide document names" rule for RAG chips) |
| D8 | ~~Crawler overlap~~ | **Resolved:** RAG not used |
| D9 | User identity mode | **Defer.** On-prem options: PAT vault or Kerberos delegation |
| D10 | External MCP clients | **Local-only** until a per-token rate limiter exists |
| D11 | Collections | **Single collection** (the configured one); the test result says which |
| D12 | Separate LLM query expansion (the brief asks for it) | **Off by default.** Keywords come from the existing classifier call; the switch is turned on only if the eval shows a ≥5-point hit@5 lift |

## 5. Phase plan (each phase is its own release; commits stay small)

1. **Phase 1: client, settings and live probe**
   - Client methods, api-version ladder, error classes, query builder, wiki path helper, cache, limits, spans, counters.
   - Settings section and the audited test endpoint. The test also saves a sanitised response-shape sample, so the real target's fields are checked against the fixtures **before** any tool work.
   - Fix defects 3–5.
   - At the end of this phase the operator can see from the UI whether the Search extension, the api-version and the indexing work on the real server.
2. **Phase 2a: refactor.** Move the local backends behind `KnowledgeBackend` with golden tests and byte-identical output.
3. **Phase 2b: retrieval eval.** Metrics, replay fixtures and `TestCoSREKnowledgeEval` (replay mode), so fusion and query building are measured before shipping.
4. **Phase 2c: the tool.**
   - ADO backends, contract v2, `get_knowledge_page` with access checks, fitting, per-exchange budget, adapter audit.
   - The `knowledge` intent, with the intent-eval gate.
   - Fix defects 1–2.
5. **Phase 3a: RCA prefetch** (behind `knowledgeInRCA`): K IDs, conditional schema, shield, `DataNotInstruction`, `refs_json` migration.
6. **Phase 3b: chat citation block,** RCA panel links and the safe-href helper (defect 6).
7. **Phase 4: live eval run** and `docs/cosre/search-knowledge-eval.md`. This decides D12.

## 6. Risks

- **Search extension absent or still indexing on the target collection.** Phase 1's test button shows this (`extension_missing` / `indexing`) before any tool work ships.
- **Recall on real wiki pages.** Wikis name applications by team or display names, not by Kubernetes service names. The repo catalog and metadata mapping (§3.9) and OR-joined queries address this, and the live eval measures it.
- **Prompt injection from wiki or work-item text.** Anyone who can edit a wiki page can plant an action. Mitigations:
  - retrieved text is framed as data;
  - in RCA, K comes from wiki only;
  - K-cited remediation is labelled unverified;
  - a remediation supported only by K has risk of at least medium;
  - the server resolves URLs;
  - K is never evidence.
- **Data exposure through the service account.** The account's permissions are the boundary. The allowlist, the off-by-default kinds and the adapter audit reduce, but do not replace, that boundary. The PAT also expires; its expiry is shown in settings and warned about.
- **Small-model intent confusion** (`knowledge` versus `how_to`). This is gated by the intent evalset before release.
