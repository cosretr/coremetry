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
  const series = useMemo<TimeChartSeries[]>(() => [
    { key: 'below', label: 'çağrı', data: ds.below, color: 'var(--sig-trace)', type: 'points', hollow: true },
    { key: 'above', label: 'sınırın üstünde', data: ds.above, color: 'var(--sig-trace)', type: 'points' },
  ], [ds]);
  const thresholds = useMemo(() => (tc.line ? [{ value: tc.line.ms, label: tc.line.label, color: 'var(--text)' }] : undefined), [tc.line]);
  const regions = useMemo(() => [{ fromSec: tc.onsetNs / 1e9, toSec: tc.endNs / 1e9, label: 'anomali', color: 'var(--err)' }], [tc.onsetNs, tc.endNs]);
  return (
    <div className="mb-3" data-testid="ml-trace-dots">
      {tc.dotsSummary && <p className="mt-0 mb-1.5 text-sm font-semibold" data-testid="ml-trace-dots-summary">{tc.dotsSummary}</p>}
      <div className="mb-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
        <span>tek tek çağrılar · örneklenmiş span'ler{tc.route ? <> · <span className="font-mono">{tc.route}</span></> : ' · servisin giriş istekleri'}</span>
        {tc.line && <span>┄ {tc.line.label}</span>}
        {tc.dotsCapped && <span className="text-[color:var(--warn)]">ilk {dots.length} çağrı gösteriliyor</span>}
      </div>
      {ds.times.length >= 2 ? (
        <TimeChart height={170} times={ds.times} series={series} thresholds={thresholds} regions={regions} hideLegend leftUnit="ms"
          playhead={cursorSec != null ? { cursorSec } : null} />
      ) : (
        <p className="m-0 text-sm text-muted-foreground" data-testid="ml-trace-dots-empty">
          {tc.dotsUnread ? 'Çağrılar okunamadı (yok demek değil).' : ds.times.length === 1 ? 'Bu pencerede yalnız bir çağrı var.' : 'Bu pencerede örneklenmiş çağrı yok.'}
        </p>
      )}
      <p className="mt-1.5 mb-0 text-xs text-muted-foreground">
        Her nokta bir çağrı. Dolu nokta: çizginin üstünde. Az çağrı alan serviste ortalama yanıltır; bu yüzden çağrılar tek tek gösterilir.
      </p>
    </div>
  );
}
