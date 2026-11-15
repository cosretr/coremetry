import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { traceFunctionCodes, traceOracleOperations, oracleOperationTitle, functionCodeLabel, functionCodeTitle, pickTraceOperation, exactTraceOperation, exactOperationTitle, traceEntryCodes, dictionaryOperationHint, type TraceOperationPick, type OracleOpRow } from './oracleFunctionOps';
import type { OracleFunctionCodesResponse, SpanRow } from './types';

// v0.10.1003 — trace sayfasında Oracle operasyon adı: span'lerdeki FUNCTION_CODE
// → sözlük (GET /api/oracle/function-codes) → ad. Ad uydurulmaz; anahtar yazımı
// span'de görüldüğü gibi taşınır (Traces süzgeci harf duyarlı).

const span = (attributes: Record<string, string>) => ({ attributes });

describe('traceFunctionCodes', () => {
  it('iki yazımı da okur, tekilleştirir, span sırasını ve yazımı korur', () => {
    const codes = traceFunctionCodes([
      span({ 'http.method': 'POST' }),
      span({ function_code: ' CLM0001 ' }),
      span({ FUNCTION_CODE: 'INVLD02' }),
      span({ function_code: 'CLM0001' }),
      span({ function_code: '' }),
    ]);
    expect(codes).toEqual([{ key: 'function_code', code: 'CLM0001' }, { key: 'FUNCTION_CODE', code: 'INVLD02' }]);
  });
  it('tavan ve boş girdi', () => {
    expect(traceFunctionCodes(null)).toEqual([]);
    const many = Array.from({ length: 9 }, (_, i) => span({ FUNCTION_CODE: `F${i}` }));
    expect(traceFunctionCodes(many)).toHaveLength(5);
    expect(traceFunctionCodes(many, 2).map(c => c.code)).toEqual(['F0', 'F1']);
  });
});

describe('traceOracleOperations', () => {
  const dict: OracleFunctionCodesResponse = {
    enabled: true,
    codes: {
      CLM0001: { ops: ['CUSTOMER_ADDRESS_REST'], total: 1 },
      SHARED: { ops: ['OP_A', 'OP_B', 'OP_C'], total: 5 },
      EMPTY: { ops: [], total: 0 },
    },
  };
  const codes = [
    { key: 'function_code', code: 'CLM0001' }, { key: 'FUNCTION_CODE', code: 'SHARED' },
    { key: 'FUNCTION_CODE', code: 'UNKNOWN' }, { key: 'FUNCTION_CODE', code: 'EMPTY' },
  ];
  it('sözlükte olan kod ada çevrilir; olmayan / boş kod sonuç üretmez', () => {
    const ops = traceOracleOperations(codes, dict);
    expect(ops).toEqual([
      { key: 'function_code', code: 'CLM0001', operation: 'CUSTOMER_ADDRESS_REST', others: [], total: 1 },
      { key: 'FUNCTION_CODE', code: 'SHARED', operation: 'OP_A', others: ['OP_B', 'OP_C'], total: 5 },
    ]);
  });
  it('kaynak yoksa / sözlük gelmediyse hiçbir şey', () => {
    expect(traceOracleOperations(codes, { ...dict, enabled: false })).toEqual([]);
    expect(traceOracleOperations(codes, undefined)).toEqual([]);
  });
  it('ipucu: kaynağı söyler; kod birden çok operasyondaysa hepsini ve kalan sayıyı', () => {
    const [one, shared] = traceOracleOperations(codes, dict);
    expect(oracleOperationTitle(one)).toBe("Kaynak: fonksiyon kodu sözlüğü — kesin DEĞİL. Bu trace'in kendi Oracle satırı ya da operasyon attribute'u yok; ad span'lerdeki function_code = CLM0001 üzerinden son 7 günün Oracle hata tablosundan, en çok satırlı aday olarak seçildi. Tıkla: bu fonksiyon kodunun diğer trace'leri.");
    expect(oracleOperationTitle(shared)).toContain('Bu kod 5 operasyonda görülüyor: OP_A, OP_B, OP_C (+2 daha).');
  });
});

