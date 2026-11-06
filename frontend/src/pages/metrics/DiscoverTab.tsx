// pages/metrics/DiscoverTab — v0.10.1865 (Metrics Explorer Faz 2 dilim 2.5;
// mockup görünüm 3 "Keşif / kardinalite"; operatör S3 + S10).
//
// Üç bölge:
//   1. Özet satırı + "Yüksek kardinalite." uyarısı (tek metrik ≥ %30).
//   2. Metrikler (seri sayısına göre, ilk 50): ad araması SUNUCUDA
//      (kardinalite `match[]` + ad zarfı), tür süzgeci; satır → Sorgu
//      sekmesinde açılır (S3 — Explore v2 P5 aşıldı; gerçek bağlantı, yeni
//      sekme çalışır).
//   3. Etikete göre gez (etiket → değerler → "bu değerle süz") + en pahalı
//      sorgular (admin / editor; açılınca okunur).
// Herkes okur (S10): kardinalite paylaşılan 10 dk önbellekten. ClickHouse'ta
// kardinalite uçları {supported:false} → neden yazılır, metrik listesi ad
// aramasıyla sürer, CH kardinalitesi için Sistem › Cardinality bağlantısı.
// Polling yok; staleTime = sunucu TTL'i.
import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { X } from 'lucide-react';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { KindBadge } from '@/components/coremetry/metrics';
import { api } from '@/lib/api';
import { useDebouncedValue } from '@/lib/perf/useDebouncedValue';
import type { MetricsCapabilities, MetricsTSDBEntry, MetricsTopQuery } from '@/lib/types';
import {
  applyDiscover, decodeDiscover, fmtCount, fmtShare, highCardinality, labelRows, metricRows, openInExplorerSearch, searchMatch,
  type DiscoverKind, type DiscoverLabelRow, type DiscoverMetricRow,
} from '@/lib/metricsExplorer/discover';

const CARD_TTL = 10 * 60_000;
const TOPN = 50;

const KIND_OPTS: { value: DiscoverKind; label: string }[] = [
  { value: 'all', label: 'Hepsi' }, { value: 'counter', label: 'Sayaç' }, { value: 'gauge', label: 'Gösterge' }, { value: 'histogram', label: 'Histogram' },
];

const METRIC_COLS: ColumnDef<DiscoverMetricRow>[] = [
  { id: 'name', label: 'Metrik', width: 340, minWidth: 180, priority: 1, sortValue: r => r.name },
  { id: 'kind', label: 'Tür', width: 130, minWidth: 90, priority: 2, sortValue: r => r.kind },
  { id: 'unit', label: 'Birim', width: 70, minWidth: 56, priority: 3, sortValue: r => r.unit },
  { id: 'series', label: 'Seri', width: 100, minWidth: 80, numeric: true, priority: 1, sortValue: r => r.series ?? -1 },
  { id: 'share', label: 'Pay', width: 120, minWidth: 90, numeric: true, priority: 2, sortValue: r => r.share ?? -1 },
  { id: 'help', label: 'Açıklama', width: 320, minWidth: 140, flex: true, priority: 3, sortValue: r => r.help },
];
const LABEL_COLS: ColumnDef<DiscoverLabelRow>[] = [
  { id: 'name', label: 'Etiket', width: 200, minWidth: 120, flex: true, priority: 1, sortValue: r => r.name },
  { id: 'values', label: 'Değer', width: 90, minWidth: 70, numeric: true, priority: 1, sortValue: r => r.values ?? -1 },
  { id: 'series', label: 'Seri', width: 110, minWidth: 80, numeric: true, priority: 2, sortValue: r => r.series ?? -1 },
];
const VALUE_COLS: ColumnDef<MetricsTSDBEntry>[] = [
  { id: 'name', label: 'Değer', width: 220, minWidth: 120, flex: true, priority: 1, sortValue: r => r.name },
  { id: 'value', label: 'Seri', width: 110, minWidth: 80, numeric: true, priority: 1, sortValue: r => r.value },
  { id: 'act', label: '', width: 120, minWidth: 100, priority: 1, kind: 'actions' },
];
const TOPQ_COLS: ColumnDef<MetricsTopQuery>[] = [
  { id: 'query', label: 'Sorgu', width: 420, minWidth: 200, flex: true, priority: 1, sortValue: r => r.query },
  { id: 'count', label: 'Çağrı', width: 80, minWidth: 64, numeric: true, priority: 1, sortValue: r => r.count ?? 0 },
  { id: 'avg', label: 'Ort süre', width: 90, minWidth: 72, numeric: true, priority: 1, sortValue: r => r.avgDurationSeconds ?? 0 },
];

