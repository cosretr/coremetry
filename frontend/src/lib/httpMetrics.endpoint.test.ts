import { describe, it, expect } from 'vitest';
import {
  endpointFilter, endpointScope, endpointHttpQueries, endpointRed, methodRouteLegend, shapeRouteRegex,
  windowQueries, quantileQuery, httpQueries, relabel, statusCodeColorVar, fmtCompact,
} from './httpMetrics';
import type { SpanMetricSeries } from './types';

// v0.10.1352 — Operator-reported (prod): /endpoint?service=acm-order-quote-
// allocation-prod&path=/ACMWEB/decision/service/rest&range=3h "Kaynak: metrik
// (vm)" Calls/requests ve gecikme grafikleri (p95 seçiliyken de) Service → HTTP
// Metrics sekmesindeki aynı route ile tutmuyordu: sayfa /api/endpoints/metric
// (throughput seam'i, 180 s adım başına yüzdelik, çağrı-ağırlıklı karo
// ortalaması) okuyordu. Karar: sekmenin AYNI PromQL'i, route'a süzülü.

const ser = (labels: Record<string, string>, vals: number[]): SpanMetricSeries => ({
  groupKey: Object.entries(labels).map(([k, v]) => `${k}="${v}"`),
  points: vals.map((v, i) => ({ time: i * 1e9, value: v })),
});

describe('endpointFilter (v0.10.1352)', () => {
  it.each([
    ['servis + route', { service: 'shop', route: '/a' }, '{service_name="shop", http_route="/a"}'],
    ['method bilinirse eklenir', { service: 'shop', route: '/a', method: 'POST' }, '{service_name="shop", http_route="/a", http_request_method="POST"}'],
    ['boş method yok sayılır', { service: 'shop', route: '/a', method: '  ' }, '{service_name="shop", http_route="/a"}'],
    ['route kaçışı: tırnak + ters bölü', { service: 'shop', route: '/a"b\\c' }, '{service_name="shop", http_route="/a\\"b\\\\c"}'],
    ['route regex metakarakteri tam eşitlikte kaçmaz', { service: 's', route: '/v1/items.json?x=(1)' }, '{service_name="s", http_route="/v1/items.json?x=(1)"}'],
    ['boş route servis geneline GENİŞLEMEZ', { service: 's', route: '' }, '{service_name="s", http_route=~""}'],
    ['şekil kipi → regex', { service: 's', route: '/users/:id/orders', sig: true }, '{service_name="s", http_route=~"/users/[^/]+/orders"}'],
    ['operatör vakası', { service: 'acm-order-quote-allocation-prod', route: '/ACMWEB/decision/service/rest' },
      '{service_name="acm-order-quote-allocation-prod", http_route="/ACMWEB/decision/service/rest"}'],
  ])('%s', (_n, ep, want) => {
    expect(endpointFilter(ep)).toBe(want);
  });
  it('şekil regex\'i metakarakterleri kaçırır', () => {
    expect(shapeRouteRegex('/a.b/:id')).toBe('/a\\.b/[^/]+');
    expect(shapeRouteRegex('/x(1)+:id')).toBe('/x\\(1\\)\\+[^/]+');
    expect(new RegExp(`^${shapeRouteRegex('/users/:id/orders')}$`).test('/users/8421/orders')).toBe(true);
    expect(new RegExp(`^${shapeRouteRegex('/users/:id/orders')}$`).test('/users/:id/orders')).toBe(true);
    expect(new RegExp(`^${shapeRouteRegex('/users/:id/orders')}$`).test('/users/1/2/orders')).toBe(false);
  });
  it('kapsam sekmenin HttpScope\'u', () => {
    expect(endpointScope({ service: 's', route: '/a', method: 'GET' })).toEqual({ app: 's', route: '/a', method: 'GET' });
  });
});

describe('methodRouteLegend (v0.10.1352)', () => {
  it.each([
    [{ http_request_method: 'GET', http_route: '/a' }, false, 'GET /a'],
    [{ http_request_method: 'GET' }, false, 'GET (route yok)'],
    [{ http_request_method: 'GET', http_route: '' }, false, 'GET (route yok)'],
    [{ http_route: '/a' }, false, '/a'],
    [{ http_response_status_code: '500', http_request_method: 'POST' }, true, '500 POST (route yok)'],
    [{ http_response_status_code: '404', http_request_method: 'GET', http_route: '/b' }, true, '404 GET /b'],
  ])('%j status=%s → %s', (labels, st, want) => {
    expect(methodRouteLegend(labels, st)).toBe(want);
  });
  it('relabel fonksiyon şablonu kabul eder', () => {
    expect(relabel([ser({ http_request_method: 'GET' }, [1])], l => methodRouteLegend(l))[0].groupKey).toEqual(['GET (route yok)']);
  });
  it('kod rengi ve kısa sayı', () => {
    expect(statusCodeColorVar('503')).toBe('var(--err)');
    expect(statusCodeColorVar('x')).toBeUndefined();
    expect(fmtCompact(null)).toBe('—');
  });
});

