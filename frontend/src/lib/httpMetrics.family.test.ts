// httpMetrics.family.test.ts — v0.10.1429 (operator context: coremetry-ingest
// gRPC RED — Service sayfası metrik kipi yalnız gRPC sunan serviste boştu).
//
// Pinler: (1) HTTP ailesi = aile verilmeyen bugünkü üreticilerle BAYT BAYT
// aynı (mevcut altın testler yeşil kalsın); (2) gRPC legacy (ms) ve stable (s)
// PromQL'i — tam __name__ (alternasyon yok), rpc_* etiketleri, sunucu-hatası
// kümesi, ms ailesinde /1000 ve le="500"; (3) liste indirgeyicisi gRPC satırı
// (path = rpc_method, method = rpc_service, sınıflar ok/istemci/sunucu,
// family: 'grpc'); (4) durum kodu lejantı / rengi / sınıf ifadesi.
import { describe, it, expect } from 'vitest';
import {
  httpQueries, windowQueries, serverSel, quantileQuery, statusCodeRateQuery, totalRequestsQuery,
  endpointHttpQueries, endpointScope, listQueries, endpointListRows, endpointListScope,
  methodRouteLegend, statusCodeSeries, statusCodeColorVar, filterOptions,
} from './httpMetrics';
import { HTTP_FAMILY, GRPC_LEGACY_FAMILY, GRPC_STABLE_FAMILY } from './redFamily';
import type { SpanMetricSeries } from './types';

const SVC = 'grpc.example.test';
const ser = (labels: Record<string, string>, v: number): SpanMetricSeries => ({
  groupKey: Object.entries(labels).map(([k, x]) => `${k}="${x}"`),
  points: [{ time: 1e9, value: v }],
});

describe('HTTP ailesi == aile verilmeyen üreticiler (bayt bayt)', () => {
  it('httpQueries / windowQueries / serverSel / quantileQuery / tekil sorgular', () => {
    const a = { app: 'orders-api', route: '/a', method: 'GET' };
    const b = { ...a, family: HTTP_FAMILY };
    expect(httpQueries(b, 3600)).toEqual(httpQueries(a, 3600));
    expect(windowQueries(b, 3600, 'http_request_method, http_route', 3600)).toEqual(windowQueries(a, 3600, 'http_request_method, http_route', 3600));
    expect(serverSel(b, 'x="1"')).toBe(serverSel(a, 'x="1"'));
    expect(quantileQuery(b, 900, 0.95, 'http_route')).toBe(quantileQuery(a, 900, 0.95, 'http_route'));
    expect(statusCodeRateQuery('orders-api', 3600, undefined, HTTP_FAMILY)).toBe(statusCodeRateQuery('orders-api', 3600));
    expect(totalRequestsQuery('orders-api', 3600, undefined, HTTP_FAMILY)).toBe(totalRequestsQuery('orders-api', 3600));
    expect(listQueries({ app: 'orders-api', family: HTTP_FAMILY }, 3600, 50, true)).toEqual(listQueries({ app: 'orders-api' }, 3600, 50, true));
    expect(endpointHttpQueries({ service: 'orders-api', route: '/a', family: HTTP_FAMILY }, 3600, { latQ: 'p50', compare: true }))
      .toEqual(endpointHttpQueries({ service: 'orders-api', route: '/a' }, 3600, { latQ: 'p50', compare: true }));
    // Literal pin: bugünkü HTTP sorguları (altın testlerin dayanağı).
    expect(httpQueries(a, 3600).sli).toBe('sum(rate(http_server_request_duration_seconds_bucket{service_name="orders-api", http_route="/a", http_request_method="GET", le="0.5"}[180s])) / sum(rate(http_server_request_duration_seconds_count{service_name="orders-api", http_route="/a", http_request_method="GET"}[180s]))');
    expect(httpQueries(a, 3600).statusClass).toBe('sum by (cls) (label_replace(rate(http_server_request_duration_seconds_count{service_name="orders-api", http_route="/a", http_request_method="GET"}[180s]), "cls", "${1}xx", "http_response_status_code", "([0-9]).*"))');
  });
});

