// @vitest-environment jsdom
// ExchangesPanel.test.tsx — v0.10.1646 (operatör onaylı): /ai "CoSRE
// etkileşimleri" pencerenin TÜM sorularını gösterir. Önceki semptom: LIMIT 200
// eski turları gizliyordu. Çivilenen kablolama: URL'deki sayfa/arama isteğe
// gider (offset, q), arama debounce'lu sunucuya + URL'e yazılır ve sayfayı
// sıfırlar, Pager "Next" offset'i ilerletir ve URL'e yazar, toplam görünür,
// CSV aynı süzgeçlerle sayfasız istenir.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { AIExchange, AIExchangesPage } from '@/lib/types';

const mk = (i: number): AIExchange => ({
  exchangeId: `ex-${i}`, createdAt: 1_761_811_200_000_000_000 - i * 1e9, tier: 'loop', question: `soru ${i}`,
  status: 'ok', durationMs: 10, llmCalls: 0, inputTokens: 0, outputTokens: 0, models: [], calls: [], userId: 'u-1',
});

const m = vi.hoisted(() => ({
  calls: [] as Record<string, unknown>[],
  csv: [] as Record<string, unknown>[],
  total: 250,
}));

vi.mock('@/lib/api', () => ({
  api: {
    aiExchanges: vi.fn(async (p: Record<string, unknown>): Promise<AIExchangesPage> => {
      m.calls.push(p);
      const off = Number(p.offset ?? 0);
      const n = Math.max(0, Math.min(100, m.total - off));
      return { rows: Array.from({ length: n }, (_, i) => mk(off + i)), total: m.total, hasMore: off + 100 < m.total };
    }),
    aiExchangeUsers: vi.fn(async () => [{ user: 'u-1', turns: 3 }]),
    aiExchangesCSV: vi.fn(async (p: Record<string, unknown>) => { m.csv.push(p); }),
  },
}));

import { ExchangesPanel } from './ExchangesPanel';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let loc = '';
afterEach(() => {
  act(() => root?.unmount()); host?.remove(); host = null; root = null;
  m.calls = []; m.csv = []; m.total = 250; loc = '';
});

function LocProbe() {
  const l = useLocation();
  loc = l.search;
  return null;
}

async function flush(ms = 0) {
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, ms)); });
}

async function render(url: string) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <ExchangesPanel range={{ preset: 'custom', fromMs: 1_761_800_000_000, toMs: 1_761_811_200_000 }} onOpenCall={() => {}} />
          <LocProbe />
        </MemoryRouter>
      </QueryClientProvider>);
  });
  await flush();
  return host;
}

const last = () => m.calls[m.calls.length - 1];
const btn = (h: HTMLElement, text: string) => [...h.querySelectorAll('button')].find(b => b.textContent?.trim().startsWith(text));

describe('ExchangesPanel — sunucu sayfası + arama + CSV', () => {
  it('URL durumu isteğe gider (sayfa → offset, arama → q, kademe)', async () => {
    const h = await render('/ai?xq=yavaş&xpage=1&xtier=loop&fb=down');
    expect(last()).toMatchObject({ limit: 100, offset: 100, q: 'yavaş', tier: 'loop', feedback: 'down' });
    expect(h.querySelectorAll('tbody tr.cv-row')).toHaveLength(100);
    expect(h.textContent).toContain('250 tur');
    const input = h.querySelector('input[aria-label="Soru içinde ara"]') as HTMLInputElement;
    expect(input.value).toBe('yavaş');
  });

  it('arama debounce sonrası sunucuya gider, URL\'e yazılır ve sayfayı sıfırlar', async () => {
    const h = await render('/ai?xpage=2&range=6h');
    const input = h.querySelector('input[aria-label="Soru içinde ara"]') as HTMLInputElement;
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      setter.call(input, 'Timeout');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    // Debounce dolmadan istek yok.
    expect(last().q).toBeUndefined();
    await flush(120);
    expect(last()).toMatchObject({ q: 'Timeout', offset: 0 });
    const sp = new URLSearchParams(loc);
    expect(sp.get('xq')).toBe('Timeout');
    expect(sp.has('xpage')).toBe(false);
    expect(sp.get('range')).toBe('6h'); // yabancı parametre korunur
  });

  it('Next offset\'i ilerletir ve URL\'e yazar', async () => {
    const h = await render('/ai');
    expect(last()).toMatchObject({ offset: 0 });
    await act(async () => { btn(h, 'Next')!.click(); });
    await flush();
    expect(last()).toMatchObject({ offset: 100 });
    expect(new URLSearchParams(loc).get('xpage')).toBe('1');
  });

  it('CSV aynı süzgeçleri sayfasız ister', async () => {
    const h = await render('/ai?xq=abc&xuser=u-1&xpage=1');
    await act(async () => { btn(h, '↓ CSV')!.click(); });
    await flush();
    expect(m.csv).toHaveLength(1);
    expect(m.csv[0]).toMatchObject({ q: 'abc', user: 'u-1' });
    expect('offset' in m.csv[0]).toBe(false);
    expect('limit' in m.csv[0]).toBe(false);
    expect(btn(h, '↓ CSV')!.getAttribute('title')).toContain('50.000');
  });

  it('boş pencere: boş durum + CSV kapalı + Pager yok', async () => {
    m.total = 0;
    const h = await render('/ai');
    expect(h.textContent).toContain('Bu pencerede CoSRE etkileşimi yok');
    expect((btn(h, '↓ CSV') as HTMLButtonElement).disabled).toBe(true);
    expect(btn(h, 'Next')).toBeUndefined();
  });
});