export interface DiscoverTabProps {
  cluster: string;
  caps?: MetricsCapabilities;
  canEdit: boolean;
}

export function DiscoverTab({ cluster, caps, canEdit }: DiscoverTabProps) {
  const [sp, setSearchParams] = useSearchParams();
  const p = decodeDiscover(sp);
  const set = (patch: Parameters<typeof applyDiscover>[1]) => setSearchParams(prev => applyDiscover(prev, patch), { replace: true });
  const [search, setSearch] = useState(p.q);
  useEffect(() => { setSearch(p.q); }, [p.q]);
  const dq = useDebouncedValue(search.trim(), 300);
  useEffect(() => { if (dq !== p.q) set({ q: dq }); }, [dq]); // eslint-disable-line react-hooks/exhaustive-deps -- yalnız debounce sonucu yazılır
  const clusterArg = cluster || undefined;
  const match = searchMatch(p.q, p.match);

  const card = useQuery({
    queryKey: ['mx-disc-card', match ?? '', clusterArg ?? ''],
    queryFn: ({ signal }) => api.metricsDiscoverCardinality({ topN: TOPN, match, cluster: clusterArg }, signal),
    staleTime: CARD_TTL,
  });
  const names = useQuery({
    queryKey: ['mx-disc-names', p.q, p.service, clusterArg ?? ''],
    queryFn: ({ signal }) => api.metricNamesSearch(p.service, p.q || undefined, TOPN, 0, undefined, { cluster: clusterArg, signal }),
    staleTime: 60_000,
  });
  const focus = useQuery({
    queryKey: ['mx-disc-focus', p.label, p.match, clusterArg ?? ''],
    queryFn: ({ signal }) => api.metricsDiscoverCardinality({ topN: TOPN, focusLabel: p.label, match: p.match || undefined, cluster: clusterArg }, signal),
    enabled: !!p.label,
    staleTime: CARD_TTL,
  });
  const [topOpen, setTopOpen] = useState(false);
  const topq = useQuery({
    queryKey: ['mx-disc-topq'],
    queryFn: ({ signal }) => api.metricsDiscoverTopQueries({ topN: 10 }, signal),
    enabled: topOpen && canEdit,
    staleTime: 30_000,
  });

  const cd = card.data;
  const stats = cd && cd.supported ? cd.stats : undefined;
  const unsupported = cd && !cd.supported ? cd.reason : null;
  const rows = metricRows(stats, names.data?.names ?? [], p.kind);
  const hc = !p.q && !p.match ? highCardinality(stats) : null;
  const labels = labelRows(stats);
  const fd = focus.data;
  const values = fd && fd.supported ? fd.stats.seriesCountByFocusLabelValue : [];
  const source = caps?.source === 'clickhouse' ? 'ClickHouse' : 'VictoriaMetrics';
  const tq = topq.data && topq.data.supported ? (topq.data.queries.topByAvgDuration.length ? topq.data.queries.topByAvgDuration : topq.data.queries.topByCount) : [];

  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm">
        {stats ? (
          <>
            <b className="tabular-nums">{fmtCount(stats.totalSeries)}</b> aktif seri · <b className="tabular-nums">{fmtCount(names.data?.total)}</b> metrik adı ·{' '}
            <b className="tabular-nums">{fmtCount(stats.totalLabelValuePairs)}</b> etiket-değer çifti
            <span className="text-muted-foreground"> · {cd && cd.supported ? cd.date : 'bugün'} · 10 dk önbellek · Kaynak: {source}{cluster ? ` · kapsam: ${cluster}` : ''}</span>
          </>
        ) : unsupported ? (
          <span className="text-muted-foreground">Kardinalite yok: {unsupported}. Metrik listesi ad aramasıyla sürer{caps?.source === 'clickhouse' ? <>; ClickHouse kardinalitesi için <Link className="text-[var(--accent2)] hover:underline" to="/system/cardinality">Sistem › Cardinality</Link></> : null}.</span>
        ) : card.isError ? (
          <span className="text-status-critical">Kardinalite okunamadı: {card.error instanceof Error ? card.error.message : String(card.error)}</span>
        ) : <span className="text-muted-foreground">Kardinalite yükleniyor…</span>}
      </p>
      {hc && (
        <div role="status" className="flex flex-wrap items-center gap-2 rounded-md border border-status-warn-border bg-status-warn-bg px-3 py-1.5 text-xs">
          <b>Yüksek kardinalite.</b>
          <span><span className="font-mono">{hc.metric}</span> tek başına {fmtCount(hc.series)} seri (toplamın {fmtShare(hc.share)}'i).</span>
          <span className="flex-1" />
          <Button type="button" variant="link" size="xs" className="h-auto px-0"
            onClick={() => set({ match: `{__name__="${hc.metric}"}`, label: '' })}>Etiket dağılımını aç</Button>
        </div>
      )}

      <div className="grid gap-3 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <div className="flex min-w-0 flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <Input value={search} onChange={e => setSearch(e.target.value)} placeholder="Metrik ara (sunucu araması)" aria-label="Metrik ara" className="h-8 w-64" />
            <SegmentedControl<DiscoverKind> size="sm" aria-label="Tür" value={p.kind} onChange={kind => set({ kind })} options={KIND_OPTS} />
            {p.service && (
              <Badge variant="outline" className="gap-1">servis: {p.service}
                <Button type="button" variant="ghost" size="icon-xs" className="size-4" aria-label="Servis süzgecini kaldır" onClick={() => set({ service: '' })}><X aria-hidden="true" /></Button>
              </Badge>
            )}
            {p.match && (
              <Badge variant="outline" className="gap-1 font-mono">{p.match}
                <Button type="button" variant="ghost" size="icon-xs" className="size-4" aria-label="Seri süzgecini kaldır" onClick={() => set({ match: '' })}><X aria-hidden="true" /></Button>
              </Badge>
            )}
            <span className="flex-1" />
            <span className="text-xs text-muted-foreground">{stats ? `Seri sayısına göre · ilk ${TOPN}` : `Ada göre · ilk ${TOPN}`}</span>
          </div>
          <DataTable<DiscoverMetricRow> storageKey="metrics-discover-metrics" columns={METRIC_COLS} rows={rows} ariaLabel="Metrikler, seri sayısına göre"
            getRowKey={r => r.name}
            getRowHref={r => `/metrics${openInExplorerSearch(sp, r)}`}
            sort={{ initial: stats ? { id: 'series', dir: 'desc' } : { id: 'name', dir: 'asc' } }}
            state={card.isLoading || names.isLoading ? { kind: 'loading' }
              : names.isError && !stats ? { kind: 'error', onRetry: () => void names.refetch(), message: 'Metrik listesi okunamadı' }
                : p.q || p.kind !== 'all' || p.match || p.service ? { kind: 'no-match', message: 'Eşleşen metrik yok — aramayı ya da süzgeçleri gevşetin.' }
                  : { kind: 'empty', message: 'Metrik yok — uygulamaların metrik gönderdiğini doğrulayın.' }}
            renderCell={(r, c) => {
              switch (c.id) {
                case 'name': return <span className="font-mono" title={r.name}>{r.name}</span>;
                case 'kind': return <KindBadge kind={r.kind} estimated={r.estimated} />;
                case 'unit': return r.unit || '—';
                case 'series': return fmtCount(r.series);
                case 'share': return r.share === undefined ? '—' : (
                  <span className="inline-flex items-center gap-1.5">
                    <meter min={0} max={1} value={r.share} className="h-1.5 w-12" aria-hidden="true" />{fmtShare(r.share)}
                  </span>
                );
                case 'help': return r.help ? <span title={r.help}>{r.help}</span> : <span className="text-muted-foreground">Açıklama yok</span>;
              }
              return null;
            }} />
        </div>

        <div className="flex min-w-0 flex-col gap-2">
          <DataTable<DiscoverLabelRow> storageKey="metrics-discover-labels" columns={LABEL_COLS} rows={labels} ariaLabel="Etikete göre gez"
            getRowKey={r => r.name}
            openRow={{ isOpen: r => r.name === p.label, onActivate: r => set({ label: r.name === p.label ? '' : r.name }) }}
            sort={{ initial: { id: 'series', dir: 'desc' } }}
            state={card.isLoading ? { kind: 'loading' } : { kind: 'empty', message: unsupported ? `Etiket gezgini yok: ${unsupported}` : 'Etiket yok' }}
            renderCell={(r, c) => {
              switch (c.id) {
                case 'name': return <span className="inline-flex items-center gap-1.5 font-mono">{r.name}{r.high && <Badge variant="outline" className="border-status-warn-border text-status-warn">yüksek</Badge>}</span>;
                case 'values': return fmtCount(r.values);
                case 'series': return fmtCount(r.series);
              }
              return null;
            }} />
          {p.label && (
            <section aria-label={`${p.label} değerleri`} className="flex flex-col gap-1.5">
              <h3 className="text-sm font-semibold"><span className="font-mono">{p.label}</span> değerleri <span className="font-normal text-muted-foreground">seri sayısına göre</span></h3>
              <DataTable<MetricsTSDBEntry> storageKey="metrics-discover-values" columns={VALUE_COLS} rows={values} ariaLabel={`${p.label} değerleri`}
                getRowKey={r => r.name}
                sort={{ initial: { id: 'value', dir: 'desc' } }}
                state={focus.isLoading ? { kind: 'loading' } : focus.isError ? { kind: 'error', onRetry: () => void focus.refetch(), message: 'Değerler okunamadı' } : { kind: 'empty', message: 'Değer yok' }}
                renderCell={(r, c) => {
                  switch (c.id) {
                    case 'name': return <span className="font-mono" title={r.name}>{r.name}</span>;
                    case 'value': return fmtCount(r.value);
                    case 'act': return (
                      <Button type="button" variant="link" size="xs" className="h-auto px-0"
                        onClick={() => set({ match: `{${p.label}=${JSON.stringify(r.name)}}` })}>Bu değerle süz</Button>
                    );
                  }
                  return null;
                }} />
            </section>
          )}
          {canEdit ? (
            <details className="rounded-md border px-3 py-2 text-sm" onToggle={e => setTopOpen((e.target as HTMLDetailsElement).open)}>
              <summary className="cursor-pointer">En pahalı sorgular <span className="text-muted-foreground">(admin / editor)</span></summary>
              <div className="mt-2">
                {topq.data && !topq.data.supported ? <p className="text-xs text-muted-foreground">Yok: {topq.data.reason}</p> : (
                  <DataTable<MetricsTopQuery> storageKey="metrics-discover-topq" columns={TOPQ_COLS} rows={tq} ariaLabel="En pahalı sorgular"
                    getRowKey={(r, i) => `${i}:${r.query}`}
                    sort={{ initial: { id: 'avg', dir: 'desc' } }}
                    state={topq.isLoading ? { kind: 'loading' } : topq.isError ? { kind: 'error', onRetry: () => void topq.refetch(), message: 'Okunamadı' } : { kind: 'empty', message: 'Kayıt yok' }}
                    renderCell={(r, c) => {
                      switch (c.id) {
                        case 'query': return <span className="font-mono" title={r.query}>{r.query}</span>;
                        case 'count': return fmtCount(r.count);
                        case 'avg': return r.avgDurationSeconds !== undefined ? `${Math.round(r.avgDurationSeconds * 1000)} ms` : '—';
                      }
                      return null;
                    }} />
                )}
              </div>
            </details>
          ) : (
            <p className="text-xs text-muted-foreground">En pahalı sorgular admin ve editor rolüne açık (başka operatörlerin sorgu metnini gösterir).</p>
          )}
        </div>
      </div>
    </div>
  );
}
