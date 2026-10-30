# shadcn interaction slice — plan (2026-10-30)

Three cross-page patterns on the Phase 1 foundation (`components/shadcn`, `components/coremetry/overlay`, escBridge, bundle budget). Plan only — no code, no commit. Inputs: `shadcn-audit.md` §3/§4.4/§6/§7/§11/§13, `shadcn-phase1-plan.md` §1/§6/§12, `components/coremetry/*`, `components/ui/Drawer.tsx`, `components/CommandPalette.tsx`, memory "entity coherence" + "ask before behaviour changes".

Rules carried over: visual = free, IA = stop-and-ask, new user behaviour = ask (audit §13); Radix/cmdk never in the entry (phase1 §1); one overlay root per table (audit §6); every entity cell pivots to its page (entity coherence); each release its own `v0.10.X`.

## 0. Baseline (what exists today)

| Item | Where | Fact that shapes the plan |
|---|---|---|
| Palette | `components/CommandPalette.tsx` (847 lines, inline styles, no `.cp-*` CSS), mounted eager `AppShell.tsx:275`; in the shell import-ban list (`eslint.config.js:171`) | Must become lazy before cmdk can enter it (phase1 §1.2) |
| Palette facade | `coremetry/CommandPalette/CommandPalette.ts` (v0.10.1617): frozen `CommandPaletteProps { open, onOpenChange, groups[], query, loading, empty }` re-exporting today's impl | Frozen type already models **groups** — the cmdk body fills it, AppShell/Topbar call shapes stay |
| Palette opener | `openCommandPalette()` `:147` (subscription set) ← `TopbarSearch.tsx:29`; hotkey `useShortcuts([{ keys:'mod+k', evenInInputs:true }])` `:246` | Both stay in the eager gate |
| Palette actions | `lib/actions.ts` `ACTIONS` ×6 (ack-problem, disable-rule, enable-rule, save-view, mark-event, silence-anomaly), role-gated `allowedRoles`, param-prompt mode (`text` / `id-suggest` / `duration`) | All six are **state-changing** and already shipped → kept 1:1, not "new" |
| Overlay wrappers | `coremetry/overlay/{Sheet,ContextMenu,DropdownMenu,Dialog,Popover,Tooltip,AlertDialog}` (v0.10.1615) controlled + `escBridge` (one Esc stack) ; `overlayImportBan.test.ts` (OVERLAYS incl. `command`, `sheet`, `context-menu` → importable only under `components/coremetry/**`) | New primitives live under `components/coremetry/`, never in pages |
| Sheet in use | `components/cosre/conversation/CosreSheet.tsx` (v0.10.1647): `modal={false}`, `onInteractOutside` blocked, `isEmptyAreaClick`, width via `--cosre-sheet-w` + `lib/cosre/sheetWidth`, <640px full width | The generalisation target for `DetailSheet` |
| Drawer atom | `components/ui/Drawer.tsx` (frozen `ui/`): `onClose, header, width=560, backdrop=true, closeOnEmptyClick, bodyStyle, ariaLabel, resizeKey` (persisted via `lib/storage`, `lib/drawerWidth` clamp ≥380, dbl-click reset), `useEscLayer`, `useFocusTrap(panelRef, backdrop)`, `DrawerSection`, `DrawerTrendRow`; CSS `.drawer-panel/.drawer-resizer/.drawer-close` (`globals.css:2391-2405`, `:4659-4660`) | 15 importing files (list §2.3); every migration lowers `uiImportRatchet` |
| Peek "drawers" | `TracePeekDrawer.tsx:110`, `CorrelationContextDrawer.tsx:92` are **centered `Modal`s** (used by Logs, Explore, ServiceCharts, LogTable, TraceLogList, ServiceTimeline) | Converting them to a side sheet = IA change → Q5, not in this slice |
| Row actions | `coremetry/DataTable` `rowActions` = ONE shared `DropdownMenu` anchor (`DataTable.tsx:98-112`, `--dt-menu-x/y`), skipped in `virtual` mode; `onContextMenu` sites in `src`: **0** | Context menu follows the same one-root rule and needs no trailing column → works in virtual mode |
| Entity href producers | `serviceHref`, `traceHref`, `tracesPivotHref` / `operationTracesHref` / `dbTracesHref` / `statementTracesHref` / `attrTracesHref` (`lib/pivotHref.ts`), `logsHref` (`lib/logsUrl.ts`), `serviceInboxHref`, `entityHref` (K8s), `podDetailPath` (`pages/service/`), `endpointDetailHref` (`pages/endpoints/endpointParam.ts`), `stmtDetailHref`, `withPivotCtx` (`lib/pivotContext.ts`: range/env/cluster/namespace carry) | Menu items are these functions, nothing new |
| Keyboard | `lib/keyboard.ts` single document listener, `useShortcuts`, `listShortcuts()`; `lib/useTableNav.ts` (j/k/Enter/o/Escape, `scope`) | Shift+F10 / menu key hangs off the table-nav focused row |
| Lazy gate pattern | `CopilotChatGate.tsx` (v0.10.1599): eager FAB + event queue (`queue.current`) + `ReplayQueued` after lazy mount; `ToasterGate` (R10) same | Palette gate copies it with a **key buffer** |
| Bundle proof | `scripts/bundle-budget.mjs` + `bundleBudget.pin.test.ts`; `radix` chunk regex includes `cmdk`; budget 347 KB gz; today ≈258 KB gz (phase1 §1.3) | Every release row carries the script's last line |
| Deps installed | `cmdk 1.1.1`, `radix-ui 1.8.1`, `sonner 2.0.8`, `axe-core 4.14.0`; `components/shadcn/{command,sheet,context-menu}.tsx` generated | No new dependency in this slice |

