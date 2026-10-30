# Global shadcn/ui + Tailwind migration — Phase 0 audit (2026-10-29)

Previous version of this file (2026-09-25 Option B / button-unity audit, shipped v0.10.919–927) is in git at `c57245f3`; it is superseded by this document.

Status: **Phase 0 complete — STOP, waiting for operator approval** (decision 5). Inputs: operator brief + 12 final decisions (scratchpad `shadcn-operator-decisions.md`), three read-only sweeps (stack / atoms / pages), `docs/cosre/chat-ui-audit.md` (built on, not redone). Every load-bearing number below is from a sweep or re-read in the repo (file:line). Snapshot: `main` @ v0.10.1591.

## 0. Operator decisions (final; they override the brief)

| # | Decision | Where it lands below |
|---|---|---|
| 1 | No PRs. Every release adds an entry to `docs/frontend/migration-log.md` (page, what changed, deleted atoms). Gate = `/design` route + vitest/axe; Playwright manual, only on a marked visual change | §9.1, §9.4 |
| 2, 11 | ECharts not in repo (verified: not in `package.json`; only a comment in `AIObservability.tsx:779`) → rule: **charts: uPlot only, rendering untouched**. Theme token export applies to the chart palette | §5, §9.2 |
| 3 | Supersedes CoSRE decisions 1 and 3 (recorded `7ecdd886`: `docs/cosre/chat-ui-audit.md` §0, `docs/DECISIONS.md:12031`). CoSRE v2 frontend builds on `components/shadcn` + `components/ai-elements` after Phase 1; CoSRE Phase 1 (Go stream adapter) continues independently | §9.3 |
| 4 | Sweep conflicts: one value per table, reason stated | §2/§3 headers |
| 5 | After Phase 0: stop and wait | this file |
| 6 | React 19 = separate release **before** Phase 1 ("Phase 0.5"); only React + `@types` + the pin test. Executed now by a separate agent | §9.0 |
| 7 | Theme default collapses to ONE source (`lib/theme.ts`) in Phase 1. Grafana bridge redhat→dark bug (`lib/chart/dataFrame.ts:50-53`) fixed NOW, separately | §1.3, §9.5 |
| 8 | `components/shadcn` + `components/ai-elements` exempt from the raw-element ban. Ratchet = "imports into `components/ui`", may only go down | §8, §9.2 |
| 9 | CoSRE lazy: immediate independent release — `AppShell.tsx:8` static `CopilotChat` import → `React.lazy` + route split | §7, §9.5 |
| 10 | Bundle budget: by end of Phase 2 the real entry page's initial load is **< 347 KB gz** (today 346.6) | §7, §9.4 |
| 12 | Old audit replaced; git pointer suffices | header |

## 1. Stack

### 1.1 Build, language, CSS

| Item | Finding | Evidence |
|---|---|---|
| Build | Vite 8.3.4 (rolldown) with `advancedChunks`; `@vitejs/plugin-react ^6.1.2`; visualizer behind `ANALYZE=1` | `vite.config.ts:51-84`, `:14-27` |
| React / TS | react + react-dom **18.3.1 exact**, `@types/react` 18.3.5, TS ~5.9 | `package.json:37-38,46,56` |
| React pin | `depPins.test.ts:32-34` fails if React ≠ 18.x or `@grafana/ui` ≠ 13.1.x. Reason in the test header: `@grafana/ui ^13.1.2` caret reached 13.2.x which peers React ≥19; the test pins 13.1.x + React 18 so a clean install cannot drift | `src/lib/chart/depPins.test.ts:5-14` |
| Second build | `build:ds` packages `src/components/ui/index.ts` into `dist-ds/` (ES module + `.d.ts`) for design-sync | `vite.lib.config.ts:14-20`, `package.json:17` |
| CSS | One global file, **6,436 lines** (`src/styles/globals.css`) + `pages/design/design.css` (20). No CSS modules, no Tailwind, no PostCSS config | `wc -l` |
| Inline styles | 5,309 `style={{` vs 4,602 `className=` across 492 TSX. Top: `AdminClickhouse.tsx` 273, `settings/AnomalyTab.tsx` 178, `adminstats/panels.tsx` 124, `settings/DevOpsTab.tsx` 109 | sweep stack §1 |
| CSS-in-JS | **None shipped.** `@emotion/*` only transitive via `@grafana/ui`; built maps contain no emotion / floating-ui / react-aria / react-select. → Tailwind makes **no runtime redundant** (brief item 7 answered: nothing to remove) | sweep stack §1 |
| `@grafana/ui` | 13.1.2 exact; imported in `components/chart/CorePanel.tsx:33-37`, `explore/QueryPanel.tsx`, `dashboard/panelViz.ts`; `@grafana/data` in `CorePanel.tsx:38` + `lib/chart/dataFrame.ts:34-39` | sweeps stack §1, pages §0 |
| Alias | `@/*` → `src/*` | `tsconfig` paths + `vite.config.ts:31` |
| Headless libs | **None direct**: no `@radix-ui`, `@base-ui`, `cmdk`, `sonner`, `clsx`, `cva`, `tailwind-merge`, `axe`, `testing-library` (verified `package.json` grep). Tooltip/Popover hand-rolled (`lib/tipPlacement`, `lib/escLayer`); Modal/Drawer use `createPortal` | sweep stack §1 |
| TanStack | `react-virtual ^3.13` direct (`ui/VirtualList`, `ui/DataTable/VirtualTable`, `TraceWaterfall`); `react-query ^5`. **TanStack Table NOT present** | `package.json:32-33` |
| Icons | `lucide-react ^1.59.0` direct: 30 files, ~72 named imports (68 icons in build). Also hand-rolled `components/icons.tsx` (199 lines), `RuntimeIcons.tsx`, `TelescopeIcon.tsx`, unicode glyphs (`theme.ts:9`) | `package.json:36`, sweep stack §1 |

### 1.2 Tokens

| Item | Finding | Evidence |
|---|---|---|
| `:root` | ~79 unique names (85 lines; duplicate decls `--accent-hover`, `--accent2`, `--backdrop`, `--fatal`, `--on-accent`, `--shadow-modal`; `--text-2` beside `--text2`). Surfaces `--bg0..3`, `--surface`, `--field-bg`; borders `--border/-strong/-control`, `--divider`; text `--text/2/3/-faint`; `--accent*`, `--on-accent`, `--brand/2`, `--focus`; shadows sm/md/pop/modal; 19 `--z-*` | `globals.css:1-417` |
| Themes | dark = `:root`; `[data-theme="light"]` :246 (42 decls); `[data-theme="redhat"]` :318 (46 decls, own radius scale 2/3/4/8 vs 4/6/8/12); `[data-theme="redhat"] #sidebar` :391-417 dark-rail remap. No `.dark` class | `globals.css` |
| Status | `--ok --err --warn --info --fatal`, `--ok-bg --warn-bg --err-bg --info-bg`, `--warn-solid --err-solid`; dark :74-87,139; light :272-281,303; redhat :347-356,373 | `globals.css` |
| Spacing / type / radius | `--sp-1..8` = 2/4/6/8/10/12/16/24 (:167-174); `--fs-3xs..xl` = 9/10/11/12/13/14/18, body 12px (:176-182); `--radius-*` :147-150 & :376-379; `--row-h` 36px, `--hit-min` 24px | `globals.css` |
| Component-scoped | `--kv-*` :2594-2596; ~30 `--cosre-*` :5656-5686 with its own 4/8/12/16/24 scale | `globals.css` |
| Hex / rgb literals outside token files | 126 hex + 108 rgb(a) in non-test TS/TSX. Top: `DependenciesTable.tsx` 16 hex/17 rgb, `lib/flameDiff.ts` 13, `dependencies/panels/shared.tsx` 11, `lib/chartFmt.ts` 8, `viz/TimeSeriesPanel.tsx` 8, `lib/chart/heatmapRamp.ts` 7 rgb. `globals.css` 157 hex, gated by `src/styles/colorLeaks.test.ts` (:77, :136 allowlists) | sweep stack §2 |
| Token vs literal ratio | globals: font-size 299 var / 260 px; radius 155 / 80; spacing 498 / 503. TSX inline: fontSize 43 var / 2,156 numeric; borderRadius 557 sites / 180 files outside the gate | sweep stack §2 |
| Style gates | ~32 tests in `src/styles` (geometryTokens, radiusTokens, surfaceTokens, contrastTokens, fieldBg, zLayers, undefinedCssRefs, colorLeaks, …) | sweep stack §2 |

### 1.3 `data-theme` — NOT single-source today

