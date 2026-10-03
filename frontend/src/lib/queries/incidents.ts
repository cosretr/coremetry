import { useQuery, useQueries, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';
import type { Incident, IncidentEvent, Problem } from '@/lib/types';

// Incidents — list, detail, events. The list refreshes every
// 30s alongside problems (keys.incidents.all is invalidated
// by the SSE problem.* event listener so a new attached problem
// surfaces immediately).

export function useIncidents(filter: {
  status?: string; service?: string; severity?: string; limit?: number;
} = {}) {
  // v0.9.456 — zarf: {items, counts, truncated}; null gövde boş-dürüst
  // zarfa normalize edilir.
  return useQuery<{ items: Incident[]; counts: Record<string, number> | null; truncated: boolean }>({
    queryKey: keys.incidents.list(filter),
    queryFn: async () => (await api.listIncidents(filter)) ?? { items: [], counts: null, truncated: false },
    refetchInterval: 30_000,
    staleTime: 25_000,
  });
}

export function useIncident(id: string) {
  return useQuery<Incident | null>({
    queryKey: keys.incidents.one(id),
    queryFn: () => api.getIncident(id),
    enabled: !!id,
    staleTime: 30_000,
  });
}

export function useIncidentEvents(id: string) {
  return useQuery<IncidentEvent[]>({
    queryKey: keys.incidents.events(id),
    queryFn: async () => (await api.incidentTimeline(id)) ?? [],
    enabled: !!id,
    staleTime: 30_000,
  });
}

export function useIncidentProblems(id: string) {
  return useQuery<string[]>({
    queryKey: keys.incidents.problems(id),
    queryFn: async () => (await api.incidentProblems(id)) ?? [],
    enabled: !!id,
    staleTime: 30_000,
  });
}

// v0.10.1081 — incident sayfasının manşeti ve bağlı problemler listesi
// problemlerin KENDİ kaydını ister (açıklama, kural, özne, pencere); bağlı
// uç yalnız kimlik döndürür. Kimlik başına tekil okuma, problem detay
// sayfasıyla AYNI anahtar (keys.problems.byID) — oradan gelince önbellek
// paylaşılır. Tavan INCIDENT_PROBLEM_DETAIL_CAP: sayfa kalanları kimlikle
// listeler (sessiz kesim yok). Yoklama yok.
export const INCIDENT_PROBLEM_DETAIL_CAP = 12;

export function useIncidentProblemDetails(ids: readonly string[]) {
  const capped = ids.slice(0, INCIDENT_PROBLEM_DETAIL_CAP);
  return useQueries({
    queries: capped.map(id => ({
      queryKey: keys.problems.byID(id),
      queryFn: () => api.problem(id),
      staleTime: 15_000,
      placeholderData: undefined,
      retry: (count: number, err: Error) => (err.message.startsWith('HTTP 404') ? false : count < 2),
    })),
    combine: (rs) => ({
      problems: rs.map(r => r.data).filter((p): p is Problem => !!p),
      pending: rs.some(r => r.isPending),
    }),
  });
}

export function useCreateIncident() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.createIncident,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.incidents.all });
    },
  });
}

export function useUpdateIncident() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: Partial<Incident> }) =>
      api.updateIncident(id, patch),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: keys.incidents.one(vars.id) });
      qc.invalidateQueries({ queryKey: keys.incidents.list({}) });
    },
  });
}
