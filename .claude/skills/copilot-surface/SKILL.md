---
name: copilot-surface
description: Add ONE new AI "✨ Explain" affordance to Coremetry — system prompt in internal/copilot/prompts.go, route in ai_routes.go (requireCopilot) + handler in its own file, the s.copilotExplain(r, …) wrapper that keeps /ai attribution honest, the lib/api.ts client method, and the button. Use when the operator wants a new explain affordance on a page or panel that has none. Do NOT use for the chat/agent runtime, providers, streaming, RAG, tool-calling or RCA verdicts (those live in internal/ai and the rest of internal/copilot), for tuning Copilot settings or models, or for explaining something to the operator yourself.
---

# /copilot-surface — add a new AI explain surface

Each "✨ Explain X" button in Coremetry follows a well-trodden
pattern (see `internal/copilot/prompts.go` for prior art:
SystemPromptTrace, SystemPromptSpan, SystemPromptSLOBurn,
SystemPromptSlowQuery, etc.). This skill walks the agent through
the 5 files to touch + the conventions to follow.

The 11-step "Ship checklist" in CLAUDE.md
collapses to 5 here because Copilot surfaces are read-only and
share the existing infrastructure — no schema change, no settings
persistence (Copilot config already lives in `system_settings`),
no audit row (Copilot is configured-or-not, not RBAC-gated).

## Args

`/copilot-surface <name>` — short kebab-case name for the new
surface, e.g. `explain-flow`, `explain-cardinality`, `runbook-incident`.

If omitted, ask the user. Don't invent a surface name.

## Conventions (from CLAUDE.md)

- AI Copilot system prompts live in `internal/copilot/prompts.go`
  (v0.9.1128 / Faz 1.6 — ALL of them, including the chat tiers that
  used to sit in `internal/api`), exported as `SystemPromptX() string`.
  A structural gate (`prompt_language_test.go`) fails the build if a
  prompt const is declared in `internal/api` or an accessor is added
  without registering it in `promptRegistry()`.
- All Copilot endpoints go through `s.copilotExplain(r, ...)`
  wrapper so the /ai surface attribution stays accurate.
  Never call `s.copilot.Explain` directly — that bypasses the
  `ai_calls` recorder and the `/ai` page goes blind.
- Surface name is derived from the URL path
  (`/api/copilot/explain-X` → `"explain-X"`) by the helper in
  `internal/api/ai_observability.go`.

## Files to touch (5)

### 1. `internal/copilot/prompts.go`

Add the system prompt here (never in `internal/api`), following the
existing shape:

```go
// systemX — operator hits "✨ Explain" on <surface>. Prompt
// receives <inputs>. Goal: <one-line goal>.
//
// Bound: short. Operator is in triage mode, not study mode.
const systemX = `You are a senior <role> assistant inside an APM
tool. The operator clicked "Explain" on <thing>. You receive:
<list of fields>.

Answer in short bullets — as many as the evidence supports, no
more:
  (1) one-line verdict: <list of canonical verdicts>.
  (2) <specific hazard you see, anchored to the data>.
  (3) <highest-impact remediation, one best fix not five>.
  (4) optional: <second-tier improvement>.

Anchor on the data you have. Don't speculate beyond what you
were shown. Don't hedge.` + AnswerInTurkish

