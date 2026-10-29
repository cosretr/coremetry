# shadcn migration log

One row per release of the global shadcn migration (plan:
`docs/frontend/shadcn-phase1-plan.md` §9; decision: DECISIONS 2026-10-30).
Newest row last; the row is written in the same commit as the code. The
"Initial-load" column carries the last line of `npm run bundle:check`
(`frontend/scripts/bundle-budget.mjs`, from R2 on) verbatim. A release whose
measured initial load exceeds 347 KB gz does not ship.

| Release | Scope (page / slice / infra) | What changed | Atoms deleted (files) | ui-import pairs before→after | raw input/select/textarea before→after | inline style={{ (page) before→after | Initial-load JS gz measured (budget 347) · main gz | §7 estimate for this release | Notes |
|---|---|---|---|---|---|---|---|---|---|
| v0.10.1604 (baseline) | — | Phase 1 start | — | 969 | 490 / 159 / 33 | TOTAL 5,244 | 258.3 · 54.6 (operator figure; R2 writes the canonical script number) | — | React 19 (1600) + CoSRE lazy (1599) landed; counts are the plan's read-only measurement (§3), ratchets commit their own in R6 |
| v0.10.1608 (R1) | infra | rule change: Option B superseded, CLAUDE.md + skills, this log | — | 969→969 | unchanged | unchanged | n/a (no code change; R2 measures) | n/a | docs-only, tagged (Q11) |
| v0.10.1609 (R2) | infra | bundle gate: radix/toast chunk groups, `bundle-budget.mjs` in CI + `npm run bundle:check`, shell import ban | — | 969→969 | unchanged | unchanged | \| initial 252.5 KB gz \| main 53.4 KB gz \| budget 347 \| radix-in-entry 0 \| | 0 | first canonical number (zlib default; operator's 258.3 was a different method/build); radix chunk absent, toast chunk absent, 22 preloaded JS |
| v0.10.1610 (R3) | infra | theme default single source: `lib/themeDefault.ts` DEFAULT_THEME, `lib/theme.ts` readTheme/isLightTheme, index.html default written by a Vite `transformIndexHtml` hook | — | 969→969 | unchanged | unchanged | \| initial 252.7 KB gz \| main 53.2 KB gz \| budget 347 \| radix-in-entry 0 \| | 0 (±50 B) | jsdom/node fallback (no document, missing or unknown attribute) is now redhat; 4 tests re-pointed |
