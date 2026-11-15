// @vitest-environment jsdom
//
// useAutoRefresh.test.tsx — v0.10.1430 global otomatik yenileme SAATİ.
// Operatör: "Sağ üstte zaman yanında Grafana gibi ayrı refresh olabilir.
// 60 saniye default olabilir, özellikle metrikler için işe yarar."
//
// Çivilenen davranış (sahte zamanlayıcı; features/tour/engine.test.ts emsali):
//   • aralıkta tik: useUrlRange kimliği KAYAR (göreli pencere ilerler),
//     LIVE_META etiketli sorgu yeniden çekilir, etiketsiz çekilmez;
//   • document.hidden: tik yok; görünür olunca süre dolmuşsa hemen tik;
//   • mutlak (custom:) pencere: saat durur, kimlik sabit;
//   • off / manuel rota / hold: tik yok;
//   • /dashboard tabanı 30 s (10s → 30 s); eski ?refresh=30 okunur;
//   • manuel ↻: kayma + yeniden anahtarlanMAYAN aktif sorgular tazelenir.
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { useAutoRefresh, useManualRefresh, useRefreshHold } from './useAutoRefresh';
import { useUrlRange } from './useUrlRange';
import { LIVE_META, __resetRefreshStore, refreshSnapshot } from './autoRefresh';
import { STORAGE_KEYS, setRaw } from './storage';
import type { TimeRange } from './types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
let qc: QueryClient;
let ranges: TimeRange[] = [];
type Fetcher = Mock<() => Promise<{ ok: boolean }>>;
let liveFn: Fetcher;
let plainFn: Fetcher;
let manual: (() => void) | null = null;

function Probe({ hold = false }: { hold?: boolean }) {
  useAutoRefresh(true);
  useRefreshHold(hold, 'edit');
  manual = useManualRefresh();
  const [range] = useUrlRange('3h');
  ranges.push(range);
  useQuery({ queryKey: ['t', 'live'], queryFn: () => liveFn(), meta: LIVE_META, staleTime: Infinity });
  useQuery({ queryKey: ['t', 'plain'], queryFn: () => plainFn(), staleTime: Infinity });
  return null;
}

