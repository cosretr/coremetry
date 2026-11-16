# Server-generated text in the EN UI — design

Status: design only (no code). Baseline: v0.10.2294. Scope: Anomaly / Problem /
Exception page family. Frontend-owned text there is already bilingual
(v0.10.2282–2292); what is left in Turkish is text the server builds.

Counts below are grep counts of Turkish string literals on non-comment lines
(approximate; a template with three fragments counts three times).

## 1. Inventory

| # | Text (wire field) | Built in | Kind | Persisted? | Other consumers | ~Lits |
|---|---|---|---|---|---|---|
| 1 | Problem standard: `std.headline`, `impact.primary.text`, `evidence.sentence`, `nextStep.text`, `context[]` | `chstore/problem_standard.go` (`problemHeadline`, `problemImpact`, `problemEvidenceSentence`, `problemNextStep`, `stdContext`, `Standardize{Problem,Exception,AnomalyEvent,Incident}`) | Go template + params; pure | **No** — computed per read (`api/problem_standard.go attachInboxStandards`, cached in inbox / std keys) | Notifications (`notify/tr_template.go`, `enforce.go`, `policy_shadow.go`, digest items), notify-thread `Topic` (headline at open, **persisted**, keeps the e-mail subject stable), policy replay report | 100 |
| 2 | Gate `name` / `note` (G1–G7) | `chstore/signal_gates.go` | 7 fixed names + templated notes; stable `id` on the wire | No | Notification "güven" line | 15 |
| 3 | `priorityReason` | `chstore/problem.go computePriority` (mostly English already), `api/inbox.go exceptionPriorityAt` / `anomalyPriority`, `inbox_keep_priority.go`, `inbox_incident_fold.go`, `notify/exception_oracle_p1.go` | Template + params (~23 return sites) | No (read-time; "screen P1 = e-mail P1" by design) | Notifications (title + body), MCP `InboxReason`, guided copilot blocks, insight prompt | 20 |
| 4 | Problem title (`ruleName`) and `description` | Detectors in `anomaly/`, `evaluator/`, `ml/group_live.go`; alert rules | Built-in: fixed/template. Alert rules: **operator-authored** | **Yes** — `problems.rule_name`, `problems.description` | Notifications, MCP rows, inbox `q=` search (matches title), **classification code** (see flags) | ~60 |
| 5 | Problem `aiSummary`, RCA verdict prose, investigation `summary.text` | `anomaly/problem_explainer.go`, `api/rca_verdict.go`, `sre/narrate.go` | **LLM output** (Turkish prompts) | Yes — problems row / hypothesis row / investigation blob | Chat, MCP investigation summary, eval sets | prompts only |
| 6 | Investigation: step `summary`, finding `reasons`, `warnings`, gate `reason` | `sre/steps.go`, `orchestrator.go`, `deps.go`, `readers_*.go`; many summaries are the MCP tool's own message passed through | Template + params; partly tool output | **Yes** — JSON blob (`saved_views.query_string`, investigation row) | Narration prompt, MCP `investigation_summary`, export | ~80 |
| 7 | Hypothesis `label`, `category`, check `reason` | `sre/hypotheses.go` (`hypothesisTable`, `chk*`) | 8 fixed labels (stable `kind`), ~30 checks (stable `name`), reasons = free templates (55 `Sprintf`) | Yes — same blob | Narration prompt (`narrate.go`), MCP summary (`summary.go`) | 117 |
| 8 | Recommendation `text` / `rationale`, runbook match reason | `sre/recommend.go` | Template; stable `id` (`rollback`, `hyp-<kind>`, runbook id) | Yes — blob; ack/dismiss carried by `id` across re-runs | MCP `InvestigationRecommendation` | 33 |
| 9 | Timeline `label` ("what happened when") | `sre/timeline.go` | Template; stable `kind` | Yes — blob | — | 8 |
| 10 | ML card: `summary`, `what`, `howMuch`, `since`, `impact`, `whyItMatters`, `whatChanged[]`, `whereToLook[]`, `header.status`, play sentences | `ml/explain_card.go`, `explain_text.go`, `explain_links.go`, `explain_play.go`, `explain_course.go` | Template + params with Turkish suffix harmony (`trNumWithSfx`) | No — `BuildExplainCard` / `ExplainSummary` per read. **But** the summary is copied into the promoted Problem's `description` and becomes its headline / notification subject | MCP v2 ML tools ("same sentence as list / detail / CoSRE"), FE `mlValueText.ts` rewrites a fragment | ~170 |
| 11 | ML evidence record: `title`, `volumeReason`, confirmer `summary` | `ml/*_record.go` (15 files) | Template + params | **Yes** — `ml_events.record` JSON (contract: later slices add no fields) | Record-based card (already catalog-rendered when `localized`, v0.10.2248) | 73 |
| 12 | Anomaly-event names in problem-event rows; older P1 investigation step text; exception pod signal sentences | `api/inbox.go anomalyToInbox` (`kind · pattern`), `anomaly/investigation.go`, `anomaly/exception_pods_signal.go`, `anomaly/external.go` | Template + raw ids | Mixed: event fields persisted, sentences read-time | RCA prompt | ~60 |
| 13 | Notification log `subject` / `bodyPreview` / `error`; "no channel matched" row | `notify/*`, `chstore/notification_log.go` | Verbatim copy of what was sent + transport error; no-match is a sentinel row (`none` / `unmatched`) | **Yes** — `notification_log` | /events, channel health | ~40 |
| 14 | Queue / enqueue `reason` | `sre/queue.go` | 3 fixed strings next to a stable `state` | No | — | 3 |

