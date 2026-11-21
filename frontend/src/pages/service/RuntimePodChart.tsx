// RuntimePodChart — v0.10.2436 (operatör onayı 2026-11-21, Service › Metrics
// sekmesi madde 4–5). Runtime grafiklerinin TEK çizim kabuğu: Metrics
// sekmesinin uygulama metrik panelleriyle (DetailsMetricsSection) AYNI motor
// — CorePanelMulti (ipucu, Series listesi, Shift+tık sabitleme, sürükle-yakınlaş,
// çift-tık geri). Eski TimeSeriesPanel kartları burada bitti.
//
// Pod görünümü (runtimePodReduce): varsayılan medyan + en kötü pod + eşik üstü
// en çok 5 pod; "Tüm pod'lar" (?rtview=lines, URL kaynak, replace) her pod'u
// çizer. Seri pod etiketi taşımıyorsa tek çizgi + "pod etiketi yok" notu.
//
// CorePanel LAZY (DetailsMetricsSection emsali): @grafana/* statik bağlansa
// Service sayfasının vendor chunk'ı ~1 MB olurdu.
import { lazy, Suspense, useMemo } from 'react';
import { Link, useSearchParams } from 'react-router';
import { Spinner } from '@/components/Spinner';
import { useT } from '@/lib/i18n';
import { msSyncKey } from '@/lib/chart/syncNamespace';
import type { ChartThreshold, ChartTimeRegion } from '@/lib/chart/overlays';
import type { CorePanelMultiItem } from '@/components/chart/corePanelEntry';
import type { SpanMetricSeries } from '@/lib/types';
import { parseRtView, rtViewParam } from './runtimeHeapView';
import { reducePods, POD_REDUCE_MIN, type PodPoint, type PodSeries } from './runtimePodReduce';
import { smoothPoints } from './runtimeSmooth';

const CorePanelMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

/** Pod'a bağlı olmayan çizgi (sınır, tavan, servis ortalaması …). */
export interface RuntimeExtraLine { label: string; points: PodPoint[]; color?: string; dashed?: boolean }

/** Kart başlığındaki birim yazımı → CorePanel (Grafana) birim kimliği. */
export function runtimeUnitId(unit: string | undefined): string | undefined {
  switch ((unit ?? '').trim()) {
    case 'MB': return 'mbytes';
    case 'ms': return 'ms';
    case '%': return 'percent';
    case '/min': return 'cpm';
    case '/s': return 'cps';
    case 'MB/s': return 'MiBs';
    case '': return 'short';
    default: return undefined;
  }
}

/** `?rtview` görünümü: Tüm pod'lar (lines) + geçiş bağlantısının araması. */
export function useRuntimePodView() {
  const [params] = useSearchParams();
  const view = parseRtView(params.get('rtview'));
  const searchFor = (all: boolean) => {
    const p = new URLSearchParams(params);
    const v = rtViewParam({ ...view, lines: all });
    if (v) p.set('rtview', v); else p.delete('rtview');
    const q = p.toString();
    return q ? `?${q}` : '';
  };
  return { all: view.lines, view, searchFor };
}

// null nokta = boşluk (spanSeriesToFrames null'ı taşır; sıfır uydurulmaz).
const toSeries = (points: PodPoint[]): SpanMetricSeries => ({ groupKey: [], points: points as SpanMetricSeries['points'] });

const fmtThr = (v: number) => (Math.abs(v) >= 100 ? v.toFixed(0) : String(Number(v.toFixed(2))));

