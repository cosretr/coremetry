import { useSyncExternalStore } from 'react';
import type { TimeRange } from './types';

// autoRefresh (v0.10.1430) — Grafana-style GLOBAL auto-refresh: the pure
// rules (URL codec, precedence, clamp, route policy) plus the one module
// store every consumer reads through useSyncExternalStore.
//
// Operator (2026-10-22): "Sağ üstte zaman yanında Grafana gibi ayrı
// refresh olabilir. 60 saniye default olabilir, özellikle metrikler için
// işe yarar."
//
// INVENTORY FINDING that made this a mechanism rather than a button: a
// relative window ("last 3h") was FROZEN at mount. useUrlRange memoises
// `range` on the URL string (v0.5.184 trap), every page derives
// `useMemo(() => timeRangeToNs(range), [range])`, so without a new
// identity no fetch effect ever re-ran — the per-query 30 s polls on
// range-keyed queries re-requested the SAME past window (a guaranteed
// cache hit, zero new data). The Dashboard's refreshTick (v0.9.779) and
// Clusters' liveTick (v0.9.43) were two local patches of the same hole.
//
// THE SLIDE: `anchorMs` below is the single extra dependency in
// useUrlRange's memo. bumpRefresh() moves it → every `range` identity
// changes → every downstream useMemo recomputes timeRangeToNs with a
// fresh Date.now() → new React Query keys → the window advances. Zero
// edits across the ~194 timeRangeToNs call sites; custom (absolute)
// windows are excluded from the slide (nothing to advance — Grafana
// parity: refresh pauses on an absolute range).
//
// Since-keyed queries (GoDuration `since=1h`) cannot slide — same key,
// server computes "now". They opt in with `meta: LIVE_META` and the tick
// invalidates them (useAutoRefresh.ts); they lose their own
// refetchInterval so one clock drives everything.

// ── Choices + codec ──────────────────────────────────────────────────

export const REFRESH_CHOICES = ['off', '10s', '30s', '1m', '5m', '15m'] as const;
export type RefreshChoice = typeof REFRESH_CHOICES[number];

export const REFRESH_CHOICE_SECONDS: Record<RefreshChoice, number> = {
  off: 0, '10s': 10, '30s': 30, '1m': 60, '5m': 300, '15m': 900,
};

/** Operator default: 5 minutes (v0.10.2264, operator decision; was 60 s).
 *  Applies only when neither `?refresh=` nor a stored user choice exists;
 *  every shorter choice (10s/30s/1m) stays one pick away in RefreshPicker. */
export const DEFAULT_REFRESH_SEC = 300;
/** House floor (CLAUDE.md: polling ≥ 10 s). */
export const REFRESH_MIN_SEC = 10;
/** /dashboard floor (lib/dashboardUrl.ts v0.9.779 rationale: a bundle
 *  request carries up to 50 panel queries). */
export const DASHBOARD_REFRESH_MIN_SEC = 30;

const CHOICE_BY_SEC = new Map<number, RefreshChoice>(
  REFRESH_CHOICES.map(c => [REFRESH_CHOICE_SECONDS[c], c]),
);

/**
 * parseRefreshParam — `?refresh=` → seconds, or null when the value is
 * not one of ours (caller falls through to the next precedence step).
 * Accepts the choice tokens (off|10s|30s|1m|5m|15m), `0`, and the legacy
 * Dashboard integers (30|60|300 — v0.9.779 wrote bare seconds) so old
 * share links keep their meaning.
 */
export function parseRefreshParam(raw: string | null | undefined): number | null {
  if (raw == null) return null;
  const v = raw.trim().toLowerCase();
  if (v === '') return null;
  if (v in REFRESH_CHOICE_SECONDS) return REFRESH_CHOICE_SECONDS[v as RefreshChoice];
  if (/^\d+$/.test(v)) {
    const n = Number(v);
    return CHOICE_BY_SEC.has(n) ? n : null;
  }
  return null;
}

/** formatRefreshParam — seconds → URL/select token. Non-choice values
 *  (a clamped 10 → 30 on /dashboard is still a choice) snap UP to the
 *  first choice ≥ sec; above 15m → '15m'. */
export function formatRefreshParam(sec: number): RefreshChoice {
  if (sec <= 0) return 'off';
  for (const c of REFRESH_CHOICES) {
    if (c !== 'off' && REFRESH_CHOICE_SECONDS[c] >= sec) return c;
  }
  return '15m';
}

/** clampRefresh — 0 stays 0 (off); anything else is floored at minSec. */
export function clampRefresh(sec: number, minSec: number): number {
  if (sec <= 0) return 0;
  return Math.max(sec, minSec);
}

/**
 * resolveRefresh — precedence URL > stored (localStorage) > default,
 * then the route floor. Same shape as useUrlEnv's resolveEnv so the
 * operator's mental model ("refresh behaves like env/range") holds.
 */
export function resolveRefresh(urlRaw: string | null | undefined, stored: string | null | undefined, minSec: number): number {
  const fromUrl = parseRefreshParam(urlRaw);
  const fromStore = fromUrl === null ? parseRefreshParam(stored) : null;
  const sec = fromUrl ?? fromStore ?? DEFAULT_REFRESH_SEC;
  return clampRefresh(sec, minSec);
}

// ── Route policy ─────────────────────────────────────────────────────

