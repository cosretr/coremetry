import { describe, it, expect } from 'vitest';
import { podLineLabel, podClusterOf, podEnvOf, podScopeLabel, POD_SCOPE_KEYS, GEN_KEY_IDX, isAnonPodSeries, divideByCount } from './runtimePodLabel';

describe('anonim pod serisi yedeği (follow-up 2026-11-21)', () => {
  it('host_name taşıyan seri anonim değil; üç anahtar boşsa anonim', () => {
    expect(isAnonPodSeries([{ groupKey: ['svc-a-7f9c-1', '', '', ''] }])).toBe(false);
    expect(isAnonPodSeries([{ groupKey: ['', 'svc-a-7f9c-1', '', ''] }])).toBe(false);
    expect(isAnonPodSeries([{ groupKey: ['', '', '', 'cl-a'] }])).toBe(true);
    expect(isAnonPodSeries([])).toBe(false);
  });
  it('oran toplamı aynı damgadaki seri sayısına bölünür', () => {
    const out = divideByCount([{ time: 1, value: 0.5 }, { time: 2, value: 0.3 }, { time: 3, value: null }],
      [{ time: 1, value: 10 }, { time: 2, value: 1 }]);
    expect(out.map(p => p.value)).toEqual([0.05, 0.3, null]);
  });
});

// v0.9.91 — pod-bazlı runtime çizgi etiketi kontratı (üçlü groupKey:
// k8s.pod.name > host.name > service.instance.id).
// v0.10.1640 — Operator-reported: "JVM heap/non-heap (by pod)" lejand ve
// tooltip'i pod adının yalnız KUYRUĞUNU gösteriyordu ("6b49d8f679-69tf7");
// podLineLabel servis-adı önekini bilerek kırpıyordu (v0.9.91). Tam pod
// adı basılır; dar lejand ellipsis + title ile kısaltır, ad uydurulmaz.
describe('podLineLabel', () => {
  const cases: Array<[string, string[], string]> = [
    ['k8s.pod.name tam ad (servis öneki KIRPILMAZ)', ['orders-api-6b49d8f679-69tf7', '', 'uuid'], 'orders-api-6b49d8f679-69tf7'],
    ['host.name (=pod adı) tam ad', ['', 'orders-api-778cccb9c-x8zp2', 'abcdef1234'], 'orders-api-778cccb9c-x8zp2'],
    ['k8s.pod.name host.name\'e yeğlenir', ['orders-api-6b49d8f679-69tf7', 'other-host', ''], 'orders-api-6b49d8f679-69tf7'],
    ['öneki eşleşmeyen pod adı olduğu gibi', ['other-pod-1', '', ''], 'other-pod-1'],
    ['boşluk kırpılır', ['  orders-api-6b49d8f679-69tf7 ', '', ''], 'orders-api-6b49d8f679-69tf7'],
    ['pod adı yok, yalnız instance id → ilk 8', ['', '', 'abcdef1234567890'], 'abcdef12'],
    ['üçü de boş → fallback', ['', '', ''], 'used'],
    ['boş groupKey → fallback', [], 'used'],
  ];
  it.each(cases)('%s', (_name, gk, want) => {
    expect(podLineLabel(gk, 'used')).toBe(want);
  });
});

// v0.10.2464 (operatör: pod çizgisinde cluster / ortam) — etiket
// "pod · cluster · ortam"; cluster [3] (CH coalesce) yoksa serinin kendi
// openshift [5] / k8s [6] etiketi; ortam [4]. İkisi de yoksa yalnız pod.
describe('podScopeLabel / podClusterOf / podEnvOf', () => {
  const POD = 'apigateway-mobile-fc7c5bf68-f4rrz';
  it('VM serisi: openshift_cluster_name → pod · cluster', () => {
    expect(podScopeLabel(POD, [POD, '', '', '', '', 'ocpkb', ''])).toBe(`${POD} · ocpkb`);
  });
  it('cluster + ortam → ikisi de; ortam cluster\'la aynıysa bir kez', () => {
    expect(podScopeLabel(POD, [POD, '', '', '', 'prod', '', 'ocpkb'])).toBe(`${POD} · ocpkb · prod`);
    expect(podScopeLabel(POD, [POD, '', '', 'ocpkb', 'ocpkb'])).toBe(`${POD} · ocpkb`);
  });
  it('yalnız ortam → pod · ortam', () => {
    expect(podScopeLabel(POD, [POD, '', '', '', 'uat'])).toBe(`${POD} · uat`);
  });
  it('ikisi de yok → yalnız pod', () => {
    expect(podScopeLabel(POD, [POD, '', '', ''])).toBe(POD);
    expect(podScopeLabel(POD, [POD])).toBe(POD);
  });
  it('podClusterOf: [3] önce, sonra [5], [6]; kırpar', () => {
    expect(podClusterOf(['p', 'h', 'i', ' ocp-eu ', '', 'x'])).toBe('ocp-eu');
    expect(podClusterOf(['p', 'h', 'i', '', '', '', ' k8s-a '])).toBe('k8s-a');
    expect(podClusterOf([])).toBe('');
    expect(podEnvOf(['p', 'h', 'i', 'c', ' prod '])).toBe('prod');
  });
  it('kapsam anahtarları 3 tane; nesil indeksi POD_GROUP 4 + 3', () => {
    expect(POD_SCOPE_KEYS.split(',')).toEqual(['deployment.environment.name', 'resource.openshift.cluster.name', 'resource.k8s.cluster.name']);
    expect(GEN_KEY_IDX).toBe(7);
  });
  it('4. anahtar eklenmesi podLineLabel sözleşmesini BOZMAZ', () => {
    // groupKey[0..2] indeksleri v0.9.91 kontratı; [3] cluster.
    expect(podLineLabel(['orders-api-6f9c7b-x2v', '', 'uuid', 'ocp-eu'], 'used'))
      .toBe('orders-api-6f9c7b-x2v');
  });
});
