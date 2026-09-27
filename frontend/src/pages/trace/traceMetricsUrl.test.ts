import { describe, it, expect } from 'vitest';
import {
  applyTraceMetricsPatch, clearTraceMetricsParams, parseMpod, parseTraceMetricsUrl, TRACE_METRICS_URL_PARAMS,
} from './traceMetricsUrl';

// v0.10.968 — Trace › Metrics URL durumu: okuma doğrulaması + ÜST KÜMEDEN
// yazıcı (router'ın `prev`i bu sayfada bayat alt küme — v0.8.256 sınıfı).
const F = (i: number) => `fraud-score-prod-6b7d9f8c5-f${i}aaa`;
const A = 'audit-log-prod-5f9c7d6b8-q2w4e';
const BY = new Map<string, { service: string }>([
  ...Array.from({ length: 6 }, (_, i) => [F(i), { service: 'fraud-score-prod' }] as const),
  [A, { service: 'audit-log-prod' }],
]);
const sp = (q: string) => new URLSearchParams(q);

describe('parseMpod', () => {
  it('bilinmeyen düşer; ilkinin servisinden olmayan düşer; >4 kesilir; tekrar düşer', () => {
    expect(parseMpod(`nope,${F(1)},${A},${F(2)},${F(1)}`, BY)).toEqual({ kind: 'list', pods: [F(1), F(2)] });
    expect(parseMpod([0, 1, 2, 3, 4, 5].map(F).join(','), BY)).toEqual({ kind: 'list', pods: [F(0), F(1), F(2), F(3)] });
    expect(parseMpod(`${A},${F(0)}`, BY)).toEqual({ kind: 'list', pods: [A] });
  });
  it('"-" kapalı; yok ya da hepsi bilinmeyen → varsayılan', () => {
    expect(parseMpod('-', BY)).toEqual({ kind: 'closed' });
    expect(parseMpod(null, BY)).toEqual({ kind: 'default' });
    expect(parseMpod('', BY)).toEqual({ kind: 'default' });
    expect(parseMpod('nope,also-nope', BY)).toEqual({ kind: 'default' });
  });
});

describe('parseTraceMetricsUrl', () => {
  it('varsayılanlar', () => {
    expect(parseTraceMetricsUrl(sp(''), BY, F(0))).toEqual({
      compare: [F(0)], closed: false, isDefault: true, win: 15, filter: 'all', query: '',
      grouping: 'service', siblingLines: true, focus: false,
    });
  });
  it('mwin basamakları; bilinmeyen → 15', () => {
    expect(parseTraceMetricsUrl(sp('mwin=5'), BY, '').win).toBe(5);
    expect(parseTraceMetricsUrl(sp('mwin=60'), BY, '').win).toBe(60);
    expect(parseTraceMetricsUrl(sp('mwin=30'), BY, '').win).toBe(15);
  });
  it('mf / mq / mgrp=flat / msib=0', () => {
    const u = parseTraceMetricsUrl(sp('mf=crit&mq=audit&mgrp=flat&msib=0'), BY, '');
    expect([u.filter, u.query, u.grouping, u.siblingLines]).toEqual(['crit', 'audit', 'flat', false]);
    expect(parseTraceMetricsUrl(sp('mf=weird'), BY, '').filter).toBe('all');
  });
  it('mview yalnız seçim varken; kapalı panelde düşer', () => {
    expect(parseTraceMetricsUrl(sp(`mpod=${F(1)}&mview=pod`), BY, F(0)).focus).toBe(true);
    expect(parseTraceMetricsUrl(sp('mview=pod'), BY, F(0)).focus).toBe(true); // varsayılan seçim de seçimdir
    const closed = parseTraceMetricsUrl(sp('mpod=-&mview=pod'), BY, F(0));
    expect([closed.compare, closed.closed, closed.focus]).toEqual([[], true, false]);
    expect(parseTraceMetricsUrl(sp('mview=pod'), BY, '').focus).toBe(false);
  });
});

describe('applyTraceMetricsPatch — üst kümeden yazar', () => {
  const SEARCH = '?id=4f1c9e2a7b3d8e60&span=00ab&tab=metrics&xn=1&range=1h';
  it('yabancı id / span / tab / xn / range korunur', () => {
    const n = applyTraceMetricsPatch(SEARCH, { compare: [F(1), F(2)] });
    expect([n.get('id'), n.get('span'), n.get('tab'), n.get('xn'), n.get('range')]).toEqual(['4f1c9e2a7b3d8e60', '00ab', 'metrics', '1', '1h']);
    expect(n.get('mpod')).toBe(`${F(1)},${F(2)}`);
  });
  it('varsayılan değerler URL\'e yazılmaz; kapalı "-" ve mview düşer', () => {
    const n = applyTraceMetricsPatch(`${SEARCH}&mwin=5&mf=err&mq=x&mgrp=flat&msib=0&mview=pod&mpod=${F(0)}`, {
      win: 15, filter: 'all', query: '  ', grouping: 'service', siblingLines: true,
    });
    for (const k of ['mwin', 'mf', 'mq', 'mgrp', 'msib']) expect(n.has(k), k).toBe(false);
    expect(n.get('mview')).toBe('pod');
    const c = applyTraceMetricsPatch(n.toString(), { compare: 'closed' });
    expect([c.get('mpod'), c.has('mview')]).toEqual(['-', false]);
    expect(applyTraceMetricsPatch(c.toString(), { compare: null }).has('mpod')).toBe(false);
    const w = applyTraceMetricsPatch('', { win: 60, filter: 'slow', query: 'fraud', grouping: 'flat', siblingLines: false, focus: true });
    expect(w.toString()).toBe('mwin=60&mf=slow&mq=fraud&mgrp=flat&msib=0&mview=pod');
  });
  it('TRACE_METRICS_URL_PARAMS yedisini listeler; temizleyici hepsini siler, yabancıya dokunmaz', () => {
    expect([...TRACE_METRICS_URL_PARAMS]).toEqual(['mpod', 'mwin', 'mf', 'mq', 'mgrp', 'msib', 'mview']);
    const s = sp(`${SEARCH}&mpod=-&mwin=5&mf=err&mq=x&mgrp=flat&msib=0&mview=pod`);
    clearTraceMetricsParams(s);
    expect(s.toString()).toBe('id=4f1c9e2a7b3d8e60&span=00ab&tab=metrics&xn=1&range=1h');
  });
});
