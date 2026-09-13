import { describe, it, expect } from 'vitest';
import { podTotals, summarizeInfraClusters, pctOfLimit, clusterStatus } from './infraClusters';
import type { ClusterPodRow } from '@/lib/types';

// v0.10.718 — Infrastructure dilim 1: cluster tablosu + KPI toplamları saf
// çekirdeği. Sentetik adlar (prod-eu / prod-us), gerçek kurum verisi yok.
const pod = (p: Partial<ClusterPodRow> & { pod: string; cluster: string }): ClusterPodRow => ({
  namespace: 'shop', cpuCores: 0, memBytes: 0, ...p,
});

describe('podTotals', () => {
  it('faz bilinmiyorsa running/failing 0 ama phaseKnown false; limit/restart null', () => {
    const t = podTotals([pod({ pod: 'a', cluster: 'prod-eu', cpuCores: 1, memBytes: 10, restartsUnknown: true })]);
    expect(t).toMatchObject({ pods: 1, phaseKnown: false, running: 0, failing: 0, cpuCores: 1, memBytes: 10 });
    expect(t.cpuLimitCores).toBeNull();
    expect(t.memLimitBytes).toBeNull();
    expect(t.restarts).toBeNull();
    expect(t.topRestart).toBeNull();
  });
  it('Succeeded failing sayılmaz; limitler yalnız bilinenlerden; en çok restart pod', () => {
    const t = podTotals([
      pod({ pod: 'a', cluster: 'prod-us', phase: 'Running', cpuLimitCores: 2, memLimitBytes: 100, restarts: 1 }),
      pod({ pod: 'b', cluster: 'prod-us', phase: 'CrashLoopBackOff', restarts: 8 }),
      pod({ pod: 'c', cluster: 'prod-us', phase: 'Succeeded', restarts: 0 }),
    ]);
    expect(t).toMatchObject({ pods: 3, phaseKnown: true, running: 1, failing: 1, cpuLimitCores: 2, memLimitBytes: 100, restarts: 9 });
    expect(t.topRestart).toEqual({ pod: 'b', restarts: 8 });
  });
});

describe('summarizeInfraClusters', () => {
  it('verilen sırayla, baskın namespace ile', () => {
    const rows = [
      pod({ pod: 'a', cluster: 'prod-us', namespace: 'shop', phase: 'Running' }),
      pod({ pod: 'b', cluster: 'prod-eu', namespace: 'shop-x', phase: 'Running' }),
      pod({ pod: 'c', cluster: 'prod-eu', namespace: 'shop', phase: 'Running' }),
      pod({ pod: 'd', cluster: 'prod-eu', namespace: 'shop', phase: 'Running' }),
    ];
    const out = summarizeInfraClusters(rows, ['prod-eu', 'prod-us']);
    expect(out.map(r => r.cluster)).toEqual(['prod-eu', 'prod-us']);
    expect(out[0]).toMatchObject({ namespace: 'shop', pods: 3, running: 3 });
    expect(out[1]).toMatchObject({ namespace: 'shop', pods: 1 });
  });
});

describe('pctOfLimit / clusterStatus', () => {
  it('limit yoksa null, varsa yuvarlanmış yüzde', () => {
    expect(pctOfLimit(6.4, null)).toBeNull();
    expect(pctOfLimit(6.4, 0)).toBeNull();
    expect(pctOfLimit(6.4, 12)).toBe(53);
  });
  it('durum: bilinmiyor / all running / n failing (yarı ve üstü err)', () => {
    expect(clusterStatus(podTotals([pod({ pod: 'a', cluster: 'c' })]))).toEqual({ text: 'durum bilinmiyor', tone: 'gray' });
    expect(clusterStatus(podTotals([pod({ pod: 'a', cluster: 'c', phase: 'Running' })]))).toEqual({ text: 'all running', tone: 'ok' });
    const two = podTotals([pod({ pod: 'a', cluster: 'c', phase: 'Running' }), pod({ pod: 'b', cluster: 'c', phase: 'Pending' }), pod({ pod: 'c', cluster: 'c', phase: 'Running' })]);
    expect(clusterStatus(two)).toEqual({ text: '1 failing', tone: 'warn' });
    const half = podTotals([pod({ pod: 'a', cluster: 'c', phase: 'Failed' }), pod({ pod: 'b', cluster: 'c', phase: 'Running' })]);
    expect(clusterStatus(half)).toEqual({ text: '1 failing', tone: 'err' });
  });
});
