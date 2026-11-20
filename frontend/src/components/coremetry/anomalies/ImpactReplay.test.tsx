// @vitest-environment jsdom
//
// v0.10.2392 — etki kuralı geri oynatma bölümü (ImpactReplay.tsx): düğmeden
// önce istek yok; özet (Problem / P1 / P2 / servis / medyan süre), notlar,
// epizot tablosu ve servis bağlantısı; boş durum. Saat dilimi bağımsız:
// zaman hücresi değil, süre / değer / bağlantı pinlenir.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { ImpactReplayResult } from '@/lib/types';

const m = vi.hoisted(() => ({ data: null as unknown, reqs: [] as unknown[] }));
vi.mock('@/lib/queries', () => ({
  useImpactReplay: (req: unknown) => { m.reqs.push(req); return { data: req ? m.data : undefined, isLoading: false, isFetching: false, error: null }; },
}));

import ImpactReplaySection, { replaySummary, replayValue } from './ImpactReplay';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function fixture(o: Partial<ImpactReplayResult> = {}): ImpactReplayResult {
  return {
    detector: 'failure_rate', days: 7, from: 1_000_000, to: 1_604_800, bucketSec: 300, candidates: 3,
    totals: { problems: 2, p1: 1, p2: 1, p3: 0, services: 2, medianSec: 1740 },
    episodes: [
      { service: 'svc-a', measure: 'failure_rate', start: 1_500_000, end: 1_502_040, durationSec: 2040, peak: 0.05, normal: 0.002, threshold: 0.012, priority: 'P1', priorityReason: 'critical + 10.0x threshold', open: false },
      { service: 'svc-b', measure: 'failure_rate', start: 1_400_000, end: 1_401_440, durationSec: 1440, peak: 0.035, normal: 0.02, threshold: 0.03, priority: 'P2', priorityReason: 'critical', open: true },
    ],
    skipped: [{ service: 'svc-c', reason: 'eksik veri: taban örneği az (en az 20 gerekir)' }], skippedTotal: 1,
    truncated: false, episodesTruncated: false, partial: false,
    notes: ['Yaklaşık: kuralın 1 dakikalık örnekleri saklanmıyor.'],
    ...o,
  };
}

let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.data = null; m.reqs = []; });

function render(): HTMLDivElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><ImpactReplaySection /></MemoryRouter>); });
  return host;
}
const run = (el: HTMLElement) => {
  const btn = [...el.querySelectorAll('button')].find(b => b.textContent === 'Oynat')!;
  act(() => { btn.click(); });
};

describe('ImpactReplaySection (v0.10.2392)', () => {
  it('düğmeden önce istek yok; düğme seçili gün + dedektörle ister', () => {
    m.data = fixture();
    const el = render();
    expect(m.reqs.every(r => r === null)).toBe(true);
    expect(el.querySelector('[data-testid="impact-replay-summary"]')).toBeNull();
    expect(el.textContent).toContain('Yaklaşık');
    run(el);
    expect(m.reqs.at(-1)).toEqual({ days: 7, detector: 'failure_rate' });
  });

  it('özet, notlar, atlanan, tablo ve servis bağlantısı', () => {
    m.data = fixture();
    const el = render();
    run(el);
    expect(el.querySelector('[data-testid="impact-replay-summary"]')!.textContent)
      .toBe('Son 7 günde 2 Problem açardı (P1 1, P2 1) · 2 servis · medyan süre 29m · 3 aday oynatıldı.');
    expect(el.querySelector('[data-testid="impact-replay-notes"]')!.textContent).toContain('Yaklaşık');
    expect(el.querySelector('[data-testid="impact-replay-skipped"]')!.textContent).toContain('svc-c');
    const rows = el.querySelectorAll('tbody tr');
    expect(rows.length).toBe(2);
    const text = el.querySelector('tbody')!.textContent!;
    expect(text).toContain('%5 / %0,2');
    expect(text).toContain('(sürüyor)');
    const href = el.querySelector('tbody a')!.getAttribute('href')!;
    expect(href.startsWith('/service?name=svc-')).toBe(true);
    expect(href).toContain('range=');
  });

  it('boş durum: Problem yok cümlesi ve tablo içi boş mesaj', () => {
    m.data = fixture({ episodes: [], totals: { problems: 0, p1: 0, p2: 0, p3: 0, services: 0, medianSec: 0 }, skipped: [], skippedTotal: 0 });
    const el = render();
    run(el);
    expect(el.querySelector('[data-testid="impact-replay-summary"]')!.textContent).toBe('Son 7 günde bu kural Problem açmazdı (3 aday servis oynatıldı).');
    expect(el.textContent).toContain('Bu pencerede kural hiç ateşlemezdi.');
    expect(el.querySelector('[data-testid="impact-replay-skipped"]')).toBeNull();
  });

  it('değer biçimi ölçü biriminde', () => {
    expect(replayValue('failure_rate', 0.0125)).toBe('%1,25');
    expect(replayValue('response_slow', 1234.4)).toBe('1.234 ms');
    expect(replayValue('consumer_lag', 165000)).toBe('165.000');
    expect(replaySummary(fixture({ totals: { problems: 3, p1: 1, p2: 1, p3: 1, services: 1, medianSec: 600 } }))).toContain('P3 1');
  });
});
