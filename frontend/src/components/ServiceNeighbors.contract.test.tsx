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
import { MemoryRouter } from 'react-router';

const { serviceNeighbors } = vi.hoisted(() => ({
  // v0.10.1408 — yanıt topology_edges_5m şekli (örneklem alanları yok).
  serviceNeighbors: vi.fn(async (_svc: string, _since?: string, _refresh?: boolean) => ({
    service: 'api', from: 0, to: 0, source: 'topology_edges_5m',
    upstream: [{ id: 'gw', name: 'gw', kind: 'service' as const, calls: 5, errors: 0, errorRate: 0, avgMs: 2, p99Ms: 4 }],
    downstream: [
      { id: 'db:oracle@db.example.test', name: 'oracle@db.example.test', kind: 'database' as const, system: 'oracle', calls: 938, errors: 0, errorRate: 0, avgMs: 4, p99Ms: 9 },
      { id: 'ext:payments.example.test', name: 'payments.example.test', kind: 'external' as const, calls: 12, errors: 0, errorRate: 0, avgMs: 30, p99Ms: 80 },
    ],
    upstreamTotal: 1,
    downstreamTotal: 2,
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
    expect(serviceNeighbors.mock.calls[0][2]).toBe(false);
    act(() => { btn.click(); });
    await flush();
    expect(serviceNeighbors).toHaveBeenCalledTimes(2);
    expect(serviceNeighbors.mock.calls[1][2]).toBe(true);
    expect((el.querySelector('button[aria-expanded]') as HTMLButtonElement).getAttribute('aria-expanded')).toBe('true');
  });
});

// v0.10.1408 — operatör: "Downstream düzgün çalışmıyor". DB / dış HTTP
// bağımlılıkları (enstrümante callee'si olmayan CLIENT span'ları) downstream
// sütununda görünmeli; başlık örneklem iddia etmemeli.
describe('ServiceNeighbors — v0.10.1408 db/external downstream', () => {
  it('downstream db + external chips; header names the rollup, not a sample', async () => {
    const el = render(<ServiceNeighbors service="api" defaultOpen />);
    await flush();
    const text = el.textContent ?? '';
    expect(text).toContain('1 upstream · 2 downstream');
    expect(text).toContain('Downstream (2)');
    expect(text).toContain('oracle@db.example.test');
    expect(text).toContain('payments.example.test');
    expect(text).not.toContain('No downstream');
    expect(text).not.toMatch(/sampled from|spans inspected/);
    // db düğümü /database'e, servis /service'e gider; external link çizmez.
    const hrefs = Array.from(el.querySelectorAll('a')).map(a => a.getAttribute('href') ?? '');
    expect(hrefs.some(h => h.startsWith('/database'))).toBe(true);
    expect(hrefs.some(h => h.startsWith('/service?name=gw'))).toBe(true);
    expect(hrefs.some(h => h.includes('payments.example.test'))).toBe(false);
  });
});