| Role | Site | Fallback |
|---|---|---|
| Writer | `index.html:2` hardcodes `data-theme="redhat"` | — |
| Writer | boot script `index.html:44-57` (`localStorage coremetry-theme`) | redhat |
| Writer | `lib/theme.ts:45` `cycleTheme` (ThemeToggle.tsx:7), `:55` `applyTheme` (Sidebar.tsx:18) | — |
| Reader | `currentTheme()` `theme.ts:38-39` | redhat |
| Reader | `chartTheme()` `lib/chartFmt.ts:193-195` | dark |
| Reader | Grafana bridge `lib/chart/dataFrame.ts:50-53`: `isDark = attr !== 'light'` → **redhat treated as dark**; redhat is a light theme (`--bg0: #f0f0f0`, `globals.css:319`; `THEME_COLOR.redhat '#f0f0f0'`, `theme.ts:21`) | — (bug; decision 7 fix in flight) |
| Reader | `lib/useThemeTick.ts:24` MutationObserver → chart redraw | — |
| CSS | `:root` with no attribute = dark | dark |

Theme-color map duplicated `index.html:44` + `theme.ts:21`. Phase 1 target: `lib/theme.ts` is the one reader/writer and the one fallback; `index.html` boot script keeps only the pre-paint attribute set.

## 2. Atom inventory — `components/ui` (metric: **importing files**, non-test, outside `components/ui`)

Measured: `components/ui` has **45 non-test entries** = 40 atom files (incl. `useFocusTrap.ts`) + `index.ts` barrel + 3 helpers (`buttonGroupContext.ts`, `drawerWidth.ts`, `emptyAreaClose.ts`) + `DataTable/` module (13 files). The brief's "45 atoms" is this entry count. Several atom files export more than one component (Field → SelectField/TextareaField, Menu → MenuItem, Popover → Link/Href items, Drawer → Section/TrendRow, ConfirmDialog → Provider/useConfirm, Stack → Row). 20 `*.contract.test.tsx` + 8 gate tests (`primitiveClasses`, `buttonStateRatchet`, `destructiveConfirm`, `searchFieldAdoption`, `sectionAtomsGate`, `statTileGate`, `noNativeConfirm`, `actionOrder`) pin class names / DOM shape.

| Atom | Files | Props surface (ours) | shadcn equivalent | Risk | Required extension (what shadcn lacks) |
|---|---|---|---|---|---|
| Button | **209** (+3) | `variant` required primary/secondary/danger/ghost/accent/ghost-danger; size xs/sm/md/lg; loading, leftIcon, rightIcon, active; forwardRef; `type=button` default | `button` | high | loading+`aria-busy`, active→`aria-pressed`, xs, accent, ghost-danger, size from ButtonGroup context; primary styled by bare `button {}` (`globals.css:634`) — no class |
| IconButton | 49 (+1) | icon, `aria-label` type-required; variant secondary/ghost/bare/danger; size xs/sm/md; tooltip, tooltipSide | `button size=icon` + tooltip | med | built-in tooltip, type-enforced label, bare variant, group size |
| LinkButton | 31 | tone accent/muted; underline hover/dotted/none | `button variant=link` | low | muted tone, underline modes |
| MenuItem (Menu.tsx) | 14 | icon, danger; role=menuitem | `DropdownMenuItem` | med | used inside our Popover kind=menu AND free-standing |
| OptionRow | 4 | selected→aria-current | `CommandItem` | low | — |
| TileButton | 1 | native | none | low | class wrapper |
| Chip | 36 | active, tone, size, pill, onRemove | `toggle` / `badge` | med | removable ×, non-interactive label without onClick |
| DisclosureButton | 31 | expanded (controlled), anatomy row/section | `CollapsibleTrigger` | low-med | controlled only, two anatomies |
| ButtonGroup | 7 | `aria-label` required, attached, size via context | `button-group` | low | size inheritance |
| Badge | 62 | tone neutral/info/success/warning/danger/accent → `.badge .b-*` | `badge` | med | ~139 files use `.badge .b-*` classes directly; density shrink |
| MethodBadge | 3 | method; `methodTone()`, `normalizeMethod()` | `badge` variant | low | method colour map |
| PriorityBadge | 8 | p P1/P2/P3, reason→title | `badge` | low | — |
| Card / CardLink | 19 | header, footer; router LinkProps | `card` | low-med | router card, density padding |
| StatTile | 13 | label, tone, onClick→button | none | low | 3 local Stat copies should merge here |
| Drawer (+Section, TrendRow, drawerWidth, emptyAreaClose, useFocusTrap) | 15 | onClose, header, width, backdrop, closeOnEmptyClick, resizeKey; mount-controlled | `sheet` (right) | **high** | persisted drag-resize + dbl-click reset, non-modal mode (no backdrop/trap, below-nav z), empty-area close, Esc stack, focus restore, scroll lock, ≤640px fullscreen |
| Modal | 23 (+1) | open, onClose, title, footer, initialFocus selector, size sm/md/lg; portal | `dialog` | med | initialFocus, Esc stack; hardcoded `id="modal-title"` (two modals clash) |
| ConfirmProvider / useConfirm | 35 | `useConfirm()(opts)→Promise<boolean>`; danger | `alert-dialog` | med | imperative promise API, pending request resolves false, Cancel-first focus, Turkish defaults; `noNativeConfirm` + `destructiveConfirm` gates |
| Tooltip | 6 (+IconButton ⇒ ~55 effective) | content, child, side, delay 500 | `tooltip` | med | pointer/focus-visible only, 100 ms grace, hover-stays, hide when clipped, suppress `title`, aria dedup, no Provider, redhat font |
| Popover (+LinkItem, HrefItem) | 11 (+1) | external `anchorRef`, open, onClose, kind menu/dialog, placement, initialFocus | `popover` + `dropdown-menu` | **high** | external anchor (row ⋯ menus), roving keys, Tab closes, blur close, router Link items, not portaled |
| PickerPopover | 2 (via Combobox 19) | anchorRef, items (recent group), controlled highlight, query, count, status, footer; portal | `command` in `popover` | **high** | server search in parent, recent group, live count header, match highlight, layer above dialogs, mousedown keeps focus |
| FacetMultiSelect | 2 | options with counts, selected Set, onSolo, onAll | `dropdown` checkbox / `command` | med | solo, cannot uncheck last, counts |
| ColumnsPicker | 2 | pinned/removable items, onMove/onRemove/onReset | none | low | built on our Popover |
| Field / SelectField / TextareaField | 37 | label, hint, error + native | `input`/`select`/`textarea`/`label` | med | `aria-describedby`/`aria-invalid` via `useId`; **SelectField is native `<select>`** (OQ b); 7 local copies |
| SearchField | 11 | controlled value/onChange(string), hint kbd, width | `input` (+input-group) | low | clear ✕, icon, kbd slot; `searchFieldAdoption` gate |
| EnterHint | 3 | title | `kbd` | low | — |
| SegmentedControl | 35 | generic value/options, aria-label, size, activation auto/manual | `toggle-group` single | med | radiogroup semantics (shadcn = aria-pressed buttons), manual/auto, roving skips disabled |
| TabStrip | 17 | generic tabs/value/onChange, trailing children; no panels | `tabs` (TabsList only) | med | panel-less, arrow activation, trailing slot, URL-driven |
| KeyValue / KeyValueRow | 14 | items, labelWidth; k, v, mono, actions | none | low | `—` for empty, actions, density vars |
| SectionHead / PanelTitle / SectionUnavailable | 12 / 10 / 3 | id, title, badges, meta, actions / sub, right / what | none / none / `alert` | low | inline token styles |
| ActionRow | 8 | destructive, secondary, confirm slots | none | low | enforces order (`actionOrder` test) |
| PageShell | 51 | variant default/full; `id=content` | none | low | single scroll container (`--controls-h`, sticky) — keep |
| PageControls | 17 | children, sticky, actions | none | **high** | publishes `--controls-h` via ResizeObserver, ≤640px "Filters (N)" collapse, Esc layer, density on `.controls`; 3 large tests |
| Stack / Row | 23 | gap 1/2/3/4/6, justify, wrap, grow | Tailwind flex | low | — |
| VirtualList / RouteSkeleton / SafeHref / IdentityChip / useFocusTrap | 1 / 1 / 12 / 1 / 2 | — | `scroll-area` (partly) / `skeleton` / none (keep) / composite / Radix built-in | low | SafeHref is a URL-safety util, stays |
| DataTable module (useDataTable, DataTableHead, Colgroup, ColResizeHandle, DataTableCell, DataTableState, VirtualTable, BulkBar, MiddleEllipsis, HeadMenu + `lib/dataTable`, `lib/columnModel`, `lib/rowSelection`) | **126** (useDataTable 122, DataTableState 117, DataTableHead 111, DataTableCell 71, VirtualTable 17, MiddleEllipsis 8, BulkBar 2) | storageKey, columns, rows, initialSort, persistSort, sortIds, sortUrlKey, serverSort/serverSortKey, onOpen, columnModel, reorderable, selection, getRowHref, rowLinkReplace | `table` (+TanStack recipe) | **high** | URL sort with sig guard, persisted widths/sort, pointer+keyboard resize, reorder/hide via URL column model, range selection + BulkBar, server sort/paging, row-as-link, header menu, middle-ellipsis, density row heights, sticky under `--controls-h` (OQ g) |

