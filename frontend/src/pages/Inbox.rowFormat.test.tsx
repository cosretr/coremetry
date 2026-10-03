// @vitest-environment jsdom
//
// Inbox.rowFormat — v0.10.1084 (operatör: "tekilleştir, Exceptions'taki format
// güzel"). Problems kuyruğu satırı Exceptions listesinin biçiminde:
//   • kolonlar: Priority · Problem · Service · Occurrences · First seen ·
//     Last seen · Assignee (ayrı Source kolonu yok);
//   • başlık hücresi ORTAK bileşen (TriageTitleCell): 1. satır kalın başlık
//     (incident/problem → olayın cümlesi, exception → tip; mono + hata rengi)
//     + satır içi durum çipi, 2. satır soluk ayrıntı (kaynak · incident adı /
//     exception mesajı);
//   • sunucunun katladığı incident satırında Occurrences = "2 problem";
//   • gerekçe ("Declared incident", "kaynak önceliği korundu") satırda yok.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
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
  return { items: [] as unknown[] };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    inbox: async () => ({
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
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'editor' }, loading: false }),
}));
vi.mock('@/features/anomalies/ProblemsSection', () => ({
  ProblemsSection: () => null,
  AlertProblemHost: () => null,
}));

import Inbox from './Inbox';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const SENT = 'couchbase orders (cb-node-1) veritabanında hata oranı %100.0 (eşik %5), 3 çağıran servis etkilendi';
const INCIDENT: InboxItem = {
  id: 'incident:inc-1', kind: 'incident', source: 'Incident', priority: 'P1',
  priorityReason: 'bağlı problem: kaynak önceliği korundu (db-health:*) · Declared incident, warning',
  severity: 'warning', service: 'db:couchbase@orders', subjectKind: 'db',
  title: 'db:couchbase@orders — DB health · couchbase', description: SENT,
  startedAt: 1_700_000_000e9, lastSeen: 1_700_000_900e9, status: 'open', category: 'ERROR',
  incident: { id: 'inc-1', severity: 'warning', status: 'open', problemCount: 2 },
};
const EXC: InboxItem = {
  id: 'exception:fp1', kind: 'exception', source: 'Exception', priority: 'P1', priorityReason: 'burst',
  severity: 'warning', service: 'checkout', title: 'java.net.SocketTimeoutException', description: 'Read timed out',
  startedAt: 1_700_000_100e9, lastSeen: 1_700_000_700e9, status: 'new',
  exception: { fingerprint: 'fp1', type: 'java.net.SocketTimeoutException', message: 'Read timed out', occurrences: 42 },
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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

const table = (el: HTMLElement) => el.querySelector('.page-body table') as HTMLTableElement;
const rowOf = (el: HTMLElement, id: string) =>
  [...table(el).querySelectorAll<HTMLTableRowElement>('tbody tr')].find(r => r.querySelector(`input[aria-label]`) && r.textContent?.includes(id))!;

beforeEach(() => { m.items = [INCIDENT, EXC]; try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Problems satırı — Exceptions biçimi (v0.10.1084)', () => {
  it('kolonlar Exceptions listesinin düzeninde; ayrı Source kolonu yok', async () => {
    const el = await mount();
    const heads = [...table(el).querySelectorAll('thead th')].map(th => th.textContent?.trim() ?? '');
    const labels = ['Priority', 'Problem', 'Service', 'Occurrences', 'First seen', 'Last seen', 'Assignee'];
    let at = -1;
    for (const l of labels) {
      // Sıralama oku başlığın başında ya da sonunda olabilir (sayısal kolon).
      const i = heads.findIndex((h, k) => k > at && h.includes(l));
      expect(i, `${l} başlığı sırada yok: ${heads.join(' | ')}`).toBeGreaterThan(at);
      at = i;
    }
    expect(heads.some(h => h.includes('Source'))).toBe(false);
  });

  it('katlanmış incident: kalın cümle + durum çipi, soluk kaynak · ad, "2 problem", gerekçe yok', async () => {
    const el = await mount();
    const r = rowOf(el, SENT);
    const cells = r.querySelectorAll('td');
    const titleCell = cells[2];
    const head = titleCell.querySelector('.triage-title')!;
    expect(head.classList.contains('triage-title--code')).toBe(false);
    expect(head.querySelector('.dt-trunc')?.textContent).toBe(SENT);
    expect(head.textContent).toContain('open'); // durum çipi başlık satırında
    expect(titleCell.querySelector('.triage-sub')?.textContent).toBe('Incident · db:couchbase@orders — DB health · couchbase');
    expect(cells[3].textContent).toContain('orders'); // Service kolonu başlıktan SONRA
    expect(cells[4].textContent).toBe('2 problem');
    expect(r.textContent).not.toContain('Declared incident');
    expect(r.textContent).not.toContain('kaynak önceliği korundu');
  });

  it('exception: tip mono + hata rengi sınıfı, mesaj mono soluk satır, oluşum sayısı', async () => {
    const el = await mount();
    const r = rowOf(el, 'SocketTimeoutException');
    const cells = r.querySelectorAll('td');
    const head = cells[2].querySelector('.triage-title')!;
    expect(head.classList.contains('triage-title--code')).toBe(true);
    expect(head.querySelector('.dt-trunc')?.textContent).toBe('java.net.SocketTimeoutException');
    const sub = cells[2].querySelector('.triage-sub')!;
    expect(sub.classList.contains('mono')).toBe(true);
    expect(sub.textContent).toBe('Read timed out');
    expect(cells[4].textContent).toBe('42');
  });

  it('iki liste AYNI başlık bileşenini kullanır (ikinci tasarım yok)', () => {
    const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');
    expect(read('./Inbox.tsx')).toContain('<TriageTitleCell');
    expect(read('../features/anomalies/AnomaliesPage.tsx')).toContain('<TriageTitleCell');
    const css = read('../styles/globals.css');
    expect(css).toMatch(/\.triage-title \{[^}]*font-size: 11\.5px/);
    expect(css).toMatch(/\.triage-sub \{[^}]*font-size: 10\.5px[^}]*color: var\(--text3\)/);
  });
});
