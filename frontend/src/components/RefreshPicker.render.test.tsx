// @vitest-environment jsdom
//
// RefreshPicker.render.test.tsx — v0.10.1430 Topbar'daki Grafana tarzı yenileme
// seçicisi (Topbar.documentTitle.test.tsx kurulumu). Operatör: "Sağ üstte zaman
// yanında Grafana gibi ayrı refresh olabilir. 60 saniye default olabilir."
//   • allowlist rotasında varsayılan 1m, altı seçenek; seçim ?refresh= (replace)
//     + localStorage'a yazılır;
//   • /dashboard'da 10s sunulmaz (taban 30 s);
//   • manuel rotada select devre dışı "Off · manual", ↻ çalışır;
//   • ↻ tik yolunu tetikler (anchor kayar);
//   • mutlak aralıkta altyazı nedenini söyler.
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { Topbar } from './Topbar';
import { __resetRefreshStore, refreshSnapshot } from '@/lib/autoRefresh';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

function LocationProbe() {
  const { search } = useLocation();
  return <output data-testid="loc">{search}</output>;
}

async function mount(path: string, node: React.ReactNode) {
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={[path]}>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          {node}<LocationProbe />
        </QueryClientProvider>
      </MemoryRouter>,
    );
  });
  await act(async () => { await vi.advanceTimersByTimeAsync(10); });
}
const sel = () => host.querySelector<HTMLSelectElement>('select.rp-select')!;
const btn = () => host.querySelector<HTMLButtonElement>('.rp button')!;
const options = () => Array.from(sel().options).map(o => o.value);
const topbar = (range = true) => (
  <Topbar title="Services" range={range ? { preset: '3h' } : undefined} onRangeChange={range ? () => {} : undefined} />
);

// Node 25'in yerleşik `localStorage` global'i jsdom'unkini gölgeliyor ve
// metotsuz (PickerPopover.contract.test.tsx emsali: bellek içi stub).
function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return {
    getItem: k => m.get(k) ?? null, setItem: (k, v) => { m.set(k, String(v)); },
    removeItem: k => { m.delete(k); }, clear: () => m.clear(), key: i => Array.from(m.keys())[i] ?? null,
    get length() { return m.size; },
  };
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-10-22T10:00:00Z'));
  vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('no network in test'))));
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('sessionStorage', memoryStorage());
  __resetRefreshStore();
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('RefreshPicker (v0.10.1430)', () => {
  it('allowlist rotasında varsayılan 5m (v0.10.2264) ve altı seçenek; zaman seçicinin sağında', async () => {
    await mount('/services', topbar());
    expect(sel().value).toBe('5m');
    expect(sel().disabled).toBe(false);
    expect(options()).toEqual(['off', '10s', '30s', '1m', '5m', '15m']);
    const trp = host.querySelector('.trp')!;
    const rp = host.querySelector('.rp')!;
    expect(trp.compareDocumentPosition(rp) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(host.querySelector('.rp-caption')?.textContent).toMatch(/^updated .* ago$/);
  });

  it('seçim ?refresh= (replace) + localStorage\'a yazılır; off da açıkça yazılır', async () => {
    await mount('/services', topbar());
    await act(async () => {
      const s = sel();
      s.value = '5m';
      s.dispatchEvent(new Event('change', { bubbles: true }));
    });
    expect(host.querySelector('[data-testid="loc"]')!.textContent).toBe('?refresh=5m');
    expect(localStorage.getItem('cm.refresh')).toBe('5m');
    expect(sel().value).toBe('5m');
    await act(async () => {
      const s = sel();
      s.value = 'off';
      s.dispatchEvent(new Event('change', { bubbles: true }));
    });
    expect(host.querySelector('[data-testid="loc"]')!.textContent).toBe('?refresh=off');
    expect(host.querySelector('.rp-caption')).toBeNull();
  });

  it('/dashboard: 10s sunulmaz (taban 30 s); URL\'deki 10s 30s görünür', async () => {
    await mount('/dashboard?id=x&refresh=10s', topbar());
    expect(options()).toEqual(['off', '30s', '1m', '5m', '15m']);
    expect(sel().value).toBe('30s');
  });

  it('manuel rotada select devre dışı "Off · manual" + neden; ↻ etkin', async () => {
    await mount('/logs', topbar());
    expect(sel().disabled).toBe(true);
    expect(options()).toEqual(['off']);
    expect(sel().options[0].textContent).toBe('Off · manual');
    expect(sel().title).toContain('Here use ↻');
    expect(btn().disabled).toBe(false);
    expect(host.querySelector('.rp-caption')).toBeNull();
  });

  it('↻ tik yolunu tetikler (anchor kayar) ve tamamlanınca aria-live duyurur', async () => {
    await mount('/services', topbar());
    const a0 = refreshSnapshot().anchorMs;
    await act(async () => { await vi.advanceTimersByTimeAsync(3_000); });
    await act(async () => { btn().click(); });
    expect(refreshSnapshot().anchorMs).toBe(Date.now());
    expect(refreshSnapshot().anchorMs - a0).toBeGreaterThanOrEqual(3_000);
    await act(async () => { await vi.advanceTimersByTimeAsync(10); });
    expect(host.querySelector('.rp [aria-live="polite"]')!.textContent).toBe('Refreshed');
  });

  it('mutlak aralıkta altyazı nedenini söyler (Grafana paritesi)', async () => {
    await mount('/services?range=custom:1000-2000', (
      <Topbar title="Services" range={{ preset: 'custom', fromMs: 1000, toMs: 2000 }} onRangeChange={() => {}} />
    ));
    expect(host.querySelector('.rp-caption')!.textContent).toBe('paused · absolute range');
  });

  it('yalnız env taşıyan sayfada da (Inbox sınıfı) ↻ çizilir; zaman/env\'siz sayfada çizilmez', async () => {
    await mount('/inbox', <Topbar title="Problems" showEnv envApplies />);
    expect(host.querySelector('.rp')).not.toBeNull();
    act(() => root.unmount());
    root = createRoot(host);
    await mount('/settings', <Topbar title="Settings" />);
    expect(host.querySelector('.rp')).toBeNull();
  });
});
