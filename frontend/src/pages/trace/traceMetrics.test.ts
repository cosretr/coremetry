import { describe, it, expect } from 'vitest';
import type { SpanRow } from '@/lib/types';
import { tracePods, podsByService, defaultPodSelection, togglePod, traceMetricsWindow, resolveCluster, shortPod, TRACE_METRICS_MAX_PODS } from './traceMetrics';

// v0.10.913 — Trace "Metrics" sekmesi: pod çıkarma, servise göre gruplama,
// varsayılan seçim (hata veren en derin span'ın servisi), aynı-servis kuralı
// (≤4 pod üst üste), pencere, cluster eşlemesi.
const span = (id: string, parent: string, svc: string, pod: string, err = false, start = 1_000, extra: Record<string, string> = {}): SpanRow => ({
  spanId: id, parentSpanId: parent, serviceName: svc, startTime: start, durationMs: 10,
  statusCode: err ? 'error' : 'ok', statusMessage: '', attributes: {},
  resourceAttributes: { ...(pod ? { 'k8s.pod.name': pod, 'k8s.namespace.name': 'ns1', 'k8s.cluster.name': 'prod-ist' } : {}), ...extra },
} as unknown as SpanRow);

const spans = [
  span('a', '', 'gateway', 'gw-6d9f7c8b5-p2m7t'),
  span('b', 'a', 'login', 'login-7b9949bb74-l4bg5', true),
  span('c', 'b', 'login', 'login-5459c84bd9-qnfgc'),
  span('d', 'c', 'login', 'login-7b9949bb74-txsg9', true),   // en derin hatalı span
  span('e', 'a', 'customer', 'cust-5f4c9d8b7-x8k2q'),
  span('f', 'a', 'nopod', ''),
];

describe('traceMetrics', () => {
  it('pod çıkarma + servis gruplama (hatalı önce)', () => {
    const pods = tracePods(spans);
    expect(pods).toHaveLength(5);
    const g = podsByService(pods);
    expect(g.map(x => x.service)).toEqual(['customer', 'gateway', 'login']);
    expect(g[2].pods[0].errors).toBe(1);
    expect(pods.find(p => p.pod === 'login-7b9949bb74-l4bg5')).toMatchObject({ namespace: 'ns1', clusterValue: 'prod-ist', spans: 1, errors: 1 });
  });
  it('varsayılan: en derin hatalı span pod\'u önce + servisin diğer hatalı pod\'ları', () => {
    expect(defaultPodSelection(spans, tracePods(spans))).toEqual(['login-7b9949bb74-txsg9', 'login-7b9949bb74-l4bg5']);
    const ok = spans.map(s => ({ ...s, statusCode: 'ok' }));
    expect(defaultPodSelection(ok, tracePods(ok))).toEqual(['gw-6d9f7c8b5-p2m7t']);
  });
  it('aynı servis kuralı: ekle / çıkar / başka servis değiştirir / tavan', () => {
    const pods = tracePods(spans);
    let sel = ['login-7b9949bb74-l4bg5'];
    sel = togglePod(sel, 'login-5459c84bd9-qnfgc', pods);
    expect(sel).toHaveLength(2);
    expect(togglePod(sel, 'cust-5f4c9d8b7-x8k2q', pods)).toEqual(['cust-5f4c9d8b7-x8k2q']);
    expect(togglePod(sel, 'login-5459c84bd9-qnfgc', pods)).toEqual(['login-7b9949bb74-l4bg5']);
    expect(togglePod(['login-7b9949bb74-l4bg5'], 'login-7b9949bb74-l4bg5', pods)).toEqual(['login-7b9949bb74-l4bg5']);
    const many = Array.from({ length: 6 }, (_, i) => span(`x${i}`, '', 'svc', `svc-aaaaa-p${i}xxx`));
    const mp = tracePods(many);
    let s: string[] = [mp[0].pod];
    for (const p of mp.slice(1)) s = togglePod(s, p.pod, mp);
    expect(s).toHaveLength(TRACE_METRICS_MAX_PODS);
  });
  it('pencere ± dk, cluster eşlemesi, kısa ad', () => {
    const w = traceMetricsWindow([span('a', '', 's', 'p', false, 10e9)], 15);
    expect(w.from).toBe(10e9 - 15 * 60e9);
    expect(w.to).toBe(10e9 + 10e6 + 15 * 60e9);
    expect(resolveCluster('prod-ist', [{ id: 'c1', name: 'ist', spanClusterValue: 'x', spanClusterValues: ['prod-ist'] }])).toBe('ist');
    expect(resolveCluster('yok', [])).toBe('');
    expect(shortPod('bsa-mobile-login-prod-7b9949bb74-l4bg5')).toBe('…7b9949bb74-l4bg5');
  });
});
