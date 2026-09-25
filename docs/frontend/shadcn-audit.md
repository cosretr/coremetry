# shadcn/ui adoption & Button consolidation — Phase 0 audit

Status: **decided — Option B approved 2026-09-25; Phase 1 shipped in v0.10.919** (see `docs/DECISIONS.md`).

> **Correction (v0.10.919):** §2.3's heuristic flagged 7 comment matches; a comment-aware
> count (`stripTsComments`, cross-checked with a TypeScript AST count and the new ESLint rule)
> finds **12**, so the real raw `<button>` total is **85**, not 90. `role="button"`: **17** JSX
> sites (the 23 in §2.4 included selector strings and test files). The row table below is kept
> as the original snapshot; the live ceilings are in `frontend/src/styles/buttonUnityRatchet.test.ts`
> and `frontend/eslint-suppressions.json`.

Snapshot: 2026-09-25, `main` @ v0.10.916.

## TL;DR

- Stack is Vite 8 + React 18.3 + TS 5.9, **plain global CSS** (`src/styles/globals.css`, 4.3k lines, 77 `:root` tokens), no Tailwind, no Radix.
- A Button system **already exists** and is widely used: `Button` (584 call sites, `variant` required at type level, `loading` built in), `IconButton` (43, `aria-label` required at type level), `Chip`, `LinkButton`, `SegmentedControl`, `DisclosureButton`.
- What is left is **97 raw `<button>`** in 51 files (7 are matches inside comments → **90 real**) and **23 `role="button"`** on `span`/`div`/`tr`. No `<a role="button">` found.
- Adding Tailwind + shadcn creates a **second styling system** next to 4.3k lines of global CSS, with a real preflight/specificity collision risk, and re-implements features the current atoms already enforce. Estimated cost: **~20–35 KB gz** JS+CSS plus a migration of ~700 call sites to delete the legacy Button.
- Recommendation (see §6): **Option B** — hit every goal of Phase 1/2 (ESLint ban, `/design` route, ButtonGroup, Tooltip, lucide rule, action-order rules) on the existing atoms, no Tailwind. If shadcn is still wanted, base it on **Radix** (§4) and disable preflight.

## 1. Stack

| Item | Finding |
|---|---|
| Build | Vite `^8.1.4`, `@vitejs/plugin-react ^6`, `rollup-plugin-visualizer` present |
| React / TS | react 18.3.1, @types/react 18.3.5, typescript ~5.9 |
| Tests / lint | vitest ^4.1, jsdom; ESLint 9 flat config (`eslint.config.js`), typescript-eslint 8; **no `no-restricted-syntax` rules yet** |
| CSS approach | Plain global CSS, token-level CSS vars in `globals.css`; per-feature CSS files; **no** CSS modules, styled-components or Tailwind. Some inline `style={{…}}`. `@grafana/ui` 13 (charts) pulls in `@emotion/*` + `@floating-ui/*` at runtime |
| Path alias | `@/*` → `src/*` (vite + tsconfig) |
| Dark mode | `data-theme` attribute on `<html>`: default (dark) on `:root`, `[data-theme="light"]`, `[data-theme="redhat"]`. **Three** themes, not two — any shadcn mapping must cover all three |
| Icons | `lucide-react ^1.17` already a dependency (19 import sites) alongside unicode glyphs (✕ ▶ ⋮ …) |

## 2. Button inventory

### 2.1 Existing atoms (`src/components/ui/`)

| Atom | Call sites | Contract already enforced |
|---|---|---|
| `Button` | 584 | `variant` **required** (type-level, v0.9.1005); `size` xs/sm/md/lg; `loading`; `leftIcon`/`rightIcon`; contract test |
| `IconButton` | 43 | `aria-label: string` **required at type level**; contract test |
| `Chip` | 37 | `active` → `aria-pressed`; focus ring kept |
| `LinkButton` | 13 | `<a>` twin of Button variants (`anchorVariantGate` test) |
| `SegmentedControl` | 20 | radiogroup, roving tabindex, arrow keys (v0.10.914–915) |
| `DisclosureButton` | 16 | `aria-expanded` |

`Button` variant usage: secondary 291 · primary 144 · ghost 65 · ghost-danger 34 · accent 23 · danger 20.
`Button` size usage: sm 361 · xs 26 · md 2 · lg 1 (rest default). 14 call sites add inline `style={{…}}`.

