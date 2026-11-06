// v0.10.1861 — eski /metrics ve Explore URL'lerinin TEK modele bir kez
// içe okunması (Faz 2 dilim 2.1; kabul ölçütü: "eski yer imi aynı grafiği
// açar", tablo testi). Beklenen model, eski biçimin sunucuya gönderdiği
// soruyla aynı anlamı taşır (internal/vmetrics/promql.go promAggregator +
// promMatcher).
import { describe, expect, it } from 'vitest';
import {
  importLegacyMetricsUrl, applyMxImport, mxFromExploreState, mxFromExploreQuery, mxFilterFromExpr,
  promLabel, likeToRegex, mxFnAggFromLegacy,
} from './legacy';
import { decodeMx, defaultMxModel, blankMxQuery } from './model';
import type { FilterExpr } from '@/lib/types';

const sp = (s: string) => new URLSearchParams(s);
const mqe = (o: unknown) => `mq=${encodeURIComponent(JSON.stringify(o))}`;

describe('promLabel / filtre çevirisi (vmetrics aynası)', () => {
  it.each([
    ['service.name', 'service_name'],
    ['resource.k8s.cluster.name', 'k8s_cluster_name'],
    ['span.http.route', 'http_route'],
    ['http_route', 'http_route'],
    ['1abc', '_1abc'],
  ])('%s → %s', (k, want) => expect(promLabel(k)).toBe(want));

  it.each<[string, FilterExpr, ReturnType<typeof mxFilterFromExpr>]>([
    ['= tek', { k: 'service.name', op: '=', v: ['orders-api'] }, { k: 'service_name', op: '=', v: 'orders-api' }],
    ['IN çok', { k: 'service.name', op: 'IN', v: ['orders-api', 'pay.v2'] }, { k: 'service_name', op: '=~', v: 'orders-api|pay\\.v2' }],
    ['!= tek', { k: 'http.route', op: '!=', v: ['/health'] }, { k: 'http_route', op: '!=', v: '/health' }],
    ['NOT IN çok', { k: 'http.route', op: 'NOT IN', v: ['/a', '/b'] }, { k: 'http_route', op: '!~', v: '/a|/b' }],
    ['LIKE', { k: 'http.route', op: 'LIKE', v: ['api_v1'] }, { k: 'http_route', op: '=~', v: '.*api.v1.*' }],
    ['NOT LIKE', { k: 'http.route', op: 'NOT LIKE', v: ['health'] }, { k: 'http_route', op: '!~', v: '.*health.*' }],
    ['EXISTS', { k: 'db.system', op: 'EXISTS', v: [] }, { k: 'db_system', op: '!=', v: '' }],
    ['NOT EXISTS', { k: 'db.system', op: 'NOT EXISTS', v: [] }, { k: 'db_system', op: '=', v: '' }],
    ['> çevrilmez', { k: 'n', op: '>', v: ['5'] }, null],
    ['değersiz =', { k: 'n', op: '=', v: [] }, null],
  ])('%s', (_n, f, want) => expect(mxFilterFromExpr(f)).toEqual(want));

  it('LIKE kaçışları', () => {
    expect(likeToRegex('%a\\%b_c%')).toBe('.*a%b.c.*');
  });

  it.each([
    ['rate', 'rate', 'sum'], ['increase', 'increase', 'sum'], ['p99', 'p99', 'sum'],
    ['avg', 'raw', 'avg'], ['sum', 'raw', 'sum'], ['min', 'raw', 'min'], ['max', 'raw', 'max'], ['last', 'raw', 'max'],
  ])('agg %s → fn %s · %s', (agg, fn, a) => {
    expect(mxFnAggFromLegacy(agg, 'x')).toEqual({ fn, agg: a });
  });
  it('agg yoksa türden', () => {
    expect(mxFnAggFromLegacy(undefined, 'orders_total')).toEqual({ fn: 'rate', agg: 'sum' });
  });
});

