// v0.10.1862 — builder satırı → MetricsQL (Faz 2 dilim 2.2).
import { describe, expect, it } from 'vitest';
import {
  unsubstitutePos, compileMxQuery, compileMxFormula, selectorText, substituteIntervals, rateWindowSec, legendFor, labelsOf,
  hasIntervalVars, quoteProm,
} from './compile';
import { blankMxQuery, type MxQuery } from './model';

const q = (p: Partial<MxQuery>): MxQuery => ({ ...blankMxQuery('A'), ...p });
const M = 'http_server_request_duration_seconds_count';

describe('compileMxQuery', () => {
  it.each<[string, Partial<MxQuery>, string]>([
    ['boş metrik', { metric: '' }, ''],
    ['sayaç rate + sum by (mockup görünüm 1)',
      { metric: M, fn: 'rate', agg: 'sum', by: ['service_name', 'http_route'], filters: [{ k: 'http_route', op: '!~', v: '/health.*' }] },
      `sum by (service_name, http_route) (rate(${M}{http_route!~"/health.*"}[$__rate_interval]))`],
    ['increase pencere = adım', { metric: M, fn: 'increase', agg: 'sum' }, `sum(increase(${M}[$__interval]))`],
    ['irate', { metric: M, fn: 'irate', agg: 'max', by: ['k8s_pod_name'] }, `max by (k8s_pod_name) (irate(${M}[$__rate_interval]))`],
    ['gösterge ham avg', { metric: 'jvm_memory_used_bytes', fn: 'raw', agg: 'avg' }, 'avg(jvm_memory_used_bytes)'],
    ['toplama yok → by yok sayılır', { metric: 'jvm_memory_used_bytes', fn: 'raw', agg: 'none', by: ['x'] }, 'jvm_memory_used_bytes'],
    ['histogram p95 → kova + le, vmrange önde',
      { metric: 'http_server_request_duration_seconds_bucket', fn: 'p95', agg: 'sum', by: ['service_name', 'le'] },
      'histogram_quantile(0.95, sum by (le, vmrange, service_name) (rate(http_server_request_duration_seconds_bucket[$__rate_interval])))'],
    ['histogram adı _bucket eklenir', { metric: 'http.server.request.duration', fn: 'p99', agg: 'sum' },
      'histogram_quantile(0.99, sum by (le, vmrange) (rate({__name__="http.server.request.duration_bucket"}[$__rate_interval])))'],
    ['çok eşleyici + kaçış', { metric: 'up', fn: 'raw', agg: 'count', filters: [{ k: 'job', op: '=', v: 'a"b\\c' }, { k: 'service.name', op: '=~', v: 'x|y' }] },
      'count(up{job="a\\"b\\\\c", "service.name"=~"x|y"})'],
    ['formül satırı derlenmez', { kind: 'formula', metric: M, expr: 'A' }, ''],
  ])('%s', (_n, p, want) => expect(compileMxQuery(q(p))).toBe(want));
});

describe('selectorText', () => {
  it('kimlik olmayan ad __name__ eşleyicisine', () => {
    expect(selectorText('a.b', [])).toBe('{__name__="a.b"}');
    expect(selectorText('up', [], [{ k: 'k8s_cluster_name', op: '=', v: 'acme-prod-1' }])).toBe('up{k8s_cluster_name="acme-prod-1"}');
  });
  it('quoteProm yeni satır', () => expect(quoteProm('a\nb')).toBe('"a\\nb"'));
});

describe('compileMxFormula (sunucuda hesap)', () => {
  const qs = [
    q({ id: 'A', metric: M, fn: 'rate', agg: 'sum', by: ['service_name'] }),
    q({ id: 'B', metric: M, fn: 'rate', agg: 'sum', by: ['service_name'], filters: [{ k: 'code', op: '=~', v: '5..' }] }),
    q({ id: 'C', metric: '' }),
  ];
  it('B / A * 100', () => {
    const r = compileMxFormula('B / A * 100', qs);
    expect(r).toEqual({
      ok: true, refs: ['B', 'A'],
      expr: `(sum by (service_name) (rate(${M}{code=~"5.."}[$__rate_interval]))) / (sum by (service_name) (rate(${M}[$__rate_interval]))) * 100`,
    });
  });
  it.each([
    ['', 'Formül boş'],
    ['Z / A', 'Bilinmeyen sorgu: Z'],
    ['C + 1', 'C sorgusunda metrik seçilmedi'],
    ['A ; 1', 'Formülde izin verilmeyen karakter: ;'],
    ['2 * 3', 'Formül en az bir sorguya başvurmalı (A, B, …)'],
    ['(A + B', 'Formülde parantezler dengesiz'],
  ])('%s → hata', (expr, error) => expect(compileMxFormula(expr, qs)).toEqual({ ok: false, error }));
});

describe('pencere yer tutucuları', () => {
  it.each([
    [15, 'rate(x[300s]) increase(x[15s])'],
    [60, 'rate(x[300s]) increase(x[60s])'],
    [600, 'rate(x[600s]) increase(x[600s])'],
    [0, 'rate(x[300s]) increase(x[1s])'],
  ])('adım %i sn', (step, want) => {
    expect(substituteIntervals('rate(x[$__rate_interval]) increase(x[$__interval])', step)).toBe(want);
  });
  it('rateWindowSec tabanı 300', () => {
    expect(rateWindowSec(30)).toBe(300);
    expect(rateWindowSec(3600)).toBe(3600);
  });
  it('hasIntervalVars', () => {
    expect(hasIntervalVars('rate(x[$__rate_interval])')).toBe(true);
    expect(hasIntervalVars('rate(x[5m])')).toBe(false);
  });
});

describe('lejant', () => {
  const gk = ['http_route="/checkout"', 'service_name="orders-api"'];
  it('labelsOf', () => expect(labelsOf(gk)).toEqual({ http_route: '/checkout', service_name: 'orders-api' }));
  it.each([
    ['{{service_name}} {{http_route}}', [], 'orders-api /checkout'],
    ['hata % {{service_name}}', [], 'hata % orders-api'],
    ['', ['service_name', 'http_route'], 'orders-api · /checkout'],
    ['', [], 'http_route=/checkout, service_name=orders-api'],
    ['{{yok}}', [], 'A'],
  ])('%s', (fmt, by, want) => expect(legendFor(gk, fmt, by, 'A')).toBe(want));
  it('etiketsiz seri → sorgu kimliği', () => expect(legendFor([], '', [], 'A')).toBe('A'));
});

describe('unsubstitutePos (hata konumu çözülmüş metinden operatörün metnine)', () => {
  const orig = 'rate(x[$__rate_interval]) + y)';
  // çözülmüş: 'rate(x[300s]) + y)' — fazla ')' çözülmüşte 17, özgünde 29
  it.each([[0, 0], [7, 7], [9, 7], [12, 24], [17, 29]])('%i → %i', (pos, want) => {
    expect(unsubstitutePos(orig, 15, pos)).toBe(want);
  });
  it('yer tutucusuz metin aynen', () => expect(unsubstitutePos('up)', 15, 2)).toBe(2));
});
