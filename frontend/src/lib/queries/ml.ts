// ml.ts — Log ML sekmesi okumaları (dilim 0). Sekme açıkken bir kez +
// staleTime; yoklama YOK (tik 5 dk, olay akışı SSE'siz; operatör yeniler).
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';
import type { MLEvent, MLScorecard } from '@/lib/types';

export function useMLEvents(signal: '' | 'log' | 'trace' = '', enabled = true) {
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