## 1. Command palette (⌘K) on cmdk

### 1.1 Keep 1:1 (behaviour parity list — each row gets a parity test)

| Feature | Today (`components/CommandPalette.tsx`) | In the cmdk body |
|---|---|---|
| Open: `mod+k` even in inputs; `openCommandPalette()` from TopbarSearch | `:246`, `:147` | unchanged, both live in the eager gate (§1.3) |
| Page catalogue, i18n via `navKey`, `aliases`, `adminOnly` role filter, Settings tabs from `SETTINGS_TAB_INDEX` | `PAGES :69-125`, `:391-402` | same arrays, moved to `lib/palette/pages.ts` (pure) |
| Services: server search `api.serviceNames(q, 20)` 200 ms debounce + stale guard; empty query → pinned ★ + recent | `:309-330`, `:286-304` | same hook (`usePaletteServices`); **never** an eager catalogue |
| Endpoints `api.operationNames(undefined, q, 6)` → `operationTracesHref({window: currentRange})`; Oracle ops `api.oracleOperations(q, 6)` (≥3 chars) | `:335-380` | same hooks |
| Trace id (`/^[a-f0-9]{16,32}$/i`) → "Go to trace"; problem id / identity query (`lib/paletteIdentity`) | `:132`, `:441` | same |
| Ranking `rankPaletteResults` (entities first: service → endpoint → oracle op → page, v0.10.126) | `lib/paletteScore` | same function; cmdk filtering **disabled** (`shouldFilter={false}`) so server ranking is authoritative |
| Actions ×6 with param-prompt mode + `id-suggest` debounce + duration chips | `lib/actions.ts`, `:222-262`, `:540-600` | same `ACTIONS`; prompt mode = a cmdk "page" (`value` swap), chips stay `Button` |
| "Search everything" footer → `searchSeeAllHref` | `:1335 note` | `CommandGroup` footer item |
| Navigation `navigate(navHref(r.to, locationSearch))`, close on pick, focus return to opener | `:593`, `:797` | same; focus return via Radix `onCloseAutoFocus` |
| A11y: `role=dialog aria-modal`, combobox/listbox/option, `aria-activedescendant`, focus trap, Esc via `useEscLayer` | `:613`, `:767-787` | Radix Dialog + cmdk (`cmdk-root` combobox pattern); Esc via `coremetry/overlay/Dialog` escBridge |