Shape of the problem: roughly **55 % is pure read-time template text** (rows 1–3,
10, 14), **35 % is persisted template text** (4, 6–9, 11–13), the rest is LLM or
operator prose (5, part of 4).

### Turkish text used as an identifier or compared in code

These must never be translated at the source, whatever option is chosen.

- `chstore/problem_standard.go:45–49, 217` — `REDEvidenceVM/Span/VMHold` markers
  are written into `problems.description` and parsed back by `ParseEvidenceSource`.
- `problem_standard.go:276` `HasPrefix(p.RuleName, "HTTP hatası · ")`; `:299`
  `HasPrefix(p.Description, "Teknik hata (5xx)")`; `:313`
  `Contains(p.RuleName, "trafik çöküşü")` — problem kind is classified from
  persisted Turkish text.
- `MLPromotedHeadline` — a promoted ML Problem's `description` **is** its headline;
  `exceptionGroupLabel` cuts `ruleName` at `" · "`; `"Monitor: "` / `"Incident · "`
  prefixes are trimmed.
- `sre/timeline.go:59` `src != "problem başlangıcı"`; `ml/explain_card.go:558`
  `HasSuffix(plain, "gün")`.
- `sre/deps.go:211` etc. — `"veri yok: "` summary prefix convention (the real flag
  is `Step.NoData`, keep it that way).
- Outside the page family but same pattern: `mcptools/service_healthcheck.go:423`,
  `cosre/uistream/errors.go:36–40` (error class from Turkish substrings),
  `chstore/sysstats_disk_history.go:70–72`.
- Frontend: `components/coremetry/anomalies/mlValueText.ts` re-implements
  `trInt`/`trDec`/dative suffixing to patch a server sentence;
  `pages/pod/podSentence.ts` compares Turkish status words.
- Notify thread `Topic` = headline at open; a different-language headline would
  change the e-mail subject and split the conversation.

## 2. Options

Common facts: `useLang()` is client-only (localStorage → branding default → EN);
no request carries a language today. Inbox is a hot, `serveCached` endpoint
(`inbox:v12:…`); the CoSRE inbox reader deliberately reuses the service strip's
query so counts match. `i18n.ts` is 313 KB raw / ~97 KB gz as source, ~4,860
entries → roughly 20 B gz per entry.

### A. Server renders by request language

`?lang=` (or header) on every affected route; every pure builder gains a `lang`
argument; Go carries EN templates.

- Cost: ~600 literals get an EN twin in Go, plus EN versions of the grammar
  helpers. `Standardize*` alone has ~25 call sites (notify, evaluator, replay,
  MCP) that must all pin `tr`.
- Schema: none. Persisted rows (4, 6–9, 11, 13) stay Turkish, so EN pages are
  still mixed — A only fixes rows 1–3, 10, 14.
- Cache: `lang` must enter every key (`inboxListKey`, `problemStandardKey`, ML
  list/detail, investigation). Hot-key population doubles; the CoSRE reader must
  pin `tr` or it stops sharing the strip's entry.
- MCP / prompts / notifications: unchanged only if nobody ever forwards `lang`.
  One leak into a write path changes a persisted description, a thread topic or a
  classification marker (flags above). This is the main risk.
- Bundle: 0. Tests: every text table doubles; prompt goldens untouched.

### B. Structured code + parameters, frontend renders

Server adds `{k, a}` (message key + raw args) next to each text; text stays.

- Cost: one small Go type; builders refactored so the switch yields the message
  and the Turkish string is rendered from it (single source). FE gets EN catalog
  entries and number/unit formatting per language.
