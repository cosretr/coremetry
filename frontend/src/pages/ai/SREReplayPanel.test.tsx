// @vitest-environment jsdom
// SREReplayPanel.test.tsx — CoSRE doğruluk ölçümü A2: karolar
// (puan, dağılım, erişim, kararsızlık), rapor tablosu, Problem satırında
// etiket ↔ koşu notları, admin "Replay başlat" → POST (varyant dahil) ve
// haftalık aç/kapa → PUT; viewer formu görmez.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SREReplayDetail, SREReplayListResponse, SREReplaySummary } from '@/lib/types';

const m = vi.hoisted(() => ({ role: 'viewer', started: [] as unknown[], schedules: [] as unknown[] }));

const summary: SREReplaySummary = { problems: 2, skipped: 1, runs: 4, pending: 0, graded: 4, errored: 0, score: 67.5,
  dist: { bullseye: 2, on_target: 1, miss: 0, nowhere: 1 }, reachKnown: 4, reached: 3, reachRate: 0.75,
  multiRun: 2, unstable: 1, meanSpread: 32.5, maxSpread: 65 };
const base = { reportId: 'r1', state: 'done' as const, trigger: 'manual' as const, by: 'admin@x', days: 30, repeats: 2, limit: 20,
  from: 1.7e18, to: 1.71e18, variant: {}, createdAt: 1.71e18, found: 3 };
const list: SREReplayListResponse = {
  reports: [{ ...base, summary }, { ...base, reportId: 'r0', createdAt: 1.70e18, summary: { ...summary, score: 50 } }],
  schedule: { weekly: false, days: 30, repeats: 2 }, enabled: true, grades: { bullseye: 100, on_target: 65, miss: 35, nowhere: 0 },
};
const detail: SREReplayDetail = { ...base, summary, items: [
  { problemId: 'p1', caseId: 'p:p1', service: 'checkout', cause: 'orders-db bağlantı havuzu doldu', category: 'capacity', culprit: 'db:orders',
    startedAt: 1.7e18, mean: 100, spread: 0, runs: [
      { rep: 1, state: 'done', grade: 'bullseye', score: 100, why: 'aynı neden', reach: true, reachBy: 'evidence', conclusion: '1. Veritabanı yavaş' },
      { rep: 2, state: 'done', grade: 'bullseye', score: 100, reach: true, reachBy: 'evidence' }] },
  { problemId: 'p2', caseId: 'p:p2', service: 'billing', cause: 'deploy 4.1 hatalı config', category: 'config', startedAt: 1.7e18, mean: 32.5, spread: 65,
    unstable: true, runs: [
      { rep: 1, state: 'done', grade: 'on_target', score: 65, reach: true, reachBy: 'judge' },
      { rep: 2, state: 'done', grade: 'nowhere', score: 0, reach: false, reachBy: 'judge' }] },
  { problemId: 'p3', caseId: 'p:p3', service: 'cart', cause: 'gürültü', category: 'noise', startedAt: 1.7e18, state: 'skipped', reason: 'gürültü — notlanmaz',
    mean: null, spread: 0, runs: [] },
] };

vi.mock('@/lib/api', () => ({
  api: {
    sreReplayReports: vi.fn(async () => list),
    sreReplayReport: vi.fn(async () => detail),
    startSREReplay: vi.fn(async (b: unknown) => { m.started.push(b); return { ...detail, reportId: 'r2' }; }),
    putSREReplaySchedule: vi.fn(async (b: unknown) => { m.schedules.push(b); return b; }),
  },
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: m.role }, loading: false }),
}));

import { SREReplayPanel } from './SREReplayPanel';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.started = []; m.schedules = []; });

async function flush() {
  for (let i = 0; i < 4; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

async function render() {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={['/ai']}><SREReplayPanel /></MemoryRouter></QueryClientProvider>);
  });
  await flush();
  return host;
}

const btn = (el: HTMLElement, text: string) => Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes(text));

describe('SREReplayPanel', () => {
  it('viewer: karolar, eğilim, Problem satırları; form yok', async () => {
    m.role = 'viewer';
    const el = await render();
    const txt = el.textContent ?? '';
    expect(txt).toContain('67,5');
    expect(txt).toContain('%75');
    expect(txt).toContain('3/4 koşu');
    expect(txt).toContain('1/2');
    expect(txt).toContain('50 → 67,5');
    expect(txt).toContain('Tam isabet 2');
    expect(txt).toContain('orders-db bağlantı havuzu doldu');
    expect(txt).toContain('atlandı — gürültü');
    const badges = Array.from(el.querySelectorAll('.sre-eval-hyp')).map(b => b.textContent);
    expect(badges).toContain('1. Hedefte');
    expect(badges).toContain('2. Çok uzak');
    expect(btn(el, 'Replay başlat')).toBeUndefined();
    expect(btn(el, 'Haftalık')).toBeUndefined();
  });

  it('admin: Replay başlat varyantla POST, haftalık aç → PUT', async () => {
    m.role = 'admin';
    const el = await render();
    const variant = Array.from(el.querySelectorAll('select')).find(s => Array.from(s.options).some(o => o.value === 'contextPack'))!;
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!;
      set.call(variant, 'contextPack');
      variant.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await act(async () => { btn(el, 'Replay başlat')!.click(); });
    await flush();
    expect(m.started).toEqual([{ days: 30, repeats: 2, limit: 20, variant: { contextPack: true } }]);
    await act(async () => { btn(el, 'Haftalık: kapalı')!.click(); });
    await flush();
    expect(m.schedules).toEqual([{ weekly: true, days: 30, repeats: 2 }]);
  });
});
