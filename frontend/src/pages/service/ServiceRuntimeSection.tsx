// ServiceRuntimeSection — v0.10.1206: eski Pods sekmesinin runtime bölümü
// (OTel dil-runtime heap/GC/threads + JVM heap bandı; aile yoksa pod başına
// Thanos kaynak grafikleri), Infrastructure sekmesinin altında.
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { SectionHead } from '@/components/coremetry/SectionHead';
import { RuntimeCharts, familyOf } from './RuntimeCharts';
import { HeapBaselineCard } from './HeapBaselineCard'; // v0.10.887 — paritesi #4 dilim 1
import { PodResourceCharts } from './PodResourceCharts';
import type { useServicePods } from './useServicePods';

const FAMILY_LABEL: Record<string, string> = { jvm: 'JVM', dotnet: '.NET', go: 'Go' };

export function ServiceRuntimeSection({ service, th, onZoom, onZoomReset }: {
  service: string;
  th: Pick<ReturnType<typeof useServicePods>, 'from' | 'to' | 'clustersWithPods' | 'effNs' | 'effDeploy' | 'cFrom' | 'cTo' | 'clamped'>;
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  onZoomReset?: () => void;
}) {
  // v0.9.546 — dil-runtime ailesi var mı? RuntimeCharts ile AYNI RQ
  // anahtarı, yani ek istek yok (cache'ten dolar). Aile yoksa aşağıda
  // Thanos kaynak grafikleri devreye giriyor.
  const runtimeQ = useQuery({
    queryKey: ['svc-runtime', service],
    queryFn: () => api.serviceRuntime(service),
    enabled: !!service, staleTime: 300_000,
  });
  const runtimeFamily = familyOf(runtimeQ.data?.language);
  return (
    <>
      {/* OTel dil-runtime (heap/GC/threads by pod) — servis-scoped, her zaman;
          Thanos'tan tamamen bağımsız. */}
      <SectionHead id="runtime-sec" title="Runtime" source="OTel · metric_points"
        badges={<>
          {runtimeFamily && <span className="badge b-info">{FAMILY_LABEL[runtimeFamily] ?? runtimeFamily}</span>}
          <span className="badge b-gray" title="Bu bölümün kaynağı: OTel runtime metrikleri (ClickHouse) — Thanos düşse de burası çalışır.">Thanos&#39;tan bağımsız</span>
        </>}
        meta={runtimeQ.data?.language ? <span className="mono">{runtimeQ.data.language}</span> : undefined} />
      <RuntimeCharts service={service} from={th.from} to={th.to} onZoom={onZoom} onZoomReset={onZoomReset} hideHeader />
      {/* v0.10.887 (paritesi #4 dilim 1, Onay 2026-09-23) — yalnız JVM: heap-after-GC pod
          başına adaptif bant; problem açmaz; JVM pod / metrik yoksa kart yok. */}
      {runtimeFamily === 'jvm' && (
        <HeapBaselineCard service={service} from={th.from} to={th.to} onZoom={onZoom} onZoomReset={onZoomReset} />
      )}
      {!runtimeQ.isPending && !runtimeFamily && (
        <div className="kc-line" title="Dil-runtime ailesi tanınmıyor (Node.js, Python ya da dil raporlanmıyor) — yerine pod başına Thanos kaynak grafikleri.">
          ◌ runtime ailesi yok{runtimeQ.data?.language ? ` (${runtimeQ.data.language})` : ''} — aşağıda Thanos kaynak grafikleri
        </div>
      )}

      {/* v0.9.546 — dil-runtime ailesi TANINMIYORSA pod başına CPU/bellek/ağ
          (Thanos deploy-trend byPod). Aile VARSA çizilmez — iki kaynak yan
          yana aynı şeyi göstermesin. */}
      {!runtimeFamily && (
        <PodResourceCharts service={service} cluster={th.clustersWithPods[0] ?? ''}
          ns={th.effNs} deploy={th.effDeploy} cFrom={th.cFrom} cTo={th.cTo} clamped={th.clamped}
          onZoom={onZoom} onZoomReset={onZoomReset} />
      )}
    </>
  );
}
