// logml.ts — Log ML sekmesi okumaları (dilim 0). Sekme açıkken bir kez +
// staleTime; yoklama YOK (tik 5 dk, olay akışı SSE'siz; operatör yeniler).
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';
import type { LogMLEvent, LogMLScorecard } from '@/lib/types';

export function useLogMLEvents(enabled = true) {
  return useQuery<{ items: LogMLEvent[]; truncated: boolean }>({
    queryKey: keys.anomalies.logMl,
    queryFn: async () => (await api.logMlEvents()) ?? { items: [], truncated: false },
    enabled, staleTime: 30_000,
  });
}

export function useLogMLScorecard(enabled = true) {
  return useQuery<LogMLScorecard>({
    queryKey: keys.anomalies.logMlScore,
    queryFn: async () => (await api.logMlScorecard()) ?? { windowDays: 30, detectors: [], promotion: {} },
    enabled, staleTime: 60_000,
  });
}
