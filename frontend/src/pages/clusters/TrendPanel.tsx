import { useMemo } from 'react';
import { thanosMaxDataPoints } from '@/lib/chartStep';
import { THANOS_MAX_WINDOW_NS } from '@/lib/thanosWindow';
import { useQuery } from '@tanstack/react-query';
import { MultiLineChart, type DeployMarker } from '@/components/MultiLineChart';
import { Spinner, Empty } from '@/components/Spinner';
import { api } from '@/lib/api';
import { limitThresholds, thanosPodSeriesToSeries, thanosTrendToSeries } from './trendSeries';
import type { ClusterPodRow } from '@/lib/types';

// TREND_WINDOWS — trend panelinin ?tw= yerel pencere seçenekleri ('' = sayfa
// range'i). URL'de yaşar: link'le paylaşılınca pencere korunur. (v0.9.151'de
// silinen PodDrawer.tsx'ten buraya taşındı — trend penceresi bu modülün işi.)
export const TREND_WINDOWS = [
  { key: '', label: 'Page range' },
  { key: '15m', label: '15m', ns: 15 * 60 * 1e9 },
  { key: '1h', label: '1h', ns: 3600 * 1e9 },
  { key: '6h', label: '6h', ns: 6 * 3600 * 1e9 },
  // v0.9.1370 — 24h basamağı. Bu liste 6h'te bitiyordu çünkü sunucu
  // pencereyi zaten oraya kelepçeliyordu; tavan 24h'e çıkınca daha geniş
  // bir rung SEÇİLEBİLİR hâle geldi. Tavanın kendisi lib/thanosWindow'da.
  { key: '24h', label: '24h', ns: THANOS_MAX_WINDOW_NS },
] as const;

// ThanosTrendPanel — Thanos trend grafiklerinin YERLEŞİM-BAĞIMSIZ
// modülü (v0.9.4, trend-upgrade audit §2). Bugün drawer'da, yarın
// sekmeli detay layout'unda AYNI bileşen mount edilir — taşıma sıfır.
//
// CPU ve Memory AYRI iki MultiLineChart (birimler tek eksene
// sığmaz); syncKey crosshair'ı senkronlar (Endpoints modal emsali).
// Threshold katmanı: limit (err) + request (warn) yatay çizgileri —
// yalnız tekil-pod modunda (multi-pod'da pod başına limit karışır).
// Deploy marker'lar T4'te (pod↔servis korelasyonuna kapılı) gelir.
//
// Fetch panel mount'unda (drawer açılışı = fetch-on-open); staleTime
// = sunucu TTL'i. Madde 4 sweep — audit §2.4'ün "drag-zoom yerel görünüm
// keşfi, fetch tetiklemez" kararı GÜNCELLENDİ: caller onZoom verirse
// drag-seçim sayfa range'ine yazar (Pod.tsx + Clusters.tsx setRange'e
// bağlar, Service.tsx emsali) ve pencere değişince chart yeni veriyle
// kurulur; onZoomReset çift-tıkı sayfanın geri-yığınına devreder.
// Verilmezse eski davranış birebir (yalnız yerel görsel zoom).