export type RefreshMode = 'auto' | 'manual';
export interface RefreshPolicy { mode: RefreshMode; minSec: number }

/**
 * AUTO_REFRESH_ROUTES — CLOSED allowlist of routes where the clock runs.
 * Metric / overview surfaces whose queries are range-keyed (they slide)
 * or LIVE_META-tagged. Everything else (logs, traces, trace, explore,
 * search, problems, anomalies, settings…) is MANUAL: the ↻ works, the
 * clock does not — a log tail or a trace waterfall re-running itself
 * every minute is cost without insight, and those pages carry their own
 * SSE / 30 s list polls where liveness matters.
 * Prefix match on the path segment: '/service' covers '/service/backtrace',
 * '/dashboard' does NOT cover '/dashboards' (its own 60 s list poll).
 */
export const AUTO_REFRESH_ROUTES: readonly string[] = [
  '/services', '/service', '/endpoints', '/endpoint', '/metrics',
  '/dashboard', '/service-map', '/topology', '/hosts', '/clusters',
];

export function refreshPolicy(pathname: string): RefreshPolicy {
  const auto = AUTO_REFRESH_ROUTES.some(p => pathname === p || pathname.startsWith(p + '/'));
  if (!auto) return { mode: 'manual', minSec: REFRESH_MIN_SEC };
  const minSec = pathname === '/dashboard' || pathname.startsWith('/dashboard/')
    ? DASHBOARD_REFRESH_MIN_SEC : REFRESH_MIN_SEC;
  return { mode: 'auto', minSec };
}

// ── Query tagging ────────────────────────────────────────────────────

/** `meta: LIVE_META` on a since-keyed query = "refetch me on the tick". */
export const LIVE_META = { refresh: 'live' } as const;

export function isLiveQuery(meta: Record<string, unknown> | undefined): boolean {
  return meta?.refresh === 'live';
}

// ── Heavy-query rung snap ────────────────────────────────────────────

/** heavyRungNs — windows ≥ 1 h snap to a 5-min rung, shorter ones to 1 min
 *  (a 5-min lag on a 15-min window would hide a third of it). */
export function heavyRungNs(lenNs: number): number {
  return lenNs >= 3600e9 ? 300e9 : 60e9;
}

/**
 * snapHeavyWindow — v0.8.270 discipline for the most expensive reads on
 * an auto-refreshing page (Service latency heatmap, endpoint downstream /
 * callers walks). The 60 s tick re-keys every range-keyed query; these
 * two raw-spans scans must not re-run every minute, so their `to` snaps
 * DOWN to a rung and `from` shifts by the same delta (window length is
 * preserved, the key changes at most once per rung). Absolute windows
 * pass through untouched — they never slide and snapping would move the
 * operator's chosen bounds.
 */
export function snapHeavyWindow(range: TimeRange, win: { from: number; to: number }): { from: number; to: number } {
  if (range.preset === 'custom') return win;
  const rung = heavyRungNs(win.to - win.from);
  const d = win.to % rung;
  if (d === 0) return win;
  return { from: win.from - d, to: win.to - d };
}

// ── Store ────────────────────────────────────────────────────────────

export type HoldReason = 'edit';

export interface RefreshState {
  /** Moves on every tick (auto or manual) — the useUrlRange slide dep. */
  anchorMs: number;
  /** Timestamp of the last tick; the picker's "updated N s ago". Page load
   *  counts as the first refresh. */
  lastRefreshAt: number;
  /** Active pauses pushed by pages (Dashboard edit mode). */
  holds: ReadonlyArray<HoldReason>;
}

let state: RefreshState = { anchorMs: Date.now(), lastRefreshAt: Date.now(), holds: [] };
const listeners = new Set<() => void>();
const holdSet = new Map<string, HoldReason>();
let holdSeq = 0;

function emit(next: RefreshState): void {
  state = next;
  listeners.forEach(l => l());
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => { listeners.delete(cb); };
}

export function refreshSnapshot(): RefreshState {
  return state;
}

/** bumpRefresh — THE tick. Slides every relative window (via useUrlRange)
 *  and stamps lastRefreshAt. Query invalidation lives in useAutoRefresh.ts
 *  (needs the QueryClient). */
export function bumpRefresh(now: number = Date.now()): void {
  emit({ ...state, anchorMs: now, lastRefreshAt: now });
}

/** holdRefresh — pause the clock while the returned release is pending
 *  (Dashboard edit: refetching under an unsaved draft is noise). */
export function holdRefresh(reason: HoldReason): () => void {
  const key = `${reason}:${++holdSeq}`;
  holdSet.set(key, reason);
  emit({ ...state, holds: Array.from(new Set(holdSet.values())) });
  return () => {
    if (!holdSet.delete(key)) return;
    emit({ ...state, holds: Array.from(new Set(holdSet.values())) });
  };
}

export function useRefreshState(): RefreshState {
  return useSyncExternalStore(subscribe, refreshSnapshot, refreshSnapshot);
}

/** useRefreshAnchor — the one dependency useUrlRange adds to its memo. */
export function useRefreshAnchor(): number {
  return useSyncExternalStore(subscribe, () => state.anchorMs, () => state.anchorMs);
}

/** Tests only. */
export function __resetRefreshStore(now: number = Date.now()): void {
  holdSet.clear();
  emit({ anchorMs: now, lastRefreshAt: now, holds: [] });
}
