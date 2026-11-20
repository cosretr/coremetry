// @vitest-environment jsdom
//
// Inbox.stdColumns — v0.10.2395 (operatör 2026-11-20, onaylı triage mockup'ı
// liste dilimi): v0.10.2121'de kalkan Etki · Güven · Kanıt kolonları geri.
//   • Etki = standardın etki çekirdeği (detay kartıyla aynı): hata oranı
//     Problem'inde "Dakikada N başarısız · M başarılı", pay alt satırda; kısa
//     biçim artık "Ne bozuk" bağlam satırında İKİNCİ KEZ yazılmaz;
//   • Güven = std.confidence rozeti (YÜKSEK yeşil / ORTA sarı / DÜŞÜK nötr),
//     güvene göre sıralanır;
//   • Kanıt = bağlı olay sayısı (eventCount); sayı yoksa soluk "—".
// Saat / saat dilimi bağımsız: zaman hücresi sınanmaz.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// v0.10.2283 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
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
  return { items: [] as unknown[], calls: [] as { sort?: string; dir?: string }[] };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    inbox: async (p: unknown) => (m.calls.push(p as { sort?: string; dir?: string }), {
      items: m.items, total: m.items.length, limit: 300, truncated: false,
      minOccDefault: true, minOcc: 5, hiddenByMinOcc: 0,
      counts: { P1: 2, P2: 0, P3: 3, problem: 0, exception: 1, httperror: 0, anomaly: 0, incident: 1 },
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
  // v0.10.1827 — katlanmış incident satırı sınanıyor: Incidents ana anahtarı AÇIK.
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'editor', features: { incidents: true } }, loading: false }),
}));
vi.mock('@/features/anomalies/ProblemsSection', () => ({
  AlertProblemHost: () => null,
}));

import Inbox from './Inbox';
import { inboxCols } from './inbox/inboxColumns';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;


const IMPACT_REQ = "Dakikada 210 başarısız · 2.890 başarılı (isteklerin %6,8'i)";
const T0 = 1_700_000_000e9;

function std(level: 'high' | 'medium' | 'low', impact: Record<string, unknown>): InboxItem['std'] {
  return {
    family: 'error', kind: 'anomaly.error_rate', subject: 'svc-a', headline: 'svc-a: hata oranı yükseldi',
    impact: { known: true, ...impact },
    since: { startedAt: T0, durationSec: 600, ongoing: true },
    evidence: { sentence: 'eşiğin üstünde', source: 'vm', sampled: false },
    confidence: { level, gates: [{ id: 'G1', name: 'trafik', status: 'pass' }] },
    nextStep: { text: 'x' },
  } as InboxItem['std'];
}

function problem(id: string, over: Partial<InboxItem>): InboxItem {
  return {
    id: `problem:${id}`, kind: 'problem', source: 'Alert rule', priority: 'P2', priorityReason: '',
    severity: 'warning', service: `svc-${id}`, title: `kural ${id}`, description: `olay ${id}`,
    startedAt: T0, lastSeen: T0, status: 'open',
    problem: { id, ruleId: `anomaly:svc-${id}:error_rate`, metric: 'error_rate', value: 1, threshold: 0.5 },
    ...over,
  } as InboxItem;
}

