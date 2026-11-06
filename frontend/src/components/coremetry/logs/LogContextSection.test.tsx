// @vitest-environment jsdom
//
// coremetry/logs/LogContextSection — v0.10.1881 (Log Search Faz 3 dilim 3.2).
// Kapalı başlar → istek YOK; açılınca tek context isteği (±5, pod kapsamı);
// basamaklar ±5 → ±50 → ±200 (üstü yok); "Aynı servis" pod'suz ister; pivot
// işaretli; komşu satıra tık onu açar; degraded dürüst.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LogRow, LogsContextResponse } from '@/lib/types';

const m = vi.hoisted(() => ({ calls: [] as Record<string, unknown>[], next: null as unknown }));
vi.mock('@/lib/api', () => ({
  api: {
    logsContext: (p: Record<string, unknown>, signal?: AbortSignal) => {
      m.calls.push({ ...p, signal: !!signal });
      return Promise.resolve(m.next);
    },
  },
}));
import { LogContextSection, CONTEXT_RUNGS } from './LogContextSection';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const TS = 1759494800000000000;
const row = (id: number, dt: number, body: string, pod = 'orders-api-7d9f6-x2k4p'): LogRow => ({
  id, timestamp: TS + dt, severity: 9, severityText: 'INFO', body, serviceName: 'orders-api', traceId: '', spanId: '',
  attributes: { 'kubernetes.pod_name': pod }, resourceAttributes: {},
});
const PIVOT = { ...row(1, 0, 'java.lang.OutOfMemoryError: Java heap space'), severity: 17, severityText: 'ERROR' };
let root: Root | null = null; let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });
beforeEach(() => {
  m.calls = [];
  m.next = { pivotTs: TS, service: 'orders-api', before: [row(0, -10, 'GC overhead 92%')], after: [PIVOT, row(2, 10, 'Request failed with 500')] } satisfies LogsContextResponse;
});
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; });
function mount(onOpenRow = vi.fn()) {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => root!.render(<QueryClientProvider client={qc}><LogContextSection pivot={PIVOT} env="prod" onOpenRow={onOpenRow} /></QueryClientProvider>));
  return onOpenRow;
}
const btn = (text: string) => [...host!.querySelectorAll('button')].find(b => b.textContent?.includes(text)) as HTMLButtonElement | undefined;

describe('LogContextSection', () => {
  it('kapalı başlar: istek yok', () => {
    mount();
    expect(m.calls).toHaveLength(0);
    expect(host!.querySelector('[data-testid="log-context-rows"]')).toBeNull();
  });

  it('açılınca tek istek (±5, pod, sinyal); pivot işaretli; komşuya tık açar', async () => {
    const open = mount();
    await act(async () => { (host!.querySelector('[data-testid="log-context-toggle"]') as HTMLButtonElement).click(); });
    await settle();
    expect(m.calls).toEqual([{ ts: TS, service: 'orders-api', env: 'prod', pod: 'orders-api-7d9f6-x2k4p', n: 5, signal: true }]);
    const items = [...host!.querySelectorAll('[data-testid="log-context-rows"] li')];
    expect(items.map(li => li.getAttribute('aria-current'))).toEqual([null, 'true', null]);
    await act(async () => { btn('Request failed')!.click(); });
    expect(open).toHaveBeenCalledWith(expect.objectContaining({ id: 2 }));
  });

  it('basamaklar ±5 → ±50 → ±200, üstü yok; Aynı servis pod göndermez', async () => {
    expect(CONTEXT_RUNGS).toEqual([5, 50, 200]);
    mount();
    await act(async () => { (host!.querySelector('[data-testid="log-context-toggle"]') as HTMLButtonElement).click(); });
    await settle();
    await act(async () => { btn('±50')!.click(); }); await settle();
    await act(async () => { btn('±200')!.click(); }); await settle();
    expect(m.calls.map(c => c.n)).toEqual([5, 50, 200]);
    expect(btn('Daha geniş')).toBeUndefined();
    const svc = [...host!.querySelectorAll('[role="radio"]')].find(r => r.textContent?.includes('Aynı servis')) as HTMLElement;
    await act(async () => { svc.click(); }); await settle();
    expect(m.calls.at(-1)).toMatchObject({ n: 200, pod: undefined });
  });

  it('degraded dürüst: satır listesi yok, uyarı var', async () => {
    m.next = { pivotTs: TS, service: 'orders-api', before: [], after: [], degraded: true, reason: 'log backend slow/unreachable' };
    mount();
    await act(async () => { (host!.querySelector('[data-testid="log-context-toggle"]') as HTMLButtonElement).click(); });
    await settle();
    expect(host!.querySelector('[data-testid="log-context-rows"]')).toBeNull();
    expect(host!.textContent).toContain('çevre okunamadı');
  });
});
