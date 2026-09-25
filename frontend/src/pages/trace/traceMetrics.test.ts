import { describe, it, expect } from 'vitest';
import type { SpanRow } from '@/lib/types';
import { tracePods, podsByService, defaultPodSelection, togglePod, traceMetricsWindow, resolveCluster, shortPod, TRACE_METRICS_MAX_PODS, jvmPodSeries } from './traceMetrics';

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

// v0.10.916 — operator-reported: bellek üstte, iki panel de y tabanı 0.
describe('TraceMetricsPanel düzeni', () => {
  it('Memory CPU\'dan önce; ikisi de zeroBase', async () => {
    const { readFileSync } = await import('node:fs');
    const { resolve } = await import('node:path');
    const src = readFileSync(resolve(__dirname, 'TraceMetricsPanel.tsx'), 'utf8');
    expect(src.indexOf('Memory (bytes)')).toBeLessThan(src.indexOf('CPU (cores)'));
    expect(src.match(/<MultiLineChart[^>]*zeroBase \/>/g)?.length).toBe(2);
  });
});

// v0.10.923 — JVM paneli seri hazırlığı (saf) + panel bağlantısı (kaynak pini).
describe('jvmPodSeries', () => {
  const s = (pod: string, vals: number[]) => ({ groupKey: [pod], points: vals.map((v, i) => ({ time: i * 1e9, value: v })) });
  it('boş seriler düşer, etiket kısa pod, tam ad fullKey', () => {
    const out = jvmPodSeries([s('checkout-api-7c8977f965-7hrqz', [1, 2]), s('checkout-api-7c8977f965-empty', [])]);
    expect(out).toHaveLength(1);
    expect(out[0].groupKey).toEqual([shortPod('checkout-api-7c8977f965-7hrqz')]);
    expect(out[0].fullKey).toEqual(['checkout-api-7c8977f965-7hrqz']);
  });
  it('ölçek uygulanır (GC saniye → ms), 1 iken noktalar aynen', () => {
    expect(jvmPodSeries([s('p-a-b', [0.012])], 1000)[0].points[0].value).toBeCloseTo(12);
    const pts = [{ time: 0, value: 5 }];
    expect(jvmPodSeries([{ groupKey: ['x-y-z'], points: pts }])[0].points).toBe(pts);
  });
  it('null/undefined güvenli', () => {
    expect(jvmPodSeries(null)).toEqual([]);
    expect(jvmPodSeries(undefined)).toEqual([]);
  });
  it('panel Trace Metrics\'e CPU\'nun altında bağlı; GC sonrası heap önce, anlık kullanım yedek', async () => {
    const { readFileSync } = await import('node:fs');
    const { resolve } = await import('node:path');
    const panel = readFileSync(resolve(__dirname, 'TraceMetricsPanel.tsx'), 'utf8');
    expect(panel.indexOf('CPU (cores)')).toBeLessThan(panel.indexOf('<TraceJvmPanel'));
    const jvm = readFileSync(resolve(__dirname, 'TraceJvmPanel.tsx'), 'utf8');
    expect(jvm.indexOf("'jvm.memory.used_after_last_gc'")).toBeLessThan(jvm.indexOf("'jvm.memory.used'"));
    expect(jvm).toContain("'jvm.gc.duration'");
    expect(jvm).toMatch(/familyOf\(runtimeQ\.data\?\.language\) === 'jvm'/);
  });
});
