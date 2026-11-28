// @vitest-environment jsdom
//
// CopilotChat.explainCards.test.tsx — v0.10.1407 (Tek CoSRE adım 4). Operatör:
//   "Trace ve exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama
//    sanki ayrı drawer hissi, CoSRE bütünselliğine uymuyor."
//
// Service → Details grafik ✨'ları (charts) ve Logs histogramı ✨'ı (log-spike)
// da CoSRE sohbetinin sıradan bir turu; çekmecenin özne kipi silindi. Pinler:
//   - ?ai=charts / ?ai=log-spike derin linki TEK tur gönderir (kodekli niyet,
//     StrictMode çift effect'i ikinci tur yapmaz), adres temizlenir, tek composer;
//   - balon künyenin çizim kipine göre kartı cevabın kanıt bloğundan çizer
//     (charts_signals → ChartsSignalsCard, log_spike → LogSpikeCard), anlatım
//     kartın yuvasında (ExplainBody);
//   - blok yoksa (zarfsız eski arşiv turu) düz ExplainBody'ye düşer;
//     v0.10.1444 — Geçmiş turu sunucudan blokları alır, kartı yeniden çizer.
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
// uPlot jsdom'da çizilmez; kartın tembel grafiği stub.
vi.mock('@/components/charts/TimeChart', () => ({
  TimeChart: (p: { times: number[] }) => <div data-testid="timechart">{p.times.length} kova</div>,
}));

import { api } from '@/lib/api';
import type { ChartsSignalsBlockPayload, ChatExplainMeta, ChatStreamEvent, ChatTurn, LogSpikeBlockPayload } from '@/lib/types';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { ChatBubble } from './ai/ChatBubble';
import { setUserLang } from '@/lib/i18n';
// v0.10.2486 dilim 2 — metinler katalogda; bu test Türkçe arayüzü pinler.
// localStorage Node'da yok/boş olabilir: dil tercihi için bellek içi depo.
beforeEach(() => {
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => { mem.set(k, String(v)); }, removeItem: (k: string) => { mem.delete(k); } });
  setUserLang('tr');
});
afterEach(() => { setUserLang(null); vi.unstubAllGlobals(); });

const FROM = 1_790_000_000_000_000_000;
const TO = FROM + 3_600_000_000_000;
const CH = `charts:orders-api:${FROM}:${TO}:err`;
const LS = `log-spike:*:${FROM}:${TO}:sel:prod::error`;
const NARR = '**Ne oldu**\n- Deploy sonrası orders-api hata oranı arttı. Kontrol: GET /orders hatalı izleri.';
const S = Math.floor(FROM / 1e6) + 600_000;

const CHARTS: ChartsSignalsBlockPayload = {
  service: 'orders-api', fromNs: FROM, toNs: TO, scope: 'err',
  signals: {
    deploy: { timeUnixNs: FROM + 60e9, kind: 'deploy', versionBefore: '1.4.1', versionAfter: '1.4.2', podsReplaced: 3 },
    problems: [{ id: 'p-1', title: 'orders-api p99 latency', severity: 'critical', priority: 'P1', startedAt: FROM, metric: 'p99', value: 2.4, threshold: 0.8 }],
    opDeltas: [{ name: 'GET /orders', calls: 400, p95Ratio: 4, errDeltaPp: 3.5 }],
    otherOps: 2,
  },
};
const SPIKE: LogSpikeBlockPayload = {
  service: '', env: 'prod', level: 'error', mode: 'sel', fromNs: FROM, toNs: TO,
  spike: {
    tool: 'explain_log_spike', ok: true, partial: false, request: {},
    spike: { fromMs: S, toMs: S + 420_000, source: 'given', factor: 5.9, ratePerMinSpike: 1840, ratePerMinBase: 310,
      baseFromMs: S - 1_260_000, baseToMs: S, spikeCount: 12880, baseCount: 6510, bucketS: 60 },
    contributors: [{ rank: 1, dim: 'service', value: 'checkout', spikeCount: 9000, baseCount: 800, sharePct: 62, spikePct: 71, basePct: 12, liftPts: 59, reason: "artışın %62'si checkout" }],
    dims: [{ dim: 'service', state: 'ok' }],
    data: { histogram: Array.from({ length: 30 }, (_, i) => [S - 1_800_000 + i * 60_000, 300, 10] as [number, number, number]), samples: [] },
  },
};

let host: HTMLDivElement;
let root: Root;
let search = '';
function Probe() { search = useLocation().search; return null; }
const oneComposer = () => document.body.querySelectorAll('textarea').length === 1;

type ChatArgs = Parameters<typeof api.copilotChat>;

