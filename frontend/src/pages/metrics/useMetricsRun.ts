// pages/metrics/useMetricsRun — v0.10.1863 (Metrics Explorer Faz 2 dilim 2.3).
//
// Çalıştırma planının (lib/metricsExplorer/run.ts) her öğesi BİR
// /api/metrics/promql isteği (meta=1 zarfı; sunucu top-N `limit`+`rank`,
// cluster kapsamı `cluster` → VM extra_filters[]). Anahtar tüm girdileri
// taşır; aralık değişince eski istek iptal (signal), önceki veri soluk kalır
// (placeholderData). Polling YOK: aralık global yenileme saatiyle kayar
// (lib/autoRefresh — /metrics allowlist'te).
//
// Thanos (kod kipi, editor+): /api/promql/query_range konsolu — Prom matrisi
// SpanMetricSeries'e çevrilir; meta yok.
import { keepPreviousData, useQueries, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { promMatrixToSeries } from '@/lib/promMatrix';
import type { MetricResultMeta, SpanMetricSeries } from '@/lib/types';
import { parseRunError, type RunError, type RunItem, type RunPlan } from '@/lib/metricsExplorer/run';

export interface RunResult {
  item: RunItem;
  /** undefined = henüz cevap yok. */
  series?: SpanMetricSeries[];
  /** Sunucu meta zarfı (Thanos konsolunda yok). */
  meta?: MetricResultMeta;
  /** Thanos gibi meta taşımayan kaynakların adı. */
  sourceLabel?: string;
  loading: boolean;
  fetching: boolean;
  error: RunError | null;
  refetch(): void;
}

export interface RunArgs {
  plan: RunPlan;
  from: number;
  to: number;
  /** Uygulanan cluster kapsamı ('' = kapsamsız). */
  cluster: string;
  /** Thanos konsolu için cluster (kod kipi, src=thanos). */
  thanosAllowed: boolean;
}

export function useMetricsRun({ plan, from, to, cluster, thanosAllowed }: RunArgs): RunResult[] {
  const thanos = plan.src === 'thanos';
  const promItems = thanos ? [] : plan.items;
  const src = plan.src === 'vm' || plan.src === 'ch' ? plan.src : undefined;
  const results = useQueries({
    queries: promItems.map(it => ({
      queryKey: ['mx-run', it.query, from, to, plan.stepSec, plan.limit, plan.rank, src ?? '', cluster] as const,
      queryFn: ({ signal }: { signal: AbortSignal }) => api.metricPromql({
        query: it.query, from, to, step: plan.stepSec, src,
        limit: plan.limit, rank: plan.rank, cluster: cluster || undefined,
      }, signal),
      enabled: from > 0 && to > from,
      staleTime: 30_000,
      retry: false,
      refetchOnWindowFocus: false,
      placeholderData: keepPreviousData,
    })),
  });

  const thItem = thanos ? plan.items[0] : undefined;
  const th = useQuery({
    queryKey: ['mx-thanos', thItem?.query ?? '', cluster, from, to, plan.stepSec] as const,
    queryFn: ({ signal }) => api.promqlQueryRange({
      query: thItem!.query, cluster, start: Math.floor(from / 1e9), end: Math.ceil(to / 1e9), step: plan.stepSec,
    }, signal),
    enabled: !!thItem && !!cluster && thanosAllowed && from > 0,
    staleTime: 30_000,
    retry: false,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
  });

  if (thItem) {
    const series: SpanMetricSeries[] = promMatrixToSeries(th.data?.data?.result).map(s => ({
      groupKey: s.label ? [s.label] : [],
      points: s.points.map(p => ({ time: p.time, value: p.value ?? NaN })),
    }));
    return [{
      item: thItem,
      series: th.data ? series : undefined,
      sourceLabel: `Thanos · ${cluster}`,
      loading: th.isLoading,
      fetching: th.isFetching,
      error: th.error ? parseRunError(th.error) : null,
      refetch: () => { void th.refetch(); },
    }];
  }
  return promItems.map((item, i) => {
    const r = results[i];
    return {
      item,
      series: r?.data ? r.data.series : undefined,
      meta: r?.data?.meta,
      loading: !!r?.isLoading,
      fetching: !!r?.isFetching,
      error: r?.error ? parseRunError(r.error) : null,
      refetch: () => { void r?.refetch(); },
    };
  });
}
