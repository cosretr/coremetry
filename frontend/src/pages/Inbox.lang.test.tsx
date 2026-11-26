// @vitest-environment jsdom
//
// v0.10.2283 — pin: arayüz dili İngilizceyken Problems listesi (/inbox: süzgeç
// çubuğu, "Diğer süzgeçler", kolonlar, satır hücreleri, sayım cümlesi, kırpma
// rozetleri, toplu eylem çubuğu ve silme onayı, boş durumlar, alt bağlantılar,
// "Problem değil" politika çubuğu ve gerekçe menüsü) bilinen Türkçe etiket
// ÇİZMEZ; Türkçe arayüzde metin aynen kalır. AnomalyStreamsPage.lang.test
// şeklinde. Sunucu metni (std manşet / kanıt / bağlam) burada İngilizce fikstür.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { InboxItem } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { lang: 'en' as 'en' | 'tr', items: [] as unknown[] };
});
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    inbox: async () => ({
      items: m.items, total: m.items.length + 40, limit: 300, truncated: m.items.length > 0, scanCapped: true,
      minOccDefault: true, minOcc: 5, hiddenByMinOcc: 2, oldestFirstSeen: Date.now() * 1e6 - 3 * 86400e9,
      counts: { P1: 2, P2: 1, P3: 0, problem: 1, exception: 1, httperror: 0, anomaly: 0, incident: 1 },
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
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'admin', features: { incidents: true } }, loading: false }),
}));
vi.mock('@/features/anomalies/ProblemsSection', () => ({ AlertProblemHost: () => null }));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/SavedViewsBar', () => ({ SavedViewsBar: () => null }));

import Inbox from './Inbox';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const NOW_NS = Date.now() * 1e6;
const STD_ROW = {
  id: 'problem:p1', kind: 'problem', source: 'Alert rule', priority: 'P1', severity: 'critical', service: 'svc-a',
  title: 'svc-a error rate', description: 'svc-a error rate above threshold', startedAt: NOW_NS - 3 * 3600e9, lastSeen: NOW_NS,
  status: 'open', category: 'ERROR', ownerTeam: 'team-a', eventCount: 3,
  problem: { id: 'p1', metric: 'error_rate', value: 0.7, threshold: 0.05 },
  std: {
    family: 'error', kind: 'anomaly.error_rate', subject: 'svc-a', headline: 'svc-a: errors keep rising',
    impact: { primary: { text: 'x', value: 0.7, normal: 0.05 }, known: true },
    since: { startedAt: NOW_NS - 3 * 3600e9, durationSec: 5 * 3600 + 40 * 60, ongoing: true, episode: 3 },
    evidence: { sentence: 'above the threshold for 30 min', source: 'vm', sampled: false },
    confidence: { level: 'high', gates: [] }, nextStep: { text: 'x' }, context: [],
  },
} as unknown as InboxItem;
const EXC: InboxItem = {
  id: 'exception:fp1', kind: 'exception', source: 'Exception', priority: 'P1', severity: 'warning', service: 'svc-b',
  title: 'java.net.SocketTimeoutException', description: 'Read timed out', startedAt: NOW_NS - 7200e9, lastSeen: NOW_NS,
  status: 'new', teams: ['team-a', 'team-b'], runbookUrl: 'https://runbook.example/x',
  exception: { fingerprint: 'fp1', type: 'java.net.SocketTimeoutException', message: 'Read timed out', occurrences: 42, spread: 3, spreadServices: ['svc-c'] },
} as InboxItem;
const INC: InboxItem = {
  id: 'incident:inc-1', kind: 'incident', source: 'Incident', priority: 'P2', severity: 'warning', service: 'svc-c',
  title: 'svc-c incident', description: 'svc-c degraded', startedAt: NOW_NS - 600e9, lastSeen: NOW_NS, status: 'open',
  incident: { id: 'inc-1', severity: 'warning', status: 'open', problemCount: 2 },
} as InboxItem;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(url: string): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><ConfirmProvider><Inbox /></ConfirmProvider></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}
// v0.10.2476 — status artık ana çubukta; "Diğer süzgeçler"i açık getiren owner=.
const ALL = '/inbox?kind=problem,exception,httperror,anomaly,incident&prio=P1,P2&status=all&team=team-x&owner=team-x';
async function pickFirstRow(el: HTMLElement) {
  const box = el.querySelector<HTMLInputElement>('.page-body tbody input[type="checkbox"]')!;
  await act(async () => { box.click(); });
  await settle();
}
const snap = (el: HTMLElement) => ({ text: document.body.textContent ?? el.textContent ?? '', html: document.body.innerHTML });

