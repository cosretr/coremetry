// @vitest-environment jsdom
//
// Logs.tableStates.test.tsx — v0.10.967 (tablo standardı T12, dilim 5, P-1).
//
// /logs'un yükleniyor / hata / yavaş backend / boş / yerel daraltma durumları
// artık LogTable'ın İÇİNDE (başlık kalır). Bu dosya GERÇEK sayfayı monte eder
// (yalnız ağ katmanı sahte) ve dört sözleşmeyi ölçer:
//   1. her durum tablonun tbody'sindeki tek `tr[data-dt-state]` satırı, thead yerinde;
//   2. eski kutuların eylemleri kaybolmadı ve SATIRIN içinde: ↻ Retry (aynı
//      refetch), "Clear filters", sunucu metni, span filtresini kaldır
//      bağlantısı, system stats linki;
//   3. başarısız bir yenilemede birikmiş eski satırlar hatanın üstünde KALMAZ;
//   4. no-match yalnız yüklenen satırlar varken (yerel daraltma eledi),
//      "Filtreleri temizle" odağı daraltma kutusuna geri verir.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogRow, LogsResponse } from '@/lib/types';

type Mode = 'rows' | 'empty' | 'error' | 'pending' | 'degraded';
const m = vi.hoisted(() => ({ mode: 'rows' as string, calls: 0 }));

const row = (id: number, body: string): LogRow => ({
  id, timestamp: 1.7e18 + id, severity: 9, severityText: 'INFO', body,
  serviceName: 'checkout', traceId: '', spanId: '', attributes: {}, resourceAttributes: {},
});

vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: () => {
      m.calls++;
      switch (m.mode as Mode) {
        case 'pending': return new Promise(() => {});
        case 'error': return Promise.reject(new Error('parse_exception: unbalanced quote at 1:5'));
        case 'empty': return Promise.resolve({ total: 0, logs: [] } satisfies LogsResponse);
        case 'degraded': return Promise.resolve({ total: 0, logs: [], degraded: true, reason: 'es timeout' } satisfies LogsResponse);
        default: return Promise.resolve({ total: 4, logs: [row(1, 'alpha ok'), row(2, 'beta ok')], nextCursor: 'c1' } satisfies LogsResponse);
      }
    },
    clusters: () => Promise.resolve({ clusters: [] }),
    logsFields: () => Promise.resolve({ fields: [], total: 0, types: {} }),
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

async function mountLogs(url: string) {
  window.history.replaceState(null, '', url);
  const Logs = (await import('./Logs')).default;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><ConfirmProvider><Logs /></ConfirmProvider></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
}

