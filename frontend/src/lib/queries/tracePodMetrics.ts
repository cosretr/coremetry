import { useEffect, useMemo, useRef } from 'react';
import { useQueries, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { TracePodMetricsResponse } from '@/lib/types';
import { keys } from './keys';
import {
  chunkEligible, chunksToEnable, type ChunkPhase, type ChunkSnapshot, type TracePodChunk,
} from '@/pages/trace/traceMetrics';

// useTracePodMetricsChunks — v0.10.968 (Trace › Metrics yeniden tasarımı,
// operatör onayı 2026-09-27). Pod başına /api/clusters/pods/detail fan-out'u
// (64 pod ≈ 67 istek) yerine TOPLU uç: (cluster değeri, ≤64 pod) dilimi
// başına TEK istek — 64 pod / 2 cluster = 2 istek.
//
//   • Dilim başına bir useQueries girdisi; queryFn signal'i iletir
//     (cancellation.test.ts HEAVY): sekme/pencere değişince istek kesilir.
//   • retry YOK (502 = Thanos yanıt vermedi; otomatik yeniden deneme 10 sn'lik
//     bekleyişi üçe katlar), odakta yeniden çekme yok, gcTime 10 dk.
//   • staleTime: açık pencere (trace son 10 dk) 60 sn = sunucu TTL'i; kapalı
//     pencere 10 dk (sunucu da 10 dk önbellekler) — ES/Thanos maliyet
//     disiplini: staleTime ≥ sunucu TTL.
//   • Açma kuralı saf yardımcıda (chunksToEnable): cluster değeri başına ilk
//     4 dilim kendiliğinden, aynı anda en çok 4 istek; kalanı açık düğmeyle.
//   • Açık pencere: `to` henüz gelmediyse `to + 90 sn`de TEK yeniden çekme
//     (yoklama DEĞİL); sekme o an gizliyse görünür olunca.
//   • v0.10.968 — eşlenmemiş / Thanos kapalı cevabı (mapped:false) sunucu
//     önbelleğe ALMAZ ("ayar değişince hemen doğru"); istemci de onu hep bayat
//     sayar: sekmenin kendi "Ayarlar › Remote Cluster ↗" bağlantısından
//     cluster ekleyip dönen operatör 10 dk "eşlenmemiş" görmesin. Bu cevap
//     ucuz (Thanos'a gitmez), yeniden bağlanmada tek istek.

export interface TracePodMetricsChunksOpts {
  fromNs: number;
  toNs: number;
  mdp: number;
  openWindow: boolean;
  loadRest: boolean;
  forced: ReadonlySet<string>;
  active: boolean;
}

export interface TracePodMetricsChunks {
  /** Dilim başına saf özet (podMetricState girdisi); imza değişmedikçe aynı dizi. */
  snapshots: ChunkSnapshot[];
  /** Şu an açık (istek atabilir) dilimler — uçuş sınırı dahil. */
  /** Özetlerin imzası — bellekleme anahtarı. */
  sig: string;
  enabled: boolean[];
  /** Dilimi yeniden çek (hata sonrası "Yeniden dene"). */
  refetch: (i: number) => void;
}

export function useTracePodMetricsChunks(chunks: TracePodChunk[], opts: TracePodMetricsChunksOpts): TracePodMetricsChunks {
  const qc = useQueryClient();
  const { fromNs, toNs, mdp, openWindow, loadRest, forced, active } = opts;
  const qkeys = chunks.map(c => keys.traces.podMetrics(c.clusterValue, fromNs, toNs, mdp, c.podsParam));
  // Açma kararı önbelleğin ŞU ANKİ durumundan (bir önceki render'ın sonucu
  // değil): biten dilim bir sonraki render'da sıradakine yer açar.
  const phases: ChunkPhase[] = qkeys.map(k => {
    const st = qc.getQueryState(k);
    if (!st) return 'idle';
    if (st.fetchStatus === 'fetching' || st.fetchStatus === 'paused') return 'pending';
    if (st.status === 'success' || st.status === 'error') return 'settled';
    return 'idle';
  });
  const eligible = chunks.map(c => active && chunkEligible(c, { loadRest, forced }));
  const enabled = chunksToEnable(chunks, phases, { loadRest, forced }).map(e => e && active);
  const results = useQueries({
    queries: chunks.map((c, i) => ({
      queryKey: qkeys[i],
      queryFn: ({ signal }) =>
        api.tracePodMetrics({ clusterValue: c.clusterValue, from: fromNs, to: toNs, mdp, pods: c.pods }, signal),
      enabled: enabled[i],
      retry: false,
      refetchOnWindowFocus: false,
      gcTime: 10 * 60_000,
      staleTime: (qq: { state: { data?: TracePodMetricsResponse } }) =>
        (qq.state.data && !qq.state.data.mapped ? 0 : openWindow ? 60_000 : 10 * 60_000),
    })),
  });

  const sig = results.map((r, i) =>
    `${eligible[i] ? 1 : 0}${r.status[0]}${r.fetchStatus[0]}${r.dataUpdatedAt}.${r.errorUpdatedAt}`).join('|');
  const snapshots = useMemo<ChunkSnapshot[]>(
    () => results.map((r, i) => ({
      eligible: eligible[i],
      status: r.status,
      fetching: r.fetchStatus === 'fetching',
      error: r.error,
      data: r.data,
      dataUpdatedAt: r.dataUpdatedAt,
    })),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- v0.10.968: imza her girdiyi taşır; results kimliği her render yenilenir
    [sig],
  );

  const resultsRef = useRef(results);
  resultsRef.current = results;
  const enabledRef = useRef(enabled);
  enabledRef.current = enabled;

  const chunkSig = chunks.map(c => c.key).join(',');
  useEffect(() => {
    if (!active) return;
    const toMs = toNs / 1e6;
    const now = Date.now();
    if (!(toMs > now)) return;
    let onVis: (() => void) | null = null;
    const fire = () => {
      resultsRef.current.forEach((r, i) => { if (enabledRef.current[i]) void r.refetch(); });
    };
    const timer = window.setTimeout(() => {
      if (!document.hidden) { fire(); return; }
      onVis = () => {
        if (document.hidden) return;
        document.removeEventListener('visibilitychange', onVis!);
        onVis = null;
        fire();
      };
      document.addEventListener('visibilitychange', onVis);
    }, toMs + 90_000 - now);
    return () => {
      window.clearTimeout(timer);
      if (onVis) document.removeEventListener('visibilitychange', onVis);
    };
  }, [toNs, active, chunkSig]);

  return {
    snapshots,
    sig,
    enabled,
    refetch: (i: number) => { void resultsRef.current[i]?.refetch(); },
  };
}