/** Sunucu gibi: blok cevaptan ÖNCE, sonra delta + answer (künye renderer'lı). */
function stubCardChat(block: { type: 'charts_signals' | 'log_spike'; payload: unknown }, meta: ChatExplainMeta) {
  return vi.spyOn(api, 'copilotChat').mockImplementation(
    async (_m, onEvent: (e: ChatStreamEvent) => void) => {
      onEvent({ kind: 'block', id: 'b1', type: block.type, seq: 1, final: true, payload: block.payload });
      onEvent({ kind: 'delta', text: NARR });
      onEvent({ kind: 'answer', text: NARR, exchangeId: 'x1', explain: meta });
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

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  (Element.prototype as unknown as { scrollIntoView: () => void }).scrollIntoView = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  search = '';
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

describe('?ai=charts / ?ai=log-spike derin linki → tek sohbet turu', () => {
  it('charts: kodekli niyet, görünen tur, kart bloktan; adres temiz, tek composer', async () => {
    const chat = stubCardChat({ type: 'charts_signals', payload: CHARTS },
      { kind: 'charts', id: 'orders-api', subject: CH, surface: 'explain-charts:chat', renderer: 'charts', label: 'orders-api' });
    await act(async () => { root.render(shell('/service?name=orders-api&ai=' + encodeURIComponent(CH))); });
    await settle();
    expect(chat).toHaveBeenCalledTimes(1); // StrictMode çift effect'i tek tur
    const c = chat.mock.calls[0] as ChatArgs;
    expect(c[20]).toEqual({ kind: 'charts', id: 'orders-api', subject: CH, src: 'ai-param' });
    expect(c[0][c[0].length - 1].text).toBe('orders-api grafiklerini açıkla · Error rate by operation');
    expect(new URLSearchParams(search).get('ai')).toBeNull();
    expect(oneComposer()).toBe(true);
    const card = document.body.querySelector('[data-charts-card]');
    expect(card, 'kart çizilmedi').not.toBeNull();
    expect(card!.textContent).toContain('İlişkili sinyaller');
    expect(card!.textContent).toContain('1.4.1 → 1.4.2');
    expect(card!.textContent).toContain('Deploy sonrası orders-api hata oranı arttı.'); // anlatım kartın yuvasında
    expect(document.body.textContent).toContain('✨ orders-api grafikleri');
  });

  it('log-spike filo geneli: boş kimlik + kodek, kart bloktan (tembel grafik)', async () => {
    const chat = stubCardChat({ type: 'log_spike', payload: SPIKE },
      { kind: 'log-spike', id: '', subject: LS, surface: 'explain-log-spike:chat', renderer: 'log-spike', label: 'tüm servisler' });
    await act(async () => { root.render(shell('/logs?ai=' + encodeURIComponent(LS))); });
    await settle();
    expect(chat).toHaveBeenCalledTimes(1);
    const c = chat.mock.calls[0] as ChatArgs;
    expect(c[20]).toEqual({ kind: 'log-spike', id: '', subject: LS, src: 'ai-param' });
    expect(c[0][c[0].length - 1].text).toBe('Tüm servislerin log artışını açıkla');
    expect(new URLSearchParams(search).get('ai')).toBeNull();
    expect(oneComposer()).toBe(true);
    const card = document.body.querySelector('[data-log-spike-card]');
    expect(card, 'kart çizilmedi').not.toBeNull();
    expect(card!.querySelector('[data-testid="timechart"]')?.textContent).toBe('30 kova');
    expect(card!.textContent).toContain('yalnız ERROR');
    expect(card!.querySelectorAll('tbody tr')).toHaveLength(1);
  });
});

describe('ChatBubble çizim kipi dağıtımı', () => {
  async function render(turn: ChatTurn) {
    await act(async () => { root.render(<MemoryRouter><ChatBubble turn={turn} /></MemoryRouter>); });
    await settle();
  }
  const meta: ChatExplainMeta = { kind: 'charts', id: 'orders-api', subject: CH, renderer: 'charts' };

  it('charts künyesi + blok → ChartsSignalsCard (anlatım "Ne oldu" yuvasında)', async () => {
    await render({ role: 'assistant', text: NARR, exchangeId: 'x1', explain: meta,
      blocks: [{ id: 'b1', type: 'charts_signals', seq: 1, final: true, payload: CHARTS }] });
    const card = host.querySelector('[data-charts-card]');
    expect(card).not.toBeNull();
    expect(card!.textContent).toContain('Deploy sonrası orders-api hata oranı arttı.');
    expect(card!.textContent).toContain('Sonraki adım');
  });

  it('blok yok (arşivden geri yüklenen tur) → düz ExplainBody', async () => {
    await render({ role: 'assistant', text: NARR, explain: meta });
    expect(host.querySelector('[data-charts-card]')).toBeNull();
    expect(host.textContent).toContain('Deploy sonrası orders-api hata oranı arttı.');
    expect(host.textContent).not.toContain('İlişkili sinyaller');
  });

  it('bozuk blok yükü kartı çizdirmez; body künyesi bloğa bakmaz', async () => {
    await render({ role: 'assistant', text: NARR, explain: meta,
      blocks: [{ id: 'b1', type: 'charts_signals', seq: 1, final: true, payload: { service: '' } }] });
    expect(host.querySelector('[data-charts-card]')).toBeNull();
    await render({ role: 'assistant', text: NARR, explain: { ...meta, renderer: 'body' },
      blocks: [{ id: 'b1', type: 'charts_signals', seq: 1, final: true, payload: CHARTS }] });
    expect(host.querySelector('[data-charts-card]')).toBeNull();
  });
});

// v0.10.1444 — Geçmiş / ?chat=<convId>: sunucu turun blok zarfını
// (cosre_interactions.blocks) mesajla döndürür → kart canlı turdaki gibi
// çizilir + "Arşiv anlık görüntüsü" notu; kod künyesi sunucu kaynaklı.
describe('Geçmiş turu (?chat=) blokları yeniden çizer', () => {
  const X = 'aaaa0000aaaa0000aaaa0000aaaa0000';
  const meta: ChatExplainMeta = { kind: 'charts', id: 'orders-api', subject: CH, renderer: 'charts', label: 'orders-api' };

  it('?chat= açılışı: charts_signals → ChartsSignalsCard + arşiv notu; exchangeId boş, archiveExchangeId dolu', async () => {
    const get = vi.spyOn(api, 'aiConversation').mockResolvedValue({
      id: 'c9', title: 't', updatedAt: 1, messages: [
        { role: 'user', text: 'orders-api grafiklerini açıkla' },
        { role: 'assistant', text: NARR, explain: meta, exchangeId: X, blocksAt: Date.now() - 3 * 86_400_000, blocksDropped: ['table'],
          blocks: [{ id: 'b1', type: 'charts_signals', seq: 1, final: true, payload: CHARTS }] },
      ],
    });
    await act(async () => { root.render(shell('/cosre?chat=c9')); });
    await settle();
    expect(get).toHaveBeenCalledWith('c9');
    const card = document.body.querySelector('[data-charts-card]');
    expect(card, 'arşiv turu kartı çizmedi').not.toBeNull();
    expect(card!.textContent).toContain('1.4.1 → 1.4.2');
    expect(card!.textContent).toContain('Deploy sonrası orders-api hata oranı arttı.');
    const note = document.body.querySelector('[data-archive-note]');
    expect(note?.textContent).toBe('Arşiv anlık görüntüsü · 3 gün önce · 1 kart sığmadı');
  });

  it('blok yok (eski satır) → düz ExplainBody, arşiv notu yok', async () => {
    vi.spyOn(api, 'aiConversation').mockResolvedValue({
      id: 'c8', title: 't', updatedAt: 1, messages: [
        { role: 'user', text: 'soru' }, { role: 'assistant', text: NARR, explain: meta },
      ],
    });
    await act(async () => { root.render(shell('/cosre?chat=c8')); });
    await settle();
    expect(document.body.querySelector('[data-charts-card]')).toBeNull();
    expect(document.body.querySelector('[data-archive-note]')).toBeNull();
    expect(document.body.textContent).toContain('Deploy sonrası orders-api hata oranı arttı.');
  });

  it('canlı turda arşiv notu yok; sunucu kaynaklı explain.code → CodeStatusNote', async () => {
    await act(async () => { root.render(<MemoryRouter><ChatBubble turn={{ role: 'assistant', text: NARR, exchangeId: 'x1', explain: meta,
      blocks: [{ id: 'b1', type: 'charts_signals', seq: 1, final: true, payload: CHARTS }] }} /></MemoryRouter>); });
    await settle();
    expect(host.querySelector('[data-charts-card]')).not.toBeNull();
    expect(host.querySelector('[data-archive-note]')).toBeNull();
    await act(async () => { root.render(<MemoryRouter><ChatBubble turn={{ role: 'assistant', text: NARR, archiveExchangeId: X,
      explain: { kind: 'trace', id: 'abc', code: { reason: 'kod entegrasyonu yapılandırılmamış' } } }} /></MemoryRouter>); });
    await settle();
    expect(host.textContent).toContain('Kod okunamadı');
  });
});
