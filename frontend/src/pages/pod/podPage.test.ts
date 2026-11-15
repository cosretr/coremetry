// podPage.test.ts — v0.10.160 (pod detay sayfası, A anatomisi). Saf çekirdekler:
//   - ?spans= kodeği: errors (varsayılan, URL'de yazılmaz) | all (v0.10.2195:
//     «Yavaş» RED bölümüyle kalktı); hatalı yoksa varsayılan «Tümü»
//   - podTraceParams: /api/traces parametreleri (k8s.pod.name süzgeci yalnız
//     pod kipinde, hatalı/tümü, sayfa 50, count=skip)
//   - traceScopeCtx: entity kapsamı → süzgeç (pod / servis geneli)
//   - joinSiblings: entity kardeşleri × clusterPods (topk 500) — eşleşmeyen
//     satırda faz/restart/cpu/mem BİLİNMİYOR ('—'), 0 değil
import { describe, it, expect } from 'vitest';
import { parseSpansParam, writeSpansParam, podTraceParams, joinSiblings, effectiveSpansMode, traceScopeCtx } from './podPage';
import type { EntityRecord, ClusterPodRow } from '@/lib/types';

describe('spans param', () => {
  it('varsayılan errors; bilinmeyen / eski slow değeri errors', () => {
    expect(parseSpansParam(null)).toBe('errors');
    expect(parseSpansParam('slow')).toBe('errors');
    expect(parseSpansParam('all')).toBe('all');
    expect(parseSpansParam('garbage')).toBe('errors');
  });
  it('varsayılan URL\'de YAZILMAZ, all yazılır; yabancı param korunur', () => {
    const p = new URLSearchParams('pod=x&range=1h');
    writeSpansParam(p, 'errors');
    expect(p.get('spans')).toBeNull();
    writeSpansParam(p, 'all');
    expect(p.get('spans')).toBe('all');
    expect(p.get('pod')).toBe('x');
    writeSpansParam(p, 'errors');
    expect(p.has('spans')).toBe(false);
  });
  it('çip seçilmemiş ve pencerede hatalı trace yok → Tümü', () => {
    expect(effectiveSpansMode(null, 0)).toBe('all');
    expect(effectiveSpansMode(null, 3)).toBe('errors');
    expect(effectiveSpansMode(null, null)).toBe('errors');
    expect(effectiveSpansMode('all', 3)).toBe('all');
  });
});

describe('podTraceParams', () => {
  const base = { pod: 'p-1', from: 1, to: 2, cluster: 'prod-eu-west', service: 'svc', podExact: true };
  it('hatalı: filters k8s.pod.name + hasError, sayfa 50, count=skip', () => {
    const p = podTraceParams({ ...base, mode: 'errors' }, 0);
    expect(JSON.parse(p.filters!)).toEqual([{ k: 'k8s.pod.name', op: '=', v: ['p-1'] }]);
    expect(p.hasError).toBe(true);
    expect(p).toMatchObject({ limit: 50, offset: 0, count: 'skip', service: 'svc', cluster: 'prod-eu-west', from: 1, to: 2 });
  });
  it('tümü: hasError yok; servis/cluster boşsa anahtar yok', () => {
    const p = podTraceParams({ ...base, cluster: '', service: '', mode: 'all' }, 50);
    expect(p.hasError).toBeUndefined();
    expect(p.offset).toBe(50);
    expect('service' in p).toBe(false);
    expect('cluster' in p).toBe(false);
  });
  it('servis geneli: pod süzgeci yok, servis + cluster var', () => {
    const p = podTraceParams({ ...base, podExact: false, mode: 'all' }, 0);
    expect(p.filters).toBeUndefined();
    expect(p).toMatchObject({ service: 'svc', cluster: 'prod-eu-west' });
  });
});

describe('traceScopeCtx (v0.10.2195, entity kapsamı)', () => {
  it('pod kenarı: span\'in gerçek cluster değeri, URL servisi daraltıcı', () => {
    expect(traceScopeCtx({ mode: 'pod', clusterValue: 'KCLA' }, ['kcla'], '')).toEqual({ cluster: 'KCLA', service: '', podExact: true });
  });
  it('pod span\'i yok → iş yükünün servisi, servis geneli', () => {
    expect(traceScopeCtx({ mode: 'service', clusterValue: 'kcla', service: 'cards' }, ['kcla'], ''))
      .toEqual({ cluster: 'kcla', service: 'cards', podExact: false });
  });
  it('kapsam yok / none → kaydın tek değeri; çok değerde süzgeç yok', () => {
    expect(traceScopeCtx(undefined, ['kcla'], 'svc')).toEqual({ cluster: 'kcla', service: 'svc', podExact: true });
    expect(traceScopeCtx({ mode: 'none' }, ['a', 'b'], '')).toEqual({ cluster: '', service: '', podExact: true });
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
