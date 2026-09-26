---
name: mcp-tools
description: Add a tool, resource or prompt to Coremetry's OWN MCP server (internal/mcptools/) — the Deps closure, the range_s convention instead of nanosecond timestamps, clampLimit caps, contract-style tool descriptions, auth gating and audit on writes. Use BEFORE editing internal/mcptools/ or exposing a Coremetry surface that an external LLM will call. Do NOT use for configuring or consuming third-party MCP servers, for MCP client work, or for adding a plain HTTP route (that is internal/api/).
---

# /mcp-tools — add a Model Context Protocol tool

Coremetry exposes a JSON-RPC 2.0 MCP server so external LLMs (Claude
Desktop/Code, agent frameworks) can query telemetry. Two transports
(v0.10.795 doc fix): **Streamable-HTTP `POST /api/mcp` (2025-03-26,
stateless, primary)** and legacy HTTP+SSE `/api/mcp/sse` (2024-11-05,
pod-local session, deprecated by the 2026-07-28 spec). `tools/list` is
sorted by name. The 2026-07-28 `server/discover` + `_meta` contract is
not implemented yet (audit 2026-09-19 M1/M2). Infrastructure shipped
v0.6.4-v0.6.7, Streamable v0.9.14:

| Concern | Where |
|---|---|
| Protocol layer (registry, JSON-RPC, SSE) | `internal/mcp/mcp.go` |
| Concrete tools (closures over chstore + logstore) | `internal/mcptools/tools.go` |
| Resources (URI-addressed snapshots) | `internal/mcptools/tools.go` (`registerResources`) |
| Prompts (curated system+user pairs) | `internal/mcptools/prompts.go` |
| Boot wiring | `main.go` (Register call after mcp.Server construction) |

This skill captures the conventions a new tool MUST follow. They
exist because LLMs are different consumers than the React UI is:
small context windows, error-tolerant input shapes, descriptions
they read to decide *whether* to call.

## When to add an MCP tool vs. just an API route

Add an MCP tool when:
- An LLM should be able to use this surface autonomously during
  an investigation (e.g. "find the failing service", "show me
  recent errors").
- The surface returns structured data that fits in <2k tokens.
  Anything bigger blows the LLM's context.
- An equivalent HTTP route already exists — MCP tools are usually
  a thin wrapper around the same `chstore.Store` method the
  React UI uses.

Don't add an MCP tool for:
- Mutations (create/update/delete) without operator-in-the-loop.
  Roles still apply (viewer/editor/admin enforced via the JWT
  middleware), but an LLM acknowledging a problem unprompted is
  blast-radius the operator hasn't approved.
- Pure UI affordances (table sort, chart pan). These exist for
  human eyes; an LLM doesn't need them.
- Surfaces that return >2k tokens worth of data. Build a paged
  / summary variant first.

## Steps

### 1. Pick the chstore (or logstore) method the tool will call

MCP tools are thin wrappers. The data path stays in `chstore.Store`
— the tool just exposes it with an LLM-shaped contract. If the
method doesn't exist yet, add it to chstore FIRST (per the
`/clickhouse-schema` skill), THEN come back here.

### 2. Define a typed args struct

Sits in `internal/mcptools/tools.go` near the tool factory. Naming
convention is `<toolName>Args`. JSON tags use `snake_case` because
that's what the JSON Schema layer below mirrors.

```go
type listServicesArgs struct {
    NameContains string `json:"name_contains,omitempty"`
    RangeS       int    `json:"range_s,omitempty"`
    Limit        int    `json:"limit,omitempty"`
}
```

Required fields don't carry `omitempty`. Optional fields do — the
LLM is more likely to omit a field than to send an explicit zero
value.

### 3. Use `range_s`, not `from` / `to` nanos

This is the single most-violated convention. LLMs are notoriously
bad at constructing 19-digit unix-nanosecond timestamps, especially
for relative windows ("last 30 minutes"). The repo's chstore
methods take from/to time.Time — the TOOL converts `range_s` into
that pair via:

```go
from, to := rangeWindow(ctx, a.RangeS) // helper in tools.go
```

`rangeWindow(ctx, 0)` returns a sane default (30min). The tool's JSON
Schema declares `range_s` with both `minimum: 0` and `maximum`
(typically 604800 = 7 days) so the LLM can't fan a request that'll
scan three months of partitions.

