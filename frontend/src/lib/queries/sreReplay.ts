// sreReplay.ts — CoSRE doğruluk ölçümü A2: replay notlama
// raporları (/ai › SREReplayPanel). staleTime = sunucu TTL (15 s). Kendi
// yoklaması YOK (pollingBudget.pin): anahtarlar keys.sre.all dalında — tekrar
// işlerinin SSE 'sre.investigation' olayları dalı düşürür; notlama tiki
// dakikalık, panelin "Yenile"si elle tazeler.
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';

export const SRE_REPLAY_STALE_MS = 15_000;

export function useSREReplayReports() {
  return useQuery({
    queryKey: keys.sre.replayReports,
    queryFn: ({ signal }) => api.sreReplayReports(signal),
    staleTime: SRE_REPLAY_STALE_MS,
  });
}

export function useSREReplayReport(id: string) {
  return useQuery({
    queryKey: keys.sre.replayReport(id),
    queryFn: ({ signal }) => api.sreReplayReport(id, signal),
    enabled: !!id,
    staleTime: SRE_REPLAY_STALE_MS,
  });
}

// CoSRE ölçüm — iki raporun karşılaştırması (GET /api/sre/replay/compare).
export function useSREReplayCompare(base: string, cand: string) {
  return useQuery({
    queryKey: keys.sre.replayCompare(base, cand),
    queryFn: ({ signal }) => api.sreReplayCompare(base, cand, signal),
    enabled: !!base && !!cand && base !== cand,
    staleTime: SRE_REPLAY_STALE_MS,
  });
}
