import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';
import type { TraceBundleResponse, TraceColumnValue, TraceColumnValuesParams } from '@/lib/types';

// useTraceBundle — v0.10.672 (trace kiosk modu Dilim 2): GET
// /api/traces/{id}/bundle. Tek istekte span + log + Oracle; pencere sunucuda.
//
// staleTime = sunucu TTL (15 s, trace_bundle.go traceBundleTTL) — ES-maliyet
// disiplini: sekme odağı yeniden çekmez. Limitler anahtarda (keys.traces.
// bundle); "daha fazla" = logLimit 1000 ile YENİ anahtar, eski sayfa
// cache'te kalır. signal geçer (cancellation.test.ts kapısı): kiosk
// penceresi kapanınca ya da limit değişince eski istek CH/ES'te kesilir.
export function useTraceBundle(
  id: string | undefined,
  // v0.10.1889 (Log Search 3.6) — hint: log zamanı (unix sn, dakikaya
  // yuvarlanır — sunucu anahtarıyla aynı kardinalite); CH pencere aramasını
  // ±1 sa ile başlatır, sonucu değiştirmez.
  opts: { logLimit?: number; oracleLimit?: number; hint?: number } = {},
) {
  const logLimit = opts.logLimit ?? 0;
  const oracleLimit = opts.oracleLimit ?? 0;
  const hint = opts.hint && opts.hint > 0 ? Math.floor(opts.hint / 60) * 60 : 0;
  return useQuery<TraceBundleResponse>({
    queryKey: keys.traces.bundle(id ?? '', logLimit, oracleLimit, hint),
    queryFn: ({ signal }) => api.traceBundle(id ?? '', { logLimit: opts.logLimit, oracleLimit: opts.oracleLimit, hint: hint || undefined }, signal),
    enabled: !!id,
    staleTime: 15_000,
    refetchOnWindowFocus: false,
  });
}

// useTraceColumnValues — v0.10.1511 (/traces header column filter popover):
// GET /api/traces/column-values. Fetch-on-open: `enabled` is the popover's
// open flag, so a closed header never reads (ES/CH-cost discipline). The
// key carries every input (keys.traces.columnValues). staleTime = server
// TTL (30 s). keepPreviousData-style placeholder keeps the old list while a
// new `q` resolves. signal is forwarded (cancellation.test.ts HEAVY).
export function useTraceColumnValues(p: TraceColumnValuesParams, enabled: boolean) {
  return useQuery<TraceColumnValue[]>({
    queryKey: keys.traces.columnValues(p),
    queryFn: ({ signal }) => api.traceColumnValues(p, signal),
    enabled,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    placeholderData: prev => prev,
  });
}
