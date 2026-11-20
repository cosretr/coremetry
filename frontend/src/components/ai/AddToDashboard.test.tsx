// @vitest-environment jsdom
// AddToDashboard.test.tsx — v0.10.1443 (sohbetten dashboard, R1). Sözleşme:
//   • "Dashboard'a ekle" yalnız çevrilebilen grafikte ve yalnız sohbet içinde;
//   • viewer düğmeyi kapalı + gerekçeyle görür (boşluk değil);
//   • "Hepsini dashboard yap" cevapta ≥2 çevrilebilen panel varken;
//   • PinToDashboardModal çok panelli: tıklanmadan hiçbir şey yazılmaz; yeni
//     pano from-chat ucundan (origin ile), var olana /panels ucundan; atlanan
//     paneller gerekçesiyle söylenir.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { role: 'editor' as string | undefined, calls: [] as Array<[string, unknown[]]> };
});
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => m.role, useAuthUserId: () => 'u1' }));
vi.mock('@/components/chart/corePanelEntry', () => ({ CorePanelMulti: () => <div>[panel]</div> }));
vi.mock('@/components/CosreChart', () => ({ CosreChart: () => <div>[grafik]</div> }));
vi.mock('@/lib/api', async (orig) => {
  const real = await orig<typeof import('@/lib/api')>();
  return {
    ...real,
    api: {
      ...real.api,
      listDashboards: vi.fn(async () => [{ id: 'd1', name: 'Orders', description: '', createdAt: 0, updatedAt: 0 }]),
      createDashboardFromChat: vi.fn(async (...a: unknown[]) => { m.calls.push(['create', a]); return { id: 'n1', name: 'orders (2)', added: 2, skipped: [], href: '/dashboard?id=n1' }; }),
      appendDashboardPanels: vi.fn(async (...a: unknown[]) => { m.calls.push(['append', a]); return { id: 'd1', name: 'Orders', added: 1, skipped: [{ title: 'dup', reason: 'duplicate' }], href: '/dashboard?id=d1' }; }),
      createDashboard: vi.fn(async (...a: unknown[]) => { m.calls.push(['legacy', a]); return { id: 'x' }; }),
    },
  };
});

import { EvidenceChart } from './EvidenceChips';
import { ChatDashContext } from './chatDash';
import { ChatBubble } from './ChatBubble';
import { PinToDashboardModal } from '@/pages/explore/PinToDashboardModal';
import type { ChatEvidenceChartPayload, ChatQueryEvidence, ChatTurn, Panel } from '@/lib/types';

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  m.role = 'editor';
  m.calls = [];
});
afterEach(() => { act(() => root.unmount()); host.remove(); document.body.innerHTML = ''; });

async function mount(ui: React.ReactElement) {
  await act(async () => {
    root.render(
      <MemoryRouter>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{ui}</QueryClientProvider>
      </MemoryRouter>,
    );
  });
}
const flush = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });
const btnByText = (t: string) => Array.from(document.querySelectorAll('button')).find(b => (b.textContent ?? '').trim().startsWith(t)) as HTMLButtonElement | undefined;

const W = { fromNs: 1e12, toNs: 1e12 + 3600e9 };
const chart = (ref: string, title = 'orders-api p99'): ChatEvidenceChartPayload => ({ title, evidenceRef: ref, window: W, series: [{ name: 'p99', unit: 'ms', points: [[1, 2]] }] });
const ev = (ref: string, over: Partial<ChatQueryEvidence> = {}): ChatQueryEvidence => ({
  tool: 'metric_chart', source: 'VictoriaMetrics', queryKind: 'promql', ref, durationMs: 1,
  query: `sum(rate(http_server_requests_total{service="orders-api"}[5m])) /* ${ref} */`, window: { fromMs: 0, toMs: 1 }, ...over,
});
const inChat = (ui: React.ReactElement) => <ChatDashContext.Provider value={{ conversationId: 'c-1', question: 'orders-api' }}>{ui}</ChatDashContext.Provider>;

