// @vitest-environment jsdom
//
// useDensity — v0.10.1615 (shadcn Faz 1 R7, plan §7.3, §12 Q9).
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { useDensity, useDensitySize, DENSITY_SIZE } from './useDensity';
import { DENSITY_STEPS } from './density';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.documentElement.removeAttribute('data-density');
});

function Probe() {
  return <span data-d={useDensity()} data-s={useDensitySize()} />;
}
const flush = () => act(async () => { await Promise.resolve(); });

describe('useDensity', () => {
  it('<html data-density> değişince değer güncellenir; bilinmeyen → comfortable', async () => {
    document.documentElement.setAttribute('data-density', 'compact');
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => { root!.render(<Probe />); });
    const span = () => host!.querySelector('span')!;
    expect(span().dataset.d).toBe('compact');
    expect(span().dataset.s).toBe('sm');
    document.documentElement.setAttribute('data-density', 'dense');
    await flush();
    expect(span().dataset.d).toBe('dense');
    expect(span().dataset.s).toBe('xs');
    document.documentElement.setAttribute('data-density', 'spacious');
    await flush();
    expect(span().dataset.d).toBe('comfortable');
    expect(span().dataset.s).toBe('md');
  });

  it('eşleme her yoğunluk basamağını kapsar (Q9: dense → xs)', () => {
    expect(Object.keys(DENSITY_SIZE).sort()).toEqual([...DENSITY_STEPS].sort());
    expect(DENSITY_SIZE).toEqual({ comfortable: 'md', compact: 'sm', dense: 'xs' });
  });
});
