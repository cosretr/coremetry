// @vitest-environment jsdom
//
// AnomaliesPage.rowActions — v0.10.1323 (operatör, prod /problems ekranı:
// "Sağdaki Ack/Resolve/Ignore butonları çıkmasın, zaten içinden çıkıyor;
// tabloda gözükmesine gerek yok." + "First seen son olacak şekilde sıralı
// gelsin default").
//
// NE ÇİVİLİYOR:
//   • Exceptions tablosunda satır başı eylem kolonu YOK — admin'de bile hiçbir
//     satırda Ack / Resolve / Ignore / Reopen / Unignore düğmesi, başlıkta
//     "Actions" yok; açılan satır (occurrences paneli) da düğme taşımaz.
//   • Eylemler satırın açtığı detayda (ProblemDetail detay çubuğu) HER durum
//     için var — satırdaki eski kümeyle aynı geçişler (acknowledged → new
//     "Reopen" dahil; o eskiden yalnız satırdaydı).
//   • Varsayılan sıra = firstSeen desc, her sekmede, URL'de sıra yoksa;
//     eski v2 localStorage kaydı (v0.10.831 öncesi mount-yazımı) gölgelemez;
//     URL `s_exception-inbox-v2` ve operatörün v3 kaydı hâlâ kazanır.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { ExceptionGroup, ExceptionGroupState } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[], params: [] as Array<Record<string, unknown>>, role: 'admin' };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    exceptionGroups: (p: unknown) => {
      m.params.push(p as Record<string, unknown>);
      return Promise.resolve({ items: m.items, total: m.items.length });
    },
    exceptionGroupSamples: async () => ({ samples: [] }),
    exceptionSamples: async () => ({ samples: [] }),
    listUsers: async () => [],
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
    serviceNames: async () => ({ names: [], total: 0 }),
    problemVerdicts: async () => ({ verdicts: [] }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: m.role }, loading: false }),
}));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('@/components/ShareButton', () => ({ ShareButton: () => null }));
vi.mock('./ExceptionPodsPanel', () => ({ ExceptionPodsPanel: () => null }));
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));

import ProblemsPage from './AnomaliesPage';
import { ProblemDetail } from './ProblemDetail';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const group = (fp: string, state: ExceptionGroupState): ExceptionGroup => ({
  fingerprint: fp, type: 'java.net.SocketTimeoutException', message: 'Read timed out', service: 'checkout',
  priority: 'P1', state, assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9,
  occurrences: 42, notes: '',
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function render(node: React.ReactNode, url = '/problems'): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><ConfirmProvider>{node}</ConfirmProvider></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

const ACTION_LABELS = ['Ack', 'Acknowledge', 'Resolve', 'Ignore', 'Reopen', 'Unignore'];
const buttonLabels = (el: Element) => [...el.querySelectorAll('button')].map(b => b.textContent?.trim() ?? '');
const lastParams = () => m.params[m.params.length - 1];

beforeEach(() => {
  m.items = []; m.params = []; m.role = 'admin';
  // Node'un yerleşik localStorage'ı bu ortamda yolsuz — Map destekli taklit
  // (resizeFit.contract ile aynı).
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
    clear: () => mem.clear(),
    key: (i: number) => Array.from(mem.keys())[i] ?? null,
    get length() { return mem.size; },
  });
});
afterEach(() => {
  vi.unstubAllGlobals();
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Exceptions tablosu — satırda durum eylemi yok (v0.10.1323)', () => {
  it('admin: hiçbir satırda Ack/Resolve/Ignore/Reopen/Unignore yok; başlıkta Actions yok', async () => {
    m.items = (['new', 'regressed', 'acknowledged', 'resolved', 'ignored'] as const).map((s, i) => group(`fp${i}`, s));
    const el = await render(<ProblemsPage />);
    const table = el.querySelector('.page-body table') as HTMLTableElement;
    const rows = table.querySelectorAll('tbody tr');
    expect(rows.length).toBe(5);
    for (const tr of rows) {
      const hits = buttonLabels(tr).filter(l => ACTION_LABELS.includes(l));
      expect(hits, `satırda eylem düğmesi: ${hits.join(',')}`).toEqual([]);
    }
    expect(table.querySelector('.col-actions')).toBeNull();
    expect(table.querySelector('thead')?.textContent).not.toContain('Actions');
    // Açılan satır (occurrences paneli) da eylem taşımaz; colSpan tüm kolonları kaplar.
    const caret = rows[0].querySelector('td') as HTMLTableCellElement;
    await act(async () => { caret.click(); });
    await settle();
    const detailRow = table.querySelectorAll('tbody tr')[1];
    const td = detailRow.querySelector('td') as HTMLTableCellElement;
    expect(td.colSpan).toBe(table.querySelectorAll('thead th').length);
    expect(buttonLabels(detailRow).filter(l => ACTION_LABELS.includes(l))).toEqual([]);
  });
});

