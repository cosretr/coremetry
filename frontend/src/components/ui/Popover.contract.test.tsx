// @vitest-environment jsdom
//
// Popover.contract.test.tsx — v0.10.968 (Trace › Metrics yeniden tasarımı).
// ui/Popover sözleşmesi: rol + ad, Esc katmanı (tek Esc kanalı), dışarı
// pointerdown, açılışta ilk öğeye odak ve kapanışta çapaya dönüş, menüde
// ↑ ↓ Home End gezinme, Tab'ın menüyü kapatması.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act, useRef, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { __resetEscLayers, escLayerDepth, topEscLayer } from '@/lib/escLayer';
import { Popover } from './Popover';
import { MenuItem } from './Menu';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function Harness({ kind, onClose }: { kind: 'menu' | 'dialog'; onClose?: () => void }) {
  const ref = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  return (
    <div>
      <button ref={ref} data-testid="anchor" onClick={() => setOpen(o => !o)}>aç</button>
      <button data-testid="outside">dış</button>
      <Popover anchorRef={ref} open={open} kind={kind} ariaLabel="Satır eylemleri"
        onClose={() => { onClose?.(); setOpen(false); }}>
        {kind === 'menu' ? (
          <>
            <MenuItem>Bir</MenuItem>
            <MenuItem aria-disabled>İki</MenuItem>
            <MenuItem>Üç</MenuItem>
          </>
        ) : (
          <>
            <span>Metrik kapsamı</span>
            <a href="#kapsam">Ayarlar</a>
          </>
        )}
      </Popover>
    </div>
  );
}

function mount(kind: 'menu' | 'dialog', onClose?: () => void): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(<Harness kind={kind} onClose={onClose} />);
  });
  return host!;
}
const anchor = (el: HTMLElement) => el.querySelector<HTMLButtonElement>('[data-testid="anchor"]')!;
const openIt = (el: HTMLElement) => act(() => { anchor(el).focus(); anchor(el).click(); });
const key = (target: Element, k: string) => act(() => {
  target.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
});

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  __resetEscLayers();
});

describe('Popover — sözleşme', () => {
  it('kapalıyken hiçbir şey basmaz ve Esc katmanı almaz', () => {
    const el = mount('menu');
    expect(el.querySelector('.ui-popover')).toBeNull();
    expect(escLayerDepth()).toBe(0);
  });

  it('menu: role=menu + aria-label; ilk ETKİN öğe odak alır', () => {
    const el = mount('menu');
    openIt(el);
    const pop = el.querySelector('.ui-popover')!;
    expect(pop.getAttribute('role')).toBe('menu');
    expect(pop.getAttribute('aria-label')).toBe('Satır eylemleri');
    expect(document.activeElement?.textContent).toBe('Bir');
    expect(pop.getAttribute('style')).toContain('width: 240px');
  });

  it('dialog: role=dialog; ilk odaklanabilir öğe odak alır', () => {
    const el = mount('dialog');
    openIt(el);
    expect(el.querySelector('.ui-popover')!.getAttribute('role')).toBe('dialog');
    expect(document.activeElement?.tagName).toBe('A');
  });

  it('↑ ↓ Home End menuitem\'lar arasında (devre dışı da odak alır, döngüsel)', () => {
    const el = mount('menu');
    openIt(el);
    const pop = el.querySelector('.ui-popover')!;
    const at = () => document.activeElement?.textContent;
    key(pop, 'ArrowDown'); expect(at()).toBe('İki');
    key(pop, 'ArrowDown'); expect(at()).toBe('Üç');
    key(pop, 'ArrowDown'); expect(at()).toBe('Bir');
    key(pop, 'ArrowUp'); expect(at()).toBe('Üç');
    key(pop, 'Home'); expect(at()).toBe('Bir');
    key(pop, 'End'); expect(at()).toBe('Üç');
  });

  it('Esc katmanı kapatır ve odak çapaya döner', () => {
    const onClose = vi.fn();
    const el = mount('menu', onClose);
    openIt(el);
    expect(escLayerDepth()).toBe(1);
    act(() => { topEscLayer()!(); });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(el.querySelector('.ui-popover')).toBeNull();
    expect(document.activeElement).toBe(anchor(el));
    expect(escLayerDepth()).toBe(0);
  });

  it('dışarı pointerdown kapatır; içeri ve çapaya pointerdown kapatmaz', () => {
    const onClose = vi.fn();
    const el = mount('menu', onClose);
    openIt(el);
    act(() => { el.querySelector('.ui-popover')!.dispatchEvent(new Event('pointerdown', { bubbles: true })); });
    act(() => { anchor(el).dispatchEvent(new Event('pointerdown', { bubbles: true })); });
    expect(onClose).not.toHaveBeenCalled();
    act(() => { el.querySelector('[data-testid="outside"]')!.dispatchEvent(new Event('pointerdown', { bubbles: true })); });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(el.querySelector('.ui-popover')).toBeNull();
  });

  it('öğe seçilip kapanınca odak çapaya döner (menü içindeyken)', () => {
    const el = mount('menu');
    openIt(el);
    expect(el.querySelector('.ui-popover')!.contains(document.activeElement)).toBe(true);
    act(() => { anchor(el).click(); }); // çağıran kapatır (toggle)
    expect(el.querySelector('.ui-popover')).toBeNull();
    expect(document.activeElement).toBe(anchor(el));
  });

  it('menüde Tab kapatır ve odağı çapaya verir', () => {
    const el = mount('menu');
    openIt(el);
    key(el.querySelector('.ui-popover')!, 'Tab');
    expect(el.querySelector('.ui-popover')).toBeNull();
    expect(document.activeElement).toBe(anchor(el));
  });

  it('dosyada Escape karşılaştırması yok (tek Esc kanalı escLayer)', async () => {
    const { readFileSync } = await import('node:fs');
    const { resolve } = await import('node:path');
    const src = readFileSync(resolve(__dirname, 'Popover.tsx'), 'utf8').replace(/\/\/.*$/gm, '');
    expect(src).not.toMatch(/key === 'Escape'|key !== 'Escape'/);
    expect(src).toContain('useEscLayer(');
  });
});
