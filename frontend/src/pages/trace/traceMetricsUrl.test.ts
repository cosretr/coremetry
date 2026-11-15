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

describe('parseMpod (v0.10.2207 — tek pod)', () => {
  it('ilk bilinen ad seçilir; eski virgüllü linkte de', () => {
    expect(parseMpod(`nope,${F(1)},${A},${F(2)}`, BY)).toEqual({ kind: 'pod', pod: F(1) });
    expect(parseMpod(A, BY)).toEqual({ kind: 'pod', pod: A });
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
      pod: F(0), closed: false, isDefault: true, win: 15, filter: 'all', query: '', grouping: 'service',
    });
  });
  it('mwin basamakları; bilinmeyen → 15', () => {
    expect(parseTraceMetricsUrl(sp('mwin=5'), BY, '').win).toBe(5);
    expect(parseTraceMetricsUrl(sp('mwin=60'), BY, '').win).toBe(60);
    expect(parseTraceMetricsUrl(sp('mwin=30'), BY, '').win).toBe(15);
  });
  it('mf / mq / mgrp=flat; eski msib / mview yok sayılır', () => {
    const u = parseTraceMetricsUrl(sp('mf=crit&mq=audit&mgrp=flat&msib=0&mview=pod'), BY, '');
    expect([u.filter, u.query, u.grouping]).toEqual(['crit', 'audit', 'flat']);
    expect(Object.keys(u).sort()).toEqual(['closed', 'filter', 'grouping', 'isDefault', 'pod', 'query', 'win']);
    expect(parseTraceMetricsUrl(sp('mf=weird'), BY, '').filter).toBe('all');
  });
  it('kapalı panel: pod boş', () => {
    const closed = parseTraceMetricsUrl(sp('mpod=-'), BY, F(0));
    expect([closed.pod, closed.closed]).toEqual(['', true]);
  });
});

describe('applyTraceMetricsPatch — üst kümeden yazar', () => {
  const SEARCH = '?id=4f1c9e2a7b3d8e60&span=00ab&tab=metrics&xn=1&range=1h';
  it('yabancı id / span / tab / xn / range korunur', () => {
    const n = applyTraceMetricsPatch(SEARCH, { pod: F(1) });
    expect([n.get('id'), n.get('span'), n.get('tab'), n.get('xn'), n.get('range')]).toEqual(['4f1c9e2a7b3d8e60', '00ab', 'metrics', '1', '1h']);
    expect(n.get('mpod')).toBe(F(1));
  });
  it('varsayılan değerler URL\'e yazılmaz; kapalı "-"', () => {
    const n = applyTraceMetricsPatch(`${SEARCH}&mwin=5&mf=err&mq=x&mgrp=flat&mpod=${F(0)}`, {
      win: 15, filter: 'all', query: '  ', grouping: 'service',
    });
    for (const k of ['mwin', 'mf', 'mq', 'mgrp']) expect(n.has(k), k).toBe(false);
    const c = applyTraceMetricsPatch(n.toString(), { pod: 'closed' });
    expect(c.get('mpod')).toBe('-');
    expect(applyTraceMetricsPatch(c.toString(), { pod: null }).has('mpod')).toBe(false);
    const w = applyTraceMetricsPatch('', { win: 60, filter: 'slow', query: 'fraud', grouping: 'flat' });
    expect(w.toString()).toBe('mwin=60&mf=slow&mq=fraud&mgrp=flat');
  });
  it('TRACE_METRICS_URL_PARAMS beşini listeler; temizleyici hepsini siler, yabancıya dokunmaz', () => {
    expect([...TRACE_METRICS_URL_PARAMS]).toEqual(['mpod', 'mwin', 'mf', 'mq', 'mgrp']);
    const s = sp(`${SEARCH}&mpod=-&mwin=5&mf=err&mq=x&mgrp=flat`);
    clearTraceMetricsParams(s);
    expect(s.toString()).toBe('id=4f1c9e2a7b3d8e60&span=00ab&tab=metrics&xn=1&range=1h');
  });
});
