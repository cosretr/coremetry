// SpanPeers — dalga 3 (2026-11-21, Honeycomb "span vs peers"): span detay
// panelinde aynı servis + operasyonun trace penceresindeki (±15 dk) süre
// dağılımı; bu span'in kovası işaretli, ≈ yüzdelik sırası yazılı, Traces'te
// aynı sorguyu açan bağlantı. Düz div çubuklar (≤ ~30 kova, statik —
// endpoints/detailSections HistogramSection emsali; uPlot gereksiz).
import { useMemo } from 'react';
import { Link } from 'react-router';
import { useSpanPeers } from '@/lib/queries/spans';
import { operationTracesHref } from '@/lib/pivotHref';
import { fmtNum } from '@/lib/utils';
import { useT } from '@/lib/i18n';
import { foldPeers, peerWindow, PEER_BUCKETS } from './spanPeersModel';
import type { SpanRow } from '@/lib/types';

function fmtMs(v: number): string {
  if (v < 1) return `${(v * 1000).toFixed(0)}µs`;
  if (v < 10) return `${v.toFixed(1)}ms`;
  if (v < 1000) return `${v.toFixed(0)}ms`;
  return `${(v / 1000).toFixed(1)}s`;
}

export function SpanPeers({ span, traceStartNs, traceEndNs }: { span: SpanRow; traceStartNs: number; traceEndNs: number }) {
  const t = useT();
  const win = useMemo(() => peerWindow(traceStartNs, traceEndNs), [traceStartNs, traceEndNs]);
  const q = useSpanPeers(span.serviceName, span.name, win.from, win.to, PEER_BUCKETS);
  const hist = useMemo(() => foldPeers(q.data, span.durationMs), [q.data, span.durationMs]);
  const max = hist ? Math.max(1, ...hist.counts) : 1;
  const href = operationTracesHref({ window: { fromNs: win.from, toNs: win.to }, operation: span.name, service: span.serviceName });
  const sampled = q.data?.samplingRate !== undefined && q.data.samplingRate > 0 && q.data.samplingRate < 1;
  return (
    <div data-testid="span-peers">
      {q.isLoading ? (
        <p className="m-0 text-xs text-muted-foreground">{t('span.peers.loading')}</p>
      ) : q.isError || !hist || hist.total === 0 ? (
        <p className="m-0 text-xs text-muted-foreground" data-testid="span-peers-empty">{t(q.isError ? 'span.peers.err' : 'span.peers.empty')}</p>
      ) : (
        <>
          <div className="flex h-14 items-end gap-px border-b border-[color:var(--border)]" role="img"
            aria-label={t('span.peers.aria').replace('{p}', String(hist.rankPct ?? '—'))}>
            {hist.counts.map((c, i) => {
              const lo = i > 0 ? hist.bins[i - 1] : 0;
              const own = i === hist.spanIdx;
              return (
                <div key={i} data-own={own ? '1' : undefined}
                  title={`${fmtNum(c)} span · ${fmtMs(lo)} – ${fmtMs(hist.bins[i])}${own ? ` · ${t('span.peers.this')}` : ''}`}
                  style={{
                    flex: 1, minWidth: 3,
                    height: `${Math.max(c > 0 || own ? 6 : 0, (c / max) * 100)}%`,
                    background: own ? 'var(--accent)' : 'color-mix(in srgb, var(--text3) 45%, transparent)',
                    outline: own ? '1px solid var(--accent)' : undefined,
                    borderRadius: '2px 2px 0 0',
                  }} />
              );
            })}
          </div>
          <div className="mt-0.5 flex justify-between font-mono text-[9px] text-muted-foreground">
            <span>{fmtMs(hist.bins.length > 1 ? hist.bins[0] : 0)}</span>
            <span>{fmtMs(hist.bins[hist.bins.length - 1])}</span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs text-muted-foreground">
            <span>
              {t('span.peers.summary')
                .replace('{d}', fmtMs(span.durationMs))
                .replace('{p}', String(hist.rankPct ?? '—'))
                .replace('{n}', fmtNum(hist.total))}
              {sampled && ` · ${t('span.peers.sampled')}`}
            </span>
            <Link to={href} data-testid="span-peers-link">{t('span.peers.open')}</Link>
          </div>
        </>
      )}
    </div>
  );
}
