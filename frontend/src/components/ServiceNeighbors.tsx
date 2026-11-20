import { useEffect, useState } from 'react';
import { seriesColor } from '@/lib/chartFmt';
import { Link } from 'react-router';
import { Spinner } from './Spinner';
import { Button, DisclosureButton } from '@/components/ui';
import { api } from '@/lib/api';
import { fmtNum, type GoDuration } from '@/lib/utils';
import type { NeighborStat, ServiceNeighborsResponse, TimeRange } from '@/lib/types';
import { serviceHref } from '@/lib/serviceHref';
import { encodeRange } from '@/lib/urlState';
import { nodeDetailHref } from '@/components/topology/nodeDetailHref';

// Service-level upstream / downstream summary. Renders as a compact
// arrow flow:
//
//   [caller-a] [caller-b] ──► <service> ──► [callee-x] [db oracle@…]
//
// v0.10.1408 (operatör: "Downstream düzgün çalışmıyor") — veri artık
// topology_edges_5m'den, Topology sekmesiyle AYNI okuma: DB / kuyruk / dış
// HTTP çağrıları da downstream sayılıyor (eski örneklemli ham-span yolu
// yalnız başka service_name'li çocuk span'ları görüyordu → "0 downstream").
// Chip'teki sayı KENARIN çağrı sayısı + ortalama süresi. Panel starts
// collapsed; the first open lazy-fetches the data.
export function ServiceNeighbors({ service, since = '10m', capped = false, defaultOpen = false, range }: {
  service: string;
  // v0.9.967 — the PAGE window the operator is reading, carried by every
  // neighbour chip.
  //
  // Deliberately NOT `since`. That is this panel's own sampling window (a
  // Go duration, default '10m') and '10m' is not a PRESET_SECONDS key —
  // decodeRange accepts any non-custom string as a preset, so emitting it
  // would silently land the destination on the 86400s fallback: a link
  // claiming ten minutes and rendering twenty-four hours. The honest
  // window for "open this neighbour" is the one the operator is looking
  // at, which is also the only one that can be a brushed `custom:`.
  range?: import('@/lib/types').TimeRange;
  since?: GoDuration;
  // v0.9.257 — true when the caller clamped `since` below the operator's
  // selected range (see NEIGHBORS_CAP_S in pages/service/Overview.tsx).
  // Surfaced in the header so a narrowed window is stated, not silent.
  capped?: boolean;
  // v0.5.294 — when true, the panel renders expanded on first
  // paint instead of waiting for the operator to click the
  // header. Used from the Service detail Details tab where
  // the operator has already signalled "show me everything"
  // by switching tabs.
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const [data, setData] = useState<ServiceNeighborsResponse | null | undefined>(undefined);
  // Bumped to force a refetch with refresh=1 (cache bypass).
  const [refreshTick, setRefreshTick] = useState(0);

  useEffect(() => {
    if (!open || !service) return;
    setData(undefined);
    api.serviceNeighbors(service, since, refreshTick > 0)
      .then(setData)
      .catch(() => setData(null));
  }, [open, service, since, refreshTick]);

  const upstream = data?.upstream ?? [];
  const downstream = data?.downstream ?? [];
  const upTotal = data?.upstreamTotal ?? upstream.length;
  const downTotal = data?.downstreamTotal ?? downstream.length;
  const moreHref = serviceHref(service, { range, tab: 'topology' });

  return (
    <div style={{
      background: 'var(--bg1)', border: '1px solid var(--border)',
      borderRadius: 8, marginBottom: 14,
    }}>
      {/* v0.10.927 — "↻ Refresh" başlık düğmesinin İÇİNDEN çıktı (button
          içinde button olmaz): kardeş gerçek Button — Tab ile ulaşılır,
          Enter/Space yalnız yeniler, bölümü açıp kapamaz. DisclosureButton
          flex:1 ile kalan genişliği alır ve ARIA'ya bağlı ayracını kendisi
          çizer; Refresh hücresi yalnız AÇIKKEN var, o yüzden aynı ayracı sabit
          taşır — çizgi tam genişlikte kesintisiz. */}
      <div style={{ display: 'flex' }}>
        <DisclosureButton anatomy="section" expanded={open} onClick={() => setOpen(o => !o)}
          // Refresh hücresi varken sağ dolgu 8 + hücrenin sol 4'ü = eski 12px
          // aralık; o 4px başlığın odak halkasının (2px + 2px ofset) Refresh
          // düğmesinin altında kalmasını da önler.
          style={{ flex: 1, minWidth: 0, ...(open && data !== undefined ? { paddingRight: 8 } : null) }}>
          <span style={{ fontSize: 12, color: 'var(--text2)', fontWeight: 600 }}>
            Upstream / downstream for <span style={{ color: 'var(--text)' }}>{service}</span>
          </span>
          {open && data && (
            <span style={{ fontSize: 11, color: 'var(--text3)' }}>
              {upTotal} upstream · {downTotal} downstream
              {' · '}last {since}{capped && ' (capped)'}
              {' · '}topology rollup (5-min)
            </span>
          )}
          <span style={{ flex: 1 }} />
          {!open && (
            <span style={{ fontSize: 11, color: 'var(--text3)', fontStyle: 'italic' }}>
              click to expand
            </span>
          )}
        </DisclosureButton>
        {open && data !== undefined && (
          // v0.10.928 — iç ayraç: `.btn-disclose.dsc-section[aria-expanded]`
          // alt çizgisiyle aynı token (--divider); ikisi birlikte değişir.
          <span style={{
            display: 'flex', alignItems: 'center', flex: 'none',
            paddingLeft: 4, paddingRight: 14, borderBottom: '1px solid var(--divider)',
          }}>
            <Button variant="secondary" size="xs"
              title="Bypass the cached result and recompute now"
              onClick={() => setRefreshTick(t => t + 1)}>
              ↻ Refresh
            </Button>
          </span>
        )}
      </div>

      {open && (
        <div style={{ padding: 14, paddingTop: 10 }}>
          {data === undefined && (
            <div style={{ minHeight: 80, display: 'grid', placeItems: 'center' }}>
              <Spinner />
            </div>
          )}
          {data === null && (
            <div style={{ fontSize: 12, color: 'var(--text3)', fontStyle: 'italic', padding: '12px 4px' }}>
              Failed to load upstream / downstream.
            </div>
          )}
          {data && upstream.length === 0 && downstream.length === 0 && (
            <div style={{ fontSize: 12, color: 'var(--text3)', fontStyle: 'italic', padding: '12px 4px' }}>
              No upstream callers or downstream dependencies for
              <code style={{ margin: '0 6px' }}>{service}</code>in this window.
            </div>
          )}
          {data && (upstream.length > 0 || downstream.length > 0) && (
            <div style={{
              display: 'flex', alignItems: 'center', gap: 16,
              fontSize: 12, flexWrap: 'wrap',
            }}>
              <Column
                range={range}
                label={`Upstream (${upTotal})`}
                items={upstream}
                total={upTotal}
                moreHref={moreHref}
                emptyText="No upstream callers"
                align="right"
              />
              <Arrow />
              <SelfChip name={service} />
              <Arrow />
              <Column
                range={range}
                label={`Downstream (${downTotal})`}
                items={downstream}
                total={downTotal}
                moreHref={moreHref}
                emptyText="No downstream dependencies"
                align="left"
              />
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Column({ label, items, total, moreHref, emptyText, align, range }: {
  label: string;
  items: NeighborStat[];
  total: number;
  moreHref: string;
  emptyText: string;
  align: 'left' | 'right';
  range?: TimeRange;
}) {
  return (
    <div style={{
      display: 'flex', flexDirection: 'column',
      gap: 4, alignItems: align === 'right' ? 'flex-end' : 'flex-start',
      minWidth: 180,
    }}>
      <div style={{
        fontSize: 10, color: 'var(--text3)', textTransform: 'uppercase',
        letterSpacing: 0.4, fontWeight: 600,
      }}>{label}</div>
      {items.length === 0 ? (
        <span style={{ fontSize: 11, color: 'var(--text3)', fontStyle: 'italic' }}>
          {emptyText}
        </span>
      ) : items.map(n => (
        <Chip key={n.id} stat={n} range={range} />
      ))}
      {total > items.length && (
        <Link to={moreHref} style={{ fontSize: 11, color: 'var(--accent2)', textDecoration: 'none' }}>
          +{total - items.length} more → topology
        </Link>
      )}
    </div>
  );
}

// KIND_TAG — servis olmayan düğümün kısa türü (Topology sözlüğü).
const KIND_TAG: Partial<Record<NeighborStat['kind'], string>> = {
  database: 'db', queue: 'queue', external: 'ext', internal: 'int',
};

// neighborHref — chip hedefi. Servis → serviceHref (pencere taşır); diğer
// türler Topology'nin nodeDetailHref'i (db → /database, kuyruk →
// /messaging); hedefi olmayan düğüm (external) link çizmez.
function neighborHref(stat: NeighborStat, range?: TimeRange): string | null {
  if (stat.kind === 'service') return serviceHref(stat.name, { range });
  return nodeDetailHref({ ...stat, rate: 0 }, range ? { range: encodeRange(range) } : {});
}

function Chip({ stat, range }: { stat: NeighborStat; range?: TimeRange }) {
  const color = seriesColor(stat.name);
  const tag = KIND_TAG[stat.kind];
  const href = neighborHref(stat, range);
  const title = `${stat.name}${stat.system ? ` (${stat.system})` : ''}\n`
    + `${fmtNum(stat.calls)} call${stat.calls === 1 ? '' : 's'} · avg ${stat.avgMs.toFixed(1)} ms · p99 ${stat.p99Ms.toFixed(0)} ms`
    + ` · ${stat.errorRate.toFixed(2)}% errors`
    + (stat.protocols?.length ? `\n${stat.protocols.join(', ')}` : '');
  const style: React.CSSProperties = {
    display: 'inline-flex', alignItems: 'center', gap: 8,
    padding: '4px 10px', borderRadius: 6,
    border: '1px solid var(--border)',
    background: 'var(--bg2)', color: 'var(--text)',
    textDecoration: 'none', fontSize: 12,
  };
  const body = (
    <>
      <span style={{
        width: 8, height: 8, borderRadius: '50%',
        background: color, flexShrink: 0,
      }} />
      {tag && <span style={{ color: 'var(--text3)', fontSize: 10, fontFamily: 'var(--font-mono)' }}>{tag}</span>}
      <span>{stat.name}</span>
      <span style={{ color: 'var(--text3)', fontSize: 10, fontFamily: 'var(--font-mono)' }}>
        {fmtNum(stat.calls)} · {stat.avgMs < 10 ? stat.avgMs.toFixed(1) : stat.avgMs.toFixed(0)} ms
      </span>
    </>
  );
  return href
    ? <Link to={href} style={style} title={title}>{body}</Link>
    : <span style={style} title={title}>{body}</span>;
}

function SelfChip({ name }: { name: string }) {
  const color = seriesColor(name);
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 8,
      padding: '6px 12px', borderRadius: 6,
      border: `2px solid ${color}`,
      background: 'var(--bg3)', color: 'var(--text)',
      fontSize: 13, fontWeight: 600,
    }}>
      <span style={{
        width: 10, height: 10, borderRadius: '50%',
        background: color, flexShrink: 0,
      }} />
      {name}
    </span>
  );
}

function Arrow() {
  return (
    <span style={{ color: 'var(--text3)', fontSize: 18, fontFamily: 'var(--font-mono)' }}>
      →
    </span>
  );
}
