// v0.10.2346 — paylaşılan Metrics link üreticisi (lib/metricsLink).
// (1) Overview'un linkleri BAYT BAYT aynı: taşınmadan önceki satır içi
//     metricsHref aşağıda referans olarak birebir duruyor; aynı girdilerle
//     aynı dizgiyi üretmeli.
// (2) Overview'un dört dersi (kırılım, ad+agg, birim, tür) + aralık.
// (3) `?mx=` / kod biçimleri Metrics sayfasının okuduğu modele döner.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { metricsHref, metricsLinkHref, parsePromMatchers } from './metricsLink';
import { encodeRange } from './urlState';
import { importLegacyMetricsUrl } from './metricsExplorer/legacy';
import { decodeMx, MX_PARAM } from './metricsExplorer/model';
import { compileMxQuery, compileMxFormula, matcherText } from './metricsExplorer/compile';
import type { TimeRange } from './types';

// Taşınmadan önceki Overview.tsx metricsHref'i (v0.10.2343), kapanış
// değişkenleri parametre olarak — referans uygulama.
function overviewBefore(service: string, rangeParam: string | null, range: TimeRange,
  opts: { by?: string; metric: string; agg: 'rate' | 'avg'; unit?: string; mtype?: 'histogram' }) {
  const r = rangeParam;
  return `/metrics?metric=${encodeURIComponent(opts.metric)}&service=${encodeURIComponent(service)}`
    + `&agg=${opts.agg}`
    + (opts.by ? `&by=${encodeURIComponent(opts.by)}` : '')
    + (opts.unit ? `&unit=${encodeURIComponent(opts.unit)}` : '')
    + (opts.mtype ? `&mtype=${opts.mtype}` : '')
    + `&range=${encodeURIComponent(r || encodeRange(range))}`;
}

const R1: TimeRange = { preset: '1h' };
const RC: TimeRange = { preset: 'custom', fromMs: 1_760_000_000_000, toMs: 1_760_003_600_000 };

