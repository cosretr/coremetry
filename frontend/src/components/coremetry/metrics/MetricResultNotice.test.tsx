// @vitest-environment jsdom
//
// MetricResultNotice.test.tsx — v0.10.1836 (Metrics Explorer Faz 1 dilim 1.2).
//   • kırpılmış cevap → "12 seriden 8'i gösteriliyor" çizilir (role=status);
//   • tam cevap → hiçbir şey çizilmez (boş kutu yok);
//   • kısmi + not → iki satır; çok sorgulu editörde önek;
//   • bildirim pano paneline ve editöre gerçekten BAĞLI (ulaşılabilirlik
//     pini: bileşen var ama hiçbir yüzey çizmiyorsa test yeşil kalırdı).
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { MetricResultNotice } from './MetricResultNotice';
import type { MetricResultMeta } from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const meta: MetricResultMeta = { source: 'vm', tookMs: 40, seriesShown: 8, seriesTotal: 12, truncated: true, partial: false };

let host: HTMLDivElement;
let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });

describe('MetricResultNotice', () => {
  it('draws the truncation notice on a truncated response', () => {
    act(() => root.render(<MetricResultNotice meta={meta} />));
    const el = host.querySelector('[role="status"]');
    expect(el).not.toBeNull();
    expect(el!.textContent).toBe("12 seriden 8'i gösteriliyor");
  });

  it('renders nothing when the answer is complete', () => {
    act(() => root.render(<MetricResultNotice meta={{ ...meta, truncated: false, seriesTotal: 8 }} />));
    expect(host.innerHTML).toBe('');
    act(() => root.render(<MetricResultNotice meta={undefined} />));
    expect(host.innerHTML).toBe('');
  });

  it('shows partial + note, with the query prefix', () => {
    act(() => root.render(<MetricResultNotice meta={{ ...meta, truncated: false, partial: true }} note="Kova serisi bulunamadı" prefix="B" />));
    const lines = [...host.querySelectorAll('[role="status"] > div')].map(d => d.textContent);
    expect(lines).toHaveLength(2);
    expect(lines[0]).toMatch(/^B: Kısmi sonuç/);
    expect(lines[1]).toBe('B: Kova serisi bulunamadı');
  });

  it('is mounted by the dashboard panels and the metrics editor', () => {
    const strip = (s: string) => s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
    const panel = strip(readFileSync(resolve(__dirname, '../../dashboard/PanelRenderer.tsx'), 'utf8'));
    // metric panel (bundle + own fetch) and the PromQL panel
    expect(panel.match(/<MetricResultNotice meta=\{meta\.meta\} note=\{meta\.note\}/g)?.length).toBe(1);
    expect(panel.match(/<MetricResultNotice meta=\{promMeta\}/g)?.length).toBe(1);
    expect(panel).toMatch(/setMeta\(\{ meta: dataOverride!\.meta, note: dataOverride!\.note \}\)/);
    const mqe = strip(readFileSync(resolve(__dirname, '../../viz/MetricQueryEditor.tsx'), 'utf8'));
    expect(mqe).toMatch(/<MetricResultNotice meta=\{promqlRes\.data\?\.meta\}/);
    expect(mqe).toMatch(/<MetricResultNotice key=\{q\.id\} meta=\{results\[qi\]\?\.data\?\.meta\}/);
    expect(mqe).toMatch(/queryFn: \(\) => api\.metricQueryFull\(/);
  });
});
