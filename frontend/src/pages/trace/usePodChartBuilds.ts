// usePodChartBuilds — v0.10.1096 — Trace › Metrics seçili pod'un Bellek + CPU
// grafik öğeleri TEK yerde kurulur: satır altı ayrıntı (TracePodPanel)
// grafiği ve katlanan açıklamayı aynı öğelerden çizer, sayılar ayrışamaz.
//
// Bileşen dosyası (TracePodCharts.tsx) yalnız bileşen dışa aktarır (Fast
// Refresh); kanca ve açıklama burada.
//
// v0.10.968 kuralları aynen: renk tema değişince yeniden çözülür
// (useThemeTick); senkron crosshair anahtarı `trace-metrics-<svc>`.
// v0.10.2207 — yalnız seçili pod çizilir (karşılaştırma / kardeş çizgileri
// ve odak görünümü kalktı).
import { useMemo } from 'react';
import { useThemeTick } from '@/lib/useThemeTick';
import type { PodMetricState, TraceMetricsModel, TraceMetricsWindowInfo, TracePodInfo } from './traceMetricsModel';
import { buildChartItems, chartCaption, chartPod, podColors, type ChartBuild } from './tracePodPanelModel';
import { useLang, type Lang } from '@/lib/i18n';

export interface PodChartInputs {
  model: TraceMetricsModel;
  selected: TracePodInfo;
  metrics: (pod: string) => PodMetricState;
  window: TraceMetricsWindowInfo;
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
export function usePodChartBuilds({ selected, metrics, window: w }: PodChartInputs): PodChartBuilds {
  const themeTick = useThemeTick();
  const lang = useLang(); // v0.10.2316 — limit çizgisi etiketi arayüz dilinde
  const pod = useMemo(() => chartPod(selected, metrics), [selected, metrics]);
  // themeTick: palet temaya göre çözülür (seriesColorsFor chartTheme okur).
  // eslint-disable-next-line react-hooks/exhaustive-deps -- v0.10.968: themeTick tema değişiminde yeniden çözme tetikleyicisi
  const colors = useMemo(() => podColors(selected.pod), [selected.pod, themeTick]);
  const stepSec = pod?.data.stepSec ?? w.stepSec ?? 0;
  const [mem, cpu] = useMemo(() => {
    const inp = { pod, traceStartNs: w.startNs, traceEndNs: w.endNs, stepSec, colors, lang };
    return [buildChartItems('mem', inp), buildChartItems('cpu', inp)];
  }, [pod, w.startNs, w.endNs, stepSec, colors, lang]);
  const xRange = useMemo(() => ({ from: w.fromNs / 1e9, to: w.toNs / 1e9 }), [w.fromNs, w.toNs]);
  return { mem, cpu, stepSec, xRange, syncKey: `trace-metrics-${selected.service}` };
}

/** v0.10.968 — grafik altı açıklama ("Mavi bant: trace …"); satır altı
 *  ayrıntıda kapalı "Teknik ayrıntı"da. */
export function podChartsCaption(b: PodChartBuilds, w: TraceMetricsWindowInfo, lang: Lang = 'tr'): string {
  return chartCaption({ durNs: w.endNs - w.startNs, stepSec: b.stepSec || null, cpuNoLimit: b.cpu.noLimit }, lang);
}
