// RuntimePoolCards — follow-up 2026-11-21 (operatör onayı): HikariCP bağlantı
// havuzu kartları. hikaricp_connections_* serileri çoğu zaman `application` +
// `host_name` taşır, `service_name` TAŞIMAZ: servis süzgeci yerine servisin
// VARLIK pod'larıyla (sunucunun /runtime-signals podEntities dizini; pod adı
// = host_name) sorulur — ad eşlemesi yok. Profil sinyali `podScoped`.
// LAZY chunk (ServiceRuntimeSection yalnız profilde db_pool_* varken yükler).
import { useMemo } from 'react';
import { useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { panelMaxDataPoints } from '@/lib/chartStep';
import { Spinner } from '@/components/Spinner';
import { TimeSeriesPanel, type TSSeries } from '@/components/viz/TimeSeriesPanel';
import type { FilterExpr, RuntimeResolvedSignal, SpanMetricSeries } from '@/lib/types';
import { useRuntimeProfile } from './runtimeFamily';
import { podLineLabel } from './runtimePodLabel';

const HOST = 'resource.host.name';
const POD_MAX = 50; // Go runtimesig.HikariPodsMax

/** SAF — pod listesi → host_name süzgeci (sıralı, ≤ 50); boş → null. */
export function podScopeFilter(pods: string[]): FilterExpr | null {
  const ps = pods.filter(Boolean).sort().slice(0, POD_MAX);
  return ps.length ? { k: HOST, op: 'IN', v: ps } : null;
}

/** SAF — iki rate serisi (sum/count) → ortalama ms; zaman damgasıyla eşleşir. */
export function ratioMs(sum: SpanMetricSeries | undefined, cnt: SpanMetricSeries | undefined): { time: number; value: number | null }[] {
  if (!sum || !cnt) return [];
  const c = new Map(cnt.points.map(p => [p.time, p.value]));
  return sum.points.map(p => {
    const n = c.get(p.time);
    return { time: p.time, value: p.value != null && n ? (p.value / n) * 1000 : null };
  });
}

interface Q { key: string; res: RuntimeResolvedSignal; byPod: boolean }

export default function RuntimePoolCards({ service, from, to, extraFilters }: {
  service: string; from: number; to: number; extraFilters?: FilterExpr[];
}) {
  const tr = useT();
  const profQ = useRuntimeProfile(service);
  const pf = useMemo(() => podScopeFilter(Object.keys(profQ.data?.podEntities ?? {})), [profQ.data]);
  const specs = useMemo<Q[]>(() => {
    const by = new Map((profQ.data?.profile?.signals ?? []).filter(s => s.podScoped).map(s => [s.signal, s]));
    const out: Q[] = [];
    const add = (sig: string, byPod: boolean) => { const r = by.get(sig); if (r) out.push({ key: sig, res: r, byPod }); };
    add('db_pool_active', true); add('db_pool_max', false); add('db_pool_pending', false);
    add('db_acquire_sum', false); add('db_acquire_count', false);
    return out;
  }, [profQ.data]);
  const filters = useMemo(() => (pf ? JSON.stringify([pf, ...(extraFilters ?? [])]) : ''), [pf, extraFilters]);
  const queries = useQueries({
    queries: specs.map(sp => ({
      queryKey: ['svc-runtime-pool', service, sp.res.metric, sp.byPod, filters, from, to],
      queryFn: () => api.metricQuery({
        name: sp.res.metric, agg: sp.res.agg, filters,
        groupBy: sp.byPod ? HOST : undefined,
        from, to, step: 0, maxDataPoints: panelMaxDataPoints(1),
      }).then(r => r ?? []),
      enabled: !!filters,
      staleTime: 60_000,
    })),
  });
  const data = (k: string) => {
    const i = specs.findIndex(s => s.key === k);
    return i < 0 ? undefined : ((queries[i]?.data ?? []) as SpanMetricSeries[]);
  };
  const pending = queries.some(q => q.isPending);
  const pts = (s: SpanMetricSeries) => s.points.map(p => ({ time: p.time, value: p.value }));

  const pool: TSSeries[] = [];
  (data('db_pool_active') ?? []).slice(0, 40).forEach(s => pool.push({ label: podLineLabel(s.groupKey, tr('rt.line.dbActive')), unit: '', points: pts(s) }));
  const mx = data('db_pool_max')?.[0];
  if (mx) pool.push({ label: tr('rt.line.dbMax'), unit: '', color: 'var(--err)', dash: [5, 4], points: pts(mx) });
  const pe = data('db_pool_pending')?.[0];
  if (pe) pool.push({ label: tr('rt.line.dbPending'), unit: '', color: 'var(--warn)', points: pts(pe) });
  const acq = ratioMs(data('db_acquire_sum')?.[0], data('db_acquire_count')?.[0]);
  const srcTip = (m: string[]) => `${m.join(', ')} · VM Micrometer (HikariCP) · host_name ∈ ${tr('rt.pods.title')}`;

  if (!pf || specs.length === 0) return null;
  const card = (key: string, title: string, tip: string, series: TSSeries[]) => (
    <div key={key} className="card" data-testid={`rt-card-${key}`}>
      <div className="ov-card-h"><h3 title={tip}>{title}</h3><span className="badge b-gray" title={tip}>Micrometer</span></div>
      <div className="ov-card-b rt-card-b">
        {pending && series.length === 0 ? <div className="rt-slot"><Spinner /></div>
          : series.length === 0 ? <div className="rt-slot rt-slot--empty">{tr('rt.noData')}</div>
            : <TimeSeriesPanel series={series} height={220} mode="line" syncKey={`runtime:${service}`} smooth />}
      </div>
    </div>
  );
  return (
    <div className="rt-cards" data-testid="runtime-pool-cards">
      {card('dbpool', tr('rt.card.dbPool'), srcTip(specs.filter(s => s.key.startsWith('db_pool')).map(s => s.res.metric)), pool)}
      {acq.length > 0 && card('dbacquire', tr('rt.card.dbAcquire'), srcTip(['hikaricp_connections_acquire_seconds_{sum,count}']),
        [{ label: tr('rt.line.dbAcquire'), unit: 'ms', color: 'var(--purple)', points: acq }])}
    </div>
  );
}
