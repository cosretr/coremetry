import { seriesEnvelope } from '@/lib/chart/seriesEnvelope';
import type { TSSeries, TSEnvelope } from '@/components/viz/TimeSeriesPanel';
import type { SpanMetricSeries } from '@/lib/types';

// metricEnvelope — v0.10.2147 (servis sekmeleri S8, docs/frontend/
// service-tabs-plan.md §4; mockup operatör onayı 2026-11-13). Çok serili
// bir MetricArea grafiğini "zarf + ortanca + en yüksek" görünümüne çevirir.
//
// Eşik SABİT (ayar değil, plan §7 soru 7): 3 ve altı seri eski çizgileriyle
// okunur; 4 ve üstü tek şekle iner. "En yüksek" pencere ortalamasına göre
// (sunucunun ve lejantın "en yoğun" sıralamasıyla aynı ölçü) ve ADIYLA çizilir.
// SAF; çizim TimeSeriesPanel'de.
export const ENVELOPE_OVER = 3;

export interface MetricEnvelopeView {
  /** Çizilen çizgiler: ortanca, en yüksek. */
  lines: TSSeries[];
  envelope: TSEnvelope;
  /** Zarfa giren seri sayısı. */
  n: number;
  /** En yüksek serinin adı. */
  top: string;
}

const nameOf = (s: SpanMetricSeries, i: number) => (s.groupKey?.length ? s.groupKey.join(' · ') : `seri ${i + 1}`);
// Tam ad varsa (lejant kırpması, v0.9.1369) notta ve tooltip'te o okunur.
const fullOf = (s: SpanMetricSeries, i: number) => (s.fullKey?.length ? s.fullKey.join(' · ') : nameOf(s, i));

export function metricEnvelope(series: SpanMetricSeries[], unit?: string): MetricEnvelopeView | null {
  if (series.length <= ENVELOPE_OVER) return null;
  const env = seriesEnvelope(series.map((s, i) => ({ label: fullOf(s, i), points: s.points })));
  if (env.n <= ENVELOPE_OVER || !env.top) return null;
  const u = unit ?? '';
  return {
    lines: [
      { label: 'ortanca', color: 'var(--text2)', unit: u, points: env.median },
      { label: `en yüksek: ${env.top.label}`, color: 'var(--accent2)', unit: u, points: env.top.points },
    ],
    envelope: {
      lo: { label: 'en düşük', unit: u, points: env.lo },
      hi: { label: 'en yüksek değer', unit: u, points: env.hi },
    },
    n: env.n,
    top: env.top.label,
  };
}
