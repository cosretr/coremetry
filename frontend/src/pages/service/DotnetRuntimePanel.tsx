// DotnetRuntimePanel — 2026-11-21 (.NET runtime paritesi; operatör: "Aynı
// Elastic APM olduğu gibi"). LAZY chunk (ServiceRuntimeSection yalnız .NET
// serviste yükler).
//
// Metrik adları ELLE yazılmaz: sunucunun runtime profili
// (/api/services/{name}/runtime-signals → runtimesig.Resolve) katalogda
// bulunan yazımı verir — eski OpenTelemetry.Instrumentation.Runtime
// (process.runtime.dotnet.*) ya da .NET 9 semconv (dotnet.*), VM'de Prom adı
// (…_count_total), CH'de noktalı ad. Kart, profilde sinyali olmayan yerde
// çizilmez (boş .NET kartı duvarı yok).
//
// Gecikme korelasyonu: GC payı ve thread-pool kartlarına servis p99'u sağ
// eksende kesikli bindirilir (span MV, /api/spans/metric-batch); tüm kartlar
// RuntimeCharts ile aynı syncKey (runtime:<servis>) → ortak imleç.
import { useMemo } from 'react';
import { useQueries, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { panelMaxDataPoints } from '@/lib/chartStep';
import { useServiceDeploys } from '@/lib/queries';
import { Spinner } from '@/components/Spinner';
import { TimeSeriesPanel, type TSSeries } from '@/components/viz/TimeSeriesPanel';
import type { RuntimeResolvedSignal, RuntimeSignal, SpanMetricSeries } from '@/lib/types';
import { podLineLabel, podClusterOf, withClusterPrefix } from './runtimePodLabel';
import { smoothPoints } from './runtimeSmooth';
import { useRuntimeProfile } from './runtimeFamily';
import { podDetailPath } from './podDetailPath';
import { Link } from 'react-router';

const MB = 1 / (1024 * 1024);
const POD_GROUP = 'resource.k8s.pod.name,resource.host.name,resource.service.instance.id,cluster';
const FANOUT_MAX = 40;

type Group = 'pod' | 'gen' | 'none';
interface LineSpec { signal: string; labelKey: string; scale: number; group: Group; unit: string; color?: string; dash?: number[] }
interface CardSpec { key: string; titleKey: string; lines: LineSpec[]; mode: 'area' | 'line'; latency?: boolean }

// Kart sırası Elastic APM .NET görünümünün sırası: GC → bellek → thread
// pool → çekişme → exception → working set.
export const DOTNET_CARDS: CardSpec[] = [
  { key: 'gc', titleKey: 'rt.card.gcShare', mode: 'line', latency: true, lines: [
    { signal: 'gc_time', labelKey: 'rt.line.gcShare', scale: 100, group: 'pod', unit: '%' },
  ] },
  { key: 'gcgen', titleKey: 'rt.card.gcCollections', mode: 'line', lines: [
    { signal: 'gc_collections', labelKey: 'rt.line.collections', scale: 60, group: 'gen', unit: '/min' },
  ] },
  { key: 'heap', titleKey: 'rt.card.heapGen', mode: 'area', lines: [
    { signal: 'heap_used', labelKey: 'rt.line.heap', scale: MB, group: 'gen', unit: 'MB' },
    { signal: 'heap_committed', labelKey: 'rt.line.committed', scale: MB, group: 'none', unit: 'MB', color: 'var(--err)', dash: [5, 4] },
  ] },
  { key: 'alloc', titleKey: 'rt.card.alloc', mode: 'area', lines: [
    { signal: 'alloc_rate', labelKey: 'rt.line.alloc', scale: MB, group: 'none', unit: 'MB/s', color: 'var(--accent)' },
  ] },
  { key: 'pool', titleKey: 'rt.card.pool', mode: 'line', latency: true, lines: [
    { signal: 'pool_queue', labelKey: 'rt.line.queue', scale: 1, group: 'none', unit: '', color: 'var(--warn)' },
    { signal: 'threads', labelKey: 'rt.line.threads', scale: 1, group: 'none', unit: '', color: 'var(--teal)' },
  ] },
  { key: 'lock', titleKey: 'rt.card.lock', mode: 'line', lines: [
    { signal: 'lock_contention', labelKey: 'rt.line.lock', scale: 1, group: 'none', unit: '/s', color: 'var(--purple)' },
  ] },
  { key: 'exc', titleKey: 'rt.card.exceptions', mode: 'line', lines: [
    { signal: 'exceptions', labelKey: 'rt.line.exceptions', scale: 1, group: 'none', unit: '/s', color: 'var(--err)' },
  ] },
  { key: 'ws', titleKey: 'rt.card.workingSet', mode: 'area', lines: [
    { signal: 'working_set', labelKey: 'rt.line.workingSet', scale: MB, group: 'pod', unit: 'MB' },
  ] },
];

// CPython (2026-11-21) — kompakt görünüm: CPU, RSS, GC (nesil), thread, bağlam değişimi.
export const PYTHON_CARDS: CardSpec[] = [
  { key: 'cpu', titleKey: 'rt.card.cpu', mode: 'line', latency: true, lines: [
    { signal: 'cpu_util', labelKey: 'rt.line.cpu', scale: 100, group: 'pod', unit: '%' },
  ] },
  { key: 'rss', titleKey: 'rt.card.rss', mode: 'area', lines: [
    { signal: 'memory_rss', labelKey: 'rt.line.rss', scale: MB, group: 'pod', unit: 'MB' },
  ] },
  { key: 'gcgen', titleKey: 'rt.card.gcCollections', mode: 'line', lines: [
    { signal: 'gc_collections', labelKey: 'rt.line.collections', scale: 60, group: 'gen', unit: '/min' },
  ] },
  { key: 'threads', titleKey: 'rt.card.threads', mode: 'line', lines: [
    { signal: 'threads', labelKey: 'rt.line.threads', scale: 1, group: 'pod', unit: '' },
  ] },
  { key: 'ctx', titleKey: 'rt.card.ctxSwitches', mode: 'line', lines: [
    { signal: 'ctx_switches', labelKey: 'rt.line.ctxSwitches', scale: 1, group: 'none', unit: '/s', color: 'var(--purple)' },
  ] },
];

export type ProfilePanelRuntime = 'dotnet' | 'python';
const CARDS_BY_RUNTIME: Record<ProfilePanelRuntime, CardSpec[]> = { dotnet: DOTNET_CARDS, python: PYTHON_CARDS };

/** SAF — profilde sinyali olan kartlar ve onların çözülmüş çizgileri. */
export function plannedCards(resolved: RuntimeResolvedSignal[], runtime: ProfilePanelRuntime = 'dotnet'): { card: CardSpec; lines: { line: LineSpec; res: RuntimeResolvedSignal }[] }[] {
  const by = new Map(resolved.map(r => [r.signal, r]));
  return CARDS_BY_RUNTIME[runtime].map(card => ({
    card,
    lines: card.lines.flatMap(line => {
      const res = by.get(line.signal);
      if (!res) return [];
      // Nesil etiketi çözülemediyse kırılım yok, tek toplam çizgi.
      return [{ line: line.group === 'gen' && !res.genLabel ? { ...line, group: 'none' as Group } : line, res }];
    }),
  })).filter(c => c.lines.length > 0);
}

const LEVEL_BADGE: Record<RuntimeSignal['level'], string> = { critical: 'b-err', warning: 'b-warn', info: 'b-gray' };
const fmt = (v: number) => (Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(1));

/** Akıllı sinyal satırları (sunucu hükmü; metin i18n şablonundan). */
export function RuntimeSignalList({ signals, mode }: { signals: RuntimeSignal[]; mode: string }) {
  const tr = useT();
  return (
    <div className="card" data-testid="rt-signals">
      <div className="ov-card-h">
        <h3>{tr('rt.sig.title')}</h3>
        {mode !== 'on' && <span className="badge b-gray" title={tr('rt.sig.modeHint')}>{tr(mode === 'off' ? 'rt.sig.off' : 'rt.sig.shadow')}</span>}
      </div>
      <div className="ov-card-b">
        {signals.length === 0 ? (
          <div className="kc-line">{tr(mode === 'off' ? 'rt.sig.disabled' : 'rt.sig.none')}</div>
        ) : signals.map(s => (
          <div key={s.check} className="kc-line" data-testid={`rt-sig-${s.check}`}>
            <span className={`badge ${LEVEL_BADGE[s.level] ?? 'b-gray'}`}>{tr(`rt.sig.level.${s.level}`)}</span>{' '}
            <strong>{tr(`rt.sig.${s.check}`)}</strong>{' — '}
            <span className="mono">
              {tr('rt.sig.detail')
                .replace('{v}', `${fmt(s.value)}${s.unit === 'items' ? '' : s.unit}`)
                .replace('{b}', `${fmt(s.baseline)}${s.unit === 'items' ? '' : s.unit}`)
                .replace('{t}', `${fmt(s.threshold)}${s.unit === 'items' ? '' : s.unit}`)}
            </span>
            {s.check === 'threadpool-starvation' && s.extra?.completedRatio != null && (
              <> · {tr('rt.sig.completed').replace('{r}', `×${s.extra.completedRatio.toFixed(2)}`)}</>
            )}
            {s.pod && <> · {tr('rt.sig.pod').replace('{pod}', s.pod).replace('{n}', String(s.pods))}</>}
            {s.podEntity && (
              <> · <Link to={podDetailPath({ pod: s.podEntity.pod, cluster: s.podEntity.clusterId, namespace: s.podEntity.namespace, service: s.service, from: 'infra' })}
                title={s.podEntity.id}>{tr('rt.sig.podLink')}</Link></>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

export default function DotnetRuntimePanel({ service, from, to, onZoom, onZoomReset, runtime = 'dotnet' }: {
  service: string;
  /** 2026-11-21 — aynı panel CPython kartlarıyla da (profil çözümü ortak). */
  runtime?: ProfilePanelRuntime;
  from: number;
  to: number;
  onZoom?: (fromSec: number, toSec: number) => void;
  onZoomReset?: () => void;
}) {
  const tr = useT();
  const profQ = useRuntimeProfile(service);
  const resolved = useMemo(() => profQ.data?.profile?.signals ?? [], [profQ.data]);
  const planned = useMemo(() => plannedCards(resolved, runtime), [resolved, runtime]);
  const specs = useMemo(() => planned.flatMap(p => p.lines.map(l => ({ card: p.card.key, ...l }))), [planned]);

  const queries = useQueries({
    queries: specs.map(({ line, res }) => ({
      queryKey: ['svc-runtime-metric', service, res.metric, line.group, res.genLabel ?? '', res.agg, from, to],
      queryFn: () => api.metricQuery({
        name: res.metric, service, agg: res.agg,
        filters: res.filterKey ? JSON.stringify([{ k: res.filterKey, op: '=', v: [res.filterValue ?? ''] }]) : undefined,
        groupBy: line.group === 'pod' ? POD_GROUP : line.group === 'gen' ? res.genLabel : undefined,
        from, to, step: 0, maxDataPoints: panelMaxDataPoints(1),
      }).then(r => r ?? []),
      staleTime: 60_000,
    })),
  });

  const wantLatency = planned.some(p => p.card.latency);
  const latQ = useQuery({
    queryKey: ['svc-runtime-latency', service, from, to],
    queryFn: () => api.spanMetricBatch({
      from, to, maxDataPoints: panelMaxDataPoints(1),
      dsl: `service.name = "${service.replace(/"/g, '\\"')}"`,
      aggs: [{ name: 'p99', agg: 'p99', field: 'duration_ms' }],
    }),
    enabled: !!service && wantLatency,
    staleTime: 60_000,
  });
  const latSeries = useMemo<TSSeries | null>(() => {
    const s = latQ.data?.series?.p99?.[0];
    if (!s) return null;
    return { label: tr('rt.line.p99'), unit: 'ms', axis: 'right', dash: [4, 3], color: 'var(--text3)', width: 1,
      points: s.points.map(p => ({ time: p.time, value: p.value })) };
  }, [latQ.data, tr]);

  const deploysQ = useServiceDeploys(service, from, to);
  const deployNs = useMemo(() => (deploysQ.data?.length ? deploysQ.data.map(x => x.timeUnixNs) : undefined), [deploysQ.data]);

  const cards = useMemo(() => planned.map(({ card }) => {
    const series: TSSeries[] = [];
    let pending = false, failed = false;
    specs.forEach((sp, qi) => {
      if (sp.card !== card.key) return;
      const q = queries[qi];
      if (q?.isPending) pending = true;
      if (q?.isError) failed = true;
      const data = (q?.data ?? []) as SpanMetricSeries[];
      const scale = sp.res.scale * sp.line.scale;
      const pts = (s: SpanMetricSeries) => s.points.map(p => ({ time: p.time, value: p.value == null ? null : p.value * scale }));
      if (sp.line.group === 'pod') {
        const multi = new Set(data.map(s => podClusterOf(s.groupKey)).filter(Boolean)).size > 1;
        data.slice(0, FANOUT_MAX).forEach(s => series.push({
          label: withClusterPrefix(podLineLabel(s.groupKey, sp.line.signal), podClusterOf(s.groupKey), multi),
          unit: sp.line.unit, points: smoothPoints(pts(s)),
        }));
      } else if (sp.line.group === 'gen') {
        data.forEach(s => series.push({ label: s.groupKey[0] || tr(sp.line.labelKey), unit: sp.line.unit, points: pts(s) }));
      } else if (data[0]) {
        series.push({ label: tr(sp.line.labelKey), unit: sp.line.unit, color: sp.line.color, dash: sp.line.dash, points: pts(data[0]) });
      }
    });
    if (card.latency && latSeries && series.length > 0) series.push(latSeries);
    return { card, series, status: failed ? 'error' : pending ? 'loading' : 'ready' };
  }), [planned, specs, queries, latSeries, tr]);

  if (profQ.isPending) return <div className="rt-slot"><Spinner /></div>;
  if (profQ.isError || !profQ.data) return <div className="kc-line">{tr('rt.err')}</div>;
  if (planned.length === 0) return <div className="kc-line">{tr(runtime === 'python' ? 'rt.python.none' : 'rt.dotnet.none')}</div>;

  return (
    <div className="rt-cards" data-testid={`${runtime}-runtime`}>
      <RuntimeSignalList signals={profQ.data.signals ?? []} mode={profQ.data.mode} />
      {cards.map(({ card, series, status }) => (
        <div key={card.key} className="card" data-testid={`rt-card-${card.key}`}>
          <div className="ov-card-h"><h3>{tr(card.titleKey)}</h3></div>
          <div className="ov-card-b rt-card-b">
            {status === 'loading' && series.length === 0 ? (
              <div className="rt-slot"><Spinner /></div>
            ) : status === 'error' && series.length === 0 ? (
              <div className="rt-slot rt-slot--err">{tr('rt.err')}</div>
            ) : series.length === 0 ? (
              <div className="rt-slot rt-slot--empty">{tr('rt.noData')}</div>
            ) : (
              <TimeSeriesPanel series={series} height={220} mode={card.mode}
                syncKey={`runtime:${service}`} deploys={deployNs}
                onZoom={onZoom} onZoomReset={onZoomReset} smooth />
            )}
          </div>
        </div>
      ))}
    </div>
  );
}
