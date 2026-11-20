// @vitest-environment jsdom
//
// MetricPanelCard — v0.10.1435 (Panelden "Bu grafiği açıkla"): SUNUM kartı.
// Meta şeridi (başlık, pencere, seçili aralık, seçili seri, kıyas), tembel
// grafik (motor yalnız kart çizilince), ölçüm satırları, anlatım yuvası,
// seçili seri top-20 dışı uyarısı, "Panoyu aç" / "Metrics'te aç" linki.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';

vi.mock('@/components/ai/EvidenceChips', () => ({
  EvidenceChart: (p: { p: { series: unknown[]; regions?: unknown[] } }) => <div data-testid="evchart">{p.p.series.length}/{p.p.regions?.length ?? 0}</div>,
}));

import type { MetricPanelBlockPayload } from '@/lib/types';
import { MetricPanelCard } from './MetricPanelCard';

const FROM = Date.UTC(2026, 9, 22, 14, 0) * 1e6;
const P: MetricPanelBlockPayload = {
  title: 'Explore · A · p95 duration', loc: 'x.A', src: 'span', fromNs: FROM, toNs: FROM + 3600e9,
  brushFromNs: FROM + 300e9, brushToNs: FROM + 1200e9, cmp: true, selected: ['GET /orders'], missing: ['POST /x'],
  chart: { title: 't', evidenceRef: '', window: { fromNs: FROM, toNs: FROM + 3600e9 },
    series: [{ name: 'GET /orders', unit: 'ms', points: [[1, 2]] }], regions: [{ fromMs: 1, toMs: 2, label: 'seçili' }] },
  stats: ['GET /orders [SEÇİLİ]: pencere min 10 ms'], href: '/metrics?editor=1',
};

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

describe('MetricPanelCard', () => {
  it('meta + grafik (brush bölgesi) + ölçüm + anlatım + eksik seçim + link', async () => {
    await act(async () => {
      root.render(<MemoryRouter><MetricPanelCard p={P}><p>ANLATIM</p></MetricPanelCard></MemoryRouter>);
    });
    await act(async () => { await new Promise(r => setTimeout(r, 5)); });
    const t = host.textContent ?? '';
    expect(t).toContain('Explore · A · p95 duration');
    expect(t).toContain('seri: GET /orders');
    expect(t).toContain('önceki pencereyle');
    expect(t).toMatch(/seçili \d\d:05:00 – \d\d:20:00/);
    expect(host.querySelector('[data-testid="evchart"]')?.textContent).toBe('1/1');
    expect(host.querySelector('[data-metric-panel-stats]')?.textContent).toContain('GET /orders [SEÇİLİ]');
    expect(t).toContain('ANLATIM');
    expect(t).toContain('POST /x');
    const a = host.querySelector('a');
    expect(a?.getAttribute('href')).toBe('/metrics?editor=1');
    expect(a?.textContent).toBe("Metrics'te aç →");
  });
});