If the chstore method really needs absolute timestamps from the
client (rare — only "look up trace by id at exact time"), accept
them as `time_iso8601` strings instead, parsed via
`time.Parse(time.RFC3339, ...)`. Still don't ask the LLM for
nanoseconds.

### 4. Cap the result count with `clampLimit`

```go
limit := clampLimit(a.Limit, /*default*/ 50, /*max*/ 500)
```

Hard rule: server-side cap is the backstop. Don't trust the LLM
to set Limit. The default is for the LLM-without-an-opinion call;
the max is for context-window safety.

Typical caps:
- list-* tools: default 50, max 500
- per-row detail tools (get_*): not paged, but the BODY should be
  bounded (truncate large strings, etc.)
- search/query tools: default 100, max 1000

### 5. Build the `mcp.Tool` registration

```go
func myTool(d Deps) mcp.Tool {
    return mcp.Tool{
        Name:             "snake_case_tool_name",
        ShortDescription: "<2-3 cümlelik TÜRKÇE sözleşme — in-app sohbet her tur bunu okur>",
        MinRole:          "", // "" = viewer; REST eşi editor/admin ise aynısı
        Description:      "<one-paragraph contract — see step 6>",
        InputSchema: map[string]any{
            "type": "object",
            "properties": map[string]any{
                "name_contains": map[string]any{
                    "type":        "string",
                    "description": "Substring match. Empty = all.",
                },
                "range_s": map[string]any{
                    "type":        "integer",
                    "minimum":     0,
                    "maximum":     604800,
                    "description": "Lookback seconds. Default 1800.",
                },
            },
            "required": []string{"name_contains"}, // only if truly required
        },
        Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
            var a myToolArgs
            if len(raw) > 0 {
                if err := json.Unmarshal(raw, &a); err != nil {
                    return nil, fmt.Errorf("decode args: %w", err)
                }
            }
            from, to := rangeWindow(ctx, a.RangeS)
            limit := clampLimit(a.Limit, 50, 500)
            rows, err := d.Store.GetXFiltered(ctx, ..., from, to, ..., limit, 0)
            if err != nil {
                return nil, err
            }
            return map[string]any{"rows": rows, "count": len(rows)}, nil
        },
    }
}
```

### 6. Write the description like a contract, not a sentence

The description is the **only** signal the LLM has for deciding
whether to call your tool. It's not user docs. Three things to
include, in order:

1. **What it returns.** "RED metrics + open problem count for one
   service." Not "queries the service summary table".
2. **When to use it.** "Use after `list_services` to drill into a
   specific service." This is how the LLM chains tools.
3. **Cost/scope warning if any.** "Reads the 5-minute pre-
   aggregate so cheap to call repeatedly." Or "scans raw spans;
   keep range_s small."
