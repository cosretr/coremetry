// @vitest-environment jsdom
//
// v0.10.2346 — MultiLineChart onExpandClick'i CorePanel'in (CorePanelMulti)
// mevcut onExpandClick'ine AYNEN geçirir; verilmezse geçirmez (grafik tık
// affordance'ı kazanmaz). İmleç / 250 ms çift-tık koruması / jest önceliği
// CorePanel'de (dokunulmadı).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';

const m = vi.hoisted(() => ({ props: [] as { onExpandClick?: () => void; onBucketClick?: unknown }[] }));
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: { onExpandClick?: () => void; onBucketClick?: unknown }) => { m.props.push(p); return <div data-core />; },
}));

import { MultiLineChart } from './MultiLineChart';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null;
let host: HTMLElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.props = []; });

async function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(node); });
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 10)); });
}

const series = [{ groupKey: ['a'], points: [{ time: 1e18, value: 1 }] }];

describe('MultiLineChart → CorePanel onExpandClick', () => {
  it('verilen eylem aynen geçer', async () => {
    const fn = vi.fn();
    await mount(<MultiLineChart series={series} onExpandClick={fn} />);
    const p = m.props.at(-1)!;
    expect(p.onExpandClick).toBe(fn);
    expect(p.onBucketClick).toBeUndefined();
  });
  it('verilmezse yok', async () => {
    await mount(<MultiLineChart series={series} />);
    expect(m.props.at(-1)!.onExpandClick).toBeUndefined();
  });
});
