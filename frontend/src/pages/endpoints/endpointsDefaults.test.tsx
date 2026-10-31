// @vitest-environment jsdom
//
// endpointsDefaults.test.tsx — v0.10.1348 (operatör isteği, prod /endpoints
// ekran görüntüsü, servis bsa-loan-assessment-allocation-prod):
//
//  1. "Endpoints sayfasında P99 ve P99 delta default kapalı gelsin." —
//     varsayılan görünür kolonlar P99 / P99 Δ içermez; Columns seçicisi
//     ikisini de sunar.
//  2. Varsayılan sıra P99 Δ ↓ → Calls ↓ (gizli kolona göre sıralı açılan
//     tablo okunmaz); başlık notu "Top N by calls", aday havuzu notu yok.
//  3. Kalıcı tercih anahtarı v2 (`endpoints-v2`): eski `dt.endpoints.sort`
//     ve sunucu tercihi `endpoints` YOK SAYILIR, yenisi uygulanır, URL
//     `s_endpoints` hepsini yener; bilinmeyen kimlik varsayılana düşer.
//  4. "Method kısmı daha net gözüksün" — kök neden başlık/gövde kayması:
//     servis süzgecinde başlık Service'i düşürüyor, gövde basmaya devam
//     ediyordu (Path başlığının altında servis adı). Her satırda <th> sayısı
//     = <td> sayısı ve kolonlar aynı sırada; Method nötr mono rozet.
//
// GERÇEK sayfa monte edilir (yalnız `@/lib/api` sahte — shapePivot emsali).
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { EndpointRow } from '@/lib/types';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Page from '../Endpoints';

const m = vi.hoisted(() => ({
  calls: [] as Array<Record<string, unknown>>,
  prefs: {} as Record<string, unknown>,
  prefKeys: [] as string[],
}));

