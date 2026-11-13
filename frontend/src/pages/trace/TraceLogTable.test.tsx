// @vitest-environment jsdom
//
// TraceLogTable — v0.10.1940: trace sayfasının log sekmesi Log Search'ün
// tarayıcı-yanı parçalarını alır (exception rozeti, Görünüm ▾, sayfalama);
// satır tablonun içinde açılır (v0.10.1941 — yan çekmece kaldırıldı). Açılışta log araması YAPILMAZ (satırlar trace paketinden gelir).
import '@/test/pageJsdom';
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LogRow } from '@/lib/types';
import { TraceLogTable } from './TraceLogTable';

const TS = 1759494800000000000;
const row = (id: number, extra: Partial<LogRow> = {}): LogRow => ({
  id, timestamp: TS + id, severity: 9, severityText: 'INFO', body: `order ${id} accepted`, serviceName: 'orders-api',
  traceId: 'abc', spanId: '', attributes: {}, resourceAttributes: {}, ...extra,
});
const OOM: LogRow = row(1, {
  severity: 17, severityText: 'ERROR', body: 'java.lang.OutOfMemoryError: Java heap space\n\tat com.acme.orders.cart.CartSerializer.write(CartSerializer.java:118)',
  exception: { type: 'java.lang.OutOfMemoryError', message: 'Java heap space', class: 'resource', critical: true, frames: 1, appFrames: 1, appLines: [1], top: ['CartSerializer.write'] },
});
const m = vi.hoisted(() => ({ logs: 0 }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: () => { m.logs++; return Promise.resolve({ total: 0, logs: [] }); },
    getDisplayPref: () => Promise.resolve(null),
  };
  return { api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }), isCanceled: () => false };
});
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null; let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
async function mount(rows: LogRow[]) {
  window.history.replaceState(null, '', '/trace/abc');
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><BrowserRouter><TraceLogTable rows={rows} head={<span>sayaç</span>} /></BrowserRouter></QueryClientProvider>);
  });
  await settle();
}
beforeEach(() => { m.logs = 0; });
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; document.body.innerHTML = ''; });
const trs = () => [...host!.querySelectorAll('tbody tr[data-row-idx]')] as HTMLElement[];

describe('TraceLogTable (v0.10.1940)', () => {
  // v0.10.2094 (operatör: "Cluster ile servis yer değiştirsin").
  it('kolon sırası: Time · Service · Message · Cluster · Pod', async () => {
    await mount([row(1)]);
    const heads = [...host!.querySelectorAll('thead th')].map(th => (th.textContent ?? '').replace(/[↓↑⋯]/g, "").trim()).filter(Boolean);
    expect(heads).toEqual(['Time', 'Service', 'Message', 'Cluster', 'Pod']);
  });

  it('exception rozeti + üst satırda sayaç ve Görünüm düğmesi; açılışta log araması yok', async () => {
    await mount([OOM, row(2)]);
    expect(trs()[0].textContent).toContain('▲ OutOfMemoryError');
    expect(host!.textContent).toContain('sayaç');
    expect([...host!.querySelectorAll('button')].some(b => /Görünüm/.test(b.textContent ?? ''))).toBe(true);
    expect(m.logs).toBe(0);
  });

  // v0.10.1941 (operatör: "çekmece yandan açılmasın, eski şekilde inline olsun").
  it('satıra tık → satır tablonun İÇİNDE açılır; yan çekmece yok, arama yok', async () => {
    await mount([OOM, row(2)]);
    const before = host!.querySelectorAll('tbody tr').length;
    await act(async () => { trs()[0].click(); }); await settle();
    expect(host!.querySelectorAll('tbody tr').length).toBe(before + 1);
    expect(document.querySelector('[data-testid="log-detail"]')).toBeNull();
    expect(new URLSearchParams(window.location.search).get('logdoc')).toBeNull();
    expect(m.logs).toBe(0);
  });

  it('sayfa başına satırı aşınca sayfalama', async () => {
    await mount(Array.from({ length: 130 }, (_, i) => row(i + 1)));
    expect(trs()).toHaveLength(100);
    const next = [...host!.querySelectorAll('button')].find(b => b.textContent?.includes('Next'));
    expect(next).toBeTruthy();
    await act(async () => { next!.click(); }); await settle();
    expect(trs()).toHaveLength(30);
  });
});
