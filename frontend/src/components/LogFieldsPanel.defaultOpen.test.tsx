// @vitest-environment jsdom
//
// LogFieldsPanel.defaultOpen — v0.10.1501 (/logs Kibana-Discover dilim 3;
// operatör onayı 2026-10-24 "Alan listesi açık başlasın", v0.8.286'nın
// "varsayılan kapalı" kararını ≥1280px'te tersine çevirir).
//   • saf kural: saklı '1' açık, saklı '0' KAPALI (operatörün kapatması her
//     genişlikte kazanır), tercih yoksa genişliğe göre;
//   • mount: geniş ekran + tercih yok → panel açık; '0' → ray; dar → ray;
//   • ES-maliyet kilidi: /logs paneli AÇIK bağlanınca alan istatistiği
//     (logsFieldStats) HİÇ çağrılmaz (yalnız akordeon açılınca) ve alan
//     listesi (logsFields) TAM BİR kez okunur.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse } from '@/lib/types';

const m = vi.hoisted(() => ({ fieldStats: 0, fields: 0 }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: () => Promise.resolve({ total: 0, logs: [] } satisfies LogsResponse),
    logsTimeseries: () => Promise.resolve([]),
    clusters: () => Promise.resolve({ clusters: [] }),
    logsFields: () => { m.fields++; return Promise.resolve({ fields: ['k8s.pod.name', 'http.status_code'], total: 2, types: {} }); },
    logsFieldStats: () => { m.fieldStats++; return Promise.resolve({ field: 'k8s.pod.name', values: [], total: 0 }); },
    getKibanaSettings: () => Promise.resolve(null),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u1', username: 'op', role: 'admin' }, loading: false }),
}));

import { LogFieldsPanel, fieldsPanelOpen } from './LogFieldsPanel';
// Sayfa modülü STATİK (v0.10.1707): test gövdesindeki dinamik import ilk
// testin 5 s bütçesinden ödeniyordu — tam paket yükünde zaman aşımı +
// sızan mount ile ardışık düşüş. Toplama aşamasının süre tavanı yok.
import Logs from '@/pages/Logs';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
const setWide = (wide: boolean) => {
  window.matchMedia = ((q: string) => ({
    matches: wide && q.includes('min-width: 1280px'), media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
};

let root: Root | null = null; let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
function mount(node: React.ReactNode) {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root!.render(<QueryClientProvider client={qc}><BrowserRouter><ConfirmProvider>{node}</ConfirmProvider></BrowserRouter></QueryClientProvider>);
  });
}
beforeEach(() => {
  m.fieldStats = 0; m.fields = 0;
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

const panelProps = {
  fields: ['k8s.pod.name'], columns: [], scope: {},
  onToggleColumn: () => {}, onPillAdd: () => {}, onPillExclude: () => {},
};
const isOpen = () => !!host!.querySelector('.ld-fields');
const rail = () => [...host!.querySelectorAll('button')].find(b => b.textContent?.includes('ƒ Fields'));

describe('fieldsPanelOpen (saf)', () => {
  it.each([
    [null, true, true], [null, false, false],
    ['1', false, true], ['1', true, true],
    ['0', true, false], ['0', false, false],
    ['garbage', true, true],
  ] as const)('stored=%s wide=%s → %s', (stored, wide, want) => {
    expect(fieldsPanelOpen(stored, wide)).toBe(want);
  });
});

describe('LogFieldsPanel varsayılan açıklık', () => {
  it('geniş ekran + tercih yok → açık; « kapatır ve "0" yazar', async () => {
    setWide(true);
    mount(<LogFieldsPanel {...panelProps} />);
    expect(isOpen()).toBe(true);
    const close = [...host!.querySelectorAll('button')].find(b => b.textContent === '«')!;
    await act(async () => { close.click(); });
    expect(isOpen()).toBe(false);
    expect(localStorage.getItem('logs.fieldsPanel.open')).toBe('0');
  });
  it('saklı "0" geniş ekranda da kapalı (ray)', () => {
    setWide(true);
    localStorage.setItem('logs.fieldsPanel.open', '0');
    mount(<LogFieldsPanel {...panelProps} />);
    expect(isOpen()).toBe(false);
    expect(rail()).toBeTruthy();
  });
  it('dar ekran (<1280) + tercih yok → ray; tık açar ve "1" yazar', async () => {
    setWide(false);
    mount(<LogFieldsPanel {...panelProps} />);
    expect(isOpen()).toBe(false);
    await act(async () => { rail()!.click(); });
    expect(isOpen()).toBe(true);
    expect(localStorage.getItem('logs.fieldsPanel.open')).toBe('1');
  });
});

describe('ES-maliyet kilidi — /logs paneli açık bağlanır', () => {
  it('0 logsFieldStats, tam 1 logsFields', async () => {
    setWide(true);
    window.history.replaceState(null, '', '/logs?range=custom:1759494780000-1759497240000');
    mount(<Logs />);
    await settle(); await settle();
    expect(isOpen()).toBe(true);
    expect(m.fields).toBe(1);
    expect(m.fieldStats).toBe(0);
  });
});