describe('endpoint PromQL pinleri — bayt bayt (v0.10.1352, 3h = 36 s adım, 180 s rate (v0.10.2489; önce 144 s), W = 10800 s)', () => {
  const got = endpointHttpQueries(
    { service: 'acm-order-quote-allocation-prod', route: '/ACMWEB/decision/service/rest' }, 10800, { latQ: 'p95', compare: true });
  it('her sorgu sabit', () => {
    const want: Array<[string, boolean, string]> = [
      ["rateByRoute", false, "sum by (http_request_method, http_route) (rate(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[180s]))"],
      ["err5ByRoute", false, "sum by (http_route, http_request_method, http_response_status_code) (rate(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\", http_response_status_code=~\"5..\"}[180s])) > 0"],
      ["err4ByRoute", false, "sum by (http_route, http_request_method, http_response_status_code) (rate(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\", http_response_status_code=~\"4..\"}[180s])) > 0"],
      ["p50", false, "histogram_quantile(0.5, sum by (le) (rate(http_server_request_duration_seconds_bucket{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[180s])))"],
      ["p90", false, "histogram_quantile(0.9, sum by (le) (rate(http_server_request_duration_seconds_bucket{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[180s])))"],
      ["p95", false, "histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[180s])))"],
      ["p99", false, "histogram_quantile(0.99, sum by (le) (rate(http_server_request_duration_seconds_bucket{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[180s])))"],
      ["latByRoute", false, "histogram_quantile(0.95, sum by (le, http_request_method, http_route) (rate(http_server_request_duration_seconds_bucket{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[180s])))"],
      ["statusCodes", true, "sum by (http_response_status_code) (increase(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s]))"],
      ["wTotal", true, "sum(increase(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s]))"],
      ["wErr5", true, "sum(increase(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\", http_response_status_code=~\"5..\"}[10800s])) or (0 * sum(increase(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s])))"],
      ["wAvg", true, "sum(increase(http_server_request_duration_seconds_sum{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s])) / sum(increase(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s]))"],
      ["wP50", true, "histogram_quantile(0.5, sum by (le) (increase(http_server_request_duration_seconds_bucket{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s])))"],
      ["wP95", true, "histogram_quantile(0.95, sum by (le) (increase(http_server_request_duration_seconds_bucket{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s])))"],
      ["wP99", true, "histogram_quantile(0.99, sum by (le) (increase(http_server_request_duration_seconds_bucket{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s])))"],
      ["pTotal", true, "sum(increase(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s] offset 10800s))"],
      ["pErr5", true, "sum(increase(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\", http_response_status_code=~\"5..\"}[10800s] offset 10800s)) or (0 * sum(increase(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s] offset 10800s)))"],
      ["pAvg", true, "sum(increase(http_server_request_duration_seconds_sum{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s] offset 10800s)) / sum(increase(http_server_request_duration_seconds_count{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s] offset 10800s))"],
      ["pP99", true, "histogram_quantile(0.99, sum by (le) (increase(http_server_request_duration_seconds_bucket{service_name=\"acm-order-quote-allocation-prod\", http_route=\"/ACMWEB/decision/service/rest\"}[10800s] offset 10800s)))"],
    ];
    expect(got.map(q => [q.id, q.instant, q.expr])).toEqual(want);
  });
  it('grafikler sekmenin AYNI üreticisi (route süzgeciyle sekme = endpoint)', () => {
    const tab = httpQueries({ app: 'acm-order-quote-allocation-prod', route: '/ACMWEB/decision/service/rest' }, 10800);
    const by = Object.fromEntries(got.map(q => [q.id, q.expr]));
    for (const id of ['rateByRoute', 'err5ByRoute', 'err4ByRoute', 'p50', 'p90', 'p95', 'p99', 'statusCodes'] as const) expect(by[id]).toBe(tab[id]);
    expect(by.latByRoute).toBe(tab.p95ByRoute);
  });
  it('lat seçici yüzdeliği değiştirir; compare yoksa offset sorgusu yok', () => {
    const q = endpointHttpQueries({ service: 's', route: '/a' }, 3600, { latQ: 'p99', compare: false });
    expect(q.find(x => x.id === 'latByRoute')?.expr).toBe(quantileQuery({ app: 's', route: '/a' }, 3600, 0.99, 'http_request_method, http_route'));
    expect(q.some(x => x.expr.includes('offset'))).toBe(false);
    expect(q.filter(x => x.instant).map(x => x.id)).toEqual(['statusCodes', 'wTotal', 'wErr5', 'wAvg', 'wP50', 'wP95', 'wP99']);
  });
});

describe('pencere sorguları — sekme tablosu ile karo aynı üretici (v0.10.1352)', () => {
  it('by = method, route → sekmenin tTotal/tAvg/tP95/tP99/tErr5/tErr4', () => {
    const sc = { app: 'shop' };
    const tab = httpQueries(sc, 3600);
    const w = windowQueries(sc, 3600, 'http_request_method, http_route');
    expect([w.total, w.avg, w.p95, w.p99, w.err5, w.err4]).toEqual([tab.tTotal, tab.tAvg, tab.tP95, tab.tP99, tab.tErr5, tab.tErr4]);
    expect(tab.tP95).toBe('histogram_quantile(0.95, sum by (le, http_request_method, http_route) (increase(http_server_request_duration_seconds_bucket{service_name="shop"}[3600s])))');
  });
  it('karolar: oran, dakika başı, null güvenli', () => {
    const r = endpointRed({ total: [ser({}, [1200])], err5: [ser({}, [12])], avg: [ser({}, [0.05])], p50: [], p95: [ser({}, [0.2])], p99: [ser({}, [NaN])] }, 3600);
    expect(r).toEqual({ calls: 1200, errors: 12, errRate: 0.01, reqPerMin: 20, avgS: 0.05, p50S: null, p95S: 0.2, p99S: null });
    expect(endpointRed({}, 3600).errRate).toBeNull();
    expect(endpointRed({ total: [ser({}, [0])], err5: [ser({}, [0])] }, 3600).errRate).toBeNull();
  });
});
