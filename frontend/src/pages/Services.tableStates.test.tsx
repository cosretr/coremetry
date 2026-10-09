// @vitest-environment jsdom
//
// Services.tableStates.test.tsx — v0.10.967 (tablo standardı dilim 5, P-1).
//
// NE ÇİVİLİYOR: /services tablosunun durumları tablonun İÇİNDE, başlık
// yerinde (sahte olan yalnız AĞ katmanı; sayfanın fetch/URL/efekt
// mekaniği gerçek):
//   • boş (hiç süzgeç yok): OTLP kurulum talimatı — kopyalanan uç nokta
//     <code> olarak — durum satırının detail yuvasında (P-1);
//   • sunucuya giden süzgeç boş döndürdüyse "eşleşme yok", kurulum talimatı
//     YOK (süzgeç enstrümantasyon eksikliği gibi okunmasın); Reset'in
//     temizlediği bir süzgeçse "Filtreleri temizle" = Reset, yalnız
//     cluster seçiliyse düğme yok (Reset onu silmez);
//   • yerel ikinci geçiş (errors-only) sayfadaki satırları elediyse de
//     "eşleşme yok" + temizle;
//   • hata: sunucu metni + v0.9.858 uyarısı + aynı ↻ (retryNonce);
//   • dolu tabloda yenileme düşerse hata görünür, bayat satır kalmaz.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Service } from '@/lib/types';

const m = vi.hoisted(() => ({
  // Sunucu yanıtı süzgece göre seçilir: süzgeçli istek → `filtered`.
  rows: [] as unknown[],
  filtered: [] as unknown[],
  fail: false,
  pending: false,
  calls: [] as Record<string, unknown>[],
}));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    servicesPage: (_r: unknown, opts: unknown) => {
      const o = opts as Record<string, unknown>;
      m.calls.push(o);
      if (m.pending) return new Promise(() => {});
      if (m.fail) return Promise.reject(new Error('upstream 503'));
      const f = o.ownerTeam || o.cluster || o.name;
      const services = f ? m.filtered : m.rows;
      return Promise.resolve({ services, hasMore: false, total: services.length });
    },
    serviceSparklines: () => Promise.resolve({}),
    clusters: () => Promise.resolve({ clusters: [] }),
    namespaces: () => Promise.resolve({ namespaces: [] }),
    allServiceRuntimes: () => Promise.resolve({}),
    servicesMetadata: () => Promise.resolve({}),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));

import ServicesPage from './Services';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;

const svc = (name: string, errorRate = 0): Service => ({
  name, spanCount: 100, errorCount: errorRate, errorRate, avgDurationMs: 10, p99DurationMs: 20,
  apdex: 0.99, apdexThresholdMs: 200,
});

let host: HTMLElement | null = null;
let root: Root | null = null;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 40)); });

async function mount(url = '/services'): Promise<HTMLElement> {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter><ServicesPage /></BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  return host!;
}
const stateRow = (el: HTMLElement) => el.querySelector('table tbody tr[data-dt-state]') as HTMLElement | null;
// agg ("This page") satırı hariç servis satırları.
const svcRows = (el: HTMLElement) => el.querySelectorAll('table tbody tr[data-row-idx]').length;
const button = (el: ParentNode, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent?.trim().startsWith(text)) as HTMLButtonElement | undefined;
const errorsOnly = (el: HTMLElement) =>
  [...el.querySelectorAll('label')].find(l => l.textContent?.includes('Errors only'))!
    .querySelector('input') as HTMLInputElement;
const click = async (b: HTMLElement) => { await act(async () => { b.click(); }); await wait(); };

beforeEach(() => {
  m.rows = []; m.filtered = []; m.fail = false; m.pending = false; m.calls = [];
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  window.history.replaceState(null, '', '/');
});

