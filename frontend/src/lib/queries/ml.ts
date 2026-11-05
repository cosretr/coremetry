// ml.ts — Log ML sekmesi okumaları (dilim 0). Sekme açıkken bir kez +
// staleTime; yoklama YOK (tik 5 dk, olay akışı SSE'siz; operatör yeniler).
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';
import type { MLEvent, MLScorecard, MLBaselineBackfillView } from '@/lib/types';

export function useMLEvents(signal: '' | 'log' | 'trace' | 'metric' = '', enabled = true) {
  return useQuery<{ items: MLEvent[]; truncated: boolean }>({
    queryKey: [...keys.anomalies.ml, signal],
    queryFn: async () => (await api.mlEvents({ signal })) ?? { items: [], truncated: false },
    enabled, staleTime: 30_000,
  });
}

export function useMLScorecard(enabled = true) {
  return useQuery<MLScorecard>({
    queryKey: keys.anomalies.mlScore,
    queryFn: async () => (await api.mlScorecard()) ?? { windowDays: 30, detectors: [], promotion: {}, signals: {} },
    enabled, staleTime: 60_000,
  });
}

/** v0.10.1815 — baseline geçmiş doldurma ilerlemesi. Yalnız koşu sürerken
 *  10 sn yoklama (RQ gizli sekmede durur); bitince/yokken yoklama yok. */
export function useMLBaselineBackfill(enabled = true) {
  return useQuery<MLBaselineBackfillView>({
    queryKey: keys.anomalies.mlBackfill,
    queryFn: async () => (await api.mlBaselineBackfill()) ?? { status: null, request: null },
    enabled, staleTime: 5_000,
    refetchInterval: q => (backfillActive(q.state.data) ? 10_000 : false),
  });
}

/** SAF: istek var ve koşu bitmemiş/iptal edilmemiş → aktif (yoklama). */
export function backfillActive(v: MLBaselineBackfillView | undefined): boolean {
  if (!v?.request || v.request.cancel) return false;
  const st = v.status;
  return !st || st.id !== v.request.id || (!st.complete && !st.cancelled);
}
