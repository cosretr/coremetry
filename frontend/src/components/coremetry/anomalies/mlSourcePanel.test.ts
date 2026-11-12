// v0.10.2033 — detay grafiği servis sayfasının paneliyle; olay penceresini
// çevreler; suçlu seri adıyla. Fikstür nötr.
import { describe, it, expect } from 'vitest';
import type { SpanMetricSeries } from '@/lib/types';
import { contributorLine, leadFromSnapshot, panelWindow, sourcePanelFor, topContributors } from './mlSourcePanel';

describe('contributorLine / leadFromSnapshot', () => {
  it('tek suçlu adıyla, diğerleri tek ifadeyle; ad kırpılmaz', () => {
    expect(contributorLine({ kind: 'single', name: 'orders-collector-consumer-v1', value: 2597, others: 11, othersZero: true }, 'istemci'))
      .toBe('Tepe anında neredeyse tamamı tek istemci: orders-collector-consumer-v1 (2.597); diğer 11 istemci 0.');
    expect(contributorLine({ kind: 'spread', count: 4 }, 'istemci')).toBe('Tepe anında 4 istemciye yayılmış; tek bir öne çıkan yok.');
    expect(contributorLine({ kind: 'none' }, 'istemci')).toBe('');
  });
  it('kayıttan panel girdisi: trace parçasında ölçü atılır', () => {
    expect(leadFromSnapshot({ detector: 'op_latency', signal: 'trace', service: 's', part: 'POST /x|slow', detectedAt: 1, lastSeen: 2 }).code).toBe('POST /x');
    expect(leadFromSnapshot({ detector: 'metric_kafka', signal: 'metric', service: 's', part: 'metric_kafka|consumer_lag', detectedAt: 1, lastSeen: 2 }).code).toBe('metric_kafka|consumer_lag');
  });
});

const ev = (detector: string, signal: string, code = '') => ({ detector, signal, code, service: 'orders-consumer' });

describe('sourcePanelFor', () => {
  it.each<[string, ReturnType<typeof ev>, string]>([
    ['Kafka lag → Messaging paneli', ev('metric_kafka', 'metric', 'metric_kafka|consumer_lag'), 'kafka-lag'],
    ['Kafka lag Thanos cluster olayı → o cluster\'ın serisi', ev('metric_kafka', 'metric', 'metric_kafka|consumer_lag@prod-east'), 'metric'],
    ['Kafka throttle → dedektörün serisi (VM)', ev('metric_kafka', 'metric', 'metric_kafka|fetch_throttle'), 'metric'],
    ['gecikme → Traces şeridi', ev('op_latency', 'trace', 'POST /orders'), 'traces'],
    ['hata sayısı → Traces şeridi', ev('trace_error_count', 'trace', 'POST /orders'), 'traces'],
    ['bağımlılık → hedefin p95 serisi', ev('dependency_latency', 'trace', 'db|orders'), 'metric'],
    ['trace pod sapması → kayıtlı', ev('trace_pod_outlier', 'trace', 'p95'), 'snapshot'],
    ['metrik olayı seri kimliksiz → kayıtlı', ev('metric_jvm', 'metric', ''), 'snapshot'],
    ['log, Logs bağlantısı yok → kayıtlı', ev('error_rate', 'log'), 'snapshot'],
    ['JVM → dedektörün serisi (VM)', ev('metric_jvm', 'metric', 'metric_jvm|heap_used'), 'metric'],
    ['k8s Thanos → pod başına', ev('metric_k8s', 'metric', 'metric_k8s|memory@prod-east'), 'metric'],
  ])('%s', (_n, e, want) => {
    expect(sourcePanelFor(e).kind).toBe(want);
  });
  it('v0.10.2036: Thanos olayında cluster adı ayrılır, VM\'de boş', () => {
    expect(sourcePanelFor(ev('metric_k8s', 'metric', 'metric_k8s|memory@prod-east'))).toMatchObject({ kind: 'metric', cluster: 'prod-east', code: 'metric_k8s|memory@prod-east' });
    expect(sourcePanelFor(ev('metric_jvm', 'metric', 'metric_jvm|heap_used'))).toMatchObject({ kind: 'metric', cluster: '' });
  });
  it('gecikme olayı p99 + minMs eşiği; hata olayı hata çubukları önde; route ayrımı', () => {
    const lat = sourcePanelFor(ev('op_latency', 'trace', '/orders/{id}'), 1);
    expect(lat).toMatchObject({ kind: 'traces', stat: 'p99', minMs: 1000, byRoute: true, errorsFirst: false });
    expect(sourcePanelFor(ev('trace_error_count', 'trace', 'POST /orders'))).toMatchObject({ errorsFirst: true, byRoute: false });
  });
});

