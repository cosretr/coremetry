// @vitest-environment jsdom
//
// v0.10.1866 (Metrics Explorer Faz 2 dilim 2.6) — Explore'un metrik satırı
// ORTAK coremetry/metrics/QueryRow'u çizer (Spans/Metric anahtarı kalır);
// Explore'un ifade edemediği seçenekler kapalı (irate, count, toplama yok;
// rate/increase'te toplama sabit sum). Gizle düğmesi `enabled`'ı çevirir.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryRow } from './QueryRow';
import { blankQuery, type BuilderQuery } from './model';

vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, api: { ...(mod.api as Record<string, unknown>) } };
});

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
let root: Root | null = null;
let host: HTMLDivElement | null = null;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root?.unmount()); host?.remove(); });

const q: BuilderQuery = { ...blankQuery('A', 'metric'), metric: 'http_server_request_duration_seconds_count', agg: 'rate', scope: 'orders-api' };

describe('Explore metrik satırı → ortak QueryRow', () => {
  it('ortak satır çizilir; scope çipte; rate\'te toplama sabit; gizle enabled\'ı çevirir', () => {
    let last: BuilderQuery | null = null;
    act(() => root!.render(
      <MemoryRouter>
        <QueryRow q={q} canRemove canDuplicate onChange={n => { last = n; }} onDuplicate={() => {}} onRemove={() => {}} />
      </MemoryRouter>));
    const row = document.querySelector('[data-testid="query-row-A"]')!;
    expect(row).not.toBeNull();
    expect(row.textContent).toContain('service.name = orders-api');
    expect(document.querySelector('[aria-label^="Toplama:"]')).toBeNull();
    expect(row.textContent).toContain('topla sum');
    expect(document.querySelector('[aria-label="Veri kaynağı"], [aria-label="Sorgu kaynağı"]')).not.toBeNull();
    act(() => document.querySelector<HTMLElement>('[aria-label="A sorgusunu grafikte gizle"]')!.click());
    expect(last).toEqual({ ...q, enabled: false });
  });
});
