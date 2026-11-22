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
// v0.10.2443 — sağ eksen bindirmesi (`right`: servis p99'u, kesikli, sağ
// eksen, etiketli) ve nesil kartlarının pod kırılımı (`gens`,
// runtimeGenReduce): varsayılan nesil başına pod medyanı; seçici
// (`?rtgen=<pod>`, en kötüden ≤10 pod) o pod'un nesil çizgilerine geçer;
// "Tüm pod'lar" (?rtview=lines) her pod × nesil.
//
// v0.10.2464 (operatör-raporlu, 53 pod'lu prod servis) — (1) DÜZ ÇİZGİ:
// CorePanelMulti'ye `plain` (viz='line' tek başına dolgusuz değildi; her seri
// %12 degrade alan dolgusu basıyordu → "altı taralı"). (2) Nesil kartları
// v0.10.2446'nın "parametresiz = Tüm pod'lar" varsayılanını ALMAZ: kendi
// parametreleri var — varsayılan nesil başına pod medyanı (nesil sayısı kadar
// çizgi), `?rtgen=<pod>` o pod'un nesilleri, `?rtgenview=all` pod × nesil.
// `?rtview` nesil kartlarını etkilemez (yokluğu "hepsi" demek; 159 çizgi).
//
// CorePanel LAZY (DetailsMetricsSection emsali): @grafana/* statik bağlansa
// Service sayfasının vendor chunk'ı ~1 MB olurdu.
import { FIELD_SURFACE } from '@/components/coremetry/Field/fieldSurface';
import { lazy, Suspense, useMemo } from 'react';
import { Link, useSearchParams } from 'react-router';
import { Spinner } from '@/components/Spinner';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { urlSpec, zParam, z, useUrlParams } from '@/lib/urlSchema';
import { useT } from '@/lib/i18n';
import { msSyncKey } from '@/lib/chart/syncNamespace';
import type { ChartThreshold, ChartTimeRegion } from '@/lib/chart/overlays';
import type { CorePanelMultiItem } from '@/components/chart/corePanelEntry';
import type { SpanMetricSeries } from '@/lib/types';
import { parseRtView, rtViewParam } from './runtimeHeapView';
import { reducePods, POD_REDUCE_MIN, type PodPoint, type PodSeries } from './runtimePodReduce';
import { smoothPoints } from './runtimeSmooth';
import { reduceGenerations, genPodChoices, type GenPodSeries } from './runtimeGenReduce';

const CorePanelMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

/** Pod'a bağlı olmayan çizgi (sınır, tavan, servis ortalaması …). */
export interface RuntimeExtraLine { label: string; points: PodPoint[]; color?: string; dashed?: boolean }
/** Sağ eksen bindirmesi (ör. servis p99'u) — kendi birimi, kesikli. */
export interface RuntimeRightLine { label: string; unit: string; points: PodPoint[] }

// `?rtgen=<pod>` — nesil kartlarının seçili pod'u (boş = medyan). Uzunluk
// tavanlı: sunucuya gitmez ama URL'e sınırsız metin yazılmaz.
// `?rtgenview=all` — v0.10.2464: nesil kartlarında pod × nesil (açık seçim);
// yok = medyan. Global `?rtview` varsayılanından bağımsız.
const GEN_SPEC = urlSpec({
  rtgen: zParam(z.string().check(z.maxLength(256)), ''),
  rtgenview: zParam(z.string().check(z.maxLength(16)), ''),
});