Existing guard tests: `buttonUnityRatchet` (raw `<button>` ≤ 97, hand-built `.segmented` = 0), `primitiveClasses`, `anchorVariantGate`, `actionOrder`, `destructiveConfirm`, `noNativeConfirm`.

### 2.2 Variant mapping (current → shadcn)

| Current | shadcn | Note |
|---|---|---|
| `primary` | `default` | 1:1 |
| `secondary` | `secondary` or `outline` | current look = bordered neutral → closer to `outline` |
| `ghost` | `ghost` | 1:1 |
| `danger` | `destructive` | 1:1 |
| `ghost-danger` (34) | — | no shadcn equivalent → custom variant needed |
| `accent` (23) | — | no equivalent (tinted accent) → custom variant or fold into `default` (visual change) |
| size `xs` (26) | — | shadcn has sm/default/lg/icon → custom `xs` or visual change |

### 2.3 Raw `<button>` table (90 real + 7 comment matches)

"Proposed" column is a heuristic first pass (class name + label). Rows marked *verify manually* need a human look. Label = how the button is named: `aria-label`, `title`, visible text, or **none** (none found).

| # | Dosya:satır | Mevcut görünüm (className / stil) | Etiket | Önerilen shadcn variant · size |
|---|---|---|---|---|
| 1 | `features/anomalies/streams.tsx:255` | `"btn-chip-x"` | text | ghost · icon (aria-label required) |
| 2 | `features/anomalies/streams.tsx:360` | `no class + inline style` | text | ghost · sm — verify manually |
| 3 | `features/dependencies/panels/shared.tsx:93` | `no class + inline style` | title | ghost · sm — verify manually |
| 4 | `features/dependencies/panels/shared.tsx:178` | `no class + inline style` | title | ghost · sm — verify manually |
| 5 | `features/dependencies/panels/shared.tsx:612` | `no class + inline style` | title | ghost · sm — verify manually |
| 6 | `features/dependencies/panels/OraclePanel.tsx:212` | `no class + inline style` | title | ghost · sm — verify manually |
| 7 | `components/MetricPanel.tsx:267` | `"metric-panel-title" + inline style` | title | ghost · sm — verify manually |
| 8 | `components/FlameGraph.tsx:38` | `no class + inline style` | **none** | ghost · icon (aria-label required) — verify manually |
| 9 | `components/FilterBuilder.tsx:104` | `"fb-chip-x"` | aria-label | ghost · icon (aria-label required) |
| 10 | `components/FilterBuilder.tsx:110` | `"fb-add"` | text | ghost · sm (left icon Plus) |
| 11 | `components/BubbleUpPanel.tsx:167` | `no class + inline style` | title | ghost · sm — verify manually |
| 12 | `components/TopbarSearch.tsx:13` | `no class` | — | — comment line (count false positive) |
| 13 | `components/TopbarSearch.tsx:20` | `no class` | — | — comment line (count false positive) |
| 14 | `components/TopbarSearch.tsx:20` | `no class` | — | — comment line (count false positive) |
| 15 | `components/TopbarSearch.tsx:28` | `"tb-search"` | aria-label | ghost · sm — verify manually |
| 16 | `components/ProblemRunbookPanel.tsx:158` | `no class + inline style` | text | ghost · sm — verify manually |
| 17 | `components/AggregateFlame.tsx:112` | `no class + inline style` | **none** | ghost · icon (aria-label required) — verify manually |
| 18 | `components/LogPillEditor.tsx:116` | `"sec" + inline style` | text | secondary · sm |
| 19 | `components/CorrelationContextDrawer.tsx:112` | `no class + inline style` | title | ghost · sm — verify manually |
| 20 | `components/CorrelationContextDrawer.tsx:362` | `"mono" + inline style` | title | ghost · sm — verify manually |
| 21 | `components/CorrelationContextDrawer.tsx:505` | `no class + inline style` | text | ghost · sm — verify manually |
| 22 | `components/Combobox.tsx:234` | `"cb-clear"` | aria-label | ghost · icon (aria-label required) |
| 23 | `components/Combobox.tsx:248` | `"cb-caret"` | aria-label | ghost · icon |
| 24 | `components/LogTable.tsx:302` | `"th-remove"` | title | ghost · icon (aria-label required) |
| 25 | `components/FilterQueryBox.tsx:269` | `"fq-x"` | aria-label | ghost · icon (aria-label required) |
| 26 | `components/FilterQueryBox.tsx:308` | `{i === hi ? 'sel mono' : 'mono'}` | title | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 27 | `components/TimeRangePicker.tsx:271` | `"trp-cal-nav"` | aria-label | ghost · icon |
| 28 | `components/TimeRangePicker.tsx:274` | `"trp-cal-nav"` | aria-label | ghost · icon |
| 29 | `components/TimeRangePicker.tsx:282` | `{dayCellClass(c)}` | **none** | ghost · icon (aria-label required) — verify manually |
| 30 | `components/TimeRangePicker.tsx:300` | `"trp-preset"` | text | ghost · sm — verify manually |
| 31 | `components/TimeRangePicker.tsx:312` | `{'trp-preset' + (value.preset === p ? ' active' : '')}` | text | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 32 | `components/CopilotChat.tsx:384` | `{criticalOpen > 0 ? 'cm-ai-fab is-alert' : 'cm-ai-fab'} + inline style` | aria-label | ghost · sm — verify manually |
| 33 | `components/ThemeToggle.tsx:41` | `"theme-toggle"` | aria-label | ghost · icon |
| 34 | `components/CopyButton.tsx:27` | `{'copy-btn' + (copied ? ' copied' : '')}` | aria-label | ghost · sm (left icon Plus) |
| 35 | `components/ShareButton.tsx:16` | `no class` | — | — comment line (count false positive) |
| 36 | `components/ClusterModeToggle.tsx:4` | `no class` | — | — comment line (count false positive) |
| 37 | `components/ClusterModeToggle.tsx:11` | `{value ? '' : 'on'}` | text | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 38 | `components/ClusterModeToggle.tsx:12` | `{value ? 'on' : ''}` | title | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 39 | `components/Sidebar.tsx:423` | `no class` | — | — comment line (count false positive) |
| 40 | `components/Sidebar.tsx:437` | `"sb-user-trigger" + inline style` | aria-label | ghost · icon |
| 41 | `components/ColumnManager.tsx:88` | `no class + inline style` | title | ghost · sm (left icon Plus) |
| 42 | `components/ColumnManager.tsx:160` | `no class + inline style` | text | ghost · sm (left icon Plus) |
| 43 | `components/RootCauseRibbon.tsx:108` | `no class + inline style` | title | ghost · sm — verify manually |
| 44 | `components/DensityToggle.tsx:65` | `"theme-toggle" + inline style` | aria-label | ghost · icon |
| 45 | `components/SpanDetail.tsx:277` | `"ps-close"` | text | ghost · icon (aria-label required) |
| 46 | `components/SpanDetail.tsx:620` | `"ex-toggle"` | title | ghost · icon |
| 47 | `components/CopilotExplain.tsx:358` | `no class` | text | ghost · sm — verify manually |
| 48 | `components/TraceWaterfall.tsx:617` | `no class` | text | ghost · sm — verify manually |
| 49 | `components/TraceWaterfall.tsx:826` | `"wf-toggle"` | aria-label | ghost · icon |
| 50 | `components/FlameDiff.tsx:48` | `no class + inline style` | **none** | ghost · icon (aria-label required) — verify manually |
| 51 | `components/SavedViewsBar.tsx:197` | `no class + inline style` | title | ghost · sm — verify manually |
| 52 | `components/SavedViewsBar.tsx:234` | `no class + inline style` | **none** | ghost · icon (aria-label required) — verify manually |
| 53 | `components/LangToggle.tsx:20` | `"theme-toggle tt-text"` | aria-label | ghost · icon |
| 54 | `components/LogFieldsPanel.tsx:261` | `no class + inline style` | title | ghost · sm — verify manually |
| 55 | `components/traces/shared.tsx:83` | `no class` | — | — comment line (count false positive) |
| 56 | `components/traces/shared.tsx:100` | `no class + inline style` | title | ghost · sm — verify manually |
| 57 | `components/dashboard/PanelEditor.tsx:232` | `no class` | text | ghost · sm — verify manually |
| 58 | `components/viz/MetricQueryEditor.tsx:312` | `"mqe-chip-x"` | aria-label | ghost · icon (aria-label required) |
| 59 | `components/viz/MetricQueryEditor.tsx:330` | `"mqe-chip-add"` | text | ghost · sm (left icon Plus) |
| 60 | `components/viz/MetricQueryEditor.tsx:333` | `"mqe-addfilter"` | title | ghost · sm (left icon Plus) |
| 61 | `components/viz/MetricQueryEditor.tsx:347` | `{'mqe-gchip' + (value.includes(key) ? ' on' : '')}` | **none** | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 62 | `components/viz/MetricQueryEditor.tsx:420` | `"mqe-lb-toggle"` | text | ghost · icon |
| 63 | `components/viz/MetricQueryEditor.tsx:454` | `{'mqe-gchip' + (k === key ? ' on' : '')}` | title | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 64 | `components/viz/MetricQueryEditor.tsx:475` | `"mqe-gchip"` | title | ghost · sm — verify manually |
| 65 | `components/viz/MetricQueryEditor.tsx:499` | `"mqe-id"` | title | ghost · sm — verify manually |
| 66 | `components/viz/MetricQueryEditor.tsx:527` | `"mqe-color-x"` | aria-label | ghost · icon (aria-label required) |
| 67 | `components/viz/MetricQueryEditor.tsx:978` | `"mqe-addq"` | text | ghost · sm (left icon Plus) |
| 68 | `components/viz/MetricQueryEditor.tsx:979` | `"mqe-addq"` | text | ghost · sm (left icon Plus) |
| 69 | `components/viz/GroupedMetricPicker.tsx:86` | `"mqe-pickbtn"` | aria-label | ghost · sm — verify manually |
| 70 | `components/viz/GroupedMetricPicker.tsx:98` | `{'mqe-facet' + (facet === f.key ? ' on' : '')}` | **none** | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 71 | `components/viz/GroupedMetricPicker.tsx:107` | `{'mqe-opt' + (m.name === value ? ' on' : '')}` | title | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 72 | `components/viz/GroupedMetricPicker.tsx:125` | `{'mqe-opt' + (m.name === value ? ' on' : '')}` | title | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 73 | `components/ai/ChatBubble.tsx:259` | `no class + inline style` | title | ghost · sm — verify manually |
| 74 | `pages/Runbook.tsx:567` | `no class + inline style` | text | ghost · sm — verify manually |
| 75 | `pages/AdminClickhouse.tsx:2873` | `{def === 'strict' ? 'on' : ''}` | title | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 76 | `pages/AdminClickhouse.tsx:2876` | `{def === 'entry' ? 'on' : ''}` | title | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 77 | `pages/Traces.tsx:1472` | `no class + inline style` | title | ghost · icon (aria-label required) |
| 78 | `pages/Traces.tsx:1549` | `no class` | text | ghost · sm — verify manually |
| 79 | `pages/Dashboard.tsx:432` | `"sec" + inline style` | title | secondary · sm |
| 80 | `pages/Profiling.tsx:146` | `"sec" + inline style` | **none** | secondary · sm |
| 81 | `pages/Profiling.tsx:353` | `no class + inline style` | **none** | ghost · icon (aria-label required) — verify manually |
| 82 | `pages/Profiling.tsx:388` | `"sec" + inline style` | **none** | secondary · sm |
| 83 | `pages/AdminSql.tsx:314` | `no class + inline style` | text | ghost · sm — verify manually |
| 84 | `pages/AdminSql.tsx:326` | `no class + inline style` | title | ghost · sm — verify manually |
| 85 | `pages/AdminSql.tsx:341` | `no class + inline style` | title | ghost · sm — verify manually |
| 86 | `pages/settings/ZoomChannelPicker.tsx:116` | `"sec" + inline style` | title | secondary · sm |
| 87 | `pages/clusters/NamespaceCombobox.tsx:52` | `no class + inline style` | title | ghost · icon (aria-label required) |
| 88 | `pages/explore/RecentQueries.tsx:75` | `no class + inline style` | title | ghost · sm — verify manually |
| 89 | `pages/explore/SplitByPicker.tsx:90` | `"fb-chip-x"` | aria-label | ghost · icon (aria-label required) |
| 90 | `pages/explore/QueryRow.tsx:70` | `no class + inline style` | title | ghost · sm — verify manually |
| 91 | `pages/explore/QueryRow.tsx:161` | `"fb-chip-x"` | aria-label | ghost · icon (aria-label required) |
| 92 | `pages/alerts/StatementPicker.tsx:53` | `"stmt-pick-row"` | title | ghost · sm — verify manually |
| 93 | `pages/service/OperationsTable.tsx:496` | `"btn-bare trend-spark-btn"` | title | ghost · sm — verify manually |
| 94 | `pages/service/ServicePodsTab.tsx:152` | `{source === v ? 'on' : ''}` | title | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 95 | `pages/service/ServicePodsTab.tsx:158` | `{view === 'flat' ? 'on' : ''}` | text | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 96 | `pages/service/ServicePodsTab.tsx:159` | `{view === 'cluster' ? 'on' : ''}` | title | toggle → ghost · sm + aria-pressed (if single-select: SegmentedControl/ToggleGroup) |
| 97 | `pages/service/ServicePodsTable.tsx:118` | `"pods-group__caret"` | aria-label | ghost · icon |

