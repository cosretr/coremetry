// @vitest-environment jsdom
//
// Inbox.rowOpen — v0.10.1032 (operatör: "Anomali ve alert rule'lara
// girdiğimde drawer çıkıyor. Exception gibi detay gözükmüyor.") ve v0.10.1081
// (operatör: "Drawer çıkmasın, problem sayfasında direkt içeriğine
// girebileyim, Exceptions sayfası gibi." + "Problems sayfasında sadece P1'ler
// gözüksün ve first seen'e göre sıralı olsun").
//
// Saf karar lib/inboxHref inboxRowOpen'da tablo testli; bu dosya sayfanın o
// kararı GERÇEKTEN uyguladığını uçtan uca ölçer (fare tıkı ve klavye Enter):
//   • alarm kuralı satırı → ?problem=<id>, YERİNDE tam sayfa;
//   • anomali satırı → ?anomaly=<id>, YERİNDE tam sayfa;
//   • kuyruk gizlenir ama MOUNT'lu kalır (display:none), "← Problems" geri getirir;
//   • incident satırı → /incident?id=<id> tam sayfa (çekmece YOK), geri
//     bağlantı için kuyruk adresi gezinme durumunda;
//   • eski ?item= linki → aynı tam sayfaya yönlendirilir (çekmece YOK);
//   • exception satırı → /problems?exc= (değişmedi);
//   • varsayılan istek prio=P1 + sort=firstSeen desc; açık parametreler aynen.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { InboxItem } from '@/lib/types';
import { setItem, dtSortKey } from '@/lib/storage';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { items: [] as unknown[], calls: [] as Record<string, unknown>[] };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    inbox: async (p: unknown) => {
      m.calls.push(p as Record<string, unknown>);
      return {
        items: m.items, total: m.items.length, limit: 300, truncated: false,
        minOccDefault: true, minOcc: 5, hiddenByMinOcc: 0,
      };
    },
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
    priorityReason: 'kaynak önceliği korundu (kritik incident) · Declared incident, critical',
    description: 'orders-db veritabanında hata oranı %100 (eşik %5), 3 çağıran servis etkilendi',
    incident: { id: 'i1', severity: 'critical', status: 'open' } },
  { ...base, id: 'exception:fp1', kind: 'exception', title: 'SocketTimeout',
    exception: { fingerprint: 'fp1', type: 'SocketTimeout', message: 'Read timed out', occurrences: 42 } },
];

