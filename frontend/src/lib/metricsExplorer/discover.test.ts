// v0.10.1865 — Keşif sekmesi saf yardımcıları (Faz 2 dilim 2.5).
import { describe, expect, it } from 'vitest';
import {
  applyDiscover, decodeDiscover, fmtCount, fmtShare, highCardinality, labelRows, metricRows, openInExplorerSearch, searchMatch,
} from './discover';
import { decodeMx } from './model';
import type { MetricInfo, MetricsTSDBStatus } from '@/lib/types';

const stats: MetricsTSDBStatus = {
  totalSeries: 1_840_000, totalLabelValuePairs: 9000,
  seriesCountByMetricName: [
    { name: 'http_server_request_duration_seconds_bucket', value: 1_120_000 },
    { name: 'http_server_request_duration_seconds_count', value: 80_000 },
    { name: 'jvm_memory_used_bytes', value: 46_200 },
  ],
  seriesCountByLabelName: [{ name: 'service_name', value: 1_840_000 }, { name: 'k8s_pod_name', value: 1_620_000 }, { name: '__name__', value: 1_840_000 }],
  seriesCountByFocusLabelValue: [], seriesCountByLabelValuePair: [],
  labelValueCountByLabelName: [{ name: 'service_name', value: 38 }, { name: 'k8s_pod_name', value: 640 }, { name: 'le', value: 14 }],
};
const names: MetricInfo[] = [
  { name: 'jvm_memory_used_bytes', type: 'gauge', unit: 'By', description: '', help: 'Kullanılan JVM belleği.', typeSource: 'vm-metadata' },
];

describe('metricRows', () => {
  it('kardinalite omurgası + ad zarfı ekleri; pay', () => {
    const r = metricRows(stats, names, 'all');
    expect(r.map(x => x.name)).toEqual(stats.seriesCountByMetricName.map(e => e.name));
    expect(r[0]).toMatchObject({ kind: 'histogram', estimated: true, series: 1_120_000 });
    expect(r[0].share).toBeCloseTo(0.6087, 3);
    expect(r[2]).toMatchObject({ kind: 'gauge', estimated: false, unit: 'By', help: 'Kullanılan JVM belleği.' });
  });
  it('tür süzgeci istemcide', () => {
    expect(metricRows(stats, names, 'counter').map(x => x.name)).toEqual(['http_server_request_duration_seconds_count']);
  });
  it('kardinalite yoksa (CH) ad listesi tek başına, seri yok', () => {
    const r = metricRows(undefined, names, 'all');
    expect(r).toHaveLength(1);
    expect(r[0].series).toBeUndefined();
  });
});

describe('uyarı + etiketler', () => {
  it('tek metrik ≥ %30 → yüksek kardinalite', () => {
    expect(highCardinality(stats)).toMatchObject({ metric: 'http_server_request_duration_seconds_bucket', series: 1_120_000 });
    expect(highCardinality({ ...stats, seriesCountByMetricName: [{ name: 'x', value: 10 }] })).toBeNull();
    expect(highCardinality(undefined)).toBeNull();
  });
  it('etiket satırları: __name__ yok, seriye göre, ≥500 değer "yüksek"', () => {
    const r = labelRows(stats);
    expect(r.map(x => x.name)).toEqual(['service_name', 'k8s_pod_name', 'le']);
    expect(r[1]).toMatchObject({ values: 640, high: true });
    expect(r[0].high).toBe(false);
  });
});

describe('URL + açılış adresi', () => {
  it('kodek gidiş-dönüş, yabancı param korunur', () => {
    const next = applyDiscover(new URLSearchParams('tab=discover&range=1h'), { q: 'http', kind: 'histogram', match: '{a="b"}', label: 'service_name' });
    expect(decodeDiscover(next)).toEqual({ q: 'http', kind: 'histogram', match: '{a="b"}', label: 'service_name', service: '' });
    expect(next.get('range')).toBe('1h');
    expect(applyDiscover(next, { kind: 'all', q: '' }).has('dkind')).toBe(false);
  });
  it('searchMatch regex kaçışlı + ek süzgeç', () => {
    expect(searchMatch('http.server', '')).toBe('{__name__=~".*http\\\\.server.*"}');
    expect(searchMatch('', '{service_name="orders-api"}')).toBe('{service_name="orders-api"}');
    expect(searchMatch('', '')).toBeUndefined();
  });
  it('satır → Sorgu sekmesi (S3), tür varsayılanı, Keşif paramları silinir', () => {
    const href = openInExplorerSearch(new URLSearchParams('tab=discover&dq=http&range=1h&cluster=acme-prod-1'),
      { name: 'http_server_request_duration_seconds_count', kind: 'counter', estimated: true, unit: '' });
    const sp = new URLSearchParams(href.slice(1));
    expect(sp.has('tab')).toBe(false);
    expect(sp.has('dq')).toBe(false);
    expect(sp.get('range')).toBe('1h');
    expect(sp.get('cluster')).toBe('acme-prod-1');
    expect(decodeMx(sp.get('mx'))!.queries[0]).toMatchObject({ metric: 'http_server_request_duration_seconds_count', fn: 'rate', agg: 'sum' });
  });
  it.each([[1_840_000, '1,84 M'], [80_000, '80.000'], [undefined, '—']])('fmtCount %s', (n, want) => expect(fmtCount(n)).toBe(want));
  it.each([[0.6087, '%61'], [0.043, '%4,3'], [undefined, '—']])('fmtShare %s', (n, want) => expect(fmtShare(n)).toBe(want));
});
