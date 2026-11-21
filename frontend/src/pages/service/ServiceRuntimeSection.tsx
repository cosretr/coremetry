// ServiceRuntimeSection — v0.10.1206: eski Pods sekmesinin runtime bölümü
// (OTel dil-runtime heap/GC/threads + JVM heap bandı; aile yoksa pod başına
// Thanos kaynak grafikleri). v0.10.2436 (operatör onayı 2026-11-21): Infra
// sekmesinden Service › Metrics sekmesinin İLK bölümüne taşındı; Infra'da
// yalnız "Runtime → Metrics" bağlantısı kaldı. Thanos pod envanteri
// (useServicePods) yalnız genel kaynak grafikleri gerektiğinde okunur.
//
// 2026-11-21 (operatör: ".NET servis JVM metrikleri göstermemeli") — aile TEK
// tespitten (runtimeFamily.useRuntimeFamily: dil → runtime adı → sunucu metrik
// kataloğu). JVM yüzeyleri (heap kartları, heap bandı) yalnız JVM'de; .NET'te
// DotnetRuntimePanel (lazy); Node / Python / bilinmeyen → genel pod kaynak
// grafikleri.
import { lazy, Suspense, useMemo } from 'react';
import { SectionHead } from '@/components/coremetry/SectionHead';
import { Spinner } from '@/components/Spinner';
import { useT } from '@/lib/i18n';
import { RuntimeCharts } from './RuntimeCharts';
import { HeapBaselineCard } from './HeapBaselineCard'; // v0.10.887 — paritesi #4 dilim 1
import { PodResourceCharts } from './PodResourceCharts';
import { useRuntimeFamily, useRuntimeProfile, useRuntimeScope, type RuntimeFamily } from './runtimeFamily';
import { useServicePods } from './useServicePods';
import { timeRangeToNs } from '@/lib/utils';
import type { TimeRange } from '@/lib/types';

const DotnetRuntimePanel = lazy(() => import('./DotnetRuntimePanel'));
// Ortak süreç sinyalleri (dosya tanıtıcısı / HttpClient / Kafka) JVM / Go /
// Node / bilinmeyen serviste de — aynı lazy chunk'tan yalnız liste.
const RuntimeSignalList = lazy(() => import('./DotnetRuntimePanel').then(m => ({ default: m.RuntimeSignalList })));

// Follow-up 2026-11-21 — giden çağrılar · hedefe göre (VM client histogramı; lazy).
const DownstreamTargetsTable = lazy(() => import('./DownstreamTargetsTable'));
// Follow-up 2026-11-21 — HikariCP havuz kartları (varlık pod'larıyla; lazy).
const RuntimePoolCards = lazy(() => import('./RuntimePoolCards'));

const FAMILY_LABEL: Record<RuntimeFamily, string> = { jvm: 'JVM', dotnet: '.NET', go: 'Go', nodejs: 'Node.js', python: 'Python' };

/** Go runtimesig/common.go ortak sinyalleri — "runtime'ın KENDİ ailesi var mı" sayımına girmez. */
const COMMON_SIGNALS = new Set(['fd_open', 'fd_max', 'http_pending', 'http_leased', 'http_pool_max',
  'kafka_waiting_threads', 'kafka_requests_in_flight', 'kafka_buffer_exhausted', 'kafka_records_lag_max',
  // Follow-up 2026-11-21 — HikariCP (pod kapsamlı, runtimesig/hikari.go).
  'db_pool_active', 'db_pool_max', 'db_pool_pending', 'db_acquire_sum', 'db_acquire_count']);

/** SAF — profilde HikariCP havuz sinyali var mı (kartlar yalnız o zaman yüklenir). */
export function hasDBPool(signals: { signal: string; podScoped?: boolean }[] | undefined): boolean {
  return (signals ?? []).some(s => s.podScoped && s.signal.startsWith('db_pool'));
}

/** SAF — profil runtime'ın kendi metrik ailesini taşıyor mu (ortak süreç sinyalleri hariç). */
export function hasOwnRuntimeMetrics(signals: { signal: string }[] | undefined): boolean {
  return (signals ?? []).some(s => !COMMON_SIGNALS.has(s.signal));
}

/** SAF — ailenin runtime yüzeyleri (test pini: .NET'te JVM yok, tersi de). ownMetrics: Go / Python'un kendi ailesi. */
export function runtimeSurfaces(family: RuntimeFamily | null, ownMetrics = true): { jvmCharts: boolean; heapBand: boolean; dotnet: boolean; goCharts: boolean; python: boolean; podResources: boolean } {
  return {
    jvmCharts: family === 'jvm',
    heapBand: family === 'jvm',
    dotnet: family === 'dotnet',
    goCharts: family === 'go' && ownMetrics,
    python: family === 'python' && ownMetrics,
    podResources: family === null || family === 'nodejs' || ((family === 'go' || family === 'python') && !ownMetrics),
  };
}

// Genel kaynak grafikleri (Thanos pod başına) — Thanos cluster taraması
// yalnız bu yedek yol çizilirken (runtime ailesi yokken) yapılır.
function PodResourcesFallback({ service, range, onZoom, onZoomReset }: {
  service: string;
  range: TimeRange;
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  onZoomReset?: () => void;
}) {
  const th = useServicePods(service, range);
  return (
    <PodResourceCharts service={service} cluster={th.clustersWithPods[0] ?? ''}
      ns={th.effNs} deploy={th.effDeploy} cFrom={th.cFrom} cTo={th.cTo} clamped={th.clamped}
      onZoom={onZoom} onZoomReset={onZoomReset} />
  );
}

