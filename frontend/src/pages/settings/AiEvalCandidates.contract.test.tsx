// @vitest-environment jsdom
// AiEvalCandidates.contract.test.tsx — v0.10.2111 (kalite döngüsü D6): aday
// vakalar bölümü. Önce düz cümle; bekleyen satırda tek "Kabul" düğmesi
// (doğru kimlikle POST), kabul edilmişte metin; boşken açıklayan mesaj;
// kabul hatası sunucunun metniyle görünür.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import '@/test/radixJsdom';
import { MemoryRouter } from 'react-router-dom';
import type { EvalsetCandidate } from '@/lib/types';

const api = vi.hoisted(() => ({
  aiEvalsetCandidates: vi.fn(async (..._a: unknown[]) => ({ candidates: [] as unknown[], ttlDays: 14 })),
  aiEvalsetAcceptCandidate: vi.fn(async (..._a: unknown[]) => ({}) as unknown),
}));
vi.mock('@/lib/api', () => ({ api }));

import { AiEvalCandidates } from './AiEvalCandidates';

const cand = (id: string, over: Partial<EvalsetCandidate> = {}): EvalsetCandidate => ({
  id, exchangeId: id.replace('evc-', ''), day: '2026-11-12', answeredAt: '2026-11-12T10:00:00Z',
  question: 'checkout neden yavaş?', answer: 'bilmiyorum', score: 0.12, signals: ['requery', 'no_evidence'],
  tier: 'loop', model: 'gemma', anyOfTools: [], mustNotContain: [], createdAt: '2026-11-13T02:10:00Z', accepted: false, ...over,
});

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  act(() => { root!.render(<MemoryRouter><QueryClientProvider client={qc}><AiEvalCandidates /></QueryClientProvider></MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  for (const f of Object.values(api)) f.mockReset();
});
const tick = async () => { for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 20)); }); };
const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll('button')).filter(b => b.textContent?.trim() === text) as HTMLButtonElement[];

describe('AiEvalCandidates', () => {
  it('boş: cümle + açıklayan boş mesaj', async () => {
    api.aiEvalsetCandidates.mockResolvedValue({ candidates: [], ttlDays: 14 });
    const el = render();
    await tick();
    expect(el.querySelector('[data-evs-cand-note]')?.textContent).toContain('kabul edilmeyen 14 gün sonra listeden düşer');
    expect(el.textContent).toContain('Henüz aday yok');
    expect(button(el, 'Kabul')).toHaveLength(0);
  });

  it('bekleyen satırda tek Kabul; kabul edilmişte metin; tık doğru kimliği POST eder', async () => {
    api.aiEvalsetCandidates.mockResolvedValue({ candidates: [cand('evc-a'), cand('evc-b', { accepted: true, acceptedBy: 'admin@example.com' })], ttlDays: 14 });
    api.aiEvalsetAcceptCandidate.mockResolvedValue({ candidate: cand('evc-a', { accepted: true }) });
    const el = render();
    await tick();
    expect(el.textContent).toContain('Şu an 1 aday bekliyor, 1 kabul edilmiş.');
    const btns = button(el, 'Kabul');
    expect(btns).toHaveLength(1);
    expect(el.textContent).toContain('Kabul edildi');
    await act(async () => { btns[0].click(); });
    await tick();
    expect(api.aiEvalsetAcceptCandidate).toHaveBeenCalledWith('evc-a');
    expect(api.aiEvalsetCandidates.mock.calls.length).toBeGreaterThanOrEqual(2); // liste tazelendi
  });

  it('kabul hatası sunucunun metniyle görünür', async () => {
    api.aiEvalsetCandidates.mockResolvedValue({ candidates: [cand('evc-a')], ttlDays: 14 });
    api.aiEvalsetAcceptCandidate.mockRejectedValue(new Error('aday bulunamadı ya da süresi doldu'));
    const el = render();
    await tick();
    await act(async () => { button(el, 'Kabul')[0].click(); });
    await tick();
    expect(el.textContent).toContain('süresi doldu');
  });
});
