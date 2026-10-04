// scopeParams.test.ts — v0.10.1082 (operator-reported, prod: "Error
// seçildiğinde histogram gelmiyor"). Liste ile Errors şeridi süzgeç
// parametrelerini TEK fonksiyondan alır; şerit yalnız Go
// `TraceErrorHistogramEligible` aynası koşulda /api/traces/error-histogram'a gider.

import { describe, expect, it } from 'vitest';
import type { FilterExpr } from '@/lib/types';
import { traceScopeParams, errorStripEligible, errorStripScopeOnly, type TraceScopeInput } from './scopeParams';

const fc: FilterExpr = { k: 'function_code', op: '=', v: ['KYC0001'] };
const pod: FilterExpr = { k: 'k8s.pod.name', op: '=', v: ['svc-a-7d9f-x2'] };

function input(over: Partial<TraceScopeInput['filter']> = {}, rest: Partial<Omit<TraceScopeInput, 'filter'>> = {}): TraceScopeInput {
  return {
    filter: { service: '', search: '', traceId: '', minMs: '', maxMs: '', hasError: true, rootOnly: false, requireServices: [], ...over },
    env: '', cluster: '', filtersEff: [fc], groupParam: '', ...rest,
  };
}

describe('traceScopeParams — listenin süzgeç alanları', () => {
  it('çipler JSON, env/cluster birinci-sınıf, boşlar yazılmaz', () => {
    const p = traceScopeParams(input({ service: 'svc-a' }, { env: 'prod', cluster: 'c1' }));
    expect(p).toEqual({
      service: 'svc-a', search: undefined, traceId: undefined, minMs: undefined, maxMs: undefined,
      hasError: true, rootOnly: undefined, env: 'prod', cluster: 'c1', services: undefined,
      filterGroup: undefined, filters: JSON.stringify([fc]),
    });
  });
  it('gruplu kök düz çipleri supersede eder (ikisi birden gitmez)', () => {
    const p = traceScopeParams(input({}, { groupParam: '{"join":"OR"}' }));
    expect(p.filterGroup).toBe('{"join":"OR"}');
    expect(p.filters).toBeUndefined();
  });
  it('trace id: tam 32-hex id gider; 32-hex olmayan kimlik ARAMA terimi olur', () => {
    const hex = 'ABCDEF0123456789abcdef0123456789';
    expect(traceScopeParams(input({ traceId: hex })).traceId).toBe(hex.toLowerCase());
    const p = traceScopeParams(input({ traceId: 'KYC-ID-42' }));
    expect(p.traceId).toBeUndefined();
    expect(p.search).toBe('KYC-ID-42');
  });
});

describe('errorStripEligible — Go TraceErrorHistogramEligible aynası', () => {
  it.each([
    ['Errors + function_code çipi', input(), true],
    ['Errors + k8s.pod.name çipi', input({}, { filtersEff: [pod] }), true],
    ['Errors + gruplu kök', input({}, { filtersEff: [], groupParam: '{"join":"OR"}' }), true],
    ['Root serbest', input({ rootOnly: true }), true],
    ['Errors yok', input({ hasError: false }), false],
    // v0.10.1101 — çipsiz Errors da error-histogram'a (liste "herhangi bir span hatalı").
    ['(a) çip yok, yalnız Errors', input({}, { filtersEff: [] }), true],
    ['(b) çip yok, Errors + servis', input({ service: 'svc-a' }, { filtersEff: [] }), true],
    ['(c) çip yok, Errors + ortam', input({}, { filtersEff: [], env: 'prod' }), true],
    ['(c) çip yok, Errors + küme', input({}, { filtersEff: [], cluster: 'c1' }), true],
    ['çip yok + Root → metric-batch', input({ rootOnly: true }, { filtersEff: [] }), false],
    ['çip yok, Errors yok → metric-batch (hacim fast-path)', input({ hasError: false, service: 'svc-a' }, { filtersEff: [] }), false],
    ['çip yok + arama → metric-batch', input({ search: 'timeout' }, { filtersEff: [] }), false],
    ['çip yok + süre → metric-batch', input({ minMs: '5' }, { filtersEff: [] }), false],
    ['arama', input({ search: 'timeout' }), false],
    ['kimlik araması', input({ traceId: 'KYC-ID-42' }), false],
    ['tam trace id', input({ traceId: '0123456789abcdef0123456789abcdef' }), false],
    ['minMs', input({ minMs: '5' }), false],
    ['maxMs', input({ maxMs: '900' }), false],
    ['services', input({ requireServices: ['a', 'b'] }), false],
  ] as const)('%s → %s', (_n, i, want) => {
    expect(errorStripEligible(traceScopeParams(i))).toBe(want);
  });
});

describe('errorStripScopeOnly — v0.10.1101 çipsiz Errors (ipucu metni)', () => {
  it.each([
    ['yalnız Errors', input({}, { filtersEff: [] }), true],
    ['Errors + servis + ortam', input({ service: 'svc-a' }, { filtersEff: [], env: 'prod' }), true],
    ['Errors + çip (1082 kipi)', input(), false],
    ['Errors + gruplu kök', input({}, { filtersEff: [], groupParam: '{"join":"OR"}' }), false],
    ['uygun değil (Root)', input({ rootOnly: true }, { filtersEff: [] }), false],
  ] as const)('%s → %s', (_n, i, want) => {
    expect(errorStripScopeOnly(traceScopeParams(i))).toBe(want);
  });
});
