// @vitest-environment jsdom
// AdminClickhouse.coverageState.test.tsx — v0.10.977 (tablo standardı, dilim 7).
//
// 0012 sihirbazının ön kontrol kutusundaki kapsama tablosu statik (T1: tanımlı
// küme sayısı kadar satır, sıralanmaz) ve boş hâli v0.10.954'ten beri elle
// yazılmış bir `<tr data-dt-state="empty">` idi ("P-2 gelince DataTableState").
// P-2 (v0.10.967) primitife `colSpan` verdi; satır artık
// `<DataTableState colSpan={6} kind="empty">`. Çivi: boş kapsama = thead'in
// altı sütununu kaplayan TEK durum satırı + aynı metin; 0011 eki yalnız kolon
// yokken; satır varsa durum satırı yok (satırlar kazanır). fetch taklit, api.ts
// gerçek (rolloutLayer.test ile aynı kalıp).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { RolloutLayerPreflightResult, RolloutLayerStatusResult } from '@/lib/types';
import { RolloutLayerWizardPanel } from './AdminClickhouse';

const status = (): RolloutLayerStatusResult => ({ cluster: 'uptrace_all', activityRows: 0, generated: 1, objects: [] });
const pre = (over: Partial<RolloutLayerPreflightResult> = {}): RolloutLayerPreflightResult => ({
  clusters: ['uptrace_all'], suggestedCluster: 'uptrace_all', spansLocal: true, layer0011: true,
  coverage: [], mvGate: false, uniqRs1h: 0, uniqImage1h: 0, supported: true,
  detail: 'uygulanabilir', generated: 1, ...over,
});

type Reply = { status?: number; body: unknown };
let routes: Record<string, Reply> = {};
function stubFetch() {
  vi.stubGlobal('fetch', (url: unknown, init?: RequestInit) => {
    const u = String(url);
    const r = routes[`${init?.method ?? 'GET'} ${u.split('?')[0]}`] ?? { status: 404, body: { error: `rota yok: ${u}` } };
    return Promise.resolve(new Response(JSON.stringify(r.body), { status: r.status ?? 200, headers: { 'content-type': 'application/json' } }));
  });
}

const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
let host: HTMLElement | null = null;
let root: Root | null = null;
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root = createRoot(host!);
    root.render(<QueryClientProvider client={qc}><MemoryRouter><RolloutLayerWizardPanel /></MemoryRouter></QueryClientProvider>);
  });
  await wait();
  return host!;
}
const group0012 = (el: HTMLElement) => {
  const g = el.querySelector<HTMLElement>('[role="group"][aria-label^="0012"]');
  expect(g, '0012 grubu yok').not.toBeNull();
  return g!;
};
/** Ön kontrol kutusundaki kapsama tablosu (başlığından tanınır; nesne tablosu değil). */
const coverageTable = (g: HTMLElement) => {
  const t = [...g.querySelectorAll('table')].find(x => x.querySelector('thead')?.textContent?.includes('Span cluster değeri'));
  expect(t, 'kapsama tablosu yok').toBeDefined();
  return t!;
};
async function preflight(el: HTMLElement): Promise<HTMLTableElement> {
  const g = group0012(el);
  const b = [...g.querySelectorAll('button')].find(x => x.textContent?.trim() === 'Ön kontrol');
  expect(b, '"Ön kontrol" düğmesi yok').toBeDefined();
  await act(async () => { b!.click(); });
  await wait();
  return coverageTable(g);
}

beforeEach(() => {
  routes = { 'GET /api/admin/rollout-layer/status': { body: status() } };
  stubFetch();
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  vi.unstubAllGlobals();
});

describe('0012 ön kontrol — kapsama tablosunun boş durumu (v0.10.977, P-2 colSpan)', () => {
  it('kapsama boş + 0011 var: tek durum satırı, altı sütunu kaplar, başlık durur, ek yok', async () => {
    routes['GET /api/admin/rollout-layer/preflight'] = { body: pre({ coverage: [], layer0011: true }) };
    const t = await preflight(await mount());
    expect(t.querySelectorAll('thead th')).toHaveLength(6);
    const rows = t.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(1);
    const tr = rows[0];
    expect(tr.getAttribute('data-dt-state')).toBe('empty');
    const tds = tr.querySelectorAll('td');
    expect(tds).toHaveLength(1);
    expect(tds[0].getAttribute('colspan')).toBe('6');
    expect(tr.textContent).toContain("Son 15 dk'da span yok");
    expect(tr.textContent).not.toContain('0011 önce');
    // T2: durum satırı tıklanabilir görünmez.
    expect(tr.getAttribute('role')).toBeNull();
    expect(tr.hasAttribute('data-row-action')).toBe(false);
  });

  it('kapsama null + 0011 yok: aynı satır, metin 0011 ekini taşır', async () => {
    routes['GET /api/admin/rollout-layer/preflight'] = { body: pre({ coverage: null, layer0011: false }) };
    const t = await preflight(await mount());
    const tr = t.querySelector('tbody tr[data-dt-state]');
    expect(tr).not.toBeNull();
    expect(tr!.getAttribute('data-dt-state')).toBe('empty');
    expect(tr!.textContent).toContain("Son 15 dk'da span yok (cluster kolonu yok — 0011 önce)");
  });

  it('kapsama satırı varsa durum satırı yok — satırlar kazanır', async () => {
    routes['GET /api/admin/rollout-layer/preflight'] = { body: pre({
      coverage: [
        { cluster: 'prod', total: 1000, sampled: 200, replicaset: 0.99, image: 0.98, namespace: 1 },
        { cluster: '', total: 5, sampled: 5, replicaset: 0, image: 0, namespace: 0 },
      ],
    }) };
    const t = await preflight(await mount());
    expect(t.querySelector('tbody tr[data-dt-state]')).toBeNull();
    const first = [...t.querySelectorAll('tbody tr')].map(r => r.querySelector('td')?.textContent);
    expect(first).toEqual(['prod', '(boş — kapıya girmez)']);
  });
});