vi.mock('@/lib/api', () => {
  const row: EndpointRow = {
    service: 'bsa-loan-assessment-allocation-prod', path: '/BSAWeb/api/allocate', method: 'POST',
    calls: 1400, errors: 0, errorRate: 0, avgMs: 14, p99Ms: 812,
    p50Ms: 10, p90Ms: 40, p95Ms: 60, reqPerMin: 47,
  };
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    endpoints: (p: unknown) => {
      m.calls.push(p as Record<string, unknown>);
      return Promise.resolve({ rows: [row], pool: 500, poolCapped: false });
    },
    getPreference: (k: unknown) => {
      m.prefKeys.push(k as string);
      return Promise.resolve({ key: k, model: m.prefs[k as string] ?? null });
    },
    putPreference: () => Promise.resolve({ ok: true }),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    clusters: () => Promise.resolve([]),
    // v0.10.1429 — aile çözümü: HTTP (metrik kipi sorguları buna bakar).
    serviceRedFamily: (svc: unknown) => Promise.resolve({ service: svc, state: 'known', primary: 'http', http: { id: 'http', kind: 'http' }, tried: [] }),
    // v0.10.1359 — metrik kipi satırları PromQL'den (sekmenin sorguları):
    // aynı satır (POST /BSAWeb/api/allocate, 1400 çağrı, p50 10 ms).
    httpMetricsPromql: (p: unknown) => {
      const q = (p as { query: string }).query;
      const lab = ['http_request_method="POST"', 'http_route="/BSAWeb/api/allocate"'];
      const one = (v: number, extra: string[] = []) => Promise.resolve([{ groupKey: [...lab, ...extra], points: [{ time: 1e9, value: v }] }]);
      if (q.startsWith('topk(')) return one(1400);
      if (q.includes('label_replace(')) return one(1400, ['cls="2xx"']);
      if (q.startsWith('(histogram_quantile(0.5,')) return one(0.01);
      if (q.startsWith('(histogram_quantile(')) return one(0.06);
      if (q.includes('_sum{')) return one(0.014);
      if (q.includes('=~"5.."')) return one(0);
      return one(1400);
    },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
// jsdom'un localStorage'ı bu pakette KISMİ — depo modülü bellek içi haritayla
// mock'lanır (sortIdValidation.test.tsx emsali); DataTable aynı modülü okur.
const mem = vi.hoisted(() => new Map<string, string>());
vi.mock('@/lib/storage', async (importOriginal) => {
  const orig = await importOriginal<typeof import('@/lib/storage')>();
  return {
    ...orig,
    getRaw: (k: string) => mem.get(k) ?? null,
    setRaw: (k: string, v: string) => { mem.set(k, v); },
    removeRaw: (k: string) => { mem.delete(k); },
    getItem: <T,>(k: string, fb: T): T => {
      const r = mem.get(k);
      if (r == null) return fb;
      try { return JSON.parse(r) as T; } catch { return fb; }
    },
    setItem: <T,>(k: string, v: T) => { mem.set(k, JSON.stringify(v)); },
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

beforeEach(() => {
  m.calls = []; m.prefs = {}; m.prefKeys = [];
  mem.clear();
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

// Başlık metni: sıralama oku ve son başlıktaki ⋯ menüsü (S8) ayıklanır.
const thLabel = (th: Element) => th.textContent?.replace(/[▲▼↕⋯]/g, '').trim() ?? '';
const headLabels = () => [...host!.querySelectorAll('thead tr th')].map(thLabel);
const lastFetch = () => m.calls[m.calls.length - 1];
const dataRows = () => [...host!.querySelectorAll('tbody tr')].filter(tr => tr.querySelectorAll('td').length > 1);

describe('/endpoints varsayılan kolonlar + sıra (v0.10.1348)', () => {
  it('P99 ve P99 Δ varsayılan gizli; sıra Calls ↓; not "by calls", havuz notu yok', async () => {
    await mount('/endpoints?range=1h');
    const labels = headLabels();
    expect(labels).not.toContain('P99');
    expect(labels).not.toContain('P99 Δ');
    for (const l of ['Service', 'Path', 'Method', 'Calls', 'Error rate', 'Status', 'P50', 'P95', 'Traces']) {
      expect(labels).toContain(l);
    }
    expect(lastFetch()).toMatchObject({ sort: 'calls', dir: 'desc' });
    expect(host!.textContent).toContain('Top 1 by calls');
    expect(host!.textContent).not.toContain('aday havuzu');
    const callsTh = [...host!.querySelectorAll('thead th')].find(th => th.textContent?.includes('Calls'))!;
    expect(callsTh.getAttribute('aria-sort')).toBe('descending');
  });

  it('Columns seçicisi P99 ve P99 Δ\'yı (işaretsiz) sunar; açınca kolon gelir', async () => {
    await mount('/endpoints?range=1h');
    const btn = [...host!.querySelectorAll('button')].find(b => b.textContent?.includes('Columns'))!;
    expect(btn.textContent).toContain('(7 hidden)');
    await act(async () => { btn.click(); });
    const box = (label: string) => [...host!.querySelectorAll('label')]
      .find(l => l.textContent?.trim() === label)?.querySelector('input') as HTMLInputElement | undefined;
    expect(box('P99')?.checked).toBe(false);
    expect(box('P99 Δ')?.checked).toBe(false);
    await act(async () => { box('P99')!.click(); });
    await settle();
    expect(headLabels()).toContain('P99');
    expect(new URLSearchParams(window.location.search).get('cols')).toContain('p99Ms');
  });

  it('"Worst by impact" davranışı değişmedi', async () => {
    await mount('/endpoints?range=1h');
    const btn = [...host!.querySelectorAll('button')].find(b => b.textContent?.includes('Worst by impact'))!;
    await act(async () => { btn.click(); });
    await settle();
    expect(lastFetch()).toMatchObject({ sort: 'impact', dir: 'desc' });
  });
});

describe('/endpoints kalıcı tercih anahtarı v2 (v0.10.1348)', () => {
  it('eski anahtarlar (dt.endpoints.sort, sunucu "endpoints") yok sayılır', async () => {
    mem.set('dt.endpoints.sort', JSON.stringify({ id: 'p99Delta', dir: 'desc' }));
    m.prefs['endpoints'] = { v: 1, order: [], hidden: [], sig: 'endpoints' };
    await mount('/endpoints?range=1h');
    expect(lastFetch()).toMatchObject({ sort: 'calls', dir: 'desc' });
    expect(m.prefKeys).toContain('endpoints-v2');
    expect(m.prefKeys).not.toContain('endpoints');
    expect(headLabels()).not.toContain('P99');
  });

  it('yeni anahtarlar uygulanır (sıra + sunucu kolon kümesi)', async () => {
    mem.set('dt.endpoints-v2.sort', JSON.stringify({ id: 'errorRate', dir: 'desc' }));
    m.prefs['endpoints-v2'] = { v: 1, order: [], hidden: ['avgMs', 'p90Ms', 'spread', 'errors', 'reqPerMin', 'p99Delta'], sig: 'endpoints-v2' };
    await mount('/endpoints?range=1h');
    await settle();
    expect(lastFetch()).toMatchObject({ sort: 'errorRate', dir: 'desc' });
    expect(headLabels()).toContain('P99');
    expect(headLabels()).not.toContain('P99 Δ');
  });

  it('URL s_endpoints kayıtlı tercihi yener; paramın adı değişmedi', async () => {
    mem.set('dt.endpoints-v2.sort', JSON.stringify({ id: 'errorRate', dir: 'desc' }));
    await mount('/endpoints?range=1h&s_endpoints=p99Delta.desc');
    expect(lastFetch()).toMatchObject({ sort: 'p99Delta', dir: 'desc' });
    expect(host!.textContent).toContain('aday havuzu');
  });

  it('bilinmeyen URL sıralama kimliği varsayılana (Calls ↓) düşer', async () => {
    await mount('/endpoints?range=1h&s_endpoints=bogus.asc');
    expect(lastFetch()).toMatchObject({ sort: 'calls', dir: 'desc' });
  });
});

describe('/endpoints başlık ↔ gövde hizası + Method rozeti (v0.10.1348)', () => {
  const assertAligned = () => {
    const ths = [...host!.querySelectorAll('thead tr th')];
    const rows = dataRows();
    expect(rows.length).toBeGreaterThan(0);
    for (const tr of rows) {
      const tds = [...tr.querySelectorAll('td')];
      expect(tds.length).toBe(ths.length);
      const at = (label: string) => ths.findIndex(th => thLabel(th) === label);
      expect(tds[at('Path')].textContent).toBe('/BSAWeb/api/allocate');
      expect(tds[at('Method')].textContent).toBe('POST');
      expect(tds[at('Calls')].textContent).toContain('1.4K');
      expect(tds[at('Error rate')].textContent).toContain('0.00%');
      expect(tds[at('P50')].textContent).toContain('10');
      expect(tds[at('Traces')].textContent).toContain('Traces');
    }
    return ths;
  };

  it('servis süzgeci yok: Service kolonu başlıkta ve gövdede birlikte', async () => {
    await mount('/endpoints?range=1h');
    const ths = assertAligned();
    const i = ths.findIndex(th => th.textContent?.trim().startsWith('Service'));
    expect(i).toBeGreaterThan(0);
    expect(dataRows()[0].querySelectorAll('td')[i].textContent).toBe('bsa-loan-assessment-allocation-prod');
  });

  it('servis süzgeci seçili: Service başlıkta da gövdede de YOK (prod kayması)', async () => {
    await mount('/endpoints?range=1h&service=bsa-loan-assessment-allocation-prod');
    const ths = assertAligned();
    expect(ths.some(th => th.textContent?.trim().startsWith('Service'))).toBe(false);
    expect(dataRows()[0].textContent).not.toContain('bsa-loan-assessment-allocation-prod');
  });

  it('metrik kipi + P90 seçili: P90 başlıkla birlikte gövdeden de düşer', async () => {
    // v0.10.1359 — metrik kipi servis ister.
    await mount('/endpoints?range=1h&src=metric&service=bsa-loan-assessment-allocation-prod&cols=path,method,calls,errorRate,status,p50Ms,p90Ms,p95Ms,traces');
    assertAligned();
    expect(headLabels()).not.toContain('P90');
  });

  it('RPC sekmesi aynı varsayılanlar ve hiza', async () => {
    await mount('/endpoints?range=1h&entry=rpc');
    expect(headLabels()).not.toContain('P99');
    expect(headLabels()).not.toContain('P99 Δ');
    expect(lastFetch()).toMatchObject({ sort: 'calls', dir: 'desc', entry: 'rpc' });
    const ths = [...host!.querySelectorAll('thead tr th')];
    for (const tr of dataRows()) expect(tr.querySelectorAll('td').length).toBe(ths.length);
  });

  it('Method rozeti atomdan (v0.10.1425 MethodBadge; v0.10.1429 göçü): POST yeşil ton', async () => {
    await mount('/endpoints?range=1h');
    const badge = dataRows()[0].querySelector('.badge.mono');
    expect(badge?.textContent).toBe('POST');
    expect(badge?.classList.contains('m-post')).toBe(true);
    expect(badge?.getAttribute('data-method')).toBe('POST');
  });
});
