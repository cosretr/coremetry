// v0.10.1866 — Explore metrik satırı ortak QueryRow'u çizer (Faz 2 dilim
// 2.6). Kabul ölçütü: var olan Explore `?q=` linkleri AYNI sonucu verir —
// dokunulmamış satır bayt-aynı kalır (sunucuya giden metrik/agg/scope/
// filtre/split değişmez); düzenlenen alan eşdeğer biçime çevrilir.
import { describe, expect, it } from 'vitest';
import { decodeBuilder, encodeBuilder } from './urlCodec';
import { blankQuery, type BuilderQuery } from './model';
import { exploreAggOf, fromMx, toMx } from './mxAdapter';

const base = (p: Partial<BuilderQuery>): BuilderQuery => ({ ...blankQuery('A', 'metric'), ...p });

const FIXTURES: [string, BuilderQuery][] = [
  ['rate + scope + IN', base({ metric: 'http_server_request_duration_seconds_count', agg: 'rate', scope: 'orders-api',
    filters: [{ k: 'http.route', op: 'IN', v: ['/a', '/b'] }], splitBy: ['http.route'] })],
  ['last (sunucu max(last_over_time))', base({ metric: 'jvm_memory_used_bytes', agg: 'last', unit: 'By' })],
  ['p99 + LIKE', base({ metric: 'http.server.request.duration', agg: 'p99', filters: [{ k: 'http.route', op: 'LIKE', v: ['api'] }] })],
  ['kapalı satır', base({ metric: 'up', agg: 'avg', enabled: false })],
];

describe('toMx → fromMx (dokunulmamış satır)', () => {
  it.each(FIXTURES)('%s: bayt-aynı, ?q= aynı', (_n, q) => {
    const back = fromMx(toMx(q), q);
    expect(back).toEqual(q);
    const st = { queries: [q], formula: '', viz: 'line' as const, step: 0 };
    expect(encodeBuilder({ ...st, queries: [back] })).toBe(encodeBuilder(st));
    expect(decodeBuilder(encodeBuilder(st))!.queries[0]).toEqual(decodeBuilder(encodeBuilder({ ...st, queries: [back] }))!.queries[0]);
  });
});

describe('fromMx (düzenleme)', () => {
  const q = FIXTURES[0][1];
  it('gizle → enabled false, başka alan değişmez', () => {
    expect(fromMx({ ...toMx(q), hidden: true }, q)).toEqual({ ...q, enabled: false });
  });
  it('süzgeç eklenir: scope çipten geri okunur, diğerleri eşdeğer FilterExpr', () => {
    const mx = toMx(q);
    const r = fromMx({ ...mx, filters: [...mx.filters, { k: 'code', op: '=~', v: '5..' }] }, q);
    expect(r.scope).toBe('orders-api');
    expect(r.filters).toEqual([{ k: 'http.route', op: '=~', v: ['/a|/b'] }, { k: 'code', op: '=~', v: ['5..'] }]);
    expect(r.agg).toBe('rate');
  });
  it('scope çipi kaldırılır → scope boş', () => {
    const mx = toMx(q);
    expect(fromMx({ ...mx, filters: mx.filters.slice(1) }, q).scope).toBe('');
  });
  it('fonksiyon/toplama → Explore agg', () => {
    expect(fromMx({ ...toMx(q), fn: 'raw', agg: 'max' }, q).agg).toBe('max');
  });
  it('metrik değişir → birim taşınır', () => {
    const r = fromMx({ ...toMx(q), metric: 'jvm_memory_used_bytes', unit: 'By', fn: 'raw', agg: 'avg' }, q);
    expect(r).toMatchObject({ metric: 'jvm_memory_used_bytes', unit: 'By', agg: 'avg' });
  });
  it.each([
    ['rate', 'avg', 'rate'], ['increase', 'sum', 'increase'], ['p95', 'sum', 'p95'], ['p90', 'sum', 'p95'],
    ['raw', 'min', 'min'], ['raw', 'count', 'avg'], ['raw', 'none', 'avg'],
  ] as const)('exploreAggOf(%s, %s) = %s', (fn, agg, want) => expect(exploreAggOf(fn, agg)).toBe(want));
});
