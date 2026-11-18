// v0.10.2346 — HTTP / gRPC sekmesi panellerinin Metrics açılışları AYNI
// veriyi mi açıyor? Her açılış Metrics sayfasının modeline çözülür,
// Metrics'in derleyicisiyle (compileMxQuery) PromQL'e çevrilir ve panelin
// httpQueries ifadesiyle karşılaştırılır. Kabul edilen tek farklar:
// rate penceresi ($__rate_interval ↔ sekmenin sabit [Ns]'i), yüzdelikte
// VM'in `vmrange` kovası, hata panellerinin görsel `> 0` kırpması ve
// gRPC ms ailesinin `/ 1000` ölçeği (link birimi 'ms' taşır).
import { describe, it, expect } from 'vitest';
import { clientSel, httpQueries, rateWindow, serverSel, type HttpScope } from './httpMetrics';
import { clientFilters, httpPanelLinks, serverFilters } from './httpMetricsLinks';
import { metricsLinkHref } from './metricsLink';
import { importLegacyMetricsUrl } from './metricsExplorer/legacy';
import { decodeMx, MX_PARAM, type MxModel } from './metricsExplorer/model';
import { compileMxFormula, compileMxQuery, matcherText } from './metricsExplorer/compile';
import { GRPC_LEGACY_FAMILY, HTTP_FAMILY } from './redFamily';

const W = 3600;
const R = rateWindow(W);

function modelOf(href: string): MxModel {
  const sp = new URLSearchParams(href.split('?')[1]);
  const mx = sp.get(MX_PARAM);
  const m = mx ? decodeMx(mx) : importLegacyMetricsUrl(sp)?.model;
  if (!m) throw new Error(`açılamadı: ${href}`);
  return m;
}
const norm = (e: string) => e.replace(/\[\$__rate_interval\]/g, `[${R}]`).replace(/le, vmrange/g, 'le');
const compiled = (m: MxModel) => m.queries.filter(q => !q.hidden).map(q => {
  if (q.kind === 'formula') { const f = compileMxFormula(q.expr, m.queries); return f.ok ? norm(f.expr) : `ERR ${q.expr}`; }
  return norm(compileMxQuery(q));
});

const SCOPES: Array<[string, HttpScope]> = [
  ['düz', { app: 'checkout', family: HTTP_FAMILY }],
  ['ortam + dışlama', { app: 'checkout', family: HTTP_FAMILY, envSel: 'deployment_environment_name="prod-a"', routeExclude: ['health', 'ready'] }],
  ['route + method süzgeci', { app: 'orders', family: HTTP_FAMILY, route: '/orders/{id}', method: 'GET', routeExclude: ['health'] }],
  ['gRPC', { app: 'payments', family: GRPC_LEGACY_FAMILY, envSel: 'deployment_environment_name=~"a|b"', method: 'pkg.Pay' }],
];

describe('serverFilters / clientFilters = serverSel / clientSel (kayma kapısı)', () => {
  it.each(SCOPES)('%s', (_, s) => {
    const sel = (fs: ReturnType<typeof serverFilters>) => `{${[`service_name="${s.app}"`, ...fs!.map(matcherText)].join(', ')}}`;
    expect(sel(serverFilters(s))).toBe(serverSel(s));
    expect(sel(serverFilters(s, [{ k: 'x', op: '=~', v: '5..' }]))).toBe(serverSel(s, 'x=~"5.."'));
    expect(sel(clientFilters(s))).toBe(clientSel(s));
  });
  it('okunamayan ortam eşleyicisi → link yok', () => {
    expect(httpPanelLinks({ app: 'checkout', envSel: 'garbled', family: HTTP_FAMILY }, {})).toEqual({});
  });
});

describe('her panel kendi çizdiği seriyi açar', () => {
  it.each(SCOPES)('%s', (_, s) => {
    const q = httpQueries(s, W);
    const links = httpPanelLinks(s, q);
    const grpc = s.family?.kind === 'grpc';
    const ctx = { service: s.app, range: { preset: '1h' as const }, src: 'vm' as const };
    const open = (id: string) => compiled(modelOf(metricsLinkHref(ctx, links[id])!));
    const unscale = (e: string) => (grpc ? e.replace(/^\((.*)\) \/ 1000$/, '$1') : e);
    expect(open('rateByRoute')).toEqual([q.rateByRoute]);
    expect(open('p95ByRoute')).toEqual([unscale(q.p95ByRoute)]);
    expect(open('p50')).toEqual([q.p50, q.p90, q.p95, q.p99].map(unscale));
    expect(open('err5ByRoute')).toEqual([q.err5ByRoute.replace(/ > 0$/, '')]);
    expect(open('err4ByRoute')).toEqual([q.err4ByRoute.replace(/ > 0$/, '')]);
    const sc = modelOf(metricsLinkHref(ctx, links.statusClass)!);
    expect([sc.mode, sc.code]).toEqual(['code', q.statusClass]);
    if (grpc) {
      expect(Object.keys(links).filter(k => k.startsWith('d'))).toEqual([]);
      return;
    }
    expect(open('dRateByAddr')).toEqual([q.dRateByAddr]);
    expect(open('dP95ByAddr')).toEqual([q.dP95ByAddr]);
    expect(open('p95')).toEqual([q.p95, q.dP95ByAddr]);
    expect(open('dErrTypes')).toEqual([q.dErrTypes.replace(/ > 0$/, '')]);
    // Panel oranı ×100 çiziyor (scale=100, birim %); formül aynısı.
    expect(open('dErrByAddr')).toEqual([`(${q.dErrByAddr.replace(' / ', ') / (')}) * 100`]);
  });

  it('süzgeçsiz HTTP paneli Overview biçiminde (by/agg/unit/mtype/range)', () => {
    const s: HttpScope = { app: 'checkout', family: HTTP_FAMILY };
    const links = httpPanelLinks(s, httpQueries(s, W));
    const ctx = { service: 'checkout', range: { preset: '1h' as const }, rangeParam: '6h' };
    expect(metricsLinkHref(ctx, links.p95ByRoute)).toBe(
      '/metrics?metric=http_server_request_duration_seconds&service=checkout&agg=p95'
      + '&by=http_request_method%2Chttp_route&unit=s&mtype=histogram&range=6h');
    expect(metricsLinkHref(ctx, links.rateByRoute)).toBe(
      '/metrics?metric=http_server_request_duration_seconds_count&service=checkout&agg=rate'
      + '&by=http_request_method%2Chttp_route&range=6h');
  });
});