describe('gRPC legacy (rpc.server.duration, ms) PromQL pinleri', () => {
  const s = { app: SVC, family: GRPC_LEGACY_FAMILY };
  const q = httpQueries(s, 3600);
  const sel = `{service_name="${SVC}"}`;
  it('seçici: rpc etiketleri; route = rpc_method, method = rpc_service', () => {
    expect(serverSel({ app: SVC, route: 'Export', method: 'opentelemetry.proto.collector.trace.v1.TraceService', family: GRPC_LEGACY_FAMILY }))
      .toBe(`{service_name="${SVC}", rpc_method="Export", rpc_service="opentelemetry.proto.collector.trace.v1.TraceService"}`);
  });
  it('rate / total / err: tam __name__ (alternasyon yok), sunucu-hatası kümesi', () => {
    expect(q.rps).toBe(`sum(rate(rpc_server_duration_milliseconds_count${sel}[180s]))`);
    expect(q.total).toBe(`sum(increase(rpc_server_duration_milliseconds_count${sel}[3600s]))`);
    expect(q.err5).toBe(`(sum(rate(rpc_server_duration_milliseconds_count{service_name="${SVC}", rpc_grpc_status_code=~"2|4|8|12|13|14|15"}[180s])) or vector(0)) / sum(rate(rpc_server_duration_milliseconds_count${sel}[180s]))`);
    expect(q.err4).toBe(`(sum(rate(rpc_server_duration_milliseconds_count{service_name="${SVC}", rpc_grpc_status_code=~"1|3|5|6|7|9|10|11|16"}[180s])) or vector(0)) / sum(rate(rpc_server_duration_milliseconds_count${sel}[180s]))`);
    for (const e of Object.values(q)) {
      expect(e).not.toContain('__name__=~');
      expect(e).not.toContain('http_server_');
      expect(e).not.toContain('rpc_server_duration_seconds');
    }
  });
  it('gecikme ms → /1000 (karolar saniye okur); SLI kovası le="500"', () => {
    expect(q.p95).toBe(`(histogram_quantile(0.95, sum by (le) (rate(rpc_server_duration_milliseconds_bucket${sel}[180s])))) / 1000`);
    expect(q.p95ByRoute).toBe(`(histogram_quantile(0.95, sum by (le, rpc_service, rpc_method) (rate(rpc_server_duration_milliseconds_bucket${sel}[180s])))) / 1000`);
    expect(q.sli).toBe(`sum(rate(rpc_server_duration_milliseconds_bucket{service_name="${SVC}", le="500"}[180s])) / sum(rate(rpc_server_duration_milliseconds_count${sel}[180s]))`);
    const w = windowQueries(s, 3600);
    expect(w.avg).toBe(`(sum(increase(rpc_server_duration_milliseconds_sum${sel}[3600s])) / sum(increase(rpc_server_duration_milliseconds_count${sel}[3600s]))) / 1000`);
    expect(w.p99).toBe(`(histogram_quantile(0.99, sum by (le) (increase(rpc_server_duration_milliseconds_bucket${sel}[3600s])))) / 1000`);
    expect(w.err5Count).toBe(`sum(increase(rpc_server_duration_milliseconds_count{service_name="${SVC}", rpc_grpc_status_code=~"2|4|8|12|13|14|15"}[3600s])) or (0 * sum(increase(rpc_server_duration_milliseconds_count${sel}[3600s])))`);
  });
  it('durum sınıfı: ok | client | server (kod kümeleri), kod dağılımı rpc_grpc_status_code', () => {
    expect(q.statusClass).toBe(`sum by (cls) (label_replace(label_replace(label_replace(rate(rpc_server_duration_milliseconds_count${sel}[180s]), "cls", "ok", "rpc_grpc_status_code", "^0$"), "cls", "client", "rpc_grpc_status_code", "^(1|3|5|6|7|9|10|11|16)$"), "cls", "server", "rpc_grpc_status_code", "^(2|4|8|12|13|14|15)$"))`);
    expect(q.statusCodes).toBe(`sum by (rpc_grpc_status_code) (increase(rpc_server_duration_milliseconds_count${sel}[3600s]))`);
    expect(q.topRoutes).toBe(`topk(10, sum by (rpc_method) (increase(rpc_server_duration_milliseconds_count${sel}[3600s])))`);
    expect(q.rateByRoute).toBe(`sum by (rpc_service, rpc_method) (rate(rpc_server_duration_milliseconds_count${sel}[180s]))`);
  });
  it('Overview tekil sorguları ve endpoint kapsamı', () => {
    expect(statusCodeRateQuery(SVC, 3600, undefined, GRPC_LEGACY_FAMILY)).toBe(`sum by (rpc_grpc_status_code) (rate(rpc_server_duration_milliseconds_count${sel}[180s]))`);
    expect(totalRequestsQuery(SVC, 3600, 'deployment_environment_name="prod-eu-a"', GRPC_LEGACY_FAMILY))
      .toBe(`sum(increase(rpc_server_duration_milliseconds_count{service_name="${SVC}", deployment_environment_name="prod-eu-a"}[3600s]))`);
    expect(endpointScope({ service: SVC, route: 'Export', method: 'x.TraceService', family: GRPC_LEGACY_FAMILY }))
      .toEqual({ app: SVC, family: GRPC_LEGACY_FAMILY, route: 'Export', method: 'x.TraceService' });
    const items = endpointHttpQueries({ service: SVC, route: 'Export', family: GRPC_LEGACY_FAMILY }, 3600, { latQ: 'p99', compare: false });
    expect(items.find(i => i.id === 'latByRoute')?.expr).toBe(`(histogram_quantile(0.99, sum by (le, rpc_service, rpc_method) (rate(rpc_server_duration_milliseconds_bucket{service_name="${SVC}", rpc_method="Export"}[180s])))) / 1000`);
  });
});

