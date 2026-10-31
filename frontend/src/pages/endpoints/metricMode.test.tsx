// @vitest-environment jsdom
//
// metricMode.test.tsx — v0.10.1352, Operator-reported (prod):
// /endpoint?service=bsa-loan-assessment-allocation-prod&path=/BSAWEB/decision/
// service/rest&range=3h "Kaynak: metrik (vm)" — Calls/requests ve gecikme
// grafikleri (p95 seçiliyken de) Service → HTTP Metrics sekmesindeki aynı
// route ile tutmuyordu; sayfa /api/endpoints/metric'in kendi adım-başı
// yüzdeliklerini ve çağrı-ağırlıklı karo ortalamalarını çiziyordu.
// Karar: sekmenin AYNI PromQL'i (/api/metrics/promql?metricsrc=vm) route'a
// süzülü. GERÇEK sayfa monte edilir (yalnız `@/lib/api` sahte, grafik
// bileşeni lejant basan bir taslak).
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { removeRaw, STORAGE_KEYS } from '@/lib/storage';
import { fmtSmart } from '@/lib/chartFmt';
import type { EndpointRow, SpanMetricSeries } from '@/lib/types';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Page from '../EndpointDetail';

type PromCall = { query: string; from: number; to: number; maxDataPoints: number; instant?: '1' };
const m = vi.hoisted(() => ({
  prom: [] as PromCall[], endpoints: 0,
  // v0.10.1377 — ortam kapsamı sahtesi: gate çözülene dek cevap gelmez.
  scope: null as unknown, scopeCalls: [] as unknown[], gate: Promise.resolve() as Promise<void>,
}));

const SVC = 'bsa-loan-assessment-allocation-prod';
const ROUTE = '/BSAWEB/decision/service/rest';
const one = (v: number, labels: Record<string, string> = {}): SpanMetricSeries[] => [{
  groupKey: Object.entries(labels).map(([k, x]) => `${k}="${x}"`),
  points: [{ time: 1e9, value: v }],
}];

