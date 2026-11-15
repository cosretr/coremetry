// TracePodCharts — v0.10.968 — seçili pod'un Bellek + CPU grafik kabı (Trace ›
// Metrics, satır altı ayrıntı). Panel `usePodChartBuilds` + `PodChart`'ı
// doğrudan dizer (Bellek, CPU, varsa JVM heap/GC aynı `.tpp-chart-row`
// ızgarasında); başlık TEK satır ("Bellek 9,03 GiB · limit 16 GiB", `head`).
//
// v0.10.968 — Çizim TEK motordan: CorePanelMulti, lazy
// (`@/components/chart/corePanelEntry`; sayfa @grafana/data'ya statik
// bağlanmaz). Öğeler SAF çekirdekten (tracePodPanelModel.buildChartItems):
// trace bandı en az bir adım geniş (var(--accent2), "trace"), limit çizgisi
// yalnız limit bilinirken (var(--warn), "limit (şu an)"), zeroBase. Senkron
// crosshair: `trace-metrics-<svc>` + msSyncKey.
//
// v0.10.2207 — lejant satırı (pod adı + aynı değer, "diğer N pod") kalktı:
// grafik yalnız seçili pod, değer ve limit başlıkta. Odak görünümünün alt
// alta `TracePodCharts`ı da kalktı.
import { lazy, Suspense, type ReactNode } from 'react';
import { Skeleton } from '@/components/Skeleton';
import { msSyncKey } from '@/lib/chart/syncNamespace';
import { useThemeTick } from '@/lib/useThemeTick';
import type { ChartBuild } from './tracePodPanelModel';

const CoreMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

const CHART_TITLE: Record<ChartBuild['kind'], string> = { mem: 'Bellek (working set)', cpu: 'CPU (çekirdek)' };
const UNIT: Record<ChartBuild['kind'], string> = { mem: 'bytes', cpu: 'cores' };

/** v0.10.968 — tek grafik kabı (başlık + CorePanelMulti). */
export function PodChart({ build, height, syncKey, xRange, head }: {
  build: ChartBuild;
  height: number;
  syncKey: string;
  xRange: { from: number; to: number };
  /** v0.10.1096 — tek satır başlık (değer + limit); verilmezse CHART_TITLE. */
  head?: ReactNode;
}) {
  useThemeTick();
  const k = build.kind;
  return (
    // v0.10.976 — grafik kabı adlandırılmış grup: CorePanel'e title="" gidiyor
    // (görsel başlık .tpp-sec-title'da), bu yüzden erişilebilir ad BURADAN;
    // CHART_TITLE[k] tek kaynak, görünen metinle ayrışamaz.
    <div className="tpp-chart" data-chart={k} role="group" aria-label={CHART_TITLE[k]}>
      <div className="tpp-sec-title">{head ?? <span>{CHART_TITLE[k]}</span>}</div>
      <Suspense fallback={<Skeleton height={height} />}>
        <CoreMultiLazy
          title=""
          storageKey={`trace-pod-${k}`}
          height={height}
          unit={UNIT[k]}
          items={build.items}
          regions={build.regions}
          thresholds={build.thresholds}
          xRange={xRange}
          syncKey={msSyncKey(syncKey)}
          hideLegend
          zeroBase
        />
      </Suspense>
    </div>
  );
}
