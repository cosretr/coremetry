// @vitest-environment jsdom
//
// listMetricMode.test.tsx — v0.10.1359, Operator-reported: /endpoints LİSTESİ
// "Kaynak: metrik" kipinde /api/endpoints/metric okuyordu ve sayılar
// Service → HTTP Metrics sekmesiyle tutmuyordu. Karar (operatör, nihai):
// liste sekmenin AYNI PromQL'i (listQueries → /api/metrics/promql?metricsrc=vm),
// servis ZORUNLU, satır = (method, route), detay linki ?method= taşır, şekil
// gruplaması metrik kipinde devre dışı. GERÇEK sayfa monte edilir (yalnız
// `@/lib/api` sahte — metricMode.test.tsx emsali).
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { removeRaw, STORAGE_KEYS } from '@/lib/storage';
import type { SpanMetricSeries } from '@/lib/types';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Page from '../Endpoints';

type PromCall = { query: string; instant?: '1' };
const m = vi.hoisted(() => ({
  prom: [] as PromCall[], endpoints: 0, vmDown: false,
  // v0.10.1377 — ortam kapsamı sahtesi: gate çözülene dek cevap gelmez.
  scope: null as unknown, scopeCalls: [] as unknown[], gate: Promise.resolve() as Promise<void>,
}));

const SVC = 'checkout';
const ser = (labels: Record<string, string>, v: number): SpanMetricSeries => ({
  groupKey: Object.entries(labels).map(([k, x]) => `${k}="${x}"`),
  points: [{ time: 1e9, value: v }],
});

