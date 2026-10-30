// @vitest-environment jsdom
// v0.10.1570 (operatör: "CoSRE çekmecesi genişletilebilir sola doğru olsun") —
// Drawer resizeKey: sol kenar tutamağı sola sürüklenince genişler, sınırlar
// kelepçeli, genişlik saklanır, çift tık varsayılana döner; resizeKey
// vermeyen çekmecede tutamak yok.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { Drawer } from './Drawer';
import { clampDrawerWidth, DRAWER_MIN_WIDTH } from '@/lib/drawerWidth';
import { getRaw, setRaw } from '@/lib/storage';

describe('clampDrawerWidth', () => {
  it.each([
    [200, 1600, DRAWER_MIN_WIDTH],
    [900, 1600, 900],
    [1500, 1600, 1440],
    [900, 400, DRAWER_MIN_WIDTH], // dar ekranda taban kazanır
  ])('%d @ %d → %d', (w, vp, want) => expect(clampDrawerWidth(w, vp)).toBe(want));
});

let host: HTMLDivElement; let root: Root;
const KEY = 'test-drawer-w';
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => { mem.set(k, v); },
    removeItem: (k: string) => { mem.delete(k); }, clear: () => mem.clear(), key: () => null, length: 0,
  });
  setRaw(KEY, '');
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  Object.defineProperty(window, 'innerWidth', { value: 1600, configurable: true });
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.unstubAllGlobals(); });

const panel = () => document.querySelector('.drawer-panel') as HTMLElement;
const handle = () => document.querySelector('.drawer-resizer') as HTMLElement | null;
const pe = (type: string, x: number) => {
  const e = new MouseEvent(type, { bubbles: true, clientX: x });
  return e;
};

describe('Drawer sol kenar tutamağı', () => {
  it('resizeKey yoksa tutamak yok, genişlik sabit', () => {
    act(() => { root.render(<Drawer onClose={() => {}} header="x" width={620} backdrop={false}>i</Drawer>); });
    expect(handle()).toBeNull();
    expect(panel().dataset.width).toBe('620');
  });

  it('sola sürükleme genişletir, saklanır; çift tık sıfırlar', () => {
    act(() => { root.render(<Drawer onClose={() => {}} header="x" width={620} backdrop={false} resizeKey={KEY}>i</Drawer>); });
    const h = handle()!;
    expect(h).not.toBeNull();
    act(() => { h.dispatchEvent(pe('pointerdown', 1000)); });
    act(() => { window.dispatchEvent(pe('pointermove', 700)); });
    act(() => { window.dispatchEvent(pe('pointerup', 700)); });
    expect(panel().dataset.width).toBe('920');
    expect(getRaw(KEY)).toBe('920');
    act(() => { h.dispatchEvent(new MouseEvent('dblclick', { bubbles: true })); });
    expect(panel().dataset.width).toBe('620');
  });
});
