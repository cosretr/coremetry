// @vitest-environment jsdom
//
// v0.10.1345 — operatör bildirimi (prod /cosre, gemma4 31b): sohbet cevabı
// "ACT (Active) $\rightarrow$ RCV (Received) $\rightarrow$ DIP …" diye ham
// LaTeX basıyordu ("Rightarrow'u düzelt"). normalizeMathEscapes v0.10.12'den
// beri vardı ama yalnız RenderedMarkdown çağırıyordu; balon habersizdi. Saf
// tablo testi (mathEscapes.test.ts) BAĞLANMAYI ölçmez — burada gerçek balon.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});
vi.mock('@/components/CosreChart', () => ({ CosreChart: () => <div>[grafik]</div> }));

import { ChatBubble } from './ChatBubble';
import type { ChatTurn } from '@/lib/types';

let host: HTMLDivElement;
let root: Root;

async function mount(turn: ChatTurn) {
  await act(async () => {
    root.render(
      <MemoryRouter>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ChatBubble turn={turn} />
        </QueryClientProvider>
      </MemoryRouter>
    );
  });
}

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const PROD = 'Akış: ACT (Active) $\\rightarrow$ RCV (Received) $\\rightarrow$ DIP (Dispatched)';

describe('v0.10.1345 — balon LaTeX sembolünü unicode basar', () => {
  it('son cevap: PROD satırı oklarla', async () => {
    await mount({ role: 'assistant', text: PROD });
    const t = host.textContent ?? '';
    expect(t).toContain('ACT (Active) → RCV (Received) → DIP (Dispatched)');
    expect(t).not.toContain('\\rightarrow');
    expect(t).not.toContain('$');
  });

  it('akış: chunk sınırı "$\\righ" ham görünmez, tamamlanınca ok', async () => {
    await mount({ role: 'assistant', text: 'ACT (Active) $\\righ', pending: true });
    expect(host.textContent).not.toContain('\\righ');
    await mount({ role: 'assistant', text: 'ACT (Active) $\\rightarrow$ RCV (Received)', pending: true });
    expect(host.textContent).toContain('ACT (Active) → RCV (Received)');
  });

  it('kod DOKUNULMAZ: satır içi kod ve çitli blok literal kalır', async () => {
    await mount({ role: 'assistant', text: 'ok $\\to$ ve `a \\to b`\n\n```tex\n$\\rightarrow$\n```\n' });
    const t = host.textContent ?? '';
    expect(t).toContain('ok → ve');
    expect(host.querySelector('code')?.textContent).toBe('a \\to b');
    expect(t).toContain('$\\rightarrow$');
  });
});
