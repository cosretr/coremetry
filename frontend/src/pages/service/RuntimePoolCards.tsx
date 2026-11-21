// RuntimePoolCards — follow-up 2026-11-21 (operatör onayı): HikariCP bağlantı
// havuzu kartları. hikaricp_connections_* serileri çoğu zaman `application` +
// `host_name` taşır, `service_name` TAŞIMAZ: servis süzgeci yerine servisin
// VARLIK pod'larıyla (sunucunun /runtime-signals podEntities dizini; pod adı
// = host_name) sorulur — ad eşlemesi yok. Profil sinyali `podScoped`.
// LAZY chunk (ServiceRuntimeSection yalnız profilde db_pool_* varken yükler).
// v0.10.2436 — kartlar RuntimePodChart (CorePanel); iki kart da pod başına
// (alma süresi pod'un kendi sum/count oranı), medyan + en kötü + eşik üstü.
import { useMemo } from 'react';
import { useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { panelMaxDataPoints } from '@/lib/chartStep';
import { RuntimePodChart, type RuntimeExtraLine } from './RuntimePodChart';
import type { PodSeries } from './runtimePodReduce';
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
    add('db_acquire_sum', true); add('db_acquire_count', true);
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
  const failed = queries.some(q => q.isError);
  const status: 'loading' | 'error' | 'ready' = failed ? 'error' : pending ? 'loading' : 'ready';
  const pts = (s: SpanMetricSeries) => s.points.map(p => ({ time: p.time, value: p.value }));
  // host_name yoksa (grup anahtarı boş) tek çizgi + "pod etiketi yok".
  const anon = (d: SpanMetricSeries[] | undefined) => !!d?.length && d.every(s => !(s.groupKey[0] ?? '').trim());
  const label = (s: SpanMetricSeries, fallback: string) => podLineLabel(s.groupKey, fallback);

  const active = data('db_pool_active');
  const poolPods: PodSeries[] = (active ?? []).map(s => ({ label: label(s, tr('rt.line.dbActive')), points: pts(s) }));
  const poolExtra: RuntimeExtraLine[] = [];
  const mx = data('db_pool_max')?.[0];
  if (mx) poolExtra.push({ label: tr('rt.line.dbMax'), color: 'var(--err)', dashed: true, points: pts(mx) });
  const pe = data('db_pool_pending')?.[0];
  if (pe) poolExtra.push({ label: tr('rt.line.dbPending'), color: 'var(--warn)', points: pts(pe) });
  const sums = data('db_acquire_sum') ?? [];
  const cnts = new Map((data('db_acquire_count') ?? []).map(c => [c.groupKey.join('\u0000'), c]));
  const acqPods: PodSeries[] = sums
    .map(s => ({ label: label(s, tr('rt.line.dbAcquire')), points: ratioMs(s, cnts.get(s.groupKey.join('\u0000'))) }))
    .filter(x => x.points.length > 0);
  const srcTip = (m: string[]) => `${m.join(', ')} · VM Micrometer (HikariCP) · host_name ∈ ${tr('rt.pods.title')}`;

  if (!pf || specs.length === 0) return null;
  return (
    <div className="rt-cards" data-testid="runtime-pool-cards">
      <RuntimePodChart testId="rt-card-dbpool" title={tr('rt.card.dbPool')} storageKey="rt-pool-dbpool" unit=""
        service={service} from={from} to={to} pods={poolPods} podless={anon(active)} extra={poolExtra}
        status={status} mode="line"
        source={{ label: 'Micrometer', tip: srcTip(specs.filter(s => s.key.startsWith('db_pool')).map(s => s.res.metric)) }} />
      {(acqPods.length > 0 || (status === 'loading' && sums.length === 0 && specs.some(s => s.key === 'db_acquire_sum'))) && (
        <RuntimePodChart testId="rt-card-dbacquire" title={tr('rt.card.dbAcquire')} storageKey="rt-pool-dbacquire" unit="ms"
          service={service} from={from} to={to} pods={acqPods} podless={anon(sums)}
          status={status} mode="line"
          source={{ label: 'Micrometer', tip: srcTip(['hikaricp_connections_acquire_seconds_{sum,count}']) }} />
      )}
    </div>
  );
}
