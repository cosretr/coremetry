# shadcn Phase 1 — foundation implementation plan

Status: **APPROVED 2026-10-30 — §12 operator rulings override §1–§11 where they differ.** Written section by section so a partial file stands on its own. Inputs: `docs/frontend/shadcn-audit.md` (§0/§7/§8/§9/§10; **§11 overrides**), operator decisions (scratchpad `shadcn-operator-decisions.md`), the three sweeps, `.claude/skills/frontend-conventions`, `.claude/skills/frontend-design-system`. Snapshot: `main` @ v0.10.1604 (React 19.3.1 landed v0.10.1600; Grafana bridge fix v0.10.1595; CoSRE lazy landed). Nothing in this file is implemented; it is the plan the implementer executes release by release. Numbers marked *(est.)* are estimates; everything else is read from the repo at the cited file:line.

## 1. Radix in page-level chunks only (Phase 1 acceptance criterion)

**Criterion (audit §11 "Bütçe"):** no module from `radix-ui` / `@radix-ui/*` / `cmdk` / `sonner` / their transitive runtime deps / `components/shadcn/**` is in the entry page's initial-load set (= `index.html` `<script type=module>` + every `<link rel=modulepreload>`), and the initial-load JS gz total stays under 347 KB. Measured by a script, enforced in CI, written to `migration-log.md` every release.

### 1.1 Why the chunk config decides this (read `frontend/vite.config.ts:61-83`)

| Fact | Evidence | Consequence for Radix |
|---|---|---|
| `advancedChunks` groups form ONE chunk per group name; a group's modules are pulled out of every other chunk | `vite.config.ts:72-77` comment ("modules of that group will be removed from other groups"); the v0.9.708 incident (react CJS pulled into `grafana`, every page statically imported it) | A dependency that lands in a group chunk is shared by **all** its importers, eager and lazy alike |
| Catch-all `{ name: 'vendor', test: /node_modules/, priority: 10 }` | `:81` | Today's eager `vendor-WCX…js` (44.5 KB raw / 15.0 KB gz) is this group: lucide + `@tanstack/virtual-core`, statically imported by the shell → **preloaded**. Without a dedicated group, Radix/cmdk/sonner/floating-ui fall into this same chunk and ride the entry preload even when only lazy pages import them |
| Entry preload = the entry's static import closure | `dist/index.html` modulepreload list; 22 files today (scratchpad `dist-r3`) | A group chunk imported only from lazy page chunks is **not** preloaded — it is fetched by `__vitePreload` when the page chunk loads |

**Decision: a dedicated `radix` group is required** (not harmful). It is harmful only if an eager module imports it — which is exactly what the shell import ban (§1.2) and the CI proof (§1.3) forbid. Add, between `charts` (95) and the catch-all (10):

```ts
// v0.10.NEXT — Radix/shadcn runtime stays OUT of the eager vendor catch-all
// (:81). Transitive deps listed explicitly: unmatched ones would drop into
// the catch-all and be preloaded with lucide/virtual-core.
{ name: 'toast', test: /node_modules\/sonner[\/]/, priority: 86 },
{ name: 'radix', test: /node_modules\/(radix-ui|@radix-ui|cmdk|@floating-ui|react-remove-scroll(-bar)?|react-style-singleton|use-callback-ref|use-sidecar|aria-hidden|get-nonce)[\/]/, priority: 85 },
```

Priority 85/86 sit above `router`/`tanstack`/`otel`/`graph` (80) so nothing swallows them, below `charts` (95). `sonner` gets its own `toast` group so the lazy Toaster (§1.4 A) fetches ~9-10 KB *(est.)* on the first toast instead of the whole Radix group. `class-variance-authority`, `clsx`, `tailwind-merge` are **not** in the group: ~8-10 KB gz *(est., audit §7)*, used by `cn()` in every migrated page chunk; if a shell composite needs `cn()` later they belong in the eager vendor anyway. The proof script (§1.3) treats them as allowed in the entry.

The transitive list must be verified at install time: `npm ls radix-ui cmdk sonner --all` after R5 (§4); any new `node_modules/<x>` that appears in a `radix-*.js` sourcemap or in `vendor-*.js` after the first page migration is added to the regex in the same release.

### 1.2 Shell import ban (Phase 1 rule)

| Rule | Scope | Enforcement |
|---|---|---|
| No import of `@/components/shadcn/*`, `@/components/coremetry/*` that wraps Radix (`Dialog`, `Sheet`, `Popover`, `DropdownMenu`, `ContextMenu`, `Tooltip`, `Command`, `Select`, `Tabs`, `ScrollArea`), `radix-ui`, `@radix-ui/*`, `cmdk`, `sonner` | `src/components/AppShell.tsx`, `Topbar.tsx`, `Sidebar.tsx`, `CommandPalette.tsx`, `TimeRangePicker.tsx`, `ContextBar/**`, `CopilotChatGate.tsx`, `Toaster.tsx`, `GlobalShortcuts.tsx`, `AnnouncementBanner.tsx`, `App.tsx`, `main.tsx` — i.e. every module in today's eager closure | (a) ESLint `no-restricted-imports` override block for those files (patterns above) — fast, line-precise; (b) the build proof in §1.3 — the only one that sees transitive edges |
| Shell composites allowed in Phase 1 | `coremetry/PageShell`, `coremetry/PageControls` (moved unchanged, decision k; no Radix) | sourcemap check passes because they import no Radix |
| `/design` route | dev-only (`App.tsx:90-96` DEV ternary, `Design.pin.test.ts`) → never in the prod closure | unchanged |

Today's eager closure (shell) imports from `components/ui` only (`Topbar.tsx:1-12`, `Sidebar.tsx:20`, `CommandPalette.tsx:2-3`, `TimeRangePicker.tsx:15-17`) — the ban changes nothing in Phase 1; it prevents the first Phase 2 slice from quietly pulling Radix into the shell.

### 1.3 CI proof — `frontend/scripts/bundle-budget.mjs`

Node ≥22, zero dependencies (reads `dist/`; `build.sourcemap: true` is already on, `vite.config.ts:41`). **No `build.manifest`:** the Vite/rolldown manifest lists chunks, not the modules inside them; the `.map` files already shipped do (`sources[]`), and `index.html`'s preload list IS the browser's initial fetch set. Nothing new is embedded in the Go binary (`//go:embed all:frontend/dist` would have picked up `dist/.vite/manifest.json`).