describe('metricsHref — Overview linkleri değişmedi', () => {
  const cases: Array<[string, string | null, TimeRange, Parameters<typeof overviewBefore>[3]]> = [
    // Response time (rtExploreHref): histogram tabanı + avg + birim + tür.
    ['checkout', null, R1, { by: 'http.route', metric: 'http_server_request_duration_seconds', agg: 'avg', mtype: 'histogram', unit: 's' }],
    ['checkout', '6h', R1, { by: 'rpc.method', metric: 'rpc_server_duration_milliseconds', agg: 'avg', mtype: 'histogram', unit: 'ms' }],
    ['orders api', null, RC, { by: 'http.route', metric: 'http_server_request_duration_seconds', agg: 'avg', mtype: 'histogram' }],
    // Throughput (tputExploreHref): _count + rate, birimsiz; ad henüz yokken ''.
    ['checkout', null, R1, { by: 'http.route', metric: 'http_server_request_duration_seconds_count', agg: 'rate' }],
    ['payments/v2', 'custom:1-2', RC, { by: 'http.route', metric: '', agg: 'rate' }],
  ];
  it.each(cases)('%s / %s', (service, rp, range, opts) => {
    expect(metricsHref({ service, rangeParam: rp, range }, opts)).toBe(overviewBefore(service, rp, range, opts));
  });

  it('Overview.tsx kapıları paylaşılan üreticiden geçer', () => {
    const src = readFileSync(resolve(__dirname, '../pages/service/Overview.tsx'), 'utf8');
    expect(src).toContain("sharedMetricsHref({ service, rangeParam: searchParams.get('range'), range }, opts)");
    expect(src).not.toMatch(/`\/metrics\?metric=/);
  });
});

describe('metricsHref — dört ders + aralık', () => {
  const ctx = { service: 'checkout', range: R1 };
  it('kırılım taşınır (by), çoklu etiket virgülle', () => {
    expect(metricsHref(ctx, { metric: 'm_count', agg: 'rate', by: ['http_request_method', 'http_route'] }))
      .toBe('/metrics?metric=m_count&service=checkout&agg=rate&by=http_request_method%2Chttp_route&range=1h');
    const imp = importLegacyMetricsUrl(new URLSearchParams('metric=m_count&agg=rate&by=http_request_method%2Chttp_route'));
    expect(imp?.model?.queries[0].by).toEqual(['http_request_method', 'http_route']);
  });
  it('sayaç rate ile, histogram tabanı avg ile (mean) açılır', () => {
    const q = (h: string) => importLegacyMetricsUrl(new URLSearchParams(h.split('?')[1]))!.model!.queries[0];
    const c = q(metricsHref(ctx, { metric: 'http_server_request_duration_seconds_count', agg: 'rate' }));
    expect([c.fn, c.agg]).toEqual(['rate', 'sum']);
    const h = q(metricsHref(ctx, { metric: 'http_server_request_duration_seconds', agg: 'avg', mtype: 'histogram', unit: 's' }));
    expect([h.fn, h.metricKind, h.unit]).toEqual(['mean', 'histogram', 's']);
  });
  it('birim ve tür URL\'de', () => {
    const h = metricsHref(ctx, { metric: 'x', agg: 'p95', unit: 'ms', mtype: 'histogram' });
    expect(h).toContain('&unit=ms');
    expect(h).toContain('&mtype=histogram');
  });
  it('aralık: URL\'deki aynen, yoksa geçerli aralık kodlanır', () => {
    expect(metricsHref({ ...ctx, rangeParam: '24h' }, { metric: 'x', agg: 'rate' })).toMatch(/&range=24h$/);
    expect(metricsHref({ service: 's', range: RC }, { metric: 'x', agg: 'rate' }))
      .toMatch(/&range=custom%3A1760000000000-1760003600000$/);
  });
});

const mxOf = (href: string) => decodeMx(new URLSearchParams(href.split('?')[1]).get(MX_PARAM));

describe('metricsLinkHref', () => {
  const ctx = { service: 'checkout', range: R1, src: 'vm' as const };
  it('süzgeçsiz tek sorgu = biçim 1 (Overview biçimi)', () => {
    const q = { metric: 'm_count', agg: 'rate' as const, by: 'client_id' };
    expect(metricsLinkHref(ctx, { queries: [q] })).toBe(metricsHref(ctx, q));
  });
  it('süzgeçli satır ?mx= olur; servis + süzgeçler + kırılım + kaynak', () => {
    const h = metricsLinkHref(ctx, { queries: [{ metric: 'm_count', agg: 'rate', by: ['a'], filters: [{ k: 'b', op: '=~', v: '5..' }] }] })!;
    expect(h).toMatch(/^\/metrics\?mx=.*&range=1h$/);
    const m = mxOf(h)!;
    expect(m.src).toBe('vm');
    expect(compileMxQuery(m.queries[0])).toBe('sum by (a) (rate(m_count{service_name="checkout", b=~"5.."}[$__rate_interval]))');
  });
  it('formül: gizli satırlar + F1', () => {
    const m = mxOf(metricsLinkHref(ctx, {
      queries: [{ metric: 'a_bytes', agg: 'sum', hidden: true }, { metric: 'b_bytes', agg: 'sum', hidden: true }],
      formula: { expr: '100 * (1 - A / B)', unit: '%' },
    })!)!;
    expect(m.queries.map(q => [q.id, q.hidden])).toEqual([['A', true], ['B', true], ['F1', false]]);
    const f = compileMxFormula(m.queries[2].expr, m.queries);
    expect(f.ok && f.expr).toBe('100 * (1 - (sum(a_bytes{service_name="checkout"})) / (sum(b_bytes{service_name="checkout"})))');
  });
  it('kod kipi: PromQL birebir', () => {
    const m = mxOf(metricsLinkHref(ctx, { code: 'sum by (cls) (x)' })!)!;
    expect([m.mode, m.code, m.src]).toEqual(['code', 'sum by (cls) (x)', 'vm']);
  });
  it('açılamayan tanım null (link takılmaz)', () => {
    expect(metricsLinkHref(ctx, { queries: [] })).toBeNull();
    expect(metricsLinkHref(ctx, { queries: [{ metric: '', agg: 'rate' }] })).toBeNull();
    expect(metricsLinkHref(ctx, { code: '  ' })).toBeNull();
  });
});

describe('parsePromMatchers', () => {
  it('sunucunun ortam eşleyicileri (altın dosya) gidiş-dönüş', () => {
    const golden = JSON.parse(readFileSync(resolve(__dirname, './httpMetrics.envScope.golden.json'), 'utf8')) as { cases: { envSel: string }[] };
    expect(golden.cases.length).toBeGreaterThan(0);
    for (const c of golden.cases) {
      const fs = parsePromMatchers(c.envSel);
      expect(fs, c.envSel).not.toBeNull();
      expect(fs!.map(matcherText).join(', ')).toBe(c.envSel);
    }
  });
  it('çoklu / kaçışlı / boş', () => {
    expect(parsePromMatchers('a="x", b=~"p|q", c!="y\\"z"')).toEqual([
      { k: 'a', op: '=', v: 'x' }, { k: 'b', op: '=~', v: 'p|q' }, { k: 'c', op: '!=', v: 'y"z' }]);
    expect(parsePromMatchers('')).toEqual([]);
    expect(parsePromMatchers('a=x')).toBeNull();
    expect(parsePromMatchers('a="x" b="y"')).toBeNull();
  });
});
