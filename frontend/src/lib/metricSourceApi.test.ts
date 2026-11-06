// metricSourceApi.test.ts — v0.9.1151, deneme modu end-to-end on the client.
//
// metricSource.test.ts proves the helper is CALLED (source scan). This file
// proves it WORKS: the URL that actually leaves api.ts carries the param.
// Both are needed — a wrapper can be present and still produce nothing (an
// import shadowed, a value read before the stub, a qs() that re-encodes the
// string). The scan cannot see that; a real call can.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

/** Captures every URL fetch() is called with, answering 200 {} to all. */
function captureFetch(): string[] {
  const urls: string[] = [];
  vi.stubGlobal('fetch', (url: unknown) => {
    urls.push(String(url));
    return Promise.resolve(new Response('{}', {
      status: 200, headers: { 'content-type': 'application/json' },
    }));
  });
  return urls;
}

function pinPage(search: string) {
  vi.stubGlobal('window', { location: { search } });
}

// Every metric method behind the backend source seam. Named individually so
// a failure says WHICH surface would read the wrong store.
const seamCalls: Array<[string, () => Promise<unknown>]> = [
  ['metricNamesSearch (picker)', () => api.metricNamesSearch('cart', 'jvm', 50, 0)],
  ['metricNames (legacy catalogue)', () => api.metricNames('cart')],
  ['metricNames (no service)', () => api.metricNames('')],
  ['metricQuery', () => api.metricQuery({ name: 'jvm.memory.used', agg: 'avg', from: 1, to: 2 })],
  ['metricQueryFull', () => api.metricQueryFull({ name: 'jvm.memory.used', agg: 'avg', from: 1, to: 2 })],
  ['metricLabels', () => api.metricLabels('jvm.memory.used', 'pod')],
  ['metricAttrKeys', () => api.metricAttrKeys('jvm.memory.used')],
  ['dashboardData (bundle POST)', () => api.dashboardData({
    from: 1, to: 2, requests: [{ id: 'p1', type: 'metric', name: 'm' }],
  })],
  // v0.9.1157 — VM Faz 2 brought these two through the seam. They moved
  // here FROM the "outside the seam" test below, which is the whole change:
  // the inverse assertion held them out for three releases on purpose.
  ['metricHistogram (heatmap)', () => api.metricHistogram({
    name: 'http.server.duration', from: 1, to: 2,
  })],
  ['metricPromql (raw query)', () => api.metricPromql({ query: 'up', from: 1, to: 2 })],
];

describe('api.ts forwards ?metricsrc= (v0.9.1151)', () => {
  afterEach(() => vi.unstubAllGlobals());

  it.each(seamCalls)('%s carries the trial param', async (_name, call) => {
    pinPage('?range=30m&metricsrc=vm');
    const urls = captureFetch();
    await call();
    expect(urls).toHaveLength(1);
    expect(urls[0]).toContain('metricsrc=vm');
    // Exactly once — Go's Query().Get() takes the first of a repeated
    // param, so a double stamp is a coin flip, not an error.
    expect(urls[0].match(/metricsrc=/g)).toHaveLength(1);
  });

  it.each(seamCalls)('%s honours the ch escape hatch', async (_name, call) => {
    pinPage('?metricsrc=ch');
    const urls = captureFetch();
    await call();
    expect(urls[0]).toContain('metricsrc=ch');
  });

  // THE regression pin. Without a param the wire format must be exactly
  // what v0.9.1150 sent — this is the state every page is in, every day.
  it.each(seamCalls)('%s is untouched without the param', async (_name, call) => {
    pinPage('?range=30m&service=cart');
    const urls = captureFetch();
    await call();
    expect(urls[0]).not.toContain('metricsrc');
  });

  it('an unrecognised param value is not forwarded — the page keeps working', async () => {
    // Client-side the value is dropped, so a typo reads the DEFAULT backend
    // rather than 400-ing every panel on the page. The server still rejects
    // a typo it receives directly (someone hand-editing an API URL); this is
    // only about what the UI chooses to send.
    pinPage('?metricsrc=victoria');
    const urls = captureFetch();
    await api.metricQuery({ name: 'm', from: 1, to: 2 });
    expect(urls[0]).not.toContain('metricsrc');
  });

  // v0.10.1867 — 'does NOT stamp endpoints outside the seam' düştü: seam
  // dışındaki son uç (/api/metrics, ham metric_points) istemcisi eski Explore
  // Metrics paneliyle birlikte silindi.

  it('survives a windowless environment', async () => {
    // api.ts is imported in node-env tests; reading location unguarded
    // would throw before any request was made.
    vi.stubGlobal('window', undefined);
    const urls = captureFetch();
    await api.metricQuery({ name: 'm', from: 1, to: 2 });
    expect(urls[0]).not.toContain('metricsrc');
  });
});

// v0.10.1309 — PromQL editörünün ?src=vm|ch seçimi metadata isteklerine de
// iner (öneri adları / anahtar / değer sorgunun koştuğu depodan). Açık src
// sayfanın ?metricsrc= denemesinden ÖNCE gelir — metricPromql'deki gibi —
// ve parametre tek kez basılır; src verilmeyen çağrı yukarıdaki üç kuralla
// aynı (sayfa param'ı / hiçbiri).
const explicitSrcCalls: Array<[string, (src: 'vm' | 'ch') => Promise<unknown>]> = [
  ['metricNamesSearch', src => api.metricNamesSearch('', 'jvm', 20, 0, src)],
  ['metricLabels', src => api.metricLabels('jvm.memory.used', 'pod', '24h', '', 200, src)],
  ['metricAttrKeys', src => api.metricAttrKeys('jvm.memory.used', '', '24h', src)],
];

describe('metadata methods take an explicit src (v0.10.1309)', () => {
  afterEach(() => vi.unstubAllGlobals());

  it.each(explicitSrcCalls)('%s stamps the explicit src on a param-less page', async (_name, call) => {
    pinPage('?editor=1&tab=promql&src=ch');
    const urls = captureFetch();
    await call('ch');
    expect(urls).toHaveLength(1);
    expect(urls[0]).toContain('metricsrc=ch');
    expect(urls[0].match(/metricsrc=/g)).toHaveLength(1);
  });

  it.each(explicitSrcCalls)('%s: explicit src wins over the page trial param', async (_name, call) => {
    pinPage('?metricsrc=vm');
    const urls = captureFetch();
    await call('ch');
    expect(urls[0]).toContain('metricsrc=ch');
    expect(urls[0].match(/metricsrc=/g)).toHaveLength(1);
  });
});