function Loc() {
  const l = useLocation();
  const st = l.state as { backTo?: string } | null;
  return <div data-loc={l.pathname + l.search} data-back={st?.backTo ?? ''} />;
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
              <Route path="/incident" element={<Loc />} />
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
const back = (el: HTMLElement) => el.querySelector('[data-loc]')?.getAttribute('data-back') ?? '';
const params = (el: HTMLElement) => new URLSearchParams(loc(el).split('?')[1] ?? '');
const row = (el: HTMLElement, title: string) =>
  [...el.querySelectorAll<HTMLTableRowElement>('.page-body tbody tr')].find(r => r.textContent?.includes(title))!;
const queueHidden = (el: HTMLElement) => (el.querySelector('.page-body') as HTMLElement | null)?.style.display === 'none';
// Çekmece kabuğu (ui/Drawer) DOM'da hiç olmamalı.
const anyDrawer = (el: HTMLElement) => document.querySelector('[role="dialog"]') ?? el.querySelector('.drawer');

beforeEach(() => { m.items = ROWS; m.calls = []; try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Problems kuyruğu — satır tıkı nereye açılır', () => {
  it('alarm kuralı satırı: ?problem= yerinde tam sayfa, kuyruk gizli ama mount\'lu', async () => {
    const el = await mount();
    act(() => { row(el, 'High error rate').click(); });
    await settle();
    expect(params(el).get('problem')).toBe('p1');
    expect(params(el).get('prio')).toBe('P1,P2,P3'); // süzgeç korunur
    expect(el.querySelector('[data-host="problem"]')?.getAttribute('data-id')).toBe('p1');
    expect(anyDrawer(el)).toBeNull();
    expect(queueHidden(el)).toBe(true);
    expect(row(el, 'High error rate')).toBeTruthy(); // gizli ama DOM'da
  });

  it('anomali satırı: ?anomaly= yerinde tam sayfa; "← Problems" kuyruğa döner', async () => {
    const el = await mount();
    act(() => { row(el, 'POST /pay').click(); });
    await settle();
    expect(params(el).get('anomaly')).toBe('e1');
    expect(el.querySelector('[data-host="anomaly"]')?.getAttribute('data-id')).toBe('e1');
    expect(el.querySelector('[data-host="problem"]')).toBeNull();
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

  it('incident satırı: çekmece YOK — incident tam sayfasına gezinir, geri bağlantı kuyruğa', async () => {
    const el = await mount();
    act(() => { row(el, 'Checkout outage').click(); });
    await settle();
    expect(loc(el)).toBe('/incident?id=i1');
    expect(back(el)).toBe('/inbox?prio=P1,P2,P3');
    expect(anyDrawer(el)).toBeNull();
  });

  it('incident satırı: klavye Enter da tam sayfaya gider', async () => {
    const el = await mount();
    const r = row(el, 'Checkout outage');
    act(() => { r.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); });
    await settle();
    expect(loc(el)).toBe('/incident?id=i1');
  });

  it('incident satırı düz cümleyi gösterir, "Declared incident" gerekçesini değil', async () => {
    const el = await mount();
    const r = row(el, 'Checkout outage');
    expect(r.textContent).toContain('orders-db veritabanında hata oranı %100 (eşik %5), 3 çağıran servis etkilendi');
    expect(r.textContent).not.toContain('Declared incident');
    expect(r.textContent).not.toContain('kaynak önceliği korundu');
  });

  it('satır onay kutusu gezinmez; toplu seçim çalışır', async () => {
    const el = await mount();
    const box = row(el, 'Checkout outage').querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    act(() => { box.click(); });
    await settle();
    expect(loc(el).startsWith('/inbox')).toBe(true);
    expect(el.textContent).toContain('1 seçili');
  });

  it('exception satırı: tam sayfa exception detayına gezinir (değişmedi)', async () => {
    const el = await mount();
    act(() => { row(el, 'SocketTimeout').click(); });
    await settle();
    expect(loc(el)).toBe('/problems?exc=fp1');
  });

  // v0.10.1032 (inceleme) — kapatmak TÜM detay paramlarını siler.
  it('"← Problems" problem + anomaly paramlarının hepsini temizler', async () => {
    const el = await mount('/inbox?prio=P1,P2,P3&problem=p1&anomaly=e1');
    expect(el.querySelector('[data-host="problem"]')).not.toBeNull();
    act(() => { (el.querySelector('[data-host="problem"] button') as HTMLButtonElement).click(); });
    await settle();
    expect(params(el).has('problem')).toBe(false);
    expect(params(el).has('anomaly')).toBe(false);
    expect(el.querySelector('[data-host]')).toBeNull();
    expect(params(el).get('prio')).toBe('P1,P2,P3');
  });

  // v0.10.1032 (inceleme) — tam sayfa açıkken gizli kuyruğun klavye gezinmesi
  // KAPALI. Önce düzeneğin kendisi: detay yokken j + Enter ilk satırı açar.
  const press = (key: string) => act(() => {
    document.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }));
  });
  it('düzenek: detay yokken j + Enter ilk satırı açar', async () => {
    m.items = [ROWS[0]];
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
});

// v0.10.1081 — eski ?item= linkleri (bildirim, kayıtlı görünüm) çekmece
// AÇMAZ; satırın tam sayfasına yönlendirilir.
describe('eski ?item= linki — tam sayfaya yönlendirme', () => {
  it('problem → ?problem= yerinde tam sayfa, ?item= silinir, süzgeç korunur', async () => {
    const el = await mount('/inbox?prio=P1,P2,P3&item=problem:p1');
    expect(params(el).get('problem')).toBe('p1');
    expect(params(el).has('item')).toBe(false);
    expect(params(el).get('prio')).toBe('P1,P2,P3');
    expect(el.querySelector('[data-host="problem"]')?.getAttribute('data-id')).toBe('p1');
    expect(anyDrawer(el)).toBeNull();
  });
  it('incident → /incident?id= tam sayfa', async () => {
    const el = await mount('/inbox?item=incident:i1');
    expect(loc(el)).toBe('/incident?id=i1');
  });
  it('anomaly → ?anomaly=; listede olmayan satır için de (kimlikten çözülür)', async () => {
    m.items = [];
    const el = await mount('/inbox?item=anomaly:zz9');
    expect(params(el).get('anomaly')).toBe('zz9');
    expect(params(el).has('item')).toBe(false);
  });
  it('exception → /problems?exc=', async () => {
    const el = await mount('/inbox?item=exception:fp1');
    expect(loc(el)).toBe('/problems?exc=fp1');
  });
  it('tanınmayan kimlik → yalnız param silinir, kuyruk açık', async () => {
    const el = await mount('/inbox?prio=P1,P2,P3&item=garbage');
    expect(params(el).has('item')).toBe(false);
    expect(queueHidden(el)).toBe(false);
  });
  it('?problem= zaten açıksa o kazanır, ?item= yalnız silinir', async () => {
    const el = await mount('/inbox?problem=p1&anomaly=e1&item=incident:i1');
    expect(loc(el).startsWith('/inbox')).toBe(true);
    expect(el.querySelectorAll('[data-host]').length).toBe(1);
    expect(el.querySelector('[data-host="problem"]')).not.toBeNull();
    expect(params(el).has('item')).toBe(false);
  });
});

// v0.10.1081 — varsayılan görünüm yalnız P1, ilk görülmeye göre (en yeni önce);
// açık URL parametreleri aynen geçerli.
describe('Problems varsayılan isteği', () => {
  const last = () => m.calls[m.calls.length - 1];
  it('parametresiz /inbox → prio=P1, sort=firstSeen, dir=desc', async () => {
    await mount('/inbox');
    expect(last().prio).toBe('P1');
    expect(last().sort).toBe('firstSeen');
    expect(last().dir).toBe('desc');
  });
  it('açık ?prio= ve ?s_inbox= aynen gider', async () => {
    await mount('/inbox?prio=P1,P2,P3&s_inbox=priority.desc');
    expect(last().prio).toBe('P1,P2,P3');
    expect(last().sort).toBe('priority');
    expect(last().dir).toBe('desc');
  });
  it('bayat sıralama kimliği varsayılana düşer', async () => {
    await mount('/inbox?s_inbox=bogus.asc');
    expect(last().sort).toBe('firstSeen');
    expect(last().dir).toBe('desc');
  });
  it('kişisel (localStorage) sıralama parametresiz adresi EZMEZ', async () => {
    setItem(dtSortKey('inbox'), { id: 'lastSeen', dir: 'asc' });
    await mount('/inbox');
    expect(last().sort).toBe('firstSeen');
  });
  it('"tüm öncelikler" tek tıkla P1,P2,P3 yazar', async () => {
    const el = await mount('/inbox');
    const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.startsWith('tüm öncelikler'));
    expect(btn).toBeTruthy();
    act(() => { btn!.click(); });
    await settle();
    expect(params(el).get('prio')).toBe('P1,P2,P3');
    expect(last().prio).toBe('P1,P2,P3');
  });
});