describe('EvidenceChart — Dashboard\'a ekle görünürlüğü', () => {
  it('VM PromQL grafiği sohbette: düğme açık', async () => {
    await mount(inChat(<EvidenceChart p={chart('r1')} ev={ev('r1')} />));
    const b = host.querySelector<HTMLButtonElement>('[data-testid="chat-dash-add"]');
    expect(b).toBeTruthy();
    expect(b!.disabled).toBe(false);
  });
  it('çevrilemeyen grafik (SQL kanıtı) ve sohbet dışı: düğme yok', async () => {
    await mount(<>{inChat(<EvidenceChart p={chart('r1')} ev={ev('r1', { queryKind: 'sql' })} />)}<EvidenceChart p={chart('r2')} ev={ev('r2')} /></>);
    expect(host.querySelector('[data-testid="chat-dash-add"]')).toBeNull();
  });
  it('viewer: düğme kapalı, gerekçe erişilebilir adda', async () => {
    m.role = 'viewer';
    await mount(inChat(<EvidenceChart p={chart('r1')} ev={ev('r1')} />));
    const b = host.querySelector<HTMLButtonElement>('[data-testid="chat-dash-add"]')!;
    expect(b.disabled).toBe(true);
    expect(b.getAttribute('aria-label')).toMatch(/editor|editör/);
  });
});

describe('ChatBubble — Hepsini dashboard yap', () => {
  const turn = (n: number): ChatTurn => ({
    role: 'assistant', text: 'cevap',
    evidence: Array.from({ length: n }, (_, i) => ev(`r${i}`)),
    blocks: Array.from({ length: n }, (_, i) => ({ id: `b${i}`, type: 'chart', seq: i, final: true, payload: chart(`r${i}`, `grafik ${i}`) })),
  } as ChatTurn);
  it('≥2 çevrilebilen panel → düğme', async () => {
    await mount(<ChatBubble turn={turn(2)} question="orders-api nasıl?" />);
    expect(host.querySelector('[data-testid="chat-dash-all"]')).toBeTruthy();
  });
  it('tek panel → yok', async () => {
    await mount(<ChatBubble turn={turn(1)} />);
    expect(host.querySelector('[data-testid="chat-dash-all"]')).toBeNull();
  });
});

describe('PinToDashboardModal — çok panel', () => {
  const panels: Panel[] = [
    { id: 'a', type: 'promql', title: 'A', width: 2, config: { query: 'sum(rate(a_total[5m]))', source: 'vm' } },
    { id: 'b', type: 'promql', title: 'B', width: 2, config: { query: 'sum(rate(b_total[5m]))', source: 'vm' } },
  ];
  it('yeni pano: tıklanmadan yazım yok; from-chat ucu origin ile', async () => {
    await mount(<PinToDashboardModal panels={panels} onClose={() => {}} origin={{ conversation: 'c-1', exchangeId: 'x1' }} defaultName="orders" />);
    await flush();
    expect(document.querySelector('[data-testid="pin-panel-list"]')?.textContent).toContain('B');
    expect(m.calls).toHaveLength(0);
    await act(async () => { btnByText('Add')!.click(); });
    await flush();
    expect(m.calls).toHaveLength(1);
    const [kind, args] = m.calls[0];
    expect(kind).toBe('create');
    expect(args[0]).toMatchObject({ dashboard: { name: 'orders' }, origin: { conversation: 'c-1', exchangeId: 'x1' } });
    expect((args[0] as { panels: Panel[] }).panels).toHaveLength(2);
    expect(document.querySelector('[data-testid="pin-done"]')?.textContent).toContain('orders (2)');
  });
  it('var olan pano: /panels ucu; atlanan panel gerekçesiyle', async () => {
    await mount(<PinToDashboardModal panels={panels} onClose={() => {}} origin={{ conversation: 'c-1' }} defaultName="orders" />);
    await flush();
    const sel = document.querySelector('select') as HTMLSelectElement;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(sel, 'd1');
      sel.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await act(async () => { btnByText('Add')!.click(); });
    await flush();
    expect(m.calls[0][0]).toBe('append');
    expect(m.calls[0][1][0]).toBe('d1');
    expect(document.querySelector('[data-testid="pin-skipped"]')?.textContent).toContain('dup');
  });
});
