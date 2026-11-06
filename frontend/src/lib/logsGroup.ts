// logsGroup — v0.10.1884 (Log Search Faz 3 dilim 3.5; audit §4 "Trace'e göre
// grupla": istemcide, YÜKLENEN örnek içinde, `group=trace`). SAF; logsGroup.test.ts.
//
// Gruplar ilk satırlarının sırasıyla dizilir (sunucu sırası korunur), grup
// içinde satırlar kendi sıralarında; trace'siz satırlar EN SONDA tek grup.
// Başlık bilgisi (süre + hata + kök) önizleme yanıtından — ek istek yok.
import type { LogRow, TracePreview } from '@/lib/types';

export const NO_TRACE_GROUP = '';

export function parseLogsGroup(v: string | null | undefined): 'trace' | '' {
  return v === 'trace' ? 'trace' : '';
}

export interface TraceGroupedRows {
  rows: LogRow[];
  /** grup anahtarı (traceId | '') → satır sayısı */
  counts: Map<string, number>;
}

export function groupRowsByTrace(rows: readonly LogRow[]): TraceGroupedRows {
  const order: string[] = [];
  const buckets = new Map<string, LogRow[]>();
  for (const r of rows) {
    const k = r.traceId || NO_TRACE_GROUP;
    let b = buckets.get(k);
    if (!b) { b = []; buckets.set(k, b); if (k !== NO_TRACE_GROUP) order.push(k); }
    b.push(r);
  }
  if (buckets.has(NO_TRACE_GROUP)) order.push(NO_TRACE_GROUP);
  const out: LogRow[] = [];
  const counts = new Map<string, number>();
  for (const k of order) {
    const b = buckets.get(k)!;
    counts.set(k, b.length);
    out.push(...b);
  }
  return { rows: out, counts };
}

/** Başlık metni (çipin yanında): süre · hata · kök · N log (bu örnekte). */
export function traceGroupSummary(p: TracePreview | undefined, count: number, fmtDur: (ms: number) => string): string {
  const logs = `${count.toLocaleString('tr-TR')} log (bu örnekte)`;
  if (!p || p.state !== 'found') {
    if (p?.state === 'sampled') return `trace ClickHouse'ta yok (sampling Collector'da) · ${logs}`;
    if (p?.state === 'pending') return `trace henüz yazılmadı · ${logs}`;
    return logs;
  }
  const err = (p.errorCount ?? 0) > 0 ? '✕ hata var' : '✓ hata yok';
  const root = p.rootName ? ` · kök ${p.rootName}` : '';
  return `${fmtDur(p.durationMs ?? 0)} · ${err}${root} · ${logs}`;
}