describe('gRPC stable (rpc.server.call.duration, s)', () => {
  it('ölçek yok, le="0.5", rpc_response_status_code', () => {
    const q = httpQueries({ app: SVC, family: GRPC_STABLE_FAMILY }, 3600);
    expect(q.p95).toBe(`histogram_quantile(0.95, sum by (le) (rate(rpc_server_call_duration_seconds_bucket{service_name="${SVC}"}[180s])))`);
    expect(q.sli).toContain('le="0.5"');
    expect(q.err5).toContain('rpc_response_status_code=~"2|4|8|12|13|14|15"');
    expect(windowQueries({ app: SVC, family: GRPC_STABLE_FAMILY }, 3600).avg).not.toContain('/ 1000');
  });
});

describe('liste (gRPC): havuz + indirgeyici', () => {
  it('listQueries gRPC: BY = rpc_service, rpc_method; cls ok/client/server', () => {
    const qs = listQueries(endpointListScope(SVC, '', undefined, GRPC_LEGACY_FAMILY), 3600, 50, false);
    expect(qs[0].expr).toBe(`topk(50, sum by (rpc_service, rpc_method) (increase(rpc_server_duration_milliseconds_count{service_name="${SVC}"}[3600s])))`);
    expect(qs.find(q => q.id === 'cls')?.expr).toContain('sum by (rpc_service, rpc_method, cls) (label_replace(label_replace(label_replace(increase(rpc_server_duration_milliseconds_count');
    expect(qs.find(q => q.id === 'avg')?.expr).toContain(') / 1000) and on (rpc_service, rpc_method) topk(50,');
  });
  it('endpointListRows gRPC: path = rpc_method, method = rpc_service, sınıflar, family', () => {
    const L = { rpc_service: 'x.TraceService', rpc_method: 'Export' };
    const rows = endpointListRows({
      total: [ser(L, 1000)], err5: [ser(L, 50)], avg: [ser(L, 0.012)], p50: [ser(L, 0.01)], p95: [ser(L, 0.05)], p99: [ser(L, 0.2)],
      cls: [ser({ ...L, cls: 'ok' }, 900), ser({ ...L, cls: 'client' }, 50), ser({ ...L, cls: 'server' }, 50)],
    }, 3600, SVC, GRPC_LEGACY_FAMILY);
    expect(rows).toEqual([{
      service: SVC, path: 'Export', method: 'x.TraceService', calls: 1000, errors: 50, errorRate: 5, reqPerMin: 1000 / 60,
      avgMs: 12, p50Ms: 10, p95Ms: 50, p99Ms: 200, http2xx: 900, http3xx: 0, http4xx: 50, http5xx: 50, family: 'grpc',
    }]);
    // HTTP satırı family taşımaz (eski şekil).
    const h = endpointListRows({ total: [ser({ http_request_method: 'GET', http_route: '/a' }, 1)] }, 3600, 'orders-api');
    expect(h[0]).not.toHaveProperty('family');
    expect(h[0].path).toBe('/a');
  });
});