**Accessibility findings:** 10 rows have no `aria-label`, no `title` and no visible text (icon/glyph-only or empty-content) → must become `size="icon"` with a required `aria-label`. The 7 comment matches mean the ratchet counts `<button` inside comments; the counter should strip comments before the Phase 2 baseline.

### 2.4 `role="button"` on non-buttons (23)

Host elements: `span` ×10, `div` ×4–5, `tr` ×2, rest in SVG/graph code. Sites:

- `features/anomalies/AnomaliesPage.tsx:568`
- `features/anomalies/ProblemsSection.tsx:333`
- `features/anomalies/ProblemsSection.tsx:335`
- `features/anomalies/ProblemsSection.tsx:624`
- `components/MetricPanel.tsx:117`
- `components/ServiceNeighbors.tsx:87`
- `components/TopologyFlowGraph.tsx:431`
- `components/TopologyFlowGraph.tsx:492`
- `components/TopologyFlowGraph.tsx:531`
- `components/TraceWaterfall.tsx:615`
- `components/TraceWaterfall.tsx:621`
- `components/LogFieldsPanel.tsx:281`
- `components/traces/shared.tsx:85`
- `components/ai/insightRow.tsx:220`
- `components/chart/PanelLegend.tsx:56`
- `components/ai/AIDrawerBody.tsx:259`
- `pages/Dashboard.tsx:844`
- `pages/Metrics.tsx:333`
- `pages/Logs.tsx:1089`
- `pages/Logs.tsx:1186`
- `pages/Trace.tsx:800`
- `pages/Trace.tsx:826`
- `pages/trace/TraceLogsPanel.tsx:48`

