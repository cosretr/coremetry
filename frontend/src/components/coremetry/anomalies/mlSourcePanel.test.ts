// v0.10.2033 — detay grafiği servis sayfasının paneliyle; olay penceresini
// çevreler; suçlu seri adıyla. Fikstür nötr.
import { describe, it, expect } from 'vitest';
import type { SpanMetricSeries } from '@/lib/types';
import { contributorLine, highlightColors, liveGapLine, serviceRedAggFor, thanosSourceText, leadFromSnapshot, panelWindow, recordLead, panelPeakLine, peakValueText, sourcePanelFor, topContributors } from './mlSourcePanel';

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
    ['gecikme → dedektörün VM serisi (v0.10.2204)', ev('op_latency', 'trace', 'POST /orders'), 'metric'],
    ['hata sayısı → Traces şeridi', ev('trace_error_count', 'trace', 'POST /orders'), 'traces'],
    ['bağımlılık → hedefin p95 serisi', ev('dependency_latency', 'trace', 'db|orders'), 'metric'],
    ['bağımlılık hata oranı → hedefin oran serisi (v0.10.2063)', ev('dependency_error_rate', 'trace', 'db|orders'), 'metric'],
    ['Kafka lag topic olayı → Messaging lag paneli (v0.10.2064)', ev('metric_kafka', 'metric', 'metric_kafka|consumer_lag#invoices'), 'kafka-lag'],
    ['trace pod sapması → pod başına kaynak serisi (v0.10.2055)', ev('trace_pod_outlier', 'trace', 'p95'), 'metric'],
    ['trace pod sapması ölçüsüz → kayıtlı', ev('trace_pod_outlier', 'trace', ''), 'snapshot'],
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
  // v0.10.2055 — trace_pod_outlier eskiden kayıtlı 5 dakikalık görüntüydü.
  it.each<[string, Parameters<typeof sourcePanelFor>[0], object]>([
    ['grup lideri: ölçü Code, pod alanı, aile kanıttan',
      { ...ev('trace_pod_outlier', 'trace', 'err_ratio'), pod: 'orders-consumer-7f9c-a', evidence: '{"family":"http","measure":"err_ratio"}' },
      { kind: 'metric', code: 'err_ratio', highlight: 'orders-consumer-7f9c-a', family: 'http', cluster: '' }],
    ['kayıttan: ölçü + pod kanıttan',
      { ...ev('trace_pod_outlier', 'trace', ''), evidence: '{"family":"grpc","measure":"p95","pod":"orders-consumer-7f9c-b"}' },
      { kind: 'metric', code: 'p95', highlight: 'orders-consumer-7f9c-b', family: 'grpc' }],
    ['bozuk kanıt, Code yok → kayıtlı', { ...ev('trace_pod_outlier', 'trace', ''), evidence: '{' }, { kind: 'snapshot' }],
  ])('trace pod: %s', (_n, e, want) => {
    expect(sourcePanelFor(e)).toMatchObject(want);
  });
  it.each<[string, string | undefined, string | null]>([
    ['behavior_change', 'error_rate', 'error_rate'],
    ['behavior_change', 'p99_ms', 'p99'],
    ['behavior_change', 'request_rate', 'rate'],
    ['behavior_change', 'unknown', null],
    ['calendar_traffic_drop', undefined, 'rate'],
    ['trace_op_latency', undefined, null],
  ])('v0.10.2062: %s/%s → Overview serisi %s', (kind, metric, want) => {
    expect(serviceRedAggFor(kind, metric)?.agg ?? null).toBe(want);
  });
  it('v0.10.2077: servis hata oranı servis Overview hata oranı serisini çizer', () => {
    expect(sourcePanelFor(ev('svc_error_rate', 'trace', ''))).toEqual({ kind: 'service-red', service: 'orders-consumer', agg: 'error_rate', label: 'hata oranı' });
  });
  it('v0.10.2086: yaygın yavaşlama servis Overview p99 serisini çizer', () => {
    expect(sourcePanelFor(ev('svc_breadth', 'trace', ''))).toEqual({ kind: 'service-red', service: 'orders-consumer', agg: 'p99', label: 'p99 yanıt süresi' });
    // v0.10.2385 — servis yanıt süresi (etki olayı I4): aynı Overview p99 serisi.
    expect(sourcePanelFor(ev('svc_response_time', 'trace', ''))).toEqual({ kind: 'service-red', service: 'orders-consumer', agg: 'p99', label: 'p99 yanıt süresi' });
    // v0.10.2128 (S8) — hacim sıçraması anlık görüntü yazmaz: istek hızı serisi, kayıtlı görüntü değil.
    expect(sourcePanelFor(ev('svc_volume_spike', 'trace', ''))).toEqual({ kind: 'service-red', service: 'orders-consumer', agg: 'rate', label: 'istek hızı' });
    expect(sourcePanelFor({ ...ev('svc_volume_spike', 'trace', ''), service: '' }).kind).toBe('snapshot');
  });
  it('v0.10.2061: canlı grafik yoksa nedeni tek satır; metrik olayı serisiz → neden yazılı', () => {
    expect(liveGapLine('olay penceresinde kaynakta seri yok')).toBe('Canlı grafik çizilemedi: olay penceresinde kaynakta seri yok. Aşağıdaki, olay anında kaydedilen 5 dakikalık görüntü.');
    expect(liveGapLine(undefined)).toContain('kaynak okunamadı');
    expect(sourcePanelFor(ev('metric_jvm', 'metric', ''))).toEqual({ kind: 'snapshot', reason: 'olay kaydı ölçülen serinin adını taşımıyor' });
  });
  it('v0.10.2060: Thanos kaynak satırı eşi olan Pods panelini söyler', () => {
    expect(thanosSourceText('pods-resource')).toBe('Servis › Pods sekmesindeki panelin aynı sorgusu');
    expect(thanosSourceText('pods')).toBe('Servis › Pods sekmesinin pod kümesi');
    expect(thanosSourceText(undefined)).toContain('namespace bilinmiyor');
  });
  it('vurgu rengi yalnız sapan seride; vurgu yoksa boş', () => {
    const res = (t: string) => (t === '--err' ? 'red' : 'gray');
    expect([...highlightColors(['a', 'b', 'c'], 'b', res)]).toEqual([['a', 'gray'], ['b', 'red'], ['c', 'gray']]);
    expect(highlightColors(['a', 'b'], 'z', res).size).toBe(0);
    expect(highlightColors(['a', 'b'], undefined, res).size).toBe(0);
  });
  // v0.10.2204 (Örnek 4) — gRPC: Code rpc_method değeri, span adı "…BagService/callBagService";
  // grafik span adında aramaz, dedektörün serisini route değeriyle ister.
  it('endpoint olayı dedektörün serisi: route değeri + ölçü, span adı değil', () => {
    const bag = { detector: 'op_latency', signal: 'trace', code: 'callBagService', service: 'acm-portfolio-summary-prod', evidence: '{"source":"vm","measure":"p95"}' };
    expect(sourcePanelFor(bag, 1)).toEqual({ kind: 'metric', detector: 'op_latency', service: bag.service, code: 'callBagService', cluster: '', measure: 'p95' });
    expect(sourcePanelFor({ ...bag, evidence: 'bozuk' }, 1)).toMatchObject({ kind: 'metric', measure: undefined });
    expect(sourcePanelFor({ ...bag, detector: 'op_error_rate' })).toMatchObject({ kind: 'metric', code: 'callBagService', measure: undefined });
    expect(sourcePanelFor({ ...bag, code: '' })).toMatchObject({ kind: 'snapshot' });
    expect(sourcePanelFor(ev('trace_error_count', 'trace', 'POST /orders'))).toMatchObject({ kind: 'traces', errorsFirst: true, byRoute: false });
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

// v0.10.2329 (audit B8) — panel tepesi başlığa yazılmaz; ayrı satır, eski
// kartın birim biçimiyle (mlValueText.test.ts 'birim' durumları buraya taşındı).
describe('panelPeakLine / peakValueText', () => {
  it.each<[number, string | undefined, string]>([
    [2800, undefined, '2.800'],
    [1340.5, undefined, '1.340,5'],
    [1.3399, undefined, '1,34'],
    [12345678, undefined, '12.345.678'],
    [1.5 * 2 ** 30, 'bytes', '1,5 GB'],
    [512 * 2 ** 20, 'bytes', '512 MB'],
    [0.25, 's', '250 ms'],
    [2.5, 's', '2,5 sn'],
    [1250, 'ms', '1,3 sn'],
    [0.42, 's/s', '0,42 sn/sn'],
    [3.2, 'core', '3,2 çekirdek'],
  ])('%d %s → %s', (v, u, text) => { expect(peakValueText(v, u)).toBe(text); });
  it('etiketli satır; düşüşte en düşük değer; tepe yoksa boş', () => {
    expect(panelPeakLine(2800, undefined, 'up')).toBe('Grafikteki tepe: 2.800');
    expect(panelPeakLine(9, '', 'down')).toBe('Grafikteki en düşük değer: 9');
    expect(panelPeakLine(null, 'bytes', 'up')).toBe('');
    expect(panelPeakLine(Number.NaN, 'bytes', 'up')).toBe('');
  });
  it('İngilizce arayüz', () => {
    expect(panelPeakLine(2800, undefined, 'up', 'en')).toBe('Peak on the chart: 2,800');
    expect(panelPeakLine(2.5, 's', 'down', 'en')).toBe('Lowest on the chart: 2.5 s');
    expect(peakValueText(3.2, 'core', 'en')).toBe('3.2 cores');
  });
});

// v0.10.2370 — kayıtlı olayda panel penceresi başlangıçtan açılır (geç algılama).
describe('recordLead', () => {
  const base = { startedAt: 2_000, lastSeen: 3_000, service: 'orders' };
  it('başlangıç algılamadan önceyse pencere başlangıca çekilir', () => {
    expect(recordLead(base, 1_000)).toEqual({ ...base, startedAt: 1_000 });
  });
  it('başlangıç yok / sonra ise girdi aynen', () => {
    expect(recordLead(base, 0)).toBe(base);
    expect(recordLead(base, undefined)).toBe(base);
    expect(recordLead(base, 2_500)).toBe(base);
  });
});
