// @vitest-environment jsdom
//
// v0.10.2312 — pin: arayüz dili İngilizceyken servis dikkat şeridi
// (ServiceAttentionStrip), üst çubuk arama / link kopyalama düğmeleri ve
// TrendDelta rozeti bilinen Türkçe metin ÇİZMEZ (metin + title / aria-label);
// Türkçe arayüzde metin aynen.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { InboxItem } from '@/lib/types';
import { ServiceAttentionStrip } from './ServiceAttentionStrip';
import { TopbarSearch } from './TopbarSearch';
import { TopbarShare } from './TopbarShare';
import { TrendDelta } from './TrendDelta';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr', fail: false }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
const NOW_NS = Date.now() * 1e6;
const item = (id: string, kind: InboxItem['kind'], extra: Partial<InboxItem>): InboxItem => ({
  id, kind, source: 'x', priority: 'P2', priorityReason: '', severity: 'warning', service: 'svc-a',
  title: `title-${id}`, description: '', startedAt: NOW_NS, lastSeen: NOW_NS, status: 'open', displayId: id,
  ...extra,
} as InboxItem);
const ITEMS: InboxItem[] = [
  item('p1', 'problem', { problem: { id: 'p1', ruleId: 'r1', metric: 'error_rate', value: 12, threshold: 5 } }),
  item('s1', 'problem', { problem: { id: 's1', ruleId: 'slo:1:fast', metric: 'burn', value: 3, threshold: 1 } }),
  item('h1', 'httperror', { exception: { fingerprint: 'f1', type: 'HTTP 504', message: 'gateway', occurrences: 4 } as InboxItem['exception'] }),
  item('e1', 'exception', { exception: { fingerprint: 'f2', type: 'IOException', message: 'm', occurrences: 2 } as InboxItem['exception'] }),
  ...['a', 'b', 'c', 'd'].map(k => item(`x${k}`, 'exception', { exception: { fingerprint: `g${k}`, type: 'E', message: '', occurrences: 1 } as InboxItem['exception'] })),
];
vi.mock('@/lib/api', () => ({
  api: { inbox: () => (m.fail ? Promise.reject(new Error('500')) : Promise.resolve({ items: ITEMS })) },
  isCanceled: () => false,
}));
vi.mock('@/lib/queries', () => ({ useProblemVerdicts: () => ({ data: { verdicts: [] } }) }));
vi.mock('@/components/AuthProvider', () => ({ useIncidentsEnabled: () => true }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
async function render(node: ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><MemoryRouter>{node}</MemoryRouter></QueryClientProvider>); });
  await act(async () => { await new Promise(r => setTimeout(r, 20)); });
}
function close() {
  act(() => { root?.unmount(); });
  host?.remove(); host = null; root = null;
}
function snap(): string {
  const c = document.body;
  const attrs = [...c.querySelectorAll('[title],[aria-label],[placeholder]')]
    .flatMap(e => ['title', 'aria-label', 'placeholder'].map(a => e.getAttribute(a) ?? ''));
  return [c.textContent ?? '', ...attrs].join('\n');
}
async function all(): Promise<string> {
  const out: string[] = [];
  const take = async (node: ReactNode) => { await render(node); out.push(snap()); close(); };
  m.fail = false;
  await take(<ServiceAttentionStrip service="svc-a" fallbackProblems={[]} />);
  m.fail = true;
  await take(<ServiceAttentionStrip service="svc-a" fallbackProblems={[{
    id: 'p9', ruleId: 'r9', ruleName: 'rule-9', service: 'svc-a', severity: 'critical', status: 'open',
    metric: 'p99', value: 900, threshold: 500, startedAt: NOW_NS, description: '', displayId: 'P-9',
  } as unknown as Parameters<typeof ServiceAttentionStrip>[0]['fallbackProblems'][number]]} />);
  m.fail = false;
  await take(<><TopbarSearch /><TopbarShare /></>);
  await take(<><TrendDelta cur={3} prior={0} kind="lowerBetter" zeroPrior="was-zero" /><TrendDelta cur={3} prior={0} kind="neutral" /></>);
  return out.join('\n');
}

afterEach(() => { close(); m.lang = 'en'; m.fail = false; });

const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;
const TR_WORDS = /\b(Kural|Anomali|konu|daha|uyarı|eşik|Ara|listede|yeni|önce)\b/;

describe('Servis dikkat şeridi, üst çubuk, TrendDelta — İngilizce arayüz (v0.10.2312)', () => {
  it('Türkçe metin yok', async () => {
    const text = await all();
    expect(text.split('\n').filter(l => TR_CHARS.test(l) || TR_WORDS.test(l))).toEqual([]);
    for (const en of [
      '! 7 open items', 'SLO burn-rate 1 alert', 'Jump to the SLO block at the bottom of the page', 'View in Inbox →',
      'Rule', 'HTTP error', ' (threshold 5)', 'last seen ', '+2 more → ',
      'exceptions could not be loaded', 'Could not read the Inbox; showing alert-rule problems only', '! 1 open item',
      'Search — opens the command palette (⌘K)', 'Search services, trace IDs, pages or settings…', 'Copy link',
      'was 0', 'It was 0 in the previous window.', 'new in list',
    ]) expect(text, en).toContain(en);
  });

  it('Türkçe arayüzde bilinen metin aynen', async () => {
    m.lang = 'tr';
    const text = await all();
    for (const tr of [
      '! 7 açık konu', 'SLO burn-rate 1 uyarı', 'Sayfanın altındaki SLO bloğuna in', "Inbox'ta gör →",
      'Kural', 'HTTP hatası', ' (eşik 5)', ' · son görülme ', '+2 daha → ',
      "exception'lar yüklenemedi", 'Inbox okunamadı; yalnız alert-kuralı problemleri gösteriliyor', '! 1 açık konu',
      'Ara — komut paletini açar (⌘K)', 'Servis, trace ID, sayfa veya ayar ara…', 'Linki kopyala',
      'önce 0', "Önceki pencerede 0'dı.", 'listede yeni',
    ]) expect(text, tr).toContain(tr);
  });
});
