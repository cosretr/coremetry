// @vitest-environment jsdom
//
// ProblemsSection.tableStates — v0.10.967 (tablo standardı T12, dilim 5).
//
// NE ÇİVİLİYOR: /inbox "Alert rules" tablosu.
//   • yükleniyor / boş / eşleşme yok tablonun İÇİNDE; başlık + süzgeç çubuğu
//     yerinde.
//   • HATA: eski kodda hata dalı YOKTU — null hiçbir dala girmiyor, bölüm
//     başlığın altında boş kalıyordu ("alarm yok" gibi okunuyordu). Artık
//     hata satırı; yenileme hatası bayat satırları da gizler.
//   • Eşleşme yok: sunucu satır döndürüp istemci şiddet çipleri hepsini
//     elediyse YA DA isteğin kendisi kullanıcı süzgeci taşıdıysa (öncelik
//     çipleri — varsayılan P1+P2 de dar —, servis, takım, cluster). Boş
//     durum metni yalnız istek durum dışında daraltılmamışken.
//   • "open" + sıfır satır çökme dalı (bölüm başlığı + "all clear" +
//     evaluator durumu) ürün kararı olarak aynen kalır.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Problem } from '@/lib/types';

const m = vi.hoisted(() => {
  // uPlot modül yüklenirken matchMedia okuyor; jsdom'da yok (importlardan ÖNCE).
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[], fail: false, pending: false };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    problems: () => {
      if (m.pending) return new Promise(() => {});
      if (m.fail) return Promise.reject(new Error('CH down'));
      return Promise.resolve({ items: m.items, total: m.items.length, truncated: false });
    },
    problemBuckets: async () => ({ priority: { P1: 0, P2: 0, P3: 0 } }),
    servicesMetadata: async () => ({}),
    blastRadiusBatch: async () => ({ items: {} }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'admin' }, loading: false }),
}));
vi.mock('@/components/RootCauseRibbon', () => ({ RootCauseRibbon: () => null }));

import { ProblemsSection } from './ProblemsSection';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const prob = (id: string, severity = 'critical'): Problem => ({
  id, ruleId: 'builtin:error_rate', ruleName: `rule ${id}`, severity, service: 'checkout',
  metric: 'error_rate', value: 12, threshold: 5, status: 'open', description: '',
  priority: 'P1', startedAt: 1_700_000_000_000_000_000,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });

async function mount(url = '/inbox'): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><ProblemsSection serviceFilter="" /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

const stateRow = (el: HTMLElement) => el.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;
const dataRows = (el: HTMLElement) =>
  [...el.querySelectorAll('tbody tr:not([data-dt-state])')].filter(tr => tr.querySelector('input[type="checkbox"]')).length;
const selectAll = (el: HTMLElement) => el.querySelector('thead input[type="checkbox"]') as HTMLInputElement | null;

beforeEach(() => {
  m.items = []; m.fail = false; m.pending = false;
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Alert rules — durumlar tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: başlık + süzgeç çubuğu + iskelet satırı; tümünü seç kapalı', async () => {
    m.pending = true;
    const el = await mount();
    expect(el.querySelector('thead')?.textContent).toContain('Priority');
    expect(el.querySelector('.facetbar')).not.toBeNull();
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('loading');
    // 1 seçim + 8 kolon + Assignee + Triage
    expect(row!.querySelector('td')!.colSpan).toBe(11);
    expect(selectAll(el)?.disabled).toBe(true);
  });

  it('ilk okuma hatası: hata satırı (eskiden bölüm boş kalıyordu)', async () => {
    m.fail = true;
    const el = await mount();
    expect(stateRow(el)?.dataset.dtState).toBe('error');
    expect(stateRow(el)?.textContent).toContain('bu bir hata, boş sonuç değil');
  });

  it('yenileme hatası: bayat satırlar gizlenir, hata satırı görünür', async () => {
    m.items = [prob('p1'), prob('p2')];
    const el = await mount();
    expect(dataRows(el)).toBe(2);
    expect(stateRow(el)).toBeNull();
    m.fail = true;
    await act(async () => { await qc.refetchQueries({ queryKey: ['problems'] }); });
    await settle();
    expect(stateRow(el)?.dataset.dtState, 'bayat satırlar hatayı gizledi').toBe('error');
    expect(dataRows(el)).toBe(0);
  });

  it('boş (resolved, tüm öncelikler): durum adıyla boş satır, "yukarıdaki süzgeç" çaresi', async () => {
    const el = await mount('/inbox?status=resolved&prio=P1,P2,P3');
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('empty');
    expect(row?.textContent).toContain('"resolved" durumunda problem yok');
    expect(row?.textContent).toContain('yukarıdaki süzgeci değiştir');
  });

  // v0.10.967 — isteğin taşıdığı süzgeçler (tarif §3): sunucu sıfır döndürse de
  // kullanıcı süzgeci daraltmıştır → eşleşme yok, "durumunda problem yok" değil.
  it('eşleşme yok: varsayılan P1+P2 öncelik isteği sıfır döndü', async () => {
    const el = await mount('/inbox?status=resolved');
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('no-match');
    expect(row?.textContent).toContain('Seçili süzgeçlerle "resolved" problem yok');
    expect(row?.textContent).not.toContain('durumunda problem yok');
  });

  it('eşleşme yok: tüm öncelikler ama sahip takım süzgeci sıfır döndü', async () => {
    const el = await mount('/inbox?status=resolved&prio=P1,P2,P3&owner=payments');
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('no-match');
    expect(row?.textContent).toContain('Seçili süzgeçlerle "resolved" problem yok');
  });

  it('eşleşme yok: sunucu satır döndürdü, şiddet çipi hepsini eledi', async () => {
    m.items = [prob('p1', 'info')];
    const el = await mount('/inbox?sev=critical');
    const row = stateRow(el);
    expect(row?.dataset.dtState).toBe('no-match');
    expect(row?.textContent).toContain('Seçili şiddetlerde');
    expect(dataRows(el)).toBe(0);
  });

  it('open + sıfır satır: çökme dalı aynen (tablo yok, "all clear")', async () => {
    const el = await mount();
    expect(el.textContent).toContain('No open alerts — all clear!');
    expect(el.querySelector('table')).toBeNull();
  });

  it('satırlar: durum satırı yok, tümünü seç açık', async () => {
    m.items = [prob('p1')];
    const el = await mount();
    expect(stateRow(el)).toBeNull();
    expect(dataRows(el)).toBe(1);
    expect(selectAll(el)?.disabled).toBe(false);
  });
});
