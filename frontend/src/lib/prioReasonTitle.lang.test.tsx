// @vitest-environment jsdom
//
// v0.10.2303 — Exceptions listesi ve servis dikkat şeridindeki öncelik
// rozeti ipucu (title) anahtardan İngilizce. Pin:
//   - EN: ipucu anahtardan (Go fikstürü), Türkçe kalmaz; anahtarsız eski satır
//     sunucu metni + " (TR)" işareti;
//   - TR: ipucu sunucu metniyle bayt bayt aynı ("P1 — <gerekçe>").
import { describe, it, expect, vi, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import type { ExceptionGroup, InboxItem, StdMsg } from '@/lib/types';
import { prioReasonTitle, stdMsgText } from './problemStandardView';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { lang: 'en' as 'en' | 'tr', inbox: [] as unknown[], groups: [] as unknown[] };
});
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    inbox: async () => ({ items: m.inbox, truncated: false, scanCapped: false }),
    exceptionGroups: async () => ({ items: m.groups, total: m.groups.length, oracleCount: -1 }),
    listUsers: async () => [],
    servicesMetadata: async () => ({}),
    savedViews: async () => [],
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'viewer' }, loading: false }),
  useIncidentsEnabled: () => true,
}));

import { ServiceAttentionStrip } from '@/components/ServiceAttentionStrip';
import ProblemsPage from '@/features/anomalies/AnomaliesPage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const ROOT = resolve(__dirname, '../../..');
type Fx = { name: string; tr: string; msg: StdMsg };
const FX: Fx[] = JSON.parse(readFileSync(resolve(ROOT, 'internal/api/testdata/priority_reason_msgs.golden.json'), 'utf8'));
const fx = (name: string) => FX.find(f => f.name === name)!;
const BURST = fx('ex-burst-ended');
const ORA = FX.find(f => f.msg.k === 'prio.ora.p2')!;
const OLD = 'eski satır: anahtarsız gerekçe';
const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;

const item = (id: string, reason: string, msg?: StdMsg): InboxItem => ({
  id: `exception:${id}`, kind: 'exception', source: 'Exception', priority: 'P1', priorityReason: reason, priorityReasonMsg: msg,
  severity: 'critical', service: 'svc-a', title: 'E', description: '', startedAt: 1, lastSeen: 1, status: 'open',
  exception: { fingerprint: id, type: 'java.lang.IllegalStateException', message: 'boom', occurrences: 3, state: 'new' },
} as InboxItem);
const group = (fp: string, reason: string, msg?: StdMsg): ExceptionGroup => ({
  fingerprint: fp, type: 'java.lang.IllegalStateException', message: 'boom', service: 'svc-a',
  priority: 'P2', priorityReason: reason, priorityReasonMsg: msg, state: 'new', assignee: '',
  firstSeen: 1_700_000_000e9, lastSeen: 1_700_000_600e9, occurrences: 42, notes: '',
} as ExceptionGroup);

let host: HTMLDivElement | null = null;
let root: Root | null = null;
async function mount(node: React.ReactNode, url = '/') {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={[url]}><ConfirmProvider>{node}</ConfirmProvider></MemoryRouter></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 30)); });
}
const close = () => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; };
const badgeTitles = () => [...document.querySelectorAll('span.badge')].map(b => b.getAttribute('title') ?? '').filter(t => /^P[123]/.test(t));
const rowTitles = () => [...document.querySelectorAll('a.svc-att-row')].map(a => a.getAttribute('title') ?? '');

afterEach(() => { close(); document.body.innerHTML = ''; m.lang = 'en'; });

async function strip() {
  m.inbox = [item('fp-1', BURST.tr, BURST.msg), item('fp-2', OLD)];
  await mount(<ServiceAttentionStrip service="svc-a" fallbackProblems={[]} />);
  const r = { badges: badgeTitles(), rows: rowTitles() };
  close();
  return r;
}
async function list() {
  m.groups = [group('fp-1', BURST.tr, BURST.msg), group('fp-2', ORA.tr, ORA.msg), group('fp-3', OLD)];
  await mount(<ProblemsPage />, '/problems');
  const r = badgeTitles();
  close();
  return r;
}

describe('öncelik ipucu — İngilizce arayüz (v0.10.2303)', () => {
  it('servis şeridi: rozet + satır ipucu anahtardan; anahtarsız satır TR işaretli', async () => {
    const r = await strip();
    const en = stdMsgText(BURST.msg, 'en')!;
    expect(r.badges).toEqual([`P1 — ${en}`, `P1 — ${OLD} (TR)`]);
    expect(r.rows).toEqual([en, `${OLD} (TR)`]);
    expect(TR_CHARS.test(en)).toBe(false);
  });
  it('Exceptions listesi: rozet ipucu anahtardan; anahtarsız satır TR işaretli', async () => {
    const r = await list();
    expect(r).toEqual([`P2 — ${stdMsgText(BURST.msg, 'en')}`, `P2 — ${stdMsgText(ORA.msg, 'en')}`, `P2 — ${OLD} (TR)`]);
    expect(r.slice(0, 2).some(s => TR_CHARS.test(s) || /son 1 sa|olay|önce/.test(s))).toBe(false);
  });
  it('saf yardımcı: boş gerekçe boş kalır, lang verilmezse TR aynen', () => {
    expect(prioReasonTitle('', undefined, 'en')).toBe('');
    expect(prioReasonTitle(undefined, undefined, 'en')).toBe('');
    expect(prioReasonTitle(BURST.tr, BURST.msg)).toBe(BURST.tr);
    expect(prioReasonTitle(OLD, undefined)).toBe(OLD);
  });
});

describe('öncelik ipucu — Türkçe arayüzde aynen (v0.10.2303)', () => {
  it('şerit ve liste: sunucu metni bayt bayt', async () => {
    m.lang = 'tr';
    const s = await strip();
    expect(s.badges).toEqual([`P1 — ${BURST.tr}`, `P1 — ${OLD}`]);
    expect(s.rows).toEqual([BURST.tr, OLD]);
    m.lang = 'tr';
    expect(await list()).toEqual([`P2 — ${BURST.tr}`, `P2 — ${ORA.tr}`, `P2 — ${OLD}`]);
  });
});
