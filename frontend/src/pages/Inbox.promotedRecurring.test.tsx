// @vitest-environment jsdom
//
// Inbox.promotedRecurring — v0.10.1054.
//
// Operatör: "Anomaliden terfi eden problem de 'yinelenen' kuralına uysun; bugün
// deploy'a hâlâ eski kurala göre bağlanıyor."
//
// NE ÇİVİLİYOR (Problems kuyruğu, birleşik akış): anomaliden terfi etmiş
// Problem satırı anomali satırıyla AYNI nötr "yinelenen" işaretini taşır.
// Kural tutunca (gece işi 8. bölüm) sunucu deploy çipini göndermez, deploy'u
// nötr priorDeploy'da gönderir → satırda işaret + ipucunda renksiz deploy metni
// (hiçbir şey kaybolmaz); vaka A (2. bölüm) deploy atfını KORUR → çip + işaret,
// anomali satırının kendi davranışıyla aynı. Alarm kuralı satırı alan gelse
// bile işaretsiz; tek bölümlü terfi satırı işaretsiz.
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
  AlertProblemHost: () => null,
}));
vi.mock('@/features/anomalies/AnomalyEventDetail', () => ({ AnomalyEventHost: () => null }));
vi.mock('@/components/InboxTriageDrawer', () => ({ InboxTriageDrawer: () => null }));

import Inbox from './Inbox';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
Element.prototype.scrollIntoView = function scrollIntoView() {};
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const base = {
  source: 's', priority: 'P2' as const, priorityReason: '', severity: 'warning', service: 'batch-svc',
  description: '', startedAt: 1_700_000_000e9, lastSeen: 1_700_000_600e9, status: 'open',
};
const FIRST = 1_699_827_200e9;
// v0.10.1055 — tel şekli (chstore.RecentDeploy): `service` YOK. Eski fikstür
// alanı uyduruyordu ve ipucundaki "undefined v…" hatasını gizliyordu.
const deploy = { version: 'v2.0.0', timeUnixNs: 1_699_999_400e9, ageSeconds: 600 };
// v0.10.1054 inceleme senaryosu: gece işi 7 gece, deploy, 8. bölüm — kural deploy'u
// "olası neden" saymadı; sunucu onu nötr priorDeploy'da gönderir (çip yok).
const PRIOR = { version: 'v2.0.0', timeUnixNs: 1_699_999_400e9, ageSeconds: 600 };
const ROWS: InboxItem[] = [
  { ...base, id: 'problem:n', kind: 'problem', title: 'Anomaly · nightly-job', priorDeploy: PRIOR,
    problem: { id: 'n', ruleId: 'anomaly-auto:0123456789abcdef', metric: 'anomaly_ratio', value: 5, threshold: 3, episodeCount: 8, firstStartedAt: FIRST } },
  { ...base, id: 'problem:a', kind: 'problem', title: 'Anomaly · broken-by-deploy', recentDeploy: deploy,
    problem: { id: 'a', ruleId: 'anomaly-auto:1123456789abcdef', metric: 'anomaly_ratio', value: 5, threshold: 3, episodeCount: 2, firstStartedAt: FIRST } },
  { ...base, id: 'problem:f', kind: 'problem', title: 'Anomaly · fresh-op', recentDeploy: deploy,
    problem: { id: 'f', ruleId: 'anomaly-auto:2123456789abcdef', metric: 'anomaly_ratio', value: 5, threshold: 3 } },
  { ...base, id: 'problem:r', kind: 'problem', title: 'High error rate', recentDeploy: deploy,
    problem: { id: 'r', ruleId: 'builtin:error_rate', metric: 'error_rate', value: 9, threshold: 1, episodeCount: 3, firstStartedAt: 1 } },
  { ...base, id: 'anomaly:e', kind: 'anomaly', title: 'trace_op · nightly-job',
    anomaly: { id: 'e', kind: 'trace_op', pattern: 'nightly-job', peakRatio: 5, currentRatio: 4, episodeCount: 8, firstStartedAt: FIRST } },
];

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
        <MemoryRouter initialEntries={['/inbox?prio=P1,P2,P3']}>
          <ConfirmProvider><Inbox /></ConfirmProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host;
}

const row = (el: HTMLElement, title: string) =>
  [...el.querySelectorAll<HTMLTableRowElement>('.page-body tbody tr')].find(r => r.textContent?.includes(title))!;
const mark = (tr: HTMLElement) => tr.querySelector('[data-recurring]') as HTMLElement | null;
const deployChip = (tr: HTMLElement) => [...tr.querySelectorAll('.badge')].find(b => b.textContent?.includes('deploy v2.0.0'));

beforeEach(() => { m.items = ROWS; try { localStorage.clear(); } catch { /* jsdom */ } });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Problems kuyruğu — terfi Problem\'i anomali satırıyla aynı işaret (v0.10.1054)', () => {
  it('(b) gece işi 8. bölüm: "yinelenen", çip yok, deploy ipucunda NÖTR — atılmaz', async () => {
    const el = await mount();
    const p = row(el, 'Anomaly · nightly-job');
    expect(mark(p)?.textContent).toBe('yinelenen');
    expect(mark(p)?.className).toBe('badge b-gray');
    expect(mark(p)?.getAttribute('data-recurring')).toBe('8');
    expect(mark(p)?.title).toMatch(/^Yinelenen anomali: bu 8\. kez, ilk kez /);
    expect(mark(p)?.title).toMatch(/\ndeploy v2\.0\.0 10 dk önce — öncesinde de görülüyordu$/);
    expect(deployChip(p)).toBeUndefined();
    // Anomali satırı aynı işaret (Problems kuyruğu anomali satırına deploy
    // okuması yapmaz — orada deploy metni yok, bugünkü gibi).
    const a = row(el, 'trace_op · nightly-job');
    expect(mark(a)?.getAttribute('data-recurring')).toBe('8');
    expect(mark(p)?.title.startsWith(mark(a)!.title)).toBe(true);
  });

  it('vaka A (2. bölüm): deploy atfı KORUNUR — çip + işaret birlikte', async () => {
    const el = await mount();
    const p = row(el, 'broken-by-deploy');
    expect(deployChip(p)).toBeTruthy();
    expect(mark(p)?.getAttribute('data-recurring')).toBe('2');
  });

  it('tek bölümlü terfi ve alarm kuralı (alan gelse bile) işaretsiz, çipleri yerinde', async () => {
    const el = await mount();
    for (const t of ['fresh-op', 'High error rate']) {
      expect(mark(row(el, t)), t).toBeNull();
      expect(deployChip(row(el, t)), t).toBeTruthy();
    }
  });
});

// v0.10.1055 (operatör: "Okdir") — deploy çipinin ipucu "undefined v2.0.0 — …"
// başlıyordu: InboxItem.recentDeploy tipinde `service` vardı ama sunucu
// chstore.RecentDeploy gönderiyor (version / timeUnixNs / ageSeconds). Servis
// satırın kendisinden gelir.
describe('Problems kuyruğu — deploy çipi ipucu (v0.10.1055)', () => {
  it('ipucu servisi ve sürümü taşır, "undefined" asla', async () => {
    const el = await mount();
    for (const t of ['broken-by-deploy', 'fresh-op', 'High error rate']) {
      const title = deployChip(row(el, t))?.getAttribute('title') ?? '';
      expect(title, t).toMatch(/^batch-svc v2\.0\.0 — /);
      expect(title, t).not.toContain('undefined');
    }
  });
});
