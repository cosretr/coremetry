// @vitest-environment jsdom
// ProblemCoSRESection.test.tsx — v0.10.1168: probleme bağlı sohbetler listesi.
import { describe, it, expect, vi, afterEach } from 'vitest';

// v0.10.2284 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.mock('@/lib/api', () => ({
  api: {
    problemConversations: vi.fn(async (id: string) => id === 'p-1'
      ? [{ id: 'c1', title: 'Ödeme gecikmesi', updatedAt: 1, messages: 4, link: { kind: 'problem', id: 'p-1' } }]
      : []),
    problemNotes: vi.fn(async (id: string) => id === 'p-1'
      ? [{ id: 'n1', problemId: 'p-1', text: 'CoSRE bulgusu (get_problem): hata oranı %12', by: 'a@b', at: 1 }]
      : []),
  },
}));

import { ProblemCoSRESection } from './ProblemCoSRESection';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; });

async function render(problemId: string) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><ProblemCoSRESection problemId={problemId} /></MemoryRouter></QueryClientProvider>);
  });
  const toggle = Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes('CoSRE sohbetleri'));
  await act(async () => { toggle?.click(); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

describe('ProblemCoSRESection', () => {
  it('bağlı konuşma /cosre?chat= bağlantısıyla listelenir', async () => {
    const el = await render('p-1');
    const a = el.querySelector('a');
    expect(a?.textContent).toBe('Ödeme gecikmesi');
    expect(a?.getAttribute('href')).toBe('/cosre?chat=c1');
    expect(el.textContent).toContain('CoSRE bulgusu (get_problem): hata oranı %12');
  });
  it('bağlı konuşma yoksa boş durum', async () => {
    const el = await render('p-2');
    expect(el.textContent).toContain('Bu probleme bağlı sohbet yok');
  });
});
