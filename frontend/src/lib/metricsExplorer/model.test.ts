// v0.10.1861 — Metrics Explorer tek model + `?mx=` kodeki (Faz 2 dilim 2.1).
import { describe, expect, it } from 'vitest';
import {
  decodeMx, encodeMx, defaultMxModel, kindOf, defaultFnFor, defaultAggFor, withMetric, blankMxQuery,
  nextMxLetter, nextMxFormulaId, type MxModel,
} from './model';

describe('kindOf / varsayılan fonksiyon (S4: sayaç → rate, histogram → p95)', () => {
  it.each([
    ['http_server_request_duration_seconds_count', undefined, 'counter', true, 'rate', 'sum'],
    ['process_cpu_seconds_total', undefined, 'counter', true, 'rate', 'sum'],
    ['http_server_request_duration_seconds_bucket', undefined, 'histogram', true, 'p95', 'sum'],
    ['jvm_memory_used_bytes', undefined, 'gauge', true, 'raw', 'avg'],
    ['kafka_consumergroup_lag', 'gauge', 'gauge', false, 'raw', 'avg'],
    ['http.server.request.duration', 'histogram', 'histogram', false, 'p95', 'sum'],
    ['orders_total', 'sum', 'counter', false, 'rate', 'sum'],
    ['', undefined, 'unknown', true, 'raw', 'avg'],
  ] as const)('%s (%s) → %s', (name, reported, kind, estimated, fn, agg) => {
    const k = kindOf(name, reported);
    expect(k).toEqual({ kind, estimated });
    expect(defaultFnFor(k.kind)).toBe(fn);
    expect(defaultAggFor(k.kind)).toBe(agg);
  });

  it('withMetric türü ve önerilen fonksiyonu yeni metriğe göre kurar', () => {
    const q = withMetric({ ...blankMxQuery('A'), fn: 'raw', agg: 'avg' }, 'http_server_request_duration_seconds_count', undefined, '1');
    expect(q).toMatchObject({ metric: 'http_server_request_duration_seconds_count', metricKind: 'counter', kindEstimated: true, fn: 'rate', agg: 'sum', unit: '1' });
  });
});

describe('?mx= kodeki', () => {
  const full: MxModel = {
    mode: 'builder',
    queries: [
      {
        ...blankMxQuery('A'), metric: 'http_server_request_duration_seconds_count', metricKind: 'counter', kindEstimated: true,
        filters: [{ k: 'http_route', op: '!~', v: '/health.*' }], fn: 'rate', agg: 'sum', by: ['service_name', 'http_route'],
        legend: '{{service_name}} {{http_route}}',
      },
      {
        ...blankMxQuery('B'), hidden: true, metric: 'http_server_request_duration_seconds_count', metricKind: 'counter',
        filters: [{ k: 'http_response_status_code', op: '=~', v: '5..' }], fn: 'rate', agg: 'sum', by: ['service_name'],
      },
      { ...blankMxQuery('F1'), kind: 'formula', expr: 'B / A * 100', unit: '%', legend: 'hata % {{service_name}}' },
    ],
    code: '', viz: 'stacked', step: 60, limit: 20, rank: 'max', scopeOff: true, src: '',
  };

  it('gidiş-dönüş kayıpsız', () => {
    expect(decodeMx(encodeMx(full))).toEqual(full);
  });

  it('varsayılan model boş JSON yazar ve geri okur', () => {
    expect(encodeMx(defaultMxModel())).toBe('{}');
    expect(decodeMx('{}')).toEqual(defaultMxModel());
  });

  it('kod kipi metni ve kaynak taşınır', () => {
    const m: MxModel = { ...defaultMxModel(), mode: 'code', code: 'sum(rate(up[5m]))', src: 'thanos' };
    expect(decodeMx(encodeMx(m))).toEqual(m);
  });

  it.each([
    ['bozuk JSON', 'not-json', null],
    ['dizi', '[]', null],
    ['boş', '', null],
  ])('%s → null', (_n, raw, want) => {
    expect(decodeMx(raw)).toBe(want);
  });

  it('tanınmayan alanlar varsayılana düşer, yinelenen kimlik atılır', () => {
    const m = decodeMx(JSON.stringify({ vz: 'pie', s: 7, n: 13, r: 'x', q: [{ i: 'A', fn: 'zz', a: 'yy', f: [['k', '~~', 'v'], [1, '=', 'x']] }, { i: 'A' }] }));
    expect(m).not.toBeNull();
    expect(m!.viz).toBe('line');
    expect(m!.step).toBe(0);
    expect(m!.limit).toBe(8);
    expect(m!.rank).toBe('avg');
    expect(m!.queries).toHaveLength(1);
    expect(m!.queries[0]).toMatchObject({ fn: 'raw', agg: 'sum', filters: [{ k: 'k', op: '=', v: 'v' }] });
  });

  it('8 KiB üstü kod URL\'e yazılmaz ve okunmaz', () => {
    const big = 'x'.repeat(8193);
    expect(encodeMx({ ...defaultMxModel(), code: big })).toBe('{}');
    expect(decodeMx(JSON.stringify({ c: big }))!.code).toBe('');
  });
});

describe('harf yardımcıları', () => {
  it('sıradaki boş harf ve formül kimliği', () => {
    expect(nextMxLetter([blankMxQuery('A'), blankMxQuery('C')])).toBe('B');
    expect(nextMxLetter('ABCDEFGH'.split('').map(l => blankMxQuery(l)))).toBeNull();
    expect(nextMxFormulaId([blankMxQuery('F1')])).toBe('F2');
  });
});
