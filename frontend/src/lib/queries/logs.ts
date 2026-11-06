import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { LogsParams, LogsTemplatesParams } from '@/lib/api';
import type { LogsResponse, LogsMyTeam, LogPatternsResult, LogTemplate, LogsSearchSettings, LogRow } from '@/lib/types';
import { parseServerTiming } from '@/lib/serverTiming';

// /api/logs query — keyed on the full filter object so a
// pagination click or filter change caches separately.
//
// v0.8.3 (operator-reported ES incident) — /api/logs is UNCACHED on
// the backend and the Elasticsearch path opens a fresh Point-in-Time
// per call. staleTime:0 + the default refetchOnWindowFocus meant every
// tab focus / reconnect re-fired the list query → another PIT opened
// (and leaked for 2m if the operator didn't page to the end). At ES
// scale this was a measurable amplifier of the api-pod CPU climb.
// staleTime 15s keeps back-nav freshness "good enough" while collapsing
// focus refires; an explicit Next / filter change still refetches
// because the queryKey (params incl. cursor) changes. refetchOnWindowFocus
// off stops the tab-focus PIT churn outright. (No effect on CH backend
// correctness — it just fetches less.)
// v0.9.286 — this hook IS the paging surface (/logs, the only list with
// a Load-more), so it declares paging intent here rather than at each
// call site. Everything else reaches /api/logs through api.logs()
// directly, reads one page and drops the cursor; those callers now cost
// the ES cluster no retained Point-in-Time.
// v0.10.420 — opts.enabled: /logs canlı kuyruktayken statik sorgu KOŞMAZ
// (yorum yıllardır öyle diyordu; hook seçeneği yoktu → her filtre dokunuşu
// ES'te ~2 dk PIT pinliyordu).
export function useLogs(params: LogsParams, opts?: { enabled?: boolean }) {
  const pagingParams: LogsParams = { ...params, paging: true };
  return useQuery<LogsResponse>({
    queryKey: ['logs', 'list', pagingParams],
    // v0.10.1834 — akışlı yükleme göstergesi: yanıtın Server-Timing'i + tarayıcı
    // süresi yanıta iliştirilir (loadTiming; yalnız istemci alanı).
    queryFn: async ({ signal }) => {
      const t0 = performance.now();
      let header: string | null = null;
      const r = await api.logs(pagingParams, signal, h => { header = h; });
      if (!r) return r;
      return { ...r, loadTiming: { ...parseServerTiming(header), clientMs: performance.now() - t0 } };
    },
    enabled: opts?.enabled ?? true,
    staleTime: 15_000,
    refetchOnWindowFocus: false,
    // v0.8.260 — "Load more" accumulation on /logs: a cursor change
    // is a NEW query key; without a placeholder the page flashed the
    // skeleton on every append. Previous data holds the table stable
    // while the next page loads (isPlaceholderData guards the
    // accumulator from ingesting the stand-in).
    placeholderData: keepPreviousData,
  });
}

// v0.10.297 — /logs "Desenler" paneli. YALNIZ panel açıkken (enabled) —
// v0.8.270 ES-maliyet disiplini: liste prefetch'i yok, açılışta fetch.
// staleTime = sunucu TTL (30 s); anahtar TÜM parametreler.
export function useLogsPatterns(params: LogsParams & { limit?: number; sample?: number; baseline?: 1 }, enabled: boolean) {
  return useQuery<LogPatternsResult>({
    queryKey: ['logs', 'patterns', params],
    queryFn: ({ signal }) => api.logsPatterns(params, signal),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
    enabled,
  });
}

// v0.10.310 — /logs "Şablonlar" sekmesi (Drain kalıcı). YALNIZ sekme
// açıkken; staleTime = sunucu TTL (30 s); anahtar TÜM parametreler.
export function useLogsTemplates(params: LogsTemplatesParams, enabled: boolean) {
  return useQuery<LogTemplate[]>({
    queryKey: ['logs', 'templates', params],
    queryFn: ({ signal }) => api.logsTemplates(params, signal),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
    enabled,
  });
}

// v0.10.1477 — /logs "Takımım" otomatik varsayılanı: kullanıcının takımı +
// özelliğin açık olup olmadığı. Ucuz uç (ES'e gitmez; sunucu 60 s cache) —
// staleTime = sunucu TTL. Hata = takımsız davranış (sayfa bugünkü gibi).
export function useLogsMyTeam() {
  return useQuery<LogsMyTeam>({
    queryKey: ['logs', 'my-team'],
    queryFn: ({ signal }) => api.logsMyTeam(signal),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    retry: false,
  });
}

// v0.10.1831 (Log Search Faz 1 dilim 1.8) — /logs örnek boyu üst sınırı
// (system_settings logs_search). Ucuz uç (ES'e gitmez); sunucu ayarı 30 sn
// tazelenir — staleTime 60 sn. Hata = varsayılan tavan (istemci 2000 varsayar).
export function useLogsSearchSettings() {
  return useQuery<LogsSearchSettings>({
    queryKey: ['logs', 'search-settings'],
    queryFn: ({ signal }) => api.getLogsSearchSettings(signal),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    retry: false,
  });
}

// v0.10.1831 — dar `_source` satırının TAM dokümanı; YALNIZ satır açılınca
// (queryClient.fetchQuery ile, ES-maliyet disiplini: liste boyunca ön-yükleme
// yok). Sunucu ve RQ 60 sn önbellekte tutar; aynı satır yeniden açılınca istek yok.
export function logsDocQuery(ref: string) {
  return {
    queryKey: ['logs', 'doc', ref] as const,
    queryFn: ({ signal }: { signal: AbortSignal }) => api.logsDoc(ref, signal),
    staleTime: 60_000,
    retry: false as const,
  };
}
export type LogsDocRow = LogRow;
