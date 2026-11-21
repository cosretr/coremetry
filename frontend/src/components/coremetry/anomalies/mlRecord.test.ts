// v0.10.2247 (anomali kartı Dilim G) — kayıt grafiğinin SAF girdisi:
// 5 dk kovalık seri 1 dk snapshot'ta tek nokta/kova; tek bant dışı kova tek
// bölge (5 dk), beş ardışık dakika değil; birimler dile göre.
import { describe, it, expect } from 'vitest';
import type { MLRecordView } from '@/lib/types';
import { fmtRecordValue, measureKey, measuredSeriesFirst, outsideRegions, peakAtSec, recordBuckets, recordUsable, sourceLabel } from './mlRecord';

const T0 = 1_800_000_000; // 300'ün katı
function recFixture(over: Partial<MLRecordView> = {}): MLRecordView {
  const v = Array.from({ length: 30 }, (_, i) => (i >= 10 && i < 15 ? 135 : 43));
  return {
    v: 1, subject: { service: 'checkout-api', operation: 'GET /cart', resolved: true },
    series: { source: 'vm', query: 'histogram_quantile(0.95, …)', aggregation: 'p95', unit: 'ms', stepSec: 300 },
    baseline: { expected: 43, lo: 0, hi: 95, method: 'class_days_hour±1_median_mad', stepSec: 300 },
    direction: 'up', onsetAt: (T0 + 600) * 1e9, detectedAt: (T0 + 600) * 1e9, endedAt: (T0 + 900) * 1e9,
    peakValue: 135, valueAtDetection: 135, expectedValue: 43, volume: 1240,
    evidence: [], class: 'service_anomaly', gates: [],
    snapshot: { t0: T0, step: 60, aggregation: 'p95', v, lo: v.map(() => 0), hi: v.map(() => 95), n: v.map(() => 100) },
    title: '', detector: 'op_latency',
    ...over,
  };
}

describe('recordBuckets — gerçek kova boyu', () => {
  it('5 dk seri: 30 dk snapshot 6 kova, tek bant dışı kova tek bölge', () => {
    const b = recordBuckets(recFixture());
    expect(b.bucketSec).toBe(300);
    expect(b.t).toEqual([0, 1, 2, 3, 4, 5].map(i => T0 + i * 300));
    expect(b.v).toEqual([43, 43, 135, 43, 43, 43]);
    expect(b.out).toEqual([false, false, true, false, false, false]);
    expect(b.n).toEqual([100, 100, 100, 100, 100, 100]);
    expect(outsideRegions(b)).toEqual([{ fromSec: T0 + 600, toSec: T0 + 900 }]);
  });
  it('1 dk seri: nokta başına kova, ardışık bant dışı noktalar tek bölge', () => {
    const r = recFixture({ series: { ...recFixture().series, stepSec: 60 } });
    const b = recordBuckets(r);
    expect(b.t.length).toBe(30);
    expect(outsideRegions(b)).toEqual([{ fromSec: T0 + 600, toSec: T0 + 900 }]);
  });
  it('olay penceresi dışındaki sapma bölge sayılmaz; tepe zamanı pencereden', () => {
    const r = recFixture();
    r.snapshot.v[2] = 200; // pencere öncesi
    expect(recordBuckets(r).out[0]).toBe(false);
    expect(peakAtSec(r)).toBe(T0 + 600);
  });
  it('kullanılabilirlik: oluşum kaydı ve boş seri eski yola', () => {
    expect(recordUsable(recFixture())).toBe(true);
    expect(recordUsable(null)).toBe(false);
    expect(recordUsable(recFixture({ series: { ...recFixture().series, kind: 'occurrence' } }))).toBe(false);
    expect(recordUsable(recFixture({ snapshot: { t0: T0, step: 60, aggregation: 'p95', v: [] } }))).toBe(false);
  });
});

describe('birim ve ölçü', () => {
  it('EN İngilizce, TR Türkçe birim', () => {
    expect(fmtRecordValue(107, 'ms', 'en')).toBe('107 ms');
    expect(fmtRecordValue(2.5, 'core', 'en')).toBe('2.5 cores');
    expect(fmtRecordValue(2.5, 'core', 'tr')).toBe('2,5 çekirdek');
    expect(fmtRecordValue(2.1, 'percent', 'en')).toBe('2.1%');
    expect(fmtRecordValue(2.1, 'percent', 'tr')).toBe('%2,1');
    expect(fmtRecordValue(1536 * 1024 * 1024, 'bytes', 'en')).toBe('1.5 GiB');
    expect(fmtRecordValue(26, 'span/request', 'en')).toBe('26 spans/request');
    expect(fmtRecordValue(1240, 'count', 'tr')).toBe('1.240');
  });
  it('ölçü anahtarı seri tanımından; metrik ifadesi kimliğinden', () => {
    expect(measureKey(recFixture())).toBe('mlRecord.measure.p95');
    expect(measureKey(recFixture({ metricId: 'memory' }))).toBe('mlRecord.metric.memory');
    expect(measureKey(recFixture({ metricId: 'custom_x' }))).toBeNull();
    expect(measureKey(recFixture({ detector: 'svc_telemetry_volume', series: { ...recFixture().series, aggregation: 'spans_per_request', unit: 'span/request' } })))
      .toBe('mlRecord.measure.spansPerReq');
  });
  it('kaynak etiketi kayıttan', () => {
    expect(sourceLabel('vm')).toBe('VictoriaMetrics');
    expect(sourceLabel('ch:service_summary_5m')).toBe('ClickHouse · service_summary_5m');
    expect(sourceLabel('thanos:cluster-a')).toBe('Thanos · cluster-a');
  });
});

describe('measuredSeriesFirst — Olayın seyri ilk grafiği', () => {
  const vol = (aggregation: string) => recFixture({
    detector: 'svc_telemetry_volume',
    series: { source: 'ch:service_summary_5m', query: 'service_summary_5m …', aggregation, unit: 'span/request', stepSec: 300 },
  });
  it('istek başına span ve 5 dk span: ölçülen seri önde (istek hızı bağlam)', () => {
    expect(measuredSeriesFirst(vol('spans_per_request'))).toBe(true);
    expect(measuredSeriesFirst(vol('span_count'))).toBe(true);
  });
  it('diğer ölçüler canlı kaynak panelinde kalır', () => {
    expect(measuredSeriesFirst(recFixture())).toBe(false);
  });
  it('boş görüntü: ölçülen seri önde değil', () => {
    const r = vol('spans_per_request');
    expect(measuredSeriesFirst({ ...r, snapshot: { ...r.snapshot, v: [] } })).toBe(false);
  });
});
