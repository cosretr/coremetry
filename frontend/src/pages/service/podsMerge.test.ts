import { describe, it, expect } from 'vitest';
import { mergePods, workloadOf, parsePodSource, parsePodView, filterBySource, groupByCluster, groupTotals } from './podsMerge';
import type { ClusterPodRow, ServicePodRow } from '@/lib/types';

// v0.10.720 — Pods dilimi: entity ∪ Thanos birleştirme saf çekirdeği.
// Sentetik adlar; gerçek kurum verisi yok.
const ent = (p: Partial<ServicePodRow> & { pod: string }): ServicePodRow => ({
  cluster: 'prod-eu', namespace: 'shop', service: 'shop-payment', spans: 10, errors: 1, avgMs: 5,
  firstSeen: '2026-09-13T10:00:00Z', lastSeen: '2026-09-13T10:05:00Z', ...p,
});
const th = (p: Partial<ClusterPodRow> & { pod: string }): ClusterPodRow => ({
  cluster: 'prod-eu', namespace: 'shop', cpuCores: 0.5, memBytes: 100, ...p,
});
const nameOf = (cid: string) => (cid === 'c-eu' ? 'prod-eu' : cid);

describe('mergePods', () => {
  it('aynı cluster adı + pod → tek satır, alanlar birleşir (both)', () => {
    const out = mergePods(
      [ent({ pod: 'a', clusterId: 'c-eu', cluster: 'eu-raw', statusKnown: false, entity: { parentId: 'wl:c-eu/shop/deployment/shop-payment' } as ServicePodRow['entity'] })],
      [th({ pod: 'a', phase: 'Running', restarts: 3, netInBps: 7 })],
      nameOf,
    );
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ source: 'both', cluster: 'prod-eu', phase: 'Running', statusKnown: true, restarts: 3, restartsUnknown: false, cpuCores: 0.5, netInBps: 7, spans: 10 });
    expect(out[0].workload).toEqual({ id: 'wl:c-eu/shop/deployment/shop-payment', clusterId: 'c-eu', namespace: 'shop', kind: 'deployment', name: 'shop-payment' });
  });
  it('yalnız entity: durum bilinmiyor → faz/restart/CPU undefined (0 değil); yalnız Thanos: spans undefined', () => {
    const out = mergePods([ent({ pod: 'e', statusKnown: false, restarts: 0, cpuCores: 0 })], [th({ pod: 't', phase: 'Pending' })], nameOf);
    const e = out.find(r => r.pod === 'e')!; const t = out.find(r => r.pod === 't')!;
    expect(e).toMatchObject({ source: 'entity', statusKnown: false, restartsUnknown: true });
    expect(e.phase).toBeUndefined(); expect(e.cpuCores).toBeUndefined();
    expect(t).toMatchObject({ source: 'thanos', phase: 'Pending', statusKnown: true });
    expect(t.spans).toBeUndefined();
  });
  it('eşlenmemiş cluster (clusterId yok) ham cluster adıyla kalır, Thanos ile birleşmez', () => {
    const out = mergePods([ent({ pod: 'a', cluster: 'unmapped-x' })], [th({ pod: 'a' })], nameOf);
    expect(out.map(r => `${r.cluster}|${r.source}`).sort()).toEqual(['prod-eu|thanos', 'unmapped-x|entity']);
  });
});

describe('workloadOf / parsers / filter', () => {
  it('ns: öneki workload değil', () => {
    expect(workloadOf('ns:c/shop')).toBeNull();
    expect(workloadOf(undefined)).toBeNull();
  });
  it('psrc/pview codec varsayılanları: hepsi, cluster', () => {
    expect(parsePodSource(null)).toBe('all'); expect(parsePodSource('thanos')).toBe('thanos'); expect(parsePodSource('x')).toBe('all');
    expect(parsePodView(null)).toBe('cluster'); expect(parsePodView('flat')).toBe('flat');
  });
  it('kaynak süzgeci: both her ikisinde de görünür', () => {
    const rows = mergePods([ent({ pod: 'a', clusterId: 'c-eu' }), ent({ pod: 'e' })], [th({ pod: 'a' }), th({ pod: 't' })], nameOf);
    expect(filterBySource(rows, 'entity').map(r => r.pod).sort()).toEqual(['a', 'e']);
    expect(filterBySource(rows, 'thanos').map(r => r.pod).sort()).toEqual(['a', 't']);
    expect(filterBySource(rows, 'all')).toHaveLength(3);
  });
});

describe('groupByCluster / groupTotals', () => {
  it('grup sırası ilk görülme; ara toplamlar dürüst (restart kısmi, err% yalnız span olan satırlardan)', () => {
    const rows = mergePods(
      [ent({ pod: 'a', clusterId: 'c-eu', spans: 100, errors: 5 }), ent({ pod: 'u1', cluster: 'prod-us', spans: 50, errors: 0 })],
      [th({ pod: 'a', phase: 'Running', restarts: 2 }), th({ pod: 'b', phase: 'Failed', restartsUnknown: true }), th({ pod: 'u1', cluster: 'prod-us', phase: 'Running' })],
      nameOf,
    );
    // prod-us satırı entity'de ham cluster adı ile; Thanos u1 aynı adı taşır → both
    const groups = groupByCluster(rows);
    expect(groups.map(g => g.cluster)).toEqual(['prod-eu', 'prod-us']);
    const eu = groups[0].totals;
    expect(eu).toMatchObject({ pods: 2, phaseKnown: true, running: 1, failing: 1, restarts: 2, restartsPartial: true, spans: 100, errPct: 5 });
    expect(groupTotals([]).errPct).toBeNull();
  });
});
