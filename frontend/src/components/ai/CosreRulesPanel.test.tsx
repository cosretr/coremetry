// @vitest-environment jsdom
// CosreRulesPanel.test.tsx — v0.10.1289 (CoSRE kurallar / hafıza). Sözleşme:
// kayıt listesi (Kapsam · Kural · Yazan · Tarih), viewer GÖRÜR ama form /
// eylem yok; editör ekler / düzenler / siler ve her yazım GET'in rev'ini
// If-Match olarak taşır; 409'da liste yeniden okunur. Boş / süzgeç / hata
// hâlleri tablonun içinde.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { CosreRule, CosreRulesPayload } from '@/lib/types';

const NS = Date.UTC(2026, 9, 17, 10, 0) * 1e6;
const rule = (id: string, text: string, scope: CosreRule['scope'], at: number): CosreRule => ({ id, text, scope, author: 'Ayşe', authorEmail: 'a@x', createdAt: at });

const m = vi.hoisted(() => ({
  payload: null as CosreRulesPayload | null,
  gets: 0,
  creates: [] as unknown[][],
  updates: [] as unknown[][],
  deletes: [] as unknown[][],
  failNext: '' as string,
}));
vi.mock('@/lib/api', () => ({
  api: {
    cosreRules: vi.fn(async () => { m.gets++; return m.payload; }),
    createCosreRule: vi.fn(async (...a: unknown[]) => {
      if (m.failNext) { const e = m.failNext; m.failNext = ''; throw new Error(e); }
      m.creates.push(a); return { rule: {}, rev: 2, rules: [] };
    }),
    updateCosreRule: vi.fn(async (...a: unknown[]) => { m.updates.push(a); return { rule: {}, rev: 2, rules: [] }; }),
    deleteCosreRule: vi.fn(async (...a: unknown[]) => { m.deletes.push(a); return { rule: {}, rev: 2, rules: [] }; }),
    serviceNames: vi.fn(async () => []),
  },
}));

import { CosreRulesPanel } from './CosreRulesPanel';
import { ruleErrorText, filterRules, ruleScopeLabel } from './cosreRules';
import { ConfirmProvider } from '@/components/ui';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => root?.unmount()); host?.remove(); host = null; root = null;
  m.gets = 0; m.creates = []; m.updates = []; m.deletes = []; m.failNext = '';
  document.body.innerHTML = '';
  try { localStorage.clear(); } catch { /* jsdom */ }
});

const tick = async () => { for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 10)); }); };

async function render(canEdit: boolean) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><ConfirmProvider><CosreRulesPanel canEdit={canEdit} /></ConfirmProvider></MemoryRouter></QueryClientProvider>);
  });
  await tick();
  return host;
}

function setValue(el: HTMLTextAreaElement | HTMLSelectElement | HTMLInputElement, v: string) {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : el instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
  act(() => {
    Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(el, v);
    el.dispatchEvent(new Event(el instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }));
  });
}
const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll('button')).find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined;
const click = async (b: HTMLElement | undefined) => { if (!b) throw new Error('düğme yok'); await act(async () => { b.click(); }); await tick(); };

const two: CosreRulesPayload = {
  rev: 1, limits: { maxRules: 200, maxPerScope: 20, minText: 8, maxText: 200 },
  rules: [
    rule('a', "checkout batch'i 02:00'de koşar", { kind: 'service', value: 'checkout' }, NS),
    rule('b', "payments deploy'u Jenkins'ten", { kind: 'team', value: 'payments' }, NS + 1e9),
  ],
};

describe('CosreRulesPanel', () => {
  it('viewer listeyi görür (en yeni önce), form ve eylemler yok', async () => {
    m.payload = two;
    const el = await render(false);
    const rows = Array.from(el.querySelectorAll('tbody tr'));
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain('takım payments');
    expect(rows[1].textContent).toContain('servis checkout');
    expect(el.textContent).toContain('Ayşe');
    expect(el.querySelector('form')).toBeNull();
    expect(button(el, 'Sil')).toBeUndefined();
    expect(button(el, 'Düzenle')).toBeUndefined();
  });

  it('editör ekler: rev If-Match olarak gider, liste yenilenir', async () => {
    m.payload = two;
    const el = await render(true);
    const before = m.gets;
    setValue(el.querySelector('form textarea') as HTMLTextAreaElement, 'hafta sonu trafik yarıya düşer');
    await click(button(el, 'Ekle'));
    expect(m.creates).toEqual([[{ text: 'hafta sonu trafik yarıya düşer', scope: { kind: 'global' } }, 1]]);
    expect(m.gets).toBeGreaterThan(before);
  });

  it('takım kapsamı değer ister; düzenle formu doldurur; sil onaylı', async () => {
    m.payload = two;
    const el = await render(true);
    setValue(el.querySelector('form textarea') as HTMLTextAreaElement, 'sre ekibi nöbeti pazartesi devreder');
    setValue(el.querySelector('form select') as HTMLSelectElement, 'team');
    expect(button(el, 'Ekle')?.disabled).toBe(true);
    const inputs = Array.from(el.querySelectorAll('form input')) as HTMLInputElement[];
    setValue(inputs[inputs.length - 1], 'sre');
    expect(button(el, 'Ekle')?.disabled).toBe(false);

    await click(button(el.querySelectorAll('tbody tr')[1], 'Düzenle'));
    expect((el.querySelector('form textarea') as HTMLTextAreaElement).value).toBe("checkout batch'i 02:00'de koşar");
    setValue(el.querySelector('form textarea') as HTMLTextAreaElement, "checkout batch'i 02:00–02:20 koşar");
    await click(button(el, 'Güncelle'));
    expect(m.updates).toEqual([['a', { text: "checkout batch'i 02:00–02:20 koşar", scope: { kind: 'service', value: 'checkout' } }, 1]]);

    await click(button(el.querySelectorAll('tbody tr')[0], 'Sil'));
    expect(m.deletes).toHaveLength(0); // onay bekler
    await click(button(document.body, 'Kuralı sil'));
    expect(m.deletes).toEqual([['b', 1]]);
  });

  it('409: liste yeniden okunur', async () => {
    m.payload = two;
    const el = await render(true);
    const before = m.gets;
    m.failNext = 'HTTP 409: {"error":"kurallar sen düzenlerken değişti","rev":4}';
    setValue(el.querySelector('form textarea') as HTMLTextAreaElement, 'hafta sonu trafik yarıya düşer');
    await click(button(el, 'Ekle'));
    expect(m.creates).toHaveLength(0);
    expect(m.gets).toBeGreaterThan(before);
  });

  it('boş hâl tablonun içinde; süzgeç eşleşmesizi ayrı', async () => {
    m.payload = { ...two, rules: [] };
    const el = await render(false);
    expect(el.querySelector('thead')).not.toBeNull();
    expect(el.textContent).toContain('Henüz kural yok');
  });

  it('saf yardımcılar', () => {
    expect(ruleErrorText(new Error('HTTP 409: {"error":"değişti"}'))).toEqual({ conflict: true, text: 'değişti' });
    expect(ruleErrorText(new Error('HTTP 400: düz'))).toEqual({ conflict: false, text: 'düz' });
    expect(filterRules(two.rules, 'JENKINS').map(r => r.id)).toEqual(['b']);
    expect(filterRules(two.rules, 'servis checkout').map(r => r.id)).toEqual(['a']);
    expect(ruleScopeLabel({ kind: 'global' })).toBe('genel');
  });
});