These are clickable non-button elements (rows, legend items, graph nodes). Most are **out of scope** per the brief ("do not migrate tables/charts"); list kept for completeness. None of them is `<a role="button">`, so the proposed ESLint selector for that pattern would currently match 0 sites.

## 3. Tokens in use

- `:root` defines **77 tokens**: colors (`--bg0..3`, `--bg`, `--surface`, `--border`, `--border-strong`, `--text`, `--text2`, `--text3`, `--text-faint`, `--accent`, `--accent2`, `--accent-soft`, `--accent-bg`, `--accent-border`, `--on-accent`, `--brand`, `--brand2`, `--ok`, `--err`, `--warn`, `--info`, `--fatal`, `--purple`, `--indigo`, `--orange`, `--teal`, `--green`), shadows (`--shadow-sm/md/pop/modal`), `--backdrop`, radius (`--radius-xs/sm/-/lg`), spacing scale `--sp-1..8`, type scale `--fs-3xs..xl`, and a 17-step z-index ladder (`--z-*`).
- Most used in TSX: `--text3` 1162, `--text2` 634, `--border` 433, `--err` 371, `--warn` 220, `--text` 185, `--bg2` 178, `--accent2` 160, `--ok` 137, `--bg1` 124, `--accent` 83.
- Hex literals: **116** in `.ts/.tsx` (mostly chart palettes / theme resolution), **109** in `globals.css` (theme blocks).
- Duplicates to clean up before any mapping: `--text-2` next to `--text2`; `--on-accent`, `--backdrop`, `--fatal`, `--shadow-modal` are declared twice in `:root`.