vi.mock('@/lib/api', () => {
  const row: EndpointRow = {
    service: 'bsa-loan-assessment-allocation-prod', path: '/BSAWEB/decision/service/rest',
    calls: 999, errors: 9, errorRate: 0.9, avgMs: 99, p99Ms: 999, p50Ms: 9, p95Ms: 99, reqPerMin: 9,
  };
  // Pencere sorgularına sabit değer (ifadenin ŞEKLİNDEN): sayfa karoları
  // bunlardan doldurmalı, satırdan (999…) DEĞİL.
  const answer = (q: string): SpanMetricSeries[] => {
    if (!q.includes('[10800s]')) {
      if (q.startsWith('sum by (http_request_method, http_route) (rate(')) return one(0.11, { http_request_method: 'POST', http_route: '/BSAWEB/decision/service/rest' });
      return [];
    }
    if (q.startsWith('sum by (http_response_status_code)')) return one(1188, { http_response_status_code: '200' });
    if (q.startsWith('histogram_quantile(0.5,')) return one(0.007);
    if (q.startsWith('histogram_quantile(0.95,')) return one(0.2);
    if (q.startsWith('histogram_quantile(0.99,')) return one(0.4);
    if (q.includes('_sum{')) return one(0.05);
    if (q.includes('=~"5.."')) return one(12);
    return one(1200);
  };
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    endpoints: () => { m.endpoints++; return Promise.resolve({ rows: [row], pool: 1, poolCapped: false }); },
    httpMetricsPromql: (p: unknown) => { const c = p as PromCall; m.prom.push(c); return Promise.resolve(answer(c.query)); },
    savedViews: () => Promise.resolve([]),
    endpointDetail: () => Promise.resolve(null),
    // v0.10.1429 — aile çözümü sahtesi (useRedFamily): gate çözülene dek cevap gelmez; HTTP ailesi + ortam kapsamı m.scope.
    serviceRedFamily: (svc: unknown, p: unknown) => { m.scopeCalls.push(p); return m.gate.then(() => ({ service: svc, state: 'known', primary: 'http', http: { id: 'http', kind: 'http', ...(m.scope ? { envScope: m.scope } : {}) }, tried: [] })); },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'viewer' }, loading: false }),
}));
vi.mock('@/components/MultiLineChart', () => ({
  MultiLineChart: ({ series }: { series: SpanMetricSeries[] }) => (
    <div data-testid="mlc">{series.map(s => s.groupKey[0]).join('|')}</div>
  ),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(extra = '') {
  m.prom = []; m.endpoints = 0;
  window.history.replaceState(null, '', `/endpoint?service=${SVC}&path=${encodeURIComponent(ROUTE)}&range=3h${extra}`);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><ConfirmProvider><Page /></ConfirmProvider></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  await settle(); // v0.10.1429 — aile çözümü (serviceRedFamily) bir tur daha: PromQL sorguları ondan sonra atılır.
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
  // v0.10.1377 — useUrlEnv env'i depoya yansıtır; sonraki teste sızmasın.
  removeRaw(STORAGE_KEYS.env);
  m.scope = null; m.scopeCalls = []; m.gate = Promise.resolve();
});

// StatTile: çerçeve > [etiket, değer]; etiket metni bileşene verilen ad.
const tile = (label: string) => {
  const el = [...host!.querySelectorAll('div')]
    .find(e => e.children.length === 2 && e.children[0].textContent === label);
  return el?.children[1].textContent ?? '';
};

describe('/endpoint metrik kipi = HTTP Metrics sekmesinin sorguları (v0.10.1352)', () => {
  it('her sorgu VM PromQL yolundan, servis + route süzgeçli; /api/endpoints/metric okunmaz', async () => {
    await mount();
    expect(m.endpoints).toBe(0);
    expect(m.prom.length).toBeGreaterThanOrEqual(15);
    for (const c of m.prom) {
      expect(c.query).toContain(`{service_name="${SVC}", http_route="${ROUTE}"`);
      expect(c.maxDataPoints).toBe(300);
    }
    // Sekmenin adım/pencere mantığı: 3h → 144 s rate, W = 10800 s.
    expect(m.prom.some(c => c.query.includes('[144s]'))).toBe(true);
    const inst = m.prom.filter(c => c.instant === '1').map(c => c.query);
    expect(inst).toContain(`sum(increase(http_server_request_duration_seconds_count{service_name="${SVC}", http_route="${ROUTE}"}[10800s]))`);
    expect(inst).toContain(`histogram_quantile(0.95, sum by (le) (increase(http_server_request_duration_seconds_bucket{service_name="${SVC}", http_route="${ROUTE}"}[10800s])))`);
  });

  // v0.10.1353 (operatör: "Default p50 olsun") — varsayılan p50; p95 ?lat=p95 ile.
  it('varsayılan gecikme yüzdeliği p50', async () => {
    await mount();
    expect(m.prom.some(c => c.query.startsWith('histogram_quantile(0.5, sum by (le, http_request_method, http_route) (rate('))).toBe(true);
    expect(host!.textContent).toContain('P50 gecikme (route)');
  });

  it('?lat=p95: route paneli sekmenin p95ByRoute\'u', async () => {
    await mount('&lat=p95');
    expect(m.prom.map(c => c.query)).toContain(
      `histogram_quantile(0.95, sum by (le, http_request_method, http_route) (rate(http_server_request_duration_seconds_bucket{service_name="${SVC}", http_route="${ROUTE}"}[144s])))`);
    expect(host!.textContent).toContain('P95 gecikme (route)');
    expect(host!.textContent).toContain('POST /BSAWEB/decision/service/rest');
  });

  it('?lat=p99 seçimi route panelini p99\'a çevirir', async () => {
    await mount('&lat=p99');
    expect(host!.textContent).toContain('P99 gecikme (route)');
    expect(m.prom.some(c => c.query.startsWith('histogram_quantile(0.99, sum by (le, http_request_method, http_route) (rate('))).toBe(true);
  });

  it('karolar pencere sorgularından (satırdan değil)', async () => {
    await mount();
    expect(tile('Calls')).toContain((1200).toLocaleString('tr-TR'));
    expect(tile('Errors')).toContain('12');
    expect(tile('Err rate')).toContain('%1.00');
    expect(tile('Req/min')).toContain('6,7');
    expect(tile('Avg')).toContain(fmtSmart(0.05, 's'));
    expect(tile('P95')).toContain(fmtSmart(0.2, 's'));
    expect(tile('P99')).toContain(fmtSmart(0.4, 's'));
    expect(host!.textContent).not.toContain('999');
    expect(host!.textContent).toContain('Tüm RED karoları ve grafikler: VictoriaMetrics');
    expect(host!.textContent).not.toContain('tdigest');
  });

  it('compare: önceki pencere aynı sorgular + offset', async () => {
    await mount('&compare=1');
    expect(m.prom.some(c => c.query.includes('[10800s] offset 10800s'))).toBe(true);
  });

  it('span kipi değişmedi: PromQL atılmaz, satır okunur, tdigest notu durur', async () => {
    await mount('&src=span');
    expect(m.prom).toHaveLength(0);
    expect(m.endpoints).toBeGreaterThan(0);
    expect(host!.textContent).toContain('tdigest');
  });
});

describe('api.httpMetricsPromql yolu (v0.10.1352)', () => {
  it('/api/metrics/promql + metricsrc=vm', async () => {
    const { api } = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
    const urls: string[] = [];
    const orig = globalThis.fetch;
    globalThis.fetch = (async (u: RequestInfo | URL) => {
      urls.push(String(u));
      return new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } });
    }) as typeof fetch;
    try {
      await api.httpMetricsPromql({ query: 'up{http_route="/a"}', from: 1, to: 2, maxDataPoints: 300 });
    } finally { globalThis.fetch = orig; }
    expect(urls[0]).toContain('/api/metrics/promql?');
    expect(urls[0]).toContain('metricsrc=vm');
    expect(new URL(urls[0], 'http://x').searchParams.get('query')).toBe('up{http_route="/a"}');
  });
});