describe('Eylemler satırın açtığı detayda — her durum (v0.10.1323)', () => {
  const cases: Array<[ExceptionGroupState, string[]]> = [
    ['new',          ['Acknowledge', 'Ignore', 'Resolve']],
    ['regressed',    ['Acknowledge', 'Ignore', 'Resolve']],
    ['acknowledged', ['Reopen', 'Ignore', 'Resolve']],
    ['resolved',     ['Reopen']],
    ['ignored',      ['Reopen']],
  ];
  for (const [state, want] of cases) {
    it(`${state}: ${want.join(' / ')}`, async () => {
      const el = await render(<ProblemDetail group={group('fp', state)} isAdmin onBack={() => {}} onChanged={() => {}} />);
      const bar = el.querySelector('.rb-bar') as HTMLElement;
      expect(buttonLabels(bar).filter(l => ACTION_LABELS.includes(l))).toEqual(want);
    });
  }
  it('viewer: detayda durum eylemi yok (salt okunur)', async () => {
    const el = await render(<ProblemDetail group={group('fp', 'acknowledged')} isAdmin={false} onBack={() => {}} onChanged={() => {}} />);
    expect(buttonLabels(el.querySelector('.rb-bar')!).filter(l => ACTION_LABELS.includes(l))).toEqual([]);
  });
});

describe('Varsayılan sıra = First seen DESC (v0.10.1323)', () => {
  for (const tab of ['inbox', 'new', 'acknowledged', 'regressed', 'resolved', 'ignored']) {
    it(`?tab=${tab}: URL'de sıra yoksa sort=firstSeen dir=desc`, async () => {
      await render(<ProblemsPage />, `/problems?tab=${tab}`);
      expect(lastParams()).toMatchObject({ sort: 'firstSeen', dir: 'desc' });
    });
  }
  it('sekmesiz /problems: firstSeen desc; başlık oku First seen sütununda', async () => {
    const el = await render(<ProblemsPage />);
    expect(lastParams()).toMatchObject({ sort: 'firstSeen', dir: 'desc' });
    const sorted = [...el.querySelectorAll('thead th')].find(th => th.getAttribute('aria-sort') === 'descending');
    expect(sorted?.textContent).toContain('First seen');
  });
  it('eski v2 localStorage kaydı (priority) yeni varsayılanı gölgelemez', async () => {
    localStorage.setItem('dt.exception-inbox-v2.sort', JSON.stringify({ id: 'priority', dir: 'desc' }));
    await render(<ProblemsPage />);
    expect(lastParams()).toMatchObject({ sort: 'firstSeen', dir: 'desc' });
  });
  it('operatörün kalıcı seçimi (v3) korunur', async () => {
    localStorage.setItem('dt.exception-inbox-v3.sort', JSON.stringify({ id: 'occurrences', dir: 'desc' }));
    await render(<ProblemsPage />);
    expect(lastParams()).toMatchObject({ sort: 'occurrences', dir: 'desc' });
  });
  it('URL / kayıtlı görünüm sırası kazanır (param adı s_exception-inbox-v2 korunur)', async () => {
    localStorage.setItem('dt.exception-inbox-v3.sort', JSON.stringify({ id: 'occurrences', dir: 'desc' }));
    await render(<ProblemsPage />, '/problems?s_exception-inbox-v2=priority.desc');
    expect(lastParams()).toMatchObject({ sort: 'priority', dir: 'desc' });
  });
});
