// @vitest-environment jsdom
//
// ColumnFilterShell.contract.test.tsx — v0.10.2084 (Traces shadcn geçişi, dilim
// S0; plan §3, S5 öncesi taban). Kabuk Traces, Problems › Exceptions
// (AnomaliesPage) ortak; S5 onu shadcn Popover'a taşıyacak. Bugünkü
// davranış burada çivilenir ki üç sayfa birden sessizce değişmesin:
//   • tetik IconButton: aria-label "Filter by <label>", aria-haspopup=dialog,
//     aria-expanded açık/kapalıyı izler, etkin süzgeçte `is-active`;
//   • gövde (children) YALNIZ açıkken çağrılır (kapalı başlık hiçbir şey
//     okumaz — ColumnFilter'ın "açılmadan fetch yok" sözünün kökü);
//   • tetik tıkı ve panel içi tık başlığa (sıralama) SIZMAZ;
//   • Temizle yalnız etkin süzgeçte; onClear + kapanış;
//   • gövdenin `close`u paneli kapatır.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { ColumnFilterShell } from './ColumnFilter';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = '';
});

function render(active: boolean, onClear = vi.fn(), onHeadClick = vi.fn(), body = vi.fn()) {
  act(() => {
    root.render(
      // Kabuk gerçekte <th> içinde: başlık tıkı = sıralama.
      <div data-th onClick={onHeadClick}>
        <ColumnFilterShell label="Service" active={active} summary="orders-api" onClear={onClear}>
          {(close) => { body(); return <button type="button" data-body-close onClick={close}>kapat</button>; }}
        </ColumnFilterShell>
      </div>,
    );
  });
  return { onClear, onHeadClick, body };
}
const trigger = () => host.querySelector('button.tcf-btn') as HTMLButtonElement;
const panel = () => document.body.querySelector('.tcf-panel') as HTMLElement | null;
const clearBtn = () => [...document.body.querySelectorAll('.tcf-head button')].find(b => b.textContent?.trim()) as HTMLButtonElement | undefined;

describe('ColumnFilterShell — paylaşılan kabuk sözleşmesi (S0)', () => {
  it('kapalı: gövde çağrılmaz; tetik aria-label / haspopup / expanded=false; etkin değilse is-active yok', () => {
    const { body } = render(false);
    expect(trigger().getAttribute('aria-label')).toMatch(/Service$/);
    expect(trigger().getAttribute('aria-haspopup')).toBe('dialog');
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
    expect(trigger().className).not.toContain('is-active');
    expect(body).not.toHaveBeenCalled();
  });

  it('tetik tıkı açar, başlığa sızmaz; gövde açıkken çizilir; gövdenin close\'u kapatır', () => {
    const { onHeadClick, body } = render(false);
    act(() => trigger().click());
    expect(onHeadClick).not.toHaveBeenCalled();
    expect(trigger().getAttribute('aria-expanded')).toBe('true');
    expect(body).toHaveBeenCalled();
    expect(panel()).not.toBeNull();
    act(() => (document.body.querySelector('[data-body-close]') as HTMLButtonElement).click());
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
  });

  it('panel içi tık başlığa sızmaz; etkin değilken Temizle yok', () => {
    const { onHeadClick } = render(false);
    act(() => trigger().click());
    act(() => panel()!.click());
    expect(onHeadClick).not.toHaveBeenCalled();
    expect(clearBtn()).toBeUndefined();
  });

  it('etkin: is-active; Temizle onClear çağırır ve kapatır', () => {
    const { onClear } = render(true);
    expect(trigger().className).toContain('is-active');
    act(() => trigger().click());
    const c = clearBtn();
    expect(c).toBeDefined();
    act(() => c!.click());
    expect(onClear).toHaveBeenCalledTimes(1);
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
  });
});
