// @vitest-environment jsdom
//
// TraceCompare.tableStates.test.tsx — v0.10.967 (tablo standardı T12, dilim 5).
//
// "Aligned diff" tablosunun durumları artık tablonun İÇİNDE, başlık yerinde.
// Eski tek Empty ("No spans to align — both traces returned without spans,
// or one of them failed to load") hatayı boşla aynı kutuda söylüyordu; artık
// ayrı türler. Bir yan patladığında diğer yanın satırları ÇİZİLMEZ (tek
// yanlı liste her satırı "only in B" diye yanlış etiketlerdi).
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SpanRow, TraceDetailResponse } from '@/lib/types';

const m = vi.hoisted(() => ({
  byId: {} as Record<string, 'spans' | 'none' | 'error' | 'pending'>,
}));

const span = (traceId: string, id: string, name: string, durNs: number): SpanRow => ({
  traceId, spanId: id, parentSpanId: '', name, kind: 'server', serviceName: 'checkout', hostName: 'h',
  startTime: 1e18, endTime: 1e18 + durNs, durationMs: durNs / 1e6, statusCode: 'ok', statusMessage: '',
  attributes: {}, resourceAttributes: {}, events: null, scopeName: '',
});

vi.mock('@/lib/api', () => ({
  api: {
    trace: (id: string): Promise<TraceDetailResponse> => {
      switch (m.byId[id]) {
        case 'pending': return new Promise(() => {});
        case 'error': return Promise.reject(new Error('boom'));
        case 'none': return Promise.resolve({ traceId: id, spans: [] });
        default: return Promise.resolve({ traceId: id, spans: [span(id, `${id}-root`, 'GET /cart', 5e6)] });
      }
    },
  },
}));
// Şelale bu testin konusu değil (split sekmesi); jsdom'da ağır.
vi.mock('@/components/TraceWaterfall', () => ({ TraceWaterfall: () => null }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

import TraceComparePage from './TraceCompare';

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mountDiff() {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/trace/compare?a=aaa&b=bbb']}><TraceComparePage /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  const tab = [...host.querySelectorAll('button[role="tab"]')].find(b => b.textContent === 'Aligned diff') as HTMLButtonElement;
  await act(async () => { tab.click(); });
  await settle();
}

beforeEach(() => {
  m.byId = { aaa: 'spans', bbb: 'spans' };
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

const table = () => host!.querySelector('.table-wrap table') as HTMLTableElement | null;
const stateRow = () => table()?.querySelector('tbody tr[data-dt-state]') as HTMLTableRowElement | null;

describe('TraceCompare aligned diff durumları tablonun içinde (v0.10.967)', () => {
  it('yükleniyor: başlık + iskelet, özet satırı yok', async () => {
    m.byId.bbb = 'pending';
    await mountDiff();
    expect(table()?.querySelector('thead th')).not.toBeNull();
    expect(stateRow()?.dataset.dtState).toBe('loading');
    expect(host!.textContent).not.toContain('matched');
  });

  it('bir yan patladı: hata satırı yanı söyler, diğer yanın satırları çizilmez', async () => {
    m.byId.bbb = 'error';
    await mountDiff();
    const r = stateRow();
    expect(r?.dataset.dtState).toBe('error');
    expect(r?.textContent).toContain('Trace B okunamadı');
    expect(table()!.querySelectorAll('tbody tr').length).toBe(1);
    expect(host!.textContent).not.toContain('only in A');
  });

  it('iki yan da spansız: boş satırı (hata değil)', async () => {
    m.byId = { aaa: 'none', bbb: 'none' };
    await mountDiff();
    const r = stateRow();
    expect(r?.dataset.dtState).toBe('empty');
    expect(r?.textContent).toContain('Hizalanacak span yok');
  });

  it('iki yan da dolu: satırlar + özet, durum satırı yok', async () => {
    await mountDiff();
    expect(stateRow()).toBeNull();
    expect(table()!.textContent).toContain('GET /cart');
    expect(host!.textContent).toContain('1 matched');
  });
});