export function RuntimePodChart({
  title, storageKey, unit, service, from, to, pods, podless, extra, threshold, status, errorText,
  deploys, mode = 'line', note, source, testId, onZoom, onZoomReset, smooth = true,
}: {
  title: string;
  storageKey: string;
  /** Başlık birimi (' MB', '%', '/s' …). */
  unit?: string;
  service: string;
  from: number; // unix ns
  to: number;
  /** Pod başına seriler; pod boyutu olmayan kartta verilmez. */
  pods?: PodSeries[];
  /** Pod kırılımı istendi ama seriler pod etiketi taşımadı → tek çizgi + not. */
  podless?: boolean;
  extra?: RuntimeExtraLine[];
  /** Sinyalin mutlak eşiği (kartın biriminde); yoksa göreli eşik. */
  threshold?: number | null;
  status: 'loading' | 'error' | 'ready';
  errorText?: string;
  /** Deploy anları (unix ns). */
  deploys?: number[];
  mode?: 'line' | 'area';
  note?: string | null;
  source?: { label: string; tip: string };
  testId?: string;
  onZoom?: (fromSec: number, toSec: number) => void;
  onZoomReset?: () => void;
  smooth?: boolean;
}) {
  const tr = useT();
  const { all, searchFor } = useRuntimePodView();
  const red = useMemo(
    () => reducePods(pods ?? [], { threshold, all, medianLabel: tr('rt.pv.median') }),
    [pods, threshold, all, tr],
  );
  const items = useMemo<CorePanelMultiItem[]>(() => {
    const sm = smooth ? smoothPoints : (p: PodPoint[]) => p;
    return [
      ...red.lines.map(l => ({
        name: l.role === 'worst' ? `${l.label} · ${tr('rt.pv.worst')}` : l.label,
        series: [toSeries(sm(l.points))],
        // Medyan nötr; pod çizgileri ad-hash paleti (renk = kimlik, sapma değil).
        color: l.role === 'median' ? 'var(--text2)' : undefined,
      })),
      ...(extra ?? []).map(x => ({
        name: x.label, series: [toSeries(sm(x.points))], color: x.color, dashed: x.dashed,
      })),
    ];
  }, [red, extra, smooth, tr]);
  const empty = status === 'ready' && items.every(it => it.series[0].points.length === 0);
  const xRange = useMemo(() => ({ from: from / 1e9, to: to / 1e9 }), [from, to]);
  const regions = useMemo<ChartTimeRegion[] | undefined>(() => {
    if (!deploys?.length) return undefined;
    const spanSec = Math.max(1, (to - from) / 1e9);
    return deploys.map(ns => ({ fromSec: ns / 1e9, toSec: ns / 1e9 + spanSec * 0.004, color: 'var(--purple)', label: '▼ deploy' }));
  }, [deploys, from, to]);
  const thresholds = useMemo<ChartThreshold[] | undefined>(
    () => (red.thresholdKind === 'signal' && red.threshold != null
      ? [{ value: red.threshold, label: `${tr('rt.pv.threshold')} ${fmtThr(red.threshold)}${(unit ?? '').trim()}` }]
      : undefined),
    [red.thresholdKind, red.threshold, unit, tr]);

  const u = (unit ?? '').trim();
  const canToggle = !podless && red.total >= POD_REDUCE_MIN;
  const summary = podless ? null
    : red.reduced
      ? `${tr('rt.pv.reduced').replace('{n}', String(red.total))}${red.overCount > 0
        ? ` · ${tr('rt.pv.over').replace('{n}', String(red.overCount))}${red.threshold != null ? ` (> ${fmtThr(red.threshold)}${u}${red.thresholdKind === 'relative' ? ` · ${tr('rt.pv.relative')}` : ''})` : ''}`
        : ''}`
      : red.total > 0 ? `${red.total} pod` : null;

  return (
    <div data-testid={testId}>
      {(summary || podless || canToggle || note) && (
        <div className="rt-pod-bar">
          {summary && <span data-testid="rt-pod-summary">{summary}</span>}
          {podless && <span data-testid="rt-no-pod-label" title={tr('rt.pv.noPodLabelTip')}>{tr('rt.pv.noPodLabel')}</span>}
          {note && <span>{note}</span>}
          {canToggle && (
            <Link replace to={{ search: searchFor(!all) }} data-testid="rt-pod-toggle">
              {all ? tr('rt.pv.summary') : tr('rt.pv.all')}
            </Link>
          )}
        </div>
      )}
      <Suspense fallback={<div className="rt-slot"><Spinner /></div>}>
        <CorePanelMultiLazy
          title={title}
          storageKey={storageKey}
          height={220}
          unit={runtimeUnitId(unit)}
          viz={mode}
          xRange={xRange}
          items={items}
          loading={status === 'loading' && items.length === 0}
          error={status === 'error' && items.length === 0 ? (errorText ?? tr('rt.err')) : undefined}
          emptyReason={empty || (status !== 'loading' && status !== 'error' && items.length === 0) ? tr('rt.noData') : undefined}
          thresholds={thresholds}
          regions={regions}
          queryText={source ? `${source.label} · ${source.tip}` : undefined}
          onZoom={onZoom}
          onZoomReset={onZoomReset}
          syncKey={msSyncKey(`runtime:${service}`)}
        />
      </Suspense>
    </div>
  );
}