shadcn mapping (no new colors) would be:
`--background→--bg`, `--foreground→--text`, `--primary→--accent`, `--primary-foreground→--on-accent`, `--secondary→--bg2`, `--muted→--bg1`, `--muted-foreground→--text3`, `--accent→--bg3` (shadcn's "accent" is the hover surface, **not** our accent — name clash), `--destructive→--err`, `--border/--input→--border`, `--ring→--accent`, `--radius→--radius`. Written once per theme (dark `:root`, light, redhat).

## 4. Radix vs Base UI

- **Neither is installed.** No `@radix-ui/*`, no `@base-ui-components/*`.
- `@floating-ui/*` and `@emotion/*` are already in the bundle via `@grafana/ui` (charts only).
- If shadcn is adopted: **Radix**. It is the shadcn default, mature, and the tooltip/popover primitives are the ones the registry is written against. Base UI would put us on the less-trodden path for no gain here.

## 5. Bundle impact (estimate, not measured)

Baseline: main CSS `index-*.css` = 124 KB raw / **22.7 KB gz**.

| Addition | Estimate (gz) | Note |
|---|---|---|
| Tailwind v4 utilities for button + button-group + tooltip | 3–6 KB CSS | JIT; grows with every migrated page |
| Tailwind preflight | ~2 KB CSS | **must be disabled** — it resets `button`, `h1..h6`, `img`, lists that globals.css styles today |
| `class-variance-authority` + `clsx` + `tailwind-merge` | ~8–10 KB JS | `tailwind-merge` is the bulk (~6–7 KB) |
| `@radix-ui/react-slot` | ~1 KB JS | |
| `@radix-ui/react-tooltip` (+ popper, portal, presence) | ~10–14 KB JS | may share `@floating-ui/dom` with `@grafana/ui` |
| **Total** | **~20–35 KB gz** | measured precisely with `rollup-plugin-visualizer` after init |

Build-time cost: a PostCSS/Tailwind step in Vite; the Docker image build needs no change.

## 6. Risks and recommendation

**Risks of Phase 1–2 as written**

1. **Two styling systems.** 4.3k lines of global CSS stay (tables, charts, pages are out of scope), and Tailwind utilities come in next to them. Specificity: the repo has element-level rules like `button.sm { … }` (0,1,1) that beat single Tailwind utilities (0,1,0) → migrated buttons can silently render wrong (this exact class of bug is documented in `Chip.tsx` / `IconButton.tsx`).
2. **Features already exist.** `loading`, required `variant`, type-level `aria-label` on icon buttons are already enforced by `Button`/`IconButton`. Phase 1's "extend button.tsx" re-builds them.
3. **Variant gaps.** `ghost-danger` (34), `accent` (23) and size `xs` (26) have no shadcn equivalent → either custom variants (drift from upstream shadcn) or visible changes on 83 call sites.
4. **Deleting the legacy Button** touches ~700 call sites (Button + IconButton + LinkButton) plus ~6 existing guard tests keyed to current class names.
5. **Three themes.** The shadcn variable file needs dark/light/redhat blocks; shadcn's own `.dark` class convention does not match `data-theme`.
6. Project constraint: `CLAUDE.md` currently says "no Tailwind (CSS vars in `globals.css`)". Adopting it is an explicit architectural decision and should be logged in `docs/DECISIONS.md`.

**Option A — as briefed.** Tailwind (preflight off) + shadcn on Radix; add button, button-group, tooltip; custom `xs`, `accent`, `ghost-danger` variants; map tokens for three themes; migrate page by page; delete legacy atoms last. Cost: ~20–35 KB gz, ~700 call sites, a permanent dual-CSS setup.

**Option B — recommended: same goals, current atoms.**
- ESLint `no-restricted-syntax`: forbid `JSXOpeningElement[name.name="button"]` and `[role="button"]` outside `components/ui/`, per-line disable with justification (exactly as briefed).
- Add `ButtonGroup` (toolbar, ghost buttons) and `Tooltip` atoms to `components/ui/` (tooltip can use the `@floating-ui` already in the bundle, ~0 KB new).
- Dev-only `/design` route rendering every variant × size × state.
- Icon rule: lucide only, 16 px / 14 px sm, left of label, chevron right — enforced in `Button`'s `leftIcon`/`rightIcon` and a lint rule on unicode glyphs inside buttons.
- Migrate the 90 raw buttons page by page with the table in §2.3 (one commit per area); ratchet goes to 0; comment-stripping fix in the ratchet.
- Bundle: ~0–2 KB. No second styling system, no theme remapping, no call-site churn in the 584 already-correct buttons.

Both options deliver: one component, lint-enforced, visual catalog, a11y-enforced icon buttons, migration of all raw buttons. The difference is whether we also switch the styling engine.

**Decision needed:** A or B. On approval of either, Phase 1 starts as one reviewable change.
