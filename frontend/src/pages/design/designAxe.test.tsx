// @vitest-environment jsdom
//
// designAxe — v0.10.1619 (shadcn Faz 1 R10, plan §8.2; §12 Q7). /design
// kataloğunun HER girdisi × hücre axe-core'dan geçer (color-contrast hariç,
// src/test/axe.ts). CI: tema başına bir hücre, varsayılan yoğunluk (3);
// tam 9'lu matris `DESIGN_AXE=all` (npm run design:axe:full; gece işi
// .github/workflows/design-axe-nightly.yml). Overlay'ler AÇIK çizilir
// (forceOpen) ve portal içeriği de taranır: axe document.body'yi koşar.
// Yeni bir shadcn dosyası katalog girdisi olmadan = kırmızı.
import { describe, it, expect, afterEach, beforeAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { MemoryRouter } from 'react-router-dom';
import { CATALOG, VARIANTS_COVERED } from './catalog';
import { axeCells, cellKey, CI_CELLS, ALL_CELLS } from './cells';
import { axeViolations } from '@/test/axe';

const SHADCN = resolve(__dirname, '../../components/shadcn');
const shadcnFiles = readdirSync(SHADCN).filter(f => f.endsWith('.tsx') && !f.includes('.test.')).map(f => f.replace(/\.tsx$/, ''));

let host: HTMLDivElement | null = null;
let root: Root | null = null;

beforeAll(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  // jsdom eksikleri (Radix popper/select bunları çağırır).
  const g = globalThis as unknown as Record<string, unknown>;
  g.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
  const proto = Element.prototype as unknown as Record<string, unknown>;
  proto.scrollIntoView ??= function () {};
  proto.hasPointerCapture ??= () => false;
  proto.releasePointerCapture ??= () => {};
});

afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

describe('/design kataloğu kapsamı', () => {
  it('components/shadcn/ altındaki her dosyanın katalog girdisi var', () => {
    expect(shadcnFiles.length).toBeGreaterThanOrEqual(19);
    expect(shadcnFiles.filter(f => !(f in CATALOG))).toEqual([]);
  });
  it('coremetry StatusBadge + Toaster girdileri var; varyant listeleri cva tipini kapsıyor', () => {
    expect(Object.keys(CATALOG)).toEqual(expect.arrayContaining(['coremetry/StatusBadge', 'coremetry/Toaster']));
    expect(VARIANTS_COVERED).toBe(true);
  });
  it('hücre kümeleri: CI 3 (tema başına, varsayılan yoğunluk), tam 9', () => {
    expect(CI_CELLS.map(cellKey)).toEqual(['dark/comfortable', 'light/comfortable', 'redhat/comfortable']);
    expect(ALL_CELLS).toHaveLength(9);
    expect(axeCells(undefined)).toBe(CI_CELLS);
    expect(axeCells('all')).toBe(ALL_CELLS);
  });
});

const CELLS = axeCells(process.env.DESIGN_AXE);

describe.each(CELLS.map(c => [cellKey(c), c] as const))('axe · %s', (_k, cell) => {
  it.each(Object.keys(CATALOG))('%s', async (name) => {
    host = document.createElement('div');
    host.setAttribute('data-theme', cell.theme);
    host.setAttribute('data-density', cell.density);
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => {
      root!.render(<MemoryRouter><main>{CATALOG[name]({ cell, forceOpen: true })}</main></MemoryRouter>);
    });
    if (name === 'context-menu') {
      const t = host.querySelector('[data-slot="context-menu-trigger"]')!;
      await act(async () => { t.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 10, clientY: 10 })); });
    }
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(await axeViolations(document.body)).toEqual([]);
  });
});