- Schema: none. Read-time text (55 %) gets codes for **all** rows, old ones
  included. Blob text (6–9) gets codes only for investigations written after the
  release; whole-row replace is satisfied because the blob is rewritten whole, but
  the ack carry-forward in `recommend.go` must copy the new fields.
  Row 11 conflicts with the record contract ("no new fields") — do not extend it.
- Cache: no language input → keys unchanged. Payload is additive; bump the inbox
  key version once so mixed-version pods do not alternate shapes.
- MCP / prompts / notifications: untouched — they keep reading the text field.
- Bundle: EN templates only if the TR UI keeps the server string (see §3):
  ~130 entries for rows 1–3 ≈ 3 KB gz; full hypothesis reasons would add ~100
  entries ≈ 2–3 KB gz; the ML card grammar is the expensive part (not estimated).
- Tests: characterization golden first (freeze today's Turkish for a fixture
  matrix), then refactor; FE vitest per key. Doing B for *everything* means ~400
  keys and re-expressing the ML sentence grammar in two languages — high churn.

### C. Hybrid (recommended)

B for read-time template text and for closed codes already on the wire; persisted
free-form templates, LLM prose and operator text stay as generated and are shown
with a visible original-language treatment.

- Cost: B's cost for rows 1–3 and 14 only; FE-only lookups for 2, 7 (labels and
  check titles), 9, 13 (no-match), 14.
- Schema / cache / MCP / notifications: as B (none / unchanged / untouched).
- Bundle: ≈ 4–6 KB gz total, inside the ~44 KB headroom. Tests: one golden
  matrix for `problem_standard`, vitest tables; no prompt or notification golden
  changes.
- Risk: EN pages remain partly Turkish by design; the marker makes that read as
  intentional rather than as a bug.

### D. Already in the code: record-based rendering

The anomaly card already renders from a structured record through the catalog
when `localized` (v0.10.2247/2248), and keeps the old Turkish card as fallback
(`L(key, fallback)` in `MLReplaySheet.tsx`, v0.10.2291). That is option B applied
to one surface. C generalises exactly this convention instead of inventing one,
and it is the way to retire `mlValueText.ts` (compose from record numbers rather
than patching a sentence).

## 3. Recommendation

**Option C, using D's convention.**

1. Text is never the contract. A message key + raw args travels beside it; the
   existing string fields keep their exact bytes.
2. **TR UI renders the server string verbatim; EN UI renders from key + args and
   falls back to the server string** when the key is absent or unknown. This makes
   "Turkish stays byte-identical" true by construction, needs no TR template
   entries, and old rows degrade gracefully.
3. No language reaches the server: no cache-key change, no risk of a language
   leaking into persisted descriptions, thread topics or classification markers.
4. Read-time text (headline, impact, evidence, next step, context, gate notes,
   priority reason) is fixed for every row immediately — it is the most visible
   text (every inbox row and detail header).
5. Do not parametrise the ~100 hypothesis-check sentences or the ML card grammar
   now: high churn, shared verbatim with prompts and MCP tools.

**Stays Turkish, explicitly:**

- Notifications (all channels, digest, thread topic) — existing decision D9
  ("notifications are entirely Turkish"); a per-channel language would be a new
  setting. Any change here needs explicit operator approval; none is proposed.
- MCP tool output and CoSRE prompts — Turkish-first chat is an existing decision
  (2026-10-09); parity tests pin tool text to list text.
- LLM prose (AI summary, RCA verdict, investigation narration) — local gateway
  only; a translate-per-view call is not negligible (one generation per viewer
  language per item, plus a cache keyed on language). Shown as generated.
- Operator-authored text (alert rule names, descriptions, runbook titles, notes).
- Persisted rule names / descriptions / evidence markers — they are identifiers.
- Notification log subject / preview / error — an audit copy of what was sent.
- Investigations written before the slice that adds codes — no backfill.

**Original-language treatment (EN UI only):** one small wrapper component that
sets `lang="tr"` on the element and adds a muted "TR" tag with a tooltip
("generated in Turkish"). No toggle, no setting.

## 4. Slices

Every slice: Turkish UI, notifications, MCP output and prompts byte-identical;
existing Go text tests must pass unmodified.

**S1 — closed codes already on the wire (frontend only).**
Gate names by `id`, hypothesis label/category by `kind`, hypothesis check title by
`name`, timeline kind, queue reason by `state`, notification no-match row by its
sentinel; add the original-language wrapper and apply it to LLM prose and
persisted reasons on these pages.
Files: `frontend/src/lib/i18n.ts`, `lib/problemStandardView.ts`,
`features/anomalies/hypothesisView.ts`, `InvestigationResult.tsx`,
`ProblemInvestigationRecommendations.tsx`, one new wrapper component.
Tests: vitest tables (every Go code has an EN entry; unknown code → server text).
Accept: EN UI shows English gate / hypothesis names; TR screenshots unchanged;
`bundle:check` passes.

