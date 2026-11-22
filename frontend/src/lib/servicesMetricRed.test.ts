import { describe, it, expect } from 'vitest';
import {
  servicesSortScope, servicesSortNote, resortByDisplayed, servicesAggregate, rowSpark, aggregateSpark, mergeBuckets,
} from './servicesMetricRed';
import type { Service, ServiceRedMetricRow, ServiceRedSparkRow, SparklineBucket } from './types';

const svc = (name: string, spanCount: number, errorRate: number, avg: number, p99: number): Service => ({
  name, spanCount, errorCount: Math.round(spanCount * errorRate / 100), errorRate,
  avgDurationMs: avg, p99DurationMs: p99, apdex: 0.9, apdexThresholdMs: 200,
});
const rm = (requests: number, errorRate?: number, avgMs?: number, p99Ms?: number): ServiceRedMetricRow =>
  ({ source: 'http', requests, ratePerSec: requests / 3600, errorRate, avgMs, p99Ms });
const b = (t: number, spans: number, errs = 0, avgMs = 0, p99Ms = 0): SparklineBucket => ({ t, spans, errs, avgMs, p99Ms });

describe('servicesSortScope', () => {
  it('client only when the whole result is on page 0', () => {
    expect(servicesSortScope(0, 30, false, 50)).toBe('client');
    expect(servicesSortScope(0, 50, true, 50)).toBe('client');
    expect(servicesSortScope(0, 51, true, 50)).toBe('server');
    expect(servicesSortScope(1, 30, false, 50)).toBe('server');
    expect(servicesSortScope(0, null, false, 50)).toBe('client');
    expect(servicesSortScope(0, null, true, 50)).toBe('server');
  });
});

describe('resortByDisplayed', () => {
  const rows = [svc('svc-a', 1000, 1, 10, 100), svc('svc-b', 500, 2, 20, 200), svc('svc-c', 100, 3, 30, 300)];
  const metric = { 'svc-c': rm(20000, 0.5, 5, 50) };
  it('reorders by the shown value on a single page, pinned first', () => {
    expect(resortByDisplayed(rows, metric, 'spanCount', 'desc', 'client', new Set()).map(r => r.name))
      .toEqual(['svc-c', 'svc-a', 'svc-b']);
    expect(resortByDisplayed(rows, metric, 'p99', 'desc', 'client', new Set(['svc-c'])).map(r => r.name))
      .toEqual(['svc-c', 'svc-b', 'svc-a']);
  });
  it('keeps the server order when paged, on non-metric columns or without metric', () => {
    expect(resortByDisplayed(rows, metric, 'spanCount', 'desc', 'server', new Set())).toBe(rows);
    expect(resortByDisplayed(rows, metric, 'apdex', 'asc', 'client', new Set())).toBe(rows);
    expect(resortByDisplayed(rows, {}, 'spanCount', 'desc', 'client', new Set())).toBe(rows);
  });
});

describe('servicesAggregate', () => {
  it('sums / weights the shown values and flags mixed sources', () => {
    const rows = [svc('svc-a', 100, 10, 50, 400), svc('svc-b', 100, 0, 10, 100)];
    const a = servicesAggregate(rows, { 'svc-b': rm(900, 0, 10, 900) })!;
    expect(a.requests).toBe(1000);
    expect(a.errorRate).toBeCloseTo(1); // 10 err / 1000
    expect(a.avgMs).toBeCloseTo((100 * 50 + 900 * 10) / 1000);
    expect(a.p99Ms).toBe(900);
    expect(a.mixed).toBe(true);
    expect(a.metricRows).toBe(1);
    const all = servicesAggregate(rows, { 'svc-a': rm(10), 'svc-b': rm(10) })!;
    expect(all.mixed).toBe(false);
    expect(all.errorRate).toBeCloseTo(5); // no metric status → shown span error rate
    expect(servicesAggregate([], {})).toBeNull();
  });
});

describe('sparklines', () => {
  const span = [b(1, 10, 1, 5, 9)];
  const metric: ServiceRedSparkRow = { source: 'http', buckets: [b(100, 200, 4, 7, 11), b(160, 100, 0, 6, 10)], hasErrors: true, hasLatency: false };
  it('row prefers metric, per-series fallback to spans', () => {
    const r = rowSpark(span, metric);
    expect(r.metric).toBe(true);
    expect(r.count).toEqual([200, 100]);
    expect(r.err).toEqual([2, 0]);
    expect(r.p99).toEqual([9]); // no metric latency → span
    expect(rowSpark(span, undefined)).toMatchObject({ metric: false, count: [10] });
  });
  it('aggregate uses metric only when every row has full metric sparks', () => {
    const rows = [svc('svc-a', 1, 0, 1, 1), svc('svc-b', 1, 0, 1, 1)];
    const full = { ...metric, hasLatency: true };
    expect(aggregateSpark(rows, { 'svc-a': span, 'svc-b': span }, { 'svc-a': full }).metric).toBe(false);
    const both = aggregateSpark(rows, {}, { 'svc-a': full, 'svc-b': full });
    expect(both.metric).toBe(true);
    expect(both.buckets[0]).toMatchObject({ t: 100, spans: 400, errs: 8, avgMs: 7, p99Ms: 11 });
  });
  it('mergeBuckets weights avg by count', () => {
    expect(mergeBuckets([[b(1, 1, 0, 10, 1)], [b(1, 3, 0, 20, 5)]])[0]).toEqual({ t: 1, spans: 4, errs: 0, avgMs: 17.5, p99Ms: 5 });
  });
});

describe('servicesSortNote (v0.10.2444)', () => {
  it('filo sırası gelince fleet; geri düşüşte eski kural; metrik dışı sütun null', () => {
    const ranked = { ranked: true, metricRanked: 3 };
    const fallback = { ranked: false, metricRanked: 0, notes: ['pencere 24 saati aşıyor'] };
    expect(servicesSortNote('p99', false, 'server', ranked)).toBe('fleet');
    expect(servicesSortNote('p99', true, 'server', fallback)).toBe('spans');
    expect(servicesSortNote('errorRate', true, 'client', fallback)).toBe('metric');
    expect(servicesSortNote('spanCount', false, 'server', fallback)).toBeNull();
    expect(servicesSortNote('apdex', true, 'server', ranked)).toBeNull();
    expect(servicesSortNote('avg', true, 'server', null)).toBe('spans');
  });
});
