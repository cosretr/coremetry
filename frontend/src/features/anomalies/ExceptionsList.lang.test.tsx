// @vitest-environment jsdom
//
// v0.10.2289 — pin: arayüz dili İngilizceyken Exceptions listesi (/problems:
// satırlar, Oracle satırı soluk metni + ipucu, yayılım / +N servis işaretleri,
// sekme ipucu, Oracle çipi, taban şeridi, boş / hata / eşleşme yok durumları,
// Service hunisi, satır altı örnek paneli) bilinen Türkçe etiket ÇİZMEZ; Türkçe
// arayüzde metin aynen kalır. Branding etiketi (oracleGroupLabel, varsayılan
// "Teknik hata") ayar değeri, kapsam dışı. Zaten İngilizce metin iki dilde aynı.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { ExceptionGroup } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { lang: 'en' as 'en' | 'tr', items: [] as unknown[], fail: false, names: [] as string[], namesTotal: 0 };
});
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    exceptionGroups: () => m.fail ? Promise.reject(new Error('CH 500'))
      : Promise.resolve({ items: m.items, total: m.items.length, oracleCount: 2, minOccDefault: false }),
    exceptionGroupSamples: async () => ({ samples: [], windowExhausted: true, scanned: 5 }),
    listUsers: async () => [],
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
    serviceNames: async () => ({ names: m.names, total: m.namesTotal }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'viewer' }, loading: false }),
}));

import ProblemsPage from './AnomaliesPage';
import { oracleFacetLabels, oracleRowDetail, oracleRowTooltip } from './oracleGroup';
import { EXCEPTION_TABS, exceptionTabHint } from './tabs';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const ora = (fp: string, service: string, serviceCount: number): ExceptionGroup => ({
  fingerprint: fp, type: 'E1', message: 'OP_1', service,
  priority: 'P2', state: 'new', assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9, occurrences: 40, notes: '',
  oracle: {
    sourceName: 'src-1', code: 'E1', operation: 'OP_1', channels: [{ name: 'CH1', count: 3 }, { name: 'CH2', count: 1 }],
    services: [{ name: 'svc-a', count: 9 }, { name: 'svc-b', count: 2 }, { name: 'svc-c', count: 1 }].slice(0, serviceCount),
    serviceCount, known: true, lastHour: 4, prevHour: 1,
  },
});
const span = {
  fingerprint: '0a1b2c3d4e5f6a7b', type: 'java.lang.IllegalStateException', message: 'boom', service: 'svc-a',
  priority: 'P1', state: 'new', assignee: '', firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9, occurrences: 42, notes: '',
  spread: 3, spreadServices: ['svc-b', 'svc-c'],
} as ExceptionGroup;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = (ms = 30) => act(async () => { await new Promise(r => setTimeout(r, ms)); });
const snap = () => ({ text: document.body.textContent ?? '', html: document.body.innerHTML });
async function mount(url: string) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><ConfirmProvider><ProblemsPage /></ConfirmProvider></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
}
const close = () => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; };
const serviceFunnel = () => [...document.querySelectorAll('thead th')]
  .find(th => th.querySelector('.tcf-th-l')?.textContent === 'Service')?.querySelector('button.tcf-btn') as HTMLButtonElement | undefined;

async function all() {
  const out = [];
  // Liste: Oracle satırları (+N servis, servis bilinmiyor), yayılım, açık taban şeridi, satır altı örnek paneli, Service hunisi.
  m.items = [ora('ora:aaaaaaaaaaaaaaaa', 'svc-a', 3), ora('ora:bbbbbbbbbbbbbbbb', 'oracle:src-1', 0), span];
  m.names = ['svc-a']; m.namesTotal = 60;
  await mount('/problems?minOcc=10');
  out.push(snap());
  act(() => { (host!.querySelector('tbody tr td.cell-faint') as HTMLElement).click(); });
  await settle();
  await act(async () => { serviceFunnel()?.click(); });
  await settle(250);
  out.push(snap()); close();
  // Service hunisi: eşleşme yok; taban kapalı şeridi (varsayılana dönüş düğmesi).
  m.names = []; m.namesTotal = 0;
  await mount('/problems?minOcc=0');
  await act(async () => { serviceFunnel()?.click(); });
  await settle(250);
  out.push(snap()); close();
  // Boş durumlar, eşleşme yok, hata.
  m.items = [];
  for (const url of ['/problems', '/problems?tab=resolved', '/problems?tab=ignored', '/problems?tab=acknowledged', '/problems?tab=regressed', '/problems?service=svc-a']) {
    await mount(url); out.push(snap()); close();
  }
  m.fail = true;
  await mount('/problems'); out.push(snap()); close();
  m.fail = false;
  return { text: out.map(r => r.text).join('\n'), html: out.map(r => r.html).join('\n') };
}

beforeEach(() => { m.lang = 'en'; m.fail = false; });
afterEach(() => { close(); document.body.innerHTML = ''; });