describe('functionCodeLabel / functionCodeTitle (endpoint kırılımı)', () => {
  const dict: OracleFunctionCodesResponse = {
    enabled: true,
    codes: { CLM0001: { ops: ['CUSTOMER_MGMT'], total: 1 }, SHARED: { ops: ['OP_A', 'OP_B', 'OP_C'], total: 5 } },
  };
  it('kodun yanına operasyon adı; çok operasyonda sayı; sözlükte yoksa değer aynen', () => {
    expect(functionCodeLabel('CLM0001', dict)).toBe('CLM0001 · CUSTOMER_MGMT');
    expect(functionCodeLabel('SHARED', dict)).toBe('SHARED · OP_A (+4)');
    expect(functionCodeTitle('SHARED', dict)).toBe('SHARED — Oracle operasyonu: OP_A, OP_B, OP_C (+2 daha)');
    expect(functionCodeLabel('UNKNOWN', dict)).toBe('UNKNOWN');
    expect(functionCodeLabel('prod', undefined)).toBe('prod');
    expect(functionCodeTitle('CLM0001', { ...dict, enabled: false })).toBe('CLM0001');
  });
  it('endpoint kırılımı: boyut listede, sözlük yalnız o boyutta çekilir', () => {
    const src = readFileSync(resolve(__dirname, '../pages/endpoints/detailSections.tsx'), 'utf8');
    expect(src).toContain("  'function_code',\n  'host.name',");
    expect(src).toContain("useOracleFunctionCodes(by === 'function_code')");
  });
});

describe('Trace sayfası kablosu', () => {
  it('çip özet şeridinde; sözlük yalnız trace kod taşıyorsa ve 5 dk tazelikle çekilir', () => {
    const page = readFileSync(resolve(__dirname, '../pages/Trace.tsx'), 'utf8');
    expect(page).toContain('<OracleOperationChip spans={spans} pageRange={range} oracleRows={chipOracleRows} />');
    const chip = readFileSync(resolve(__dirname, '../components/traces/OracleOperationChip.tsx'), 'utf8');
    // v0.10.1329 — sözlük yalnız kesin kaynak (Oracle satırı / span attribute'u) yoksa.
    expect(chip).toContain('useOracleFunctionCodes(codes.length > 0 && exact === null)');
    const hook = readFileSync(resolve(__dirname, 'queries/oracleFunctionCodes.ts'), 'utf8');
    expect(hook).toContain("queryKey: ['oracle-function-codes']");
    expect(hook).toContain('staleTime: 5 * 60_000');
    expect(chip).toContain('functionCodeTracesHref({ window: pageRange, attrKey: o.key, codes: [o.code] })');
  });
  it('v0.10.1329 — Oracle satırları kod taşıyan trace için Logs sekmesi kapalıyken de çekilir', () => {
    const page = readFileSync(resolve(__dirname, '../pages/Trace.tsx'), 'utf8');
    expect(page).toContain("const oracleWanted = tab === 'logs' || traceHasFunctionCodes;");
    expect(page).toContain('useOracleTraceLogs(oracleWanted ? id : undefined, { from: logWin?.from, to: logWin?.to, enabled: oracleWanted })');
    expect(page).toContain('const chipOracleRows = oracleQuery.isLoading ? undefined : oracleRows;');
  });
});