beforeEach(() => { m.lang = 'en'; m.items = [STD_ROW, EXC, INC]; try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

// Bu ekranda bugün Türkçe çizilen etiketler (sunucu verisi değil). v0.10.2287 —
// FacetMultiSelect özeti de çevrildi ("seçili" artık muaf değil).
const TR_LABELS = [
  'Ekip', 'Öncelik', 'Tür', 'Durum', 'Öğrenilen', 'Özne', 'Kategori', 'Servisler', 'Veritabanları', 'Dış kaynak',
  'takım', 'Takım süzgeci', 'tüm öncelikler', 'Problem değil', 'Diğer süzgeçler', 'etkin', 'varsayılan', 'İlk görülme',
  'Ne bozuk', 'Servis', 'Ne zamandan beri', 'İlgilenen', 'açık problem', 'En eskisi', 'gündür', 'kalemden', 'kırpıldı',
  'sırasına göre', 'taranan adaylar', 'Seç:', 'Görünen satırların', 'oluşum', 'servis', 'ekip', 'sahipsiz', 'Alarm',
  'beri', 'sürüyor', 'örneklenmemiş', 'Kaynak:', '. kez', 'Runbook aç', 'Aynı exception', 'Varsayılan kural', 'olay',
  'Güven', 'Kanıt', 'kanıt', 'YÜKSEK', // v0.10.2395
  'Diğer görünümler', 'Çözülenler', 'Yok sayılanlar', 'Kapananlar', 'Susturulanlar', 'Alarm kurallarını', 'Gerçek problem', 'seçili', 'onaylanabilir',
  'atlanacak', 'Onayla', 'Kapat', 'Kalıcı sil', 'Seçimi temizle', 'Durum:',
];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('Problems listesi — İngilizce arayüz (v0.10.2283)', () => {
  it('süzgeçler, kolonlar, satırlar, rozetler ve toplu eylem çubuğu: Türkçe etiket yok', async () => {
    const el = await mount(ALL);
    await pickFirstRow(el);
    const r = snap(el);
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of [
      'Team: all', 'all priorities (3)', 'team: team-x', 'Status', 'Learned', 'Not a problem (0)', 'Subject', 'Services',
      'Databases', 'External sources', 'More filters', ' active)', 'What is broken', 'Since', 'Assignee',
      'Impact', 'Confidence', 'Evidence', // v0.10.2395
      '3 open problems: 2 P1, 1 P2, 0 P3. The oldest has been open for 3 days.',
      '⚠ The filter ran over the scanned candidates — the queue may be larger than this',
      '⚠ Showing the first 3 of 43 items — cut by priority order',
      'VM · not sampled', '5 h 40 min · ongoing · episode 3', '3 events', '42 occurrences', '3 services', '2 teams', 'unowned',
      'Alert', '1 selected', '1 can be acknowledged', 'Acknowledge (1)', 'Real problem (0)', 'Not a problem (0)', 'Close (1)',
      'Delete permanently (1)', 'Clear selection', 'Closed', 'Ignored', 'Open', 'All', 'Manage alert rules',
    ]) expect(r.text, en).toContain(en);
    expect(r.text).toMatch(/since \d{2}[.:]\d{2}/);
    for (const en of ['Select every visible row', 'Select: svc-a error rate', 'Other views', 'Source: VictoriaMetrics · not sampled',
      'The same exception in 3 services at the same time', 'Open runbook', 'Status: open', 'Default rule: groups with 5+ occurrences',
      'Remove the team-x team filter', 'Real problem: java.net.SocketTimeoutException']) {
      expect(r.html, en).toContain(en);
    }
  });

  it('kalıcı silme onayı ve gerekçe menüsü İngilizce', async () => {
    const el = await mount(ALL);
    await pickFirstRow(el);
    const del = el.querySelector<HTMLButtonElement>('[data-testid="bulk-delete"]')!;
    await act(async () => { del.click(); });
    await settle();
    let r = snap(el);
    expect(r.text).toContain('Delete 1 records permanently?');
    expect(r.text).toContain('Problem and exception group rows are deleted from the database');
    expect(r.text).toContain('Cancel');
    expect(leaked(r)).toEqual([]);
    await act(async () => { document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); });
    const noise = el.querySelector<HTMLButtonElement>('button[aria-label^="Not a problem: "]')!;
    await act(async () => { noise.click(); });
    await settle();
    r = snap(el);
    for (const en of ['Why is it not a problem?', 'Expected behaviour', 'Threshold too sensitive', 'Note (optional)',
      'Hide permanently', 'Mark as not a problem', 'Notifications are muted for 7 days']) expect(r.text, en).toContain(en);
    for (const tr of ['Neden problem değil', 'Beklenen davranış', 'Not (isteğe', 'Kalıcı gizle', 'listeden gizler']) expect(r.text, tr).not.toContain(tr);
  });

  it('boş kuyruk ve "Problem değil" görünümü İngilizce', async () => {
    m.items = [];
    // v0.10.2287 — FacetMultiSelect özeti de arayüz dilinde ("N selected"); muafiyet kalktı.
    const leakedHere = leaked;
    let el = await mount('/inbox');
    expect(snap(el).text).toContain('The queue is empty');
    expect(leakedHere(snap(el))).toEqual([]);
    act(() => { root?.unmount(); }); host?.remove();
    el = await mount('/inbox?verdict=noise');
    const r = snap(el);
    expect(r.text).toContain('No rows in this view');
    expect(r.text).toContain('Notifications: sent');
    expect(r.text).toContain('Mute notifications too');
    expect(r.text).not.toContain('Bildirim');
    expect(leakedHere(r)).toEqual([]);
  });
});

