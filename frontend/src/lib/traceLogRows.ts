// traceLogRows — v0.10.1889 (Log Search 3.6, audit §4 "Birleşik görünüm").
//
// SAF: trace'in log satırlarını span'lerine dağıtır. Eşleşme span_id ile
// (büyük/küçük harf duyarsız — log gönderen boru hattı büyük harf yazabilir,
// ClickHouse küçük harf tutar). span_id'si olmayan ya da trace'in span'leri
// arasında bulunmayan satırlar `orphans` (mockup: "Span'e bağlanamayan
// loglar") — zaman sırasıyla. Her span'in listesi de zaman sırasıyla.
import type { LogRow, SpanRow } from '@/lib/types';

export interface TraceLogGroups {
  /** span id (span'in KENDİ yazımı) → o span'in logları. */
  bySpan: Map<string, LogRow[]>;
  orphans: LogRow[];
  /** span'e bağlanan satır sayısı. */
  linked: number;
}

export function groupTraceLogs(logs: readonly LogRow[], spans: readonly SpanRow[]): TraceLogGroups {
  const idOf = new Map<string, string>();
  for (const s of spans) idOf.set(s.spanId.toLowerCase(), s.spanId);
  const sorted = [...logs].sort((a, b) => a.timestamp - b.timestamp);
  const bySpan = new Map<string, LogRow[]>();
  const orphans: LogRow[] = [];
  let linked = 0;
  for (const l of sorted) {
    const sid = l.spanId ? idOf.get(l.spanId.toLowerCase()) : undefined;
    if (!sid) { orphans.push(l); continue; }
    linked++;
    const arr = bySpan.get(sid);
    if (arr) arr.push(l); else bySpan.set(sid, [l]);
  }
  return { bySpan, orphans, linked };
}

/** Loglu span'ler + ataları (odak kipi: "Yalnız loglu span'ler" ağacı kırmasın). */
export function spansWithLogsAndAncestors(spans: readonly SpanRow[], withLogs: ReadonlySet<string>): Set<string> {
  const parent = new Map(spans.map(s => [s.spanId, s.parentSpanId] as const));
  const out = new Set<string>();
  for (const id of withLogs) {
    let cur: string | undefined = id;
    let guard = 0;
    while (cur && !out.has(cur) && guard++ < 10_000) {
      out.add(cur);
      cur = parent.get(cur) || undefined;
    }
  }
  return out;
}
