// @vitest-environment jsdom
//
// CopilotChat.metricPanel.test.tsx — v0.10.1435 (Panelden "Bu grafiği açıkla";
// brush aralığı ve seçili seri bağlamda). Pinler:
//   - ?ai=metric_panel TEK tur gönderir; ✨ anında bırakılan panel yükü niyete
//     girer (kodek konum taşır, sorgu yükte), görünen metin başlık + seçili
//     aralık + seri; adres temizlenir;
//   - yüksüz derin link (paylaşılan bağlantı) yine tek tur, yük YOK (sunucu
//     dürüst cevap verir);
//   - sayfanın yayınladığı brush + seçili seri context.page'e katılır;
//   - balon künyenin metric-panel kipiyle cevabın bloğundan MetricPanelCard çizer;
//     blok yoksa (arşiv) düz ExplainBody.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, StrictMode } from 'react';
import { MemoryRouter, useLocation } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: q.includes('prefers-reduced-motion: reduce'), media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});
// Grafik motoru jsdom'da çizilmez; kartın tembel grafiği stub.
vi.mock('@/components/ai/EvidenceChips', async (orig) => ({
  ...(await orig<typeof import('@/components/ai/EvidenceChips')>()),
  EvidenceChart: (p: { p: { series: unknown[] } }) => <div data-testid="evchart">{p.p.series.length} seri</div>,
}));

import { api } from '@/lib/api';
import { formatAiParam, type AISubject } from '@/lib/aiSubject';
import { resetPageOverlayForTest, setPageOverlay, stageMetricPanelAsk } from '@/lib/pageContextOverlay';
import type { ChatExplainMeta, ChatStreamEvent, ChatTurn, MetricPanelBlockPayload, PageContext } from '@/lib/types';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { ChatBubble } from './ai/ChatBubble';

const FROM = Date.UTC(2026, 9, 22, 14, 0) * 1e6;
const TO = Date.UTC(2026, 9, 22, 15, 0) * 1e6;
const SUBJ: AISubject = {
  kind: 'metric_panel', id: 'd.dash01.p1', fromNs: FROM, toNs: TO,
  brushFromNs: Date.UTC(2026, 9, 22, 14, 5) * 1e6, brushToNs: Date.UTC(2026, 9, 22, 14, 20) * 1e6, series: ['pod-a'],
};
const KEY = formatAiParam(SUBJ);
const PANEL = { src: 'promql' as const, promql: 'sum by (pod) (rate(http_server_requests_total[5m]))', vars: { svc: 'checkout' }, title: 'Checkout RPS' };
const NARR = 'pod-a 14:07 sıçradı; diğer podlar yatay.';
const BLOCK: MetricPanelBlockPayload = {
  title: 'Checkout RPS', loc: 'd.dash01.p1', src: 'promql', query: PANEL.promql, unit: 'rps', fromNs: FROM, toNs: TO,
  brushFromNs: SUBJ.brushFromNs, brushToNs: SUBJ.brushToNs, selected: ['pod-a'], missing: ['pod-z'],
  chart: { title: 'Checkout RPS', evidenceRef: '', window: { fromNs: FROM, toNs: TO }, deepLink: '/dashboard?id=dash01',
    series: [{ name: 'pod-a', unit: 'rps', points: [[1, 2], [3, 4]] }], regions: [{ fromMs: 1, toMs: 2, label: 'seçili' }] },
  stats: ['pod-a [SEÇİLİ]: pencere min 10 rps, max 50 rps (14:07)'], notes: ['kırılım notu'], href: '/dashboard?id=dash01',
};
const META: ChatExplainMeta = { kind: 'metric_panel', id: 'd.dash01.p1', subject: KEY, renderer: 'metric-panel', label: 'Checkout RPS', note: 'özet' };

let host: HTMLDivElement;
let root: Root;
let search = '';
function Probe() { search = useLocation().search; return null; }
type ChatArgs = Parameters<typeof api.copilotChat>;

function stubChat() {
  return vi.spyOn(api, 'copilotChat').mockImplementation(async (_m, onEvent: (e: ChatStreamEvent) => void) => {
    onEvent({ kind: 'block', id: 'b1', type: 'metric_panel', seq: 1, final: true, payload: BLOCK });
    onEvent({ kind: 'delta', text: NARR });
    onEvent({ kind: 'answer', text: NARR, exchangeId: 'x1', explain: META });
    onEvent({ kind: 'done', ok: true });
  });
}

