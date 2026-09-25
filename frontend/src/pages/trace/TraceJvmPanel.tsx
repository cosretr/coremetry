import { useQueries, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { panelMaxDataPoints } from '@/lib/chartStep';
import type { FilterExpr } from '@/lib/types';
import { MultiLineChart, type DeployMarker } from '@/components/MultiLineChart';
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

const HEAP: FilterExpr = { k: 'jvm.memory.type', op: '=', v: ['heap'] };
const POD_KEY = 'resource.k8s.pod.name';

export function TraceJvmPanel({ service, pods, from, to, syncKey, deploys, xRange }: {
  service: string;
  pods: string[];
  from: number; // unix ns
  to: number;   // unix ns
  syncKey: string;
  deploys?: DeployMarker[];
  xRange?: { from: number; to: number } | null;
}) {
  const runtimeQ = useQuery({
    queryKey: ['svc-runtime', service],
    queryFn: () => api.serviceRuntime(service),
    enabled: !!service,
    staleTime: 5 * 60_000,
  });
  const isJvm = familyOf(runtimeQ.data?.language) === 'jvm';
  const podFilter: FilterExpr = { k: POD_KEY, op: 'IN', v: pods };
  const spec = (name: string, filters: FilterExpr[]) => ({
    queryKey: ['trace-jvm', service, name, pods.join(','), from, to],
    queryFn: () => api.metricQuery({
      name, service, agg: 'avg', filters: JSON.stringify(filters), groupBy: POD_KEY,
      from, to, step: 0, maxDataPoints: panelMaxDataPoints(1),
    }),
    enabled: isJvm && pods.length > 0,
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
  if (!isJvm) return null;

  const postGc = jvmPodSeries(postGcQ.data);
  const heap = postGc.length > 0 ? postGc : jvmPodSeries(usedQ.data);
  const heapTitle = postGc.length > 0 ? 'JVM heap, GC sonrası (bytes)' : 'JVM heap, anlık kullanım (bytes)';
  const gc = jvmPodSeries(gcQ.data, 1000);
  const pending = postGcQ.isLoading || usedQ.isLoading || gcQ.isLoading;
  if (pending) return null;
  if (heap.length === 0 && gc.length === 0 && (postGcQ.isError || usedQ.isError || gcQ.isError)) {
    return <div className="pod-cap">JVM runtime metrikleri okunamadı — sorgu hata verdi.</div>;
  }
  if (heap.length === 0 && gc.length === 0) {
    return <div className="pod-cap">JVM runtime metrikleri bu pod'lar için gelmiyor (OTel jvm.* yok).</div>;
  }
  return (
    <>
      {heap.length > 0 && (
        <div>
          <div style={{ fontSize: 11, color: 'var(--text2)', marginBottom: 4 }}>{heapTitle}</div>
          <MultiLineChart series={heap} height={180} syncKey={syncKey} unit="bytes" deploys={deploys} xRange={xRange} zeroBase />
        </div>
      )}
      {gc.length > 0 && (
        <div>
          <div style={{ fontSize: 11, color: 'var(--text2)', marginBottom: 4 }}>GC duraklaması, ortalama (ms)</div>
          <MultiLineChart series={gc} height={160} syncKey={syncKey} unit="ms" deploys={deploys} xRange={xRange} zeroBase />
        </div>
      )}
    </>
  );
}
