// mlGroupDetail — v0.10.2022 (anomali tekilleştirme, mockup
// docs/frontend/mockups/anomaly-unified.html "Hedef: tek anomali" kartı):
// /anomalies/ml/:id grubun sayfasıdır. Saf yardımcılar: açılan olayın grubu,
// kanıt satırının kaynak notu, başlık operasyonu. Grup üyeleri liste ucunun
// (son 24 sa, aynı önbellek) satırlarından; ek sunucu okuması YOK.
import type { MLEvent } from '@/lib/types';
import { parseObj } from '@/lib/mlReplay';
import { groupRows, type MLGroupRow } from './mlGroupRows';

/** SAF: açılan olay kimliğinin grubu (liste satırlarında yoksa null — tek olay görünümü). */
export function findGroup(events: MLEvent[] | undefined, id: string): MLGroupRow | null {
  if (!events || !id) return null;
  const me = events.find(e => e.id === id);
  if (!me) return null;
  const key = me.groupId || `ev:${me.id}`;
  return groupRows(events.filter(e => (e.groupId || `ev:${e.id}`) === key))[0] ?? null;
}

/** SAF (test): kanıt satırının kaynak notu — sinyal nereden okundu. */
export function evidenceSource(detector: string, signal: string, evidence?: string): string {
  if (detector === 'trace_error_count') return "ClickHouse span'leri";
  if (signal === 'log') return 'Elasticsearch';
  if (signal === 'metric') {
    const ev = parseObj(evidence ?? '');
    if (ev.source === 'thanos') return `Thanos · ${typeof ev.cluster === 'string' && ev.cluster ? ev.cluster : 'cluster'}`;
  }
  return 'VictoriaMetrics';
}

/** SAF: trace olayında başlığın operasyonu (Code = endpoint / operasyon); diğerlerinde boş. */
export function headerOperation(e: Pick<MLEvent, 'signal' | 'code'> | null | undefined): string {
  return e && e.signal === 'trace' && e.code ? e.code : '';
}