describe('/services — durumlar tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: 10 iskelet çizgisi tabloda, başlık yerinde, sayfa şeridi yok', async () => {
    m.pending = true;
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('loading');
    expect(row.querySelectorAll('.dt-state-skel').length).toBe(10);
    expect(el.querySelectorAll('table thead th').length).toBeGreaterThan(0);
    expect(el.querySelector('.pager')).toBeNull();
  });

  it('boş (süzgeç yok): OTLP kurulum talimatı detail yuvasında, <code> ile', async () => {
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('empty');
    expect(row.textContent).toContain('Henüz servis yok');
    const codes = [...row.querySelectorAll('[data-dt-state-detail] code')].map(c => c.textContent);
    expect(codes).toEqual(['OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:14318', ':14317']);
  });

  it('sunucu süzgeci (owner team) boş döndü: eşleşme yok, talimat yok; Filtreleri temizle = Reset', async () => {
    m.rows = [svc('checkout'), svc('payments')];
    const el = await mount('/services?ownerTeam=payments');
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('no-match');
    expect(row.textContent).toContain('Geçerli süzgeçlerle eşleşen servis yok');
    expect(row.querySelector('[data-dt-state-detail]')).toBeNull();
    await click(button(row, 'Filtreleri temizle')!);
    await wait();
    expect(m.calls.at(-1)?.ownerTeam).toBeUndefined();
    expect(stateRow(el)).toBeNull();
    expect(svcRows(el)).toBe(2);
  });

  it('yalnız cluster süzgeci: eşleşme yok ama Reset onu silmediği için düğme yok', async () => {
    const el = await mount('/services?cluster=c1');
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('no-match');
    expect(button(row, 'Filtreleri temizle')).toBeUndefined();
  });

  it('yerel ikinci geçiş (errors-only) sayfayı eledi: eşleşme yok + temizle satırları geri getirir', async () => {
    m.rows = [svc('checkout', 0), svc('payments', 0)];
    const el = await mount();
    expect(svcRows(el)).toBe(2);
    await click(errorsOnly(el));
    expect(stateRow(el)?.dataset.dtState).toBe('no-match');
    expect(svcRows(el)).toBe(0);
    // Toplam satırı ("This page") durum satırının yanında çizilmez.
    expect(el.querySelector('tr.agg-row')).toBeNull();
    await click(button(stateRow(el)!, 'Filtreleri temizle')!);
    expect(errorsOnly(el).checked).toBe(false);
    expect(svcRows(el)).toBe(2);
  });

  it('hata: sunucu metni + uyarı, ↻ yeniden çeker', async () => {
    m.fail = true;
    const el = await mount();
    const row = stateRow(el)!;
    expect(row.dataset.dtState).toBe('error');
    expect(row.textContent).toContain('upstream 503');
    expect(row.textContent).toContain('collector tarafında arama');
    expect(row.textContent).not.toContain('OTEL_EXPORTER_OTLP_ENDPOINT');
    const before = m.calls.length;
    m.fail = false; m.rows = [svc('checkout')];
    await click(button(row, '↻ Retry')!);
    expect(m.calls.length).toBeGreaterThan(before);
    expect(stateRow(el)).toBeNull();
    expect(svcRows(el)).toBe(1);
  });

  it('dolu tabloda yenileme düştü: hata görünür, bayat satır ve sayfa şeridi kalmaz', async () => {
    m.rows = [svc('checkout', 1), svc('payments', 1)];
    const el = await mount();
    expect(svcRows(el)).toBe(2);
    expect(el.querySelector('.pager')).not.toBeNull();
    m.fail = true;
    await click(errorsOnly(el));
    expect(stateRow(el)?.dataset.dtState).toBe('error');
    expect(svcRows(el)).toBe(0);
    expect(el.querySelector('.pager')).toBeNull();
  });
});

// v0.10.1140 — namespace süzgeci açıkken çok-namespace'li servisin satırı
// "metrikler toplamdır" rozetini taşır (service_summary_5m'de namespace
// boyutu yok); süzgeç yokken ya da tek namespace'te rozet yok.
describe('/services — namespace toplam rozeti (v0.10.1140)', () => {
  const badges = (el: HTMLElement) =>
    [...el.querySelectorAll('table tbody .badge')].filter(b => b.textContent?.includes('ns · toplam'));

  it('süzgeç açık + 2 namespace: rozet, title sayıyı ve süzgeci söyler; tek namespace: rozet yok', async () => {
    m.rows = [{ ...svc('svc-gateway'), namespaceCount: 2 }, svc('svc-orders')];
    const el = await mount('/services?namespace=payments-uat');
    expect(m.calls.at(-1)?.namespace).toBe('payments-uat');
    const b = badges(el);
    expect(b.length).toBe(1);
    expect(b[0].textContent).toContain('2 ns');
    expect(b[0].getAttribute('title')).toContain("2 namespace'te çalışıyor; metrikler toplamdır");
    expect(b[0].getAttribute('title')).toContain('payments-uat');
  });

  it('süzgeç yok: alan gelse bile rozet yok', async () => {
    m.rows = [{ ...svc('svc-gateway'), namespaceCount: 2 }];
    const el = await mount('/services');
    expect(badges(el).length).toBe(0);
  });
});
