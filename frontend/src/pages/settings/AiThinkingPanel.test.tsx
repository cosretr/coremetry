// @vitest-environment jsdom
// AiThinkingPanel.test.tsx — v0.10.2458. Contract: every surface shows its
// server mode (default off); changing one select and saving PUTs ONLY the
// changed surface; stats render per surface; inherit summary per profile.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { AIThinkingPayload } from '@/lib/types';

const api = vi.hoisted(() => ({
  aiThinking: vi.fn(async (..._a: unknown[]): Promise<unknown> => null),
  putAIThinking: vi.fn(async (..._a: unknown[]): Promise<unknown> => null),
}));
vi.mock('@/lib/api', () => ({ api }));

import { AiThinkingPanel } from './AiThinkingPanel';
import { thinkingStatsLine, inheritSummary } from './aiThinking';

const payload = (trace: 'off' | 'on' | 'inherit' = 'off'): AIThinkingPayload => ({
  surfaces: [
    { id: 'intent', label: 'Intent / router classifier', hint: 'chat-intent', mode: 'off' },
    { id: 'trace', label: 'Trace explain', hint: 'explain-trace', mode: trace },
  ],
  stats: [
    { surface: 'trace', thinking: 'off', calls: 4, errors: 0, avgMs: 900 },
    { surface: 'trace', thinking: 'on', calls: 2, errors: 1, avgMs: 3400 },
  ],
  profiles: [
    { id: 'qwen', model: 'qwen38-flash', thinking: '', switch: true },
    { id: 'gemma', model: 'gemma-4', switch: false },
  ],
});

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => { root!.render(<QueryClientProvider client={qc}><AiThinkingPanel /></QueryClientProvider>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  document.body.innerHTML = '';
  vi.clearAllMocks();
});
const tick = async () => {
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 15)); });
};

describe('AiThinkingPanel', () => {
  it('shows server modes, PUTs only the changed surface', async () => {
    api.aiThinking.mockResolvedValueOnce(payload());
    api.putAIThinking.mockResolvedValueOnce(payload('on'));
    const el = render();
    await tick();
    const selects = Array.from(el.querySelectorAll('select'));
    expect(selects.map(s => s.value)).toEqual(['off', 'off']);
    expect(el.textContent).toContain('on 2 · 3.4 s avg (1 err)');
    expect(el.textContent).toContain('gemma: no thinking key (ignored)');
    const save = Array.from(el.querySelectorAll('button')).find(b => b.textContent === 'Save')!;
    expect(save.disabled).toBe(true);
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(selects[1], 'on');
      selects[1].dispatchEvent(new Event('change', { bubbles: true }));
    });
    expect(save.disabled).toBe(false);
    act(() => { save.click(); });
    await tick();
    expect(api.putAIThinking).toHaveBeenCalledWith({ trace: 'on' });
    expect(el.textContent).toContain('Thinking settings saved.');
  });

  it('helpers format stats and inherit summary', () => {
    expect(thinkingStatsLine(payload().stats, 'trace')).toBe('off 4 · 900 ms avg | on 2 · 3.4 s avg (1 err)');
    expect(thinkingStatsLine(payload().stats, 'intent')).toBe('');
    expect(inheritSummary(payload().profiles)).toBe('qwen: model default · gemma: no thinking key (ignored)');
  });
});