const TR_LABELS = [
  'Inbox boş', 'sekmesinde', 'gruplar buraya düşer', 'satıra tıkla', 'okunamadı', 'süzgeçle', 'Anomali sekmeleri', 'Ignored hariç',
  'Oracle hata', 'Tümü', 'varsayılan', 'Oracle gruplarını gizle', 'hariç', 'Bir kez düşen', 'servis bulamadı', ' servis<', ' servis\n',
  'servis bilinmiyor', 'Kanal:', 'Servisler:', 'Son 1 sa', 'önceki saat', 'KAPANMIŞ', 'Eşleşen servis yok', 'servisten ilk',
  'aramayı daralt', 'baştan sona', 'aday)',
];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('Exceptions listesi — İngilizce arayüz (v0.10.2289)', () => {
  it('satırlar, süzgeçler, boş / hata durumları, örnek paneli: Türkçe etiket yok', async () => {
    const r = await all();
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of [
      'Teknik hata · src-1 · 3 services', 'Teknik hata · src-1 · service unknown', '+2 services', '3 services',
      'All', 'Teknik hata 2', 'Teknik hata excluded', '5+ (default)', 'First 1 of 60 services — narrow the search',
      'No matching service', 'The group\'s time range was scanned end to end (5 candidates)',
      'Inbox is empty, no groups besides ignored ones — click a row to open its detail',
      'No groups in the "resolved" tab — groups land here when you press Resolve',
      'No groups in the "ignored" tab — groups land here when you press Ignore',
      'No groups in the "acknowledged" tab — groups land here when you Acknowledge',
      'No groups in the "regressed" tab — click a row',
      'No groups in the "inbox" tab match the filter', 'Could not read the exception list: CH 500',
    ]) expect(r.text, en).toContain(en);
    for (const en of ['aria-label="Anomaly tabs"', 'Every state except ignored', 'aria-label="Oracle error groups"',
      'Groups built from Oracle error table rows (code × operation)', 'Span exceptions + Oracle groups (default)',
      'Only Oracle error table groups', 'Hide Oracle groups', 'An exception that fired once is a fact about this window',
      'Trace → service resolution found no service for this operation', 'Group built from Oracle error table rows',
      'Channel: CH1 75% · CH2 25%', 'Services: svc-a, svc-b, svc-c', 'Last 1 h: 4 (previous hour 1)', 'Only CLOSED minutes are counted',
    ]) expect(r.html, en).toContain(en);
  });

  it('saf yardımcılar: lang verilmezse Türkçe aynen', () => {
    const g = ora('ora:aaaaaaaaaaaaaaaa', 'svc-a', 1);
    expect(oracleRowDetail(g, 'L')).toBe('L · src-1 · 1 servis');
    expect(oracleRowDetail(g, 'L', 'en')).toBe('L · src-1 · 1 service');
    expect(oracleRowTooltip(g, 'L').split('\n').slice(2)).toEqual([
      'Kanal: CH1 %75 · CH2 %25', 'Servisler: svc-a', 'Son 1 sa: 4 (önceki saat 1)',
      'Yalnız KAPANMIŞ dakikalar sayılır (özel SQL penceresinden çıkan dakika) — son görülme duvar saatinin birkaç dakika gerisindedir.',
    ]);
    expect(oracleFacetLabels('L', 2, String)).toEqual({ only: 'L 2', exclude: 'L hariç' });
    expect(oracleFacetLabels('L', 2, String, 'en').exclude).toBe('L excluded');
    expect(exceptionTabHint(EXCEPTION_TABS[0])).toBe(EXCEPTION_TABS[0].hint);
    expect(exceptionTabHint(EXCEPTION_TABS[0], 'en')).toBe('Every state except ignored: new + acknowledged + regressed + resolved');
    expect(exceptionTabHint(EXCEPTION_TABS[1], 'en')).toBe(EXCEPTION_TABS[1].hint);
  });
});

describe('Exceptions listesi — Türkçe arayüzde metin aynen (v0.10.2289)', () => {
  it('satırlar, süzgeçler, durumlar', async () => {
    m.lang = 'tr';
    const r = await all();
    for (const tr of [
      'Teknik hata · src-1 · 3 servis', 'Teknik hata · src-1 · servis bilinmiyor', '+2 servis', '3 servis',
      'Tümü', 'Teknik hata 2', 'Teknik hata hariç', '5+ (varsayılan)', '60 servisten ilk 1 — aramayı daralt',
      'Eşleşen servis yok', 'Grubun zaman aralığı baştan sona tarandı (5 aday)',
      'Inbox boş, ignored dışında hiç grup yok — detayı açmak için bir satıra tıkla; durumu orada Acknowledge / Resolve / Ignore ile yönet',
      '"resolved" sekmesinde grup yok — bir grubun detayında Resolve\'a basınca ya da 14 gün yeni oluşum gelmeyince gruplar buraya düşer',
      '"ignored" sekmesinde grup yok — bir grubun detayında Ignore\'a basınca gruplar buraya düşer; ignored grup yeniden tetiklense de sessiz kalır',
      '"acknowledged" sekmesinde grup yok — bir grubu detayında Acknowledge\'layınca (gördün ama henüz düzeltmedin) gruplar buraya düşer; ack\'ten çıkarmak için Resolve\'a bas',
      '"inbox" sekmesinde süzgeçle eşleşen grup yok', 'Exception listesi okunamadı: CH 500',
    ]) expect(r.text, tr).toContain(tr);
    for (const tr of ['aria-label="Anomali sekmeleri"', 'Ignored hariç her durum: new + acknowledged + regressed + resolved',
      'aria-label="Oracle hata grupları"', 'Oracle hata tablosu satırlarından oluşan gruplar (kod × operasyon)',
      'Span exception\'ları + Oracle grupları (varsayılan)', 'Yalnız Oracle hata tablosu grupları', 'Oracle gruplarını gizle',
      'Bir kez düşen bir exception bu pencere hakkında bir olgudur', 'Trace → servis çözümü bu operasyon için servis bulamadı',
      'Oracle hata tablosu satırlarından oluşan grup (kaynak · hata kodu · operasyon)', 'Kanal: CH1 %75 · CH2 %25',
      'Servisler: svc-a, svc-b, svc-c', 'Son 1 sa: 4 (önceki saat 1)', 'Yalnız KAPANMIŞ dakikalar sayılır',
    ]) expect(r.html, tr).toContain(tr);
  });
});
