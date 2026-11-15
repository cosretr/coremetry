import { useEffect, useRef, useState } from 'react';
import { useIsFetching } from '@tanstack/react-query';
import { RefreshCw } from 'lucide-react';
import { IconButton } from './ui/IconButton';
import { useT } from '@/lib/i18n';
import { useIsNarrow } from '@/lib/useNarrow';
import { REFRESH_CHOICES, REFRESH_CHOICE_SECONDS, formatRefreshParam, type RefreshChoice } from '@/lib/autoRefresh';
import { useManualRefresh, useRefreshSettings } from '@/lib/useAutoRefresh';

// RefreshPicker (v0.10.1430) — Grafana-style refresh control in the Topbar,
// right of the time picker. Operator: "Sağ üstte zaman yanında Grafana gibi
// ayrı refresh olabilir. 60 saniye default olabilir."
//
// Anatomy (ui/ atoms + a native <select>, the ≤10-value rule):
//   ↻  — manual refresh, everywhere; spins while any query is in flight.
//   ▾  — Off / 10s / 30s / 1m / 5m / 15m. On a MANUAL route the select is
//        disabled and reads "Off · manual" with the reason in its title —
//        disabled, not hidden, so the operator can see the clock is not
//        running here (the EnvPicker `applies` honesty rule, v0.9.864).
//        Choices below the route floor (/dashboard: 30 s) are not offered.
//   caption — "updated 12 s ago", or the pause reason ("paused · absolute
//        range", "paused · editing"). Re-rendered every 5 s (a LOCAL tick,
//        no request; skipped while the tab is hidden). Hidden ≤640px.
// Screen readers: the manual ↻ announces "Refreshed" once the in-flight
// count returns to zero (aria-live=polite, manual path only — a periodic auto
// announcement would be noise).
//
// All state lives in lib/useAutoRefresh.ts (URL `?refresh=` > localStorage >
// DEFAULT_REFRESH_SEC, 5 min since v0.10.2264); this component holds only the caption tick and the live message.

function agoLabel(t: (k: string) => string, ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return t('rp.ago.s').replace('{n}', String(s));
  return t('rp.ago.m').replace('{n}', String(Math.round(s / 60)));
}

export function RefreshPicker() {
  const t = useT();
  const narrow = useIsNarrow();
  const { mode, minSec, sec, paused, lastRefreshAt, setSec } = useRefreshSettings();
  const refreshNow = useManualRefresh();
  const fetching = useIsFetching();
  const manual = mode === 'manual';

  // Caption clock — local re-render only.
  const [, setTick] = useState(0);
  useEffect(() => {
    if (narrow || manual || sec === 0) return;
    const id = setInterval(() => { if (!document.hidden) setTick(n => n + 1); }, 5_000);
    return () => clearInterval(id);
  }, [narrow, manual, sec]);

  // Manual-path completion announcement.
  const pendingRef = useRef(false);
  const [announce, setAnnounce] = useState('');
  useEffect(() => {
    if (pendingRef.current && fetching === 0) {
      pendingRef.current = false;
      setAnnounce(t('rp.refreshed'));
    }
  }, [fetching, t]);

  const value: RefreshChoice = manual ? 'off' : formatRefreshParam(sec);
  const choices = REFRESH_CHOICES.filter(c => c === 'off' || REFRESH_CHOICE_SECONDS[c] >= minSec);
  const title = manual ? t('rp.manualReason')
    : sec === 0 ? t('rp.offReason')
    : t('rp.every').replace('{t}', value);
  const caption = manual || sec === 0 ? ''
    : paused === 'absolute' ? t('rp.pausedAbsolute')
    : paused ? t('rp.pausedEdit')
    : t('rp.updatedAgo').replace('{ago}', agoLabel(t, Date.now() - lastRefreshAt));

  return (
    <div className="rp" role="group" aria-label={t('rp.label')}>
      <IconButton aria-label={t('rp.refreshNow')} tooltip={t('rp.refreshNow')} variant="secondary"
        className={fetching > 0 ? 'rp-spin' : undefined}
        icon={<RefreshCw size={13} />}
        onClick={() => { pendingRef.current = true; setAnnounce(''); refreshNow(); }} />
      <select className="rp-select" aria-label={t('rp.label')} title={title} value={value} disabled={manual}
        onChange={e => setSec(REFRESH_CHOICE_SECONDS[e.target.value as RefreshChoice] ?? 0)}>
        {manual
          ? <option value="off">{t('rp.manualOnly')}</option>
          : choices.map(c => <option key={c} value={c}>{c === 'off' ? t('rp.off') : c}</option>)}
      </select>
      {!narrow && caption && <span className="rp-caption" title={title}>{caption}</span>}
      <span className="sr-only" aria-live="polite">{announce}</span>
    </div>
  );
}
