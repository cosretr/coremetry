import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { ProblemVerdictInput, ProblemVerdictsResponse } from '@/lib/types';

// v0.10.1015 — Problems sekmesinde öğretme: kararlar (imza → gerçek / değil).
// TEK paylaşılan anahtar: liste (Inbox) ve çekmece aynı önbelleği okur.
// staleTime 30 sn > sunucu TTL'i (10 sn): yazımdan sonra cevabı doğrudan
// önbelleğe koyarız ve bayat sunucu önbelleğinden geri okumayız.
const KEY = ['problem-verdicts'] as const;

export function useProblemVerdicts() {
  return useQuery({ queryKey: KEY, queryFn: () => api.problemVerdicts(), staleTime: 30_000 });
}

/** v0.10.1016 — politika (bildirimi de sustur) yaz. Cevap aynı biçim → aynı önbellek. */
export function useSetProblemVerdictPolicy() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (muteNotifications: boolean) => api.putProblemVerdictPolicy(muteNotifications),
    onSuccess: (res: ProblemVerdictsResponse) => { qc.setQueryData(KEY, res); },
  });
}

/** Karar yaz / kaldır. Cevap taze listenin tamamıdır → doğrudan önbelleğe. */
export function useSetProblemVerdict() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ProblemVerdictInput) => api.putProblemVerdict(body),
    onSuccess: (res: ProblemVerdictsResponse) => { qc.setQueryData(KEY, res); },
  });
}
