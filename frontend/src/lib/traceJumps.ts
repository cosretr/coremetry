// traceJumps — v0.10.1953 hızlı atlama çiplerinin hedefleri (SAF).
// v0.10.1952'de düz cümle özetinin parçasıydı; v0.10.1954'te özet kaldırıldı
// (operatör: "metin olarak açıklama olmasın, o zaman CoSRE'ye sor anlamı kalmaz"),
// yalnız çiplerin ihtiyacı kaldı. Kanıtı olmayan hedef undefined döner (çip çizilmez).
import type { SpanRow } from '@/lib/types';

export interface TraceJumps {
  /** En uzun süren dış çağrı (client / producer / DB); trace süresindeki payı < HOT_MIN_SHARE ise yok. */
  hot?: { spanId: string; ms: number };
  /** Kök hata: hatalı çocuğu olmayan, mesajı olan önce, en erken başlayan. */
  firstError?: { spanId: string; service: string; message: string };
}

export const HOT_MIN_SHARE = 0.2;

const isError = (s: SpanRow) => s.statusCode === 'error';

/** Span'in hata mesajı: exception olayı > exception.message özniteliği > durum mesajı. */
export function errorMessageOf(s: SpanRow): string {
  const ev = (s.events ?? []).find(e => e.name === 'exception' && e.attributes?.['exception.message']);
  return (ev?.attributes['exception.message'] ?? s.attributes?.['exception.message'] ?? s.statusMessage ?? '').trim();
}

export function traceJumps(spans: readonly SpanRow[]): TraceJumps | null {
  if (!spans || spans.length === 0) return null;
  const ids = new Set(spans.map(s => s.spanId));
  const root = spans.find(s => !s.parentSpanId || !ids.has(s.parentSpanId)) ?? spans[0];
  const kids = new Map<string, SpanRow[]>();
  for (const s of spans) if (s.parentSpanId && ids.has(s.parentSpanId)) {
    const a = kids.get(s.parentSpanId) ?? []; a.push(s); kids.set(s.parentSpanId, a);
  }
  let hot: TraceJumps['hot'];
  if (root.durationMs > 0) {
    let best: SpanRow | null = null;
    for (const s of spans) {
      if (s === root || !(s.kind === 'client' || s.kind === 'producer' || s.dbSystem)) continue;
      if (!best || s.durationMs > best.durationMs) best = s;
    }
    if (best && best.durationMs / root.durationMs >= HOT_MIN_SHARE) hot = { spanId: best.spanId, ms: best.durationMs };
  }
  const roots = spans.filter(s => isError(s) && !(kids.get(s.spanId) ?? []).some(isError));
  const pick = [...roots].sort((a, b) => (errorMessageOf(b) ? 1 : 0) - (errorMessageOf(a) ? 1 : 0) || a.startTime - b.startTime)[0];
  return {
    ...(hot ? { hot } : {}),
    ...(pick ? { firstError: { spanId: pick.spanId, service: pick.serviceName, message: errorMessageOf(pick) } } : {}),
  };
}
