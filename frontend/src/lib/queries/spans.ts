import { useQuery } from '@tanstack/react-query';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';
import type { SpanExemplar, ProfileRow, SpanHotspotsResponse, OperationSummary, LatencyHeatmap } from '@/lib/types';

// ── SpanDetail paneli (2026-11-21, effect-fetch → React Query) ──────────
// Panel açıkken çekilir; span değişince / panel kapanınca uçuştaki istek
// signal ile kesilir. Hata davranışı eski effect'lerle aynı: profil listesi
// boş, hotspot null, baseline satırı gizli — panel bir okumanın hatası
// yüzünden kırılmaz. retry YOK (eski effect'ler de tek atıştı; panel
// okumasında tekrar = operatörün çoktan geçtiği span'in gecikmiş cevabı).

// Trace-to-profile: penceresi span'le çakışan profiller.
export function useSpanProfiles(service: string, startNs: number, endNs: number) {
  return useQuery<ProfileRow[]>({
    queryKey: keys.spans.profiles(service, startNs, endNs),
    queryFn: async ({ signal }) => (await api.profilesForSpan(service, startNs, endNs, signal)) ?? [],
    enabled: !!service && !!startNs,
    staleTime: 60_000,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

// Span penceresindeki birleşik CPU hotspot'ları (ilk `top`).
export function useSpanHotspots(service: string, startNs: number, endNs: number, top = 10) {
  return useQuery<SpanHotspotsResponse | null>({
    queryKey: keys.spans.hotspots(service, startNs, endNs, top),
    queryFn: async ({ signal }) => (await api.spanHotspots(service, startNs, endNs, top, signal)) ?? null,
    enabled: !!service && !!startNs,
    staleTime: 60_000,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

// Baseline p50 — span'in BİTİŞİNE demirli önceki 24 saat, aynı servis
// (operation_summary_5m, /api/services/{svc}/operations). now()'a değil
// span'e demirli: eski bir trace kendi zamanının tabanıyla kıyaslanır.
// Eşleşme (ham ad, sonra gösterim adı) `select`te; anahtar yalnız istek
// girdileri — aynı servis+bitiş için iki span tek isteği paylaşır.
const DAY_NS = 86_400_000_000_000;
export function useSpanBaselineP50(service: string, endNs: number, names: string[]) {
  return useQuery<OperationSummary[], Error, number | null>({
    queryKey: keys.spans.baselineOps(service, endNs),
    queryFn: async ({ signal }) =>
      (await api.serviceOperations(service, { from: endNs - DAY_NS, to: endNs }, false, false, '', signal)) ?? [],
    enabled: !!service && !!endNs,
    staleTime: 60_000,
    retry: false,
    refetchOnWindowFocus: false,
    select: ops => {
      for (const n of names) {
        const op = ops.find(o => o.name === n);
        if (op) return op.p50DurationMs > 0 ? op.p50DurationMs : null;
      }
      return null;
    },
  });
}

// Dalga 3 (Honeycomb "span vs peers") — aynı servis + operasyonun trace
// penceresindeki (±15 dk) süre ızgarası; mevcut /api/spans/heatmap (sunucuda
// 30 sn serveCached; ham span taraması zaman sınırlı + LIMIT + max_execution_time).
// Pencere dakikaya yuvarlı gelir → aynı trace'te gezinmek aynı anahtar.
export function useSpanPeers(service: string, operation: string, from: number, to: number) {
  return useQuery<LatencyHeatmap | null>({
    queryKey: keys.spans.peers(service, operation, from, to),
    // 2026-11-21 — /api/spans/peers: MV-öncelikli (spanmetrics_1m), ham heatmap yedek.
    queryFn: ({ signal }) => api.spanPeers({ service, operation, from, to }, signal),
    enabled: !!service && !!operation && to > from,
    staleTime: 5 * 60_000,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

// Span exemplar — the bridge from a metric chart point to a
// representative trace in that bucket. The hook is `enabled`-
// gated because we don't want to fire it eagerly on every
// chart mount; pages call `useExemplar(…, { enabled: open })`
// or use the imperative `prefetchExemplar` for click handlers.
//
// Cache lifetime: 60s. The bucket data shifts after a window
// rolls over, but a user clicking the same point twice within
// a minute should hit the cache. Anything older is cheap
// enough to refetch.
export function useExemplar(
  args: { service: string; op?: string; from: number; to: number; kind?: 'slow' | 'error' | 'any' },
  opts?: { enabled?: boolean },
) {
  return useQuery<SpanExemplar | null>({
    queryKey: keys.spans.exemplar(args.service, args.op ?? '', args.from, args.to, args.kind ?? 'slow'),
    queryFn: () => api.spanExemplar(args),
    enabled: !!args.service && !!args.from && !!args.to && (opts?.enabled ?? true),
    staleTime: 60_000,
  });
}

// Imperative prefetch — for chart click handlers that fire a
// one-shot fetch and navigate; we don't want a hook re-render
// cascade for that. Use the QueryClient cache so a subsequent
// useExemplar with the same key picks up the result.
export function useExemplarFetcher() {
  const qc = useQueryClient();
  return (args: { service: string; op?: string; from: number; to: number; kind?: 'slow' | 'error' | 'any' }) =>
    qc.fetchQuery<SpanExemplar | null>({
      queryKey: keys.spans.exemplar(args.service, args.op ?? '', args.from, args.to, args.kind ?? 'slow'),
      queryFn: () => api.spanExemplar(args),
      staleTime: 60_000,
    });
}
