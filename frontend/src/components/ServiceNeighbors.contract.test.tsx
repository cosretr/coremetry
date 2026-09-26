// @vitest-environment jsdom
//
// ServiceNeighbors.contract.test.tsx — v0.10.927 (buton bütünlüğü, Faz 2
// artığı). "↻ Refresh" eskiden DisclosureButton'ın İÇİNDE role=button
// span'dı (button içinde button). Artık başlığın KARDEŞİ gerçek bir düğme:
// tık önbelleği atlayarak yeniden çeker, bölümü açıp kapamaz; kapalıyken
// ve yüklenirken yok.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';

const { serviceNeighbors } = vi.hoisted(() => ({
  serviceNeighbors: vi.fn(async (_svc: string, _since?: string, _n?: number, _refresh?: boolean) => ({
    upstream: [{ service: 'gw', traceCount: 3, spanCount: 5 }],
    downstream: [],
    sampledFrom: 3,
    totalSpans: 42,
  })),
}));
vi.mock('@/lib/api', () => ({ api: { serviceNeighbors } }));

import { ServiceNeighbors } from './ServiceNeighbors';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  serviceNeighbors.mockClear();
});

const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); });
const refreshBtn = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('Refresh')) ?? null;

describe('ServiceNeighbors — Refresh', () => {
  it('kapalıyken Refresh yok; başlık tek düğme', () => {
    const el = render(<ServiceNeighbors service="api" />);
    expect(refreshBtn(el)).toBeNull();
    expect(el.querySelector('[role="button"]')).toBeNull();
    expect(el.querySelector('button[aria-expanded="false"]')!.textContent).toContain('click to expand');
  });

  it('açık + veri: Refresh başlığın KARDEŞİ gerçek düğme; tık yeniden çeker, bölümü kapatmaz', async () => {
    const el = render(<ServiceNeighbors service="api" defaultOpen />);
    await flush();
    const header = el.querySelector('button[aria-expanded]') as HTMLButtonElement;
    expect(header.getAttribute('aria-expanded')).toBe('true');
    const btn = refreshBtn(el)!;
    expect(btn).not.toBeNull();
    expect(header.contains(btn)).toBe(false);
    expect(header.querySelector('button, [role="button"]')).toBeNull();
    expect(btn.type).toBe('button');
    expect(btn.title).toBe('Bypass the cached result and recompute now');
    expect(serviceNeighbors).toHaveBeenCalledTimes(1);
    expect(serviceNeighbors.mock.calls[0][3]).toBe(false);
    act(() => { btn.click(); });
    await flush();
    expect(serviceNeighbors).toHaveBeenCalledTimes(2);
    expect(serviceNeighbors.mock.calls[1][3]).toBe(true);
    expect((el.querySelector('button[aria-expanded]') as HTMLButtonElement).getAttribute('aria-expanded')).toBe('true');
  });
});