// v0.10.1377 — metrik kipi env/cluster süzgeci VM PromQL'inde (adlar SENTETİK).
describe('/endpoint metrik kipi: env/cluster süzgeci (v0.10.1377)', () => {
  const ENV = 'deployment_environment_name="prod-eu-a"';
  const applied = {
    label: 'deployment_environment_name', source: 'default', applied: true, matcher: ENV,
    terms: [
      { role: 'env', want: 'eu-a', label: 'deployment_environment_name', values: ['prod-eu-a'], tier: 'prefix' },
      { role: 'cluster', want: 'prod-eu-a', label: 'deployment_environment_name', values: ['prod-eu-a'], tier: 'exact' },
    ],
  };

  it('çözüm gelene dek sorgu YOK; sonra her sorgu eşleyiciyle, bant uygulandı der', async () => {
    let release!: () => void;
    m.gate = new Promise<void>(r => { release = r; });
    m.scope = applied;
    await mount('&env=eu-a&cluster=prod-eu-a');
    expect(m.scopeCalls).toHaveLength(1);
    expect(m.scopeCalls[0]).toMatchObject({ env: 'eu-a', cluster: 'prod-eu-a' });
    expect(m.prom).toHaveLength(0); // süzgeçsiz an yok
    expect(host!.textContent).toContain('Ortam süzgeci çözülüyor…');
    await act(async () => { release(); });
    await settle();
    expect(m.prom.length).toBeGreaterThanOrEqual(15);
    for (const c of m.prom) expect(c.query).toContain(`{service_name="${SVC}", ${ENV}, http_route="${ROUTE}"`);
    expect(host!.textContent).toContain('Ortam süzgeci metrikte uygulandı: deployment_environment_name=prod-eu-a (env eu-a → prod-eu-a, önek eşleşmesi; cluster prod-eu-a, birebir)');
    expect(host!.textContent).not.toContain('uygulanmaz');
  });

  it('metrikte ortam yok: süzgeçsiz sorgular + TÜM ortamlar bandı', async () => {
    m.scope = {
      label: 'deployment_environment_name', source: 'default', applied: false,
      reason: 'metrikte bu ortam yok (deployment_environment_name değerleri arasında "us-x" eşleşmedi)',
      terms: [{ role: 'env', want: 'us-x', label: 'deployment_environment_name', tier: 'none' }],
    };
    await mount('&env=us-x');
    expect(m.prom.length).toBeGreaterThanOrEqual(15);
    for (const c of m.prom) expect(c.query).toContain(`{service_name="${SVC}", http_route="${ROUTE}"`);
    expect(host!.textContent).toContain('Ortam süzgeci uygulanamadı (metrikte bu ortam yok: deployment_environment_name değerleri arasında "us-x" eşleşmedi) — seriler TÜM ortamları kapsıyor');
  });

  it('env yok: aile çözümü env\'siz sorulur (v0.10.1429), sorgular bayt bayt eski', async () => {
    await mount();
    expect(m.scopeCalls).toHaveLength(1);
    expect(m.scopeCalls[0]).toMatchObject({ env: undefined, cluster: undefined });
    expect(m.prom.length).toBeGreaterThanOrEqual(15);
    for (const c of m.prom) expect(c.query).toContain(`{service_name="${SVC}", http_route="${ROUTE}"`);
  });
});

// v0.10.1429 — api.httpEnvScope kaldırıldı (aile + ortam tek istekte: api.serviceRedFamily; yol redFamily.test.ts'te).