**S2 — problem standard message keys.**
New `chstore` message type; `Standardize*` emit keys for headline, impact, evidence,
next step, context and gate notes; EN catalog + per-language number formatting.
Files: `internal/chstore/problem_standard.go`, `signal_gates.go`, one new small
file for the type, `frontend/src/lib/types.ts`, `problemStandardView.ts`,
`ProblemStandardHeader.tsx`, `pages/inbox/InboxCells.tsx`, `i18n.ts`;
inbox key version bump in `internal/api/inbox.go` (one token).
Tests: (a) golden matrix of today's Turkish output written **before** the refactor,
asserted byte-equal after; (b) every emitted key exists in the EN catalog
(Go emits the key list to a fixture, vitest reads it); (c) `notify/tr_template_test.go`
untouched and green.
Accept: EN inbox rows and detail header fully English for built-in kinds; ML-promoted
and incident headlines (free text) fall back with the TR marker.

**S3 — priority reason + built-in problem titles.**
Reason builders return key + args beside the string; FE derives a display title for
built-in rules from `std.kind` (persisted `ruleName` untouched; search still matches
it).
Files: `internal/chstore/problem.go`, `incident.go`, `internal/api/inbox.go`
(reason functions only), `inbox_keep_priority.go`, `inbox_incident_fold.go`, FE
`inboxColumns.ts`, `InboxCells.tsx`, `ProblemDetail.tsx`, `i18n.ts`.
Tests: table test per return site (string unchanged, key present); vitest.
Accept: priority tooltip English in EN UI; notification "Öncelik" line unchanged.

**S4 — investigation blobs written from now on.**
Recommendations (`rollback`, `hyp-<kind>`, runbook), timeline labels and
skip/no-data step summaries carry key + args inside the JSON blob; carry-forward
copies them. Tool-message summaries and check reasons stay original + marker.
Files: `internal/sre/recommend.go`, `timeline.go`, `orchestrator.go`,
`investigation.go` (types), FE investigation components, `i18n.ts`.
Tests: blob round-trip (old blob without keys decodes and renders as before);
narration prompt golden unchanged; ack survives a re-run.
Accept: a new investigation reads in English except tool summaries / reasons,
which carry the TR marker; an old one is unchanged.

**S5 — ML sentence from the record; retire `mlValueText`.**
For events with a valid record, the list / detail sentence is composed on the FE
from record numbers in both languages; events without a record keep the server
sentence. Needs its own short audit of `explain_text.go` first.
Files: FE `components/coremetry/anomalies/*` (`unifiedRows.ts`, `mlEvidenceRows.ts`,
`mlValueText.ts` removed), `i18n.ts`. No Go change expected.
Tests: vitest parity — FE Turkish composition equals the server sentence on a
recorded fixture set before the switch.
Accept: no code path rewrites a fragment of a server sentence.

## 5. Decisions for the operator

1. **Turkish UI: keep the server string verbatim, or render both languages from
   the catalog?** Recommended: verbatim (zero risk, half the catalog).
2. **Notifications stay Turkish-only (D9)?** Recommended: yes, no change.
3. **LLM prose stays as generated, with a "TR" marker in the EN UI?** Recommended:
   yes. (Generating background prose in the branding default language is possible
   later but changes prompts, goldens and eval baselines.)
4. **MCP tool output / CoSRE prompts stay Turkish, and message keys are not added
   to tool rows?** Recommended: yes — keeps tool text and token cost identical.
5. **Old persisted investigations and ML records: no backfill, shown in Turkish
   with the marker until re-run?** Recommended: accept.
6. **Built-in rule names are translated for display only; persisted values are
   never renamed** (they are classification inputs)? Recommended: yes.
7. **Hypothesis check reasons: English check titles + original reason, or full
   structured reasons (~100 keys)?** Recommended: titles only for now.
8. **Is the "TR" marker acceptable visually, or should untranslated text simply
   appear unmarked?** Recommended: marker, EN UI only.

## 6. Not determined from the code

- Real bundle delta (no build run); the per-entry estimate is from source size.
- Whether all 15 record-writing detectors give S5 enough fields to compose the
  sentence without touching the record contract.
- How much of `problems.description` for alert-rule problems is operator text
  versus generated text in production data.
- Where digest items are stored and for how long (they persist headline + impact).
- Exact count of FE render sites per field (file list above is from a field grep).