beforeEach(() => {
  m.mode = 'rows'; m.calls = 0;
  try { localStorage.clear(); } catch { /* özel pencere */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

// Log tablosu: `.logtbl-dense` (LogTable'ın kendi sınıfı) — sayfadaki diğer tablolarla karışmaz.
const table = () => host!.querySelector('table.logtbl-dense') as HTMLTableElement | null;
const stateRow = () => table()?.querySelector('tbody tr[data-dt-state]') as HTMLTableRowElement | null;
const buttonIn = (el: Element | null | undefined, text: string) =>
  [...(el?.querySelectorAll('button') ?? [])].find(b => b.textContent?.includes(text)) as HTMLButtonElement | undefined;

describe('/logs durumları tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: başlık yerinde, tek iskelet satırı', async () => {
    m.mode = 'pending';
    await mountLogs('/logs');
    expect(table()?.querySelector('thead th')).not.toBeNull();
    expect(stateRow()?.dataset.dtState).toBe('loading');
    expect(table()!.querySelectorAll('tbody tr').length).toBe(1);
  });

  it('hata: sunucu metni + Clear filters + ↻ Retry SATIRIN içinde; Retry aynı sorguyu yeniden atar', async () => {
    m.mode = 'error';
    await mountLogs('/logs');
    const r = stateRow();
    expect(r?.dataset.dtState).toBe('error');
    expect(r?.textContent).toContain('Log sorgusu çalışmadı');
    expect(r?.textContent).toContain('parse_exception: unbalanced quote at 1:5');
    expect(r?.textContent).toContain('level:error AND service.name:"checkout"');
    expect(buttonIn(r, 'Clear filters')).toBeDefined();
    const retry = buttonIn(r, 'Retry');
    expect(retry).toBeDefined();
    const before = m.calls;
    await act(async () => { retry!.click(); });
    await settle();
    expect(m.calls).toBeGreaterThan(before);
  });

  it('yavaş backend: rozet üstte kalır, satır hata türü (⚠ + Retry), "No logs found" DEMEZ', async () => {
    m.mode = 'degraded';
    await mountLogs('/logs');
    expect(host!.textContent).toContain('log backend yavaş/erişilemez — bu liste eksik');
    const r = stateRow();
    expect(r?.dataset.dtState).toBe('error');
    expect(r?.textContent).toContain('Log backend yavaş — bu liste eksik: es timeout');
    expect(buttonIn(r, 'Retry')).toBeDefined();
    expect(r?.textContent).not.toContain('Log bulunamadı');
  });

  it('boş: öğüt + system stats bağlantısı satırın içinde', async () => {
    m.mode = 'empty';
    await mountLogs('/logs');
    const r = stateRow();
    expect(r?.dataset.dtState).toBe('empty');
    expect(r?.textContent).toContain('Log bulunamadı');
    expect(r?.textContent).toContain('COREMETRY_LOGS_BACKEND');
    const link = r?.querySelector('a[href="/system/stats"]');
    expect(link?.textContent).toBe('system stats');
    // Pager yalnız satır varken.
    expect(host!.querySelector('.pager')).toBeNull();
  });

  it('trace kilidi: açıklama + "removing the span filter" satırın içinde; tık span filtresini düşürür', async () => {
    m.mode = 'empty';
    await mountLogs('/logs?traceId=abc123&spanId=def456');
    const r = stateRow();
    expect(r?.dataset.dtState).toBe('empty');
    expect(r?.textContent).toContain('Bu trace için log kaydı yok');
    expect(r?.textContent).toContain('The log shipper');
    const a = [...(r?.querySelectorAll('a') ?? [])].find(x => x.textContent === 'removing the span filter');
    expect(a).toBeDefined();
    a!.focus();
    expect(document.activeElement).toBe(a);
    await act(async () => { a!.click(); });
    await settle();
    const after = stateRow();
    expect(after?.dataset.dtState).toBe('empty');
    expect(after?.textContent).toContain('Bu trace için log kaydı yok');
    expect([...(after?.querySelectorAll('a') ?? [])].some(x => x.textContent === 'removing the span filter')).toBe(false);
    // detailKey 'span' → 'trace': yuva yeniden bağlandı, odak <body>'ye
    // düşmedi — tablonun kabına indi.
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toBe(table()!.closest('.table-wrap'));
  });

  it('satırlar varken durum satırı yok; yerel daraltma hepsini elerse no-match + Filtreleri temizle (odak kutuya)', async () => {
    await mountLogs('/logs');
    expect(stateRow()).toBeNull();
    expect(table()!.textContent).toContain('alpha ok');
    const input = host!.querySelector('input[placeholder^="Filter the 2 loaded rows"]') as HTMLInputElement;
    expect(input).not.toBeNull();
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      set.call(input, 'zzz-nope');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    const r = stateRow();
    expect(r?.dataset.dtState).toBe('no-match');
    expect(r?.textContent).toContain('Yüklenen 2 satırın hiçbiri "zzz-nope" içermiyor');
    const clear = buttonIn(r, 'Filtreleri temizle');
    expect(clear).toBeDefined();
    clear!.focus();
    await act(async () => { clear!.click(); });
    expect(stateRow()).toBeNull();
    expect(table()!.textContent).toContain('beta ok');
    expect(document.activeElement).toBe(input);
  });

  it('bayat satır + başarısız "Load more": hata satırı çizilir, birikmiş eski satırlar tabloda KALMAZ', async () => {
    // ↻ yenileme birikimi önce sıfırlar (resetPaging); birikmiş satırların
    // hatayla birlikte ekranda kalabildiği yol imleçli sonraki sayfa.
    await mountLogs('/logs');
    expect(table()!.textContent).toContain('alpha ok');
    m.mode = 'error';
    const more = buttonIn(host!.querySelector('.pager'), 'Load more');
    expect(more).toBeDefined();
    await act(async () => { more!.click(); });
    await settle();
    expect(stateRow()?.dataset.dtState).toBe('error');
    expect(table()!.textContent).not.toContain('alpha ok');
    expect(table()!.querySelectorAll('tbody tr').length).toBe(1);
  });
});