export function ThanosTrendPanel({ cluster, namespace, pod, row, fromNs, toNs, onZoom, onZoomReset }: {
  cluster: string;
  namespace: string;
  // pod verilirse tekil-pod modu; verilmezse multi-pod (namespace).
  pod?: string;
  // Tekil modda threshold kaynakları listedeki satırdan gelir
  // (deep-link'te satır yoksa çizgiler atlanır — veri yine çizilir).
  row?: ClusterPodRow;
  fromNs: number;
  toNs: number;
  // Madde 4 sweep — drag-seçim sayfa range'ine (unix sec), çift-tık
  // sayfanın zoom geri-yığınına. İkisi de MultiLineChart'a aynen iletilir.
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  onZoomReset?: () => void;
}) {
  const single = !!pod;
  // v0.10.2182 (Clusters S13, plan §10.4) — restart dikey işaretleri yalnız
  // pod satırı restart>0 dediğinde istenir (+1 range sorgu, probe'lu).
  const wantRestarts = single && (row?.restarts ?? 0) > 0;
  const podQ = useQuery({
    queryKey: ['cluster-pod-detail', cluster, namespace, pod, fromNs, toNs, wantRestarts],
    queryFn: () => api.clusterPodDetail(cluster, namespace, pod!, fromNs, toNs, wantRestarts),
    staleTime: 60_000,
    enabled: single,
  });
  const podsMdp = thanosMaxDataPoints(1); // v0.10.287 — sunucu nokta bütçesi
  const multiQ = useQuery({
    queryKey: ['cluster-ns-pods-trend', cluster, namespace, fromNs, toNs, podsMdp],
    queryFn: () => api.clusterNamespacePodsTrend(cluster, namespace, fromNs, toNs, podsMdp),
    staleTime: 60_000,
    enabled: !single,
  });
  const q = single ? podQ : multiQ;

  // v0.9.13 (T4) — deploy marker'lar: pod'un Coremetry servis
  // eşleşmesi (v0.9.11-12) varsa o servisin deploy zaman damgaları
  // grafiğe dikey çizgi olarak biner. Eşleşme yoksa sorgu HİÇ
  // atılmaz (korelasyon kapısı); mevcut /deploys ucu + ServiceCharts
  // marker dili.
  const svc = single ? row?.service ?? '' : '';
  const deploysQ = useQuery({
    queryKey: ['service-deploys', svc, fromNs, toNs],
    queryFn: () => api.serviceDeploys(svc, { from: fromNs, to: toNs }),
    staleTime: 60_000,
    enabled: !!svc,
  });
  const deployMarkers: DeployMarker[] | undefined = useMemo(() => {
    const out: DeployMarker[] = (deploysQ.data ?? []).map(d => ({
      timeUnixNs: d.timeUnixNs,
      label: d.version,
      description: `${d.service} deploy · ${d.version}`,
    }));
    // Restart işaretleri aynı dikey çizgi dilinde (veri yoksa çizilmez).
    for (const ts of podQ.data?.restarts ?? []) {
      out.push({ timeUnixNs: ts * 1e9, label: 'restart', description: 'konteyner yeniden başladı' });
    }
    return out.length > 0 ? out : undefined;
  }, [deploysQ.data, podQ.data]);

  const { cpuSeries, memSeries, totalPods } = useMemo(() => {
    if (single) {
      const trend = podQ.data?.trend ?? [];
      return {
        cpuSeries: thanosTrendToSeries(trend, 'CPU (cores)', t => t.cpuCores),
        memSeries: thanosTrendToSeries(trend, 'Memory (bytes)', t => t.memBytes),
        totalPods: 0,
      };
    }
    const pods = multiQ.data?.pods ?? [];
    return {
      cpuSeries: thanosPodSeriesToSeries(pods, t => t.cpuCores),
      memSeries: thanosPodSeriesToSeries(pods, t => t.memBytes),
      totalPods: multiQ.data?.totalPods ?? 0,
    };
  }, [single, podQ.data, multiQ.data]);

  if (q.isPending) return <Spinner />;
  if (q.isError) return <Empty icon="✗" title="Failed to load trend" />;
  if (cpuSeries.length === 0 && memSeries.length === 0) {
    return <div className="cl-trend__empty">No samples in this window.</div>;
  }

  const syncKey = `thanos-trend-${cluster}-${namespace}-${pod ?? ''}`;
  const shownPods = cpuSeries.length;
  // v0.9.1042 — x-ekseni panelin kendi sorgu penceresine mıhlı
  // (ServiceCharts v0.9.725 paritesi; ns→unix sec).
  const xRange = { from: fromNs / 1e9, to: toNs / 1e9 };
  return (
    <div className="cl-trend">
      {!single && totalPods > shownPods && (
        <div className="cl-trend__note">
          Top {shownPods} of {totalPods} pods by average CPU.
        </div>
      )}
      {/* Madde 4 sweep — unit'ler eksen/tooltip'e iner: Memory ham 1e9
          rakamları yerine "1.2 GB" okur (fmtSmart 'bytes'); CPU "cores". */}
      <div>
        <div className="cl-trend__title">CPU (cores)</div>
        <MultiLineChart series={cpuSeries} height={180} syncKey={syncKey} unit="cores"
          deploys={deployMarkers} onZoom={onZoom} onZoomReset={onZoomReset} xRange={xRange}
          thresholds={single ? limitThresholds(row?.cpuLimitCores, row?.cpuRequestCores, 'cores') : undefined} />
      </div>
      <div>
        <div className="cl-trend__title">Memory (bytes)</div>
        <MultiLineChart series={memSeries} height={180} syncKey={syncKey} unit="bytes"
          deploys={deployMarkers} onZoom={onZoom} onZoomReset={onZoomReset} xRange={xRange}
          thresholds={single ? limitThresholds(row?.memLimitBytes, row?.memRequestBytes, 'bytes') : undefined} />
      </div>
    </div>
  );
}
