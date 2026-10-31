// @vitest-environment jsdom
//
// columnPicker — v0.10.1502 (/logs Kibana-Discover dilim 4). Columns ▾ tek
// yoldan yazar (changeCols → state + URL `cols=` + sunucu tercihi BİR kez);
// Reset columns varsayılana döner ve `cols=` URL'den düşer; aday listesi
// YÜKLENMİŞ alanlardan gelir (yeni istek yok). Doc viewer Table/JSON sekmesi
// tarayıcıda hatırlanır.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { settleUntil } from '@/test/actWait';
import type { LogRow, LogsResponse } from '@/lib/types';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Logs from '../Logs';

const m = vi.hoisted(() => ({ prefPuts: 0, fields: 0, other: [] as string[] }));
const row = (id: number, body: string): LogRow => ({
  id, timestamp: 1.7e18 + id, severity: 9, severityText: 'INFO', body,
  serviceName: 'checkout', traceId: '', spanId: '', attributes: { 'k8s.pod.name': 'p-1' }, resourceAttributes: {},
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: () => Promise.resolve({ total: 2, logs: [row(1, 'alpha ok'), row(2, 'beta ok')] } satisfies LogsResponse),
    logsTimeseries: () => Promise.resolve([]),
    clusters: () => Promise.resolve({ clusters: [] }),
    logsFields: () => { m.fields++; return Promise.resolve({ fields: ['k8s.pod.name', 'http.status_code'], total: 2, types: {} }); },
    getKibanaSettings: () => Promise.resolve(null),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? ((..._a: unknown[]) => { m.other.push(k); return Promise.resolve({}); }) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u1', username: 'op', role: 'admin' }, loading: false }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
Element.prototype.scrollIntoView = function scrollIntoView() {};
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

let root: Root | null = null; let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
async function mountLogs(url: string) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><BrowserRouter><ConfirmProvider><Logs /></ConfirmProvider></BrowserRouter></QueryClientProvider>);
  });
  // Sabit uyku değil: sorgular yerleşene dek.
  await settleUntil(() => qc.isFetching() === 0, 'logs sorguları yerleşti');
  await settle();
}
beforeEach(() => {
  m.prefPuts = 0; m.fields = 0; m.other = [];
  const store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => store.clear(), key: () => null, length: 0,
  });
});
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

const RANGE = 'custom:1759494780000-1759497240000';
const btn = (text: string) => [...document.body.querySelectorAll('button')].find(b => (b.textContent ?? '').includes(text)) as HTMLButtonElement | undefined;
const heads = () => [...document.querySelectorAll('table.logtbl-dense thead th')].map(th => th.textContent?.replace(/×/g, '').trim());
const urlCols = () => new URLSearchParams(window.location.search).get('cols');
async function click(el: Element | null | undefined) {
  expect(el).toBeTruthy();
  await act(async () => { (el as HTMLElement).click(); });
  await settle();
}

describe('Columns ▾ (v0.10.1502)', () => {
  it('sırala → URL cols= + başlık sırası; aday ekle; Reset columns cols=’u düşürür; yeni alan isteği yok', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    expect(m.fields).toBe(1);
    await click(btn('Columns'));
    await click(document.querySelector('button[aria-label="Move left: Service"]'));
    expect(urlCols()).toBe('cluster,service,message,pod');
    expect(heads().slice(0, 4)).toEqual(['Time', 'Cluster', 'Service', 'Message']);
    await click(btn('+ http.status_code'));
    expect(urlCols()).toBe('cluster,service,message,pod,http.status_code');
    await click(document.querySelector('button[aria-label="Remove column: Pod"]'));
    expect(urlCols()).toBe('cluster,service,message,http.status_code');
    // Message kaldırılamaz: × yok
    expect(document.querySelector('button[aria-label="Remove column: Message"]')).toBeNull();
    await click(btn('Reset columns'));
    expect(urlCols()).toBeNull();
    expect(localStorage.getItem('dt.logs.columns')).toBe(JSON.stringify(['cluster', 'message', 'service', 'pod']));
    expect(m.fields).toBe(1); // aday listesi yüklenmiş alanlardan
  });
});

describe('doc viewer sekme hafızası (v0.10.1502)', () => {
  it('JSON seçimi hatırlanır: başka satır açılınca JSON ile başlar', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    const rows = () => [...document.querySelectorAll('table.logtbl-dense tbody tr[data-row-idx]')] as HTMLElement[];
    await click(rows()[0]);
    const jsonTab = () => document.querySelector('[role="tablist"][aria-label="Log kaydı görünümü"] button[data-tab-key="json"]') as HTMLButtonElement | null;
    await click(jsonTab());
    expect(localStorage.getItem('logs.docTab')).toBe('json');
    await click(rows()[0]); // kapat
    await click(rows()[1]); // diğer satırı aç
    const lists = document.querySelectorAll('[role="tablist"][aria-label="Log kaydı görünümü"]');
    expect(lists.length, 'tek açık satır').toBe(1);
    expect(jsonTab()?.getAttribute('aria-selected')).toBe('true');
  });
});