4. **When NOT to use it** — name the sibling tool that answers the
   neighbouring question ("for one endpoint's RED use
   get_operation_health").
5. **What it does not return / its limits** — caps, windows, fields
   left out, so the model doesn't infer absence from omission.

Bad: `"Lists services."`
Good: `"List Coremetry services with their current RPS, error rate, and p99 latency. Reads the 5-minute pre-aggregate so it's cheap to call repeatedly. Use this as the entry point when investigating an incident: 'which services are unhealthy right now?'"`

### 7. Mirror args → JSON Schema EXACTLY

If the struct field is `NameContains string \`json:"name_contains"\``,
the JSON Schema property is `"name_contains": {"type": "string"}`.
Mismatches silently break tool calls — the args unmarshal to a
zero-value struct and the tool returns "all services" instead of
the filter the LLM asked for.

JSON Schema types map: `string`, `integer`, `number`, `boolean`,
`array`, `object`. Use `minimum` / `maximum` for integer bounds,
`enum` for string allowlists, `description` for free-form
guidance the LLM reads.

### 8. Add it to `ToolList(d)`

`Register()` delegates to `ToolList(d)` (`tools.go`), which also feeds
the in-app chat's function-calling spec — one registry, two consumers.
Place the tool next to the tools it chains with; the list order is
deliberate (see the adjacency comments) and is what the chat model sees.
Tools that need in-app conversation state go in `chatOnlyTools` too:
`Register` hides them from external MCP clients.

### 9. Auth gating

Tarayıcı kökenli istekler için ek kapı (v0.10.804, M4): `/api/mcp*`
allowlist dışı bir `Origin` taşıyorsa handler'a girmeden 403 döner
(`internal/api/cors.go` `requireOrigin`; allowlist =
`COREMETRY_ALLOWED_ORIGINS` + `COREMETRY_PUBLIC_URL` + aynı-origin).
Origin başlığı olmayan CLI/ajan istemcileri etkilenmez.

MCP tools inherit the api server's JWT middleware: viewer / editor
/ admin roles flow through the same path as REST. The chstore
method should NOT carry role logic — the route layer does. For a
mutation tool, also call `s.audit(...)` from inside the handler
(through Deps if needed) so the action lands in the audit log.

Role gating is per tool: set `mcp.Tool.MinRole` to the REST sibling's
gate (`""` = any authenticated viewer; `"editor"`/`"admin"` when the
REST route is wrapped in `RequireAnyRole`/`RequireRole`). `mcpCallGate`
(`internal/api/mcp_gate.go`) enforces it on MCP calls and the chat
hides tools above the caller's role — a mismatch with REST is a bug.
A write tool (none exist today) needs MinRole ≥ "editor" plus an audit
row.

### 10. Boot wiring sanity

`main.go` builds the MCP server after every `srv.Set*` the Deps
read, calls `mcptools.Register(mcpSvc, srv.MCPDeps())` — the same
constructor (`internal/api/mcp_deps.go`) the in-app chat uses — then
`srv.SetMCP(mcpSvc)`. Tools must be registered BEFORE the endpoint
starts accepting traffic, or early `tools/list` requests return
empty.

This is already wired — don't move it. A new Deps field is set in
`mcpDeps()` only: it is the single constructor
(`TestOnlyOneMCPDepsConstructionSite` also scans `main.go`).

### 11. Test

Add a table test next to the tool (`internal/mcptools/<tool>_test.go`;
`tools_test.go`, `short_desc_test.go` and `has_more_test.go` are the
catalogue-wide gates). Then smoke-test the live endpoint
(Streamable-HTTP, stateless):

```bash
curl -sS -b "$JAR" -X POST http://localhost:8090/api/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call",
       "params":{"name":"my_tool",
                 "arguments":{"range_s":300,"limit":5}}}' \
  | python3 -m json.tool
```

(`$JAR` = the login cookie jar from /perf-triage ADIM 1.) Check: tool
found, no `decode args` error, expected shape, count reasonable.

### 12. Ship via `/release`

Single `v0.10.X — MCP tool <name>` release. Brief body
describing what it surfaces + when an LLM would reach for it.
Update the `## Tool catalogue` comment block at top of `tools.go`.

## Patterns you should not invent locally

- **Don't write your own range parser.** Use `rangeWindow(s)`.
- **Don't return raw chstore types directly.** Wrap in
  `map[string]any{"<thing>": rows, "count": ..., "window_s":
  ...}` so the LLM has signals about what each field is.
- **Don't reach into the api.Server.** Tools are stateless
  closures over Deps. If something needs the http.Request, it's
  a route, not an MCP tool.
- **Don't add OAuth / OIDC handshake inside the tool.** JWT
  middleware handles it.

## Resources + Prompts — adjacent surfaces

If your "tool" really exposes a **pinned snapshot** an LLM should
attach to its context (no args, stable URI), register a
**Resource** instead — example pattern in `registerResources()`.
URIs follow `coremetry://<noun>` or `coremetry://<noun>/<id>`.

If your "tool" really is a **curated investigation template**
(system+user message pair the LLM should run on demand), register
a **Prompt** in `prompts.go`. The in-app ✨ Explain buttons are
the canonical examples.

If you're unsure: tools are verbs (`list_X`, `get_Y`, `search_Z`),
resources are nouns (`coremetry://services`), prompts are tasks
(`explain_problem`, `compare_traces`).

## Anti-patterns

- **Don't expose mutations as tools without an operator gate.**
  An LLM autonomously ack'ing problems is a postmortem waiting
  to happen.
- **Don't accept `from_ns` / `to_ns` instead of `range_s`.** Every
  LLM that hits the tool will get the nanosecond math wrong; you'll
  spend the next bug-fix release explaining "why is the LLM asking
  about year 56000".
- **Don't omit `description` on properties.** The LLM treats an
  un-described property as guessable. It will guess wrong.
- **Don't lie in the tool description.** "Reads the 5-minute
  pre-aggregate" promises an SLO — if the chstore method actually
  scans raw spans, your tool will time out under load and the LLM
  will retry, and retry, and retry.
- **Don't add a "debug" tool that returns the raw SQL.** Pure
  blast-radius; an LLM can leak it. If you need that during
  development, use `/admin/sql` in the UI.
