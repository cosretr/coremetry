// topicLag — v0.10.1959 (operatör: "Messaging sayfasında VM metrikte lag olan topic'ler üstte gözüksün,
// servis Messaging sekmesindeki lag nasıl gözüküyorsa aynı şekilde"). SAF yardımcılar.
//
// Kaynak servis Messaging sekmesi ve topic detayının "Max lag" karosuyla AYNI metrik:
// kafka_consumer_records_lag_max (OTel Java agent; tüketici istemcinin topic'te gördüğü en kötü partition lag'i).
// Topic başına maks. — listedeki sayı detay karosuyla birebir. Eşik lib/kafkaMetrics ile aynı
// (≥ 1.000 sarı, ≥ 10.000 kırmızı — DependenciesTable lagTone).
import type { SpanMetricSeries } from '@/lib/types';
import { lastValue, seriesLabels } from '@/lib/httpMetrics';

export const TOPIC_LAG_EXPR = 'max by (topic) (kafka_consumer_records_lag_max)';

/** topic → son lag değeri (0 dahil; değeri olmayan seri yok sayılır). */
export function topicLagMap(series: SpanMetricSeries[] | null | undefined): Map<string, number> {
  const out = new Map<string, number>();
  for (const s of series ?? []) {
    const t = seriesLabels(s).topic;
    const v = lastValue(s);
    if (!t || v === null || v < 0) continue;
    out.set(t, Math.max(out.get(t) ?? 0, v));
  }
  return out;
}

/** Varsayılan sıra: lag'i olanlar (azalan) üstte, gerisi çağrı hacmine göre. */
export function sortByLag<T extends { lag?: number; spanCount: number }>(rows: readonly T[]): T[] {
  return [...rows].sort((a, b) => (b.lag ?? -1) - (a.lag ?? -1) || b.spanCount - a.spanCount);
}