/** Nesil kartı geçişinin araması: `?rtgenview=all` ↔ yok (pod seçimi düşer). SAF. */
export function genViewSearch(search: string, all: boolean): string {
  const p = new URLSearchParams(search);
  p.delete('rtgen');
  if (all) p.set('rtgenview', 'all'); else p.delete('rtgenview');
  const q = p.toString();
  return q ? `?${q}` : '';
}

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
  deploys, note, source, testId, onZoom, onZoomReset, smooth = true, right, gens,
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
  /** v0.10.2446: yok sayılır — tüm runtime pod grafikleri düz çizgi (alan dolgusu yok). */
  mode?: 'line' | 'area';
  note?: string | null;
  source?: { label: string; tip: string };
  testId?: string;
  onZoom?: (fromSec: number, toSec: number) => void;
  onZoomReset?: () => void;
  smooth?: boolean;
  /** v0.10.2443 — sağ eksen bindirmesi (kesikli, kendi birimi, etiketli eksen). */
  right?: RuntimeRightLine | null;
  /** v0.10.2443 — nesil kırılımlı kart: pod × nesil serileri (verilirse `pods` yok sayılır). */
  gens?: GenPodSeries[];
}) {
  const tr = useT();
  const { all, searchFor } = useRuntimePodView();
  const [params] = useSearchParams();
  const [{ rtgen, rtgenview }, setGen] = useUrlParams(GEN_SPEC);
  const genAll = rtgenview === 'all';
  const red = useMemo(
    () => reducePods(gens ? [] : (pods ?? []), { threshold, all, medianLabel: tr('rt.pv.median') }),
    [pods, gens, threshold, all, tr],
  );
  const gred = useMemo(
    () => (gens ? reduceGenerations(gens, { pod: rtgen || null, all: genAll, medianSuffix: tr('rt.pv.median') }) : null),
    [gens, rtgen, genAll, tr],
  );
  const items = useMemo<CorePanelMultiItem[]>(() => {
    const sm = smooth ? smoothPoints : (p: PodPoint[]) => p;
    const main: CorePanelMultiItem[] = gred
      ? gred.lines.map(l => ({ name: l.label, series: [toSeries(sm(l.points))] }))
      : red.lines.map(l => ({
        name: l.role === 'worst' ? `${l.label} · ${tr('rt.pv.worst')}` : l.label,
        series: [toSeries(sm(l.points))],
        // Medyan nötr; pod çizgileri ad-hash paleti (renk = kimlik, sapma değil).
        color: l.role === 'median' ? 'var(--text2)' : undefined,
      }));
    return [
      ...main,
      ...(extra ?? []).map(x => ({
        name: x.label, series: [toSeries(sm(x.points))], color: x.color, dashed: x.dashed,
      })),
      // Sağ eksen: kesikli + nötr; boş seri item üretmez (tek eksenli kart bayt-aynı).
      ...(right && right.points.length ? [{
        name: right.label, series: [toSeries(sm(right.points))], color: 'var(--text3)', dashed: true,
        axis: 'right' as const, unit: runtimeUnitId(right.unit),
      }] : []),
    ];
  }, [red, gred, extra, right, smooth, tr]);
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
  const podTotal = gred ? gred.total : red.total;
  const canToggle = !podless && podTotal >= POD_REDUCE_MIN;
  // Nesil kartında geçiş kendi parametresini yazar (?rtgenview), ?rtview'e dokunmaz.
  const shownAll = gred ? genAll : all;
  const toggleSearch = gred ? genViewSearch(params.toString(), !genAll) : searchFor(!all);
  const genChoices = gred && !genAll && !podless && gred.total >= POD_REDUCE_MIN ? genPodChoices(gred.pods, gred.selected) : null;
  const summary = podless ? null
    : gred
      ? (gred.reduced ? tr('rt.gv.median').replace('{n}', String(gred.total))
        : gred.total > 0 ? `${gred.total} pod` : null)
    : red.reduced
      ? `${tr('rt.pv.reduced').replace('{n}', String(red.total))}${red.overCount > 0
        ? ` · ${tr('rt.pv.over').replace('{n}', String(red.overCount))}${red.threshold != null ? ` (> ${fmtThr(red.threshold)}${u}${red.thresholdKind === 'relative' ? ` · ${tr('rt.pv.relative')}` : ''})` : ''}`
        : ''}`
      : red.total > 0 ? `${red.total} pod` : null;

  return (
    <div data-testid={testId}>
      {(summary || podless || canToggle || note || genChoices) && (
        <div className="rt-pod-bar">
          {summary && <span data-testid="rt-pod-summary">{summary}</span>}
          {podless && <span data-testid="rt-no-pod-label" title={tr('rt.pv.noPodLabelTip')}>{tr('rt.pv.noPodLabel')}</span>}
          {note && <span>{note}</span>}
          {genChoices && (
            <NativeSelect size="sm" value={gred?.selected ?? ''} data-testid="rt-gen-pod" className={FIELD_SURFACE}
              aria-label={tr('rt.gv.pick')} title={tr('rt.gv.pick')}
              onChange={e => setGen({ rtgen: e.target.value })}>
              <NativeSelectOption value="">{tr('rt.gv.medianOpt')}</NativeSelectOption>
              {genChoices.map((p, i) => (
                <NativeSelectOption key={p} value={p}>{i === 0 ? `${p} · ${tr('rt.pv.worst')}` : p}</NativeSelectOption>
              ))}
            </NativeSelect>
          )}
          {canToggle && (
            <Link replace to={{ search: toggleSearch }} data-testid="rt-pod-toggle">
              {shownAll ? tr('rt.pv.summary') : tr('rt.pv.all')}
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
          viz={'line'}
          plain /* v0.10.2464 (operatör): düz çizgi — seri dolgusu yok, pod serisine kesikli desen yok */
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
          rightAxisLabel={right && right.points.length ? `${right.label}${right.unit ? ` (${right.unit.trim()})` : ''}` : undefined}
        />
      </Suspense>
    </div>
  );
}
