// @vitest-environment jsdom
// ActionCard.test.tsx — v0.10.2353 (interaction-audit I5): onaylı aksiyon kartı.
// Pinler: alanlar sunucudan dolu ve düzenlenir; Onayla tek idemKey ile POST eder
// (yeniden onay aynı anahtar); sonuç onResult'a; needs_save kartı açık tutar;
// İptal yalnız iptal ucunu çağırır; sonuçlu kart düğmesiz.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { CosreActionCard } from '@/lib/types';

const m = vi.hoisted(() => ({
  confirm: vi.fn((_b: unknown) => Promise.resolve({ type: 'data-action-result', id: 'act-problem_note-result', data: { kind: 'problem_note', status: 'done', link: '/problems?problem=P-1' } } as { type: string; id?: string; data?: unknown })),
  cancel: vi.fn((_b: unknown) => Promise.resolve({ ok: true })),
}));
vi.mock('@/lib/api', () => ({ api: { confirmCosreAction: (b: unknown) => m.confirm(b), cancelCosreAction: (b: unknown) => m.cancel(b) } }));

import { ActionCard, actionCardText } from './ActionCard';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.confirm.mockClear(); m.cancel.mockClear(); });
const tick = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

const CARD: CosreActionCard = {
  kind: 'problem_note', token: 'tok', exchangeId: 'x-1', expiresAt: 2_000, label: 'Probleme not olarak ekle', problemId: 'P-1',
  fields: { title: 'CoSRE bulgusu — checkout', service: 'checkout', window: 'son 1 saat', summary: 'p95 870 ms.', evidence: ['s1 get_service_health'] },
};

function render(el: React.ReactElement): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(el));
  return host;
}
const btn = (el: HTMLElement, text: string) => [...el.querySelectorAll('button')].find(b => b.textContent === text)!;

describe('ActionCard', () => {
  it('dolu alanlar; düzenlenmiş başlıkla onay, aynı idemKey ile yeniden onay', async () => {
    const onResult = vi.fn();
    const el = render(<ActionCard card={CARD} onResult={onResult} now={() => 1_000} />);
    const title = el.querySelector<HTMLInputElement>('input')!;
    expect(title.value).toBe('CoSRE bulgusu — checkout');
    expect(el.textContent).toContain('s1 get_service_health');
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      set.call(title, 'checkout yavaş');
      title.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => { btn(el, 'Probleme not olarak ekle').click(); });
    await tick();
    await act(async () => { btn(el, 'Probleme not olarak ekle').click(); });
    await tick();
    const [a, b] = m.confirm.mock.calls.map(c => c[0] as { idemKey: string; fields: { title: string }; token: string });
    expect(a.fields.title).toBe('checkout yavaş');
    expect(a.token).toBe('tok');
    expect(a.idemKey).toBe(b.idemKey);
    expect(a.idemKey).toMatch(/^[A-Za-z0-9_-]{8,64}$/);
    expect(onResult).toHaveBeenCalledTimes(2);
  });
  it('needs_save: kart açık, uyarı görünür, sonuç eklenmez', async () => {
    m.confirm.mockResolvedValueOnce({ type: 'data-action-result', data: { kind: 'share_link', status: 'needs_save' } });
    const onResult = vi.fn();
    const el = render(<ActionCard card={{ ...CARD, kind: 'share_link', problemId: undefined, label: '' }} onResult={onResult} conversationId={() => null} now={() => 1_000} />);
    await act(async () => { btn(el, 'Onayla').click(); });
    await tick();
    expect(el.textContent).toContain('Önce konuşma kaydedilmeli');
    expect(onResult).not.toHaveBeenCalled();
    expect(btn(el, 'Onayla').disabled).toBe(false);
  });
  it('İptal: yalnız iptal ucu, kart kapanır', async () => {
    const el = render(<ActionCard card={CARD} onResult={vi.fn()} now={() => 1_000} />);
    await act(async () => { btn(el, 'İptal').click(); });
    expect(m.cancel).toHaveBeenCalledWith({ token: 'tok', kind: 'problem_note' });
    expect(m.confirm).not.toHaveBeenCalled();
    expect(el.querySelector('[data-testid="cosre-action-cancelled"]')).not.toBeNull();
  });
  it('sonuçlu kart düğmesiz, bağlantılı; süresi dolan kart kapalı', () => {
    const el = render(<ActionCard card={CARD} result={{ kind: 'problem_note', status: 'done', link: '/problems?problem=P-1' }} onResult={vi.fn()} now={() => 1_000} />);
    expect([...el.querySelectorAll('button')].some(b => b.textContent === 'Onayla' || b.textContent === 'İptal')).toBe(false);
    expect(el.querySelector('a')?.getAttribute('href')).toBe('/problems?problem=P-1');
    act(() => root!.render(<ActionCard card={CARD} onResult={vi.fn()} now={() => 5_000} />));
    expect(btn(el, 'Probleme not olarak ekle').disabled).toBe(true);
    expect(el.textContent).toContain('süresi doldu');
  });
  it('kopyalanacak metin alanları taşır', () => {
    expect(actionCardText(CARD.fields, '/x')).toBe('CoSRE bulgusu — checkout (servis checkout, pencere son 1 saat)\np95 870 ms.\nKanıt: s1 get_service_health\n/x');
  });
});
