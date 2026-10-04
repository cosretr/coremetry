// usePodChartBuilds — v0.10.1096 — Trace › Metrics seçili pod'un Bellek + CPU
// grafik öğeleri TEK yerde kurulur: odak görünümü (TracePodCharts, alt alta)
// ve satır altı ayrıntı (TracePodPanel: grafikler üstte yan yana, açıklama
// kapalı "Teknik ayrıntı"da) aynı kurulumu paylaşır — ayrıntı paneli grafiği
// ve katlanan açıklamayı aynı öğelerden çizer, sayılar ayrışamaz.
//
// Bileşen dosyası (TracePodCharts.tsx) yalnız bileşen dışa aktarır (Fast
// Refresh); kanca ve açıklama burada (usePanelNote.ts emsali).
//
// v0.10.968 kuralları aynen: renk karşılaştırma sırasıyla çakışmasız
// (compareColors), tema değişince yeniden çözülür (useThemeTick); senkron
// crosshair anahtarı `trace-metrics-<svc>`.
import { useMemo } from 'react';
import { useThemeTick } from '@/lib/useThemeTick';
import type { PodMetricState, TraceMetricsModel, TraceMetricsWindowInfo, TracePodInfo } from './traceMetricsModel';
import {
  buildChartItems, chartCaption, chartPods, compareColors, servicePods, suffixLabels, type ChartBuild,
} from './tracePodPanelModel';

export interface PodChartInputs {
  model: TraceMetricsModel;
  selected: TracePodInfo;
  compare: string[];
  metrics: (pod: string) => PodMetricState;
  window: TraceMetricsWindowInfo;
  siblingLines: boolean;
}

export interface PodChartBuilds {
  mem: ChartBuild;
  cpu: ChartBuild;
  stepSec: number;
  xRange: { from: number; to: number };
  syncKey: string;
}

/** v0.10.1096 — iki grafiğin öğeleri. Metrik ok değilse öğeler boş — çağıran
 *  grafiği çizmez (durum kutusu). */
export function usePodChartBuilds({ model, selected, compare, metrics, window: w, siblingLines }: PodChartInputs): PodChartBuilds {
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
  return { mem, cpu, stepSec, xRange, syncKey: `trace-metrics-${selected.service}` };
}

/** v0.10.968 — grafik altı açıklama ("Mavi bant: trace …"); v0.10.1096'te satır
 *  altı ayrıntıda kapalı "Teknik ayrıntı"da, odak görünümünde grafiklerin altında. */
export function podChartsCaption(b: PodChartBuilds, w: TraceMetricsWindowInfo, service: string, focus?: boolean): string {
  return chartCaption({
    durNs: w.endNs - w.startNs, stepSec: b.stepSec || null, service,
    siblingsDrawn: b.mem.siblingsDrawn, siblingsHidden: b.mem.siblingsHidden, cpuNoLimit: b.cpu.noLimit, focus,
  });
}