// v0.10.2035 — log olayı: Logs histogramı, süzgeç kartın Logs bağlantısından (aynı nüfus).
describe('log olayı → Logs histogramı', () => {
  it.each<[string, string | undefined, unknown]>([
    ['yeni mesaj: servis + tümcecik', '/logs?service=orders-batch&q=%22payment+failed%22&range=1h',
      { kind: 'logs', service: 'orders-batch', search: '"payment failed"', severity: 0 }],
    ['hata oranı: seviye 17', '/logs?service=orders-batch&severity=17', { kind: 'logs', search: '', severity: 17 }],
    ['hacim: yalnız servis', '/logs?service=orders-batch', { kind: 'logs', search: '', severity: 0 }],
    ['servissiz bağlantı → kayıtlı', '/logs?q=x', { kind: 'snapshot' }],
    ['Logs dışı bağlantı → kayıtlı', '/traces?service=orders-batch', { kind: 'snapshot' }],
    ['bağlantı yok → kayıtlı', undefined, { kind: 'snapshot' }],
  ])('%s', (_n, href, want) => {
    expect(sourcePanelFor(ev('error_rate', 'log'), 1, href)).toMatchObject(want as object);
  });
});

describe('panelWindow', () => {
  const m = 60e9;
  it('başlangıçtan 30 dk önce → bitişten 15 dk sonra', () => {
    expect(panelWindow(100 * m, 120 * m, false, 500 * m)).toEqual({ fromNs: 70 * m, toNs: 135 * m });
  });
  it('sürüyorsa şimdiye kadar; gelecek kesilir', () => {
    expect(panelWindow(100 * m, 120 * m, true, 130 * m)).toEqual({ fromNs: 70 * m, toNs: 130 * m });
    expect(panelWindow(100 * m, 120 * m, false, 125 * m)).toEqual({ fromNs: 70 * m, toNs: 125 * m });
  });
});

const ser = (name: string, vals: number[]): SpanMetricSeries => ({ groupKey: [name], points: vals.map((v, i) => ({ time: i * 60e9, value: v })) });

describe('topContributors', () => {
  it('en büyük ≥ %60 → tek ad, diğerleri 0', () => {
    const c = topContributors([ser('orders-collector-consumer-v1', [0, 2597, 0]), ...Array.from({ length: 11 }, (_, i) => ser(`client-${i}`, [0, 0, 0]))]);
    expect(c).toEqual({ kind: 'single', name: 'orders-collector-consumer-v1', value: 2597, others: 11, othersZero: true });
  });
  it('baskın yok → ilk 3', () => {
    const c = topContributors([ser('a', [0, 50]), ser('b', [0, 30]), ser('c', [0, 15]), ser('d', [0, 5])]);
    expect(c).toMatchObject({ kind: 'top', items: [{ name: 'a', value: 50 }, { name: 'b', value: 30 }, { name: 'c', value: 15 }] });
  });
  it('hepsi benzer → ad yok', () => {
    expect(topContributors([ser('a', [10]), ser('b', [11]), ser('c', [9]), ser('d', [10])])).toEqual({ kind: 'spread', count: 4 });
  });
  it('veri yok / sıfır → none', () => {
    expect(topContributors([])).toEqual({ kind: 'none' });
    expect(topContributors([ser('a', [0, 0])])).toEqual({ kind: 'none' });
  });
});
