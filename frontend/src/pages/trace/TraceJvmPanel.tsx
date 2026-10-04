import { useMemo } from 'react';
import { useQueries, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { panelMaxDataPoints } from '@/lib/chartStep';
import type { FilterExpr, SpanMetricSeries } from '@/lib/types';
import { MultiLineChart, type DeployMarker } from '@/components/MultiLineChart';
import { Skeleton } from '@/components/Skeleton';
import { familyOf } from '@/pages/service/RuntimeCharts';
import { jvmPodSeries } from './traceMetrics';

// TraceJvmPanel — v0.10.923 (operatör, v0.10.913 sonrası: "sonra jvm gc
// metriğine bakarız"). Trace'teki seçili pod'ların JVM heap'i ve GC
// duraklaması, trace penceresinde. Kaynak Thanos DEĞİL: OTel runtime
// metrikleri (metric_points) — Servis › Runtime kartlarıyla aynı uç.
//
// Heap: GC SONRASI kullanım (jvm.memory.used_after_last_gc) önce — sızıntı
// sorusunun doğru sinyali; anlık kullanım GC testeresiyle dalgalanır.
// Ajan bu metriği göndermiyorsa anlık kullanıma (jvm.memory.used) düşer ve
// başlık bunu SÖYLER. GC duraklaması jvm.gc.duration (dışa aktarım başına
// ortalama, saniye → ms), RuntimeCharts'ın gc kartıyla aynı okuma.
//
// Servis JVM değilse hiç çizilmez (familyOf); JVM ama metrik yoksa tek
// satır not: "JVM runtime metrikleri gelmiyor".
//
// v0.10.968 — Trace › Metrics yeniden tasarımı (seçili pod paneli + odak
// görünümü) bu bileşeni YENİDEN kullanır; üç opsiyonel prop:
//   • `runtime` — span'lerin telemetry.sdk.language'ı biliniyorsa
//     /api/services/{svc}/runtime İSTENMEZ, karar familyOf(runtime) ile;
//   • `queryPods` — sorgu anahtarı ve IN süzgeci bu küme (varsayılan `pods`):
//     panel servisin bu trace'teki TÜM pod'larını bir kez sorar, sonuç
//     istemcide `pods`a (karşılaştırma kümesi) süzülür — çip açıp kapamak
//     yeniden istek atmaz;
//   • `maxDataPoints` — varsayılan panelMaxDataPoints(2) (panel yarım genişlik).
// Sıra ve adlar aynen: GC sonrası heap önce, jvm.gc.duration duruyor, dürüst
// başlıklar ("GC sonrası" / "anlık kullanım"). Heap limit çizgisi YOK (ertelendi).
//
// v0.10.968 — iki opsiyonel prop daha (seçili pod paneli için): `labelOf`
// seri etiketini çip / Bellek / CPU ile AYNI yapar (varsayılan shortPod —
// "…6b7d9f8c5-m3t9w" yerine "m3t9w"), `seriesColors` o etiketlerin
// çakışmasız rengini MultiLineChart'a geçirir: bir pod dört grafikte de tek
// renk. Süzgeç (only) HAM pod adıyla eşleşir; yeniden adlandırma sonra.
//
// v0.10.1096 — `chartHeight`: satır altı ayrıntıda heap ve GC, Bellek/CPU ile
// AYNI yatay ızgarada ve aynı yükseklikte çizilir (varsayılan 180 / 160).

const HEAP: FilterExpr = { k: 'jvm.memory.type', op: '=', v: ['heap'] };
const POD_KEY = 'resource.k8s.pod.name';

export function TraceJvmPanel({
  service, pods, from, to, syncKey, deploys, xRange, runtime, queryPods, maxDataPoints, labelOf, seriesColors, chartHeight,
}: {
  service: string;
  pods: string[];
  from: number; // unix ns
  to: number;   // unix ns
  syncKey: string;
  deploys?: DeployMarker[];
  xRange?: { from: number; to: number } | null;
  runtime?: string;
  queryPods?: string[];
  maxDataPoints?: number;
  labelOf?: (pod: string) => string;
  seriesColors?: ReadonlyMap<string, string>;
  chartHeight?: number;
}) {
  const runtimeQ = useQuery({
    queryKey: ['svc-runtime', service],
    queryFn: () => api.serviceRuntime(service),
    enabled: !!service && !runtime,
    staleTime: 5 * 60_000,
  });
  const isJvm = runtime ? familyOf(runtime) === 'jvm' : familyOf(runtimeQ.data?.language) === 'jvm';
  const asked = queryPods ?? pods;
  const mdp = maxDataPoints ?? panelMaxDataPoints(2);
  const podFilter: FilterExpr = { k: POD_KEY, op: 'IN', v: asked };
  const spec = (name: string, filters: FilterExpr[]) => ({
    queryKey: ['trace-jvm', service, name, asked.join(','), from, to, mdp],
    queryFn: () => api.metricQuery({
      name, service, agg: 'avg', filters: JSON.stringify(filters), groupBy: POD_KEY,
      from, to, step: 0, maxDataPoints: mdp,
    }),
    enabled: isJvm && asked.length > 0,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  });
  const [postGcQ, usedQ, gcQ] = useQueries({
    queries: [
      spec('jvm.memory.used_after_last_gc', [HEAP, podFilter]),
      spec('jvm.memory.used', [HEAP, podFilter]),
      spec('jvm.gc.duration', [podFilter]),
    ],
  });
  // v0.10.968 — istemci süzgeci: sorgu `queryPods` kapsar, çizim yalnız `pods`.
  const shown = useMemo(() => new Set(pods), [pods]);
  const only = (s: SpanMetricSeries[] | null | undefined) => (s ?? []).filter(x => shown.has(x.groupKey[0] ?? ''));
  if (!isJvm) return null;

  const postGc = jvmPodSeries(only(postGcQ.data), 1, labelOf);
  const heap = postGc.length > 0 ? postGc : jvmPodSeries(only(usedQ.data), 1, labelOf);
  const heapTitle = postGc.length > 0 ? 'JVM heap, GC sonrası (bytes)' : 'JVM heap, anlık kullanım (bytes)';
  const gc = jvmPodSeries(only(gcQ.data), 1000, labelOf);
  const pending = postGcQ.isLoading || usedQ.isLoading || gcQ.isLoading;
  if (pending) return <Skeleton height={chartHeight ?? 120} />;
  if (heap.length === 0 && gc.length === 0 && (postGcQ.isError || usedQ.isError || gcQ.isError)) {
    return <div className="pod-cap is-err">JVM runtime metrikleri okunamadı — sorgu hata verdi.</div>;
  }
  if (heap.length === 0 && gc.length === 0) {
    return <div className="pod-cap">JVM runtime metrikleri bu pod'lar için gelmiyor (OTel jvm.* yok).</div>;
  }
  return (
    <>
      {heap.length > 0 && (
        <div className="tpp-chart">
          <div className="tpp-sec-title"><span>{heapTitle}</span></div>
          <MultiLineChart series={heap} height={chartHeight ?? 180} syncKey={syncKey} unit="bytes" deploys={deploys} xRange={xRange} zeroBase
            seriesColors={seriesColors} />
        </div>
      )}
      {gc.length > 0 && (
        <div className="tpp-chart">
          <div className="tpp-sec-title"><span>GC duraklaması, ortalama (ms)</span></div>
          <MultiLineChart series={gc} height={chartHeight ?? 160} syncKey={syncKey} unit="ms" deploys={deploys} xRange={xRange} zeroBase
            seriesColors={seriesColors} />
        </div>
      )}
    </>
  );
}