async function mount(path: string, hold = false) {
  await act(async () => {
    root.render(
      <MemoryRouter initialEntries={[path]}>
        <QueryClientProvider client={qc}><Probe hold={hold} /></QueryClientProvider>
      </MemoryRouter>,
    );
  });
  await advance(0); // ilk fetch'ler otursun
}
const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
// Aynı testte ikinci rota: önbellek sıfırlanır ki mount fetch'i sayılabilsin.
async function remount(path: string, hold = false) {
  act(() => root.unmount());
  qc.clear();
  root = createRoot(host);
  await mount(path, hold);
}
function setHidden(hidden: boolean) {
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => (hidden ? 'hidden' : 'visible') });
}
const last = () => ranges[ranges.length - 1];

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
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('sessionStorage', memoryStorage());
  __resetRefreshStore();
  setHidden(false);
  ranges = [];
  liveFn = vi.fn(async () => ({ ok: true }));
  plainFn = vi.fn(async () => ({ ok: true }));
  qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  qc.clear();
  manual = null;
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('useAutoRefresh — tik (varsayılan 5 dk — v0.10.2264, allowlist rotası)', () => {
  it('5 dk\'da kimlik kayar, LIVE_META sorgusu tazelenir, etiketsiz sorgu dokunulmaz', async () => {
    await mount('/services');
    const first = last();
    expect(first.preset).toBe('3h');
    expect(liveFn).toHaveBeenCalledTimes(1);
    expect(plainFn).toHaveBeenCalledTimes(1);
    const a0 = refreshSnapshot().anchorMs;

    await advance(299_000);
    expect(refreshSnapshot().anchorMs).toBe(a0);
    expect(last()).toBe(first); // tik öncesi aynı kimlik (v0.5.184 sözleşmesi)

    await advance(1_000);
    expect(refreshSnapshot().anchorMs).toBe(a0 + 300_000);
    expect(last()).not.toBe(first);
    expect(last()).toEqual(first); // aynı değer, yeni kimlik = useMemo([range]) yeniden koşar
    expect(liveFn).toHaveBeenCalledTimes(2);
    expect(plainFn).toHaveBeenCalledTimes(1);

    await advance(300_000);
    expect(liveFn).toHaveBeenCalledTimes(3);
  });

  it('gizli sekmede tik yok; görünür olunca süre dolmuşsa HEMEN tik (catch-up)', async () => {
    await mount('/services');
    const a0 = refreshSnapshot().anchorMs;
    setHidden(true);
    await advance(360_000);
    expect(refreshSnapshot().anchorMs).toBe(a0);
    expect(liveFn).toHaveBeenCalledTimes(1);
    setHidden(false);
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    await advance(0);
    expect(refreshSnapshot().anchorMs).toBe(a0 + 360_000);
    expect(liveFn).toHaveBeenCalledTimes(2);
  });

  it('görünür olunca süre DOLMAMIŞSA tik yok (ritim bozulmaz)', async () => {
    await mount('/services');
    const a0 = refreshSnapshot().anchorMs;
    setHidden(true);
    await advance(20_000);
    setHidden(false);
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    await advance(0);
    expect(refreshSnapshot().anchorMs).toBe(a0);
  });

  it('mutlak (custom:) pencerede saat durur ve kimlik sabit kalır', async () => {
    await mount('/services?range=custom:1000-2000');
    const first = last();
    expect(first.preset).toBe('custom');
    await advance(330_000);
    expect(last()).toBe(first);
    expect(liveFn).toHaveBeenCalledTimes(1);
  });

  it('?refresh=off, manuel rota ve hold → tik yok', async () => {
    await mount('/services?refresh=off');
    await advance(330_000);
    expect(liveFn).toHaveBeenCalledTimes(1);

    await remount('/logs');
    await advance(330_000);
    expect(liveFn).toHaveBeenCalledTimes(2); // yalnız mount fetch'i

    await remount('/services', true);
    await advance(330_000);
    expect(liveFn).toHaveBeenCalledTimes(3);
  });

  it('seçilen aralık uygulanır (10s) ve /dashboard tabanı 30 s\'e çeker; eski ?refresh=30 okunur', async () => {
    await mount('/services?refresh=10s');
    await advance(10_000);
    expect(liveFn).toHaveBeenCalledTimes(2);

    await remount('/dashboard?refresh=10s');
    await advance(10_000);
    expect(liveFn).toHaveBeenCalledTimes(3); // 10 s'de tik YOK
    await advance(20_000);
    expect(liveFn).toHaveBeenCalledTimes(4); // 30 s'de tik

    await remount('/dashboard?refresh=30');
    await advance(30_000);
    expect(liveFn).toHaveBeenCalledTimes(6);
  });

  it('localStorage kaydı URL yokken uygulanır; URL kaydı ezer', async () => {
    setRaw(STORAGE_KEYS.refresh, '10s');
    await mount('/services');
    await advance(10_000);
    expect(liveFn).toHaveBeenCalledTimes(2);

    await remount('/services?refresh=5m');
    await advance(10_000);
    expect(liveFn).toHaveBeenCalledTimes(3);
  });
});

describe('useManualRefresh — ↻', () => {
  it('kaydırır ve yeniden anahtarlanMAYAN aktif sorguları tazeler; kimlik değişimi tek fetch', async () => {
    await mount('/services?refresh=off');
    const first = last();
    expect(plainFn).toHaveBeenCalledTimes(1);
    await advance(5_000);
    act(() => { manual!(); });
    expect(last()).not.toBe(first);
    await advance(0); // ertelenmiş invalidate + fetch
    expect(plainFn).toHaveBeenCalledTimes(2);
    expect(liveFn).toHaveBeenCalledTimes(2);
  });

  it('mutlak pencerede de çalışır (Grafana: mutlak aralıkta refresh yeniden sorgular)', async () => {
    await mount('/traces?range=custom:1000-2000');
    const first = last();
    act(() => { manual!(); });
    expect(last()).toBe(first); // mutlak: kimlik sabit
    await advance(0);
    expect(plainFn).toHaveBeenCalledTimes(2);
  });
});
