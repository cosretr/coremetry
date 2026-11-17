// aggResult.ts — v0.10.1505 (operator-reported, prod): /traces Aggregated
// sekmesi gRPC callee servisinde (operasyon + kind + http.method çipleri)
// her denemede "Toplu sorgu hata verdi ya da zaman aşımına uğradı" dedi.
// Sorgu HATA VERMİYORDU: sunucu sıfır grup buldu, Go nil dilimi JSON `null`
// oldu ve sayfa `null`u hata dalına soktu (v0.9.858 "null = hata"). Saf ve
// tablo-testli: boş cevap boş tablo, gerçek hata ise sınıfıyla söylenir.
import type { AggregateRow } from '@/lib/types';
import { t, type Lang } from '@/lib/i18n';

/** Sunucu cevabı → satırlar. `null` (eski sunucu / boş gövde) = sıfır grup, hata DEĞİL. */
export function aggRowsOf(a: AggregateRow[] | null | undefined): AggregateRow[] {
  return a ?? [];
}

export type AggErrorClass = 'timeout' | 'memory' | 'invalid' | 'generic';

/** Hata metninin sınıfı — istemci zaman aşımı, CH 159/241, HTTP 400. */
export function aggErrorClass(msg: string | null | undefined): AggErrorClass {
  const m = (msg ?? '').toLowerCase();
  if (/timed out|timeout exceeded|timeout_exceeded|code: 159|http 504/.test(m)) return 'timeout';
  if (/memory limit|memory_limit_exceeded|code: 241/.test(m)) return 'memory';
  if (/^http 400\b/.test(m)) return 'invalid';
  return 'generic';
}

// v0.10.2314 — başlıklar katalogda (trc.agg.err.*); TR değerler birebir aynı.
/** Hata satırının başlığı (ayrıntı = sunucu metni, ayrıca gösterilir). */
export function aggErrorHeadline(msg: string | null | undefined, lang: Lang = 'tr'): string {
  return t(`trc.agg.err.${aggErrorClass(msg)}`, lang);
}
