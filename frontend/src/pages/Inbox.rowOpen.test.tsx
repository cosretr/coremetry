// @vitest-environment jsdom
//
// Inbox.rowOpen — v0.10.1032 (operatör: "Anomali ve alert rule'lara
// girdiğimde drawer çıkıyor. Exception gibi detay gözükmüyor.").
//
// Saf karar lib/inboxHref inboxRowOpen'da tablo testli; bu dosya sayfanın o
// kararı GERÇEKTEN uyguladığını uçtan uca ölçer (fare tıkı ve klavye Enter):
//   • alarm kuralı satırı → ?problem=<id>, YERİNDE tam sayfa (çekmece yok);
//   • anomali satırı → ?anomaly=<id>, YERİNDE tam sayfa (çekmece yok);
//   • kuyruk gizlenir ama MOUNT'lu kalır (display:none), "← Problems" geri getirir;
//   • incident satırı ve eski ?item= linki → çekmece (değişmedi);
//   • exception satırı → /problems?exc= (değişmedi).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { InboxItem } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[] };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    inbox: async () => ({
      items: m.items, total: m.items.length, limit: 300, truncated: false,
      minOccDefault: true, minOcc: 5, hiddenByMinOcc: 0,
    }),
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
    problemVerdicts: async () => ({ verdicts: [] }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'editor' }, loading: false }),
}));
vi.mock('@/features/anomalies/ProblemsSection', () => ({
  ProblemsSection: () => null,
  AlertProblemHost: ({ id, onBack }: { id: string; onBack: () => void }) => (
    <div data-host="problem" data-id={id}><button onClick={onBack}>back</button></div>
  ),
}));
vi.mock('@/features/anomalies/AnomalyEventDetail', () => ({
  AnomalyEventHost: ({ id, onBack }: { id: string; onBack: () => void }) => (
    <div data-host="anomaly" data-id={id}><button onClick={onBack}>back</button></div>
  ),
}));
vi.mock('@/components/InboxTriageDrawer', () => ({
  InboxTriageDrawer: ({ item }: { item?: InboxItem }) => <div data-drawer={item?.id ?? '(yok)'} />,
}));

import Inbox from './Inbox';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
// jsdom'da scrollIntoView yok; tablo gezinmesi seçili satırı görünüme kaydırır.
Element.prototype.scrollIntoView = function scrollIntoView() {};
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const base = {
  source: 's', priority: 'P1' as const, priorityReason: '', severity: 'warning', service: 'checkout',
  description: '', startedAt: 1_700_000_000e9, lastSeen: 1_700_000_600e9, status: 'open',
};
const ROWS: InboxItem[] = [
  { ...base, id: 'problem:p1', kind: 'problem', title: 'High error rate',
    problem: { id: 'p1', ruleId: 'r1', metric: 'error_rate', value: 9, threshold: 1 } },
  { ...base, id: 'anomaly:e1', kind: 'anomaly', title: 'trace_op · POST /pay',
    anomaly: { id: 'e1', kind: 'trace_op', pattern: 'POST /pay', peakRatio: 4, currentRatio: 2 } },
  { ...base, id: 'incident:i1', kind: 'incident', title: 'Checkout outage',
    incident: { id: 'i1', severity: 'sev2', status: 'open' } },
  { ...base, id: 'exception:fp1', kind: 'exception', title: 'SocketTimeout',
    exception: { fingerprint: 'fp1', type: 'SocketTimeout', message: 'Read timed out', occurrences: 42 } },
];

function Loc() {
  const l = useLocation();
  return <div data-loc={l.pathname + l.search} />;
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(url = '/inbox?prio=P1,P2,P3'): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}>
          <ConfirmProvider>
            <Routes>
              <Route path="/inbox" element={<><Inbox /><Loc /></>} />
              <Route path="/problems" element={<Loc />} />
            </Routes>
          </ConfirmProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

const loc = (el: HTMLElement) => el.querySelector('[data-loc]')?.getAttribute('data-loc') ?? '';
const params = (el: HTMLElement) => new URLSearchParams(loc(el).split('?')[1] ?? '');
const row = (el: HTMLElement, title: string) =>
  [...el.querySelectorAll<HTMLTableRowElement>('.page-body tbody tr')].find(r => r.textContent?.includes(title))!;
const queueHidden = (el: HTMLElement) => (el.querySelector('.page-body') as HTMLElement | null)?.style.display === 'none';

