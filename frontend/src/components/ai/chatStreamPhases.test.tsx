// @vitest-environment jsdom
//
// v0.10.1273 — CoSRE adım 4: serbest döngüde token akışı + adım satırları.
//
//   (1) `delta` metni CANLI ekler; `reset: true` o ana dek akanı siler;
//       `answer` akanın YERİNE geçer (son işlem — künye, link — otoriter);
//   (2) evre kırıntısı ("Metrik → Log → Trace") adım sırasıyla, ardışık aynı
//       evre tek halka; evresiz/yürütülmeyen adım girmez; akarken canlı büyür;
//   (3) ChatBubble kırıntıyı cevabın ÜSTÜNDE çizer; evre yoksa hiç çizmez.
// Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router';
import { api } from '@/lib/api';
import type { ChatStepDetail, ChatStreamEvent, ChatTurn } from '@/lib/types';
import { useChatThread } from './useChatThread';
import { applyChatDelta, phaseTrail } from './phaseTrail';
import { ChatBubble } from './ChatBubble';
import { setUserLang } from '@/lib/i18n';
// v0.10.2486 dilim 2 — metinler katalogda; bu test Türkçe arayüzü pinler.
// localStorage Node'da yok/boş olabilir: dil tercihi için bellek içi depo.
beforeEach(() => {
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => { mem.set(k, String(v)); }, removeItem: (k: string) => { mem.delete(k); } });
  setUserLang('tr');
});
afterEach(() => { setUserLang(null); vi.unstubAllGlobals(); });

const d = (i: number, over: Partial<ChatStepDetail> = {}): ChatStepDetail => ({ i, tool: `tool_${i}`, ...over });

describe('phaseTrail', () => {
  it('sırayı korur, ardışık aynı evreyi tekler, evresiz ve yürütülmeyeni atlar', () => {
    const details = [
      d(1, { label: 'bütçe: 6 çağrı', tool: '' }),
      d(2, { phase: 'Metrik' }), d(3, { phase: 'Metrik' }),
      d(4, { phase: 'Log' }), d(5, { tool: 'dis_arac' }), d(6, { phase: 'Log' }),
      d(7, { phase: 'Trace', skipped: true }), d(8, { phase: 'Trace' }),
      d(9, { phase: 'Metrik' }),
    ];
    expect(phaseTrail(details)).toEqual(['Metrik', 'Log', 'Trace', 'Metrik']);
    expect(phaseTrail(undefined)).toEqual([]);
  });
  it('applyChatDelta: ekler; reset siler', () => {
    expect(applyChatDelta('ab', { text: 'c' })).toBe('abc');
    expect(applyChatDelta(undefined, { text: 'x' })).toBe('x');
    expect(applyChatDelta('önsöz', { text: '', reset: true })).toBe('');
    expect(applyChatDelta('önsöz', { text: 'yeni', reset: true })).toBe('yeni');
  });
});

describe('useChatThread — akış + kırıntı (canlı)', () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    vi.restoreAllMocks();
  });

  it('delta canlı ekler, reset siler, answer yerine geçer; evreler canlı büyür', async () => {
    let emit: (e: ChatStreamEvent) => void = () => {};
    let finish: () => void = () => {};
    vi.spyOn(api, 'copilotChat').mockImplementation(async (_m, onEvent: (e: ChatStreamEvent) => void) => {
      emit = onEvent;
      await new Promise<void>(res => { finish = res; });
    });
    let thread: ReturnType<typeof useChatThread> | null = null;
    function Probe() {
      thread = useChatThread();
      return null;
    }
    act(() => root.render(<Probe />));
    let sent: Promise<void> = Promise.resolve();
    act(() => { sent = thread!.send('checkout neden yavaş?'); });
    const last = (): ChatTurn => thread!.turns[thread!.turns.length - 1];

    act(() => emit({ kind: 'step', i: 1, tool: 'query_metrics', args: '{}', phase: 'Metrik' }));
    expect(phaseTrail(last().stepDetails)).toEqual(['Metrik']);
    act(() => emit({ kind: 'step', i: 2, tool: 'query_logs', args: '{}', phase: 'Log' }));
    act(() => emit({ kind: 'step', i: 3, tool: 'query_logs', args: '{}', phase: 'Log' }));
    act(() => emit({ kind: 'step', i: 4, tool: 'query_traces', args: '{}', phase: 'Trace' }));
    expect(phaseTrail(last().stepDetails)).toEqual(['Metrik', 'Log', 'Trace']);

    act(() => emit({ kind: 'delta', text: 'Önce bakı' }));
    expect(last().text).toBe('Önce bakı');
    act(() => emit({ kind: 'delta', text: '', reset: true }));
    expect(last().text).toBe('');
    act(() => emit({ kind: 'delta', text: 'p95 ' }));
    act(() => emit({ kind: 'delta', text: '1830 ms' }));
    expect(last().text).toBe('p95 1830 ms');
    expect(last().pending).toBe(true);

    act(() => emit({ kind: 'answer', text: 'p95 1830 ms\n\n_Kaynak: query_metrics_', exchangeId: 'x1' }));
    expect(last().text).toBe('p95 1830 ms\n\n_Kaynak: query_metrics_');
    expect(last().pending).toBe(false);
    act(() => emit({ kind: 'done', ok: true }));
    await act(async () => { finish(); await sent; });
  });
});

describe('ChatBubble — evre kırıntısı', () => {
  const html = (turn: ChatTurn) => renderToStaticMarkup(<MemoryRouter><ChatBubble turn={turn} /></MemoryRouter>);
  it('cevabın üstünde, sırayla, tekillenmiş', () => {
    const details = [d(1, { phase: 'Metrik' }), d(2, { phase: 'Log' }), d(3, { phase: 'Log' }), d(4, { phase: 'Trace' })];
    const h = html({ role: 'assistant', text: 'CEVAP-METNİ', steps: details.map(x => x.tool), stepDetails: details });
    expect(h).toContain('aria-label="Soruşturma akışı: Metrik → Log → Trace"');
    expect(h.match(/>Log</g)?.length).toBe(1);
    const iTrail = h.indexOf('cm-phase-trail');
    expect(iTrail).toBeGreaterThan(-1);
    expect(iTrail).toBeLessThan(h.indexOf('CEVAP-METNİ'));
    const order = ['>Metrik<', '>Log<', '>Trace<'].map(x => h.indexOf(x));
    expect(order).toEqual([...order].sort((a, b) => a - b));
  });
  it('evre yoksa (eski sunucu / arşiv) çizilmez', () => {
    expect(html({ role: 'assistant', text: 'x', steps: ['get_topology'], stepDetails: [d(1)] })).not.toContain('cm-phase-trail');
    expect(html({ role: 'assistant', text: 'x' })).not.toContain('cm-phase-trail');
  });
});
