import { describe, it, expect } from 'vitest';
import { topByTimeShare, mergePerCluster, shareBar, parseEndpointsMode, timeShareOf } from './detailsEndpoints';
import type { EndpointRow } from '@/lib/types';

// v0.10.715 — Details Endpoints saf yarısı: süre payı sıralaması,
// cluster birleştirme, çubuk oranı, kip codec'i.
const ep = (path: string, calls: number, avgMs: number): EndpointRow => ({
  service: 'shop-payment', path, calls, errors: 0, errorRate: 0, avgMs, p99Ms: avgMs * 4,
  p50Ms: avgMs, p95Ms: avgMs * 3,
} as EndpointRow);

describe('detailsEndpoints', () => {
  it('süre payı = calls × avg; en pahalı önce, eşitlikte yol adı', () => {
    const rows = [ep('/b', 10, 10), ep('/a', 10, 10), ep('/c', 1000, 1), ep('/d', 1, 5000)];
    expect(topByTimeShare(rows, 3).map(r => r.path)).toEqual(['/d', '/c', '/a']);
    expect(timeShareOf(ep('/x', 3, 7))).toBe(21);
    expect(topByTimeShare(rows, 0)).toEqual([]);
  });
  it('cluster başına birleştirme: etiket + küresel sıralama', () => {
    const merged = mergePerCluster(['prod-eu', 'prod-us'], [[ep('/pay', 100, 10)], [ep('/pay', 20, 200), ep('/x', 1, 1)]], 2);
    expect(merged.map(r => `${r.cluster}:${r.path}`)).toEqual(['prod-us:/pay', 'prod-eu:/pay']);
    expect(mergePerCluster(['a'], [undefined], 5)).toEqual([]);
  });
  it('çubuk oranı en pahalıya göre', () => {
    const rows = [ep('/a', 100, 10), ep('/b', 50, 10)];
    expect(shareBar(rows[0], rows)).toBe(1);
    expect(shareBar(rows[1], rows)).toBe(0.5);
    expect(shareBar(rows[1], [])).toBe(0);
  });
  it('kip codec: yalnız "cluster" tanınır', () => {
    expect(parseEndpointsMode('cluster')).toBe('cluster');
    expect(parseEndpointsMode(null)).toBe('combined');
    expect(parseEndpointsMode('bogus')).toBe('combined');
  });
});
