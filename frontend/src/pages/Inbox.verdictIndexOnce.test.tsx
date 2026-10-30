// @vitest-environment jsdom
//
// Inbox.verdictIndexOnce — v0.10.1626 (kuyruk maddesi, perf denetimi §10 i).
//
// BELİRTİ: /inbox her satırda ProblemVerdictActions(row) çiziyordu; her satır
// kendi useProblemVerdicts + useSetProblemVerdict örneğini açıp kararları
// satır başına useMemo içinde verdictIndex(...) ile YENİDEN diziyordu —
// N satır = N harita inşası + N mutation gözlemcisi (300 satırlık sayfa,
// her karar yazımı / refetch'te tekrar).
//
// NE ÇİVİLİYOR: dizin tablo düzeyinde BİR kez kurulur (satır sayısından
// bağımsız), mutation tek örnek; satır kendi kararını prop olarak alır.
// Davranış aynı: kararlı satır `is-set`, yalnız tıklanan satır yükleniyor,
// yalnız o satır hata gösterir.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { InboxItem } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[], verdicts: [] as unknown[], put: [] as unknown[], putFail: false, putHold: false };
});
const spy = vi.hoisted(() => ({ index: 0, mutHook: 0 }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    inbox: () => Promise.resolve({
      items: m.items, total: m.items.length, limit: 300, truncated: false,
      minOccDefault: true, minOcc: 5, hiddenByMinOcc: 0,
    }),
    problemVerdicts: () => Promise.resolve({ verdicts: m.verdicts }),
    putProblemVerdict: (body: unknown) => {
      m.put.push(body);
      if (m.putHold) return new Promise(() => {});
      if (m.putFail) return Promise.reject(new Error('boom'));
      return Promise.resolve({ verdicts: m.verdicts });
    },
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'admin' }, loading: false }),
}));
vi.mock('@/features/anomalies/ProblemsSection', () => ({
  ProblemsSection: () => null,
  AlertProblemHost: () => null,
}));
vi.mock('@/lib/problemVerdict', async (orig) => {
  const real = await orig<typeof import('@/lib/problemVerdict')>();
  return {
    ...real,
    verdictIndex: (...a: Parameters<typeof real.verdictIndex>) => { spy.index++; return real.verdictIndex(...a); },
  };
});
vi.mock('@/lib/queries', async (orig) => {
  const real = await orig<typeof import('@/lib/queries')>();
  return { ...real, useSetProblemVerdict: () => { spy.mutHook++; return real.useSetProblemVerdict(); } };
});

import Inbox from './Inbox';
import { exceptionSignature } from '@/lib/problemVerdict';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const item = (id: string): InboxItem => ({
  id: `exception:${id}`, kind: 'exception', source: 'Exception', priority: 'P1', priorityReason: 'burst',
  severity: 'error', service: 'checkout', title: `SocketTimeout ${id}`, description: '',
  startedAt: 1_700_000_000e9, lastSeen: 1_700_000_600e9, status: 'new',
  exception: { fingerprint: id, type: 'SocketTimeout', message: 'Read timed out', occurrences: 42 },
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/inbox']}><ConfirmProvider><Inbox /></ConfirmProvider></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}
const rows = (el: HTMLElement) => Array.from(el.querySelectorAll('.page-body table tbody tr:not([data-dt-state])'));
const rowOf = (el: HTMLElement, fp: string) => rows(el).find(x => x.textContent?.includes(`SocketTimeout ${fp}`))!;
const realBtn = (row: Element) =>
  Array.from(row.querySelectorAll('button')).find(b => b.textContent?.includes('Gerçek problem')) as HTMLButtonElement;

const N = 60;
beforeEach(() => {
  m.items = Array.from({ length: N }, (_, i) => item(`fp${i}`));
  m.verdicts = [{ signature: exceptionSignature({ fingerprint: 'fp3' }), verdict: 'real', at: 1 }];
  m.put = []; m.putFail = false; m.putHold = false;
  spy.index = 0; spy.mutHook = 0;
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Inbox — karar dizini tablo düzeyinde bir kez (v0.10.1626)', () => {
  it(`${N} satır: verdictIndex ve mutation örneği satır sayısından bağımsız`, async () => {
    const el = await mount();
    expect(rows(el).length).toBe(N);
    expect(spy.index, 'satır başına dizin inşası').toBeLessThan(N / 4);
    expect(spy.mutHook, 'satır başına mutation örneği').toBeLessThan(N / 4);
  });

  it('davranış aynı: kararlı satır is-set; yalnız tıklanan satır yükleniyor', async () => {
    const el = await mount();
    const setRows = rows(el).filter(x => x.querySelector('.pv-row.is-set'));
    expect(setRows.length).toBe(1);
    expect(setRows[0].textContent).toContain('SocketTimeout fp3');
    expect(realBtn(setRows[0]).disabled).toBe(true);

    m.putHold = true;
    await act(async () => { realBtn(rowOf(el, 'fp7')).click(); });
    await settle();
    expect(m.put).toHaveLength(1);
    expect((m.put[0] as { signature: string }).signature).toBe(exceptionSignature({ fingerprint: 'fp7' }));
    expect(realBtn(rowOf(el, 'fp7')).getAttribute('aria-busy')).toBe('true');
    expect(realBtn(rowOf(el, 'fp8')).getAttribute('aria-busy'), 'başka satır yükleniyor olmamalı').toBeNull();
  });

  it('hata yalnız tıklanan satırda', async () => {
    m.putFail = true;
    const el = await mount();
    await act(async () => { realBtn(rowOf(el, 'fp9')).click(); });
    await settle();
    expect(el.querySelectorAll('.pv-row [role="alert"]').length).toBe(1);
    expect(rowOf(el, 'fp9').querySelector('[role="alert"]')).not.toBeNull();
  });
});
