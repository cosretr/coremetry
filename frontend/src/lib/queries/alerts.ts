import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { AlertRule, AlertRuleSeries } from '@/lib/types';

const ALERTS_KEY = ['alerts', 'rules'] as const;

// Alerts — list + CRUD mutations. The list refetches every
// 60s (rules don't change often) but every mutation invalidates
// it eagerly so a save shows up immediately.

export function useAlertRules() {
  return useQuery<AlertRule[]>({
    queryKey: ALERTS_KEY,
    queryFn: async () => (await api.alertRules()) ?? [],
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
}

// useAlertRuleSeries — v0.10.1064 (operatör: "grafik olmadığı için de anlamak
// çok zor artışları"). Alarm problemi detayının grafiği.
//
// Yalnız detay sayfası açıkken (args null → sorgu yok, liste ön-yüklemesi
// yok); staleTime = sunucu TTL'i (60 s); yoklama YALNIZ açık problemde ve
// 60 s — refetchIntervalInBackground varsayılanı (false) gizli sekmede durdurur.
// Açık problemde toNs = null: anahtar yoklamada sabit, sunucu "şimdi"yi alır.
//
// data === null → sunucu 404 (bu kural türünün dizisi yok / kural silinmiş):
// bölüm hiç çizilmez. placeholderData: undefined — bir problemin dizisi
// başkasının altında görünmesin (küresel keepPreviousData tuzağı).
export function useAlertRuleSeries(
  args: { ruleId: string; service: string; metric: string; fromNs: number; toNs: number | null } | null,
  opts: { live: boolean },
) {
  return useQuery<AlertRuleSeries | null>({
    queryKey: ['alerts', 'rule-series', args?.ruleId ?? '', args?.service ?? '', args?.metric ?? '',
      args?.fromNs ?? 0, args?.toNs ?? 0],
    queryFn: async ({ signal }) => {
      if (!args) return null;
      try {
        return (await api.alertRuleSeries(
          { ruleId: args.ruleId, service: args.service, metric: args.metric, fromNs: args.fromNs, toNs: args.toNs ?? undefined },
          signal)) ?? null;
      } catch (err) {
        if (err instanceof Error && err.message.startsWith('HTTP 404')) return null;
        throw err;
      }
    },
    enabled: !!args,
    staleTime: 60_000,
    refetchInterval: opts.live ? 60_000 : false,
    placeholderData: undefined,
    retry: (count, err) => (err instanceof Error && err.message.startsWith('HTTP 404') ? false : count < 1),
  });
}

// Kural başına açık problem sayısı — /alerts "Open problems" kolonu
// (v0.9.1109). Sunucu 15s TTL'li; 30s poll rozetle aynı tazelikte,
// RQ gizli sekmede zaten duraklatır.
export function useAlertRuleProblemCounts() {
  return useQuery<Record<string, number>>({
    queryKey: ['alerts', 'rule-problem-counts'],
    queryFn: async () => (await api.problemRuleCounts())?.counts ?? {},
    staleTime: 15_000,
    refetchInterval: 30_000,
  });
}

function useAlertMutation<T>(fn: (input: T) => Promise<unknown>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => qc.invalidateQueries({ queryKey: ALERTS_KEY }),
  });
}

export function useCreateAlertRule() {
  return useAlertMutation<Partial<AlertRule>>(api.createAlertRule);
}

export function useUpdateAlertRule() {
  return useAlertMutation<{ id: string; patch: Partial<AlertRule> }>(
    ({ id, patch }) => api.updateAlertRule(id, patch),
  );
}

export function useDeleteAlertRule() {
  return useAlertMutation<string>(api.deleteAlertRule);
}

export function useEnableAlertRule() {
  return useAlertMutation<string>(api.enableAlertRule);
}

export function useDisableAlertRule() {
  return useAlertMutation<string>(api.disableAlertRule);
}
