import { useQueries, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { HTTP_METRICS_POINTS } from '@/lib/httpMetrics';
import { httpEnvScopeView, type HttpEnvScopeView } from '@/lib/httpEnvScopeNote';
import { redFamilyView, type RedFamilySection, type RedFamilyView } from '@/lib/redFamily';
import type { SpanMetricSeries } from '@/lib/types';

// useHttpPromql — v0.10.1352. Service → HTTP Metrics sekmesinin VM PromQL
// okuması tek kancada; endpoint sayfasının metrik kipi AYNI kancayı
// kullanır, böylece iki yüzey aynı uçtan (/api/metrics/promql?metricsrc=vm,
// api.httpMetricsPromql), aynı anahtarla (aynı ifade + pencere ⇒ aynı RQ
// önbellek girdisi), aynı staleTime (30 sn = sunucu serveCached TTL) ve
// retry'sız okur. Yoklama YOK (refetchInterval verilmez); `enabled` false
// iken hiç istek atılmaz (endpoint sayfası yalnız metrik kipinde açar).
// Instant (anlık, `to` anında) sorgular sunucuda yalnız VM'de var.

export interface HttpPromqlItem { id: string; expr: string; instant: boolean }

export interface HttpPromqlResult {
  data: Record<string, SpanMetricSeries[] | null | undefined>;
  pending: (id: string) => boolean;
  error: (id: string) => Error | null;
}

export function useHttpPromql(
  items: readonly HttpPromqlItem[], win: { from: number; to: number }, enabled = true,
): HttpPromqlResult {
  const results = useQueries({
    queries: items.map(q => ({
      queryKey: ['http-metrics', q.expr, win.from, win.to, q.instant],
      queryFn: () => api.httpMetricsPromql({
        query: q.expr, from: win.from, to: win.to, maxDataPoints: HTTP_METRICS_POINTS,
        ...(q.instant ? { instant: '1' as const } : {}),
      }),
      staleTime: 30_000,
      retry: false,
      enabled,
    })),
  });
  const data: Record<string, SpanMetricSeries[] | null | undefined> = {};
  const idx = new Map<string, number>();
  items.forEach((q, i) => { data[q.id] = results[i]?.data; idx.set(q.id, i); });
  return {
    data,
    pending: id => { const i = idx.get(id); return i === undefined ? false : !!results[i]?.isPending; },
    error: id => { const i = idx.get(id); return i === undefined ? null : ((results[i]?.error as Error | null) ?? null); },
  };
}

// useRedFamily — v0.10.1429 (metrik kipi: yalnız gRPC sunan servislerde RED;
// v0.10.1377'nin useHttpEnvScope'unun yerine). Servisin VM'de yazdığı RED
// ailesini (HTTP / gRPC legacy ms / gRPC stable s) ve aile başına ortam
// eşleyicisini TEK istekte alır (GET /api/services/{name}/red-family, sunucu
// 60 sn önbellek). `to` 10 dk'ya yuvarlanır — sunucunun yoklama penceresi de
// öyle; anahtar TÜM girdiler (servis, env, cluster, dilim). Yoklama yok;
// staleTime = sunucu TTL'i; retry yok (hata → HTTP sorguları koşar, rozet
// söyler). Çözüm gelene dek sorgu YOK (önce HTTP sonra gRPC yanıltır).
export const HTTP_ENV_SCOPE_SNAP_NS = 600e9;

export function useRedFamily(service: string, env: string, cluster: string, toNs: number, enabled = true): RedFamilyView {
  const e = env.trim();
  const c = cluster.trim();
  const on = enabled && !!service;
  const bucket = Math.floor(toNs / HTTP_ENV_SCOPE_SNAP_NS);
  const q = useQuery({
    queryKey: ['red-family', service, e, c, bucket],
    queryFn: ({ signal }) => api.serviceRedFamily(service, { env: e || undefined, cluster: c || undefined, to: bucket * HTTP_ENV_SCOPE_SNAP_NS }, signal),
    enabled: on,
    staleTime: 60_000,
    retry: false,
  });
  return redFamilyView({ data: q.data, error: (q.error as Error | null) ?? null }, on);
}

/** Bölümün ortam eşleyicisi görünümü (bant + envSel): aile çözümünün envScope'u httpEnvScopeView'a girer. */
export function sectionEnvScope(sec: RedFamilySection | undefined, v: RedFamilyView, env: string, cluster: string): HttpEnvScopeView {
  const on = !!(env.trim() || cluster.trim());
  if (!on) return { state: 'off' };
  if (v.state === 'pending') return { state: 'pending' };
  if (v.state === 'error' || v.state === 'unknown') return { state: 'error', reason: v.reason };
  const sc = sec?.presence?.envScope;
  if (!sc) return { state: 'unresolved' }; // aile yok / kapsam gelmedi → süzgeçsiz, bant söyler (asla bekletmez)
  return httpEnvScopeView(env, cluster, { data: sc, error: null }, true);
}

/** v0.10.1976 — "her metrik" route dışlama desenleri (healthcheck varsayılanı); 60 sn önbellek, hata → []. */
export function useRouteExclusions(): string[] {
  const q = useQuery({
    queryKey: ['metric-route-exclusions'],
    queryFn: () => api.metricRouteExclusions(),
    staleTime: 60_000,
    retry: false,
  });
  return q.data?.patterns ?? NO_PATTERNS;
}
const NO_PATTERNS: string[] = [];