const FAIL_RATE = problem('fr', {
  eventCount: 4,
  std: std('high', {
    primary: { text: IMPACT_REQ, value: 0.068, normal: 0.005 },
    requests: { failedPerMin: 210, okPerMin: 2890, normalFailedPerMin: 12, normalOkPerMin: 3090 },
  }),
});
const PLAIN = problem('pl', { std: std('medium', { primary: { text: '%3,0 hata · normal %0,5 (6×)', value: 3, normal: 0.5 } }) });
const LOW = problem('lo', { eventCount: 1, std: std('low', { primary: { text: 'p95 1,2 sn · normal 300 ms', value: 1.2, normal: 0.3 } }) });
const BARE: InboxItem = {
  id: 'exception:fp-n', kind: 'exception', source: 'Exception', priority: 'P2', priorityReason: '',
  severity: 'warning', service: 'svc-n', title: 'example.Err', description: 'boom',
  startedAt: T0, lastSeen: T0, status: 'new',
  exception: { fingerprint: 'fp-n', type: 'example.Err', message: 'boom', occurrences: 3 },
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(url = '/inbox?kind=problem,exception,httperror,anomaly,incident'): Promise<HTMLElement> {
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

const table = (el: HTMLElement) => el.querySelector('.page-body table') as HTMLTableElement;
const bodyRows = (el: HTMLElement) =>
  [...table(el).querySelectorAll<HTMLTableRowElement>('tbody tr')].filter(r => r.querySelector('input[type=checkbox]'));
const rowOf = (el: HTMLElement, needle: string) => bodyRows(el).find(r => r.textContent?.includes(needle))!;
function cellByHead(el: HTMLElement, r: HTMLTableRowElement, label: string): HTMLTableCellElement {
  const heads = [...table(el).querySelectorAll('thead th')].map(th => th.textContent?.trim() ?? '');
  const i = heads.findIndex(h => h.includes(label));
  expect(i, `${label} başlığı yok: ${heads.join(' | ')}`).toBeGreaterThanOrEqual(0);
  return r.querySelectorAll('td')[i] as HTMLTableCellElement;
}

beforeEach(() => { m.items = [FAIL_RATE, PLAIN, LOW, BARE]; m.calls = []; try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Problems listesi — Etki · Güven · Kanıt (v0.10.2395)', () => {
  it('hata oranı satırı: Etki kısa biçim + pay alt satırda; bağlam satırında ikinci kez yok', async () => {
    const el = await mount();
    const r = rowOf(el, 'svc-fr');
    const impact = cellByHead(el, r, 'Etki');
    expect(impact.querySelector('.std-impact__main')?.textContent).toBe('Dakikada 210 başarısız · 2.890 başarılı');
    expect(impact.querySelector('.std-impact__sub')?.textContent).toContain("isteklerin %6,8'i");
    expect(cellByHead(el, r, 'Ne bozuk').querySelector('.std-ctx')?.textContent ?? '').not.toContain('Dakikada');
  });

  it('düz satır: Etki standardın etki cümlesi aynen', async () => {
    const el = await mount();
    const impact = cellByHead(el, rowOf(el, 'svc-pl'), 'Etki');
    expect(impact.querySelector('.std-impact__main')?.textContent).toBe('%3,0 hata · normal %0,5 (6×)');
  });

  it('Güven rozeti: YÜKSEK yeşil, ORTA sarı, DÜŞÜK nötr; renk yalnız rozette', async () => {
    const el = await mount();
    const badge = (needle: string) => cellByHead(el, rowOf(el, needle), 'Güven').querySelector('.badge')!;
    expect(badge('svc-fr').textContent).toBe('YÜKSEK');
    expect(badge('svc-fr').classList.contains('b-ok')).toBe(true);
    expect(badge('svc-pl').textContent).toBe('ORTA');
    expect(badge('svc-pl').classList.contains('b-warn')).toBe(true);
    expect(badge('svc-lo').textContent).toBe('DÜŞÜK');
    expect(badge('svc-lo').classList.contains('b-gray')).toBe(true);
    expect(badge('svc-fr').getAttribute('title')).toContain('G1');
  });

  it('Kanıt: bağlı olay sayısı "N kanıt"; "N olay" başlığın alt satırında tekrar yok', async () => {
    const el = await mount();
    const r = rowOf(el, 'svc-fr');
    expect(cellByHead(el, r, 'Kanıt').textContent).toBe('4 kanıt');
    expect(cellByHead(el, rowOf(el, 'svc-lo'), 'Kanıt').textContent).toBe('1 kanıt');
    expect(r.textContent).not.toContain('4 olay');
  });

  it('veri yoksa üç kolon da soluk "—" (sayı / güven uydurulmaz)', async () => {
    const el = await mount();
    const r = rowOf(el, 'example.Err');
    for (const h of ['Etki', 'Güven', 'Kanıt']) {
      const c = cellByHead(el, r, h);
      expect(c.textContent, h).toBe('—');
      expect(c.querySelector('.cell-faint'), h).not.toBeNull();
    }
    expect(cellByHead(el, rowOf(el, 'svc-pl'), 'Kanıt').textContent).toBe('—');
  });

  // Sıralama sunucuda (serverSort: tavandan önce tüm aday kümesi; Go
  // sortInboxItems "confidence"); sayfa kolonun kimliğini isteğe taşır.
  it('Güven başlığı sıralanabilir: tık → istek sort=confidence (yüksek önce, desc)', async () => {
    const el = await mount();
    const th = [...table(el).querySelectorAll<HTMLElement>('thead th')].find(h => h.textContent?.includes('Güven'))!;
    const btn = th.querySelector<HTMLElement>('button, [role="button"]') ?? th;
    await act(async () => { btn.click(); });
    await settle();
    expect(m.calls.at(-1)).toMatchObject({ sort: 'confidence', dir: 'desc' });
  });

  it('Güven sıralaması: kolonun sortValue\'su yüksek > orta > düşük (sunucu inboxConfidenceRank ile aynı)', () => {
    const col = inboxCols('tr').find(c => c.id === 'confidence')!;
    const ranked = [LOW, PLAIN, FAIL_RATE].sort((a, b) => Number(col.sortValue!(b)) - Number(col.sortValue!(a)));
    expect(ranked.map(r => r.std?.confidence.level)).toEqual(['high', 'medium', 'low']);
    const ev = inboxCols('tr').find(c => c.id === 'evidence')!;
    expect(ev.numeric).toBe(true);
    expect(ev.sortValue!(BARE)).toBe(0);
  });
});