function shell(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <StrictMode>
      <MemoryRouter initialEntries={[path]}>
        <QueryClientProvider client={qc}>
          <AuthProvider><ConfirmProvider><CopilotChat /><Probe /></ConfirmProvider></AuthProvider>
        </QueryClientProvider>
      </MemoryRouter>
    </StrictMode>
  );
}

async function settle() {
  for (let i = 0; i < 6; i++) await act(async () => { await new Promise(r => setTimeout(r, 5)); });
}

const pageArg = (c: ChatArgs): PageContext | undefined =>
  (c as unknown[]).find(a => !!a && typeof a === 'object' && 'page' in (a as object) && 'path' in (a as object)) as PageContext | undefined;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  (Element.prototype as unknown as { scrollIntoView: () => void }).scrollIntoView = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  search = '';
  resetPageOverlayForTest();
  __resetCopilotEnabledCache();
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.com', role: 'admin', firstName: 'Deniz' });
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 });
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false });
  vi.spyOn(api, 'aiConversations').mockResolvedValue([]);
  vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'c1', title: 't', updatedAt: 1, messages: [] });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe('?ai=metric_panel → tek sohbet turu (v0.10.1435)', () => {
  it('bırakılan yük niyete girer; görünen metin; adres temiz; brush + seri page bağlamında; kart', async () => {
    const chat = stubChat();
    stageMetricPanelAsk(KEY, PANEL);
    setPageOverlay({ brush: { fromMs: SUBJ.brushFromNs / 1e6, toMs: SUBJ.brushToNs / 1e6 }, selectedSeries: ['pod-a'] });
    await act(async () => { root.render(shell('/dashboard?id=dash01&ai=' + encodeURIComponent(KEY))); });
    await settle();
    expect(chat).toHaveBeenCalledTimes(1);
    const c = chat.mock.calls[0] as ChatArgs;
    expect(c[20]).toEqual({ kind: 'metric_panel', id: 'd.dash01.p1', subject: KEY, src: 'ai-param', panel: PANEL });
    expect(c[0][c[0].length - 1].text).toMatch(/^Bu grafiği açıkla · Checkout RPS · \d\d:05–\d\d:20 seçili · seri: pod-a$/);
    expect(new URLSearchParams(search).get('ai')).toBeNull();
    const pg = pageArg(c);
    expect(pg?.brush).toEqual({ fromMs: SUBJ.brushFromNs / 1e6, toMs: SUBJ.brushToNs / 1e6 });
    expect(pg?.selectedSeries).toEqual(['pod-a']);
    const card = document.body.querySelector('[data-metric-panel-card]');
    expect(card, 'kart çizilmedi').not.toBeNull();
    expect(card!.querySelector('[data-testid="evchart"]')?.textContent).toBe('1 seri');
    expect(card!.textContent).toContain('pod-a [SEÇİLİ]');
    expect(card!.textContent).toContain('seri: pod-a');
    expect(card!.textContent).toContain('pod-z');
    expect(card!.textContent).toContain(NARR);
    expect(document.body.textContent).toContain('✨ grafik Checkout RPS');
  });

  it('yüksüz derin link: tek tur, yük yok', async () => {
    const chat = stubChat();
    await act(async () => { root.render(shell('/explore?ai=' + encodeURIComponent(formatAiParam({ ...SUBJ, id: 'x.A' })))); });
    await settle();
    expect(chat).toHaveBeenCalledTimes(1);
    const ask = (chat.mock.calls[0] as ChatArgs)[20] as { panel?: unknown; kind: string };
    expect(ask.kind).toBe('metric_panel');
    expect(ask.panel).toBeUndefined();
  });
});

describe('ChatBubble metric-panel dağıtımı', () => {
  async function render(turn: ChatTurn) {
    await act(async () => { root.render(<MemoryRouter><ChatBubble turn={turn} /></MemoryRouter>); });
    await settle();
  }
  it('blok yok (arşiv) → ExplainBody + künye notu', async () => {
    await render({ role: 'assistant', text: NARR, explain: META });
    expect(host.querySelector('[data-metric-panel-card]')).toBeNull();
    expect(host.textContent).toContain(NARR);
    expect(host.textContent).toContain('özet');
  });
  it('güvensiz link kartta çizilmez', async () => {
    await render({ role: 'assistant', text: NARR, explain: META,
      blocks: [{ id: 'b1', type: 'metric_panel', seq: 1, final: true, payload: { ...BLOCK, href: 'https://evil.example.test' } }] });
    const card = host.querySelector('[data-metric-panel-card]');
    expect(card).not.toBeNull();
    expect(card!.querySelector('a[href^="https://"]')).toBeNull();
  });
});
