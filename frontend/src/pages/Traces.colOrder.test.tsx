// @vitest-environment jsdom
//
// Traces.colOrder.test.tsx — v0.10.1516 (operatör isteği 2026-10-24: "kolon
// sıralamasını da değiştirebilsin isteyen kullanıcılar"). GERÇEK sayfa monte
// edilir (yalnız `@/lib/api` sahte; Traces.opGroupChip deseni). Pinlenen:
//   • başlık sırası saklı sırayı izler (URL `cols=` tam sıra / localStorage),
//     gövde hücreleri AYNI sırada, v0.10.1511 hunisi kendi kolonuyla taşınır;
//   • Sütunlar ▾ ↑/↓ (klavye yolu) sırayı değiştirir → URL `cols=` + LS;
//   • başlık sürükle-bırak sırayı değiştirir; Start time sürüklenmez, hedef olmaz;
//   • Sırayı sıfırla varsayılana döner: LS anahtarı düşer, `cols=` eski biçim.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Traces from './Traces';

const m = vi.hoisted(() => ({ prefPuts: [] as unknown[] }));
vi.mock('@/lib/api', () => {
  const traces = Array.from({ length: 2 }, (_, i) => ({
    traceId: String(i).padStart(32, '0'), serviceName: 'svc-orders', rootName: `GET /orders/${i}`,
    startTime: 1.7e18, durationMs: 12, spanCount: 4, hasError: false,
  }));
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    traces: () => Promise.resolve({ traces, hasMore: false }),
    spanMetricBatch: () => Promise.resolve({ series: { count: [], errors: [], rt: [] } }),
    attributeKeys: () => Promise.resolve([{ key: 'http.route' }]),
    tracesCount: () => Promise.resolve({ value: 2, atLeast: false }),
    tracesExtras: () => Promise.resolve({ extras: {} }),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    operationNames: () => Promise.resolve({ names: [], total: 0 }),
    getPreference: () => Promise.resolve(null),
    putPreference: (_k: unknown, model: unknown) => { m.prefPuts.push(model); return Promise.resolve({}); },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'admin' }, loading: false }),
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

async function mountTraces(url: string) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><ConfirmProvider><Traces /></ConfirmProvider></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
}

