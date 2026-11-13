// @vitest-environment jsdom
//
// ColumnFilterShell.radix.test.tsx — Traces shadcn S5. Kabuk ui/Popover'dan
// coremetry/overlay Popover'a (Radix) geçti; eski panelin kapanma ve odak
// sözleri burada ölçülür (S0 kabuk testi açılış/tık/Temizle'yi çiviliyor):
//   • Esc paneli kapatır ve odak huni düğmesine döner;
//   • dışarı tık (pointerdown + click) paneli kapatır;
//   • panel body portalında, çapayla aynı ağaçta değil (sanal tablo kabının
//     `overflow` kırpmasına girmez). Telefon genişliğinde sığma (Radix
//     collisionPadding 8 + max-width calc(100vw - 16px)) jsdom'da ölçülemez.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, beforeEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { useShortcuts } from '@/lib/keyboard';
import { ColumnFilterShell } from './ColumnFilter';

// Uygulama kabuğu gibi keyboard.ts belge dinleyicisini kurar (Esc katman yığını oradan).
function KeyboardHost() { useShortcuts([]); return null; }

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  host = document.createElement('div');
  host.className = 'vt-scroll';
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => {
    root.render(
      <><KeyboardHost /><table><thead><tr><th>
        <ColumnFilterShell label="Service" active={false} summary="" onClear={() => {}}>
          {() => <input aria-label="ara" />}
        </ColumnFilterShell>
      </th></tr></thead></table></>,
    );
  });
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = '';
});

const trigger = () => host.querySelector('button.tcf-btn') as HTMLButtonElement;
const pop = () => document.body.querySelector('[data-tcf-pop]') as HTMLElement | null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });

describe('ColumnFilterShell — Radix yüzeyi (S5)', () => {
  it('panel body portalında (vt-scroll kabının dışında), role=dialog, aria-label', async () => {
    act(() => trigger().click());
    await settle();
    expect(pop()).not.toBeNull();
    expect(host.contains(pop())).toBe(false);
    expect(pop()!.getAttribute('role')).toBe('dialog');
    expect(pop()!.getAttribute('aria-label')).toMatch(/Service$/);
  });

  it('Esc kapatır; odak huni düğmesine döner', async () => {
    trigger().focus();
    act(() => trigger().click());
    await settle();
    expect(pop()).not.toBeNull();
    const target = (document.activeElement ?? document.body) as HTMLElement;
    act(() => { target.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })); });
    await settle();
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(trigger());
  });

  it('dışarı tık kapatır', async () => {
    act(() => trigger().click());
    await settle();
    expect(pop()).not.toBeNull();
    const out = document.createElement('div');
    document.body.appendChild(out);
    // Gerçek tık sırası: Radix sol tuşta kapatmayı pointerdown'dan sonraki click'e erteler.
    act(() => { out.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, cancelable: true, button: 0 })); });
    act(() => { out.click(); });
    await settle();
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
  });
});