describe('lejant / durum kodu adları / renk', () => {
  it('methodRouteLegend gRPC: pkg.Service/Method; method yoksa "(method yok)"; durumla kod adı', () => {
    expect(methodRouteLegend({ rpc_service: 'x.TraceService', rpc_method: 'Export' }, false, GRPC_LEGACY_FAMILY)).toBe('x.TraceService/Export');
    expect(methodRouteLegend({ rpc_service: 'x.TraceService' }, false, GRPC_LEGACY_FAMILY)).toBe('x.TraceService/(method yok)');
    expect(methodRouteLegend({ rpc_service: 'x.S', rpc_method: 'M', rpc_grpc_status_code: '14' }, true, GRPC_LEGACY_FAMILY)).toBe('14 UNAVAILABLE x.S/M');
    expect(methodRouteLegend({ http_request_method: 'GET', http_route: '/a' })).toBe('GET /a'); // HTTP aynen
  });
  it('statusCodeSeries gRPC: "14 UNAVAILABLE", sıra sayısal, renk ok/istemci/sunucu sınıfından', () => {
    const r = statusCodeSeries([ser({ rpc_grpc_status_code: '14' }, 1), ser({ rpc_grpc_status_code: '0' }, 9), ser({ rpc_grpc_status_code: '5' }, 2)], GRPC_LEGACY_FAMILY);
    expect(r.series.map(s => s.groupKey[0])).toEqual(['0 OK', '5 NOT_FOUND', '14 UNAVAILABLE']);
    expect(r.tokens.get('0 OK')).toBe('--ok');
    expect(r.tokens.get('5 NOT_FOUND')).toBe('--orange');
    expect(r.tokens.get('14 UNAVAILABLE')).toBe('--err');
    expect(statusCodeColorVar('14 UNAVAILABLE', GRPC_LEGACY_FAMILY)).toBe('var(--err)');
    expect(statusCodeColorVar('5', GRPC_LEGACY_FAMILY)).toBe('var(--orange)');
    expect(statusCodeColorVar('503')).toBe('var(--err)'); // HTTP aynen
  });
  it('filterOptions gRPC: route = rpc_method, method = rpc_service', () => {
    const o = filterOptions([ser({ rpc_service: 'x.S', rpc_method: 'Export' }, 1), ser({ rpc_service: 'x.S', rpc_method: 'none' }, 1)], GRPC_LEGACY_FAMILY);
    expect(o).toEqual({ routes: ['Export'], methods: ['x.S'] });
  });
});
