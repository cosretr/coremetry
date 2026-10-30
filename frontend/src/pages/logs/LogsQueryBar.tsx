// LogsQueryBar — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı, dilim 1;
// operatör onaylı taslak 2026-10-24 — v0.10.1332 "Filtre barı · Search son
// filtrenin arkasında" mockup pinini BİLİNÇLİ olarak geçersiz kılar, bkz.
// docs/DECISIONS.md).
//
// Çubukta YALNIZ sorgunun kendisi: Service · Cluster · KQL · [Search | ↻ HH:MM].
// Search ile ↻ tek düğme (Kibana "Update" ⇄ "Refresh"): taslak uygulanan
// süzgeçle aynıyken düğme pencere sonunu gösterir ve tık pencereyi şimdiye
// taşır; taslak değiştiyse "Search" der ve uygular. Canlı kuyrukta pencere
// yok → hep "Search". Sağdaki eylem yuvası (PageControls `actions`, telefonda
// katlanmaz): + Filter ▾ · Live tail · Saved ▾ · Share ▾ · ⋯ — menüler
// Logs.tsx'te kurulup `menus` olarak gelir. Saf sunum: durum Logs.tsx'te.
import type { ReactNode } from 'react';
import { ServicePicker } from '@/components/ServicePicker';
import { KqlSearchInput } from '@/components/KqlSearchInput';
import { Button } from '@/components/ui/Button';
import { EnterHint } from '@/components/ui/EnterHint';
import { PageControls } from '@/components/coremetry/PageControls';
import type { LogsUrlFilter } from '@/lib/logsUrl';
import { fmtClock } from '@/lib/utils';

export interface LogsQueryBarProps {
  draft: LogsUrlFilter;
  onDraft: (next: LogsUrlFilter) => void;
  clusters: string[];
  autocompleteSince: string;
  fields: string[];
  fieldsTotal?: number;
  /** Taslağı uygula (Search). */
  onApply: () => void;
  /** Pencereyi şimdiye taşı + yeniden sorgula (↻). */
  onRefresh: () => void;
  /** Taslak uygulanan süzgeçten farklı mı (isDraftDirty). */
  dirty: boolean;
  /** Pencere sonu (ns) — ↻ etiketi; yoksa yalnız ↻. */
  to?: number;
  live: boolean;
  onToggleLive: () => void;
  liveGap: boolean;
  /** + Filter ▾ — çubukta Live'ın solunda. */
  addFilter: ReactNode;
  /** Saved ▾ · Share ▾ · ⋯ — Live'ın sağında. */
  menus: ReactNode;
}

export function LogsQueryBar(p: LogsQueryBarProps) {
  const { draft } = p;
  const refreshMode = !p.dirty && !p.live;
  return (
    <PageControls sticky className="ld-qbar" actions={(
      <>
        {p.addFilter}
        {/* v0.10.1318 (K3) — canlı kuyruk bir KİP: tint + nabız noktası
            (.live-on yalnız yerleşim), birincil dolgu değil. */}
        <Button variant="secondary" active={p.live}
          className={p.live ? 'live-on' : undefined}
          onClick={p.onToggleLive}
          title="Stream the latest logs live (server-pushed; pauses when the tab is hidden)">
          {p.live ? '⏸ Pause Live' : '▶ Live tail'}
        </Button>
        {p.live && p.liveGap && (
          <span className="badge b-warn" title="A busy service produced more lines than one tick could read — narrow the filter to see them all.">
            ⚠ high volume — some lines skipped
          </span>
        )}
        {p.menus}
      </>
    )}>
      <ServicePicker value={draft.service} onChange={v => p.onDraft({ ...draft, service: v })}
        placeholder="Service…" width={170} onEnter={p.onApply} />
      {/* v0.5.471 — cluster selector. /api/clusters: typically 1-5 entries
          per install, so a plain <select> is the right shape. */}
      {p.clusters.length > 0 && (
        <select value={draft.cluster}
          onChange={e => p.onDraft({ ...draft, cluster: e.target.value })}
          title="Filter logs to a single k8s/openshift cluster"
          className="ld-cluster">
          <option value="">All clusters</option>
          {p.clusters.map(c => <option key={c} value={c}>{c}</option>)}
        </select>
      )}
      <KqlSearchInput
        className="ld-kql"
        width="auto"
        // v0.9.291 — autocomplete term walk bounded by the window (memoised
        // in Logs.tsx — a bare rangeToSince(range) in JSX refetches forever).
        since={p.autocompleteSince}
        // v0.9.955 / v0.9.970 — field-name completion from the SAME
        // /api/logs/fields state the fields panel uses (zero extra ES calls),
        // with the mapping's real path count for the "first N of M" note.
        fields={p.fields}
        fieldsTotal={p.fieldsTotal}
        // v0.9.1003 — the page's ONE `/` shortcut target; v0.9.1004 — stays
        // on the surface on phones (PageControls lead mark).
        shortcutSearch
        data-pc-lead
        value={draft.search}
        onChange={v => p.onDraft({ ...draft, search: v })}
        onSubmit={p.onApply}
        placeholder='Search… (KQL: level:error AND service.name:"checkout")'
        title={'Free-text on body OR KQL/Lucene syntax (Elasticsearch backend).\n\n' +
          'Examples:\n' +
          '  level:error\n' +
          '  service.name:"checkout-svc" AND NOT message:health\n' +
          '  trace.id:c9ea*\n' +
          '  message:"connection refused" AND k8s.namespace:prod\n\n' +
          'Plain words match the body. Use double quotes for exact phrases.\n\n' +
          'Field-aware autocomplete (v0.5.464): type `field:` and pick from the dropdown.'} />
      {/* Tek birincil (K4). ↻ kipinde etiket PENCERE SONU (sorgulanan an),
          fetch zamanı değil (cevap 15 sn sunucu önbelleğinden gelebilir —
          v0.10.440 B3). `data-pc-pin`: telefonda panele inmez. */}
      <span className="search-go" data-pc-pin>
        {refreshMode ? (
          <Button variant="primary" aria-label="Yenile" onClick={p.onRefresh}
            title={p.to ? `Veri penceresi ${fmtClock(p.to / 1e6)} anında bitiyor — ↻ pencereyi şimdiye taşır ve yeniden sorgular` : 'Yeniden sorgula'}>
            ↻{p.to ? ` ${fmtClock(p.to / 1e6)}` : ''}
          </Button>
        ) : (
          <Button variant="primary" onClick={p.onApply}>Search</Button>
        )}
        <EnterHint title="Arama kutusunda (ve + Filter içindeki Trace ID kutusunda) Enter" />
      </span>
    </PageControls>
  );
}
