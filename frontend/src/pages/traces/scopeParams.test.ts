// scopeParams.test.ts — v0.10.1082 (operator-reported, prod: "Error
// seçildiğinde histogram gelmiyor"). Liste ile Errors şeridi süzgeç
// parametrelerini TEK fonksiyondan alır; şerit yalnız Go
// `TraceErrorHistogramEligible` aynası koşulda /api/traces/error-histogram'a gider.

import { describe, expect, it } from 'vitest';
import type { FilterExpr } from '@/lib/types';
import { traceScopeParams, errorStripEligible, errorStripScopeOnly, chipStripEligible, type TraceScopeInput } from './scopeParams';
import { encodeFilterGroup } from '@/lib/urlState';

const fc: FilterExpr = { k: 'function_code', op: '=', v: ['KYC0001'] };
const pod: FilterExpr = { k: 'k8s.pod.name', op: '=', v: ['svc-a-7d9f-x2'] };

function input(over: Partial<TraceScopeInput['filter']> = {}, rest: Partial<Omit<TraceScopeInput, 'filter'>> = {}): TraceScopeInput {
  return {
    filter: { service: '', search: '', minMs: '', maxMs: '', hasError: true, rootOnly: false, requireServices: [], ...over },
    env: '', cluster: '', filtersEff: [fc], groupParam: '', ...rest,
  };
}

describe('traceScopeParams — listenin süzgeç alanları', () => {
  it('çipler JSON, env/cluster birinci-sınıf, boşlar yazılmaz', () => {
    const p = traceScopeParams(input({ service: 'svc-a' }, { env: 'prod', cluster: 'c1' }));
    expect(p).toEqual({
      service: 'svc-a', search: undefined, minMs: undefined, maxMs: undefined,
      hasError: true, rootOnly: undefined, env: 'prod', cluster: 'c1', services: undefined,
      filterGroup: undefined, filters: JSON.stringify([fc]),
    });
  });
  it('gruplu kök düz çipleri supersede eder (ikisi birden gitmez)', () => {
    const p = traceScopeParams(input({}, { groupParam: '{"join":"OR"}' }));
    expect(p.filterGroup).toBe('{"join":"OR"}');
    expect(p.filters).toBeUndefined();
  });
  // v0.10.1423 — Trace ID kutusu kalktı: sayfa `traceId` göndermez; kimlik
  // (function_id) operasyon kutusunun `search` terimidir.
  it('kimlik operasyon kutusunun ARAMA terimi; traceId hiç yazılmaz', () => {
    const p = traceScopeParams(input({ search: ' KYC-ID-42 ' }));
    expect(p.search).toBe('KYC-ID-42');
    expect('traceId' in p).toBe(false);
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
    ['kimlik araması', input({ search: 'KYC-ID-42' }), false],
    ['minMs', input({ minMs: '5' }), false],
    ['maxMs', input({ maxMs: '900' }), false],
    ['services', input({ requireServices: ['a', 'b'] }), false],
    // Trace-level NOT HAS → metric-batch (it carries the list's anti-join).
    ['NOT HAS çipi', input({}, { filtersEff: [{ k: 'function_code', op: 'NOT HAS', v: ['X'] }] }), false],
    ['NOT HAS düz-AND grupta', input({}, { filtersEff: [], groupParam: '{"join":"AND","filters":[{"k":"function_code","op":"NOT HAS","v":["X"]}],"groups":[{"join":"OR","filters":[]}]}' }), false],
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

// v0.10.1801 — Operator-reported (test ortamı): /traces?range=24h&service=X&
// filters=[http.route =, channel_code =] → liste 15+ satır, şerit "0 SPANS ·
// No traces in view to bucket" (Errors kapalı). Liste çipleri trace düzeyinde
// keser; metric-batch aynı span'de AND'liyordu. Bu şekil ④ çip-trace yoluna.
describe('chipStripEligible — Go traceChipTraceEligible aynası', () => {
  const route: FilterExpr = { k: 'http.route', op: '=', v: ['/api/v1/orders'] };
  const channel: FilterExpr = { k: 'channel_code', op: '=', v: ['MOBILE'] };
  const method: FilterExpr = { k: 'http.method', op: '=', v: ['POST'] };
  const p = (over: Partial<TraceScopeInput['filter']> = {}, rest: Partial<Omit<TraceScopeInput, 'filter'>> = {}) =>
    traceScopeParams(input({ service: 'orders-api', hasError: false, ...over }, { filtersEff: [route, channel], ...rest }));

  it('operatör URL şekli: servis + http.route + channel_code, Errors kapalı → çip yolu', () => {
    expect(chipStripEligible(p())).toBe(true);
    expect(errorStripEligible(p())).toBe(false);
  });
  it('Root serbest', () => expect(chipStripEligible(p({ rootOnly: true }))).toBe(true));
  it('tek çip → metric-batch', () => expect(chipStripEligible(p({}, { filtersEff: [channel] }))).toBe(false));
  it('yalnız giriş anahtarları (entry kapsamı) → metric-batch', () =>
    expect(chipStripEligible(p({}, { filtersEff: [route, method] }))).toBe(false));
  it('servissiz → metric-batch', () => expect(chipStripEligible(p({ service: '' }))).toBe(false));
  it('Errors açık → eski error-histogram kararı (çip yolu değil)', () => {
    expect(chipStripEligible(p({ hasError: true }))).toBe(false);
    expect(errorStripEligible(p({ hasError: true }))).toBe(true);
  });
  it('arama / süre / services / NOT HAS → metric-batch', () => {
    expect(chipStripEligible(p({ search: 'x' }))).toBe(false);
    expect(chipStripEligible(p({ minMs: '5' }))).toBe(false);
    expect(chipStripEligible(p({ requireServices: ['a', 'b'] }))).toBe(false);
    expect(chipStripEligible(p({}, { filtersEff: [route, channel, { k: 'peer.service', op: 'NOT HAS', v: ['cache'] }] }))).toBe(false);
  });
  it('gruplu kök: yapraklar sayılır', () => {
    const g = encodeFilterGroup({ join: 'OR', filters: [route], groups: [{ join: 'AND', filters: [channel] }] });
    expect(chipStripEligible(p({}, { groupParam: g }))).toBe(true);
  });
});
