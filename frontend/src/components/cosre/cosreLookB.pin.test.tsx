// @vitest-environment jsdom
//
// cosreLookB.pin — v0.10.1730 (CoSRE v2 görünüm turu B, composer; operatör
// onayı: mockup "CoSRE sohbet shadcn"). Pinler: tek kart sınıfı + odak halkası
// kuralı (:focus-within, [data-cosre-v2] altında), Aa / Derin / Code tek
// ToggleGroup (segmented) ve davranış aynı (Derin aç/kapa aynı çağrı), v1
// (segmented yok) hapları değişmedi, v2 composer segmented verir.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, useState } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { MemoryRouter } from 'react-router-dom';
import { ChatComposer } from '@/components/ai/ChatComposer';

const SRC = resolve(__dirname, '..', '..');
const read = (p: string) => readFileSync(resolve(SRC, p), 'utf8');

function memStorage() {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => (m.has(k) ? m.get(k)! : null),
    setItem: (k: string, v: string) => { m.set(k, String(v)); },
    removeItem: (k: string) => { m.delete(k); },
    clear: () => m.clear(),
    key: (i: number) => [...m.keys()][i] ?? null,
    get length() { return m.size; },
  };
}

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  vi.stubGlobal('localStorage', memStorage());
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

function Harness({ segmented, onDeep }: { segmented?: boolean; onDeep: (on: boolean) => void }) {
  const [v, setV] = useState('');
  const [deep, setDeep] = useState(false);
  return (
    <MemoryRouter>
      <ChatComposer value={v} onChange={setV} onSubmit={() => {}} placeholder="sor" ariaLabel="CoSRE'ye mesaj"
        actions={<span data-actions="" />} deep={deep} onDeepChange={on => { setDeep(on); onDeep(on); }}
        code={false} onCodeChange={() => {}} segmented={segmented} />
    </MemoryRouter>
  );
}

describe('CoSRE v2 görünüm turu B (1730)', () => {
  it('segmented: tek kart sınıfı + Aa/Derin/Code tek ToggleGroup; Derin aynı çağrı', async () => {
    const onDeep = vi.fn();
    act(() => root.render(<Harness segmented onDeep={onDeep} />));
    expect(host.querySelector('.cm-composer__box')?.classList.contains('cm-composer__box--card')).toBe(true);
    const group = host.querySelector('.cm-mode-group[data-slot="toggle-group"]')!;
    expect(group).toBeTruthy();
    expect([...group.querySelectorAll('[data-slot="toggle-group-item"]')].map(b => b.textContent?.trim())).toEqual(['Aa', 'Derin', 'Code']);
    const deep = group.querySelector<HTMLButtonElement>('.cm-deep-pill:not(.cm-code-pill)')!;
    expect(deep.getAttribute('data-state')).toBe('off');
    await act(async () => { deep.click(); });
    expect(onDeep).toHaveBeenCalledWith(true);
    expect(deep.getAttribute('data-state')).toBe('on');
  });

  it('v1 (segmented yok): hap düzeni aynı, kart sınıfı yok', () => {
    act(() => root.render(<Harness onDeep={() => {}} />));
    expect(host.querySelector('.cm-mode-group')).toBeNull();
    expect(host.querySelector('.cm-composer__box--card')).toBeNull();
    expect(host.querySelector('button.cm-deep-pill[aria-pressed="false"]')).toBeTruthy();
  });

  it('CSS: odak halkası :focus-within, tek kart --radius-lg, her kural [data-cosre-v2] altında', () => {
    const css = read('styles/globals.css');
    const i = css.indexOf('v0.10.1730 — CoSRE v2 çekmecesi görünüm turu B');
    expect(i).toBeGreaterThan(-1);
    const block = css.slice(css.indexOf('*/', i) + 2);
    const body = block.replace(/\/\*[\s\S]*?\*\//g, '');
    expect(body).toMatch(/\.cm-composer__box--card:focus-within \{[^}]*border-color: var\(--ring\)[^}]*box-shadow/);
    expect(body).toMatch(/\.cm-composer__box--card \{[^}]*border: 1px solid var\(--border\)[^}]*border-radius: var\(--radius-lg\)/);
    for (const m of body.matchAll(/([^{}]+)\{/g)) for (const one of m[1].split(',')) expect(one.trim().startsWith('[data-cosre-v2]')).toBe(true);
    expect(body).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgba?\(/);
  });

  it('v2 composer segmented verir; bağlam çipleri composer altında (ChatContextBar)', () => {
    expect(read('components/cosre/conversation/CosreChatV2.tsx')).toMatch(/onRemoveCtx=\{explainCtx\.removeCtx\} submit=\{submit\} segmented/);
    const form = read('components/CopilotChat.tsx');
    const f = form.slice(form.indexOf('export function CosreComposerForm'));
    expect(f.indexOf('<ChatContextBar')).toBeGreaterThan(f.indexOf('<ChatComposer'));
  });
});
