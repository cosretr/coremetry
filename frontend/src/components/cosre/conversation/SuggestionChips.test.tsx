// @vitest-environment jsdom
// SuggestionChips.test.tsx — v0.10.2364 (interaction-audit I3/I4): tık çipi
// onSuggest'e verir + suggestion_click; yazınca suggestion_ignored bir kez.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { CosreSuggestions } from '@/lib/types';

const m = vi.hoisted(() => ({ sig: vi.fn((_x: string, _k: string, _d?: string) => undefined) }));
vi.mock('@/lib/api', () => ({ api: { aiSignal: (x: string, k: string, d?: string) => m.sig(x, k, d) } }));

import { SuggestionChips, noteSuggestionsIgnored } from './SuggestionChips';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const sg: CosreSuggestions = {
  exchangeId: 'x-1',
  chips: [
    { id: 'g1', label: "Son deploy'lar — checkout", rule: 'deploy', intent: { tool: 'list_deployments', args: { service: 'checkout' } }, token: 't1' },
    { id: 'g2', label: "En yavaş trace'ler — checkout", rule: 'slow', intent: { tool: 'find_traces', args: { service: 'checkout', sort_by: 'slowest' } }, token: 't2' },
  ],
};

let root: Root | null = null;
let host: HTMLDivElement | null = null;
function mount(el: React.ReactElement) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(el));
  return host;
}
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null; host = null;
  m.sig.mockClear();
});

describe('SuggestionChips (I3)', () => {
  it('tık: çip onSuggest’e, suggestion_click {chip, rule, tool, rank}', () => {
    const onSuggest = vi.fn();
    const el = mount(<SuggestionChips suggestions={sg} onSuggest={onSuggest} />);
    const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.includes('En yavaş'))!;
    act(() => btn.click());
    expect(onSuggest).toHaveBeenCalledWith(sg.chips[1]);
    expect(m.sig).toHaveBeenCalledWith('x-1', 'suggestion_click', JSON.stringify({ chip: 'g2', rule: 'slow', tool: 'find_traces', rank: 2 }));
  });
  it('onSuggest yoksa çizilmez', () => {
    const el = mount(<SuggestionChips suggestions={sg} />);
    expect(el.querySelector('[data-cosre-suggestions]')).toBeNull();
  });
  it('suggestion_ignored alışveriş başına bir kez; kullanıcı mesajı sonda → yok', () => {
    const msgs = [{ role: 'assistant', parts: [{ type: 'data-suggestions', data: sg }] }];
    const sent = new Set<string>();
    noteSuggestionsIgnored(msgs, sent);
    noteSuggestionsIgnored(msgs, sent);
    expect(m.sig).toHaveBeenCalledTimes(1);
    expect(m.sig).toHaveBeenCalledWith('x-1', 'suggestion_ignored', JSON.stringify({ chips: ['g1', 'g2'], rules: ['deploy', 'slow'] }));
    noteSuggestionsIgnored([{ role: 'user', parts: [] }], new Set());
    expect(m.sig).toHaveBeenCalledTimes(1);
  });
});
