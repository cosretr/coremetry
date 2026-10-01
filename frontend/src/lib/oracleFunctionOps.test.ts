import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { traceFunctionCodes, traceOracleOperations, oracleOperationTitle, functionCodeLabel, functionCodeTitle } from './oracleFunctionOps';
import type { OracleFunctionCodesResponse } from './types';

// v0.10.1003 — trace sayfasında Oracle operasyon adı: span'lerdeki FUNCTION_CODE
// → sözlük (GET /api/oracle/function-codes) → ad. Ad uydurulmaz; anahtar yazımı
// span'de görüldüğü gibi taşınır (Traces süzgeci harf duyarlı).

const span = (attributes: Record<string, string>) => ({ attributes });

describe('traceFunctionCodes', () => {
  it('iki yazımı da okur, tekilleştirir, span sırasını ve yazımı korur', () => {
    const codes = traceFunctionCodes([
      span({ 'http.method': 'POST' }),
      span({ function_code: ' CAF0001 ' }),
      span({ FUNCTION_CODE: 'INVLD02' }),
      span({ function_code: 'CAF0001' }),
      span({ function_code: '' }),
    ]);
    expect(codes).toEqual([{ key: 'function_code', code: 'CAF0001' }, { key: 'FUNCTION_CODE', code: 'INVLD02' }]);
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
      CAF0001: { ops: ['CUSTOMER_MANAGEMENT_DIGITAL_ADDRESS_FUNCTIONS_REST'], total: 1 },
      SHARED: { ops: ['OP_A', 'OP_B', 'OP_C'], total: 5 },
      EMPTY: { ops: [], total: 0 },
    },
  };
  const codes = [
    { key: 'function_code', code: 'CAF0001' }, { key: 'FUNCTION_CODE', code: 'SHARED' },
    { key: 'FUNCTION_CODE', code: 'UNKNOWN' }, { key: 'FUNCTION_CODE', code: 'EMPTY' },
  ];
  it('sözlükte olan kod ada çevrilir; olmayan / boş kod sonuç üretmez', () => {
    const ops = traceOracleOperations(codes, dict);
    expect(ops).toEqual([
      { key: 'function_code', code: 'CAF0001', operation: 'CUSTOMER_MANAGEMENT_DIGITAL_ADDRESS_FUNCTIONS_REST', others: [], total: 1 },
      { key: 'FUNCTION_CODE', code: 'SHARED', operation: 'OP_A', others: ['OP_B', 'OP_C'], total: 5 },
    ]);
  });
  it('kaynak yoksa / sözlük gelmediyse hiçbir şey', () => {
    expect(traceOracleOperations(codes, { ...dict, enabled: false })).toEqual([]);
    expect(traceOracleOperations(codes, undefined)).toEqual([]);
  });
  it('ipucu: kaynağı söyler; kod birden çok operasyondaysa hepsini ve kalan sayıyı', () => {
    const [one, shared] = traceOracleOperations(codes, dict);
    expect(oracleOperationTitle(one)).toBe("Oracle hata tablosundaki operasyon adı — span'lerdeki function_code = CAF0001 üzerinden eşlendi. Tıkla: bu fonksiyon kodunun diğer trace'leri.");
    expect(oracleOperationTitle(shared)).toContain('Bu kod 5 operasyonda görülüyor: OP_A, OP_B, OP_C (+2 daha).');
  });
});

describe('functionCodeLabel / functionCodeTitle (endpoint kırılımı)', () => {
  const dict: OracleFunctionCodesResponse = {
    enabled: true,
    codes: { CAF0001: { ops: ['CUSTOMER_MGMT'], total: 1 }, SHARED: { ops: ['OP_A', 'OP_B', 'OP_C'], total: 5 } },
  };
  it('kodun yanına operasyon adı; çok operasyonda sayı; sözlükte yoksa değer aynen', () => {
    expect(functionCodeLabel('CAF0001', dict)).toBe('CAF0001 · CUSTOMER_MGMT');
    expect(functionCodeLabel('SHARED', dict)).toBe('SHARED · OP_A (+4)');
    expect(functionCodeTitle('SHARED', dict)).toBe('SHARED — Oracle operasyonu: OP_A, OP_B, OP_C (+2 daha)');
    expect(functionCodeLabel('UNKNOWN', dict)).toBe('UNKNOWN');
    expect(functionCodeLabel('prod', undefined)).toBe('prod');
    expect(functionCodeTitle('CAF0001', { ...dict, enabled: false })).toBe('CAF0001');
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
    expect(page).toContain('<OracleOperationChip spans={spans} pageRange={range} />');
    const chip = readFileSync(resolve(__dirname, '../components/traces/OracleOperationChip.tsx'), 'utf8');
    expect(chip).toContain('useOracleFunctionCodes(codes.length > 0)');
    const hook = readFileSync(resolve(__dirname, 'queries/oracleFunctionCodes.ts'), 'utf8');
    expect(hook).toContain("queryKey: ['oracle-function-codes']");
    expect(hook).toContain('staleTime: 5 * 60_000');
    expect(chip).toContain('functionCodeTracesHref({ window: pageRange, attrKey: o.key, codes: [o.code] })');
  });
});