describe('importLegacyMetricsUrl — eski yer imleri', () => {
  it('yeni biçim / boş URL → null (dokunulmaz)', () => {
    expect(importLegacyMetricsUrl(sp(''))).toBeNull();
    expect(importLegacyMetricsUrl(sp('mx=%7B%7D&range=1h'))).toBeNull();
    expect(importLegacyMetricsUrl(sp('tab=discover'))).toBeNull();
  });

  it('MQE ?mq= → builder modeli (harfler, gizli, formül, filtre, by, adım, sınır)', () => {
    const imp = importLegacyMetricsUrl(sp(mqe({
      n: 12, vz: 'bars', un: '%',
      q: [
        { i: 'A', k: 'm', e: 1, m: 'http_server_request_duration_seconds_count', u: '1', mt: 'sum', a: 'rate',
          f: [{ k: 'http.route', op: 'NOT LIKE', v: ['health'] }], g: ['service.name'], s: 60, l: 'istek' },
        { i: 'B', k: 'm', e: 0, m: 'http_server_request_duration_seconds_count', a: 'rate',
          f: [{ k: 'http.response.status_code', op: 'IN', v: ['500', '503'] }], g: ['service.name'] },
        { i: 'C', k: 'f', e: 1, x: 'B / A * 100', l: 'hata %' },
      ],
    }) + '&editor=1&range=6h'))!;
    expect(imp.tab).toBe('query');
    expect(imp.consumed.sort()).toEqual(['editor', 'mq']);
    expect(imp.model).toEqual({
      ...defaultMxModel(), viz: 'line', step: 60, limit: 20,
      queries: [
        { ...blankMxQuery('A'), metric: 'http_server_request_duration_seconds_count', metricKind: 'counter', unit: '1',
          filters: [{ k: 'http_route', op: '!~', v: '.*health.*' }], fn: 'rate', agg: 'sum', by: ['service_name'], legend: 'istek' },
        { ...blankMxQuery('B'), hidden: true, metric: 'http_server_request_duration_seconds_count', metricKind: 'counter', kindEstimated: true,
          filters: [{ k: 'http_response_status_code', op: '=~', v: '500|503' }], fn: 'rate', agg: 'sum', by: ['service_name'] },
        { ...blankMxQuery('F1'), kind: 'formula', expr: 'B / A * 100', legend: 'hata %', unit: '%' },
      ],
    });
    const next = applyMxImport(sp(mqe({ q: [] }) + '&editor=1&range=6h&cluster=acme-prod-1'), imp);
    expect(next.get('range')).toBe('6h');
    expect(next.get('cluster')).toBe('acme-prod-1');
    expect(next.has('mq')).toBe(false);
    expect(next.has('editor')).toBe(false);
    expect(decodeMx(next.get('mx'))).toEqual(imp.model);
  });

  it.each([
    ['vm', 'editor=1&tab=promql&promql=sum(rate(up%5B5m%5D))&src=vm&range=1h', 'vm', 'sum(rate(up[5m]))'],
    ['thanos + cluster', 'editor=1&tab=promql&promql=up&src=thanos&cluster=acme-prod-1', 'thanos', 'up'],
    ['thanos cluster yok → varsayılan', 'tab=promql&promql=up&src=thanos', '', 'up'],
    ['tanınmayan src', 'tab=promql&promql=up&src=zz', '', 'up'],
  ])('PromQL sekmesi (kanıt çipi) → kod kipi: %s', (_n, url, src, code) => {
    const imp = importLegacyMetricsUrl(sp(url))!;
    expect(imp.model).toEqual({ ...defaultMxModel(), mode: 'code', code, src });
    const next = applyMxImport(sp(url), imp);
    for (const k of ['tab', 'promql', 'src', 'editor']) expect(next.has(k)).toBe(false);
    if (url.includes('cluster=')) expect(next.get('cluster')).toBe('acme-prod-1');
  });

  it('eski katalog linki (Overview kapısı) → builder sorgusu A', () => {
    const url = 'metric=http_server_request_duration_seconds_count&service=orders-api&agg=rate&by=http.route&unit=s&range=1h';
    const imp = importLegacyMetricsUrl(sp(url))!;
    expect(imp.model!.queries).toEqual([{
      ...blankMxQuery('A'), metric: 'http_server_request_duration_seconds_count', metricKind: 'counter', kindEstimated: true, unit: 's',
      filters: [{ k: 'service_name', op: '=', v: 'orders-api' }], fn: 'rate', agg: 'sum', by: ['http_route'],
    }]);
    const next = applyMxImport(sp(url), imp);
    expect([...next.keys()].sort()).toEqual(['mx', 'range']);
  });

  it('katalog görünümü (editor=0, servis) → Keşif + servis süzgeci', () => {
    const imp = importLegacyMetricsUrl(sp('editor=0&service=payments-mobile&range=1h'))!;
    expect(imp).toMatchObject({ model: null, tab: 'discover', service: 'payments-mobile' });
    const next = applyMxImport(sp('editor=0&service=payments-mobile&range=1h'), imp);
    expect(next.get('tab')).toBe('discover');
    expect(next.get('dsvc')).toBe('payments-mobile');
    expect(next.has('service')).toBe(false);
    expect(next.has('editor')).toBe(false);
  });

  it('yalnız editor=1 → param silinir, model yok', () => {
    const imp = importLegacyMetricsUrl(sp('editor=1&range=1h'))!;
    expect(imp).toMatchObject({ model: null, tab: 'query', consumed: ['editor'] });
  });

  it('mx varken eski param\'lar yalnız silinir', () => {
    const imp = importLegacyMetricsUrl(sp('mx=%7B%7D&mq=x&tab=promql'))!;
    expect(imp.model).toBeNull();
    expect(imp.consumed.sort()).toEqual(['mq', 'tab']);
  });
});

describe('Explore ?q= metrik satırı', () => {
  const eq = {
    letter: 'A', source: 'metric', enabled: true, metric: 'jvm_memory_used_bytes', unit: 'By', agg: 'max',
    scope: 'orders-api', splitBy: ['k8s.pod.name'], filters: [{ k: 'jvm.memory.pool.name', op: '=', v: ['G1 Old Gen'] } as FilterExpr],
  };
  it('satır → MxQuery (scope service_name süzgecine, splitBy by\'a)', () => {
    expect(mxFromExploreQuery(eq)).toEqual({
      ...blankMxQuery('A'), metric: 'jvm_memory_used_bytes', metricKind: 'gauge', kindEstimated: true, unit: 'By',
      filters: [{ k: 'service_name', op: '=', v: 'orders-api' }, { k: 'jvm_memory_pool_name', op: '=', v: 'G1 Old Gen' }],
      fn: 'raw', agg: 'max', by: ['k8s_pod_name'],
    });
  });
  it('durum → model; span satırı uyarıyla düşer; metrik yoksa null', () => {
    const w: string[] = [];
    const m = mxFromExploreState({
      queries: [eq, { ...eq, letter: 'B', source: 'span', metric: 'duration_ms' }], formula: 'A / 2', viz: 'toplist', step: 300, topN: 50,
    }, w)!;
    expect(m.queries.map(q => q.id)).toEqual(['A', 'F1']);
    expect(m.queries[1]).toMatchObject({ kind: 'formula', expr: 'A / 2' });
    expect(m).toMatchObject({ viz: 'line', step: 300, limit: 50 });
    expect(w).toHaveLength(1);
    expect(mxFromExploreState({ queries: [{ ...eq, source: 'span' }], formula: '', viz: 'line', step: 0 })).toBeNull();
  });
});
