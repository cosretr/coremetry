import { describe, it, expect } from 'vitest';
import { podLineLabel, podClusterOf, withClusterPrefix } from './runtimePodLabel';

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

// v0.9.532 — cluster öneki sözleşmesi (operatör: "jvm var ama hangi
// cluster belli değil"). Önek YALNIZ çok-cluster'lı seri kümesinde ve
// yalnız cluster'ı bilinen seride basılır — tek cluster'da her etikete
// "ocp-eu · " eklemek gürültü, cluster'sız seriye önek basmak yalan.
describe('withClusterPrefix / podClusterOf', () => {
  it('çok cluster + cluster bilinen → önek', () => {
    expect(withClusterPrefix('6f9c7b-x2v', 'ocp-eu', true)).toBe('ocp-eu · 6f9c7b-x2v');
  });
  it('tek cluster → önek YOK (gürültü)', () => {
    expect(withClusterPrefix('6f9c7b-x2v', 'ocp-eu', false)).toBe('6f9c7b-x2v');
  });
  it('cluster bilinmiyor → önek YOK (attr basmayan ~%1-2 pod)', () => {
    expect(withClusterPrefix('6f9c7b-x2v', '', true)).toBe('6f9c7b-x2v');
  });
  it('podClusterOf groupKey[3] okur, kırpar, yoksa boş', () => {
    expect(podClusterOf(['p', 'h', 'i', ' ocp-eu '])).toBe('ocp-eu');
    expect(podClusterOf(['p', 'h', 'i'])).toBe('');
    expect(podClusterOf([])).toBe('');
  });
  it('4. anahtar eklenmesi podLineLabel sözleşmesini BOZMAZ', () => {
    // groupKey[0..2] indeksleri v0.9.91 kontratı; [3] cluster.
    expect(podLineLabel(['orders-api-6f9c7b-x2v', '', 'uuid', 'ocp-eu'], 'used'))
      .toBe('orders-api-6f9c7b-x2v');
  });
});