vi.mock('@/lib/api', () => {
  const MR = (method: string, extra: Record<string, string> = {}) => ({ http_request_method: method, http_route: '/orders', ...extra });
  // İfadenin ŞEKLİNDEN cevap: aynı route iki method (GET 1200, POST 300).
  const answer = (q: string): SpanMetricSeries[] => {
    const two = (g: number, p: number) => [ser(MR('GET'), g), ser(MR('POST'), p)];
    if (q.startsWith('topk(')) return two(1200, 300);
    if (q.includes('label_replace(')) return [ser(MR('GET', { cls: '2xx' }), 1188), ser(MR('GET', { cls: '5xx' }), 12), ser(MR('POST', { cls: '5xx' }), 30)];
    if (q.startsWith('(histogram_quantile(0.5,')) return two(0.007, 0.1);
    if (q.startsWith('(histogram_quantile(0.95,')) return two(0.2, 0.5);
    if (q.startsWith('(histogram_quantile(0.99,')) return two(0.4, 0.9);
    if (q.includes('_sum{')) return two(0.05, 0.3);
    if (q.includes('=~"5.."')) return two(12, 30);
    return [];
  };
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    endpoints: () => { m.endpoints++; return Promise.resolve({ rows: [], pool: 0, poolCapped: false }); },
    httpMetricsPromql: (p: unknown) => {
      const c = p as PromCall;
      m.prom.push(c);
      if (m.vmDown) return Promise.reject(new Error('bad request: metricsrc=vm — VictoriaMetrics yapılandırılmamış (Settings → Metrik backend\'i: bir base URL girin)'));
      return Promise.resolve(answer(c.query));
    },
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    clusters: () => Promise.resolve([]),
    getPreference: (k: unknown) => Promise.resolve({ key: k, model: null }),
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

async function mount(url: string) {
  m.prom = []; m.endpoints = 0;
  window.history.replaceState(null, '', url);
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
  m.vmDown = false;
  document.body.innerHTML = '';
  // v0.10.1377 — useUrlEnv env'i depoya yansıtır; sonraki teste sızmasın.
  removeRaw(STORAGE_KEYS.env);
  m.scope = null; m.scopeCalls = []; m.gate = Promise.resolve();
});

const dataRows = () => [...host!.querySelectorAll('tbody tr')].filter(tr => tr.querySelectorAll('td').length > 1);
const shapeBox = () => [...host!.querySelectorAll('label')]
  .find(l => l.textContent?.includes('Group by shape'))!;

describe('/endpoints metrik kipi = HTTP Metrics PromQL (v0.10.1359)', () => {
  it('servis yok: "Metrik kipinde servis seçin", hiçbir sorgu atılmaz', async () => {
    await mount('/endpoints?src=metric&range=3h');
    expect(host!.textContent).toContain('Metrik kipinde servis seçin');
    expect(m.prom).toHaveLength(0);
    expect(m.endpoints).toBe(0);
  });

  it('servisle: satırlar PromQL\'den (method + route), /api/endpoints okunmaz', async () => {
    await mount(`/endpoints?src=metric&range=3h&service=${SVC}&compare=0`);
    expect(m.endpoints).toBe(0);
    expect(m.prom.length).toBe(7);
    for (const c of m.prom) {
      expect(c.instant).toBe('1');
      expect(c.query).toContain(`{service_name="${SVC}"`);
      expect(c.query).toContain('[10800s]');
      // limit varsayılanı 100 → N = min(100, 300).
      expect(c.query).toContain('topk(100, sum by (http_request_method, http_route) (increase(');
    }
    const rows = dataRows();
    expect(rows).toHaveLength(2);
    // İstemci sıralaması: Calls ↓ → GET (1200) önce.
    expect(rows[0].textContent).toContain('GET');
    expect(rows[1].textContent).toContain('POST');
    expect(rows[1].textContent).toContain('10.00%'); // 30 / 300
    expect(host!.textContent).toContain('aynı');
    expect(host!.textContent).toContain('increase[W]');
  });

  it('compare (varsayılan açık): önceki pencere offset W ile', async () => {
    await mount(`/endpoints?src=metric&range=3h&service=${SVC}`);
    expect(m.prom.length).toBe(11);
    expect(m.prom.filter(c => c.query.includes('[10800s] offset 10800s'))).toHaveLength(4);
  });

  it('satır tıkı detaya ?method= ile gider', async () => {
    await mount(`/endpoints?src=metric&range=3h&service=${SVC}&compare=0`);
    const post = dataRows().find(tr => tr.textContent?.includes('POST'))!;
    await act(async () => { (post.querySelector('td.num') as HTMLElement).click(); });
    expect(window.location.pathname).toBe('/endpoint');
    const p = new URLSearchParams(window.location.search);
    expect(p.get('service')).toBe(SVC);
    expect(p.get('path')).toBe('/orders');
    expect(p.get('method')).toBe('POST');
    expect(p.get('sig')).toBeNull();
  });

  it('şekil gruplaması metrik kipinde devre dışı (gerekçe title\'da); URL shape=1 sorguyu değiştirmez', async () => {
    await mount(`/endpoints?src=metric&range=3h&service=${SVC}&shape=1`);
    const lab = shapeBox();
    expect((lab.querySelector('input') as HTMLInputElement).disabled).toBe(true);
    expect((lab.querySelector('input') as HTMLInputElement).checked).toBe(false);
    expect(lab.getAttribute('title')).toContain('Metrik kipinde şekle göre gruplama yok');
  });

  it('span kipinde şekil kutusu etkin', async () => {
    await mount('/endpoints?range=3h');
    expect((shapeBox().querySelector('input') as HTMLInputElement).disabled).toBe(false);
    expect(m.prom).toHaveLength(0);
  });

  it('VM yapılandırılmamış: sunucunun 400 cümlesi tablo hata satırında', async () => {
    m.vmDown = true;
    await mount(`/endpoints?src=metric&range=3h&service=${SVC}`);
    expect(host!.textContent).toContain('VictoriaMetrics yapılandırılmamış');
    expect(dataRows()).toHaveLength(0);
  });
});

// v0.10.1377 — liste metrik kipi env süzgeci VM PromQL'inde (adlar SENTETİK).
describe('/endpoints metrik kipi: env süzgeci (v0.10.1377)', () => {
  const ENV2 = 'deployment_environment_name=~"dr-eu-a|prod-eu-a"';

  it('çözüm gelene dek sorgu YOK; sonra her sorgu eşleyiciyle; not uygulandı der', async () => {
    let release!: () => void;
    m.gate = new Promise<void>(r => { release = r; });
    m.scope = {
      label: 'deployment_environment_name', source: 'default', applied: true, matcher: ENV2,
      terms: [{ role: 'env', want: 'eu-a', label: 'deployment_environment_name', values: ['dr-eu-a', 'prod-eu-a'], tier: 'prefix' }],
    };
    await mount(`/endpoints?src=metric&range=3h&service=${SVC}&compare=0&env=eu-a`);
    expect(m.scopeCalls).toHaveLength(1);
    expect(m.scopeCalls[0]).toMatchObject({ env: 'eu-a' });
    expect(m.prom).toHaveLength(0);
    await act(async () => { release(); });
    await settle();
    await settle();
    expect(m.prom.length).toBe(7);
    for (const c of m.prom) expect(c.query).toContain(`{service_name="${SVC}", ${ENV2}`);
    expect(host!.textContent).toContain('Ortam süzgeci metrikte uygulandı: deployment_environment_name=dr-eu-a|prod-eu-a (env eu-a → dr-eu-a | prod-eu-a, önek eşleşmesi)');
    expect(host!.textContent).not.toContain('süzgeci bu tabloya uygulanmaz');
  });

  it('aile ucu hata verirse süzgeçsiz sorgular + çözülemedi notu', async () => {
    m.gate = Promise.reject(new Error('HTTP 502: VictoriaMetrics erişilemiyor'));
    m.gate.catch(() => {});
    await mount(`/endpoints?src=metric&range=3h&service=${SVC}&compare=0&env=eu-a`);
    await settle();
    expect(m.prom.length).toBe(7);
    for (const c of m.prom) expect(c.query).toContain(`{service_name="${SVC}"`);
    for (const c of m.prom) expect(c.query).not.toContain('deployment_environment_name');
    expect(host!.textContent).toContain('Ortam süzgeci çözülemedi (HTTP 502: VictoriaMetrics erişilemiyor) — süzgeç uygulanmadı, seriler TÜM ortamları kapsıyor');
  });
});
