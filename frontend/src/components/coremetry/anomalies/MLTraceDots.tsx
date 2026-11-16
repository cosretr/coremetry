// MLTraceDots — v0.10.1872, öğrenme döngüsü Faz 2 parti B (audit §7.2 "Az
// çağrılan servis: noktasal", §17 "ısı haritasının YERİNE nokta grafiği,
// ikisi birlikte gösterilmez"; mockup görünüm 3 "Az çağrılan servis
// görünümü"). Her nokta bir giriş çağrısı (ham span, sunucuda LIMIT'li);
// dolu nokta = referans çizgisinin üstünde. Çizgi ve özet cümlesi sunucudan
// (ml.BuildTraceCourse); FE yalnız noktaları ikiye ayırır. Grafik uPlot
// (TimeChart `points` kipi); oynatma imleci ana grafikle aynı.
import { useMemo } from 'react';
import { TimeChart, type TimeChartSeries } from '@/components/charts/TimeChart';
import type { CallPoint, TraceCourse } from '@/lib/types';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2291 — metin arayüz dilinde

export interface DotSeries { times: number[]; above: (number | null)[]; below: (number | null)[] }

/** SAF (test): çağrılar → zaman ekseni (unix sn, artan) + çizginin üstü / altı serileri. Çizgi yoksa hepsi "alt". */
export function dotSeries(dots: readonly CallPoint[], lineMs: number | null | undefined): DotSeries {
  const sorted = [...dots].filter(d => Number.isFinite(d.t) && Number.isFinite(d.ms)).sort((a, b) => a.t - b.t);
  const times: number[] = [], above: (number | null)[] = [], below: (number | null)[] = [];
  for (const d of sorted) {
    const sec = d.t / 1000;
    const hi = lineMs != null && d.ms > lineMs;
    times.push(sec);
    above.push(hi ? d.ms : null);
    below.push(hi ? null : d.ms);
  }
  return { times, above, below };
}

export function MLTraceDots({ tc, cursorSec }: { tc: TraceCourse; cursorSec: number | null }) {
  const dots = tc.dots ?? [];
  const ds = useMemo(() => dotSeries(dots, tc.line?.ms), [dots, tc.line?.ms]);
  const lang = useLang();
  const series = useMemo<TimeChartSeries[]>(() => [
    { key: 'below', label: t('mlx.dots.call', lang), data: ds.below, color: 'var(--sig-trace)', type: 'points', hollow: true },
    { key: 'above', label: t('mlx.dots.above', lang), data: ds.above, color: 'var(--sig-trace)', type: 'points' },
  ], [ds, lang]);
  const thresholds = useMemo(() => (tc.line ? [{ value: tc.line.ms, label: tc.line.label, color: 'var(--text)' }] : undefined), [tc.line]);
  const regions = useMemo(() => [{ fromSec: tc.onsetNs / 1e9, toSec: tc.endNs / 1e9, label: t('mlx.region.anomaly', lang), color: 'var(--err)' }], [tc.onsetNs, tc.endNs, lang]);
  return (
    <div className="mb-3" data-testid="ml-trace-dots">
      {tc.dotsSummary && <p className="mt-0 mb-1.5 text-sm font-semibold" data-testid="ml-trace-dots-summary">{tc.dotsSummary}</p>}
      <div className="mb-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
        <span>{t('mlx.dots.head', lang)}{tc.route ? <> · <span className="font-mono">{tc.route}</span></> : t('mlx.tc.entry', lang)}</span>
        {tc.line && <span>┄ {tc.line.label}</span>}
        {tc.dotsCapped && <span className="text-[color:var(--warn)]">{tf('mlx.dots.capped', lang, { n: dots.length })}</span>}
      </div>
      {ds.times.length >= 2 ? (
        <TimeChart height={170} times={ds.times} series={series} thresholds={thresholds} regions={regions} hideLegend leftUnit="ms"
          playhead={cursorSec != null ? { cursorSec } : null} />
      ) : (
        <p className="m-0 text-sm text-muted-foreground" data-testid="ml-trace-dots-empty">
          {t(tc.dotsUnread ? 'mlx.dots.unread' : ds.times.length === 1 ? 'mlx.dots.one' : 'mlx.dots.none', lang)}
        </p>
      )}
      <p className="mt-1.5 mb-0 text-xs text-muted-foreground">
        {t('mlx.dots.foot', lang)}
      </p>
    </div>
  );
}
