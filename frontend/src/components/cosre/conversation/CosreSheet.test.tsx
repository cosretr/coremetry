// @vitest-environment jsdom
//
// CosreSheet.test — v0.10.1714 (operatör: "CoSRE v2 açılınca panel arka planı
// gri, kötü görünüyor"). shadcn SheetContent'in bg-background'u = --bg0 (SAYFA
// zemini) → panel sayfaya karışıyordu. v1 Drawer (var(--bg) = --bg1) paritesi:
// SheetContent bg-card taşır, bg-background taşımaz (twMerge ezer).
import { describe, it, expect, beforeAll, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { CosreSheet } from './CosreSheet';

let root: Root | null = null;
let host: HTMLDivElement | null = null;

beforeAll(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null;
  host = null;
});

describe('CosreSheet zemini (v0.10.1714)', () => {
  it('SheetContent bg-card taşır, sayfa zemini bg-background değil', () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => root!.render(<CosreSheet open onOpenChange={() => {}} width={480} />));
    const panel = document.querySelector<HTMLElement>('[data-cosre-v2]');
    expect(panel).not.toBeNull();
    const cls = panel!.className.split(/\s+/);
    expect(cls).toContain('bg-card');
    expect(cls).not.toContain('bg-background');
  });
});