export function ServiceRuntimeSection({ service, range, onZoom, onZoomReset }: {
  service: string;
  range: TimeRange;
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  onZoomReset?: () => void;
}) {
  const tr = useT();
  // Sayfanın çözülmüş penceresi (unix ns) — RQ anahtarları hizalı; bare JSX'te değil.
  const th = useMemo(() => timeRangeToNs(range), [range]);
  const { family, pending: famPending, language } = useRuntimeFamily(service);
  // Profil (sunucu 60 s önbellekli, panel aynı RQ anahtarını okur): .NET kuşağı
  // rozeti, Go / Python'un kendi ailesi var mı, ortak süreç sinyalleri.
  const profQ = useRuntimeProfile(service, !famPending);
  const needOwn = family === 'go' || family === 'python';
  const ownMetrics = !needOwn || hasOwnRuntimeMetrics(profQ.data?.profile?.signals);
  const surf = runtimeSurfaces(family, ownMetrics);
  const pending = famPending || (needOwn && profQ.isPending);
  const gen = profQ.data?.profile?.dotnetGen;
  const sigs = profQ.data?.signals ?? [];
  const panelRuntime = surf.dotnet ? 'dotnet' : surf.python ? 'python' : null;
  // Follow-up 2026-11-21 — Topbar ortam / küme kapsamı: sunucu profil metriğiyle
  // çözer (cluster → ortam varlık haritası dahil); grafikler kapsam gelene
  // kadar beklemez yalnız kapsam istenmişse (kapsamsız → kapsamlı titreme yok).
  const scope = useRuntimeScope();
  const scopeReady = !(scope.env || scope.cluster) || !profQ.isPending;
  const scopeNote = profQ.data?.scopeNote;
  return (
    <>
      <SectionHead id="runtime-sec" title="Runtime" source={panelRuntime ? 'OTel runtime · VM / metric_points' : 'OTel · metric_points'}
        badges={<>
          {family && <span className="badge b-info">{FAMILY_LABEL[family]}</span>}
          {surf.dotnet && gen && <span className="badge b-gray mono" title={tr('rt.gen.hint')}>{gen === 'semconv' ? 'dotnet.*' : gen === 'instrumentation' ? 'process.runtime.dotnet.*' : tr('rt.gen.mixed')}</span>}
          <span className="badge b-gray" title="Bu bölümün kaynağı: OTel runtime metrikleri — Thanos düşse de burası çalışır.">Thanos&#39;tan bağımsız</span>
        </>}
        meta={language ? <span className="mono">{language}</span> : undefined} />
      {scopeNote && <div className="kc-line" data-testid="rt-scope-note">◌ {scopeNote}</div>}
      {!panelRuntime && sigs.length > 0 && profQ.data && (
        <Suspense fallback={null}>
          <RuntimeSignalList signals={sigs} mode={profQ.data.mode} />
        </Suspense>
      )}
      {(surf.jvmCharts || surf.goCharts) && scopeReady && (
        <RuntimeCharts service={service} family={family} from={th.from} to={th.to} onZoom={onZoom} onZoomReset={onZoomReset} hideHeader
          scopeFilters={profQ.data?.scopeFilters} />
      )}
      {/* v0.10.887 (paritesi #4 dilim 1, Onay 2026-09-23) — yalnız JVM: heap-after-GC pod
          başına adaptif bant; problem açmaz; JVM pod / metrik yoksa kart yok. */}
      {surf.heapBand && (
        <HeapBaselineCard service={service} from={th.from} to={th.to} onZoom={onZoom} onZoomReset={onZoomReset} />
      )}
      {panelRuntime && (
        <Suspense fallback={<Spinner />}>
          <DotnetRuntimePanel service={service} runtime={panelRuntime} from={th.from} to={th.to} onZoom={onZoom} onZoomReset={onZoomReset} />
        </Suspense>
      )}
      {hasDBPool(profQ.data?.profile?.signals) && (
        <Suspense fallback={null}>
          <RuntimePoolCards service={service} from={th.from} to={th.to} />
        </Suspense>
      )}
      <Suspense fallback={null}>
        <DownstreamTargetsTable service={service} from={th.from} to={th.to} env={scope.env} cluster={scope.cluster} />
      </Suspense>
      {!pending && surf.podResources && (
        <div className="kc-line" title="Dil-runtime ailesinin kendi metrik ailesi yok — yerine pod başına Thanos kaynak grafikleri.">
          ◌ {tr('rt.generic')}{family ? ` (${FAMILY_LABEL[family]})` : language ? ` (${language})` : ''}
        </div>
      )}

      {/* v0.9.546 — dil-runtime ailesi grafik taşımıyorsa pod başına CPU/bellek/ağ
          (Thanos deploy-trend byPod). Aile VARSA çizilmez — iki kaynak yan
          yana aynı şeyi göstermesin. */}
      {!pending && surf.podResources && (
        <PodResourcesFallback service={service} range={range} onZoom={onZoom} onZoomReset={onZoomReset} />
      )}
    </>
  );
}

// Service.tsx Metrics sekmesi lazy yükler (uPlot / CorePanel zinciri ilk JS'e girmez).
export default ServiceRuntimeSection;
