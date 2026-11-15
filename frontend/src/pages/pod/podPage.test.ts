// podPage.test.ts — v0.10.160 (pod detay sayfası, A anatomisi). Saf çekirdekler:
//   - ?spans= kodeği: errors (varsayılan, URL'de yazılmaz) | slow | all
//     (çağrı = Σ rate·Δt, hata % = ağırlıklı, ort. ms = rate-ağırlıklı)
//   - podTraceParams: /api/traces parametreleri (k8s.pod.name süzgeci,
//     hatalı/yavaş/tümü, sayfa 50, count=skip)
//   - joinSiblings: entity kardeşleri × clusterPods (topk 500) — eşleşmeyen
//     satırda faz/restart/cpu/mem BİLİNMİYOR ('—'), 0 değil
import { describe, it, expect } from 'vitest';
import { parseSpansParam, writeSpansParam, podTraceParams, joinSiblings, windowP95 } from './podPage';
import type { EntityRecord, ClusterPodRow } from '@/lib/types';

const pt = (t: number, v: number) => ({ time: t * 1e9, value: v });

describe('spans param', () => {
  it('varsayılan errors; bilinmeyen değer errors', () => {
    expect(parseSpansParam(null)).toBe('errors');
    expect(parseSpansParam('slow')).toBe('slow');
    expect(parseSpansParam('all')).toBe('all');
    expect(parseSpansParam('garbage')).toBe('errors');
  });
  it('varsayılan URL\'de YAZILMAZ, diğerleri yazılır; yabancı param korunur', () => {
    const p = new URLSearchParams('pod=x&range=1h');
    writeSpansParam(p, 'errors');
    expect(p.get('spans')).toBeNull();
    writeSpansParam(p, 'slow');
    expect(p.get('spans')).toBe('slow');
    expect(p.get('pod')).toBe('x');
    writeSpansParam(p, 'errors');
    expect(p.has('spans')).toBe(false);
  });
});

describe('podTraceParams', () => {
  const base = { pod: 'p-1', from: 1, to: 2, cluster: 'prod-eu-west', service: 'svc' };
  it('hatalı: filters k8s.pod.name + hasError, sayfa 50, count=skip', () => {
    const p = podTraceParams({ ...base, mode: 'errors', p95Ms: null }, 0)!;
    expect(JSON.parse(p.filters!)).toEqual([{ k: 'k8s.pod.name', op: '=', v: ['p-1'] }]);
    expect(p.hasError).toBe(true);
    expect(p.minMs).toBeUndefined();
    expect(p).toMatchObject({ limit: 50, offset: 0, count: 'skip', service: 'svc', cluster: 'prod-eu-west', from: 1, to: 2 });
  });
  it('yavaş: minMs = p95 (yuvarlanmış); p95 yoksa null döner (çip kapalı)', () => {
    const p = podTraceParams({ ...base, mode: 'slow', p95Ms: 812.4 }, 50);
    expect(p!.minMs).toBe(812);
    expect(p!.hasError).toBeUndefined();
    expect(p!.offset).toBe(50);
    expect(podTraceParams({ ...base, mode: 'slow', p95Ms: null }, 0)).toBeNull();
  });
  it('tümü: ne hasError ne minMs; servis/cluster boşsa anahtar yok', () => {
    const p = podTraceParams({ pod: 'p-1', from: 1, to: 2, cluster: '', service: '', mode: 'all', p95Ms: null }, 0);
    expect(p!.hasError).toBeUndefined();
    expect(p!.minMs).toBeUndefined();
    expect('service' in p!).toBe(false);
    expect('cluster' in p!).toBe(false);
  });
});

describe('joinSiblings', () => {
  const rec = (name: string, ns = 'payments'): EntityRecord => ({
    type: 'pod', clusterId: 'c1', id: `pod:c1/${ns}/${name}`, namespace: ns, name, source: 'thanos',
    validFrom: '2026-08-27T09:00:00Z', firstSeen: '2026-08-27T09:00:00Z', lastSeen: '2026-08-29T17:00:00Z',
  });
  const row = (pod: string, extra: Partial<ClusterPodRow> = {}): ClusterPodRow => ({ cluster: 'prod', namespace: 'payments', pod, cpuCores: 0.5, memBytes: 1e9, phase: 'Running', restarts: 0, ...extra });
  it('namespace+ad ile birleşir; listede olmayan pod BİLİNMİYOR', () => {
    const out = joinSiblings([rec('a'), rec('b')], [row('a', { restarts: 3 })]);
    expect(out[0]).toMatchObject({ name: 'a', known: true, restarts: 3, phase: 'Running' });
    expect(out[1]).toMatchObject({ name: 'b', known: false });
    expect(out[1].restarts).toBeNull();
    expect(out[1].cpuCores).toBeNull();
  });
  it('aynı ad farklı namespace eşleşmez; restartsUnknown → null', () => {
    const out = joinSiblings([rec('a', 'other')], [row('a', { restartsUnknown: true })]);
    expect(out[0].known).toBe(false);
    const out2 = joinSiblings([rec('a')], [row('a', { restartsUnknown: true })]);
    expect(out2[0].known).toBe(true);
    expect(out2[0].restarts).toBeNull();
  });
  it('null liste → hepsi bilinmiyor', () => {
    expect(joinSiblings([rec('a')], null)[0].known).toBe(false);
  });
});
