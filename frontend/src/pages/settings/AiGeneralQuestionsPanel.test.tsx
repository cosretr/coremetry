// @vitest-environment jsdom
// AiGeneralQuestionsPanel.test.tsx — v0.10.1458. Sözleşme: kutu sunucu değerini
// gösterir (varsayılan açık); admin tıklayınca PUT {allow} gönderir ve cevabı
// yansıtır; viewer salt-okunur (kutu kilitli, not görünür, PUT yok); okuma
// hatası görünür metinle.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import '@/test/radixJsdom';

const api = vi.hoisted(() => ({
  aiGeneralQuestions: vi.fn(async (..._a: unknown[]): Promise<unknown> => null),
  putAIGeneralQuestions: vi.fn(async (..._a: unknown[]): Promise<unknown> => null),
}));
vi.mock('@/lib/api', () => ({ api }));

import { AiGeneralQuestionsPanel } from './AiGeneralQuestionsPanel';

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(canEdit: boolean): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(<AiGeneralQuestionsPanel canEdit={canEdit} />); });
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
// R11: Radix Checkbox `<button role="checkbox" aria-checked>` (eskiden native input).
const box = (el: HTMLElement) => el.querySelector('[role="checkbox"]') as HTMLButtonElement;
const checked = (b: HTMLButtonElement) => b.getAttribute('aria-checked') === 'true';

describe('AiGeneralQuestionsPanel', () => {
  it('varsayılan açık görünür; admin kapatır (PUT allow=false) ve cevap yansır', async () => {
    api.aiGeneralQuestions.mockResolvedValueOnce({ allow: true });
    api.putAIGeneralQuestions.mockResolvedValueOnce({ allow: false, updatedBy: 'admin@example.test', updatedAt: '2026-10-23T12:00:00Z' });
    const el = render(true);
    await tick();
    expect(el.textContent).toMatch(/General questions|Genel sorular/);
    expect(el.textContent).toMatch(/Answer general questions|Genel sorulara cevap ver/);
    expect(checked(box(el))).toBe(true);
    expect(box(el).disabled).toBe(false);
    act(() => { box(el).click(); });
    await tick();
    expect(api.putAIGeneralQuestions).toHaveBeenCalledWith(false);
    expect(checked(box(el))).toBe(false);
    expect(el.textContent).toContain('admin@example.test');
  });

  it('viewer salt-okunur: kutu kilitli, not görünür, yazım yok', async () => {
    api.aiGeneralQuestions.mockResolvedValueOnce({ allow: false });
    const el = render(false);
    await tick();
    expect(checked(box(el))).toBe(false);
    expect(box(el).disabled).toBe(true);
    expect(el.textContent).toMatch(/Read-only|Salt-okunur/);
    act(() => { box(el).click(); });
    await tick();
    expect(api.putAIGeneralQuestions).not.toHaveBeenCalled();
  });

  it('okuma hatası görünür', async () => {
    api.aiGeneralQuestions.mockRejectedValueOnce(new Error('HTTP 500: down'));
    const el = render(true);
    await tick();
    expect(el.querySelector('[role="alert"]')?.textContent).toMatch(/down/);
    expect(box(el).disabled).toBe(true);
  });
});