beforeEach(() => { m.items = ROWS; try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Problems kuyruğu — satır tıkı nereye açılır (v0.10.1032)', () => {
  it('alarm kuralı satırı: ?problem= yerinde tam sayfa, çekmece YOK, kuyruk gizli ama mount\'lu', async () => {
    const el = await mount();
    act(() => { row(el, 'High error rate').click(); });
    await settle();
    expect(params(el).get('problem')).toBe('p1');
    expect(params(el).get('prio')).toBe('P1,P2,P3'); // süzgeç korunur
    expect(el.querySelector('[data-host="problem"]')?.getAttribute('data-id')).toBe('p1');
    expect(el.querySelector('[data-drawer]')).toBeNull();
    expect(queueHidden(el)).toBe(true);
    expect(row(el, 'High error rate')).toBeTruthy(); // gizli ama DOM'da
  });

  it('anomali satırı: ?anomaly= yerinde tam sayfa, çekmece YOK; "← Problems" kuyruğa döner', async () => {
    const el = await mount();
    act(() => { row(el, 'POST /pay').click(); });
    await settle();
    expect(params(el).get('anomaly')).toBe('e1');
    expect(el.querySelector('[data-host="anomaly"]')?.getAttribute('data-id')).toBe('e1');
    expect(el.querySelector('[data-host="problem"]')).toBeNull();
    expect(el.querySelector('[data-drawer]')).toBeNull();
    expect(queueHidden(el)).toBe(true);
    act(() => { (el.querySelector('[data-host="anomaly"] button') as HTMLButtonElement).click(); });
    await settle();
    expect(params(el).has('anomaly')).toBe(false);
    expect(params(el).get('prio')).toBe('P1,P2,P3');
    expect(queueHidden(el)).toBe(false);
    expect(el.querySelector('[data-host]')).toBeNull();
  });

  it('klavye Enter fare tıkıyla AYNI yere açar', async () => {
    const el = await mount();
    const r = row(el, 'POST /pay');
    act(() => { r.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); });
    await settle();
    expect(params(el).get('anomaly')).toBe('e1');
  });

  it('incident satırı: çekmece (değişmedi)', async () => {
    const el = await mount();
    act(() => { row(el, 'Checkout outage').click(); });
    await settle();
    expect(params(el).get('item')).toBe('incident:i1');
    expect(el.querySelector('[data-drawer]')?.getAttribute('data-drawer')).toBe('incident:i1');
    expect(el.querySelector('[data-host]')).toBeNull();
  });

  it('exception satırı: tam sayfa exception detayına gezinir (değişmedi)', async () => {
    const el = await mount();
    act(() => { row(el, 'SocketTimeout').click(); });
    await settle();
    expect(loc(el)).toBe('/problems?exc=fp1');
  });

  it('eski ?item= linki hâlâ çekmeceyi açar (problem satırı bile)', async () => {
    const el = await mount('/inbox?prio=P1,P2,P3&item=problem:p1');
    expect(el.querySelector('[data-drawer]')?.getAttribute('data-drawer')).toBe('problem:p1');
    expect(el.querySelector('[data-host]')).toBeNull();
  });

  // v0.10.1032 (inceleme) — kapatmak TÜM detay paramlarını siler: elle
  // yazılmış bir link "← Problems"ten sonra öteki detaya ya da çekmeceye
  // sıçramaz.
  it('"← Problems" problem + anomaly + item paramlarının hepsini temizler', async () => {
    const el = await mount('/inbox?prio=P1,P2,P3&problem=p1&anomaly=e1&item=incident:i1');
    expect(el.querySelector('[data-host="problem"]')).not.toBeNull();
    act(() => { (el.querySelector('[data-host="problem"] button') as HTMLButtonElement).click(); });
    await settle();
    expect(params(el).has('problem')).toBe(false);
    expect(params(el).has('anomaly')).toBe(false);
    expect(params(el).has('item')).toBe(false);
    expect(el.querySelector('[data-host]')).toBeNull();
    expect(el.querySelector('[data-drawer]')).toBeNull();
    expect(params(el).get('prio')).toBe('P1,P2,P3');
  });

  // v0.10.1032 (inceleme) — tam sayfa açıkken gizli kuyruğun klavye gezinmesi
  // KAPALI. Önce düzeneğin kendisi: detay yokken j + Enter ilk satırı açar.
  const press = (key: string) => act(() => {
    document.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }));
  });
  it('düzenek: detay yokken j + Enter ilk satırı açar', async () => {
    const el = await mount();
    press('j'); press('Enter');
    await settle();
    expect(params(el).get('problem')).toBe('p1');
  });
  it('detay açıkken j + Enter gizli kuyruktan satır AÇMAZ', async () => {
    const el = await mount('/inbox?prio=P1,P2,P3&anomaly=e1');
    press('j'); press('Enter');
    await settle();
    expect(params(el).get('anomaly')).toBe('e1');
    expect(params(el).has('problem')).toBe(false);
    expect(el.querySelector('[data-host="anomaly"]')).not.toBeNull();
  });

  it('?problem= ile ?anomaly= birlikte gelirse yalnız BİR tam sayfa çizilir; çekmece bastırılır', async () => {
    const el = await mount('/inbox?problem=p1&anomaly=e1&item=incident:i1');
    expect(el.querySelectorAll('[data-host]').length).toBe(1);
    expect(el.querySelector('[data-host="problem"]')).not.toBeNull();
    expect(el.querySelector('[data-drawer]')).toBeNull();
  });
});
