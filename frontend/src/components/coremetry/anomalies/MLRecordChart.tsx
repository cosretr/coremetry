// MLRecordChart — v0.10.2247 (anomali kartı Dilim G, audit §10 G; mockup
// D0/G0 onaylı, K4 = A): kartın TEK grafiği. Yalnız olay kaydındaki seri —
// dedektörün yargıladığı seri — gerçek kova boyunda çizilir: gölgeli beklenen
// bant, kesikli beklenen değer, bant dışı kovalar kırmızı bölge, başlangıç /
// tespit / bitiş işaretleri, aynı bileşende sağ eksende hacim şeridi (çağrı/dk).
// Render'da ek istek yok (ölçüt 9); servis paneli bağlantı olarak kalır.
import { lazy, Suspense, useMemo } from 'react';
import { Link } from 'react-router-dom';
import type { TSSeries } from '@/components/viz/TimeSeriesPanel';
import { Spinner } from '@/components/Spinner';
import { tf, useLang, useT } from '@/lib/i18n';
import { fmtClock } from '@/lib/utils';
import { serviceHref } from '@/lib/serviceHref';
import type { MLRecordView } from '@/lib/types';
import { fmtRecordValue, measureKey, outsideRegions, recordBuckets } from './mlRecord';

// Ağır uPlot bileşeni ayrı parça (MetricArea ile aynı yol; sayfa bütçesi).
const TimeSeriesPanel = lazy(() => import('@/components/viz/TimeSeriesPanel').then(m => ({ default: m.TimeSeriesPanel })));

const hm = (sec: number) => fmtClock(new Date(sec * 1000)).slice(0, 5);

/** SAF (test): adım metni — "5 min" / "5 dk", "1 h" / "1 sa". */
export function stepText(sec: number, lang: 'en' | 'tr'): string {
  if (sec % 3600 === 0) return `${sec / 3600} ${lang === 'tr' ? 'sa' : 'h'}`;
  if (sec % 60 === 0) return `${sec / 60} ${lang === 'tr' ? 'dk' : 'min'}`;
  return `${sec} ${lang === 'tr' ? 'sn' : 's'}`;
}

export function measureLabel(rec: MLRecordView, t: (k: string) => string): string {
  const k = measureKey(rec);
  return k ? t(k) : rec.metricLabel || rec.metricId || t('mlRecord.measure.value');
}

export function MLRecordChart({ rec, height = 200 }: { rec: MLRecordView; height?: number }) {
  const t = useT();
  const lang = useLang();
  const measure = measureLabel(rec, t);
  const b = useMemo(() => recordBuckets(rec), [rec]);
  const chartUnit = rec.series.unit === 'percent' ? '%' : rec.series.unit;
  const volLabel = t(rec.detector === 'svc_telemetry_volume' ? 'mlRecord.chart.spansPerMin' : 'mlRecord.chart.volume');
  const series = useMemo<TSSeries[]>(() => {
    const pts = (xs: number[]) => xs.map((v, i) => ({ time: b.t[i] * 1e9, value: v }));
    const out: TSSeries[] = [
      { label: measure, points: pts(b.v), unit: chartUnit, stepped: true },
      { label: t('mlRecord.chart.expected'), points: pts(b.t.map(() => rec.expectedValue)), unit: chartUnit, dash: [4, 4], width: 1 },
    ];
    if (b.n) out.push({ label: volLabel, points: pts(b.n), axis: 'right', unit: '', width: 1, stepped: true });
    return out;
  }, [b, measure, chartUnit, rec.expectedValue, t, volLabel]);
  const envelope = useMemo(() => (b.lo && b.hi ? {
    lo: { label: 'lo', points: b.lo.map((v, i) => ({ time: b.t[i] * 1e9, value: v })) },
    hi: { label: 'hi', points: b.hi.map((v, i) => ({ time: b.t[i] * 1e9, value: v })) },
    fill: 'var(--accent)',
  } : undefined), [b]);
  const regions = useMemo(() => outsideRegions(b).map(r => ({ ...r, color: 'var(--err)', subtle: true })), [b]);
  const events = useMemo(() => [
    { timeUnixNs: rec.onsetAt, kind: 'maintenance', label: t('mlRecord.chart.onset') },
    { timeUnixNs: rec.detectedAt, kind: 'incident', label: t('mlRecord.chart.detected') },
    ...(rec.endedAt ? [{ timeUnixNs: rec.endedAt, kind: 'end', label: t('mlRecord.chart.ended') }] : []),
  ], [rec.onsetAt, rec.detectedAt, rec.endedAt, t]);
  const fmt = (v: number) => fmtRecordValue(v, rec.series.unit, lang);
  const band = `${fmt(rec.baseline.lo)}–${fmt(rec.baseline.hi)}`;
  const span = b.t.length ? `${hm(b.t[0])}–${hm(b.t[b.t.length - 1] + b.bucketSec)}` : '';
  return (
    <div className="rounded-md border border-border bg-card px-3 py-2.5" data-testid="ml-record-chart">
      <p className="m-0 mb-1.5 text-xs text-muted-foreground" data-testid="ml-record-chart-caption">
        <span data-testid="ml-course-kind">{t('mlCourse.snapshot')}</span>
        {' · '}{measure}{' · '}{tf('mlRecord.chart.step', lang, { step: stepText(b.bucketSec, lang) })}{span && ` · ${span}`}
      </p>
      <Suspense fallback={<Spinner />}>
        <TimeSeriesPanel series={series} envelope={envelope} regions={regions} events={events} height={height} hideLegend />
      </Suspense>
      <p className="m-0 mt-1.5 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground" data-testid="ml-record-chart-legend">
        <span>— {measure}</span>
        <span>▒ {tf('mlRecord.chart.band', lang, { band })}</span>
        <span>┄ {t('mlRecord.chart.expected')} {fmt(rec.expectedValue)}</span>
        {regions.length > 0 && <span className="text-[color:var(--err)]">▮ {t('mlRecord.chart.outside')}</span>}
        <span>{t('mlRecord.chart.onset')} {hm(rec.onsetAt / 1e9)} · {t('mlRecord.chart.detected')} {hm(rec.detectedAt / 1e9)}{rec.endedAt ? ` · ${t('mlRecord.chart.ended')} ${hm(rec.endedAt / 1e9)}` : ''}</span>
        {b.n && <span>┃ {volLabel} ({t('mlRecord.chart.rightAxis')})</span>}
        <Link to={serviceHref(rec.subject.service)} className="ml-auto">{t('mlRecord.chart.servicePanel')} ↗</Link>
      </p>
    </div>
  );
}
