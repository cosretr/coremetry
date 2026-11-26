// @vitest-environment jsdom
//
// v0.10.2478 — Problems kaynak dökümü: aileler çizilir (bilinmeyen anahtar
// ham görünür), pencere değişimi URL'e yazılır ve uca gider, severity notu
// "P1/P2/P3 değil" der, sapan değer (< 15 dk kapananların çoğunluğu) renklenir.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ProblemSourceBreakdownResponse, ProblemSourceFamily } from '@/lib/types';
import { ProblemSourceBreakdownView, ProblemSourceBreakdownEntry } from './ProblemSourceBreakdown';

const m = vi.hoisted(() => ({ calls: [] as { since: string; refresh: boolean }[] }));

const stats = { services: 2, openNow: 1, critical: 3, warning: 1, otherSev: 0, medianSec: 300, long1h: 0 };
const fam = (family: string, count: number, short15: number, extra: Partial<ProblemSourceFamily> = {}): ProblemSourceFamily => ({
  family, count, sharePct: count, short15, short15Pct: (short15 * 100) / count, ...stats,
  churnPairs: 0, churnCount: 0, topServices: [{ service: 'svc-a', count }], metrics: [], ...extra,
});
const body = (since: string): ProblemSourceBreakdownResponse => ({
  since: since as ProblemSourceBreakdownResponse['since'], from: 0, to: 86400, bucketSec: 86400,
  total: 100, openNow: 2, short15: 60,
  series: [{ t: 0, n: 40 }, { t: 86400, n: 60 }],
  families: [
    fam('ml_svc', 70, 56, { churnPairs: 2, churnCount: 9, metrics: [{ metric: 'error_rate', ...stats, count: 70, sharePct: 100, short15: 56, short15Pct: 80 }] }),
    fam('user_rule', 20, 2),
    fam('some_future_family', 10, 2),
  ],
  severityBasis: 'severity',
});

vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' }));
vi.mock('@/lib/api', () => ({
  api: new Proxy({}, {
    get: (_t, k: string) => k === 'problemSourceBreakdown'
      ? (since: string, refresh: boolean) => { m.calls.push({ since, refresh }); return Promise.resolve(body(since)); }
      : () => Promise.resolve(null),
  }),
  isCanceled: () => false,
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
async function mount(url: string, node: React.ReactNode) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><BrowserRouter>{node}</BrowserRouter></QueryClientProvider>); });
  await settle(); await settle();
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove(); root = null; host = null;
  document.body.innerHTML = '';
  m.calls = [];
});

describe('ProblemSourceBreakdown', () => {
  it('aileleri, toplamı ve önem notunu çizer; bilinmeyen aile ham anahtarla görünür', async () => {
    await mount('/inbox?view=breakdown', <ProblemSourceBreakdownView />);
    expect(m.calls[0]).toEqual({ since: '7d', refresh: false });
    const text = document.body.textContent ?? '';
    expect(text).toContain('ML · servis (hata / yanıt süresi)');
    expect(text).toContain('Kullanıcı alarm kuralı');
    expect(text).toContain('some_future_family');
    expect(document.querySelector('[data-testid="bd-total"]')?.textContent).toBe('100 Problem açıldı · şu an 2 açık');
    expect(text).toContain('P1/P2/P3 önceliği değil');
    expect(text).toContain('56 (%80)');
    // Eski "≥ 15 dk sayılsaydı" satırı kapsam dışı (operatör kararı).
    expect(text).not.toContain('sayılsaydı');
    // Renk yalnız sapan değerde: ml_svc'nin < 15 dk hücresi uyarı tonunda, user_rule'unki değil.
    const warnCells = [...document.querySelectorAll('.cell-warn')].map(e => e.textContent);
    expect(warnCells).toContain('56 (%80)');
    expect(warnCells).not.toContain('2 (%10)');
    // Servis bağlantısı listenin mevcut ?service= süzgecine gider.
    expect([...document.querySelectorAll('a')].map(a => a.getAttribute('href'))).toContain('/inbox?service=svc-a&status=all');
  });

  it('pencere değişimi URL\'e yazılır ve uca gider', async () => {
    await mount('/inbox?view=breakdown', <ProblemSourceBreakdownView />);
    const radio = [...document.querySelectorAll('[role="radio"]')].find(e => e.textContent === '30 gün') as HTMLElement;
    expect(radio).toBeTruthy();
    await act(async () => { radio.click(); });
    await settle();
    expect(new URLSearchParams(window.location.search).get('bdw')).toBe('30d');
    expect(m.calls.map(c => c.since)).toContain('30d');
  });

  it('metrik alt düzeyi açılır', async () => {
    await mount('/inbox?view=breakdown', <ProblemSourceBreakdownView />);
    const btn = document.querySelector('button[aria-label^="ML · servis"]') as HTMLButtonElement;
    await act(async () => { btn.click(); });
    expect(document.body.textContent).toContain('error_rate');
  });

  it('giriş: ?view yoksa yalnız bağlantı, uç çağrılmaz', async () => {
    await mount('/inbox', <ProblemSourceBreakdownEntry />);
    expect(document.body.textContent).toContain('Döküm');
    expect(m.calls).toEqual([]);
  });
});
