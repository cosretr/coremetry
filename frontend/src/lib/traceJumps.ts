// traceSummary — v0.10.1952 (operatör: tekil trace mockup'ından "düz cümle özeti").
// SAF: trace'in tek paragraflık okunuşunun parçaları. Cümleyi Trace sayfası
// i18n kalıplarıyla kurar (pages/trace/TraceSummaryLine). Sayı/ad uydurmaz:
// bir parça kanıtı yoksa undefined döner ve cümleden düşer.
import type { SpanRow } from '@/lib/types';
import type { TraceRepeatGroup } from '@/lib/traceRepeats';

export interface TraceSummary {
  totalMs: number;
  failed: boolean;
  httpStatus?: number;
  /** En uzun süren dış çağrı; trace süresindeki payı < HOT_MIN_SHARE ise yok. */
  hot?: { spanId: string; service: string; target: string; ms: number; share: number };
  /** Kök hata: hatalı çocuğu olmayan, mesajı olan önce, en erken başlayan. */
  firstError?: { spanId: string; service: string; message: string; code?: string };
  errorCount: number;
  repeat?: { service: string; name: string; count: number };
}

export const HOT_MIN_SHARE = 0.2;
export const REPEAT_SUMMARY_MIN = 5;
const ERROR_CODE_RE = /\b([A-Z]{2,}[A-Z0-9]*-\d{1,6})\b/;

const isError = (s: SpanRow) => s.statusCode === 'error';

/** Span'in hata mesajı: exception olayı > exception.message özniteliği > durum mesajı. */
export function errorMessageOf(s: SpanRow): string {
  const ev = (s.events ?? []).find(e => e.name === 'exception' && e.attributes?.['exception.message']);
  return (ev?.attributes['exception.message'] ?? s.attributes?.['exception.message'] ?? s.statusMessage ?? '').trim();
}

/** Dış çağrının hedefi: DB sistemi > server.address > peer.service > çocuğun servisi > span adı. */
function targetOf(s: SpanRow, children: SpanRow[]): string {
  const childSvc = children.find(c => c.serviceName && c.serviceName !== s.serviceName)?.serviceName;
  return s.dbSystem || s.attributes?.['db.system'] || childSvc || s.attributes?.['peer.service'] || s.attributes?.['server.address'] || s.name;
}

export function traceSummary(spans: readonly SpanRow[], repeats: readonly TraceRepeatGroup[] = []): TraceSummary | null {
  if (!spans || spans.length === 0) return null;
  const ids = new Set(spans.map(s => s.spanId));
  const root = spans.find(s => !s.parentSpanId || !ids.has(s.parentSpanId)) ?? spans[0];
  const totalMs = root.durationMs;
  const kids = new Map<string, SpanRow[]>();
  for (const s of spans) if (s.parentSpanId && ids.has(s.parentSpanId)) {
    const a = kids.get(s.parentSpanId) ?? []; a.push(s); kids.set(s.parentSpanId, a);
  }

  let hot: TraceSummary['hot'];
  if (totalMs > 0) {
    // Dış çağrı = client / producer ya da DB span'i; en uzun süren (çağıran tarafın gördüğü bekleme).
    let best: SpanRow | null = null, bestW = -1;
    for (const s of spans) {
      if (s === root || !(s.kind === 'client' || s.kind === 'producer' || s.dbSystem)) continue;
      const w = s.durationMs;
      if (w > bestW) { best = s; bestW = w; }
    }
    if (best && best.durationMs / totalMs >= HOT_MIN_SHARE) {
      hot = { spanId: best.spanId, service: best.serviceName, target: targetOf(best, kids.get(best.spanId) ?? []), ms: best.durationMs, share: best.durationMs / totalMs };
    }
  }

  // Kök hatalar: hatalı ve hatalı çocuğu olmayan.
  const roots = spans.filter(s => isError(s) && !(kids.get(s.spanId) ?? []).some(isError));
  const pick = [...roots].sort((a, b) => (errorMessageOf(b) ? 1 : 0) - (errorMessageOf(a) ? 1 : 0) || a.startTime - b.startTime)[0];
  let firstError: TraceSummary['firstError'];
  if (pick) {
    const message = errorMessageOf(pick);
    const code = ERROR_CODE_RE.exec(message)?.[1];
    firstError = { spanId: pick.spanId, service: pick.serviceName, message, ...(code ? { code } : {}) };
  }

  const top = [...repeats].sort((a, b) => b.count - a.count)[0];
  return {
    totalMs,
    failed: isError(root) || (root.httpStatus ?? 0) >= 500,
    ...(root.httpStatus ? { httpStatus: root.httpStatus } : {}),
    ...(hot ? { hot } : {}),
    ...(firstError ? { firstError } : {}),
    errorCount: roots.length,
    ...(top && top.count >= REPEAT_SUMMARY_MIN ? { repeat: { service: top.service, name: top.name, count: top.count } } : {}),
  };
}
