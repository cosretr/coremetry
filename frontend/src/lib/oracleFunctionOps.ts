// oracleFunctionOps — v0.10.1003 (kuyruk: "operasyon adını trace sayfasında
// göster"). Span'lerin taşıdığı FUNCTION_CODE değerini Oracle hata tablosundaki
// OPERASYON ADINA çevirir (TRANSFER_CONFIRM_SERVICE gibi) — ad
// trace'lerde yok, köprü fonksiyon kodu (GET /api/oracle/function-codes).
//
// SAF çekirdek: span'lerden kod toplama + sözlükten ad çözme. Anahtar YAZIMI
// span'de görülen hâliyle saklanır: Traces süzgeci harf duyarlı, "bu
// operasyonun diğer trace'leri" linki span'in gerçekten taşıdığı anahtarla
// kurulmalı.

import type { OracleFunctionCodesResponse, SpanRow } from '@/lib/types';

/** Span attribute'unda fonksiyon kodunu taşıyan anahtar yazımları (terfi kolonuyla aynı ikili). */
export const FUNCTION_CODE_KEYS = ['FUNCTION_CODE', 'function_code'] as const;

export interface TraceFunctionCode {
  /** Span'de görülen anahtar yazımı. */
  key: string;
  code: string;
}

/** Trace'in span'lerindeki tekil fonksiyon kodları — span sırasıyla, ≤ max. */
export function traceFunctionCodes(spans: Pick<SpanRow, 'attributes'>[] | null | undefined, max = 5): TraceFunctionCode[] {
  const out: TraceFunctionCode[] = [];
  const seen = new Set<string>();
  for (const s of spans ?? []) {
    for (const key of FUNCTION_CODE_KEYS) {
      const code = (s.attributes?.[key] ?? '').trim();
      if (!code || seen.has(code)) continue;
      seen.add(code);
      out.push({ key, code });
      if (out.length >= max) return out;
    }
  }
  return out;
}

export interface TraceOracleOperation {
  key: string;
  code: string;
  /** En çok satırlı operasyon adı. */
  operation: string;
  /** Aynı koda bağlı DİĞER operasyon adları (ops[1..]). */
  others: string[];
  /** Koda bağlı toplam operasyon adı sayısı (ops kesilmiş olabilir). */
  total: number;
}

/** Kodları sözlükten ada çevirir; sözlükte olmayan kod sonuç ÜRETMEZ (uydurma ad yok). */
export function traceOracleOperations(codes: TraceFunctionCode[], dict: OracleFunctionCodesResponse | null | undefined): TraceOracleOperation[] {
  if (!dict?.enabled) return [];
  const out: TraceOracleOperation[] = [];
  for (const c of codes) {
    const e = dict.codes?.[c.code];
    if (!e || !e.ops?.length) continue;
    out.push({ key: c.key, code: c.code, operation: e.ops[0], others: e.ops.slice(1), total: Math.max(e.total, e.ops.length) });
  }
  return out;
}

/** Çipin ipucu metni: adın nereden geldiğini ve tek olmadığını dürüstçe söyler. */
export function oracleOperationTitle(o: TraceOracleOperation): string {
  const base = `Oracle hata tablosundaki operasyon adı — span'lerdeki ${o.key} = ${o.code} üzerinden eşlendi.`;
  if (o.total <= 1) return `${base} Tıkla: bu fonksiyon kodunun diğer trace'leri.`;
  const names = [o.operation, ...o.others].join(', ');
  const more = o.total > 1 + o.others.length ? ` (+${o.total - 1 - o.others.length} daha)` : '';
  return `${base} Bu kod ${o.total} operasyonda görülüyor: ${names}${more}. Tıkla: bu fonksiyon kodunun diğer trace'leri.`;
}

// functionCodeLabel / functionCodeTitle — endpoint "Break down by function_code"
// tablosunda kodun yanında Oracle operasyon adı ("CAF0001 · CUSTOMER_…").
// Sözlük yoksa (başka boyut, kaynak yok) ya da kod sözlükte değilse değer
// AYNEN döner — ad uydurulmaz.
export function functionCodeLabel(value: string, dict: OracleFunctionCodesResponse | null | undefined): string {
  const e = dict?.enabled ? dict.codes?.[value] : undefined;
  if (!e?.ops?.length) return value;
  return `${value} · ${e.ops[0]}${e.total > 1 ? ` (+${e.total - 1})` : ''}`;
}
export function functionCodeTitle(value: string, dict: OracleFunctionCodesResponse | null | undefined): string {
  const e = dict?.enabled ? dict.codes?.[value] : undefined;
  if (!e?.ops?.length) return value;
  const more = e.total > e.ops.length ? ` (+${e.total - e.ops.length} daha)` : '';
  return `${value} — Oracle operasyonu: ${e.ops.join(', ')}${more}`;
}
