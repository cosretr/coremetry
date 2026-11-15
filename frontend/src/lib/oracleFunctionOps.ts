// oracleFunctionOps — v0.10.1003 (kuyruk: "operasyon adını trace sayfasında
// göster"). Span'lerin taşıdığı FUNCTION_CODE değerini Oracle hata tablosundaki
// OPERASYON ADINA çevirir (TRANSFER_CONFIRM_SERVICE gibi) — ad
// trace'lerde yok, köprü fonksiyon kodu (GET /api/oracle/function-codes).
//
// SAF çekirdek: span'lerden kod toplama + sözlükten ad çözme. Anahtar YAZIMI
// span'de görülen hâliyle saklanır: Traces süzgeci harf duyarlı, "bu
// operasyonun diğer trace'leri" linki span'in gerçekten taşıdığı anahtarla
// kurulmalı.

import type { LogRow, OracleFunctionCodesResponse, SpanRow } from '@/lib/types';
import { ENTRY_SPAN_KINDS } from '@/lib/entrySpans';

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

/** v0.10.1497 — kanal kodu anahtar yazımları (span'de görüldüğü gibi). */
export const CHANNEL_CODE_KEYS = ['CHANNEL_CODE', 'channel_code', 'channel.code'] as const;

export interface TraceEntryCode { key: string; code: string }

/**
 * v0.10.1497 (operatör: "function code / channel code yukarıda operasyonun
 * yanında yazsın") — trace başlığında gösterilecek fonksiyon + kanal kodu.
 * Öncelik: kök span → giriş (server/consumer) span'leri → herhangi bir span
 * (zaman sırasıyla); her tür için İLK bulunan, anahtar yazımı korunur.
 */
