// @vitest-environment jsdom
//
// Logs.loadStatus.test.tsx — v0.10.1834 (Log Search Faz 1 dilim 1.11).
// Akışlı yükleme göstergesi: satırlar histogramı BEKLEMEZ; "✓ Satırlar 0,4 sn"
// Server-Timing `total`ından; "Sorguyu iptal et" uçuştaki liste + histogram
// isteklerini iptal eder (sinyal abort), tablo eski satırı göstermez,
// ↻ yeniden çalıştırır; boşta düğme yer tutar ama görünmez; aria-live polite.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { LogsResponse, LogRow } from '@/lib/types';
import Logs from './Logs';

const T0 = 1759494780000000000;
const row = (i: number): LogRow => ({
  id: i + 1, timestamp: T0 + i * 1e6, service: 'orders-api', severityText: 'INFO', severityNumber: 9,
  body: `order ${i} accepted`, traceId: '', spanId: '', attributes: {}, resourceAttributes: {},
} as unknown as LogRow);
const m = vi.hoisted(() => ({
  logsHang: false,
  logsCalls: 0,
  signals: [] as { kind: string; signal: AbortSignal }[],
}));
const hang = (kind: string, signal: AbortSignal) => {
  m.signals.push({ kind, signal });
  return new Promise<never>((_r, reject) => {
    signal.addEventListener('abort', () => reject(Object.assign(new Error('canceled'), { name: 'CanceledError' })), { once: true });
  });
};
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (_p: unknown, signal: unknown, onTiming: unknown) => {
      m.logsCalls++;
      if (m.logsHang) return hang('logs', signal as AbortSignal);
      (onTiming as ((h: string | null) => void) | undefined)?.('cache;desc="MISS", es;dur=310, total;dur=400');
      return Promise.resolve({ total: 3, logs: [row(0), row(1), row(2)], nextCursor: '' } as unknown as LogsResponse);
    },
    // Histogram hiç bitmez: satırlar onu beklememeli.
    logsTimeseries: (_p: unknown, signal: unknown) => hang('ts', signal as AbortSignal),
    getDisplayPref: () => Promise.resolve({ key: 'logs', display: null }),
    getLogsSearchSettings: () => Promise.resolve({ maxSample: 2000, defaults: { maxSample: 2000 }, min: 10, hardCap: 5000, sampleDefault: 500 }),
    clusters: () => Promise.resolve({ clusters: [] }),
    logsFields: () => Promise.resolve({ fields: [], total: 0, types: {} }),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }),
    isCanceled: (e: unknown) => (e as Error)?.name === 'CanceledError',
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { id: 'u-op', username: 'op', role: 'viewer' }, loading: false }),
}));
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Element.prototype.scrollIntoView = function scrollIntoView() {};

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 40)); });
const RANGE = 'custom:1759494780000-1759497240000';
async function mountLogs(url: string) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><BrowserRouter><ConfirmProvider><Logs /></ConfirmProvider></BrowserRouter></QueryClientProvider>);
  });
  await settle();
}
const chip = (k: string) => host!.querySelector(`[data-testid="load-${k}"]`)?.textContent ?? null;
const cancelBtn = () => [...host!.querySelectorAll('button')].find(b => b.textContent === 'Sorguyu iptal et') as HTMLButtonElement;
const bodyRows = () => host!.querySelectorAll('table.logtbl-dense > tbody > tr[data-row-idx]').length;

beforeEach(() => { m.logsHang = false; m.logsCalls = 0; m.signals = []; });
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; document.body.innerHTML = ''; });

describe('/logs akışlı yükleme göstergesi (v0.10.1834)', () => {
  it('satırlar histogramı beklemez; süre Server-Timing total', async () => {
    await mountLogs(`/logs?range=${RANGE}`);
    expect(bodyRows()).toBe(3);
    expect(chip('rows')).toBe('✓ Satırlar 0,4 sn');
    expect(host!.querySelector('[data-testid="load-rows"]')!.getAttribute('title')).toContain('ES 0,3 sn');
    expect(chip('histogram')).toBe('◌ Histogram…');
    expect(chip('fields')).toBeNull(); // alan açılmadı → çip yok
    const live = host!.querySelector('[data-testid="logs-load-status"] [role="status"]')!;
    expect(live.getAttribute('aria-live')).toBe('polite');
    expect(cancelBtn().className).not.toContain('invisible'); // histogram hâlâ uçuşta
  });

  it('iptal: liste + histogram sinyali kesilir; eski satır yok; ↻ yeniden çalıştırır', async () => {
    m.logsHang = true;
    await mountLogs(`/logs?range=${RANGE}`);
    expect(chip('rows')).toBe('◌ Satırlar…');
    await act(async () => { cancelBtn().click(); });
    await settle();
    const aborted = (k: string) => m.signals.filter(s => s.kind === k).every(s => s.signal.aborted);
    expect(m.signals.some(s => s.kind === 'logs') && aborted('logs')).toBe(true);
    expect(m.signals.some(s => s.kind === 'ts') && aborted('ts')).toBe(true);
    expect(chip('rows')).toBe('⊘ Satırlar iptal');
    expect(chip('histogram')).toBe('⊘ Histogram iptal');
    expect(host!.textContent).toContain('Sorgu iptal edildi — satırlar yüklenmedi.');
    expect(host!.textContent).toContain('histogram iptal edildi');
    expect(bodyRows()).toBe(0);
    // Boşta: düğme yer tutar ama görünmez ve odak dışı (düzen kaymaz).
    expect(cancelBtn().className).toContain('invisible');
    expect(cancelBtn().getAttribute('tabindex')).toBe('-1');
    m.logsHang = false;
    const retry = [...host!.querySelectorAll('button')].find(b => /Retry|Yeniden/i.test(b.textContent ?? '')) as HTMLButtonElement;
    expect(retry).toBeTruthy();
    const before = m.logsCalls;
    await act(async () => { retry.click(); });
    await settle();
    expect(m.logsCalls).toBe(before + 1);
    expect(bodyRows()).toBe(3);
    expect(chip('rows')).toBe('✓ Satırlar 0,4 sn');
  });
});