### 1.2 New: grouped results + navigation-like actions

| Group (order = today's rank order) | Source | Note |
|---|---|---|
| Pinned / Recent (empty query only) | `getPinnedServices` / `getRecentServices` | header "Pinned", "Recent" |
| Services → Endpoints → Operations | server hooks | hint column = today's hint |
| Trace / Problem (only when query matches an id) | identity helpers | single-item groups |
| Pages | `PAGES` filtered by role | |
| Actions | `filterActions(role, q)` + new list below | state-changing ones render with a `CommandShortcut`-style tag "changes state" (visual only) |
| Search everything | footer | |

Grouping is presentation over the existing order (v0.10.126 already sorts by kind), so no cross-kind reorder — still Q1 because headers are new UI.

Proposed **new** actions (all navigation/local-preference, none touch server state; all need Q2):

| Action id | Does | Uses | Roles |
|---|---|---|---|
| `copy-link` | copies absolute, range-frozen URL of the current view | `absoluteShareHref(location, Date.now())` + `copyToClipboard` + `toast` | all |
| `ask-cosre` | opens CoSRE prefilled with the current page subject | `emitAiPrefill` (`components/ai/aiEvents.ts`) — same event the FAB path consumes | all (only if copilot enabled) |
| `theme-<dark\|light\|redhat>` | switches theme | `applyTheme` (`lib/theme.ts:66`) | all |
| `density-<comfortable\|compact\|dense>` | switches density | setter extracted from `DensityToggle.tsx` into `lib/density.ts` (pure, same storage key) | all |
| `shortcuts-help` | opens the `?` help | `ShortcutsHelp` opener | all |
| `pin-service` / `unpin-service` | toggles pin of the service in the current URL | `toggleServicePin` (`lib/recentServices.ts`) | all |

Excluded on purpose (state or destructive): refresh-interval change, ack/resolve/mute beyond the existing six, delete anything, settings writes. Any addition = operator question first.

### 1.3 Lazy loading — cmdk never in the entry

| Piece | File | Eager? | Contents |
|---|---|---|---|
| Gate | `components/CommandPalette.tsx` (same path, shrinks to ~80 lines; stays in the shell ban list) | yes | `useShortcuts(mod+k)`, `openCommandPalette()` subscription, `mounted` latch, **key buffer**, `React.lazy(() => import('@/components/coremetry/CommandPalette/CommandPaletteBody'))` |
| Body | `components/coremetry/CommandPalette/CommandPaletteBody.tsx` + `lib/palette/*` hooks | no (page-style chunk; cmdk/Radix land in the `radix` group) | cmdk `Command` inside `coremetry/overlay/Dialog`; receives `initialQuery` |
| Facade | `coremetry/CommandPalette/CommandPalette.ts` | type only | re-export switches from `@/components/CommandPalette` to the body; frozen props unchanged |

Key buffer (the `CopilotChatGate` queue, for keystrokes): on first open the gate mounts the lazy body AND installs a capture-phase `keydown` listener that appends printable chars / handles Backspace into `queue.current` (Esc = cancel open, pop latch) until the body signals `live`; the body mounts with `initialQuery = queue.join('')` and focuses its input. Opening the N-th time is synchronous (latch stays mounted, `open` toggles). Warm-up: `requestIdleCallback(() => import(body))` once after first paint of an authenticated shell, so the first ⌘K normally finds the chunk cached — the import is dynamic, so it is **not** in `index.html`'s preload set (bundle-budget step 1 unaffected). No `ui/` import in the gate (would raise `uiImportRatchet`); no `<input>` in the gate (would hit `rawElementRatchet`).

Shell ban stays on the gate; the body is outside the banned file list and imports `cmdk` only through `components/shadcn/command.tsx` (allowed under `coremetry/**` per `overlayImportBan`). Proof = `bundle-budget.mjs` sourcemap scan: `forbidden in entry: none`, plus the measured delta of moving today's 847-line body + `lib/actions` + `paletteScore` out of `index-*.js` (recorded, not estimated).

### 1.4 Keyboard + axe

| Check | Test |
|---|---|
| ↑/↓/Enter/Esc, Enter on action enters prompt mode, Esc in prompt mode returns to results (today: closes — Q3) | `CommandPaletteBody.contract.test.tsx` (RTL, user-event) |
| Esc order: palette above an open sheet closes only the palette (escLayer incident #1) | `escLayer.contract.test.tsx` gains a cmdk case |
| Focus returns to TopbarSearch button after pick/close | contract test |
| axe: `role=dialog` labelled, combobox/listbox relations, 3 theme cells | `designAxe.test.tsx` via `pages/design/catalog.tsx` `CommandDemo` (exists `:160`) + a populated-groups case |
| Server-search rules unchanged (debounce, min length, limit) | `lib/palette/*.test.ts` table-driven (pure parts) |

## 2. Sheet standard — `coremetry/DetailSheet`

One composite over `coremetry/overlay/Sheet` (already escBridged, Radix focus management) that carries every `ui/Drawer` feature, so a page swaps `<Drawer>` for `<DetailSheet>` without redesigning. `CosreSheet` stays its own shell (chat ≠ detail) but shares the width + empty-area helpers.

### 2.1 Frozen API (sketch) — `components/coremetry/DetailSheet/{DetailSheet.tsx,types.ts,useSheetParam.ts,index.ts}`

| Prop | Type | Carries from `ui/Drawer` |
|---|---|---|
| `open`, `onOpenChange(o)` | controlled | `onClose` (callers pass `() => setParams({key:null})`) |
| `title: ReactNode`, `badges?: ReactNode`, `pageHref?: string` | header slot | `header` ReactNode; `pageHref` renders "Open page ↗" — entity coherence: every sheet subject has a full page |
| `width?: number` (default 560) | px | `width` |
| `resizeKey?: string` | localStorage key | `resizeKey` (persisted width, `clampDrawerWidth`, dbl-click reset, `role=separator`) — width written to `--sheet-w` by ref (no inline style; CosreSheet pattern) |
| `modal?: boolean` (default true) | backdrop | `backdrop` (true = `aria-modal`, focus trap; false = companion mode) |
| `closeOnEmptyClick?: boolean` | only when `modal=false` | `closeOnEmptyClick` via `isEmptyAreaClick` + `onInteractOutside` prevented |
| `ariaLabel?: string` | | `ariaLabel` (default from `coremetry/strings`) |
| `children`, `footer?` | | body; `bodyStyle` dropped (callers use a class) |
| `<SheetSection title>` | sub-component | `DrawerSection`; `DrawerTrendRow` moves as `SheetTrendRow` (Sparkline inside) |

`useSheetParam(key)`: `[value, set(v | null)]` over `useSearchParams` with `replace:true` + sig guard — the URL-as-source-of-truth half, so every detail sheet is shareable and Back closes it (today 6 of 15 sites read a param themselves, 9 hold local state → Q6 for those 9).

Mobile: `<=640px` → full width (`CosreSheet` rule, CSS only). Esc: via escBridge → one stack with the palette, modals and remaining hand-rolled drawers.

### 2.2 Parity tests (written before the first migration)

| Behaviour | Source test to port | Target |
|---|---|---|
| Esc closes the topmost layer only (sheet under palette survives) | `escLayer.contract.test.tsx` | add DetailSheet case |
| Empty-area click closes non-modal; click on `KEEP_OPEN_SELECTOR` element does not | `lib/emptyAreaClose` tests | `DetailSheet.contract.test.tsx` |
| Resize persists under `resizeKey`, clamps at viewport, dbl-click resets | Drawer tests | same file |
| Focus trapped when modal, returned to opener on close; not trapped when non-modal | Drawer / Modal contract | same file |
| `<=640px` full width; `pageHref` link present when given | — | same file + axe 3 cells via catalog demo |
| Zero inline style in the composite | `inlineStyleRatchet.test.ts` | counter 0 for `coremetry/DetailSheet/**` |

### 2.3 Migration order (15 `ui/Drawer` importers; one or two per release)

| # | Site | Width / mode | Open state today | Why this position |
|---|---|---|---|---|
| 1 | `pages/External.tsx:223`, `pages/Hosts.tsx:191` | default / modal | local | the two byte-identical first Drawer adopters (v0.8.465) — smallest risk pilot |
| 2 | `pages/Watchers.tsx:267` (`?watcher=`), `pages/ai/ExchangesPanel.tsx:268` (`?exchange=`), `pages/settings/AiEvalPanel.tsx:386` (`?case=`) | 640 / 680 / 680 | URL | already URL-keyed → `useSheetParam` drop-in |
| 3 | `components/ServiceMapNodeDrawer.tsx:42` (420), `pages/search/SearchDetailDrawer.tsx:58/68` (560) | modal | ServiceMap `?node=`; Search local | entity pivots inside (service / traces / logs / inbox) exercise `pageHref` |
| 4 | `features/anomalies/AnomalyDetailDrawer.tsx:81` (560) | modal | local | detail-page rule "plain sentence first" untouched (visual only) |
| 5 | `components/RolloutDrawer.tsx:57` (760, `?rollout=` codec) | modal | URL | widest; contains `useDataTable` + `KeyValue` — proves nesting |
| 6 | `pages/Clusters.tsx:1239`, `pages/AIObservability.tsx:829` (680) | modal | local / `?source=` partial | big pages, last of the detail set |
| — | `components/ai/{MetricPanelCard,LogSpikeCard,ChartsSignalsCard}.tsx` | CoSRE v1 cards | — | **skip**: CoSRE v2 replaces (audit §9.3) |
| — | `components/CopilotChat.tsx:1051` (v1 chat, `backdrop={false}`, `resizeKey`) | companion | `?chat=` | **skip**: v1 retires with v2; `CosreSheet` is its successor |
| end | delete `ui/Drawer.tsx` + `DrawerSection/TrendRow` + `.drawer-*` CSS | — | — | only when the two v1 skips are gone (CoSRE v2 live); until then `uiImportRatchet` baseline drops by 13 |

`TracePeekDrawer` / `CorrelationContextDrawer` are Modals → they migrate to `coremetry/overlay/Dialog` in the Logs/Trace slices; turning them into side sheets is Q5.

## 3. Context menu on entity cells

### 3.1 Shape — one root per table, items from one pure registry

| Piece | File | Role |
|---|---|---|
| Registry (pure) | `lib/entityPivots.ts`: `type EntityKind = 'service' \| 'pod' \| 'trace' \| 'endpoint' \| 'problem' \| 'statement'`; `pivotsFor(kind, id, ctx: PivotCtx & { service?; cluster?; range }): MenuItemSpec[]` | every item is an existing href producer (§0) wrapped by `withPivotCtx`; table-driven vitest per kind × item (href pins) |
| Primitive | `components/coremetry/EntityContextMenu/EntityContextMenu.tsx` | ONE controlled `coremetry/overlay/ContextMenu` whose `ContextMenuTrigger asChild` wraps the table's `<tbody>` (or the virtual body container). On `onContextMenu` it reads `e.target.closest('[data-entity]')` → `{kind,id}`; **no entity under the pointer → `e.preventDefault()` is NOT called, native menu stays**. Items computed on open only. Zero per-row React nodes: rows carry two data attributes |
| Cell marker | `data-entity="service" data-entity-id="checkout"` on the `<td>` (or the link inside) | rendered by the page's existing cell renderers; a `lib/entityAttrs.ts` `entityAttrs(kind,id)` helper keeps the spelling single (gate-single-spelling lesson) |
| DataTable hook-up | `coremetry/DataTable` **additive** prop `cellMenu?: { ctx(): PivotCtx }` (frozen API → operator approval like G4, Q10); un-migrated `useDataTable` pages mount `<EntityContextMenu>` directly around their own `<tbody>` (they render it themselves — `Services.tsx`, `Traces.tsx` VirtualTable, `Inbox.tsx`, `Endpoints.tsx` each have exactly one) | works in virtual mode (no trailing column needed — lifts the `rowActions` limitation for this feature) |

Menu per entity (existing pivots only; copy = `copyToClipboard` + toast; "Ask CoSRE" = `emitAiPrefill`, hidden when copilot disabled):

| Entity | Items (in this order) |
|---|---|
| service | Open service (`serviceHref`) · Traces (`tracesPivotHref`) · Logs (`logsHref`) · Problems (`serviceInboxHref`) · Ask CoSRE · Pin/Unpin · Copy name |
| pod | Open pod (`podDetailPath`) · Traces with pod filter (`attrTracesHref` k8s.pod.name) · Logs with pod filter (`logsHref`) · Ask CoSRE · Copy name |
| trace id | Open trace (`traceHref`) · Open in new tab · Logs for trace (`logsHref` trace_id) · Copy id |
| endpoint | Open endpoint (`endpointDetailHref`) · Traces (`operationTracesHref`) · Ask CoSRE · Copy name |
| problem / exception group | Open (`problemDisplayHref`) · Traces (`identityTracesHref`) · Ask CoSRE · Copy id |
| DB statement | Open statement (`stmtDetailHref`) · Traces (`statementTracesHref`) · Copy text |

No destructive or state-changing item (ack/mute/resolve stay in `rowActions` ⋯ and on detail pages). Items that would duplicate the cell's own left-click target still appear (first item) — a right-click menu with the obvious action missing reads as broken.

### 3.2 Keyboard + touch

| Input | Behaviour |
|---|---|
| Right-click on a marked cell | opens at the pointer (Radix positions from the `contextmenu` event) |
| Shift+F10 / `ContextMenu` key with a `useTableNav` selected row | `useTableNav` gets `onMenuKey` → dispatches a synthetic `contextmenu` on the row's first `[data-entity]` cell (Radix listens to the same event); menu anchored to that cell. Registered via `useShortcuts` with the table `scope` so two tables arbitrate as today |
| Inside the menu | Radix roving focus, type-ahead, Esc through escBridge (one stack) |
| Touch | **no long-press** (Radix default long-press disabled via `onPointerDown` guard — Q11); the existing ⋯ `rowActions` / row link remain the touch path |
| Non-entity cell | browser's native menu, untouched |

### 3.3 First tables (order) and performance

| # | Table | Entities | Note |
|---|---|---|---|
| 1 | `pages/Services.tsx` | service | has `useTableNav` → keyboard path proven first |
| 2 | `pages/Traces.tsx` (VirtualTable) | trace id, service, endpoint | virtual body — proves the no-trailing-column claim |
| 3 | `pages/Inbox.tsx` | problem/exception group, service, pod | per-row overlay hot spot (audit §6) — this adds **0** per-row nodes |
| 4 | `pages/Endpoints.tsx` | endpoint, service | |
| 5 | `pages/pod/PodTracesTable.tsx`, `pages/service/ServicePodsTable.tsx`, `pages/Clusters.tsx` pod tables | pod, trace id | K8s entity layer |
| 6 | `coremetry/DataTable` `cellMenu` prop | — | once ≥2 migrated consumers exist (today 1) |

Perf acceptance: React DevTools profile of Inbox at 500 rows shows no new per-row components; one `ContextMenu` root per table (test: `querySelectorAll('[data-slot="context-menu-trigger"]').length === tablesOnPage`); the `radix` chunk is fetched with the page chunk, entry unchanged (`radix-in-entry 0`).

## 4. Release split + acceptance

Each row = its own `v0.10.X` (number assigned at tag time), its own `migration-log.md` row with the `bundle-budget.mjs` last line verbatim + a `design:` line. Common gate on every row: `npx tsc --noEmit && npx eslint src && TZ=UTC npx vitest run` (ratchets: `uiImportRatchet` ↓ or =, `inlineStyleRatchet` 0 for new `coremetry/**` files, `overlayImportBan`, `bundleBudget.pin`, `buttonUnity`, `tableUnity`), `npm run bundle:check` → `radix-in-entry 0`, initial ≤ 347 KB gz (today ≈258), `go test ./...` untouched (FE only), `make audit`.

| # | Release | Content | Acceptance (beyond common gate) | Needs |
|---|---|---|---|---|
| P1 | Palette gate + lazy body | `CommandPalette.tsx` → gate (key buffer, idle warm-up); today's body moved verbatim to `coremetry/CommandPalette/CommandPaletteBody.tsx` (still hand-rolled, still `ui/` imports — allowed, count unchanged) | `index-*.js` gz shrinks (recorded delta); `escLayer.contract` green; gate contract test: ⌘K + typed "che" before chunk → body opens with "che" | — |
| P2 | Body on cmdk, grouped | `shadcn/command` + `overlay/Dialog`; `lib/palette/{pages,usePaletteServices,usePaletteEndpoints,identity}.ts`; parity tests §1.1; axe 3 cells; inline style 0 | all §1.1 rows green; `uiImportRatchet` −1 (body no longer imports `ui/`); `rawElementRatchet` unchanged | Q1, Q3 |
| P3 | New actions | §1.2 list (only the approved ones); `lib/density.ts` setter extraction | each action a pure test (`copy-link` href frozen range; theme/density storage key unchanged) | Q2 |
| S1 | `DetailSheet` composite | `coremetry/DetailSheet/*`, `useSheetParam`, `SheetSection/TrendRow`, catalog demo, contract tests §2.2 | contract + axe green; no consumer yet | Q8, Q9 |
| S2 | External + Hosts | §2.3 #1 | visual diff reviewed on prep; `uiImportRatchet` −2 | — |
| S3 | Watchers + ExchangesPanel + AiEvalPanel | §2.3 #2 on `useSheetParam` | Back closes sheet (test); −3 | — |
| S4 | ServiceMapNodeDrawer + SearchDetailDrawer | §2.3 #3, `pageHref` | pivots inside unchanged (href tests); −2 | Q6 (Search URL key) |
| S5 | AnomalyDetailDrawer | §2.3 #4 | detail-page memory rule respected (plain sentence first) ; −1 | — |
| S6 | RolloutDrawer | §2.3 #5 | nested `useDataTable` + `KeyValue` render; −1 | — |
| S7 | Clusters + AIObservability | §2.3 #6 | −2 (Drawer importers left: 4 CoSRE v1 files) | — |
| C1 | `lib/entityPivots.ts` + `entityAttrs` | pure registry, table-driven href pins per kind | 100% of items point at an existing producer (test imports them) | Q7 |
| C2 | `EntityContextMenu` + Services | primitive, catalog demo, Services `<tbody>` wrap + `data-entity` on service cells, Shift+F10 via `useTableNav` | one root per table test; keyboard test; axe | Q6a, Q11 |
| C3 | Traces + Endpoints | virtual body + endpoint/trace cells | virtual: no trailing column, menu opens on row 1 and row 5000 | — |
| C4 | Inbox | problem/service/pod cells | profile: 0 new per-row components at 500 rows | — |
| C5 | Pod tables | `PodTracesTable`, `ServicePodsTable`, Clusters pods | | — |
| C6 | `DataTable.cellMenu` | additive frozen-API prop; `SpanClusterValuesPanel` + next migrated table adopt | frozen-API note in `coremetry/DataTable/types.ts` | Q10 |

Order: P1 → S1 → C1 → C2 (three independent tracks may interleave; P2 waits for Q1/Q3, P3 for Q2). Checkpoints: stop after P2 and after S2 and show the operator on prep before continuing the track. A release that moves initial-load above 347 or puts anything Radix/cmdk into the entry does not tag.

## 5. Operator questions (behaviour / IA only)

Visual choices (group headers' look, menu styling, sheet chrome) are free per audit §13 and not asked. Recommended default in italics; nothing below ships before the answer.

| Q | Question | Recommended default |
|---|---|---|
| Q1 | Palette results get **group headers** (Pinned/Recent, Services, Endpoints, Operations, Pages, Actions, Search). Order is today's rank order, so only the headers are new. OK? | *yes* |
| Q2 | Which **new palette actions** ship (§1.2): copy-link, ask-cosre, theme ×3, density ×3, shortcuts-help, pin/unpin-service? Anything that changes server state stays out unless named here. | *all six; refresh-interval stays out* |
| Q3 | In action prompt mode, **Esc returns to the result list** (today Esc closes the palette). Change, or keep close? | *return to list (Raycast/Linear convention); second Esc closes* |
| Q4 | First ⌘K of a session may wait one same-origin chunk fetch (idle warm-up makes this rare); keystrokes typed meanwhile are kept. Accept, as for the toast and CoSRE gates? | *accept* |
| Q5 | `TracePeekDrawer` / `CorrelationContextDrawer` are centered modals today. Keep as dialogs (migrate in Logs/Trace slices) or convert to side sheets? | *keep as dialogs — IA unchanged* |
| Q6 | 9 drawer sites hold open-state locally (External, Hosts, Search, Anomaly detail, Clusters, AIObservability …). Move them to a URL param (shareable, Back closes) during migration, or keep local? If URL: `?detail=<id>` per page, param name confirmed per site. | *URL — matches "URL is source of truth"; one param name per page listed in each release* |
| Q6a | Right-click is intercepted **only** on marked entity cells; everywhere else the browser menu stays. OK? | *yes* |
| Q7 | Context-menu item sets per entity (§3.1 table), including "Ask CoSRE" and "Pin/Unpin service". Remove or add anything? | *as listed* |
| Q8 | All detail sheets stay **modal** (backdrop, as today); only CoSRE is companion-mode. Any detail sheet that should become non-modal? | *none — unchanged* |
| Q9 | **Resize handle** on every detail sheet (today only CoSRE chat has one); width persisted per sheet. Enable, or CoSRE-only? | *enable on all (persisted per `resizeKey`)* |
| Q10 | `coremetry/DataTable` frozen API gains additive `cellMenu` (C6), like the G4 `detail` precedent. Approve? | *approve, additive only* |
| Q11 | Touch **long-press** to open the context menu (Radix default ~700 ms) — enable, or keep ⋯ as the only touch path? | *off; ⋯ remains* |
| Q12 | Palette state-changing actions (ack / disable / enable rule / save view / mark event / silence) stay exactly as today inside the cmdk body — confirm no change wanted while moving them. | *keep 1:1* |

## 6. Not in this slice

| Item | Why | Where it belongs |
|---|---|---|
| `rowActions` ⋯ in virtual mode | body decision (TanStack vs `VirtualTable`) is the Traces slice's | audit §11 g |
| Converting peek modals to sheets | IA change (Q5 default no) | Logs / Trace slices as `overlay/Dialog` |
| CoSRE v1 drawer + 3 ai/* card drawers | replaced by CoSRE v2 (`CosreSheet`) | CoSRE v2 track |
| New palette data sources (logs, dashboards, users …) | new search surfaces = IA | separate operator request |
| Context menu on charts / topology nodes / waterfall rows | not tables; rendering layer rules | chart-layer workstream |
| Hover kebabs / `data-row-action` hand-written sites (audit §4.4) | migrate with their page via `coremetry/DataTable.rowActions` | page slices |
| Tooltip (`title=` ~2,134) | decided out of scope (audit §11 l) | — |
| Deleting `ui/Drawer.tsx` + `.drawer-*` CSS | 4 CoSRE v1 importers remain until v2 is live | final cleanup |

## Operator approval (2026-10-30)

Approved with all recommended defaults (§ questions). Lean execution: no security scans / CI watching.