// v0.10.1329 (operatör-bildirimli) — trace 6008dded… kökü POST
// /ACMWEB/application/expressOrderPreValidate, kendi Oracle satırı
// …APPLICATIONTRX_EXPRESS_ORDER_PRE_VALIDATE_REST; çip sözlüğün en çok satırlı
// adayını (…APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST, "+15") gösteriyordu.
// Öncelik: bu trace'in Oracle satırları → span attribute'u → etiketli sözlük.
describe('pickTraceOperation', () => {
  const TRX = 'LEND_PERSONAL_WEB_APPLICATIONTRX_EXPRESS_ORDER_PRE_VALIDATE_REST';
  const INQ = 'LEND_PERSONAL_WEB_APPLICATIONINQ_CREDIT_PRODUCT_INFORMATION_REST';
  const INQ2 = 'LEND_PERSONAL_WEB_APPLICATIONINQ_CUSTOMER_LIMIT_REST';
  const sp = (o: Partial<SpanRow> & { spanId: string }): SpanRow => ({
    traceId: 't', parentSpanId: '', name: 'n', kind: 'internal', serviceName: 'svc', hostName: '',
    startTime: 0, endTime: 0, durationMs: 0, statusCode: 'unset', statusMessage: '',
    attributes: {}, resourceAttributes: {}, events: null, scopeName: '', ...o,
  });
  const row = (op: string, spanId: string, timestamp: number): OracleOpRow => ({ attributes: op ? { 'operation.code': op } : {}, spanId, timestamp });
  const spans = [
    sp({ spanId: 'r', kind: 'server', serviceName: 'acmweb', startTime: 1, attributes: { FUNCTION_CODE: 'LNX01' } }),
    sp({ spanId: 'c1', parentSpanId: 'r', kind: 'server', serviceName: 'loan-inq', startTime: 2 }),
    sp({ spanId: 'c2', parentSpanId: 'r', kind: 'server', serviceName: 'loan-inq', startTime: 3 }),
  ];
  const dict: OracleFunctionCodesResponse = {
    enabled: true, codes: { LNX01: { ops: [INQ, TRX, INQ2], total: 16 } },
  };
  const cases: { name: string; rows: OracleOpRow[] | null; spans: SpanRow[]; want: TraceOperationPick | null }[] = [
    { name: 'tek Oracle satırı → o operasyon, +0 (sözlük adayı ne derse desin)',
      rows: [row(TRX, '', 10)], spans,
      want: { source: 'oracle', main: TRX, others: [], rows: 1 } },
    { name: 'TRX kök + INQ çocuk satırları → TRX ana, diğerleri INQ (satır sayısı INQ lehine olsa da)',
      rows: [row(INQ, 'c1', 5), row(INQ, 'c2', 6), row(INQ2, 'c2', 7), row(TRX, 'r', 9)], spans,
      want: { source: 'oracle', main: TRX, others: [INQ, INQ2], rows: 4 } },
    { name: 'span bağı yoksa en çok satırlı, eşitlikte en erken',
      rows: [row(INQ2, '', 8), row(TRX, '', 3), row(INQ, '', 5), row(INQ, '', 6), row(TRX, '', 4)], spans,
      want: { source: 'oracle', main: TRX, others: [INQ, INQ2], rows: 5 } },
    { name: 'operation.code taşımayan satır sayılmaz',
      rows: [row('', 'r', 1), row(INQ, 'c1', 2)], spans,
      want: { source: 'oracle', main: INQ, others: [], rows: 1 } },
    { name: "satır yok + kök span attribute'u → span kaynağı",
      rows: [], spans: [sp({ ...spans[0], attributes: { ...spans[0].attributes, OPERATION_CODE: TRX } }), spans[1],
        sp({ ...spans[2], attributes: { 'operation.code': INQ } })],
      want: { source: 'span', main: TRX, others: [INQ], key: 'OPERATION_CODE', where: 'root' } },
    { name: "kökte yoksa giriş span'i, sonra herhangi bir span",
      rows: null, spans: [spans[0], sp({ spanId: 'i', parentSpanId: 'r', kind: 'internal', startTime: 2, attributes: { operation_code: INQ2 } }),
        sp({ spanId: 's', parentSpanId: 'r', kind: 'server', startTime: 3, attributes: { 'operation.code': INQ } })],
      want: { source: 'span', main: INQ, others: [INQ2], key: 'operation.code', where: 'entry' } },
    { name: 'kesin kaynak yok → sözlük, ETİKETLİ aday (tahmin)',
      rows: [], spans,
      want: { source: 'dictionary', candidates: [{ key: 'FUNCTION_CODE', code: 'LNX01', operation: INQ, others: [TRX, INQ2], total: 16 }] } },
    { name: 'hiçbir şey yok → null', rows: [], spans: [sp({ spanId: 'x' })], want: null },
    { name: 'boş girdi → null', rows: null, spans: [], want: null },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(pickTraceOperation(c.rows, c.spans, dict)).toEqual(c.want);
    });
  }
  it('sözlük kapalıyken kesin kaynak yine çalışır; yedek hiçbir şey üretmez', () => {
    expect(pickTraceOperation([row(TRX, '', 1)], spans, undefined)?.source).toBe('oracle');
    expect(pickTraceOperation([], spans, { enabled: false, codes: {} })).toBeNull();
  });
  it('v0.10.1514 — sözlük adayının ipucu hücresi kısa: "tahmin · N aday"', () => {
    const p = pickTraceOperation([], spans, dict);
    expect(p?.source).toBe('dictionary');
    if (p?.source !== 'dictionary') return;
    expect(dictionaryOperationHint(p.candidates[0])).toBe('tahmin · 16 aday');
    const chip = readFileSync(resolve(__dirname, '../components/traces/OracleOperationChip.tsx'), 'utf8');
    expect(chip).toContain('hint={dictionaryOperationHint(o)} hintTone="warn" hintTitle={title}');
    expect(chip).not.toContain('dictionaryOperationLabel');
  });
  it('ipucu metni kaynağı söyler', () => {
    const o = exactTraceOperation([row(TRX, 'r', 9), row(INQ, 'c1', 5)], spans)!;
    expect(exactOperationTitle(o)).toBe(`Kaynak: bu trace'in kendi Oracle hata satırları (operation.code, 2 satır) — kesin. Bu trace'teki diğer operasyonlar: ${INQ}.`);
    expect(exactOperationTitle({ source: 'span', main: TRX, others: [], key: 'operation.code', where: 'root' }))
      .toBe("Kaynak: kök span attribute'u operation.code — kesin.");
  });
});