describe('Problems listesi — Türkçe arayüzde metin aynen (v0.10.2283)', () => {
  it('sayfa', async () => {
    m.lang = 'tr';
    const el = await mount(ALL);
    await pickFirstRow(el);
    const r = snap(el);
    for (const tr of [
      'Ekip: hepsi', 'tüm öncelikler (3)', 'takım: team-x', 'Durum', 'Öğrenilen', 'Problem değil (0)', 'Özne', 'Servisler',
      'Diğer süzgeçler', ' etkin)', 'Ne bozuk', 'Ne zamandan beri', 'İlgilenen', 'Etki', 'Güven', 'Kanıt', 'YÜKSEK', // v0.10.2395
      '3 açık problem: 2 P1, 1 P2, 0 P3. En eskisi 3 gündür açık.',
      '⚠ Filtre taranan adaylar üzerinde çalıştı — kuyruk bundan büyük olabilir',
      "⚠ 43 kalemden ilk 3'i gösteriliyor — öncelik sırasına göre kırpıldı",
      'VM · örneklenmemiş', '5 sa 40 dk · sürüyor · 3. kez', '3 kanıt', // v0.10.2395: "N olay" Kanıt kolonunda
      '42 oluşum', '3 servis', '2 ekip', 'sahipsiz', 'Alarm',
      '1 seçili', '1 onaylanabilir', 'Onayla (1)', 'Gerçek problem (0)', 'Problem değil (0)', 'Kapat (1)', 'Kalıcı sil (1)',
      'Seçimi temizle', 'Kapananlar', 'Susturulanlar', 'Açık', 'Kapanan', 'Tümü', 'Alarm kurallarını yönet',
    ]) expect(r.text, tr).toContain(tr);
    expect(r.text).toMatch(/\d{2}:\d{2}'(?:ten|den|tan|dan) beri/);
    for (const tr of ['Görünen satırların hepsini seç', 'Seç: svc-a error rate', 'Kaynak: VictoriaMetrics · örneklenmemiş',
      'Aynı exception aynı anda 3 serviste: svc-c (+1)', 'Runbook aç', 'Durum: open', 'Varsayılan kural: 5+ oluşumlu gruplar',
      'team-x takım filtresini kaldır']) expect(r.html, tr).toContain(tr);
  });
});