export function traceEntryCodes(
  spans: Pick<SpanRow, 'parentSpanId' | 'startTime' | 'kind' | 'attributes'>[] | null | undefined,
): { functionCode: TraceEntryCode | null; channelCode: TraceEntryCode | null } {
  const sp = [...(spans ?? [])].sort((a, b) => a.startTime - b.startTime);
  const root = sp.find(s => !s.parentSpanId) ?? sp[0];
  const ordered = [
    ...(root ? [root] : []),
    ...sp.filter(s => s !== root && isEntryKind(s.kind)),
    ...sp.filter(s => s !== root && !isEntryKind(s.kind)),
  ];
  const first = (keys: readonly string[]): TraceEntryCode | null => {
    for (const s of ordered) {
      for (const key of keys) {
        const code = (s.attributes?.[key] ?? '').trim();
        if (code) return { key, code };
      }
    }
    return null;
  };
  return { functionCode: first(FUNCTION_CODE_KEYS), channelCode: first(CHANNEL_CODE_KEYS) };
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

/** Çipin ipucu metni: adın nereden geldiğini ve tek olmadığını dürüstçe söyler.
 *  v0.10.1329 — yalnız SÖZLÜK yedeğinde çizilir: metin bunun bir tahmin olduğunu
 *  (trace'in kendi Oracle satırı yok) ve aday listesini açıkça söyler. */
export function oracleOperationTitle(o: TraceOracleOperation): string {
  const base = `Kaynak: fonksiyon kodu sözlüğü — kesin DEĞİL. Bu trace'in kendi Oracle satırı ya da operasyon attribute'u yok; ad span'lerdeki ${o.key} = ${o.code} üzerinden son 7 günün Oracle hata tablosundan, en çok satırlı aday olarak seçildi.`;
  if (o.total <= 1) return `${base} Tıkla: bu fonksiyon kodunun diğer trace'leri.`;
  const names = [o.operation, ...o.others].join(', ');
  const more = o.total > 1 + o.others.length ? ` (+${o.total - 1 - o.others.length} daha)` : '';
  return `${base} Bu kod ${o.total} operasyonda görülüyor: ${names}${more}. Tıkla: bu fonksiyon kodunun diğer trace'leri.`;
}

// functionCodeLabel / functionCodeTitle — endpoint "Break down by function_code"
// tablosunda kodun yanında Oracle operasyon adı ("CLM0001 · CUSTOMER_…").
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

// ── v0.10.1329 (operatör-bildirimli) — KESİN kaynak önce ───────────────────
// Trace 6008dded… kökü POST …/directCreditPreApproval, kendi Oracle satırı
// LOAN_…_APPLICATIONTRX_DIRECT_CREDIT_PRE_APPROVAL_REST diyor; çip ise
// "Operasyon: LOAN_…_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST (+15)"
// gösteriyordu. Kök neden: çip YALNIZ fonksiyon kodu sözlüğünü okuyordu —
// kodun bankanın tamamında son 7 günde EN ÇOK satırlı operasyonu (1:n
// ilişkinin tahmini) "bu trace'in operasyonu" gibi sunuluyordu; trace'in
// kendi Oracle satırları (/api/oracle/errors?trace_id=) hiç sorulmuyordu.
//
// Öncelik (backend ikizi: internal/api/explain_trace_operation.go
// pickMainOperation — sözlük HARİÇ aynı sıra):
//   (a) oracle — bu trace'in Oracle hata satırlarındaki tekil operation.code.
//       Ana operasyon: satırı kök servisin span'ine bağlı olan → en çok satırlı
//       → en erken satır. "+N" = BU trace'teki diğer gerçek operasyonlar.
//   (b) span — kök span'in, sonra giriş (server/consumer) span'lerinin, sonra
//       herhangi bir span'in operasyon attribute'u.
//   (c) dictionary — bugünkü sözlük yolu, ama ETİKETİYLE: kesin değil, aday
//       sayısı görünür ("Operasyon (fonksiyon kodundan, N aday): X").

/** Span attribute'unda operasyon kodunu taşıyan anahtar yazımları (öncelik sırasıyla). */
export const OPERATION_CODE_KEYS = ['operation.code', 'OPERATION_CODE', 'operation_code'] as const;

/** Oracle satırının (LogRow ikizi, origin 'oracle') seçicinin okuduğu alanları. */
export type OracleOpRow = Pick<LogRow, 'attributes' | 'spanId' | 'timestamp'>;
type OpSpan = Pick<SpanRow, 'spanId' | 'parentSpanId' | 'serviceName' | 'kind' | 'startTime' | 'attributes'>;

export type TraceOperationPick =
  | { source: 'oracle'; main: string; others: string[]; rows: number }
  | { source: 'span'; main: string; others: string[]; key: string; where: 'root' | 'entry' | 'span' }
  | { source: 'dictionary'; candidates: TraceOracleOperation[] };

/** Kök span: parent'ı olmayan; yoksa en erken başlayan (backend traceRootSpan ile aynı kural). */
function rootSpanOf<T extends Pick<SpanRow, 'parentSpanId' | 'startTime'>>(spans: T[]): T | undefined {
  let root: T | undefined;
  for (const s of spans) {
    if (!s.parentSpanId) return s;
    if (!root || s.startTime < root.startTime) root = s;
  }
  return root;
}

const isEntryKind = (k: string) => (ENTRY_SPAN_KINDS as readonly string[]).includes((k ?? '').toLowerCase());

function spanOperationCode(s: Pick<SpanRow, 'attributes'>): { key: string; code: string } | null {
  for (const key of OPERATION_CODE_KEYS) {
    const code = (s.attributes?.[key] ?? '').trim();
    if (code) return { key, code };
  }
  return null;
}

/** (a) + (b): kesin kaynaklar. Sözlüğe dokunmaz; ikisi de yoksa null. */
export function exactTraceOperation(
  oracleRows: OracleOpRow[] | null | undefined,
  spans: OpSpan[] | null | undefined,
): Exclude<TraceOperationPick, { source: 'dictionary' }> | null {
  const sp = spans ?? [];
  const root = rootSpanOf(sp);
  // (a) Oracle satırları.
  const rootSvcSpans = new Set(sp.filter(s => root && s.serviceName === root.serviceName && s.spanId).map(s => s.spanId));
  const groups = new Map<string, { op: string; rows: number; first: number; rootLinked: boolean }>();
  let total = 0;
  for (const r of oracleRows ?? []) {
    const op = (r.attributes?.['operation.code'] ?? '').trim();
    if (!op) continue;
    total++;
    const g = groups.get(op) ?? { op, rows: 0, first: Number.POSITIVE_INFINITY, rootLinked: false };
    g.rows++;
    if (r.timestamp < g.first) g.first = r.timestamp;
    if (r.spanId && rootSvcSpans.has(r.spanId)) g.rootLinked = true;
    groups.set(op, g);
  }
  if (groups.size > 0) {
    const ranked = [...groups.values()].sort((a, b) =>
      Number(b.rootLinked) - Number(a.rootLinked) || b.rows - a.rows || a.first - b.first || (a.op < b.op ? -1 : a.op > b.op ? 1 : 0));
    return { source: 'oracle', main: ranked[0].op, others: ranked.slice(1).map(g => g.op), rows: total };
  }
  // (b) Span attribute'u: kök → giriş span'leri → herhangi bir span (zaman sırasıyla).
  const ordered = [...sp].sort((a, b) => a.startTime - b.startTime);
  const tiers: { where: 'root' | 'entry' | 'span'; list: OpSpan[] }[] = [
    { where: 'root', list: root ? [root] : [] },
    { where: 'entry', list: ordered.filter(s => isEntryKind(s.kind)) },
    { where: 'span', list: ordered },
  ];
  for (const t of tiers) {
    for (const s of t.list) {
      const hit = spanOperationCode(s);
      if (!hit) continue;
      const others: string[] = [];
      for (const o of ordered) {
        const c = spanOperationCode(o)?.code;
        if (c && c !== hit.code && !others.includes(c)) others.push(c);
      }
      return { source: 'span', main: hit.code, others, key: hit.key, where: t.where };
    }
  }
  return null;
}

/** Tam öncelik zinciri: kesin kaynak yoksa etiketli sözlük adayları; hiçbiri yoksa null. */
export function pickTraceOperation(
  oracleRows: OracleOpRow[] | null | undefined,
  spans: OpSpan[] | null | undefined,
  dict: OracleFunctionCodesResponse | null | undefined,
): TraceOperationPick | null {
  const exact = exactTraceOperation(oracleRows, spans);
  if (exact) return exact;
  const candidates = traceOracleOperations(traceFunctionCodes(spans), dict);
  return candidates.length > 0 ? { source: 'dictionary', candidates } : null;
}

/** Kesin kaynağın ipucu/aria metni: adın NEREDEN geldiğini söyler. */
export function exactOperationTitle(p: Exclude<TraceOperationPick, { source: 'dictionary' }>): string {
  const others = p.others.length > 0 ? ` Bu trace'teki diğer operasyonlar: ${p.others.join(', ')}.` : '';
  if (p.source === 'oracle') {
    return `Kaynak: bu trace'in kendi Oracle hata satırları (operation.code, ${p.rows} satır) — kesin.${others}`;
  }
  const where = p.where === 'root' ? 'kök span' : p.where === 'entry' ? 'giriş span' : 'span';
  return `Kaynak: ${where} attribute'u ${p.key} — kesin.${others}`;
}

/** Sözlük adayının ipucu hücresi — tahmin olduğu ve kaç aday olduğu yazının içinde.
 *  v0.10.1514: uzun "Operasyon (fonksiyon kodundan, N aday):" etiketinin yerine
 *  kimlik çipinin amber hücresi; kaynağın uzun hâli oracleOperationTitle ipucunda. */
export function dictionaryOperationHint(o: TraceOracleOperation): string {
  return `tahmin · ${o.total} aday`;
}