beforeEach(() => {
  m.prefPuts = [];
  const store = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => store.clear(), key: () => null, length: 0,
  });
});
afterEach(() => {
  vi.unstubAllGlobals();
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

const LABEL: Record<string, string> = {
  'Start time': 'time', Service: 'service', Name: 'operation', Duration: 'duration', Status: 'status', Spans: 'spans',
};
const listTable = () => [...host!.querySelectorAll('table')].find(t => t.querySelector('thead[data-table-id="traces-list"]'))!;
const headThs = () => [...listTable().querySelectorAll<HTMLTableCellElement>('thead th')];
const headIds = () => headThs().map(th => {
  const l = (th.querySelector('.tcf-th-l')?.textContent ?? th.textContent ?? '').replace(/[▲▼↕⋯]/g, '').trim();
  return LABEL[l] ?? l;
});
const thOf = (id: string) => headThs()[headIds().indexOf(id)];
const colsParam = () => new URLSearchParams(window.location.search).get('cols');
const button = (label: string) => document.body.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);

function dnd(type: string, el: Element) {
  const ev = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(ev, 'dataTransfer', {
    value: { setData() {}, getData: () => '', effectAllowed: 'move', dropEffect: 'move' },
  });
  act(() => { el.dispatchEvent(ev); });
}

const FULL = 'time,duration,service,operation,http.route,status,spans';

describe('/traces kullanıcı sütun sırası (v0.10.1516)', () => {
  it('URL `cols=` tam sırası başlığı ve gövdeyi sürer; huni kolonuyla taşınır', async () => {
    await mountTraces(`/traces?view=list&range=1h&cols=${FULL}`);
    expect(headIds()).toEqual(FULL.split(','));
    // v0.10.1511 hunisi Service başlığında, artık 3. sırada.
    expect(thOf('service').querySelector('button.tcf-btn')?.getAttribute('aria-label')).toContain('Service');
    expect(thOf('duration').querySelector('button.tcf-btn')?.getAttribute('aria-label')).toContain('Duration');
    expect(thOf('time').querySelector('button.tcf-btn')).toBeNull();
    // Gövde aynı sırada: jsdom'da sanal gövde satır basmaz (yükseklik 0), bu
    // yüzden kaynak kapısı — renderRow hücreleri başlığın dizisinden çizer.
    const src = readFileSync(resolve(__dirname, 'Traces.tsx'), 'utf8');
    const body = src.slice(src.indexOf('renderRow={(t) =>'), src.indexOf('renderRow={(t) =>') + 400);
    expect(body).toContain('{dt.visibleColumns.map(({ id }) => {');
    expect(body).not.toContain('colIds.map(');
    // Kendi yazımı biçimi korur (tam sıra, yeni parametre yok).
    expect(colsParam()).toBe(FULL);
  });

  it('localStorage sırası URL yokken benimsenir', async () => {
    localStorage.setItem('traces-extra-cols', JSON.stringify(['http.route']));
    localStorage.setItem('traces-col-order', JSON.stringify(FULL.split(',')));
    await mountTraces('/traces?view=list&range=1h');
    expect(headIds()).toEqual(FULL.split(','));
    expect(colsParam()).toBe(FULL);
  });

  it('Sütunlar ▾ ↑/↓ sırayı değiştirir → URL + LS + sunucu tercihi; Sıfırla varsayılana döner', async () => {
    localStorage.setItem('traces-extra-cols', JSON.stringify(['http.route']));
    await mountTraces('/traces?view=list&range=1h');
    expect(headIds()).toEqual(['time', 'service', 'operation', 'http.route', 'duration', 'status', 'spans']);
    const trigger = [...document.body.querySelectorAll('button')].find(b => b.textContent?.startsWith('Columns'))!;
    act(() => { trigger.click(); });
    // Start time sabit: ↑/↓ yok sayılır; Service'in ↑'su da (önü sabit).
    expect(button('Move left: Start time')!.disabled).toBe(true);
    expect(button('Move right: Start time')!.disabled).toBe(true);
    expect(button('Move left: Service')!.disabled).toBe(true);
    // Klavye yolu: odaklanabilir gerçek düğmeler.
    const down = button('Move right: Service')!;
    down.focus();
    expect(document.activeElement).toBe(down);
    act(() => { down.click(); });
    await settle();
    expect(headIds()).toEqual(['time', 'operation', 'service', 'http.route', 'duration', 'status', 'spans']);
    expect(colsParam()).toBe('time,operation,service,http.route,duration,status,spans');
    expect(JSON.parse(localStorage.getItem('traces-col-order')!)).toEqual(headIds());
    await act(async () => { await new Promise(r => setTimeout(r, 900)); });
    expect(m.prefPuts.at(-1)).toMatchObject({ order: headIds() });
    // Sıfırla.
    const reset = [...document.body.querySelectorAll('button')].find(b => b.textContent === 'Reset order')!;
    expect(reset.disabled).toBe(false);
    act(() => { reset.click(); });
    await settle();
    expect(headIds()).toEqual(['time', 'service', 'operation', 'http.route', 'duration', 'status', 'spans']);
    expect(localStorage.getItem('traces-col-order')).toBeNull();
    expect(colsParam()).toBe('http.route');
  });

  it('başlık sürükle-bırak taşır; Start time sürüklenmez ve hedef olmaz', async () => {
    localStorage.setItem('traces-extra-cols', JSON.stringify(['http.route']));
    await mountTraces('/traces?view=list&range=1h');
    expect(thOf('time').getAttribute('draggable')).toBeNull();
    expect(thOf('status').getAttribute('draggable')).toBe('true');
    dnd('dragstart', thOf('status'));
    dnd('dragover', thOf('service'));
    expect(thOf('service').className).toContain('dt-drop-before');
    dnd('drop', thOf('service'));
    await settle();
    expect(headIds()).toEqual(['time', 'status', 'service', 'operation', 'http.route', 'duration', 'spans']);
    // Huni Status ile birlikte 2. sırada.
    expect(headThs()[1].querySelector('button.tcf-btn')?.getAttribute('aria-label')).toContain('Status');
    // Start time'a bırakmak sırayı değiştirmez.
    dnd('dragstart', thOf('spans'));
    dnd('dragover', thOf('time'));
    dnd('drop', thOf('time'));
    await settle();
    expect(headIds()[0]).toBe('time');
    expect(headIds()).toEqual(['time', 'status', 'service', 'operation', 'http.route', 'duration', 'spans']);
  });
});