Highest-risk atoms: Button (209 files + global `button {}`), DataTable, Popover + PickerPopover/Combobox, Drawer, PageControls.

### 2.1 Atoms outside `components/ui` (importing files)

| Family | Files | shadcn / composite target |
|---|---|---|
| Spinner / PageLoader / LoaderMark / Empty (`components/Spinner.tsx`) | 155 (Spinner 141, Empty 87) | `spinner` + `empty` (keep API) |
| Combobox (wraps PickerPopover) | 19 | `command` in `popover` |
| ServicePicker / OperationPicker / MetricNamePicker / TeamPicker / EnvPicker / RolloutWorkloadPicker | 25 / 3 / 3 / 2 / 2 / 1 | built on the Combobox replacement; server search stays |
| TimeRangePicker (own `role=dialog`) / RefreshPicker | 2 / 2 | `coremetry/TimeRangePicker` |
| CopyButton / Pager / QueryError / CommandPalette | 18 / 7 / 12 / 2 | button / pagination / alert / `coremetry/CommandPalette` |
| Toaster + `lib/toast.ts` | 1 + **21** (grep excluding tests; the atom sweep's 22 included a test) | OQ h |
| Skeleton family (`components/Skeleton.tsx`) | 7 | `skeleton` |
| KindBadge / AlertSourceBadge / ServiceRuntimeBadge | 4 / 3 / 2 | `coremetry/StatusBadge` variants |
| ThemeToggle / DensityToggle / LangToggle / ClusterModeToggle | 4 / 1 / 1 / 2 | dropdown-menu items in the shell slice |
| SavedViewsBar / Sparkline / LazyMount / TrendDelta / SubjectLink / ShareButton / DrillButton / FilterQueryBox | 10 / 10 / 8 / 7 / 5 / 3 / 2 / 3 | FilterBar (SavedViewsBar, FilterQueryBox); rest unchanged |
| ColumnManager | 2 | duplicates `ui/ColumnsPicker` → one `coremetry/DataTable` column menu |

Local duplicates to retire in the owning page's Phase 2 slice:

| Copy of | Sites |
|---|---|
| Field ×7 | `pages/settings/shared.tsx:16`, `ServiceCatalogPill.tsx:240`, `dashboard/PanelEditor.tsx:686`, `AdminStatusPage.tsx:309`, `Runbook.tsx:701`, `Alerts.tsx:849`, +1 |
| Stat ×3 | `dependencies/panels/shared.tsx:55`, `DBQueriesPanel.tsx:370`, `topology/FocusedNeighborhood.tsx:678` |
| Chip / Pill / KV ×6 | `ServiceNeighbors.tsx:218`, `traces/TraceHonesty.tsx:136`, `ServiceMap.tsx:528`, `ServiceCatalogPill.tsx:123`, `SpanDetail.tsx:606`, `AIObservability.tsx:901` |
| Raw CSS-class atoms (files) | `.badge` 139, `.b-gray` 74, `.b-warn` 51, `.b-err` 49, `.b-info` 32, `.card` 43, `.sec` 51, `.empty` 18, `.controls` 16, `.pill` 12 |

## 3. Page / component inventory by category (metric: **JSX sites**, `pages/` + `components/` (non-ui) + `features/`, tests excluded; multi-line JSX under-counted)

| Category | Atoms (sites) | Raw / hand-rolled (sites) | Evidence |
|---|---|---|---|
| Buttons | Button 690, IconButton 83, LinkButton 35, DisclosureButton 36, ButtonGroup 12, OptionRow 5 | raw `<button>` **3** (`TopbarSearch.tsx:29`, `TimeRangePicker.tsx:285` calendar day, `Sidebar.tsx:450`); `role="button"` 5 (`Dashboard.tsx:819`, `chart/PanelLegend.tsx:76`, `TopologyFlowGraph.tsx:612/723/766`); `<a role=button>` 0. All sit inside the **9 reasoned line exemptions** (`grep eslint-disable-next-line ui/no-raw-button` = 9; ratchet `MAX_REASONED_EXEMPTIONS 9`, `MAX_RAW_BUTTONS 0`, `buttonUnityRatchet.test.ts:34-36`) | ratchet + sweep pages §1 |
| Tables | DataTableHead 151 of 203 `<table>` (116 files); DataTableState 278 | ~52 bespoke `<table>` = `tableUnityRatchet` `rawTable` ceiling 52 (`:31-161`); `tdStyle` 60; `containIntrinsicSize` 3 | sweep pages §1, ratchet |
| Forms | Field 339, SelectField 31, TextareaField 10, SearchField 10, Combobox 21, FacetMultiSelect 7 | `<input>` 502 / 120 files (checkbox 127, number 96, password 16, text 12, file 6, radio 5, other 13, untyped ~227); `<select>` 167 / 71; `<textarea>` 36 / 21; `<dialog>` 0. Offenders: AnomalyTab 73, LdapTab 38, ChannelModal 30, `dashboard/PanelEditor` 38, Runbook 18, MetricQueryEditor 18, DevOpsTab 17 (page-sweep figures — same scope as the rest of this table) | sweep pages §1 |
| Overlays | Modal 34, Drawer 12, Popover 15, PickerPopover 1, Tooltip 13, MenuItem 33, useConfirm (35 files) | hand-rolled dialogs 12, menus 6, listboxes 4 (table below); `createPortal` outside ui: TourOverlay, SparkReadout; `title=` ~2,134 vs 13 `<Tooltip>` | sweep pages §1 |
| Navigation | TabStrip 16, SegmentedControl 35, Pager (7 files) | Sidebar (`components/Sidebar.tsx`, 9.5 KB raw); Settings side nav `.sys-subnav` NavLinks `Settings.tsx:130`; breadcrumbs without an atom: `Service.tsx:487`, `Trace.tsx:610`, `TriageCrumb.tsx`, `FlameGraph.tsx:34`; CommandPalette 1 impl (`components/CommandPalette.tsx`, 847 lines, mod+k :247, own dialog :613 + listbox :779) | sweep pages §1, §5 |
| Feedback | Spinner 198, Empty 151, Skeleton 15, QueryError 15, SectionUnavailable 15, StatTile 72, Badge 137 + PriorityBadge 9, `toast(` 50, FlashBox 52 | raw `className="badge b-*"` ~523 (vs 137 `<Badge>`) | sweep pages §1 |

Hand-rolled overlay sites (each migrates in its page's slice; shadcn target in the header):

| `role=dialog` / fixed backdrop → `dialog`/`sheet` | `div role=menu` → `dropdown-menu` | listbox → `command` |
|---|---|---|
| `settings/ChannelModal.tsx:164` | `Dashboard.tsx:628` | `FilterQueryBox.tsx:327` |
| `settings/LdapUserPicker.tsx:121` | `Trace.tsx:1594` | `viz/MetricQueryEditor.tsx:1183` |
| `settings/ZoomChannelPicker.tsx:147` | `explore/RecentQueries.tsx:70` | `ai/NameCompletionPopup.tsx:16` |
| `Slos.tsx:246` | `MetricPanel.tsx:236` | `CommandPalette.tsx:779` |
| `HeatmapCellExemplars.tsx:141` | `Sidebar.tsx:535` | |
| `dashboard/PanelEditor.tsx:105` | `chart/CorePanel.tsx:1209` | |
| `chart/CorePanel.tsx:1180` (fullscreen) | | |
| `Trace.tsx:1258` (share) | | |
| `MetricPanel.tsx:323` | | |
| `TimeRangePicker.tsx:229` | | |
| `CommandPalette.tsx:613` | | |
| `tour/TourOverlay.tsx:129` | | |

### 3.1 Target page groups (per-group counts: raw button/input/select/textarea/table | key atoms | badge raw/atom | inline `fontSize` < 13)

| Group (files) | Raw | Atoms | Badge | fs<13 | Notes |
|---|---|---|---|---|---|
| **Settings** (62 + `Settings.tsx`) | 0/320/38/12/28 | Button 173, Field 187, SelectField 16, Spinner 45, DataTableHead 17, Modal 4 | 47/24 | 392 (1,181 `style={{`) | Phase 2 slice 1 |
| **Remote Cluster** (`settings/ClustersTab.tsx`, `SpanClusterValuesPanel.tsx`) | 0/10/2/0/1 | Button 7, Field 3, Combobox 1, Badge 3 | 3/3 | 14 | raw fields `ClustersTab.tsx:359,375,383,396,419,426,444,489,495`; `SpanClusterValuesPanel.tsx:66,72,90`; Phase 2 slice 1 pilot |
| /clusters (10) | 0/3/2/0/4 | IconButton 2, Drawer 1, TabStrip 1, DataTableHead 4 | 20/0 | 41 | `Clusters.tsx` 1,363 lines, 23 numeric cols |
| **Log search** (15: `pages/Logs.tsx`, `pages/logs/*`, `LogTable.tsx`, LogsHistogram, LogFieldsPanel, LogPatternsPanel, LogPillEditor, LogContextModal, KqlSearchInput) | 0/11/8/1/3 | Button 48, IconButton 12, Popover 5, MenuItem 4, TabStrip 2 | 15/0 | 37 | Phase 2 slice 2 |
| **Trace view** (19: `pages/Trace.tsx` 1,655, `pages/trace/*`, `TraceWaterfall.tsx`, `SpanDetail.tsx`, SpanK8sSection, TracePeekDrawer, FlameGraph) | 0/3/1/0/7 | Button 20, IconButton 8, Tooltip 6, Chip 10, Popover 3, MenuItem 5 | 11/6 | 54 | Phase 2 slice 3 — **not first** |
| Traces list (3: `pages/Traces.tsx` 2,237, `pages/traces/*`, `components/traces/*`) | 0/10/3/0/1 | Button 14, SegmentedControl 3 | 8/0 | 36 | with slice 3 |
| **Metrics explorer** (16: `/explore` `pages/Explore.tsx` + `pages/explore/*` embedding `MetricsExplorer.tsx:1072` / `LogsExplorer.tsx:1079`; `/metrics` `pages/Metrics.tsx` + `viz/MetricQueryEditor.tsx` + `TimeSeriesPanel.tsx`) | 0/3/6/1/4 + 0/0/0/0/1 | Button 17+2, SegmentedControl 6, Combobox 2+1, SearchField 1 | 1/0 + 0/2 | 57 + 16 | Phase 2 slice 4; `@grafana/ui` in `explore/QueryPanel.tsx` |
| **CoSRE** (37 `components/ai/*` + `pages/CoSRE.tsx` wrapper + `CoSREShared.tsx` + `CopilotChat.tsx` 1,098 lines) | 0/1/0/4/9 | Button 51, Chip 27 | 23/5 | 33 (144 `title=`) | separate track: v2 on `components/ai-elements` after Phase 1 (decision 3) |

## 4. Cross-page duplication

| Concern | Implementations today | Phase 1 composite |
|---|---|---|
| Time range | 1 global + ~10 local window selectors + 2 compare presets + 5 brush handlers | `coremetry/TimeRangePicker` (global + `window` variant fed by one preset table) |
| key=value filtering | ~12 chip editors + 2 inline in/out action groups | `coremetry/FilterBar` (chip model + editor + in/out actions) |
| Status colouring | 1 declared source used by 2 files; ~329 ternaries, 326 inline vars, ~30 threshold functions, 9 duplicate badges | `coremetry/StatusBadge` + one `tone()` map in the tokens file; chart palette reads the same tokens (§5) |
| Row actions | 6 kebabs (2 shared-anchor, 4 per-row/hand), 6 hover-group CSS classes, 7 hand-written `data-row-action` | `coremetry/DataTable` row-action slot on `dropdown-menu`, one shared anchor per table |
| Density | 1 global toggle + 5 independent row ladders + 5 per-component `compact` props | density context (§9.2) feeding shadcn size variants; waterfall/flame ladders stay (rendering) |

### 4.1 Time range

- Global: `components/TimeRangePicker.tsx`; presets `lib/rangePicker.ts:19`; URL `lib/useUrlRange.ts` (26 callers); mounted `Topbar.tsx:62` + `ContextBar/ContextBar.tsx:94`.
- Local window selectors: `clusters/TrendPanel.tsx:14` TREND_WINDOWS (`Clusters.tsx:1248`); `AdminClickhouse.tsx:233` COORD_WINDOWS (raw `<select>` :614); `Inbox.tsx:354` SINCE_OPTS; `AdminAudit.tsx:198` inline; `dashboard/PanelEditor.tsx:190-196` per panel; `alerts/constants.ts:61` WINDOWS (`Alerts.tsx:591`, `RouteAlertModal.tsx:66`, `KafkaAlertModal.tsx:75`); `shift/shiftWindow.ts:22` (`Shift.tsx:110`); `trace/traceMetricsModel.ts:10` (`TraceMetricsPanel.tsx:386`); `settings/oracleProbe.ts:8` (`OracleTab.tsx:544`).
- Compare: `ServiceMap.tsx:44/:359` DIFF_PRESETS; `charts/CompareToggle.tsx:10` (`Pod.tsx:420`, `service/Overview.tsx:1049`, `ServiceCharts.tsx:522`).
- Brush: `onZoom` in CorePanel / TimeChart / OverviewChart / TimeSeriesPanel; `onRangeSelect` in LogsHistogram.

### 4.2 key=value filtering

- Logs: `logs/LogsFilterRow.tsx:119` ld-pill + LogPillEditor (+ `LogsActionsMenus.tsx:101`), KqlSearchInput, level chips `Logs.tsx:1053`.
- Traces / Explore: `FilterQueryBox` fq-chip :307 (Traces, Explore, `explore/QueryRow`); `FilterGroupBuilder` → `FilterBuilder` fb-chip :97-99 (Traces, QueryRow); `traces/ColumnFilter` (`Traces.tsx:1218`).
- Metrics: `viz/MetricQueryEditor` mqe-filters :327 / mqe-chip :341; `explore/SplitByPicker`; `dashboard/VariablesBar`.
- Others: FacetMultiSelect (Inbox, `search/FacetRail`); srch-chips `Search.tsx:204`; pod-chips `pod/PodTracesTable.tsx:97`; tpp-chips `trace/TracePodPanel.tsx:283`; svc-kafka-filters `service/ServiceMessagingTab.tsx:111`; ClusterChips (3 anomaly files); cm-scope-chips `CopilotChat.tsx:991`; pb-pill `anomalies/ExternalEvidencePanel.tsx:147`.
- Inline filter in/out: `LogTable.tsx:449` kv-actions, `SpanDetail.tsx:800/804` ps-attr-act. SavedViewsBar on 10 pages.

### 4.3 Status colouring

- `lib/statusColor.ts:14` declared single source; imported by **2** files (`traces/volumeSeries.ts`, `anomalies/ProblemDetail.tsx`).
- ~329 ternary tone picks in 136 files + 326 inline `var(--err|--warn|--ok)` styles. Top: AdminClickhouse 34/39, adminstats/panels 10, Postgres/MySQL/Redis/Oracle panels 9/9/8/6, ArgoCDInstancesPanel 6, Traces 6, Trace 4.
- `sevClass` ×3: `lib/utils.ts:318`, `traces/TraceLogList.tsx:13`, `LogContextModal.tsx:264`.
- Duplicate badges: SeverityBadge (`settings/ChannelsTab.tsx:256`, `Alerts.tsx:858`, `anomalies/ProblemsSection.tsx:947`), SeverityPill (`Incidents.tsx:225`, `Incident.tsx:487`), StatusPill (`Incidents.tsx:221`, `Incident.tsx:483`), StatusBadge (`Inbox.tsx:1442`, `ai/ExchangesPanel.tsx:168`).
- Threshold→tone functions: `Clusters.tsx:60/:1342`, `clusters/thresholds.ts:7/14/25`, `AdminClickhouse.tsx:227/:414/:1886/:1905`, `Monitors.tsx:210`, `endpoints/opCallTreeCols.ts:49`, `databases/databaseProblems.ts:117`, `Services.tsx:1031/:1087`, `DependenciesTable.tsx:999`, `traces/shared.tsx:24`, `lib/health.ts:12`, `lib/httpMetrics.ts:339-351` (4), `settings/EntitiesTab.tsx:32`, `pod/PodContextTables.tsx:23`, `anomalies/statusTone.tsx:44`, `anomalies/EvaluatorStatus.tsx:23`, `ai/rcaQualityView.ts:42`, `cosreQualityView.ts:43`, `routingReportView.ts:52`, `aiBudgetView.ts:20/62`, `ai/toolSteps.ts:151`, `rcaVerdictView.ts:14`, `lib/rolloutRow.ts:44`, `lib/serviceGitOps.ts:15/24`, `dashboard/PanelRenderer.tsx:1165`, `TraceCompare.tsx:300`.

### 4.4 Row actions

- Kebab: `settings/ArgoCDInstancesPanel.tsx:157` (shared Popover :191), `trace/TracePodTable.tsx:324` (shared :567), `Dashboard.tsx:621` (hover, hand menu :628), `MetricPanel.tsx:209-236` (hover, hand), `CorePanel.tsx:1207` (hand :1209), `Users.tsx:325` (UserRowMenu Popover per row :500).
- Hover action groups (CSS): `.kv-actions` :2579/:5040, `.cm-msg-actions` :5572, `.tcf-btn` :6263, `.ps-attr-act` :6425, `.dash-panel-menu-btn` :2409; renderers `LogTable.tsx:449`, `SpanDetail.tsx:800`, `ai/ChatBubble.tsx:1186/1253`, `explore/QueryRow.tsx:178`, `viz/MetricQueryEditor.tsx:568`.
- `data-row-action` hand-written: `Incidents:159`, `Rollouts:280`, `Inbox:1221`, `Endpoints:916`, `Dashboards:259`, `explore/GroupTable:388`, `anomalies/streams:480`. `onContextMenu`: 0.

### 4.5 Density

- Global: `components/DensityToggle.tsx` → `data-density` on `<html>` (`Topbar.tsx:76` only); steps `lib/density.ts` comfortable·compact·dense; `--row-h` `globals.css:190/:3514`; `ROW_H=36` `ui/DataTable/rowHeight.ts:8`.
- Independent ladders: `AdminSql.tsx:516` ROW_HEIGHT 22; `TraceWaterfall.tsx:19` ROW_EST_PX 24 vs `.wf-row` min-height 22 (`globals.css:2020`; density 18/15); `FlameGraph.tsx:13` and `FlameDiff.tsx:21` ROW_H 18; `trace/TracePodSpanTimeline.tsx:21` LANE_H 14.
- Per-component `compact` props: Logs wrap (`Logs.tsx:1291`), TracePodPanel (:244), MetricPanel, ServiceRuntimeBadge, LazyMount.

## 5. Hand-rolled chart chrome around uPlot (rendering untouched — decisions 2/11)

Four uPlot wrappers, each with its own `innerHTML` tooltip; uPlot is imported directly in TimeChart, `viz/TimeSeriesPanel`, `lib/chart/engine.ts`, `service/charts/OverviewChart`. No zoom-reset button anywhere (dbl-click only).

| Wrapper | Tooltip | Legend | Menu / overlay | Other chrome |
|---|---|---|---|---|
| `chart/CorePanel.tsx` | `tt.innerHTML` :998, container :1344 | PanelLegend :1382 | ⋯ IconButton :1207 → hand `div role=menu` :1209; fullscreen fixed overlay :1180 | CSV `<a>` :1094 |
| `charts/TimeChart.tsx` | :394/:474 | StatsLegend :480 | — | dbl-click unpin :150 |
| `service/charts/OverviewChart.tsx` | :355/:453 | StatsLegend :463 | — | — |
| `viz/TimeSeriesPanel.tsx` | :723 | own legend table :1036 (LegendRow :165, 11px) | — | dbl-click :784, reset :883 |

Shared chrome: `chart/PanelLegend.tsx` (sort Buttons :48, collapse :55, `role=button` rows :76), `chart/StatsLegend.tsx` (inline table :96/:106, click/Ctrl-click toggle :123-128, IconButton per series :133, collapse :67), `LatencyHeatmap.tsx` legend :574/:586, `SparkReadout.tsx:77` `role=tooltip`, Sparkline SVG `<title>`, `CompareToggle.tsx:15` Button row.

| Chrome | shadcn primitive | Touches rendering? |
|---|---|---|
| Panel ⋯ menus (CorePanel :1209, MetricPanel :236, Dashboard :628) | `dropdown-menu` | no |
| CorePanel fullscreen overlay :1180 | `dialog` | no (same uPlot instance re-parented as today) |
| CompareToggle :15 | `toggle-group` | no |
| PanelLegend / StatsLegend / TimeSeriesPanel legend | `table` + `collapsible` + `toggle` | no (series visibility flags unchanged) |
| LogsHistogram chrome | `toggle-group` | no |
| innerHTML tooltips (4) | none — only token/class names move | no |
| Palette: `SERIES_PALETTES` `chartFmt.ts:159-163` (8 hex/theme; light==redhat), `seriesRoleColor` `lib/chart/seriesRole.ts:23-29` → `var(--err/--ok/--text3)` via `resolveVar` (`lib/chart/resolveVar.ts:10-14`), redraw on `useThemeTick`; heatmap own dark hex `heatmapRamp.ts:20` | tokens file is the source; palette reads it | no |

## 6. Density, tabular figures, per-row overlays

| Finding | Evidence |
|---|---|
| Base table 12px (`--fs-sm`, `globals.css:1475`), `tbody td` 9px 12px (:1613); rows 36 default / compact 24 (table 11px, :3514-3515) / dense 20 (table 10px, badge 8px, :3541-3557); `--hit-min` 24 / 24 / 20 | `globals.css` |
| Log cells mono 11.5px (`.logtbl-dense` :4046); waterfall rows 22/18/15; `.wf-bar-label` 10px :2282, `.wf-tick-label` 10 :2013, `.wf-toggle` 9 :2102, `.wf-sub` 9 :2201; `.badge` 11px :1932 | `globals.css` |
| Inline `fontSize`: **1,880 of 2,153 below 13px**. Top: AdminClickhouse 116, AnomalyTab 85, adminstats/panels 59, DevOpsTab 42, Trace 29, endpoints/detailSections 27, anomalies/streams 23, Clusters 22 | sweep pages §4 |
| Tabular figures: 35 `tabular-nums` rules in globals; `td.num/th.num` (:1834) via DataTable `numeric:true` (Clusters 23 cols, Traces 8, `trace/tracePodCols.ts` 4). **No marker**: `AdminSql.tsx` (2 grids), `settings/AiToolsetsPanel` (2), `trace/KioskSpanPanel`, `settings/ZoomChannelPicker`, TeamRoutingTab, OracleTab, LdapUserPicker, ExternalLinksTab, `anomalies/ExceptionPodsPanel`. Weak: SpanDetail (4 tables / 1 marker), LdapTab (3/1), RootCausePanel (4, no DataTable). 6 right-aligned `td` without `num` (TimeSeriesPanel ×4, AdminClickhouse, ExceptionSampleRow) | sweep pages §4 |
| Virtualised: TraceWaterfall (`useVirtualizer` :594), Traces (VirtualTable :1740); `content-visibility` only: LogTable (:479), explore/GroupTable (:386), TracePodTable (>100 rows) | sweep pages §4 |

Per-row overlays (a Radix root per row would multiply these):

| Site | What runs per row | Note |
|---|---|---|
| **`Inbox.tsx:1349`** `<ProblemVerdictActions row>` | `useProblemVerdicts` + `useSetProblemVerdict` per row; `verdictIndex(q.data?.verdicts)` rebuilt inside a per-row `useMemo` (`ProblemVerdictActions.tsx:55`, verified) + NoiseReasonButton (3 `useState` + Popover, :88/:119/:149) | measured perf finding → OQ i |
| `anomalies/streams.tsx` :167/:626, :542 | SnoozeButton, AIExplainButton | |
| `AnomalyWindowTable.tsx:83` | SnoozeButton | |
| `ai/ChatTraceList.tsx:36`, `ai/EvidenceChips.tsx:211` | RowExplainCell | CoSRE v2 replaces |
| `trace/TracePodTable.tsx:343`, :349 | Tooltip per CPU/mem cell (`TraceMetricCell.tsx:41/65`) + per row | shared-anchor pattern already at :567 |
| `Users.tsx:325/:500` | Popover per row (null when closed) | |
| `SpanDetail.tsx:798-804` | CopyButton + 2 Links per attribute row | |
| `StatsLegend.tsx:133` | IconButton per series | |
| `settings/ArgoCDInstancesPanel.tsx:191` | shared Popover anchor | the pattern to generalise |

Phase 1 rule for `coremetry/DataTable`: one hover-action anchor per table (shadcn `dropdown-menu` with a shared trigger), never a Radix root per row; numeric columns default to `tabular-nums` via the column spec.

## 7. Bundle

| Item | Today | Evidence |
|---|---|---|
| Chunk groups | vendor (react, prio 100), grafana 90, charts (uplot) 95, router, tanstack, otel, graph 80, catch-all vendor 10 | `vite.config.ts:64-83` |
| Lazy boundaries | 88 `React.lazy`: 53 in `App.tsx` (CoSRE :79, CoSREShared :81), 11 in `pages/System.tsx:18-28`, ~24 elsewhere (mostly CorePanel wrappers) | sweep stack §3 |
| Initial load | 36 JS preloaded ≈ **346.6 KB gz** (vendor 45.4, index 134.4, charts 22.3 eager, i18n 21.3, api 15.5, ui atoms 15.2, lucide+virtual-core 14.9, VirtualTable 12.3, tanstack 11.2, router 8.8). CSS 202.8 KB raw / 36.4 KB gz | sweep stack §3 |
| Main chunk | `index-*.js` 400.9 KB raw / 134.4 KB gz. **CoSRE chat is eager**: `AppShell.tsx:8` static `CopilotChat` import → ~192 KB of 398 KB raw (~48%; ~54% with topology dragged by `ai/ChatTopologyCard.tsx`). Ineffective lazy: `ai/EvidenceChips` lazy in MetricPanelCard but static in ChatBubble, LogSpikeCard, ServiceReportCard, anomalies/* | sweep stack §3 |
| Heavy lazy chunks | grafana 1,143 KB / 173.4 gz; **Settings 605 KB / 167.9 gz** (62 files, one chunk); Service 75.2 gz; AdminClickhouse 47.0 gz; Trace 41.1 gz; otel 38.1 gz | sweep stack §3 |
| CSS-in-JS removable | none (§1.1) | — |
| Added cost | **≈ 75-95 KB gz JS + 8-15 KB gz CSS** if the whole install set is adopted (published sizes, NOT measured; table below). Replaced atoms today live mostly in the ui chunk (15.2 gz) + index | sweep stack §3 |

Estimated additions (gz, published package sizes; measured in Phase 1 before any page migrates):

| Layer | Estimate | Notes |
|---|---|---|
| Tailwind v4 CSS, preflight off | ~3-6 KB for the first 13 components; ~6-12 KB full app | shrinks only as `globals.css` rules are deleted |
| `@theme` layer | ~1-2 KB | tokens file |
| clsx + cva + tailwind-merge | ~8-10 KB JS | shell |
| Radix shared core | ~10-13 KB JS | first Radix component pays it |
| Popper layer (floating-ui core/dom/react-dom + radix popper) | ~11-13 KB JS | new bytes; today's placement is `lib/tipPlacement` |
| tooltip / popover / dialog+sheet / tabs | +2-3 / +1.5-2 / +2-3 / ~2 KB | |
| dropdown-menu + context-menu | ~8-11 KB together | |
| select / scroll-area / cmdk | ~8-10 / ~4-5 / ~4-5 KB | select → OQ b |
| sonner | ~9-10 KB (own CSS) | OQ h |
| button, button-group, input, textarea, badge, skeleton, table | ~0 JS | button needs slot + cva |

Budget arithmetic (decision 10): +75-95 KB gz is not absorbed by deleting the 15.2 KB ui chunk. The target (< 347 KB gz initial) is reachable only if (a) the CoSRE lazy split (decision 9, in flight) removes the eager chat share from `index`, (b) Radix/cmdk/sonner chunks load with the first page that needs them rather than in the shell, and (c) each migrated page deletes its `globals.css` rules in the same release. Phase 2 releases report the measured initial-load delta in `migration-log.md`; a release that moves the number above 347 does not ship until it is split.

## 8. Gates, ratchets and collisions

| Gate | Today | Effect on shadcn | Phase 1 change (decision 8) |
|---|---|---|---|
| `ui/no-raw-button` (inline rule `eslint.config.js:30-67`, applied :94-101 to `src/**/*.tsx`, ignores `src/components/ui/**` + tests, error; `eslint-suppressions.json` = `{}`) | line exemption needs a reason (`eslintDisableReason.test.ts`) | `components/shadcn/button.tsx` is flagged | add `src/components/shadcn/**` + `src/components/ai-elements/**` to `ignores` |
| `buttonUnityRatchet.test.ts` | MAX_RAW_BUTTONS 0 (:34), MAX_ROLE_BUTTON 0, MAX_REASONED_EXEMPTIONS 9, MAX_SEGMENTED_FILES 0, MAX_ICONBUTTON_TITLE 7 (:42); excludes only `components/ui/` (:52) | counts shadcn/ai-elements buttons | exclude the two dirs; ceilings unchanged |
| `tableUnityRatchet.test.ts` | PRIMITIVE = `components/ui/DataTable/` (:29); rawTable 52, tdStyle 60, …; several at 0 | `components/shadcn/table.tsx` and `coremetry/DataTable` count toward rawTable | PRIMITIVE becomes a list (`ui/DataTable/`, `coremetry/DataTable/`, `shadcn/`) |
| New ratchet (decision 8) | — | — | `uiImportRatchet.test.ts`: number of non-test files importing from `components/ui` (today 209 Button / 126 DataTable / … per §2; baseline frozen at the Phase 1 commit) — may only go down |
| Contract + gate tests | 20 `*.contract.test.tsx` + 8 gates in `components/ui`; 93 test files reference the `components/ui` path | pin class names / DOM of atoms that will be deleted | OQ e |
| `corePanelContracts.test.ts:787` | `@grafana/ui` only in CorePanel | unaffected | none |
| `depPins.test.ts:32-34` | React 18.x | blocks React 19 | Phase 0.5 edits the test (decision 6) |
| `build:ds` | packages `components/ui` | `components/ui` frozen then deleted | OQ f |
| Global element CSS | `button {}` `globals.css:634`, `input, select {}` :624, `textarea {}` :632 | restyle every shadcn component with preflight off | OQ c |
| Esc layer | `lib/escLayer.ts` LIFO stack (`pushEscLayer` :31, `useEscLayer` :59); single document listener in `keyboard.ts` | Radix `DismissableLayer` has its own Esc handling | OQ d |
| macOS case | APFS `core.ignorecase=true`; `components/shadcn`, `components/coremetry`, `components/ai-elements` all absent; no `components.json`; `Skeleton.tsx` collides only if the shadcn target were `components/` | no collision for the three planned dirs | shadcn target = `components/shadcn` (never `components/ui`) |

## 9. Phases

### 9.0 Phase 0.5 — React 19 (separate release, decision 6)

Scope: `react`, `react-dom`, `@types/react` (+ `@types/react-dom`), `depPins.test.ts:32-34`. Known pin reason: `@grafana/ui` 13.1.x peer ceiling (test header). If 13.1.2 refuses React 19 the agent resolves that first (nothing else changes). Why before Phase 1: shadcn sources are React-19 style (no `forwardRef`); on React 18 every generated component would need a forwardRef port that is thrown away later. **Result: pending** (being executed now by a separate agent; recorded here when it lands).

### 9.1 Release discipline (decision 1)

Every migration release = one logical unit, own `v0.10.X`, standard gates (`tsc`, `eslint`, `vitest`, `go test`, `make audit`) + the Phase 1 additions (axe, `/design`). Each release appends to `docs/frontend/migration-log.md`: `| vX | page/scope | what changed | atoms deleted | ui-import ratchet before→after | initial-load gz before→after |`. Screenshots optional; Playwright manual and only when the look changes markedly.

### 9.2 Phase 1 — foundation (no page migrates; shell unchanged)

| # | Item | Detail |
|---|---|---|
| 1 | Tailwind v4 | preflight **off**; dark variant bound to `[data-theme="dark"]` (selector strategy — no `.dark` class); utilities layered **after** `globals.css` so migrated pages win; `@theme` reads the tokens file |
| 2 | Tokens file | one `src/styles/tokens.css`: shadcn semantic vars per theme (`:root`=dark, `[data-theme="light"]`, `[data-theme="redhat"]`) mapped from the existing palette — draft: `--background←--bg0`, `--card`/`--popover←--bg1`, `--foreground←--text`, `--muted←--bg2`, `--muted-foreground←--text2`, `--primary←--accent`, `--primary-foreground←--on-accent`, `--secondary←--bg2`, `--destructive←--err`, `--border←--border`, `--input←--border-control`, `--ring←--focus`, `--radius←--radius-md` (redhat keeps its 2/3/4/8 scale). Status (`--ok/--warn/--err/--info/--fatal` + `-bg`/`-solid`) exported as `--status-*` and consumed by `SERIES_PALETTES`/`seriesRoleColor` so uPlot reads the same values (rendering untouched). `lib/theme.ts` becomes the single reader/writer (decision 7); `chartFmt.ts:193` and `dataFrame.ts:50` import it |
| 3 | Coremetry preset | `components.json` (style, base colour, `@/components/shadcn` alias, lucide) + a `cva` preset that adds our sizes (`xs`) and tones (`accent`, `ghost-danger`, status tones) once, so generated files are not hand-edited |
| 4 | Density context | `DensityProvider` reads `data-density` (comfortable·compact·dense, `lib/density.ts`) and maps to shadcn `size` variants + `--row-h`; `[data-density]` CSS rules stay for unmigrated pages. Value naming → OQ a |
| 5 | Directories | `components/shadcn/` (generated, exempt), `components/ai-elements/` (generated, exempt, CoSRE v2), `components/coremetry/` (composites with **frozen APIs**: `DataTable`, `TimeRangePicker`, `FilterBar`, `StatusBadge`, `CommandPalette`); `components/ui/` **frozen** (no new atom, no new importer) |
| 6 | Install set | union of the §2 equivalents: button, button-group, badge, input, textarea, label/field, kbd, skeleton, table, card, tooltip, popover, dropdown-menu, context-menu, dialog, alert-dialog, sheet, select, tabs, toggle, toggle-group, collapsible, scroll-area, command, sonner (toast → OQ h) |
| 7 | Gates | ESLint ignores + ratchet exclusions (§8); `uiImportRatchet` with frozen baseline; `tableUnityRatchet` PRIMITIVE list |
| 8 | `/design` route | exists dev-only (`App.tsx:243`, v0.10.919) → extended with every installed shadcn component + every `coremetry/*` composite, rendered under 3 themes × 3 densities |
| 9 | vitest + axe | `axe-core` (none installed today) + a vitest harness that runs axe over each `/design` section; becomes a release gate |
| 10 | Icons | lucide only (already direct); `components/icons.tsx`, `RuntimeIcons.tsx`, `TelescopeIcon.tsx`, unicode glyphs retire page by page in Phase 2 |

Exit criteria: `/design` green under axe for all 9 theme×density cells; initial load unchanged (±1 KB gz — Radix/cmdk/sonner must not enter the shell chunk); `uiImportRatchet` baseline committed; `migration-log.md` created with the Phase 1 row.

### 9.3 CoSRE v2 (separate track, decision 3)

CoSRE Phase 1 (Go `uistream` adapter, `v0.10.1589-1591`) continues now. CoSRE v2 frontend (`docs/cosre/chat-ui-audit.md` §6 F1-F9) starts only after shadcn Phase 1 merges, on `components/shadcn` + `components/ai-elements`; its F1 "missing atoms go into `ui/`" row is void (`ui/` frozen). The 37 `components/ai/*` files are **not** migrated in Phase 2; they are replaced by v2 and deleted in CoSRE P2.

### 9.4 Phase 2 — per-page migration (order)

| Slice | Pages | Why here | Composites exercised | Deletes |
|---|---|---|---|---|
| 1 | **Settings / Remote Cluster** (`settings/ClustersTab.tsx` + `SpanClusterValuesPanel.tsx` first, then the 62 settings files tab by tab) | lowest risk: forms + static tables, no chart, no virtualisation, no URL-range coupling; 320 raw inputs / 38 selects give the Field→shadcn form path its volume; Settings chunk (167.9 gz) is the natural place to prove per-tab splitting | Field/Select/Textarea, Button, Badge, Dialog (ChannelModal :164, LdapUserPicker :121, ZoomChannelPicker :147), StatusBadge | local Field copy `settings/shared.tsx:16`; `.sys-subnav` → shadcn nav |
| 2 | **Log search** (15 files) | first operator-critical page with a DataTable-class list (`LogTable`, content-visibility), FilterBar (`LogsFilterRow` ld-pill + LogPillEditor + KqlSearchInput), Popover menus (5), LogContextModal; histogram chrome only | FilterBar, DataTable (non-virtual), TimeRangePicker (brush `onRangeSelect`), dropdown-menu | `ld-pill`, level chips `Logs.tsx:1053` |
| 3 | **Trace view** (19) + Traces list (3) | highest density + virtualised waterfall + per-row Tooltip/Popover; benefits from slices 1-2 having settled Tooltip/Popover/Drawer; **not first** (operator) | Sheet (TracePeekDrawer), Tooltip (6 + TraceMetricCell per cell), DataTable virtual (Traces :1740), FilterBar (fq-chip, fb-chip, ColumnFilter) | hand menus `Trace.tsx:1594`, share dialog :1258, breadcrumb :610 |
| 4 | **Metrics explorer** (`/explore` 16 + `/metrics` 4) | `MetricQueryEditor` listbox :1183 + chip editor :327/:341 and `TimeSeriesPanel` legend :1036 are the last bespoke filter/legend pair; `explore/QueryPanel.tsx` touches `@grafana/ui` so it waits until the chart-chrome mapping (§5) is proven on slices 2-3 | command (listbox), toggle-group, legend table | `mqe-chip`, `LegendRow` |
| 5… | remaining pages grouped by composite: Inbox/Problems/Anomalies (StatusBadge + per-row overlays, OQ i), Services/Service/Endpoints, Clusters/K8s, Dashboards (PanelEditor :105 dialog, VariablesBar), Admin* (AdminClickhouse 273 inline styles), Topology/ServiceMap, Alerts/SLOs/Incidents | by shared composite, largest `uiImportRatchet` drop first | — | per page |
| last | shell: Sidebar (+redhat rail remap `globals.css:391-417`), Topbar, CommandPalette (847 lines → `coremetry/CommandPalette` on `command`), TimeRangePicker global, PageShell/PageControls | touching the shell last keeps every earlier slice's gate meaningful | CommandPalette, TimeRangePicker | `components/ui` deleted when `uiImportRatchet` = 0 |

Per-slice rules: one page group per release; the release deletes the page's `globals.css` rules and local duplicates; `migration-log.md` row with ratchet + bundle delta; `/design` + axe green; the page's existing contract tests updated in the same release (OQ e).

### 9.5 In-flight independent fixes (not part of a phase; separate releases, in progress)

| Fix | Site | Status |
|---|---|---|
| Grafana bridge redhat→dark (decision 7) | `lib/chart/dataFrame.ts:50-53` `!== 'light'` → `=== 'dark'` (or `currentTheme()`); redhat verified light (`globals.css:319`, `theme.ts:21`) | in progress, separate release |
| CoSRE lazy chunk (decision 9) | `AppShell.tsx:8` static `CopilotChat` → `React.lazy` + route split; FAB stays eager | in progress, separate release |

### 9.6 Final cleanup

`components/ui` removed (`uiImportRatchet` 0 → test deleted); `build:ds` retargeted or removed (OQ f); `buttonUnityRatchet`/`tableUnityRatchet` collapse to "no raw element outside `components/shadcn`"; `globals.css` reduced to tokens + the chart/waterfall/flame rendering CSS; `docs/DECISIONS.md` entry; CLAUDE.md "Frontend UI conventions" rewritten (atoms → shadcn/coremetry); `.claude/skills/frontend-design-system` + `frontend-conventions` updated.

## 10. Open questions for the operator — **DECIDED 2026-10-29, see §11** (recommended default in italics)

1. **(a) Density values.** Brief says `compact|comfortable`; the system is 3 steps `comfortable·compact·dense` (`lib/density.ts:10`, attribute values `compact|dense` on `<html>`, comfortable = base) with a global `DensityToggle`. *Keep the 3 existing values and the attribute; the density context maps them to shadcn sizes; no rename.*
2. **(b) SelectField is a native `<select>`** (31 sites + 167 raw `<select>`). Radix Select changes keyboard/type-ahead/mobile behaviour and option rendering. *Native `<select>` styled by shadcn `native-select` for data forms; Radix Select only where options carry icons/descriptions (ask-before-behaviour-change rule).*
3. **(c) Global element rules** `button {}` (`globals.css:634`), `input, select {}` (:624), `textarea {}` (:632) will restyle shadcn components with preflight off. *Phase 1 scopes them to `:where(:not([data-slot]))` (shadcn marks every element with `data-slot`) rather than deleting them, so unmigrated pages keep their look; delete in final cleanup.*
4. **(d) Esc layer.** `lib/escLayer.ts` LIFO stack vs Radix `DismissableLayer`. Mixed stacks reproduce the two bugs the stack was built for (header comment :4-10). *Radix components register in the stack via `onEscapeKeyDown(e => e.preventDefault())` + `useEscLayer` in the `coremetry/*` wrappers; raw Radix dialogs/popovers outside wrappers are banned by lint.*
5. **(e) Contract/gate tests** (20 contract + 8 gates in `components/ui`, 93 files referencing the path) pin class names/DOM. *Per slice: tests of deleted atoms are deleted with the atom; behaviour tests (focus return, Esc order, confirm promise) are re-pointed at the `coremetry/*` wrapper, not kept on the old DOM.*
6. **(f) `build:ds`** library build packages `components/ui` for design-sync. *Retarget the entry to `components/coremetry/index.ts` + `components/shadcn` in final cleanup; until then it keeps building the frozen `ui/`.*
7. **(g) `coremetry/DataTable` on TanStack Table** (new dependency) vs today's `useDataTable` (URL sort with sig guard, column model, range selection + BulkBar, server sort, row-as-link, persisted widths). *Parity scope = the full `useDataTable` option list (§2 row); TanStack supplies sort/resize/visibility state only; URL/persistence/selection adapters are ported, not rewritten. If parity cannot be shown on Traces (virtual) and Logs in slices 2-3, keep `useDataTable` under `coremetry/` without TanStack.*
8. **(h) Toast:** sonner vs `lib/toast.ts` (21 importing files, 50 `toast(` sites, one `Toaster`). *Keep the `lib/toast.ts` API; swap the renderer to sonner behind it in Phase 1; no call-site change.*
9. **(i) Inbox per-row `verdictIndex` rebuild** (`Inbox.tsx:1349`, `ProblemVerdictActions.tsx:55`). *Fix now as a separate bug release (lift `verdictIndex` to the table, pass the row's verdict down); it is a measured perf finding, not a migration item.*
10. **(j) Settings chunk** 605 KB raw / 167.9 gz as one lazy chunk. *Split per tab inside slice 1 (it is the first page anyway) and record the delta.*
11. **(k) PageShell / PageControls** have no shadcn equivalent and publish `--controls-h`. *Move both to `components/coremetry` unchanged in Phase 1 (they are layout, not atoms); `FilterBar` composes `PageControls`.*
12. **(l) `title=` native tooltips** (~2,134) vs 13 `<Tooltip>`. *Out of scope: only glyph-only controls (ratchet `MAX_ICONBUTTON_TITLE 7`) and existing `<Tooltip>` sites move to Radix tooltip; data-cell `title=` stays.*
13. **(m) Raw `.badge .b-*` classes** (~523 sites, 139 files) vs 137 `<Badge>`. *Convert per page in the page's slice; the CSS classes stay until the last page, then are deleted (no alias layer).*
14. **(n) Inline `style={{`** (5,309 sites). *Only inside migrated pages, and only where a token exists; no global sweep release.*
15. **(o) Phase 0.5 fallback.** If `@grafana/ui` 13.1.2 refuses React 19 and 13.2.x changes chart behaviour. *Wait for the Phase 0.5 result; fallback is forwardRef ports on React 18 (throw-away work, so only if the bump is blocked for more than one release).*
16. **(p) Shell `Sidebar` + redhat dark-rail remap** (`globals.css:391-417`). *Last slice; keep the scoped remap as a token override under `[data-theme="redhat"] [data-slot=sidebar]`.*

## 11. Operator rulings on §10 (2026-10-29) — binding, override §9/§10 where they differ

§10 kararları: a, b, c, d, e, f, h, i, j, k, l, m, p önerildiği gibi.
Değişenler:

- **g — Tek tablo yığını.** Karar Traces diliminde: TanStack `useDataTable`'ın tüm
  özelliklerini + virtualization'ı karşılarsa her yer (Traces/Logs dahil) TanStack;
  karşılamazsa TanStack hiç girmez, `coremetry/DataTable` bileşiği mevcut
  `useDataTable`'ı sarar. İki yığın paralel yaşamaz.
- **o — `@grafana/ui` React 19'u engellerse paket kaldırılır, beklenmez.** Bugün ne
  sağladığı (2026-10-29, `grep -rnE "from ['\"]@grafana/(ui|data)" frontend/src`, testler hariç):
  - `@grafana/ui` → yalnız `components/chart/CorePanel.tsx:33-37`: `UPlotChart` (uPlot
    örneğinin React yaşam döngüsü: config kimliği değişince destroy/recreate, :1081,
    :1367), `UPlotConfigBuilder` (eksen/ölçek/seri config üreticisi; :124'te
    `MeasuredAxisConfigBuilder` ondan türer) ve enum'lar (`AxisPlacement`,
    `ScaleOrientation`, `ScaleDirection`, `ScaleDistribution`, `GraphGradientMode`,
    `DrawStyle`, `PointVisibility`).
  - `@grafana/data` → `CorePanel.tsx:38` (`DataFrame`, `DecimalCount` tipleri) +
    `lib/chart/dataFrame.ts:30-39` (`FieldType`, `ThresholdsMode`,
    `getDisplayProcessor`, `createTheme`; `framesToAligned`, `chartTheme`).
  - `CorePanel`'i kullanan dosya: 64. Diğer uPlot sarmalayıcıları (`TimeChart`,
    `viz/TimeSeriesPanel`, `lib/chart/engine.ts`, `service/charts/OverviewChart`)
    uPlot'u DOĞRUDAN kullanır, Grafana'ya bağlı değil.
  - **Sonuç: yalnız tema köprüsü DEĞİL.** Kaldırmak = `CorePanel`'in config üreticisi +
    yaşam döngüsü + değer biçimleyicisini (display processor: birim/ondalık/eşik rengi)
    elle yazmak. Bu, "uPlot only, rendering untouched" kuralına dokunur → kaldırma
    gerekirse ayrı sürüm(ler), çıktı eşitliği CorePanel sözleşme testleriyle
    (`corePanelContracts.test.ts`) pinlenir; çizim motoru uPlot kalır. React 19 bu paket
    yüzünden gecikmez.
  - **Karar merdiveni (operatör, 2026-10-29 — `o` bunu uygular):**
    1. `@grafana/ui` peer aralığı React 19'u kapsıyor **veya** `CorePanel` testleri React
       19'da yeşil → paket kalır; kaldırma chart katmanı workstream'ine ertelenir.
    2. Değilse → React 19 destekli `@grafana/ui` **minor** yükseltmesi.
    3. O da yoksa → `CorePanel` uPlot katmanı `corePanelContracts.test.ts` arkasında elle
       yazılır, ayrı sürüm.
  - `@grafana/data` `@grafana/ui`'den **bağımsız** değerlendirilir; `@grafana/ui` giderse
    de kalabilir (display processor, `DataFrame`).
  - **Chart-layer audit input — hedef tek `TimeSeriesPanel`** (bu geçişte DOKUNULMAZ):
    beş uPlot sarmalayıcısı — `components/chart/CorePanel.tsx`,
    `components/charts/TimeChart.tsx`, `components/viz/TimeSeriesPanel.tsx`,
    `lib/chart/engine.ts`, `pages/service/charts/OverviewChart.tsx`.
- **n — Geçirilen sayfada inline style sıfır.** Ratchet'e sayfa bazlı inline-style
  sayacı eklenir; sayfa ancak sayaç sıfırlanınca "geçti" sayılır.
- **Bütçe — görevler, koşul değil.**
  - CoSRE lazy: ayrı sürüm (yürüyor).
  - Radix'in sayfa bazlı chunk'a girmesi (giriş paketinde Radix yok) **Faz 1 kabul kriteri**.
  - `globals.css` kalıntı silimi **her sayfa diliminin kabul kriteri**.
  - CI gerçek giriş sayfası ilk yüklemesini (preload edilen JS gz toplamı) ölçer, 347 KB
    gz ile karşılaştırır; §7'deki hesap her sürümde ölçülen sayıyla yan yana
    `migration-log.md`'ye yazılır.
- **b notu:** form başına tek select tipi; aynı formda native + Radix karışmaz.

**Sıra:** Faz 1'e onay. Faz 0.5 (React 19) ve CoSRE lazy merge olmadan Faz 1 başlamaz.


## 13. Design permission on migrated pages (operator, 2026-10-30) — binding for Phase 2

- **Free on a migrated page:** visual hierarchy, spacing/density, alignment, icons, empty/loading/error states, typographic consistency.
- **Reference language:** two screens the operator places under `docs/frontend/mockups/` (CoSRE chat, service overview); shadcn zinc dark, Inter + JetBrains Mono, dense tables, the status palette. Mockups stay in `docs/` — never copied into `src/`.
- **Stop and ask (information architecture):** page layout, navigation, which data lives where, column set, default filters.
- **Logging:** every visual change gets a `design:` line in that release's `docs/frontend/migration-log.md` entry.
- **Fonts (decided 2026-10-30):** `@fontsource-variable/inter` + `@fontsource-variable/jetbrains-mono`, subsets latin + latin-ext, `font-display: swap`, no preload. Bundled woff2 (air-gapped safe). Font files do NOT count toward the JS budget; `bundle-budget.mjs` reports them on a separate line with a **font budget of 120 KB woff2**. Redhat keeps its local-only font preference (`globals.css:316`).
- **Zinc dark (decided 2026-10-30):** re-values the **global dark theme tokens** only; light and redhat untouched. Ships as its own small release **before the Settings slice**, with a `design:` line in migration-log. The contrast pin test runs for dark too; a secondary text tone that fails 4.5:1 is not used. Operator reviews three pages on prep before approving the slice.
- **Order:** after Phase 1 R10 → font release → zinc-dark release → (operator prep review) → Settings slice.
