import { useCallback, useEffect } from 'react';
import { useLocation, useSearchParams } from 'react-router-dom';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import {
  bumpRefresh, formatRefreshParam, holdRefresh, isLiveQuery, refreshPolicy, refreshSnapshot,
  resolveRefresh, useRefreshState, type HoldReason, type RefreshMode,
} from './autoRefresh';
import { currentRange } from './useUrlRange';
import { STORAGE_KEYS, getRaw, setRaw } from './storage';

// useAutoRefresh (v0.10.1430) — the ONE clock behind the Topbar
// RefreshPicker. Mounted once in AppShell next to useEventStream; the
// picker and the pages read the same resolved settings through
// useRefreshSettings. Pure rules + store: lib/autoRefresh.ts.
//
// Tick = bumpRefresh() (slides every relative window via useUrlRange's
// anchor dependency) + invalidate the LIVE_META-tagged queries that are
// since-keyed and cannot slide. Both are coalesced into one React commit.
//
// Pauses (Grafana parity, the caption says which):
//   • document.hidden — no request from a background tab; on return the
//     tick fires immediately if a full interval has elapsed (catch-up),
//     otherwise the regular cadence resumes.
//   • absolute (custom:) range — a frozen window has nothing to advance.
//   • a page hold (Dashboard edit mode) — refetching under an unsaved
//     draft is noise.
//   • manual routes — outside AUTO_REFRESH_ROUTES the clock never runs;
//     ↻ still works everywhere.

export type RefreshPause = 'absolute' | HoldReason | null;

export interface RefreshSettings {
  mode: RefreshMode;
  minSec: number;
  /** Resolved + clamped interval in seconds; 0 = off. */
  sec: number;
  /** Why the clock is not running even though sec > 0 (null = running). */
  paused: RefreshPause;
  lastRefreshAt: number;
  setSec: (sec: number) => void;
}

function invalidateLive(qc: QueryClient): void {
  void qc.invalidateQueries({ predicate: q => isLiveQuery(q.meta) });
}

/** useRefreshSettings — resolved picker state for the current location. */
export function useRefreshSettings(): RefreshSettings {
  const { pathname, search } = useLocation();
  const [sp, setSp] = useSearchParams();
  const { lastRefreshAt, holds } = useRefreshState();
  const policy = refreshPolicy(pathname);
  const sec = resolveRefresh(sp.get('refresh'), getRaw(STORAGE_KEYS.refresh), policy.minSec);
  const absolute = currentRange(search).preset === 'custom';
  const paused: RefreshPause = holds.length > 0 ? holds[0] : absolute ? 'absolute' : null;

  // URL (replace:true, prev copied — frontend-design-system §4 (eski frontend-conventions)) + localStorage
  // mirror, the useUrlEnv shape: the pick follows the operator across pages
  // and a share link reproduces it.
  const setSec = useCallback((next: number) => {
    const token = formatRefreshParam(next);
    setSp(prev => {
      const n = new URLSearchParams(prev);
      n.set('refresh', token);
      return n;
    }, { replace: true });
    setRaw(STORAGE_KEYS.refresh, token);
  }, [setSp]);

  return { mode: policy.mode, minSec: policy.minSec, sec, paused, lastRefreshAt, setSec };
}

/** useAutoRefresh — the clock. Mount ONCE (AppShell). */
export function useAutoRefresh(enabled: boolean): void {
  const qc = useQueryClient();
  const { mode, sec, paused } = useRefreshSettings();
  const running = enabled && mode === 'auto' && sec > 0 && paused === null;

  useEffect(() => {
    if (!running) return;
    const ms = sec * 1000;
    const tick = () => { bumpRefresh(); invalidateLive(qc); };
    const id = setInterval(() => {
      if (document.hidden) return; // gizli sekmede istek yok (CLAUDE.md)
      tick();
    }, ms);
    const onVisible = () => {
      if (document.hidden) return;
      if (Date.now() - refreshSnapshot().lastRefreshAt >= ms) tick(); // catch-up
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      clearInterval(id);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [running, sec, qc]);
}

/** useRefreshHold — a page pauses the clock while `on` (Dashboard edit). */
export function useRefreshHold(on: boolean, reason: HoldReason): void {
  useEffect(() => {
    if (!on) return;
    return holdRefresh(reason);
  }, [on, reason]);
}

/**
 * useManualRefresh — the ↻. Same slide as the tick, no clamp, and —
 * unlike the auto tick — it also re-runs the active queries that did NOT
 * re-key (absolute windows, point-in-time lists): Grafana's refresh on
 * an absolute range re-queries. The invalidation is deferred one task so
 * the queries the slide just re-keyed are already in flight (excluded by
 * fetchStatus) and their old keys are no longer active — otherwise every
 * range-keyed query would fetch twice.
 */
export function useManualRefresh(): () => void {
  const qc = useQueryClient();
  return useCallback(() => {
    bumpRefresh();
    setTimeout(() => {
      void qc.invalidateQueries({
        type: 'active',
        predicate: q => q.state.fetchStatus !== 'fetching',
      });
    }, 0);
  }, [qc]);
}