func SystemPromptX() string { return systemX }
```

Then register it in `promptRegistry()` + `promptTexts()`
(`internal/copilot/prompt_language_test.go`) with its class —
`classDirective` for prose (the const ends with `+ AnswerInTurkish`),
`classTurkishNative` for Turkish-written instructions,
`classStructured` for machine-parsed output — and in
`promptVersionRegistry` (`observe_meta.go`). If the surface's main
consumer is the small local model and the output must follow a fixed
shape, use the Turkish-native pattern with ONE few-shot and fixed
section labels (`systemProblem`, `systemServiceAnalysis`).

Patterns to copy:
- Lead with "You are a senior X assistant inside an APM tool."
- Enumerate the input shape so the model knows what it has.
- Fix the section schema ((1)…(4)); let the evidence set the count — no numeric bullet cap (v0.10.253 D7).
- Demand a one-line verdict + specific quote / clause / number.
- Demand ONE best fix, not a menu.
- Explicit "don't hedge" / "don't speculate" at the end.

### 2. `internal/api/ai_routes.go` (route)

Register inside `registerAIRoutes`, wrapped by the single 503 gate:

```go
mux.HandleFunc("POST   /api/copilot/explain-X", s.requireCopilot(s.copilotExplainX))
```

No role wrapper unless the surface exposes data the viewer couldn't
otherwise see. `TestRequireCopilotRouteCoverage` fails on an unwrapped
`/api/copilot/` route; api.go does not grow (`TestApiGoDoesNotGrow`).

### 3. `internal/api/copilot_explain_x.go` (handler)

Own file (emsal `copilot_explain_slo.go`). No configured/active check in
the handler — `requireCopilot` already answered 503
(`TestNoInlineCopilotGates`).

```go
func (s *Server) copilotExplainX(w http.ResponseWriter, r *http.Request) {
    // Read inputs — either from the request body (when the
    // frontend already has the data on hand) or from the
    // chstore (when the operator only knows a key like an id).
    var body struct {
        // … fields
    }
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
        writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
        return
    }
    var sb strings.Builder
    fmt.Fprintf(&sb, "...", body.X, body.Y) // cap free-text fields (SQL, log bodies) ~4KB
    out, err := s.copilotExplain(r, copilot.SystemPromptX(), sb.String())
    if err != nil {
        writeErr(w, err)
        return
    }
    writeJSON(w, map[string]any{"explanation": out})
}
```

**Critical:** use `s.copilotExplain(r, …)`, NOT
`s.copilot.Explain(r.Context(), …)`. The wrapper attributes the
call to the surface for `/ai` analytics + records the `ai_calls`
row. Direct calls silently break the dashboard.

### 4. `frontend/src/lib/api.ts` + `lib/aiSubject.ts`

Add the client method (`request<{ explanation: string }>` POST to
`/api/copilot/explain-X`) and a new entry in `AI_KINDS`
(`lib/aiSubject.ts`) plus its branch in `CopilotExplain`'s kind
dispatch. Shared response types go in `lib/types.ts` (CLAUDE.md
"What goes WHERE": `lib/types.ts` is the single source of truth for
shared shapes).

### 5. Frontend trigger

Don't build a local explain state machine or button. The surface
renders the shared trigger `<AIExplainButton subject={…}>`
(`components/ai/AIExplainButton.tsx`); the answer lives in the AI
drawer, where `CopilotExplain` (`components/CopilotExplain.tsx`)
fetches by kind and renders `ExplainBody` + feedback. Mirror an
existing kind (e.g. `anomaly`, `service-health`). Raw `<button>` is
blocked outside `ui/` (`buttonUnityRatchet`, ESLint
`ui/no-raw-button`).

## Verification

After the 5 files are touched:

1. `go build ./...` — handler + system prompt compile.
2. `cd frontend && npx tsc --noEmit && TZ=UTC npx vitest run` — api.ts + AI kind type-check; button ratchet.
3. Trigger the button manually in the running app (or simulate
   via `curl -X POST /api/copilot/explain-X -d '{…}'`).
4. **Verify `/ai` attribution.** A row should land with
   `surface = "explain-X"` + sensible token counts. If the row
   doesn't appear, you've bypassed `s.copilotExplain` somewhere
   — that's the single most common failure mode and the whole
   point of the wrapper.

## Then ship

Use `/release "add ✨ Explain X surface"` to commit + push +
rebuild. Surface attribution will start flowing on the next
operator click.

## Anti-patterns

- **Don't bypass `copilotExplain`.** Direct `s.copilot.Explain`
  calls skip the recorder, making /ai blind to the new surface.
- **Don't pad the system prompt.** Keep it to what the model
  needs: input shape, section schema, grounding rule — plus one
  example when the small local model must reproduce a fixed shape
  (`systemProblem`).
- **Don't pass entire CH responses through.** Cap free-text
  fields (~4KB): the model is billed for every token you send;
  `ai_calls` only truncates its recorded sample
  (`chstore.SamplePromptCap`), not the prompt.
- **Don't ship without the /ai surface attribution working.**
  The whole point of the wrapper is operator visibility into
  AI usage. Verify the surface name appears in /ai before
  shipping.
- **Don't add a new Copilot config setting per surface.**
  Provider/model/key all live in `system_settings` under the
  existing `copilot` key — that's what `LoadPersisted` already
  hydrates. New surfaces consume the same config, don't fan
  out new ones.