// v0.10.1497 (operatör) — trace başlığındaki fonksiyon/kanal kodu: kök önce,
// sonra giriş span'i, sonra herhangi biri; anahtar yazımı korunur.
describe('traceEntryCodes', () => {
  const s = (o: { parentSpanId?: string; startTime: number; kind?: string; attributes?: Record<string, string> }) =>
    ({ parentSpanId: '', kind: 'internal', attributes: {}, ...o });
  it('kök span kazanır; kanal kodu ayrı aranır', () => {
    const r = traceEntryCodes([
      s({ parentSpanId: 'r', startTime: 2, kind: 'producer', attributes: { function_code: 'CHILD1', channel_code: '060201' } }),
      s({ startTime: 1, kind: 'server', attributes: { FUNCTION_CODE: 'ROOT1' } }),
    ]);
    expect(r).toEqual({ functionCode: { key: 'FUNCTION_CODE', code: 'ROOT1' }, channelCode: { key: 'channel_code', code: '060201' } });
  });
  it('giriş span\'i iç span\'den önce', () => {
    const r = traceEntryCodes([
      s({ startTime: 1, kind: 'server' }),
      s({ parentSpanId: 'a', startTime: 2, kind: 'internal', attributes: { function_code: 'INNER' } }),
      s({ parentSpanId: 'a', startTime: 3, kind: 'consumer', attributes: { function_code: 'ENTRY', 'channel.code': ' 0602 ' } }),
    ]);
    expect(r.functionCode).toEqual({ key: 'function_code', code: 'ENTRY' });
    expect(r.channelCode).toEqual({ key: 'channel.code', code: '0602' });
  });
  it('kod yoksa ikisi de null', () => {
    expect(traceEntryCodes([s({ startTime: 1 })])).toEqual({ functionCode: null, channelCode: null });
    expect(traceEntryCodes(undefined)).toEqual({ functionCode: null, channelCode: null });
  });
});
