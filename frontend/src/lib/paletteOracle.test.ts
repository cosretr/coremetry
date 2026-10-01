import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { oracleOperationQuery, oraclePaletteResults } from './paletteOracle';
import { functionCodeTracesHref } from './pivotHref';
import type { OracleOperationsResponse } from './types';

// v0.10.1002 — operatör: "operasyon ismiyle trace bulabilir miyim". Operasyon
// adı trace'lerde yok; palet, Oracle hata satırlarındaki fonksiyon kodu
// üzerinden Traces'e (ve hata satırındaki son trace'e) götürür.

const win = { preset: '1h' } as const;

function filtersOf(href: string): unknown {
  const q = new URLSearchParams(href.slice(href.indexOf('?') + 1));
  return JSON.parse(q.get('filters') ?? 'null');
}

describe('functionCodeTracesHref', () => {
  it('tek kodda eşitlik, çok kodda IN; anahtar yazımı AYNEN gider; kök şartı yok', () => {
    const one = functionCodeTracesHref({ window: win, attrKey: 'function_code', codes: ['CAF0001'] });
    expect(one.startsWith('/traces?')).toBe(true);
    expect(filtersOf(one)).toEqual([{ k: 'function_code', op: '=', v: ['CAF0001'] }]);
    const q = new URLSearchParams(one.slice(one.indexOf('?') + 1));
    expect(q.get('rootOnly')).toBe('false');
    expect(q.get('range')).toBeTruthy();
    const many = functionCodeTracesHref({ window: win, attrKey: 'FUNCTION_CODE', codes: ['A1', 'B2'] });
    expect(filtersOf(many)).toEqual([{ k: 'FUNCTION_CODE', op: 'IN', v: ['A1', 'B2'] }]);
  });
});

describe('oraclePaletteResults', () => {
  const resp: OracleOperationsResponse = {
    enabled: true, spanAttrKey: 'function_code',
    operations: [
      { operation: 'DIGITAL_TRANSFER_EFT_CONFIRM_SERVICE', rows: 370, lastSeen: 1, functionCodes: ['EFT001'], lastTraceId: 'ab12cd34ef56ab12cd34ef56ab12cd34', service: 'bsa-localtransfer-prod' },
      { operation: 'OP_MANY', rows: 90, lastSeen: 1, functionCodes: ['A1', 'B2', 'C3'] },
      { operation: 'OP_SERVICE_ONLY', rows: 12, lastSeen: 1, functionCodes: [], service: 'bsa-atm-core-prod' },
      { operation: 'OP_NOTHING', rows: 3, lastSeen: 1, functionCodes: [] },
    ],
  };

  it('fonksiyon kodu olan operasyon: Traces süzgeci + son hata trace\'i', () => {
    const r = oraclePaletteResults(resp, win);
    expect(r[0]).toMatchObject({ kind: 'operation', label: 'DIGITAL_TRANSFER_EFT_CONFIRM_SERVICE', hint: "Oracle operasyonu · fonksiyon kodu EFT001 → trace'ler" });
    expect(filtersOf(r[0].to)).toEqual([{ k: 'function_code', op: '=', v: ['EFT001'] }]);
    expect(r[1]).toMatchObject({ kind: 'trace', label: 'DIGITAL_TRANSFER_EFT_CONFIRM_SERVICE', hint: "Oracle · son hata trace'i" });
    expect(r[1].to).toContain('/trace?id=ab12cd34ef56ab12cd34ef56ab12cd34');
    expect(r[1].to).toContain('tab=logs');
  });

  it('çok kod kısaltılır ama süzgece hepsi girer; kod yoksa servisin hatalı trace\'leri; ikisi de yoksa sonuç YOK', () => {
    const r = oraclePaletteResults(resp, win);
    const many = r.find(x => x.label === 'OP_MANY');
    expect(many?.hint).toBe("Oracle operasyonu · fonksiyon kodu A1, B2 +1 → trace'ler");
    expect(filtersOf(many!.to)).toEqual([{ k: 'function_code', op: 'IN', v: ['A1', 'B2', 'C3'] }]);
    const svc = r.find(x => x.label === 'OP_SERVICE_ONLY');
    expect(svc?.hint).toBe("Oracle operasyonu · fonksiyon kodu yok → bsa-atm-core-prod hatalı trace'leri");
    const q = new URLSearchParams(svc!.to.slice(svc!.to.indexOf('?') + 1));
    expect(q.get('service')).toBe('bsa-atm-core-prod');
    expect(q.get('hasError')).toBe('true');
    expect(r.some(x => x.label === 'OP_NOTHING')).toBe(false);
    expect(r).toHaveLength(4);
  });

  it('kaynak yoksa / cevap boşsa sonuç yok; anahtar yazımı boşsa FUNCTION_CODE', () => {
    expect(oraclePaletteResults({ ...resp, enabled: false }, win)).toEqual([]);
    expect(oraclePaletteResults(null, win)).toEqual([]);
    const r = oraclePaletteResults({ ...resp, spanAttrKey: '' }, win);
    expect(filtersOf(r[0].to)).toEqual([{ k: 'FUNCTION_CODE', op: '=', v: ['EFT001'] }]);
  });
});

describe('oracleOperationQuery', () => {
  it('≥3 karakter, kırpılmış, harf KORUNUR; kısa / aşırı uzun reddedilir', () => {
    expect(oracleOperationQuery('  Digital_Eft ')).toBe('Digital_Eft');
    expect(oracleOperationQuery('ef')).toBeNull();
    expect(oracleOperationQuery('x'.repeat(81))).toBeNull();
  });
});

describe('CommandPalette kablosu', () => {
  it('Oracle isabetleri endpoint\'lerin ardından sıralanır ve kendi rozetini alır', () => {
    const src = readFileSync(resolve(__dirname, '../components/CommandPalette.tsx'), 'utf8');
    expect(src).toContain('api.oracleOperations(q, 6)');
    expect(src).toContain('[...endpoints, ...oracleOps]');
    expect(src).toContain("r.kind === 'operation' ? 'oracle'");
  });
});