| Step | What |
|---|---|
| 1 | Parse `dist/index.html`: `<script type="module" src>` + every `<link rel="modulepreload" href>` → initial-load set `S` (today 22 files) |
| 2 | For each `f ∈ S`: raw bytes, gz bytes (`zlib.gzipSync`, default level — same as Vite's build reporter so the number matches the build log), and `sources[]` from `f.map` |
| 3 | Forbidden-source regex over all `sources[]` of `S`: `/node_modules\/(radix-ui|@radix-ui|cmdk|sonner|@floating-ui|react-remove-scroll|aria-hidden)\//` and `/src\/components\/shadcn\//` → any hit = **FAIL** (lists chunk + first 5 offending sources) |
| 4 | `initialGz = Σ gz(S)`; `mainGz = gz(index-*.js)`; `BUNDLE_BUDGET_KB` (default 347) → `initialGz > budget` = **FAIL** |
| 5 | Also reports (no gate): `radix-*.js` size if present, number of chunks containing shadcn sources (expected 0 in Phase 1, >0 from slice 1) |

Output (stdout, also written to `dist/bundle-budget.txt` so the release can paste it):

```
bundle-budget  dist/index.html  22 preloaded JS
  65.8 KB gz  vendor-DvFIeGfB.js
  53.3 KB gz  index-CeSG4r8q.js
  ...
initial-load JS gz: 251.8 KB   budget 347 KB   main (index-*.js) 53.3 KB gz   OK
forbidden in entry: none (radix/cmdk/sonner/shadcn)   radix chunk: absent
| initial 251.8 KB gz | main 53.3 KB gz | budget 347 | radix-in-entry 0 |
```

(Numbers above are this plan's cross-check on the scratchpad `dist-r3` build with gzip -9: 22 files, 251.8 KB. The operator's figure after React 19 + CoSRE lazy is **≈258.3 KB gz, main 54.6 KB gz**; the difference is gzip level + build — the script pins ONE method, zlib default, and R2 records the first canonical number.)

| Where it runs | Line |
|---|---|
| `.github/workflows/ci.yml` frontend job, new step directly after `Build (Vite SPA)` (`:46-48`): `- name: Bundle budget (initial ≤ 347 KB gz, no Radix in entry)` / `working-directory: frontend` / `run: node scripts/bundle-budget.mjs` | blocks the job; the `backend`/`lint`/`security` jobs `needs: frontend` so a red budget blocks the tag path |
| `frontend/package.json` script `"bundle:check": "vite build && node scripts/bundle-budget.mjs"` | local release gate for every migration release (added to the `/release` skill's frontend gate line and to CLAUDE.md's release flow in R1) |
| `src/styles/bundleBudget.pin.test.ts` (vitest) | pins: `vite.config.ts` contains the `radix` group regex and `sourcemap: true`; `ci.yml` contains the step; `scripts/bundle-budget.mjs` contains the forbidden regex and `347`. Reason: "tested but unreachable" — a gate that exists but is not wired is green and useless |

**Into `migration-log.md`:** every release row carries the script's last line verbatim (`initial … | main … | budget 347 | radix-in-entry 0`) next to the §7 estimate for that release (format §9). Source of the number = the CI step log of the tagged commit (or the local `npm run bundle:check` immediately before tagging).

### 1.4 Toast renderer swap (decision h) vs this criterion

`Toaster` is mounted in the shell (`AppShell.tsx:10`, `:293`); `lib/toast.ts` is a module-level pub/sub (21 importing files, 50 `toast(` sites). A static `sonner` import in `Toaster.tsx` would put sonner (~9-10 KB gz *(est.)* + its CSS) into the entry — a direct violation.

| Option | Entry cost | Behaviour | Verdict |
|---|---|---|---|
| **A. Lazy gate (recommended)** — `components/Toaster.tsx` becomes a `ToasterGate`: eager, subscribes to `subscribeToasts`, on the FIRST entry sets `mounted` and `React.lazy(() => import('./coremetry/Toaster'))` (sonner inside); entries that arrive before the lazy module mounts are queued and replayed — the exact pattern of `CopilotChatGate.tsx:45-77` (v0.10.1599, `queue.current` + `ReplayQueued`) | 0 | First toast of a session is delayed by one chunk fetch (same-origin, ~5-15 KB *(est.)*); later toasts immediate. `toast.*` API untouched (decision h) | ship in R10 |
| B. Keep hand-rolled `Toaster` in Phase 1; swap in the shell slice (Phase 2 last) | 0 | No change now; sonner lands when the shell migrates and the entry budget is re-measured anyway | fallback if A's first-toast delay is rejected |
| C. Static sonner in shell | +9-10 KB gz | — | violates the criterion |

The shadcn registry `sonner.tsx` imports `next-themes` (`registry: deps ['sonner','next-themes']`) — it is **not installed**; `components/coremetry/Toaster.tsx` uses the `sonner` package directly with `theme={isLightTheme(currentTheme()) ? 'light' : 'dark'}` from `lib/theme.ts` (§2.4) and re-renders on `useThemeTick`. The 4 `toast-*` CSS classes (`globals.css:3599-3646`) are deleted in the same release (CSS family goes with the component). → Operator question Q1 (§11): A vs B.

## 2. One token source for all three themes

### 2.1 Files

| File | Role | Notes |
|---|---|---|
| `src/styles/globals.css` (exists, 6,436 lines) | **stays the value source** for every Coremetry colour/geometry token: dark `:root` (:1-245), `[data-theme="light"]` (:246-317), `[data-theme="redhat"]` (:318-390), `[data-theme="redhat"] #sidebar` (:391-417) | not duplicated; tokens.css only references it. All ~32 `src/styles/*.test.ts` gates keep reading it by path |
| **`src/styles/tokens.css`** (new) | shadcn semantic vars + status palette + series tokens, **by reference** (`--background: var(--bg0)`), plus the Tailwind `@theme inline` block and `@custom-variant dark` | the "one token source" for shadcn: a thin adapter, zero new colours; hex appears only in `--series-*` (mirrors `SERIES_PALETTES`, pinned equal) |
| **`src/styles/index.css`** (new, 8 lines) | CSS entry: layer order + imports | replaces `./styles/globals.css` + `uplot/dist/uPlot.min.css` in `main.tsx:8,14` (single eager owner preserved — v0.9.708 note stays) |
| `src/styles/tokens.pin.test.ts` (new) | every `var(--x)` target in tokens.css is declared in globals.css for dark, light AND redhat (three-block parse, same regex as `seriesPalette.test.ts:12-18`); no hex outside `--series-*`; `--series-N` == `SERIES_PALETTES[theme][N-1]`; the dark-variant selector string; `:root, [data-theme]` selector present | gate |

`index.css`:

```css
@layer legacy, theme, base, components, utilities;   /* legacy = today's CSS, lowest */
@import 'uplot/dist/uPlot.min.css' layer(legacy);      /* order inside the layer = today's main.tsx order */
@import './globals.css' layer(legacy);
@import 'tailwindcss/theme.css' layer(theme);          /* NO preflight: theme + utilities only */
@import 'tailwindcss/utilities.css' layer(utilities);
@import './tokens.css';
```

Why a layer: cascade layers beat unlayered CSS only in one direction — **unlayered wins over every layer**. If globals.css stays unlayered, Tailwind utilities (`@layer utilities`) lose to `tbody td { padding: 9px 12px }` (`:1613`) and to `[data-density="compact"] .controls > button {…}` (`:3522`) on migrated pages, and the audit's "utilities layered after globals.css so migrated pages win" (§9.2 #1) is false as written. Putting uPlot CSS + globals.css into ONE `legacy` layer in today's order keeps every cascade decision *inside* the old CSS identical; only old-vs-new flips. Known exposure: `@grafana/ui` emotion styles are injected unlayered at runtime (CorePanel only) and would now beat globals' `.u-*` rules (17 rules, `:2300-2342`) even where globals is more specific → R4 ships with a manual 3-theme Playwright pass on one CorePanel page (operator question Q4). Inline `style={{` is unaffected (always wins) — which is why §11 n's "zero inline style on a migrated page" matters.

### 2.2 Mapping — shadcn var ← Coremetry var (resolved values per theme from globals.css, no new colours)

Selector for the base block is `:root, [data-theme]` (not only `:root`): a custom property that references `var(--bg0)` is **computed on the element where it is declared and inherited as a value**, so a `<div data-theme="light">` container (needed by `/design`, §8) or the redhat `#sidebar` remap (`:391-417`) would not re-resolve `--background` unless the shadcn vars are re-declared on that element too. `[data-theme]` covers the containers; the sidebar remap gets its own re-declaration in the shell slice (OQ p), not in Phase 1.

| shadcn var | ← Coremetry | dark (`:root`) | light | redhat | Note |
|---|---|---|---|---|---|
| `--background` | `--bg0` | `#1e2124` :13 | `#f4f6f8` :250 | `#f0f0f0` :319 | page floor |
| `--foreground` | `--text` | `#e6ebf1` :58 | `#212429` :261 | `#151515` :333 | |
| `--card`, `--popover` | `--bg1` | `#24272b` :14 | `#ffffff` :251 | `#ffffff` :320 | `--bg` alias is the same value (D7, `surfaceTokens.test`) |
| `--card-foreground`, `--popover-foreground` | `--text` | as above | | | |
| `--primary` | `--accent` | `#2d73d8` :71 | `#2572de` :270 | `#0066cc` :343 | fill |
| `--primary-foreground` | `--on-accent` | `#ffffff` :131 | inherits | inherits | |
| `--secondary` | `--bg2` | `#2c3035` :15 | `#f0f3f6` :252 | `#f5f5f5` :321 | secondary button fill |
| `--secondary-foreground` | `--text` | | | | |
| `--muted` | `--bg2` | | | | |
| `--muted-foreground` | `--text2` | `#c2c9d1` :59 | `#474c54` :262 | `#4f5255` :334 | audit draft; `--text3` is the ≥4.5-everywhere tertiary if a slice needs it |
| **shadcn `--accent` / `--accent-foreground`** | **NOT declared** — name collision with Coremetry `--accent` (primary blue, ~300 rules) | | | | mapped at Tailwind level only: `--color-accent: var(--bg3)` (`#363a40` / `#e4e8ed` / `#ebebeb`), `--color-accent-foreground: var(--text)` — hover/selected surface of menu items + ghost buttons |
| `--destructive` | `--err-solid` | `#cc4740` :88 | `#c42a28` :281 | `#c9190b` :356 | shadcn uses it as a FILL (`bg-destructive text-white`); Coremetry rule: filled red = `--err-solid`, red TEXT = `--err` → error text goes through `--status-critical-fg`, never `text-destructive` |
| `--destructive-foreground` | `--on-accent` | | | | |
| `--border` | exists (same meaning) — **not redeclared** | `#41454a` :54 | `#dde2e9` :258 | `#d2d2d2` :326 | |
| `--input` | `--border-control` | `#51565b` :103 | `#b2b7be` :296 | `#b6b7b8` :367 | field border (Y4) |
| `--ring` | `--focus` | `#6ba6fb` :74 | `#2572de` :273 | `#0066cc` :346 | |
| `--radius` | exists per theme — **not redeclared** | `8px` :149 | inherits | `4px` :378 | redhat keeps 2/3/4/8 |
| `--color-field` (Tailwind only) | `--field-bg` | `var(--bg2)` :21 | `var(--bg1)` :255 | `var(--bg1)` :325 | v0.10.1423 white fields on light themes; shadcn `input.tsx` is `bg-transparent` — the Field composite (Phase 2) adds `bg-field` |
| `--sidebar*`, `--chart-1..5` | not declared | | | | sidebar = shell slice; charts = `--series-*` below |

Status palette (badges, cells, StatusBadge; fg = text/dot/line ≥4.5, bg = pre-mixed badge ground, solid = white-text fill):

| Token | ← | dark | light | redhat | Today's consumer |
|---|---|---|---|---|---|
| `--status-ok-fg` / `-bg` / `-border` | `--ok` / `--ok-bg` / `--ok` | `#4db96b` / `#293933` | `#047735` / `#e8f3ed` | `#317928` / `#eef4ed` | `.b-ok` :1933 (bg+color, no border today → border = fg, outline variant only) |
| `--status-warn-fg` / `-bg` / `-border` / `-solid` | `--warn` / `--warn-bg` / `--warn` / `--warn-solid` | `#f3b94c` / `#3d392f` / — / `#f3b94c` | `#8f5c04` / `#f5f0e8` / — / `#b98206` | `#795600` / `#fdf5e0` / — / `#b08307` | `.b-warn` :1939 |
| `--status-critical-fg` / `-bg` / `-border` / `-solid` | `--err` / `--err-bg` / `--err` / `--err-solid` | `#fe7f73` / `#3e3234` / — / `#cc4740` | `#c42a28` / `#faecec` / — / `#c42a28` | `#c9190b` / `#fbefee` / — / `#c9190b` | `.b-err` :1934 |
| `--status-info-fg` / `-bg` | `--info` / `--info-bg` | `#6ba6fb` / `#253040` | `#1263cd` / `#ebf2fc` | `#0066cc` / `#e7f1fa` | `.b-info` :1940 |
| `--status-unknown-fg` / `-bg` / `-border` | `--text2` / `--bg3` / `--border` | `#c2c9d1` / `#363a40` | `#474c54` / `#e4e8ed` | `#4f5255` / `#ebebeb` | `.b-gray` :1943; `statusColor('no-data')` = `--text-faint` stays for chart "no data" (disabled tone, `lib/statusColor.ts:12`) |
| `--status-fatal-fg` | `--fatal` | `#fe7f73` | `#a40e26` | `#a30000` | `.s-fatal` |
| `--series-1..8` | `SERIES_PALETTES` (`chartFmt.ts:159-163`) | `#288dd4 #06d8d9 #cb4f73 #b5b614 #95b6fe #d76f04 #ac78d2 #fca0e8` | `#045c93 #0c9999 #921545 #898905 #567cd3 #d46e07 #7b489e #bf68ae` | = light (`:162` light==redhat) | DOM consumers of `seriesPalette()` (KindBadge, LogsHistogram, endpoints/detailSections, httpMetricsViz) may use `var(--series-N)`; canvas path unchanged |

**How it reaches uPlot without touching rendering:** `seriesRoleColor` (`lib/chart/seriesRole.ts:23-29`) returns `var(--err)` / `var(--ok)` / `var(--text3)`; `resolveVar` (`lib/chart/resolveVar.ts:10-14`) resolves any `var()` at draw time; `useThemeTick` redraws on `data-theme` flips. Phase 1 (R4) defines `--status-*` as references to the SAME declarations, so nothing in the chart path changes. R8 (StatusBadge release) re-points `seriesRole.ts` to `var(--status-critical-fg)` / `var(--status-ok-fg)` — identical resolved values (asserted by `tokens.pin.test.ts`: `--status-critical-fg`'s declaration is `var(--err)`), one token name shared by badge, cell tone and chart role. `SERIES_PALETTES` stays the JS source: the canvas needs hex at build time and `inkOn()` (`chartFmt.ts:170-183`) returns white for non-hex input; `--series-*` mirror it (pinned equal), they do not replace it. Heatmap ramp (`heatmapRamp.ts:20`) untouched (rendering).

### 2.3 Tailwind: v4, and the collision guard

| Question | Answer | Reason |
|---|---|---|
| v3 vs v4 | **v4** (`tailwindcss` 4.3.3 + `@tailwindcss/vite` 4.3.3, 2026-07-16) | `@tailwindcss/vite@4.3.3` peers `vite ^5.2 \|\| ^6 \|\| ^7 \|\| ^8` (verified `npm view`) — Vite 8.3.4 ✓, no PostCSS config needed (repo has none). CSS-first: `tokens.css` IS the config (one token file). shadcn's current registry (`new-york-v4`: `data-slot`, no `forwardRef`) targets v4; v3 would mean the retired `new-york` style. `@theme inline` solves the variable collisions below; v3 has no equivalent (its utilities read a JS config). Preflight: import `theme.css` + `utilities.css` only (above); v3 would use `corePlugins.preflight:false` |
| darkMode | `@custom-variant dark (&:where([data-theme="dark"], [data-theme="dark"] *, html:not([data-theme]), html:not([data-theme]) *));` | repo reality: dark = `:root` with no attribute OR `data-theme="dark"` (`globals.css:1`, no `.dark` class). `index.html:2` hardcodes `data-theme="redhat"` and the boot script (`:44-57`) always sets the attribute before paint with fallback `redhat` (`:50`), so "absent" only happens in jsdom/tests — covered anyway. Container-scoped (`[data-theme="dark"] *`) so `/design` cells work. Which shadcn classes need it: `dark:bg-input/30`, `dark:hover:bg-input/50` in `button.tsx`, `dark:` tints in `badge.tsx`/`select.tsx` |
| Variable namespace collisions | `@theme inline { --radius-*: initial; --shadow-*: initial; --font-*: initial; --text-*: initial; --color-*: initial; … }` then re-declare mapped names | Tailwind's default theme emits `--radius-sm` (.25rem), `--shadow-sm/-md`, `--font-mono`, which **Coremetry already declares** (`--radius-sm` 6px :148, `--shadow-sm/-md` :105/:118, `--font-mono` :185 — 155 radius usages in globals alone). `inline` makes utilities embed `var(--radius-sm)` instead of emitting a `:root` value, so Coremetry's declaration stays the only one. Wiping `--color-*` also removes Tailwind's palette → "no new colours" is enforced by the theme itself |

`tokens.css` theme block (sketch; the full mapping table above is the content):

```css
@custom-variant dark (&:where([data-theme="dark"], [data-theme="dark"] *, html:not([data-theme]), html:not([data-theme]) *));
:root, [data-theme] {
  --background: var(--bg0); --foreground: var(--text);
  --card: var(--bg1); --card-foreground: var(--text); --popover: var(--bg1); --popover-foreground: var(--text);
  --primary: var(--accent); --primary-foreground: var(--on-accent);
  --secondary: var(--bg2); --secondary-foreground: var(--text); --muted: var(--bg2); --muted-foreground: var(--text2);
  --destructive: var(--err-solid); --destructive-foreground: var(--on-accent);
  --input: var(--border-control); --ring: var(--focus);
  --status-ok-fg: var(--ok); --status-ok-bg: var(--ok-bg); /* … rest of the status table … */
}
:root, [data-theme="dark"] { --series-1: #288dd4; /* … */ }
[data-theme="light"], [data-theme="redhat"] { --series-1: #045c93; /* … */ }
@theme inline {
  --color-*: initial; --radius-*: initial; --shadow-*: initial; --font-*: initial; --text-*: initial;
  --color-background: var(--background); --color-foreground: var(--foreground);
  --color-accent: var(--bg3); --color-accent-foreground: var(--text);     /* shadcn "accent" = hover surface */
  --color-border: var(--border); --color-input: var(--input); --color-ring: var(--ring); --color-field: var(--field-bg);
  --color-status-ok: var(--status-ok-fg); /* … one Tailwind colour per status token … */
  --radius-xs: var(--radius-xs); --radius-sm: var(--radius-sm); --radius-md: var(--radius); --radius-lg: var(--radius-lg);
  --shadow-sm: var(--shadow-sm); --shadow-md: var(--shadow-md); --shadow-pop: var(--shadow-pop); --shadow-modal: var(--shadow-modal);
  --font-mono: var(--font-mono);
  --text-xs: var(--fs-xs); --text-sm: var(--fs-sm); --text-base: var(--fs-md); --text-lg: var(--fs-lg); --text-xl: var(--fs-xl);
}
```

Type scale remap is deliberate: shadcn components are written in `text-sm` (14px); the app body is 12px (`--fs-sm`, `:180`). Mapping `--text-sm → --fs-sm` makes the generated files match the house scale **without editing them** (only `button.tsx`/`table.tsx` may be edited, §5). Spacing stays Tailwind's 4px scale — it covers the `--sp-1..8` ladder exactly (`p-0.5`=2, `p-1`=4, `p-1.5`=6, `p-2`=8, `p-2.5`=10, `p-3`=12, `p-4`=16, `p-6`=24).

### 2.4 Theme default → ONE source (`lib/theme.ts`, decision 7)

| Site | Role today | Fallback today | Phase 1 change (R3) |
|---|---|---|---|
| `index.html:2` `data-theme="redhat"` | pre-JS writer | — | **keep**; pinned equal to `DEFAULT_THEME` by `theme.singleSource.pin.test.ts` |
| `index.html:44-57` boot script | pre-paint writer (`localStorage coremetry-theme`), `theme-color` meta | `redhat` (:50, :52) | **keep verbatim** (cannot import a module before paint); fallback literal pinned equal to `DEFAULT_THEME`; colour map already pinned by `lib/themeColor.test.ts:12-25` |
| `lib/theme.ts:38-39` `currentTheme()` | reader | `redhat` | becomes THE reader. Add `export const DEFAULT_THEME: Theme = 'redhat'`; `export function readTheme(attr: string \| null): Theme` (pure: `'dark'`/`'light'`/`'redhat'` else `DEFAULT_THEME`); `currentTheme()` = `typeof document === 'undefined' ? DEFAULT_THEME : readTheme(document.documentElement.getAttribute('data-theme'))`; `export const isLightTheme = (t: Theme) => t !== 'dark'` |
| `lib/theme.ts:45` `cycleTheme`, `:55` `applyTheme` | writers (ThemeToggle.tsx:7, Sidebar.tsx:18) | — | unchanged |
| `lib/chartFmt.ts:193-195` `chartTheme()` | reader | `dark` | body → `return currentTheme();` (`ChartTheme` = `Theme`). Behaviour change only where no document exists (node tests): fallback dark → redhat palette. `seriesPalette.test.ts` reads per-theme blocks explicitly (:12-25) → unaffected; grep `chartTheme()` in tests at implementation time |
| `lib/chart/dataFrame.ts:48-55` Grafana `chartTheme()` | reader; `attr !== 'light' && attr !== 'redhat'` (v0.10.1595) | `light` in node | `const isDark = !isLightTheme(currentTheme());` → node fallback redhat ⇒ `mode: 'light'`, same as today |
| `lib/useThemeTick.ts:24` | observer (`attributeFilter: ['data-theme']`) | — | unchanged |
| `components/coremetry/Toaster.tsx` (R10) | new reader | — | `isLightTheme(currentTheme())` + `useThemeTick` |
| **pin** `src/lib/theme.singleSource.pin.test.ts` | | | (1) `getAttribute('data-theme')` appears in no non-test `src/**` file except `lib/theme.ts` (today: `chartFmt.ts:194`, `dataFrame.ts:53`, `theme.ts:38`); (2) `index.html` attribute and boot fallback == `DEFAULT_THEME`; (3) `tokens.css` dark variant string present; (4) `readTheme(null) === DEFAULT_THEME`, `readTheme('x') === DEFAULT_THEME` |

Undefined-reference gate: `undefinedCssRefs.test.ts` checks TSX `var(--x)` against the CSS it reads; R4 adds `tokens.css` to its CSS source list so `var(--status-*)` / `var(--series-*)` in TSX are not reported as undefined. `colorLeaks.test.ts` scope is extended to `tokens.css` with `--series-*` declarations allowlisted (the only hex there).

## 3. `components/ui` ratchet measurement

### 3.1 `uiImportRatchet.test.ts` — what is counted

| Item | Definition |
|---|---|
| Population | every `src/**/*.{ts,tsx}` **excluding** `*.test.*` and everything under `src/components/ui/**` (the atoms importing each other are not adoption). Dev-only `pages/Design.tsx` IS counted (10 atoms today; it switches to shadcn in R10) |
| Source prep | `stripTsComments` (`styles/zLayers.test.ts`, the repo's standard stripper — v0.10.919 lesson: 12 of 97 "raw buttons" were comment text) |
| Statement | regex `import\s+(type\s+)?([\s\S]*?)\s+from\s+['"]([^'"]+)['"]` over the stripped source — multi-line imports are one statement; `import type` counts (a type import still couples the file to the atom and must go before the atom is deleted) |
| Resolution | specifier `@/components/ui[/…]` → direct; relative specifiers are `path.resolve`d against the importing file and kept only if they land inside `src/components/ui/` (covers `../ui`, `./ui/Button`, `../../components/ui/DataTable/DataTable`) |
| Barrel → atom | specifier `components/ui` or `components/ui/index`: each named binding (`Button`, `MenuItem`, `useConfirm`, `Row`, `Field as LabeledInput` → `Field`) is mapped to its atom FILE by **parsing `components/ui/index.ts` `export … from './X'` lines at test time** (sweep 2 used a hand map; parsing the barrel means a barrel edit cannot desync the ratchet). `DataTable/*` paths collapse to one atom `DataTable`; helpers (`useFocusTrap`, `buttonGroupContext`, `drawerWidth`, `emptyAreaClose`) are their own atoms |
| Direct path | `components/ui/Button` → atom `Button`; `components/ui/DataTable/VirtualTable` → `DataTable` |
| **Ratcheted number** | **importing FILES per atom** (a `CEILINGS` map like `tableUnityRatchet.test.ts:31-161`) **+ the headline total = Σ file×atom pairs**. Why files, not statements: a file that splits one barrel import into two statements would move the statement count with zero migration; file counts are stable under import-style edits and are what the audit §2 table already uses (comparable). Why per-atom and not only a total: a total-only ceiling lets `+5 Button / −5 Badge` pass; per-atom ceilings make the migration-log "atoms deleted" column verifiable, and an atom missing from `CEILINGS` has ceiling **0** = "ui/ is frozen, no new importer" (audit §9.2 #5) |
| Reported, not gated | import statements (for the log) |

**Baseline (this plan's read-only script, `main` @ v0.10.1604; frontend tree == HEAD):** 342 importing files · 671 import statements · **969 file×atom pairs**.

| Atom | Files | Atom | Files | Atom | Files |
|---|---|---|---|---|---|
| Button | 209 | Stack | 23 | PanelTitle | 10 |
| DataTable | 126 | Modal | 23 | PriorityBadge | 8 |
| Badge | 63 | Card | 19 | ActionRow | 8 |
| PageShell | 51 | TabStrip | 17 | ButtonGroup | 7 |
| IconButton | 50 | PageControls | 17 | Tooltip | 6 |
| Field | 37 | Drawer | 15 | OptionRow | 4 |
| Chip | 36 | KeyValue | 14 | EnterHint · MethodBadge · SectionUnavailable | 3 each |
| ConfirmDialog | 35 | Menu | 14 | useFocusTrap · PickerPopover · FacetMultiSelect · ColumnsPicker | 2 each |
| SegmentedControl | 35 | StatTile | 13 | RouteSkeleton · TileButton · IdentityChip · VirtualList | 1 each |
| LinkButton | 31 | SafeHref | 12 | | |
| DisclosureButton | 31 | SectionHead | 12 | | |

Badge is 63 here vs 62 in the audit §2 (sweep 2 skipped one `.ts` type importer); the test's own count at the R6 commit is the committed baseline — this table is the expected value, not the source. Ceilings only go down and are lowered in the migrating commit (same language as `buttonUnityRatchet.test.ts:13-14`). Also pinned: the frozen entry list of `components/ui/` (45 non-test entries today) — a new file under `ui/` is red.

### 3.2 Per-page inline-style counter (§11 n) — `inlineStyleRatchet.test.ts`

| Item | Definition |
|---|---|
| Unit | occurrences of the literal `style={{` in comment-stripped `.tsx` (tests excluded, `components/ui/**` excluded). `style={expr}` is **not** counted |
| Escape for computed values | `lib/cssVars.ts` (R6): `vars(v: Record<\`--${string}\`, string \| number>): CSSProperties` — type-level: only custom-property keys, so a progress width is `style={vars({ '--w': pct + '%' })}` + a CSS rule reading `var(--w)`; a static `fontSize: 11` cannot pass through it. "Zero `style={{`" is then achievable on every page |
| Page → files | `src/styles/pageFiles.ts` (shared by this ratchet, the raw-element ratchet and the migration log): `PAGE_FILES: Record<Page, string[]>` of file/dir prefixes. Initial keys = the Phase 2 slices (audit §3.1/§9.4) — Settings: `pages/Settings.tsx`, `pages/settings/`; RemoteCluster: `pages/settings/ClustersTab.tsx`, `pages/settings/SpanClusterValuesPanel.tsx`; Logs: `pages/Logs.tsx`, `pages/logs/`, `components/LogTable.tsx`, `LogsHistogram`, `LogFieldsPanel`, `LogPatternsPanel`, `LogPillEditor`, `LogContextModal`, `KqlSearchInput`; Trace: `pages/Trace.tsx`, `pages/trace/`, `components/TraceWaterfall.tsx`, `SpanDetail.tsx`, `SpanK8sSection.tsx`, `TracePeekDrawer.tsx`, `FlameGraph.tsx`; Traces: `pages/Traces.tsx`, `pages/traces/`, `components/traces/`; Explore: `pages/Explore.tsx`, `pages/explore/`, `components/MetricsExplorer.tsx`, `LogsExplorer.tsx`; Metrics: `pages/Metrics.tsx`, `components/viz/MetricQueryEditor.tsx`, `TimeSeriesPanel.tsx`; Shell: `AppShell`, `Topbar`, `Sidebar`, `CommandPalette`, `TimeRangePicker`. New slices add a key in their first release |
| Ceilings | per page in the test (`CEILINGS[page]`) + `TOTAL` for the whole tree so an unlisted page cannot grow; `MIGRATED: Page[]` — a page is "passed" only when its ceiling is 0 and it is listed here (the test asserts both) |
| Baseline (read-only count, v0.10.1604) | Settings 1,182 (63 files) · RemoteCluster 50 (2; subset of Settings) · Logs 94 (14) · Trace 150 (17) · Traces 103 (13) · Explore 141 (17) · Metrics 50 (3) · Shell 40 (5) · **TOTAL 5,244** (449 non-test `.tsx` outside `components/ui`). The sweep's 5,309 included `components/ui` and comments — one scope per table (decision 4): the ratchet's scope is the one above |

### 3.3 Raw-element ban + exemption (decision 8)

| Item | Today | Phase 1 (R5 lint scope, R6 ratchets) |
|---|---|---|
| ESLint rule | `ui/no-raw-button` inline in `eslint.config.js:30-67`; applied `:94-101` to `src/**/*.tsx`; ignores `src/components/ui/**`, `*.test.tsx`; `eslint-suppressions.json` = `{}` | rule renamed **`ui/no-raw-element`**: same AST visitor, `JSXOpeningElement` name ∈ {`button`, `input`, `select`, `textarea`} + `role="button"`; `input` with a **literal** `type` ∈ {checkbox, radio, file, hidden, range, color} is allowed (no shadcn equivalent in the install set — Q3); ignores add `src/components/shadcn/**`, `src/components/ai-elements/**` (exempt per decision 8). `components/coremetry/**` is NOT exempt (composites compose shadcn, never raw elements). Message names the replacement (`shadcn/input` via the Field composite, `native-select`, `textarea`) |
| Baseline mechanism | `eslint-suppressions.json` + `--prune-suppressions` (documented `eslint.config.js:20-22`, `eslintDisableReason.test.ts` guards reasons) | `npx eslint src --suppress-rule ui/no-raw-element` once in R6 → per-file counts committed; any file exceeding its count is red in CI (`ci.yml:37-38` ESLint step already blocks); the fixing commit runs `--prune-suppressions`. Line exemptions keep the `-- <reason>` requirement (`eslintDisableReason.test.ts` regex extended to the new rule name) |
| vitest twin | `buttonUnityRatchet.test.ts` (`MAX_RAW_BUTTONS 0`, `MAX_ROLE_BUTTON 0`, `MAX_REASONED_EXEMPTIONS 9`, `MAX_ICONBUTTON_TITLE 7`) | **`rawElementRatchet.test.ts`** (same walker + `exemptReasoned`): `MAX_RAW_INPUT`, `MAX_RAW_SELECT`, `MAX_RAW_TEXTAREA`, measured at the R6 commit. This plan's read-only count outside `components/ui`, comments stripped: `<input` **490** (119 files), `<select` **159** (69), `<textarea` **33** (19); with the allowed literal types removed (sweep split: checkbox 127, radio 5, file 6) the gated input count is ≈352 *(est.; the test measures)*. The sweep's 502/167/36 counted `components/ui` too — one scope, the ratchet's. Reason for the twin (as `buttonUnityRatchet.test.ts:26-27`): the path that runs only vitest must not pass |
| Existing ratchets | `buttonUnityRatchet.test.ts:52` excludes only `components/ui/`; `tableUnityRatchet.test.ts:29` `PRIMITIVE = components/ui/DataTable/` | `:52` filter → list `[components/ui/, components/shadcn/, components/ai-elements/]`; `PRIMITIVE` → list `[ui/DataTable/, coremetry/DataTable/, shadcn/]` (`:172`). Ceilings unchanged |
| Shrinking | — | every page slice lowers its files' suppression counts to 0 and the three `MAX_RAW_*`; migration-log row carries before→after. Final cleanup: suppressions file back to `{}`, `MAX_RAW_* = 0` |

## 4. Release sequence

Ten releases, each its own `v0.10.X` on `main` (no PRs), each passing the standard gates (`npx tsc --noEmit && npx eslint src && TZ=UTC npx vitest run`; `go build/vet/test`; `make audit`) plus the gates this plan adds as they land. No page migrates; the shell stays unchanged; the initial-load number is reported in every row of `migration-log.md`. Order matters: measurement (R2) precedes dependencies (R4/R5); lint scopes (R5) precede ratchet baselines (R6); wrappers (R7) precede composites (R8/R9); the catalog + axe gate (R10) closes the phase.

| # | Release (commit title) | Goal | Files created / touched | Acceptance criteria → proving test | Bundle expectation | Risk |
|---|---|---|---|---|---|---|
| R1 | `shadcn Faz 1 — karar kaydı, CLAUDE.md, migration-log` | Rules first: Option B superseded by the global shadcn migration; CoSRE v2 builds on it | `docs/DECISIONS.md` (new dated entry: "Option B superseded — global shadcn migration; CoSRE v2 builds on it. Rationale: one style system for the whole app, no chat-only exception."); `CLAUDE.md` (156 lines today, stays <200): replace `:35-40` "no Tailwind (CSS vars in globals.css) … CoSRE v2 Seçenek B'ye uyar … shadcn sorusu ertelendi" with "Tailwind v4 + shadcn (`components/shadcn`, generated) + `components/coremetry` composites; `components/ui` FROZEN (ratchet only down); tokens in `styles/globals.css` + `styles/tokens.css`"; `:75` atoms rule → "`coremetry/*` + `shadcn/*` first; `ui/` atoms only on unmigrated pages; raw `<button>/<input>/<select>/<textarea>` outside `shadcn/`/`ai-elements/` blocked"; release flow line gains `npm run bundle:check`; `.claude/skills/frontend-conventions/SKILL.md:106` ("No PatternFly/shadcn/Tailwind packages") rewritten; `frontend-design-system/SKILL.md` §9 item 3 ("ui/ dışına atom yazmak") → `coremetry/`; `docs/frontend/migration-log.md` created (§9 format) with row 0 = baseline (§1.3 number, §3 baselines) | `wc -l CLAUDE.md` < 200; no code change → gates unchanged | n/a | none (docs) — but it is a rule change, so it is tagged, not a bare `docs —` commit |
| R2 | `Bundle kapısı: radix chunk grubu + initial-load ölçümü (347 KB)` | Measure before anything lands | `frontend/vite.config.ts` (+`radix` group §1.1, +`toast` group for sonner §1.4/§4 R10); `frontend/scripts/bundle-budget.mjs`; `frontend/package.json` `bundle:check`; `.github/workflows/ci.yml` step after `:46-48`; `frontend/eslint.config.js` `no-restricted-imports` shell block (§1.2 — a no-op today); `src/styles/bundleBudget.pin.test.ts` | CI step green with `forbidden in entry: none`, `radix chunk: absent`; `bundleBudget.pin` asserts config/CI/script wiring; migration-log row 1 carries the first canonical number | 0 (groups match no module yet; the script asserts `radix-*.js`/`toast-*.js` absent) | low — a misconfigured group would show as a changed chunk list in the script output |
| R3 | `Tema varsayılanı tek kaynak: lib/theme.ts (chartFmt, dataFrame)` | Decision 7 | `src/lib/theme.ts` (+`DEFAULT_THEME`, `readTheme`, `isLightTheme`); `src/lib/chartFmt.ts:193-195`; `src/lib/chart/dataFrame.ts:48-55`; `src/lib/theme.singleSource.pin.test.ts`; any test asserting the old `dark` no-document fallback (grep `chartTheme(` in `*.test.*` at implementation) | §2.4 pin (1)-(4) green; `seriesPalette.test.ts`, `themeColor.test.ts`, `dataFrame.test.ts`, CorePanel contract tests green | 0 (±50 B est.) | low; behaviour change only without `document` (tests) |
| R4 | `Tailwind v4 + tokens.css + katmanlı CSS girişi + data-slot kapsamı` | Tokens + utilities infrastructure, no consumer yet | devDeps `tailwindcss@4.3.3`, `@tailwindcss/vite@4.3.3`; `vite.config.ts` `plugins: [react(), tailwindcss()]`; `src/styles/index.css`, `src/styles/tokens.css` (§2.1-2.3); `src/main.tsx:8,14` → `import './styles/index.css'`; `globals.css:624-650` `input, select {}` / `textarea {}` / `button …{}` rules → `…:where(:not([data-slot]))` (decision c); `src/styles/tokens.pin.test.ts`; `undefinedCssRefs.test.ts` + `colorLeaks.test.ts` CSS source lists; pins that read `main.tsx` by path (`systemShell.test.ts`, `singleMonoStack.pin.test.ts`, `pageShellAdoption.test.ts`, `singleContentId.test.ts`, `topologyArrowFlow.test.ts`, `mdOverflowWrap.test.tsx` — re-point only if they assert the import line) | `tokens.pin` green; `data-slot` count in `src/` is 0 so the scoping is provably a no-op (pin asserts the three rules carry `:where(:not([data-slot]))`); all 32 `styles/*.test.ts` green; **manual Playwright pass** 3 themes × {Service overview (CorePanel), Settings tab, Traces} — the cascade-layer flip is the one change that can move pixels (§2.1) | CSS +1-2 KB gz *(est.)* (theme vars; Tailwind emits only used utilities = none in prod); JS 0 | medium — `@grafana/ui` emotion styles now outrank `globals.css` `.u-*` rules (17); mitigation = the Playwright pass, rollback = one-line `main.tsx` revert |
| R5 | `shadcn kurulumu: components/shadcn (18 bileşen), cn(), lint muafiyeti` | Generated components exist, nobody imports them | deps `radix-ui@1.8.1`, `class-variance-authority@0.7.1`, `clsx@2.1.1`, `tailwind-merge@3.7.0`, `cmdk@1.1.1`, `sonner@2.0.8` (exact pins; `depPins.test.ts` extended); `frontend/components.json` (§5); `src/lib/cn.ts`; `npx shadcn@4.21.5 add …` (§5 set) into `src/components/shadcn/`; allowed edits in `button.tsx`, `table.tsx` (§5, `// coremetry:` markers); `eslint.config.js:98` ignores += `src/components/shadcn/**`, `src/components/ai-elements/**`; `buttonUnityRatchet.test.ts:52`, `tableUnityRatchet.test.ts:29,172` exclusion lists (§3.3) | `tsc` clean over generated files (React 19.3.1 — no `forwardRef` ports; registry verified: `button`, `table`, `native-select`, `button-group`, `command` contain no `forwardRef`); `npx shadcn diff` reports only the marked edits; bundle script: `shadcn sources in output: 0 chunks`, `radix-*.js absent` | 0 — no prod importer (`/design` is dev-only) | medium — `radix-ui` monopackage tree-shaking is only proven when the first consumer lands (slice 1); fallback = per-package `@radix-ui/react-*` specifiers (CLI supports both) |
| R6 | `Mandallar: uiImportRatchet, inlineStyleRatchet, ui/no-raw-element` | Freeze `components/ui`, baseline the three counters | `src/styles/uiImportRatchet.test.ts` (§3.1 ceilings, frozen `ui/` entry list); `src/styles/pageFiles.ts`; `src/styles/inlineStyleRatchet.test.ts` (§3.2); `eslint.config.js` rule `ui/no-raw-element` (§3.3) + `npx eslint src --suppress-rule ui/no-raw-element` → `eslint-suppressions.json`; `src/styles/rawElementRatchet.test.ts`; `eslintDisableReason.test.ts` rule name; `buttonUnityRatchet.test.ts` keeps its button/role counters | All ceilings equal the measured numbers at this commit (expected: 969 pairs / 342 files; inline TOTAL 5,244; raw ≈352 input gated *(est.)* / 159 select / 33 textarea); CI ESLint step green with the suppressions file | 0 | low; `lib/cssVars.ts` (the §3.2 escape) is NOT added here — first use is slice 1 (no helper without a use) |
| R7 | `Radix katman sözleşmesi: coremetry/overlay sarmalayıcıları, Esc yığını, yoğunluk kancası` | Decisions d + a; raw Radix overlays banned outside wrappers | `src/components/coremetry/overlay/{Dialog,Sheet,Popover,DropdownMenu,ContextMenu,Tooltip}.tsx` (controlled; `useEscLayer`; Esc pattern §7.2; `Tooltip` carries its own `TooltipProvider` — a shell-level provider would import Radix into the entry); `src/lib/useDensity.ts` (`data-density` observer, §7.3); `eslint.config.js` `no-restricted-imports`: `@/components/shadcn/{dialog,sheet,popover,dropdown-menu,context-menu,tooltip,command,select}` only from `src/components/coremetry/**` (shadcn/ai-elements exempt); `src/components/coremetry/overlay/escLayer.contract.test.tsx` (jsdom) | Esc harness (§7.2) green: Dialog + hand-rolled Drawer above → Esc closes the Drawer only; inner `<input>` that `preventDefault`s Esc keeps the Dialog open; second Esc closes it (`escLayer.ts:4-10` bug pair as tests); `useDensity` test: attribute flip → value | 0 (no prod importer) | medium — Radix listens on `document` in **capture** phase (`useEscapeKeydown`), i.e. before React handlers and before `keyboard.ts:238`; the §7.2 pattern is designed around that and the harness pins it |
| R8 | `PageShell/PageControls → coremetry (değişmeden) + StatusBadge + durum token'ı` | Decision k; one status source | `git mv` `components/ui/{PageShell,PageControls}.tsx` → `components/coremetry/`; 51 + 17 importers' paths (mechanical); `components/ui/index.ts:…` two exports removed; tests re-pointed: `platformCss.pin.test.ts`, `singleContentId.test.ts`, `pageShellAdoption.test.ts`, `ui/pageControls.test.ts` (moves too); `src/components/coremetry/StatusBadge.tsx` on `shadcn/badge` + `src/lib/statusTone.ts` (§6); `lib/chart/seriesRole.ts:24-26` → `var(--status-critical-fg)` / `var(--status-ok-fg)`; `tokens.pin` equality assertion | `uiImportRatchet` 969 → 901 (PageShell 51 + PageControls 17 leave `ui/`), ceilings lowered in the same commit; CorePanel contract tests unchanged (identical resolved colours); `/design` not yet extended | shell bytes unchanged (path strings only, ±0.1 KB *(est.)*); StatusBadge has no prod importer → 0 | low — the 68-file path sweep is mechanical; Q6 confirms sweep vs shim |
| R9 | `coremetry bileşik API'leri donduruldu: DataTable, TimeRangePicker, FilterBar, CommandPalette` | Phase 2 codes against these types | `src/components/coremetry/{DataTable,TimeRangePicker,FilterBar,CommandPalette}/` (§6: frozen prop types + thin wrappers); `src/components/coremetry/index.ts` barrel; `*.api.test.ts` per composite (type-level + the wrapper delegates) | Types compile against today's callers in a scratch file (`tsc` only); wrapper tests green; `build:ds` untouched (OQ f) | 0 (no prod importer; the shell keeps importing `components/TimeRangePicker` and `components/CommandPalette` directly until the shell slice) | low |
| R10 | `/design: shadcn kataloğu × 3 tema × 3 yoğunluk + axe kapısı + sonner Toaster kapısı` | Phase 1 exit gate | `src/pages/Design.tsx` (271 lines today) → sections per installed component (§8); `src/pages/design/designAxe.test.tsx` (jsdom + `axe-core@4.14.0` devDep); `src/components/Toaster.tsx` → `ToasterGate` (§1.4 A) + `src/components/coremetry/Toaster.tsx` (sonner); `globals.css:3599-3646` `.toaster/.toast*` deleted; `Design.pin.test.ts` extended (catalog covers every file in `components/shadcn/`) | axe: 0 violations over 18 components × 9 cells (`color-contrast` rule off in jsdom — covered by `contrastTokens.test.ts`); `/design` renders in `vite dev`; `bundle:check`: entry unchanged, `toast-*.js` present and NOT in entry; first `toast.*` call mounts sonner (jsdom test with queued replay) | entry −~1 KB (old Toaster) *(est.)*; lazy `toast-*.js` ≈9-10 KB gz *(est.)* | medium — first toast of a session waits one chunk fetch (Q1) |

Phase 1 exit (audit §9.2): `/design` green under axe for all 9 cells; initial load within ±1 KB gz of the R2 baseline (Radix/cmdk/sonner never in the entry — script-proven every release); `uiImportRatchet` baseline committed; `migration-log.md` has rows R1-R10.

## 5. Directories, `components.json`, install set, dependencies

### 5.1 Directories (audit §8 macOS note: none of these exist today; `components.json` absent; no case collision)

| Dir | Content | Rules |
|---|---|---|
| `src/components/shadcn/` | generated by the CLI; exempt from the raw-element ban and both unity ratchets (§3.3) | only `button.tsx` and `table.tsx` may be hand-edited (below); every edit sits between `// coremetry: …` / `// /coremetry` markers so `npx shadcn diff` output is readable; any other file is regenerated verbatim on upgrade |
| `src/components/coremetry/` | composites with frozen APIs (§6), overlay wrappers (§7.2), `StatusBadge`, `Toaster`, moved `PageShell`/`PageControls`, `index.ts` barrel | NOT exempt from any ratchet; the only place allowed to import shadcn overlay files (§7.2 lint) |
| `src/components/ai-elements/` | created by the CoSRE v2 track, not by Phase 1 | Phase 1 only adds the path to the lint/ratchet exemption lists (decision 8) |
| `src/components/ui/` | frozen (§3.1): no new file, no new importer; shrinks as Phase 2 deletes atoms | `build:ds` keeps packaging it (OQ f) |

### 5.2 `frontend/components.json`

```json
{
  "$schema": "https://ui.shadcn.com/schema.json",
  "style": "new-york",
  "rsc": false,
  "tsx": true,
  "tailwind": { "config": "", "css": "src/styles/tokens.css", "baseColor": "neutral", "cssVariables": true, "prefix": "" },
  "iconLibrary": "lucide",
  "aliases": { "components": "@/components", "ui": "@/components/shadcn", "utils": "@/lib/cn", "lib": "@/lib", "hooks": "@/hooks" }
}
```

`aliases.ui` → `@/components/shadcn` (never `components/ui`: `Button.tsx`/`Badge.tsx`/`Tooltip.tsx`/`Popover.tsx` would collide on APFS `core.ignorecase=true`). `tailwind.css` points at `tokens.css` so `shadcn init` writes nothing into `globals.css` (`init` is run once with `--yes` and its CSS output diffed away — the token mapping of §2 is the source). `utils` → `src/lib/cn.ts`: `export const cn = (...i: ClassValue[]) => twMerge(clsx(i))`. `npx shadcn diff <name>` stays usable because paths resolve through these aliases; `shadcn` itself is not a dependency (invoked as `npx shadcn@4.21.5`, 2026-10-14).

### 5.3 Install set (exactly the brief's list; everything else is Q2)

| Component | Registry deps (verified via `ui.shadcn.com/r/styles/new-york-v4/<name>.json`) | Phase 1 consumer | Note |
|---|---|---|---|
| `button` | `radix-ui` (Slot), `cn` | `/design`; `coremetry/*` | allowed edits below |
| `button-group` | registry: `button`, `separator` → **`separator` is installed as a transitive registry dep** | `/design` | |
| `tooltip` | `radix-ui` | `coremetry/overlay/Tooltip` | own `TooltipProvider` inside the wrapper |
| `dropdown-menu`, `context-menu` | `radix-ui` | `coremetry/overlay/*` | |
| `dialog`, `sheet`, `popover` | `radix-ui` | `coremetry/overlay/*` | `sheet` = Drawer target (right side) |
| `input`, `textarea` | `cn` | `/design` | `input.tsx` is `bg-transparent`; the Field composite (Phase 2) adds `bg-field` (§2.2) |
| `select` | `radix-ui` | `/design` | decision b: only where options carry icons/descriptions; never mixed with native in one form |
| `native-select` | `cn` (registry entry exists: verified 200) | `/design` | decision b default for data forms |
| `badge` | `radix-ui` (Slot), `cn` | `coremetry/StatusBadge` | |
| `skeleton` | `cn` | `/design` | `components/Skeleton.tsx` family (7 importers) migrates in Phase 2 |
| `table` | `cn` | `coremetry/DataTable` (Phase 2 real use) | allowed edits below |
| `command` | `cmdk`, registry: `dialog` | `coremetry/CommandPalette` (shell slice) | |
| `scroll-area`, `tabs` | `radix-ui` | `/design` | `tabs`: TabStrip is panel-less (audit §2) — only `TabsList` is used, decided in the first slice that needs it |
| toast | **package `sonner` only** — the registry `sonner.tsx` imports `next-themes` and is NOT installed | `coremetry/Toaster` (§1.4) | theme from `lib/theme.ts` |

Total: 18 registry files + `separator` = 19 files in `components/shadcn/`.

### 5.4 Allowed edits (only these two files)

| File | Edit | Why | Proof |
|---|---|---|---|
| `button.tsx` | (1) `loading?: boolean` → `aria-busy="true"` + `disabled` + spinner, label stays in the DOM at `opacity:0` (width-stable, the v0.10.919 contract, `globals.css:641-650`); (2) `size: 'icon' \| 'icon-sm' \| 'icon-xs'` → props union **requires `'aria-label'` at type level** (`IconButton` contract, audit §2); (3) cva additions: size `xs`, variants `accent`, `ghost-danger` mapped to `--primary`/`--status-critical-*` tokens; (4) `type="button"` default | the five things the audit lists as "has, shadcn lacks" for Button/IconButton (209 + 50 importers) | `components/shadcn/button.contract.test.tsx` (jsdom; port of `ui/Button.contract.test.tsx` cases: type default, aria-busy, className merge via `cn`, icon-size needs label = `tsc` negative test) |
| `table.tsx` | density-aware: `Table` reads `useDensity()` (§7.3) and sets `data-density` on the wrapper; row/cell paddings from `--row-h`/`--hit-min` tokens (`globals.css:190,196`; compact/dense :3514,:3541) instead of `p-2` | T6 one row rhythm; `rowHeight.ts` `ROW_H` must equal `--row-h` (`rowHeight.test`) | `components/shadcn/table.contract.test.tsx` |

React 19.3.1 is already in (`package.json:38`, v0.10.1600): the registry files have no `forwardRef` (verified on `button`, `table`, `native-select`, `button-group`, `command`, `sonner`), so no ports are needed and no throw-away work exists (audit §9.0 reasoning holds).

### 5.5 Dependencies (versions published ≥ 2 weeks before 2026-10-30; `npm view <pkg> time`)

| Package | Version | Published | Peer check | Where |
|---|---|---|---|---|
| `tailwindcss` | 4.3.3 | 2026-07-16 | — | devDependencies (build-time) |
| `@tailwindcss/vite` | 4.3.3 | 2026-07-16 | `vite ^5.2 \|\| ^6 \|\| ^7 \|\| ^8` ✓ | devDependencies |
| `radix-ui` | 1.8.1 | 2026-10-12 (18 days) | `react ^16.8 … ^19` ✓ | dependencies, exact pin |
| `class-variance-authority` | 0.7.1 | 2024-11-26 | — | dependencies |
| `clsx` | 2.1.1 | 2024-04-23 | — | dependencies (today transitive via `@grafana/ui`, same version) |
| `tailwind-merge` | 3.7.0 | 2026-09-12 | Tailwind v4 class model (3.x) | dependencies |
| `cmdk` | 1.1.1 | 2025-03-14 | `react ^18 \|\| ^19` ✓ | dependencies, exact pin |
| `sonner` | 2.0.8 | 2026-08-09 | `react ^18 \|\| ^19` ✓ | dependencies, exact pin |
| `axe-core` | 4.14.0 | 2026-10-05 (25 days) | — | devDependencies (§8: direct, no `jest-axe`/`vitest-axe`) |
| `lucide-react` | keep `^1.59.0` (installed) | — | — | unchanged |
| `shadcn` CLI | 4.21.5 via `npx` | 2026-10-14 | — | not a dependency |

Rejected: `next-themes` (registry sonner dep — not needed, §1.4), `jest-axe` 11.0.0 (pins its own `axe-core` 4.12.1 → two axe copies), `vitest-axe` 0.1.0 (2022, unmaintained), `@testing-library/react` (contract tests already render with `react-dom/client` + `act`, `ui/Button.contract.test.tsx:26-29`). Any package outside this table = operator question. `depPins.test.ts` gains the three exact pins (`radix-ui`, `cmdk`, `sonner`): DOM-shaping UI libs whose minor bumps change markup/behaviour.

## 6. `components/coremetry` composites — APIs frozen in Phase 1

"Frozen" = the exported prop types are the contract Phase 2 slices code against; the implementation behind them may be a thin wrapper over today's code (justified per row — each has ≥2 concrete consumers queued in audit §4, so the "no abstraction without a second use" rule is met by the queue, not by Phase 1 usage). All live under `src/components/coremetry/<Name>/index.ts` + `<Name>.tsx`, re-exported by `coremetry/index.ts`.

| Composite | Frozen API (sketch) | Phase 1 implementation | Why a wrapper is acceptable |
|---|---|---|---|
| **DataTable** (R9) | `DataTableProps<T> = { storageKey: string; columns: ColumnDef<T>[]; rows: T[]; sort?: { initial?: SortState; ids?: string[]; urlKey?: string; server?: ServerBinding; onChange?(s: SortState): void }; columnModel?: ColumnModelBinding; selection?: SelectionBinding<T>; getRowHref?(row: T): string \| undefined; rowLinkReplace?: boolean; onOpen?(row: T): void; state: DataTableStateProps; virtual?: { rowHeight?: number; overscan?: number }; density?: Density; renderCell?(row: T, col: ColumnDef<T>): ReactNode; rowActions?: { items(row: T): MenuItemSpec[]; label: string } }` — `ColumnDef`, `SortState`, `ServerBinding`, `SelectionBinding`, `ColumnModelBinding`, `DataTableStateProps` are **today's types re-exported** from `ui/DataTable/types.ts` / `lib/dataTable.ts` (`ui/DataTable/index.ts:19-33`), so no caller rewrites its column arrays. Backend-agnostic: nothing in the props names `useDataTable` or TanStack | `<DataTable>` = `useDataTable({…})` (`ui/DataTable/DataTable.tsx:267` option list mapped 1:1) + `<table {...dt.tableProps}><DataTableColgroup/><DataTableHead/>…<DataTableState/></table>`, `VirtualTable` when `virtual` is set; `rowActions` renders ONE shared `coremetry/overlay/DropdownMenu` anchor per table (audit §6 per-row rule), not a Radix root per row | TanStack-vs-`useDataTable` is decided in the Traces slice (§11 g): if TanStack wins, only the body behind these props changes; if not, the wrapper IS the final form. 126 importers queued |
| **TimeRangePicker** (R9) | `{ value: TimeRange; onChange(r: TimeRange): void; variant?: 'global' \| 'window'; presets?: readonly string[] /* subset of QUICK_PRESETS, lib/rangePicker.ts:19 */; allowCustom?: boolean; label?: string }` (`TimeRange` = `lib/types.ts:5582`) | `variant='global'` → renders today's `components/TimeRangePicker` (`:36-38` already `{ value, onChange }`); `variant='window'` → throws in dev / renders a `native-select` of `presets` (the local-window selectors' common shape: TREND_WINDOWS, COORD_WINDOWS, SINCE_OPTS, WINDOWS…, audit §4.1) | 1 global + ~10 local selectors + 2 compare presets queued; the preset table becomes the single source (`PRESET_SECONDS` already is for the global one) |
| **FilterBar** (R9) | `FilterChip = { key: string; op: '=' \| '!=' \| '~' \| '!~' \| 'exists' \| '!exists'; value?: string }`; `{ chips: FilterChip[]; onChange(chips: FilterChip[]): void; suggestKeys?(q: string): Promise<string[]>; suggestValues?(key: string, q: string): Promise<string[]> /* server, debounced, never a catalogue */; query?: { value: string; onChange(v: string): void; placeholder?: string }; saved?: ReactNode /* SavedViewsBar slot */; sticky?: boolean; actions?: ReactNode; children?: ReactNode }` | composes `coremetry/PageControls` (decision k: `FilterBar` composes `PageControls`); chip row + editor popover land in the Logs slice (first use); Phase 1 ships the types, the `PageControls` composition and the chip model's pure codec (`lib/filterChips.ts`: parse/format `key op value`, with a vitest file) | ~12 chip editors + 2 in/out action groups queued (audit §4.2); the codec is pure and testable now |
| **StatusBadge** (R8) | `Status = 'ok' \| 'warn' \| 'critical' \| 'unknown' \| 'info'`; `{ status: Status; children?: ReactNode /* label; default = i18n of status */; size?: 'sm' \| 'md'; dot?: boolean; title?: string }`; `lib/statusTone.ts`: `statusToken(s: Status): { fg: string; bg: string; border: string }` (the `--status-*` names), `fromLegacy(s: lib/statusColor Status): Status` (`error→critical`, `no-data→unknown`) | `shadcn/badge` with cva variants per status using `bg-status-*-bg text-status-*-fg`; density via `useDensity` (badge shrinks at compact/dense exactly like `.badge` `:3539,:3557`) | 9 duplicate badges + ~329 ternaries queued (audit §4.3); `lib/statusColor.ts` (2 importers) is re-exported from `statusTone.ts` and deleted when those two move |
| **CommandPalette** (R9) | `{ open: boolean; onOpenChange(o: boolean): void; groups: { id: string; label: string; items: { id: string; label: string; hint?: string; icon?: LucideIcon; href?: string; run?(): void }[] }[]; query: { value: string; onChange(v: string): void; placeholder?: string }; loading?: boolean; empty?: ReactNode }` + `openCommandPalette()` re-exported from `components/CommandPalette.tsx:147` | facade only: re-exports today's `CommandPalette` (847 lines, mounted `AppShell.tsx:7`) and the opener; the `cmdk` implementation lands in the shell slice (last) | 1 implementation + the `TopbarSearch` opener; the frozen type lets the shell slice swap without touching AppShell/Topbar call shapes |
| **PageShell / PageControls** (R8) | unchanged: `PageShell { children; variant?: 'default' \| 'full' }` (`ui/PageShell.tsx:40`); `PageControlsProps { children; sticky?; className?; style?; actions? } & div attrs` (`ui/PageControls.tsx:30-62`) | `git mv`, zero code change; `--controls-h` publication, ≤640px "Filters (N)" collapse, Esc layer all as today | decision k: layout, not atoms |
| **Toaster** (R10) | none exported; `lib/toast.ts` API unchanged (`toast.success/error/info/warning(message, durationMs)`) | `coremetry/Toaster.tsx` = sonner `<Toaster theme position="top-right" />` fed by `subscribeToasts`; mounted lazily by `components/Toaster.tsx` gate (§1.4) | decision h |

**Known limitation — `coremetry/DataTable` (frozen API, R9; recorded R10 v0.10.1619):** `virtual` + `rowActions` together draw **no actions column** — `VirtualTable` has no trailing column, so the ⋯ cell and its `DropdownMenu` are skipped in `virtual` mode (types.ts JSDoc on `rowActions`, `DataTable.tsx` comment). Not a bug to patch in place: the Traces slice decides the virtual body (TanStack or today's `VirtualTable`, audit §11 g) and lifts this limitation there.

Not composites (stay where they are): `Spinner`/`Empty` (`components/Spinner.tsx`, 155 importers — keep API, restyle in place when `shadcn/spinner`/`empty` are added, Q2), `Combobox` + pickers (server search stays; `command`-based list in the first picker slice), `Pager`, `CopyButton`, `QueryError`.

## 7. Global element-rule scoping, Esc layer, density, `cn()`, icons

### 7.1 Global element rules → `:where(:not([data-slot]))` (decision c, R4)

| Rule today (`globals.css`) | Becomes | Specificity | Note |
|---|---|---|---|
| `input, select { … }` :624-627; `input:focus, select:focus` :628; `select option` :629 | `input:where(:not([data-slot])), select:where(:not([data-slot])) { … }` etc. | unchanged (0,0,1) — `:where()` contributes 0 | shadcn `input.tsx`/`native-select.tsx` carry `data-slot`; `select option` stays global (options are not slots) |
| `textarea { … }` :632 | `textarea:where(:not([data-slot]))` | | |
| `button { … }` :633-637, `button:hover:not(:disabled)` :638, `button:disabled` :639, `button[aria-busy="true"]:disabled` :647, `button.is-loading…` :653-655 | each selector gets `:where(:not([data-slot]))` on the `button` compound | unchanged | the `.is-loading` trio is `ui/Button`'s width-stable loading; `shadcn/button.tsx` reimplements it with its own classes (§5.4) |
| `:where(button, a, .btn-icon, [role="button"], summary, label[for]) { touch-action… }` (`platformCss.pin.test.ts` item 1) and `:focus-visible` ring (`:656+`) | **unchanged** — they are correct for shadcn elements too | | pin stays green |
| `[data-density="compact|dense"] .controls > button`, `.controls input/select` :3520-3524, :3547-3551 | unchanged in Phase 1 | | shadcn controls inside `PageControls` get density from `useDensity` (§7.3), the legacy rules keep styling `ui/` controls; the first slice that puts a shadcn Button inside `.controls` adds `:where(:not([data-slot]))` to these four selectors too (noted in `pageFiles.ts` as a slice-1 chore) |

`data-slot` usage in `src/` today: **0**, so R4's scoping changes no pixel; `tokens.pin.test.ts` asserts the three rule groups carry the guard and that `data-slot` appears only under `components/shadcn/**`.

### 7.2 Esc-layer integration (decision d, R7)

Facts: `lib/escLayer.ts` is a LIFO stack (`pushEscLayer` :31, `useEscLayer` :59; 33 non-test consumers); the ONLY document listener is `lib/keyboard.ts` (`:223-247`): it honours an element's claim (`if (e.defaultPrevented) return;` :238 — React handlers run before it) and otherwise calls `topEscLayer()`. Radix `DismissableLayer` registers its own `keydown` listener on `document` **in capture phase** (`useEscapeKeydown`), i.e. it fires BEFORE React handlers and before `keyboard.ts`. Two naive integrations fail: (a) `onEscapeKeyDown={e => e.preventDefault()}` stops Radix but also trips `:238`, so nothing closes; (b) letting Radix dismiss itself reproduces bug #1 of `escLayer.ts:4-10` (two listeners, both close).

**Pattern (every `coremetry/overlay/*` wrapper):**

```tsx
const escPending = useRef(false);
useEscLayer(open, () => onOpenChange(false));                 // the stack owns Esc
<Primitive.Content
  onEscapeKeyDown={() => { escPending.current = true; }}      // do NOT preventDefault: React handlers + keyboard.ts still see the event
  … />
// controlled root:
<Primitive.Root open={open} onOpenChange={next => {
  if (!next && escPending.current) { escPending.current = false; return; }   // Radix's own Esc dismiss is ignored
  onOpenChange(next);                                                        // pointer-outside / close button / programmatic: pass through
}}>
```

Flow for Esc: Radix capture handler → `escPending` set → Radix calls `onDismiss` → wrapper ignores it → React handlers run (an inner input that consumes Esc calls `preventDefault`) → `keyboard.ts:238` honours that claim, else `topEscLayer()` closes the TOP layer — which is the wrapper only if nothing newer (a hand-rolled Drawer, a Combobox list) is open. Nested Radix layers: Radix only fires Esc on its highest `DismissableLayer`; the stack's top is the same element by construction (LIFO). `onPointerDownOutside`, `onInteractOutside` are untouched. No change to `keyboard.ts`, `escLayer.ts`, or any input.

Harness `coremetry/overlay/escLayer.contract.test.tsx` (jsdom): (1) Dialog open + `useEscLayer` Drawer opened after it → Esc closes the Drawer, Dialog stays; (2) Dialog with an `<input onKeyDown={e => e.key==='Escape' && e.preventDefault()}>` focused → Esc leaves the Dialog open; (3) Dialog alone → Esc closes it exactly once (`onOpenChange(false)` called once); (4) `escLayerDepth()` returns to 0 after close.

Lint (R7): `no-restricted-imports` — patterns `@/components/shadcn/{dialog,sheet,popover,dropdown-menu,context-menu,tooltip,command,select}` and `radix-ui`/`@radix-ui/*` are errors in `src/**` except `src/components/coremetry/**`, `src/components/shadcn/**`, `src/components/ai-elements/**`. Message: "Radix overlays only via `@/components/coremetry/overlay` (Esc stack, lib/escLayer.ts)".

### 7.3 Density (decision a, R7)

| Item | Decision |
|---|---|
| Values | existing three — `comfortable · compact · dense` (`lib/density.ts:10`, `DENSITY_STEPS`), attribute `data-density` on `<html>` set by `index.html:66-77` boot + `components/DensityToggle.tsx`; **no rename**, no provider |
| Hook | `src/lib/useDensity.ts`: `useDensity(): Density` — module-level `MutationObserver` on `documentElement` (`attributeFilter: ['data-density']`) + `useSyncExternalStore`, the exact shape of `lib/useThemeTick.ts:13-33`; `normalizeDensity` (`density.ts:16`) for the read |
| Mapping to shadcn sizes | `densitySize: Record<Density, 'md' \| 'sm' \| 'xs'>` = comfortable→`md`, compact→`sm`, dense→`xs` — consumed by `coremetry/*` (DataTable rows, FilterBar controls, StatusBadge) and `shadcn/table.tsx` (allowed edit). Page-level `.controls` density rules (`globals.css:3514-3557`) keep working for unmigrated pages |
| Tokens | `--row-h` 36/24/20, `--hit-min` 24/24/20 (`:190,:196,:3514,:3541`) stay the only row-rhythm source; `ROW_H` (`ui/DataTable/rowHeight.ts:8`) equality pin unchanged |

### 7.4 `cn()` rule

`src/lib/cn.ts` = `twMerge(clsx(...))` (the registry's `utils` alias). Allowed in `components/shadcn/**`, `components/coremetry/**`, `components/ai-elements/**` and pages already migrated; never used to assemble legacy class strings (`cn('badge', 'b-' + tone)` is the "templated className" the design-system skill §9 already rejects — no type gate, silent break). `clsx` is not imported directly anywhere else (one spelling).

### 7.5 Icons (lucide only)

| Rule | Value |
|---|---|
| Library | `lucide-react` only (direct dep already; 30 files, ~72 icons). `components/icons.tsx` (199 lines), `RuntimeIcons.tsx`, `TelescopeIcon.tsx`, unicode glyphs (`theme.ts:9`) retire page by page in Phase 2 — not in Phase 1 |
| Size | default **16px** (`size={16}`); `sm` controls **14px**; `shadcn/button.tsx` sets `[&_svg:not([class*='size-'])]:size-4` (16) — the `xs`/`sm` sizes in the allowed edit set `size-3.5` (14) |
| Placement | icon LEFT of the label (`<Button><Save/> Save</Button>`); a RIGHT icon only for a chevron on disclosure/menu triggers (`ChevronDown`/`ChevronRight`) |
| Icon-only | `size="icon*"` requires `aria-label` at type level (§5.4) and gets a `coremetry/overlay/Tooltip` with the same text (today's `IconButton tooltip=` contract, `MAX_ICONBUTTON_TITLE 7` ratchet) |
| Colour | `currentColor` only; status icons take colour from the `--status-*` token of their surface, never a hex |

## 8. `/design` route extension + vitest/axe gate

### 8.1 Route (R10)

Today: `pages/Design.tsx` (271 lines) is a dev-only button catalog — `App.tsx:96` `const Design = import.meta.env.DEV ? lazy(() => import('./pages/Design')) : null;`, route `:243`; `Design.pin.test.ts` pins the DEV ternary and that no prod module references it. Both stay (the catalog must never enter the prod build — it would import every shadcn component into a page chunk and, worse, `radix-*.js` would exist with no page using it).

| Dimension | Values | How rendered |
|---|---|---|
| Components | the 19 files in `components/shadcn/` + `coremetry/StatusBadge`, `coremetry/overlay/*`, `coremetry/Toaster` | one `<Section>` per file, driven by a `CATALOG: Record<string, () => ReactNode>` map; `Design.pin.test.ts` asserts every `components/shadcn/*.tsx` has a key (a new install without a catalog entry is red) |
| Variants × sizes × states | per component from its cva export (`buttonVariants.variants`) — iterated, not hand-listed; states: default, hover (interactive), focus-visible (Tab), disabled, loading/aria-busy, active/aria-pressed, open (overlays rendered open, non-portaled where Radix allows `container`) | the existing `Row`/`Section` helpers (`Design.tsx:28-45`) |
| Themes × densities | 3 × 3 = 9 cells as `<div data-theme={t} data-density={d}>` containers — works because `tokens.css` declares the shadcn vars on `:root, [data-theme]` (§2.2) and `[data-density]` rules are descendant selectors (`globals.css:3514-3557`); the `dark` Tailwind variant is container-aware (§2.3) | a `?cell=dark/compact` URL param (replace:true) narrows to one cell for eyeballing; default = all 9 stacked |

### 8.2 axe gate (R10) — `axe-core` 4.14.0 directly

| Choice | Reason |
|---|---|
| `axe-core` (devDependency) + a 15-line matcher in `src/test/axe.ts` (`expectNoAxeViolations(node, opts)` → `axe.run(node, { rules: { 'color-contrast': { enabled: false } } })`, formats violations as `id: help — selector`) | one dependency, no wrapper pinning its own axe version (`jest-axe` 11.0.0 ships `axe-core 4.12.1` → two copies; `vitest-axe` 0.1.0 is from 2022). The matcher is trivial |
| `color-contrast` disabled in jsdom | axe cannot compute contrast without layout/paint; the token-level gate `styles/contrastTokens.test.ts` already pins every text/surface pair in all three themes (296 pairs per `globals.css:8-9`) |
| Environment | `// @vitest-environment jsdom` per file (repo rule, `vitest.config.ts:14-18`); rendering via `react-dom/client` `createRoot` + `act` (the `*.contract.test.tsx` harness) — no testing-library |
| File | `src/pages/design/designAxe.test.tsx`: for each `CATALOG` key × 9 cells → render the section inside a `<div data-theme data-density>` → `expectNoAxeViolations`. 19 + 8 sections × 9 cells ≈ 243 runs *(est. 40-90 s in CI; Q7 — `DESIGN_AXE=themes` env runs the 3 theme cells only, for local loops)* |
| What it catches that nothing else does | missing names on icon buttons, `aria-controls` dangling, duplicate ids (the `modal-title` clash in `ui/Modal`, audit §2), menu/listbox role structure, label↔input association on the generated form components |
| Gate | runs inside the existing `npx vitest run` CI step (`ci.yml:43-45`) — no new workflow step; a release that adds a component or variant without a green axe run does not tag |

Playwright: manual only, on marked visual changes (decision 1); R4 (cascade layer) and R10 (catalog) are the two Phase 1 releases where the operator eyeballs `/design` in `vite dev` and one CorePanel page in 3 themes. `browser_snapshot` is not used (operator rule).

## 9. `migration-log.md` entry format

`docs/frontend/migration-log.md` is created in R1 with this header and one row per release from then on (decision 1; audit §9.1). One table, newest row last; the row is written in the same commit as the code (the number comes from `npm run bundle:check` run right before tagging, or the CI step log of that commit).

```
| Release | Scope (page / slice / infra) | What changed | Atoms deleted (files) | ui-import pairs before→after | raw input/select/textarea before→after | inline style={{ (page) before→after | Initial-load JS gz measured (budget 347) · main gz | §7 estimate for this release | Notes |
|---|---|---|---|---|---|---|---|---|---|
| v0.10.1604 (baseline, R1) | — | Phase 1 start | — | 969 | 490 / 159 / 33 | TOTAL 5,244 | 258.3 · 54.6 (operator figure; R2 writes the canonical script number) | — | React 19 (1600) + CoSRE lazy (1599) landed |
| v0.10.X (R2) | infra | bundle gate | — | 969→969 | unchanged | unchanged | <script line> | 0 | first canonical number |
```

| Column | Source |
|---|---|
| Release | the tag |
| Scope | `pageFiles.ts` key, `infra`, or `shell` |
| What changed | one line, operator-readable (no file lists) |
| Atoms deleted | `components/ui/<File>.tsx` names removed in this release (Phase 2); `—` in Phase 1 except R8 (`PageShell`, `PageControls` moved, not deleted) |
| ui-import pairs | `uiImportRatchet` headline before→after (R6 onwards; before R6 the plan's read-only count) |
| raw elements | the three `MAX_RAW_*` ceilings before→after |
| inline style | the page key's counter before→after; `TOTAL` for infra releases |
| Initial-load gz | the last line of `scripts/bundle-budget.mjs` verbatim (§1.3): `\| initial … KB gz \| main … KB gz \| budget 347 \| radix-in-entry 0 \|` |
| §7 estimate | the audit §7 table's expected delta for what this release adds (e.g. "sonner lazy 9-10 KB, entry 0") so estimate and measurement sit side by side (§11 "Bütçe") |
| Notes | Playwright pass done (y/n), operator questions answered, follow-ups |

Rule from §11: a release whose measured initial load exceeds 347 KB does not ship until split; the row records the failing attempt only if it shipped after the split (so the log is a history of shipped states, not of builds).

## 10. Explicitly NOT in Phase 1

| Not done | Where it lands | Why not now |
|---|---|---|
| Any page migration (Settings/Remote Cluster first per §9.4), any `components/ui` atom deletion | Phase 2 slices | Phase 1 = foundation; the exit criterion is "initial load unchanged" |
| `.badge .b-*` class → `<StatusBadge>` conversion (~523 sites) | per page slice (decision m) | |
| `title=` → Tooltip (~2,134) | only glyph-only controls, per slice (decision l) | |
| TanStack Table decision / `coremetry/DataTable` body change | Traces slice (§11 g) | one table stack; decided where virtualisation is tested |
| `globals.css` rule deletion (except `.toast*` in R10) | every page slice deletes its own rules (§11 acceptance) | |
| Sidebar redhat dark-rail remap (`globals.css:391-417`) → `[data-theme="redhat"] [data-slot=sidebar]` tokens | shell slice (decision p) | |
| `build:ds` retarget / `dist-ds` | final cleanup (decision f) | |
| `checkbox`, `radio-group`, `switch`, `label`/`field`, `kbd`, `card`, `alert-dialog` (useConfirm), `toggle`, `toggle-group`, `collapsible`, `spinner`, `empty`, `input-group` | Q2: added in the slice that first needs each, own release | brief's install set is exact |
| `useConfirm` promise API on `alert-dialog` | Settings slice (first `useConfirm` consumer migrated) | the 35-importer API stays as is |
| Global `TooltipProvider` in the shell | never (wrappers carry their own) | would put Radix in the entry |
| Chart chrome (panel ⋯ menus, fullscreen dialog, legends, CompareToggle) | audit §5 mapping, slices 2-4 | rendering untouched; chrome follows the overlay wrappers |
| `components/icons.tsx` / `RuntimeIcons` / `TelescopeIcon` / unicode glyph retirement | per page (audit §9.2 #10) | |
| Inline-style sweep outside migrated pages | never as a sweep (decision n) — only migrated pages reach zero | |
| `lib/cssVars.ts` escape helper | first page slice that needs a computed style | no helper without a use |
| i18n of generated components' default strings | per composite when first used (Turkish defaults live in the composites, not in `shadcn/`) | |
| CoSRE v2 (`components/ai-elements/`, AI SDK UI stream UI) | its own track after Phase 1 merges (decision 3) | Phase 1 only exempts the path |
| `@grafana/ui` removal / CorePanel rewrite | chart-layer workstream (§11 o ladder) | not a shadcn concern |
| Reducing the Settings chunk (605 KB raw / 167.9 gz) | slice 1 (decision j) | |
| `eslint-suppressions.json` pruning to `{}` | final cleanup | |
| Playwright automation | never unless asked (operator rule) | |

## 11. Operator questions

Only items not already settled by audit §0/§10/§11. Each with the default the plan assumes; a "no" changes the named release only.

| # | Question | Default assumed (and where) |
|---|---|---|
| Q1 | **sonner Toaster in Phase 1 via lazy gate (first toast waits one chunk fetch, queued entries replayed) — or keep the hand-rolled Toaster until the shell slice?** | A: lazy gate in R10 (§1.4). The gate pattern is already in production (`CopilotChatGate`, v0.10.1599) |
| Q2 | **Install set beyond the brief's list** (`label`/`field`, `kbd`, `card`, `alert-dialog`, `toggle`, `toggle-group`, `collapsible`, `checkbox`, `radio-group`, `switch`, `spinner`, `empty`, `input-group`, `separator` is pulled in by `button-group` anyway) — add now in R5 or per slice? | per slice, each its own release, catalog + axe entry in the same commit (§5.3, §10) |
| Q3 | **Raw-element ban scope for `<input type="checkbox|radio|file|hidden|range|color">`** (checkbox 127 + radio 5 + file 6 sites today): allow by literal type until `checkbox`/`radio-group` are installed, or ban now and install them in R5? | allow by literal type (§3.3); counted separately as `rawCheckbox` in `rawElementRatchet` so the number is visible |
| Q4 | **`globals.css` + uPlot CSS into a `legacy` cascade layer in R4** (utilities win on migrated pages; the one exposure is `@grafana/ui` emotion styles outranking 17 `.u-*` rules). Alternative: unlayered globals + Tailwind `important` utilities. | layer (§2.1); R4 ships with a manual 3-theme Playwright pass on Service overview (CorePanel), one Settings tab, Traces; rollback is the `main.tsx` import line |
| Q5 | **Theme fallback without a document** (node tests) becomes `redhat` for `chartTheme()` (today `dark`) — accept the test-time palette change? | accept (R3); no runtime path has a missing attribute (`index.html:54` sets it before paint) |
| Q6 | **PageShell/PageControls move**: update all 68 importers in R8 (mechanical, ratchet 969→901) — or leave a re-export in `components/ui` (ratchet unchanged, shim)? | update importers (R8); CLAUDE.md anti-pattern rejects compat shims and the ratchet would otherwise lie |
| Q7 | **axe matrix size in CI**: full 9 cells (≈243 runs, est. 40-90 s) or 3 theme cells (≈81 runs) with the density cells local-only? | full 9 in CI (R10), `DESIGN_AXE=themes` env for local loops |
| Q8 | **`radix-ui` monopackage vs per-package `@radix-ui/react-*` imports** — the monopackage is what the registry emits; tree-shaking is proven only at the first real consumer. If slice 1 shows a bloated `radix-*.js`, switch? | monopackage in R5; the bundle script's `radix-*.js` size line is the trigger; switching is a CLI flag + regenerate, one release |
| Q9 | **Density → size names** `comfortable→md`, `compact→sm`, `dense→xs` for shadcn-side sizes (no rename of the density values themselves) | as stated (§7.3) |
| Q10 | **`--muted-foreground ← --text2`** (audit draft) vs `--text3` (the ≥4.5-on-every-surface tertiary): shadcn uses `text-muted-foreground` for helper/placeholder text | `--text2` (§2.2), per the audit draft; a slice may re-map once with a contrast check if helper text reads too strong |
| Q11 | **R1 tagging**: rule-changing docs commit tagged as `v0.10.X` (plan) vs the repo's untagged `docs —` convention | tagged — it changes CLAUDE.md rules that gate R2+ |

## 12. Operator rulings (2026-10-30) — binding

| Q | Ruling |
|---|---|
| Q1 | Sonner lazy gate **now** (R10, Option A). |
| Q2 | Components beyond the brief's install list: **per slice**, not in Phase 1. |
| Q3 | `type="file"` **permanently exempt**; `checkbox`/`radio` exempt **until** their shadcn counterparts are installed (then the exemption is removed in that release). |
| Q4 | `legacy` cascade layer **and** split `globals.css`: uPlot / CorePanel / `.u-*` rules move to an **unlayered `chart.css`** (beats emotion as today); everything else into `layer(legacy)`. CorePanel contract test gains a **computed-style spot check** (a few `.u-*` properties resolved via `getComputedStyle`). |
| Q5 | No-document theme fallback = **redhat** (`DEFAULT_THEME`). |
| Q6 | PageShell/PageControls move by **codemod** (all importers updated); **no re-export shim**. |
| Q7 | axe in CI = **3 cells** (one per theme, default density); full 9-cell matrix **nightly / manual**. |
| Q8 | Radix packaging = **whatever `shadcn init` generates**; the `radix` chunk regex is written to match it (verified with `npm ls` + the bundle-budget sourcemap scan). |
| Q9 | Coremetry density names stay (comfortable/compact/dense); `dense` → a custom `xs` size variant; the mapping lives in `useDensity`. |
| Q10 | `--muted-foreground` ← whichever of `--text2`/`--text3` **passes 4.5:1 on its background in all three themes**; a pin test verifies the contrast. |
| Q11 | R1 is a **tagged** release. |
| `index.html` `data-theme` | Prefer a Vite `transformIndexHtml` hook that writes the attribute and the boot-script fallback **from `DEFAULT_THEME`**; only if that is not feasible, keep the literal + pin test. |
| Checkpoints | Start at R1. **Stop after R4 and after R9** and show the operator before continuing. |

