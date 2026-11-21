// @vitest-environment jsdom
// brushCompare.test.tsx — dalga 3: fırça → karşılaştır, BubbleUp zaman kipi.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';

const { compare, same } = vi.hoisted(() => ({
  compare: vi.fn(async (_p: unknown) => ({ selectionTotal: 0, baselineTotal: 0, attributes: [] })),
  same: vi.fn(async () => null),
}));
vi.mock('@/lib/api', () => ({ api: { spanBubbleUpCompare: compare, spanBubbleUp: same } }));

import { BrushCompare, compareWindows } from './BrushCompare';

let host: HTMLDivElement;
let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); compare.mockClear(); same.mockClear(); });
afterEach(() => { act(() => root.unmount()); host.remove(); });

const H = 3600e9;

describe('compareWindows', () => {
  it('≤24 sa aynen; daha genişse taban seçimin sonuna hizalı 24 sa', () => {
    expect(compareWindows({ from: 2 * H, to: 3 * H }, { from: 0, to: 6 * H }).base).toEqual({ from: 0, to: 6 * H });
    const w = compareWindows({ from: 100 * H, to: 101 * H }, { from: 0, to: 168 * H });
    expect(w.base.to - w.base.from).toBe(24 * H);
    expect(w.base.from).toBeLessThanOrEqual(100 * H);
  });
});

describe('BrushCompare', () => {
  it('yakınlaştırma yokken çizilmez; varken Karşılaştır compare ucunu çağırır', async () => {
    const range = { preset: 'custom', fromMs: 2 * 3600e3, toMs: 3 * 3600e3 } as const;
    act(() => root.render(<MemoryRouter><BrushCompare range={range} zoomBase={null} baseline={[]} /></MemoryRouter>));
    expect(host.querySelector('[data-testid="brush-compare"]')).toBeNull();
    act(() => root.render(<MemoryRouter><BrushCompare range={range}
      zoomBase={{ preset: 'custom', fromMs: 3600e3, toMs: 6 * 3600e3 }}
      baseline={[{ k: 'service.name', op: '=', v: ['api'] }]} /></MemoryRouter>));
    await act(async () => { host.querySelector<HTMLElement>('[data-testid="brush-compare-toggle"]')!.click(); });
    expect(compare).toHaveBeenCalledTimes(1);
    expect(same).not.toHaveBeenCalled();
    expect(compare.mock.calls[0]).toMatchObject([{ selFrom: 2 * H, selTo: 3 * H, baseFrom: H, baseTo: 6 * H }]);
  });
});
