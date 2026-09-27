// TracePodCharts — v0.10.968 — seçili pod'un Bellek + CPU grafikleri (Trace ›
// Metrics yeniden tasarımı, mockup PodDetail.dc.html / Main.dc.html sağ panel).
//
// v0.10.976 — `layout="row"`: satır altı kompakt ayrıntıda iki grafik YAN
// YANA (`.tpp-chart-row`, içsel auto-fit: kap 2×240 px'ten darsa alt alta);
// odak görünümü `stack` (varsayılan, bellek üstte CPU altta). Sıra, zeroBase,
// band, limit ve kardeşler aynı. Her grafik kabı `role=group` + görünen
// başlıkla aynı `aria-label` (CorePanel'e title="" gider).
//
// v0.10.968 — Çizim TEK motordan: CorePanelMulti, MultiLineChart'ın yüklediği
// gibi lazy (`@/components/chart/corePanelEntry`; sayfa @grafana/data'ya
// statik bağlanmaz). Öğeler SAF çekirdekten (tracePodPanelModel.buildChartItems):
// karşılaştırılan pod'lar `data`, kardeşler `muted` (≤12, "+N çizilmedi"),
// trace bandı en az bir adım geniş (var(--accent2), "trace"), limit çizgisi
// yalnız limit bilinirken (var(--warn), "limit (şu an)"), iki panel de
// zeroBase. Bellek ÖNCE, CPU sonra (v0.10.916 operator-reported sırası).
//
// Lejant panelin kendi tablosu DEĞİL (hideLegend): tek satır — swatch + son
// ek + trace-anı değeri, "limit X (şu an)", "diğer N pod". Swatch rengi
// çizgi rengiyle AYNI kaynaktan; tema değişince useThemeTick yeniden çözer.
//
// v0.10.968 — renk ARTIK yalnız addan değil: karşılaştırma sırasıyla
// ÇAKIŞMASIZ atama (compareColors → seriesColorsFor). Hash-yalnız renkte iki
// pod aynı yuvaya düşüp (mockup'ın kendi p5r8d/b2q6f çifti) aynı çizgiyle
// çiziliyordu. Harita karşılaştırma listesinin TAMAMINDAN kurulur ki çip
// swatch'ı (TracePodPanel) ile aynı rengi versin.
// Senkron crosshair: `trace-metrics-<svc>` + msSyncKey (JVM paneli MLC
// üzerinden aynı ad alanına düşer).
import { lazy, Suspense, useMemo } from 'react';
import { Skeleton } from '@/components/Skeleton';
import { msSyncKey } from '@/lib/chart/syncNamespace';
import { useThemeTick } from '@/lib/useThemeTick';
import type { PodMetricState, TraceMetricsModel, TraceMetricsWindowInfo, TracePodInfo } from './traceMetricsModel';
import {
  buildChartItems, chartCaption, chartPods, compareColors, legendValue, limitLegend, servicePods, suffixLabels, type ChartBuild,
} from './tracePodPanelModel';

const CoreMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

export interface TracePodChartsProps {
  model: TraceMetricsModel;
  selected: TracePodInfo;
  compare: string[];
  metrics: (pod: string) => PodMetricState;
  window: TraceMetricsWindowInfo;
  siblingLines: boolean;
  height?: number;
  /** Odak görünümü: açıklama "Limitler bugünkü değerdir…" ile biter. */
  focus?: boolean;
  /** v0.10.976 — `row`: Bellek ve CPU yan yana (kompakt satır altı ayrıntı). */
  layout?: 'stack' | 'row';
}

const TITLE: Record<ChartBuild['kind'], string> = { mem: 'Bellek (working set)', cpu: 'CPU (çekirdek)' };
const UNIT: Record<ChartBuild['kind'], string> = { mem: 'bytes', cpu: 'cores' };

function PodChart({ build, height, syncKey, xRange }: {
  build: ChartBuild;
  height: number;
  syncKey: string;
  xRange: { from: number; to: number };
}) {
  useThemeTick();
  const k = build.kind;
  return (
    // v0.10.976 — grafik kabı adlandırılmış grup: CorePanel'e title="" gidiyor
    // (görsel başlık .tpp-sec-title'da), bu yüzden erişilebilir ad BURADAN;
    // TITLE[k] tek kaynak, görünen metinle ayrışamaz.
    <div className="tpp-chart" data-chart={k} role="group" aria-label={TITLE[k]}>
      <div className="tpp-sec-title"><span>{TITLE[k]}</span></div>
      <div className="tpp-legend">
        {build.legend.map(l => (
          <span key={l.pod} title={l.pod}>
            <span className="tpp-swatch" aria-hidden="true" style={{ background: l.color }} />
            {' '}<span className="mono">{l.label}</span> {legendValue(k, l.value)}
          </span>
        ))}
        {build.limit != null && <span>{limitLegend(k, build.limit)}</span>}
        {build.siblingsDrawn > 0 && <span>diğer {build.siblingsDrawn + build.siblingsHidden} pod</span>}
      </div>
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

export function TracePodCharts({ model, selected, compare, metrics, window: w, siblingLines, height = 140, focus, layout = 'stack' }: TracePodChartsProps) {
  const themeTick = useThemeTick();
  const pods = useMemo(() => chartPods(model, selected, compare, metrics), [model, selected, compare, metrics]);
  const labels = useMemo(() => suffixLabels(servicePods(model, selected.service)), [model, selected.service]);
  // themeTick: palet temaya göre çözülür (seriesColorsFor chartTheme okur).
  // eslint-disable-next-line react-hooks/exhaustive-deps -- v0.10.968: themeTick tema değişiminde yeniden çözme tetikleyicisi
  const colors = useMemo(() => compareColors(compare, labels), [compare, labels, themeTick]);
  const stepSec = pods.compared[0]?.data.stepSec ?? w.stepSec ?? 0;
  const [mem, cpu] = useMemo(() => {
    const inp = { ...pods, siblingLines, traceStartNs: w.startNs, traceEndNs: w.endNs, stepSec, colors };
    return [buildChartItems('mem', inp), buildChartItems('cpu', inp)];
  }, [pods, siblingLines, w.startNs, w.endNs, stepSec, colors]);
  const xRange = useMemo(() => ({ from: w.fromNs / 1e9, to: w.toNs / 1e9 }), [w.fromNs, w.toNs]);
  const syncKey = `trace-metrics-${selected.service}`;
  // v0.10.916 (operator-reported) — bellek üstte (satır kipinde solda), CPU altta (sağda); ikisi de zeroBase.
  const charts = (
    <>
      <PodChart build={mem} height={height} syncKey={syncKey} xRange={xRange} />
      <PodChart build={cpu} height={height} syncKey={syncKey} xRange={xRange} />
    </>
  );
  return (
    <div className="tpp-chart">
      {layout === 'row' ? <div className="tpp-chart-row">{charts}</div> : charts}
      <p className="tpp-caption">
        {chartCaption({
          durNs: w.endNs - w.startNs, stepSec: stepSec || null, service: selected.service,
          siblingsDrawn: mem.siblingsDrawn, siblingsHidden: mem.siblingsHidden, cpuNoLimit: cpu.noLimit, focus,
        })}
      </p>
    </div>
  );
}
