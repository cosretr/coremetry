// @vitest-environment jsdom
//
// TraceLogTable — v0.10.1940: trace sayfasının log sekmesi Log Search'ün
// tarayıcı-yanı parçalarını alır (exception rozeti, detay çekmecesi, Görünüm ▾,
// sayfalama). Açılışta log araması YAPILMAZ (satırlar trace paketinden gelir).
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
  it('exception rozeti + üst satırda sayaç ve Görünüm düğmesi; açılışta log araması yok', async () => {
    await mount([OOM, row(2)]);
    expect(trs()[0].textContent).toContain('▲ OutOfMemoryError');
    expect(host!.textContent).toContain('sayaç');
    expect([...host!.querySelectorAll('button')].some(b => /Görünüm/.test(b.textContent ?? ''))).toBe(true);
    expect(m.logs).toBe(0);
  });

  it('satıra tık → detay çekmecesi (URL logdoc) ve Exception bölümü', async () => {
    await mount([OOM, row(2)]);
    await act(async () => { trs()[0].click(); }); await settle();
    expect(new URLSearchParams(window.location.search).get('logdoc')).toBe('1');
    expect(document.querySelector('[data-testid="log-detail"]')).not.toBeNull();
    expect(document.querySelector('[data-testid="log-exception"]')!.textContent).toContain('OutOfMemoryError');
    expect(m.logs).toBe(0); // çevre loglar yalnız istenince
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
